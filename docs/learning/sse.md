# Server-Sent Events (SSE)

## TL;DR + Mental Model

Server-Sent Events (SSE) is a way for a server to push a continuous stream of updates to a browser
over a single, plain HTTP connection — no special protocol upgrade, no client-side polling loop.

**Mental model:** an SSE connection is a phone call the browser leaves open. The server keeps talking
whenever it has something new to say; the browser just listens and reacts to each thing it hears. If
the call drops, the browser automatically redials on its own.

Concretely:

- The client opens a normal `GET` request and the server responds with `Content-Type:
  text/event-stream`, but instead of closing the response, it keeps the connection open and writes
  small text "event" frames to it over time.
- The browser's built-in `EventSource` object reads that stream, splits it into events, and fires a
  JS callback per event — and reconnects by itself if the connection drops.
- It only flows one way: server → client. If the client needs to send data back, it does that over a
  separate, ordinary request.

This project (goldstream) uses exactly this: `internal/httpserver/sse.go` streams price ticks to the
browser over `GET /events`, and `web/index.html` consumes it with a plain `new EventSource("/events")`.

## Why SSE Exists

Before SSE, "the server has new data, show it to the user" was solved in ways that were all somewhat
awkward:

- **Polling** — the client asks "anything new?" every N seconds. Simple, but wasteful (most requests
  return nothing) and never faster than your poll interval.
- **Long polling** — the client asks, and the server holds the request open until it has something,
  then the client immediately asks again. Better latency, but every framework and proxy has to be
  taught to handle indefinitely-hanging requests, and you're still reopening a connection per event.
- **WebSockets** — a full duplex, message-based protocol over a single connection. It solves the
  push problem completely, but it's a different protocol (its own handshake, framing, and often its
  own client library), and it's overkill if the server never needs to *receive* a stream back.

SSE fills the specific gap: **the server needs to push a stream of updates, the client never needs to
push a stream back, and you'd rather not adopt a new protocol to get it.** It's just HTTP, kept open,
carrying a simple text format the browser already knows how to parse.

## How It Works

**1. The client asks for a stream.** A normal `GET` request, nothing special about it.

**2. The server answers with a stream, not a document.** It sends these response headers and then
never sends a final byte count:

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

`text/event-stream` is what tells the browser "don't treat this as a downloaded file — parse it live,
event by event, as bytes arrive."

**3. The server writes events as plain text, one field per line.** Each event is a small block of
`field: value` lines, terminated by a blank line:

```
id: 42
event: price
data: {"symbol": "XAU", "price": 2374.10}

```

- `data:` — the payload. If you need structured data, put JSON here as a plain string; SSE itself
  doesn't parse it, your JS does (`JSON.parse(event.data)`).
- `event:` — an optional name. The client only reacts to it if it registered a listener for that
  name (`addEventListener("price", ...)`). Omit it and the browser fires the generic `onmessage`
  instead.
- `id:` — an optional event ID. The browser remembers the last one it saw.
- A line starting with `:` is a comment — the browser ignores it entirely. Servers send these purely
  as heartbeats, to keep idle connections from being killed by a proxy or load balancer that closes
  connections it hasn't seen traffic on.
- The blank line is what marks "this event is complete, deliver it now."

**4. The browser's `EventSource` does the parsing and the reconnecting.** In JS:

```js
const es = new EventSource("/events");
es.addEventListener("price", (e) => {
  const tick = JSON.parse(e.data);
  console.log(tick);
});
```

You never touch raw bytes — `EventSource` buffers the stream, splits it into events on blank lines,
and calls the right listener per `event:` name.

**5. Reconnection is automatic and built into the browser, not your code.** If the connection drops
(network blip, server restart, proxy timeout), `EventSource` waits a bit and reissues the same `GET`
request itself — you don't write any retry logic. Two knobs control this:

- `retry: 5000` — a line the *server* can send to tell the browser "wait this many milliseconds
  before reconnecting" (default is a few seconds if the server never says).
- `Last-Event-ID` — a request header the *browser* sends automatically on reconnect, echoing the
  last `id:` it saw. The server can use this to resume from where the client left off. Using it well
  requires the server to actually keep enough history to replay — many simple servers (including this
  project's, deliberately) ignore it and just send whatever the current state is.

## Worked Example (runnable Python)

This demonstrates the actual wire format above: a minimal SSE server (stdlib only, no frameworks)
and a client that manually parses the event frames the way a browser's `EventSource` would.

```python
import http.server
import io
import json
import threading
import time
import urllib.request

HOST, PORT = "localhost", 8765


class SSEHandler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"  # closes the connection when the handler returns

    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()

        for i in range(3):
            frame = (
                f"id: {i}\n"
                f"event: tick\n"
                f"data: {json.dumps({'count': i})}\n"
                f"\n"
            )
            self.wfile.write(frame.encode("utf-8"))
            self.wfile.flush()
            time.sleep(0.3)

    def log_message(self, *args):
        pass  # keep the demo output quiet


def run_server():
    http.server.HTTPServer((HOST, PORT), SSEHandler).handle_request()


def read_events(response):
    """Parse an SSE byte stream into a sequence of {field: value} events."""
    event = {}
    for raw_line in io.TextIOWrapper(response, encoding="utf-8"):
        line = raw_line.rstrip("\n")
        if line == "":
            if event:
                yield event
                event = {}
            continue
        if line.startswith(":"):
            continue  # comment/heartbeat, not part of an event
        field, _, value = line.partition(": ")
        event[field] = value


if __name__ == "__main__":
    threading.Thread(target=run_server, daemon=True).start()
    time.sleep(0.2)  # let the server start listening

    with urllib.request.urlopen(f"http://{HOST}:{PORT}/") as response:
        for event in read_events(response):
            print(event)
```

Running it prints three parsed events:

```
{'id': '0', 'event': 'tick', 'data': '{"count": 0}'}
{'id': '1', 'event': 'tick', 'data': '{"count": 1}'}
{'id': '2', 'event': 'tick', 'data': '{"count": 2}'}
```

That's the whole protocol: a plain HTTP response, held open, carrying `field: value` blocks separated
by blank lines. `EventSource` in the browser does what `read_events` does here, plus the automatic
reconnect.

## Common Pitfalls & Gotchas

- **Forgetting to flush.** If you don't flush after each write, the event sits in a buffer and the
  client never sees it until the buffer fills or the response ends — defeating the point of
  streaming. (goldstream's server flushes after every write, `internal/httpserver/sse.go`.)
- **A proxy buffers the stream anyway.** Nginx and some load balancers buffer responses by default,
  which turns your live stream into "nothing happens, then it all arrives at once." You have to
  explicitly disable proxy buffering for SSE routes.
- **Thinking it's bidirectional.** SSE is server → client only. If you need the client to send
  data, that's a normal separate HTTP request (or you actually need WebSockets).
- **The browser connection-per-origin ceiling.** Over plain HTTP/1.1, browsers cap concurrent
  connections to the same origin (commonly 6). Every open `EventSource` tab counts against that, so
  many tabs to the same site can starve each other. HTTP/2 removes this limit by multiplexing many
  streams over one connection.
- **Assuming `Last-Event-ID` gets you free gap-free resume.** The browser sends it automatically on
  reconnect, but nothing resumes unless your server actually reads that header and replays missed
  events from some buffer. Skipping this (as this project does, on purpose) means a reconnecting
  client just gets whatever the current state is, not what it missed.
- **Non-ASCII or multi-line data without escaping.** A `data:` value can't contain a raw newline —
  split it across multiple `data:` lines instead (the browser concatenates them with `\n`).

## When To Use / When Not To

| Situation | Reach for |
|---|---|
| Server pushes updates, client never needs to push a stream back (price ticks, notifications, progress bars, log tails) | **SSE** |
| Both sides need to send messages continuously (chat, multiplayer, collaborative editing) | WebSockets |
| Updates are infrequent and a few seconds of staleness is fine | Plain polling |
| You need binary data, not text | WebSockets (SSE is text-only) |
| You're already inside a gRPC service and want a streaming RPC | gRPC server-streaming |

Rule of thumb: if you'd describe the feature as "the browser needs to know when something changes,"
that's SSE. If you'd describe it as "the two sides are having a conversation," that's WebSockets.

## Quick Reference

**Response headers a server sends:**

| Header | Value | Why |
|---|---|---|
| `Content-Type` | `text/event-stream` | Tells the browser to parse it as an event stream, live |
| `Cache-Control` | `no-cache` | Stop intermediaries from caching a live stream |
| `Connection` | `keep-alive` | Keep the socket open (HTTP/1.1 only; irrelevant on HTTP/2) |

**Event frame fields (each block ends with a blank line):**

| Field | Meaning |
|---|---|
| `data:` | The payload (repeat the field for multi-line data) |
| `event:` | Named event type; drives which JS listener fires |
| `id:` | Event ID; echoed back by the browser as `Last-Event-ID` on reconnect |
| `retry:` | Milliseconds to wait before the browser's next reconnect attempt |
| `: comment` | Ignored by the browser; used as a heartbeat |

**JS client:**

```js
const es = new EventSource("/events");
es.addEventListener("price", (e) => console.log(JSON.parse(e.data)));
es.onerror = () => {};      // EventSource reconnects on its own
```

## Sources

- [WHATWG HTML Living Standard — Server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html) — the authoritative spec for the `text/event-stream` format, the `EventSource` interface, default reconnection time, and `Last-Event-ID` behavior.
- [MDN — Using server-sent events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events) — practical guide, including the browser per-origin connection limit and the HTTP/2 note.
- [MDN — EventSource](https://developer.mozilla.org/en-US/docs/Web/API/EventSource) — the browser API surface (`onmessage`, `onerror`, `onopen`, `addEventListener`).
