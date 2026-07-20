package httpserver

import (
	"bufio"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"goldstream/internal/broker"
	"goldstream/internal/gold"
)

func TestSSEStreamsPriceFrame(t *testing.T) {
	// Shorten the heartbeat so the handler notices a closed connection quickly
	// at teardown instead of waiting on the default 15s tick.
	restore := heartbeatInterval
	heartbeatInterval = 50 * time.Millisecond
	defer func() { heartbeatInterval = restore }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := broker.New(slog.New(slog.DiscardHandler))
	go b.Run(ctx)

	srv := httptest.NewServer(New(b, 2*time.Second, slog.New(slog.DiscardHandler)))
	defer srv.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q want text/event-stream", ct)
	}

	// The stream opens with a retry: hint (in ms) telling the browser how long
	// to wait before reconnecting.
	if retry := readEventFrame(t, resp.Body, "retry:"); !strings.Contains(retry, "retry: 2000") {
		t.Fatalf("expected retry: 2000 hint, got %q", retry)
	}

	time.Sleep(20 * time.Millisecond) // ensure the handler has subscribed
	b.Publish(gold.Price{USDPerOunce: 2405.5, At: time.Unix(1700000000, 0)})

	frame := readEventFrame(t, resp.Body, "event: price")
	if !strings.Contains(frame, "2405.5") {
		t.Fatalf("frame missing price: %q", frame)
	}
	if !strings.Contains(frame, "id: 1") {
		t.Fatalf("frame missing event id: %q", frame)
	}

	// Cancelling the request closes the connection, which the handler observes
	// via r.Context() and returns — so teardown doesn't wait on the heartbeat.
	cancel()
}

// readEventFrame reads SSE lines until it finds a complete frame (terminated
// by a blank line) containing marker, then returns that frame.
func readEventFrame(t *testing.T, body interface{ Read([]byte) (int, error) }, marker string) string {
	t.Helper()
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(body)
		var frame strings.Builder
		for sc.Scan() {
			line := sc.Text()
			if line == "" { // blank line ends one SSE event
				if strings.Contains(frame.String(), marker) {
					found <- frame.String()
					return
				}
				frame.Reset()
				continue
			}
			frame.WriteString(line + "\n")
		}
	}()

	select {
	case frame := <-found:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatalf("did not receive a frame containing %q", marker)
		return ""
	}
}
