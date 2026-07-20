package httpserver

import (
	"fmt"
	"net/http"
	"time"
)

// heartbeatInterval keeps idle connections alive through proxies that would
// otherwise close a silent stream. It is a var so tests can shorten it.
var heartbeatInterval = 15 * time.Second

// handleEvents streams gold prices to one browser over SSE. It shows every
// core piece of the protocol in one place:
//
//   - the text/event-stream content type and no-cache headers;
//   - named events ("event: price") and event ids ("id: N");
//   - keep-alive comment lines (": ...") on an idle connection;
//   - flushing after every write so the browser sees each event immediately;
//   - honouring client disconnect via the request context.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// ResponseController is the modern (Go 1.20+) way to flush a streaming
	// response without type-asserting http.Flusher.
	stream := http.NewResponseController(w)

	// One id per connection so a single browser's frames can be followed in the
	// logs even when several clients are connected at once.
	connID := s.conns.Add(1)
	log := s.log.With("conn", connID, "remote", r.RemoteAddr)
	log.Info("client connected")

	// eventID restarts at 1 per connection and we don't read the client's
	// Last-Event-ID header: gap-free resumption is intentionally out of scope.
	// This stream only ever shows the latest price, and the broker replays the
	// current price on subscribe, so a reconnecting client loses nothing.
	var eventID int

	// reason is set on each exit path and reported once by this deferred line,
	// so every disconnect is accounted for (clean close vs shutdown vs write error).
	reason := "unknown"
	defer func() { log.Info("client disconnected", "reason", reason, "frames", eventID) }()

	// Tell the browser how long to wait before reconnecting after a drop. Sent
	// once, up front, and flushed immediately so a client that reconnects and
	// drops again still learns the delay. EventSource remembers it for the life
	// of the stream.
	fmt.Fprintf(w, "retry: %d\n\n", s.reconnectDelay.Milliseconds())
	if stream.Flush() != nil {
		reason = "write error"
		return
	}
	log.Debug("sent retry hint", "ms", s.reconnectDelay.Milliseconds())

	prices := s.broker.Subscribe()
	defer s.broker.Unsubscribe(prices)
	log.Debug("subscribed to broker")

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done(): // browser closed the tab or navigated away
			reason = "context canceled"
			return

		case price, ok := <-prices:
			if !ok { // broker is shutting down
				reason = "broker shutdown"
				return
			}
			eventID++
			fmt.Fprintf(w, "id: %d\nevent: price\ndata: {\"usd\":%.2f,\"at\":%q}\n\n",
				eventID, price.USDPerOunce, price.At.Format(time.RFC3339))
			if stream.Flush() != nil {
				reason = "write error"
				return
			}
			log.Debug("sent price frame", "id", eventID, "usd", price.USDPerOunce)

		case <-heartbeat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			if stream.Flush() != nil {
				reason = "write error"
				return
			}
			log.Debug("sent keep-alive")
		}
	}
}
