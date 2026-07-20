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

	prices := s.broker.Subscribe()
	defer s.broker.Unsubscribe(prices)

	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	ctx := r.Context()
	// eventID restarts at 1 per connection and we don't read the client's
	// Last-Event-ID header: gap-free resumption is intentionally out of scope.
	// This stream only ever shows the latest price, and the broker replays the
	// current price on subscribe, so a reconnecting client loses nothing.
	var eventID int
	for {
		select {
		case <-ctx.Done(): // browser closed the tab or navigated away
			return

		case price, ok := <-prices:
			if !ok { // broker is shutting down
				return
			}
			eventID++
			fmt.Fprintf(w, "id: %d\nevent: price\ndata: {\"usd\":%.2f,\"at\":%q}\n\n",
				eventID, price.USDPerOunce, price.At.Format(time.RFC3339))
			if stream.Flush() != nil {
				return
			}

		case <-heartbeat.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			if stream.Flush() != nil {
				return
			}
		}
	}
}
