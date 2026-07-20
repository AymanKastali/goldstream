// Package httpserver exposes the SSE endpoint and the embedded browser UI.
package httpserver

import (
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"goldstream/internal/broker"
	"goldstream/web"
)

// server holds the dependencies the HTTP handlers need.
type server struct {
	broker         *broker.Broker
	reconnectDelay time.Duration // advertised to clients via the SSE "retry:" field
	log            *slog.Logger
	conns          atomic.Int64 // hands each connection a stable id for log tracing
}

// New wires the routes and returns the handler. The broker must already be
// running; each connection to /events subscribes to it. reconnectDelay is how
// long a browser should wait before reconnecting after a drop.
func New(b *broker.Broker, reconnectDelay time.Duration, log *slog.Logger) http.Handler {
	s := &server{broker: b, reconnectDelay: reconnectDelay, log: log.With("component", "http")}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.Handle("GET /", http.FileServer(http.FS(web.Files))) // serves index.html at "/"
	return mux
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}
