// Package httpserver exposes the SSE endpoint and the embedded browser UI.
package httpserver

import (
	"net/http"

	"goldstream/internal/broker"
	"goldstream/web"
)

// server holds the dependencies the HTTP handlers need.
type server struct {
	broker *broker.Broker
}

// New wires the routes and returns the handler. The broker must already be
// running; each connection to /events subscribes to it.
func New(b *broker.Broker) http.Handler {
	s := &server{broker: b}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.Handle("GET /", http.FileServer(http.FS(web.Files))) // serves index.html at "/"
	return mux
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}
