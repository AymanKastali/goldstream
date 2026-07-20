// Command goldstream serves live gold prices to browsers over Server-Sent
// Events. It polls goldapi.io on a timer and fans each update out to every
// connected client through an in-process broker.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"goldstream/internal/broker"
	"goldstream/internal/config"
	"goldstream/internal/goldapi"
	"goldstream/internal/httpserver"
	"goldstream/internal/poller"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1) // fail fast: no key, no service
	}

	// One context cancels the broker, poller, and server together on Ctrl-C
	// or SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	prices := broker.New()
	go prices.Run(ctx)

	client := goldapi.NewDefault(cfg.GoldAPIKey, &http.Client{Timeout: cfg.HTTPTimeout})
	feed := poller.New(client, prices.Publish, cfg.PollInterval, cfg.FetchTimeout, log)
	go feed.Run(ctx)

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: httpserver.New(prices)}
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			stop() // unblock shutdown if the listener fails
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
}
