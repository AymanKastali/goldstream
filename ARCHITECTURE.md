# goldstream — How It Works

A technical walkthrough of the goldstream server, written so any developer can
read the code with a mental model already in place. It covers the architecture,
the exact path a price takes from the upstream API to a browser, the concurrency
model, and the design decisions behind it.

> **TL;DR** — The server polls gold-api.com **once per interval**, pushes each
> price into an in-memory **broker**, and the broker **fans it out over
> Server-Sent Events (SSE)** to every connected browser. N viewers still cost
> only 1 upstream call per tick. The upstream is keyless, so there is no secret
> to configure.

---

## 1. The big picture

```
 gold-api.com ─poll─▶ poller ──publish──▶ broker ──fan-out──▶ SSE handler ──stream──▶ browser
 (1 call/interval)                        (1 goroutine        (1 goroutine            (EventSource,
                                           owns all state)     per client)             auto-reconnect)
```

Three long-lived goroutines do the work, connected by Go channels:

| Goroutine        | Count            | Job                                                        |
| ---------------- | ---------------- | ---------------------------------------------------------- |
| **poller**       | 1                | Fetch the price on a ticker, hand each result to the broker |
| **broker**       | 1                | Own the subscriber set + last price; fan each price out     |
| **SSE handler**  | 1 per browser    | Write SSE frames to one HTTP connection until it closes     |

Everything is pure Go **standard library** — no third-party dependencies.

---

## 2. Layout & layers (light hexagonal architecture)

Dependencies point **inward**: `infra → app → domain`. The domain knows nothing
about HTTP or JSON; the infra knows nothing about how prices are fanned out.

```
cmd/goldstream/main.go        composition root — wires everything, owns lifecycle
internal/
  gold/price.go               DOMAIN  — the Price value type (no behavior)
  broker/broker.go            APP     — pub/sub fan-out hub
  poller/poller.go            APP     — ticker → fetch → publish
  goldapi/client.go           INFRA   — gold-api.com HTTP client
  httpserver/server.go        INFRA   — routes + embedded UI + /healthz
  httpserver/sse.go           INFRA   — the SSE handler (the core demo)
  config/config.go            INFRA   — env config, fail-fast
web/index.html                the browser UI (embedded into the binary)
```

Why this split: the **broker** and **poller** (the app logic) can be tested with
fakes and never touch the network. The **goldapi client** and **SSE handler**
(the infra) are the only parts that know about HTTP. Swapping the price source or
the transport wouldn't touch the middle.

---

## 3. The domain: `gold.Price`

The single value that flows through the whole system.

```go
// internal/gold/price.go
type Price struct {
    USDPerOunce float64
    At          time.Time
}
```

No methods, no behavior — just data. Every layer speaks in `gold.Price`.

---

## 4. Startup & lifecycle: `cmd/goldstream/main.go`

The composition root. It runs, in order:

1. **Load config** — `config.Load()`. Invalid input (e.g. a bad `POLL_INTERVAL`
   or `LOG_LEVEL`) logs and `os.Exit(1)`. *Fail fast, never start half-configured.*
   The upstream is keyless, so there is no required secret.
2. **Create one root context** tied to `SIGINT`/`SIGTERM` via
   `signal.NotifyContext`. This single context is the app's off switch — the
   broker, poller, and server all watch it.
3. **Start the broker** — `go prices.Run(ctx)`.
4. **Build the client + poller and start it** — `go feed.Run(ctx)`.
5. **Start the HTTP server** in its own goroutine.
6. **Block on `<-ctx.Done()`**, then `srv.Shutdown(shutdownCtx)` with a timeout
   to drain in-flight connections.

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

prices := broker.New(log)
go prices.Run(ctx)

client := goldapi.NewDefault(&http.Client{Timeout: cfg.HTTPTimeout}, log)
feed := poller.New(client, prices.Publish, cfg.PollInterval, cfg.FetchTimeout, log)
go feed.Run(ctx)

srv := &http.Server{Addr: ":" + cfg.Port, Handler: httpserver.New(prices, cfg.ReconnectDelay, log)}
// ... ListenAndServe in a goroutine, then wait on ctx, then Shutdown
```

Note the wiring: the poller is handed `prices.Publish` (a function value) rather
than the broker itself. The poller depends on "something I can publish to," not
on the broker's concrete type.

---

## 5. The upstream client: `internal/goldapi/client.go`

A thin wrapper over one HTTP call.

```go
func (c *Client) Fetch(ctx context.Context) (gold.Price, error)
```

`Fetch` does `GET https://api.gold-api.com/price/XAU` with:

- header `Accept: application/json`.
- **no auth header** — gold-api.com is keyless and unmetered.

It then:

1. Checks for HTTP 200 — otherwise returns an error with a truncated body.
2. Decodes `{"price": …, "updatedAt": …}` (`updatedAt` is an RFC3339 string).
3. Returns `gold.Price{USDPerOunce: price, At: <parsed updatedAt>}`
   (falling back to `time.Now()` if `updatedAt` is absent or unparseable).

**The client sets no deadline of its own** — the *caller* controls timeout via
`ctx`. That keeps timeout policy in one place (the poller).

---

## 6. The clock: `internal/poller/poller.go`

Turns "fetch a price" into "fetch a price forever, on a schedule."

```go
func (p *Poller) Run(ctx context.Context) {
    p.PollOnce(ctx)                       // fetch immediately — first viewer isn't blank
    t := time.NewTicker(p.interval)       // then every interval (default 60s)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:        p.PollOnce(ctx)
        }
    }
}
```

`PollOnce` wraps each fetch in a **per-call timeout** and decides what to do with
the result:

```go
func (p *Poller) PollOnce(ctx context.Context) {
    ctx, cancel := context.WithTimeout(ctx, p.timeout)
    defer cancel()
    price, err := p.fetcher.Fetch(ctx)
    if err != nil {
        p.log.Error("gold price fetch failed", "err", err)
        return                            // keep-last: don't crash, don't clear
    }
    p.publish(price)                      // → broker.Publish
}
```

Two resilience choices live here:

- **Per-call deadline** — a hung upstream can't stall the ticker.
- **Keep-last on error** — a failed fetch logs and returns; the broker keeps
  serving the last good price. The stream never breaks over a transient blip.

`fetcher` is an interface (`Fetch(ctx) (gold.Price, error)`), so tests inject a
fake and the poller never touches the network.

---

## 7. The heart: `internal/broker/broker.go`

A **single-goroutine pub/sub hub**. Instead of a mutex around shared state, one
goroutine *owns* all state and everyone talks to it over channels. This is the
idiomatic Go pattern: *share memory by communicating.*

State owned by `Run`:

- `subs map[chan gold.Price]struct{}` — the set of connected subscribers.
- `last gold.Price` + `hasLast bool` — the most recent price.

```go
func (b *Broker) Run(ctx context.Context) {
    subs := make(map[chan gold.Price]struct{})
    var last gold.Price
    var hasLast bool
    for {
        select {
        case <-ctx.Done():
            for ch := range subs { delete(subs, ch); close(ch) }
            return
        case ch := <-b.subscribeCh:
            subs[ch] = struct{}{}
            if hasLast { send(ch, last) }        // replay: newcomer sees price now
        case ch := <-b.unsubscribeCh:
            if _, ok := subs[ch]; ok { delete(subs, ch); close(ch) }
        case p := <-b.publishCh:
            last, hasLast = p, true
            for ch := range subs { send(ch, p) } // fan out to everyone
        }
    }
}
```

The public API just sends on those channels:

```go
func (b *Broker) Subscribe() chan gold.Price { ch := make(chan gold.Price, 1); b.subscribeCh <- ch; return ch }
func (b *Broker) Unsubscribe(ch chan gold.Price) { b.unsubscribeCh <- ch }
func (b *Broker) Publish(p gold.Price)           { b.publishCh <- p }
```

Two details that matter:

- **Replay on subscribe.** A browser connecting mid-interval gets the current
  price immediately, instead of staring at a blank screen for up to 60s.
- **Non-blocking send.** The `send` helper drops a tick for a slow subscriber
  rather than stalling the whole fan-out:

  ```go
  func send(ch chan gold.Price, p gold.Price) {
      select {
      case ch <- p: // delivered
      default:      // buffer full → this client misses this tick, others don't
      }
  }
  ```

  Channels are buffered (size 1), so a client that's keeping up never misses a
  tick; only a genuinely stuck client does.

**Ownership contract:** the broker *closes* a subscriber channel on unsubscribe
or shutdown. So a handler must call `Unsubscribe` exactly once (via `defer`) and
must be ready for its channel to be closed (the `ok` check on receive).

---

## 8. The stream: `internal/httpserver/sse.go`

One `handleEvents` runs **per connected browser**. It's the whole SSE protocol in
one function.

```go
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")

    stream := http.NewResponseController(w)   // Go 1.20+ way to Flush

    // Advertise the reconnect delay up front (see §8a), flushed immediately.
    fmt.Fprintf(w, "retry: %d\n\n", s.reconnectDelay.Milliseconds())
    stream.Flush()

    prices := s.broker.Subscribe()
    defer s.broker.Unsubscribe(prices)        // guaranteed cleanup on any exit

    heartbeat := time.NewTicker(heartbeatInterval) // 15s
    defer heartbeat.Stop()

    ctx := r.Context()
    var eventID int
    for {
        select {
        case <-ctx.Done():                    // browser closed the tab
            return
        case price, ok := <-prices:
            if !ok { return }                 // broker shutting down
            eventID++
            fmt.Fprintf(w, "id: %d\nevent: price\ndata: {\"usd\":%.2f,\"at\":%q}\n\n",
                eventID, price.USDPerOunce, price.At.Format(time.RFC3339))
            if stream.Flush() != nil { return }
        case <-heartbeat.C:
            fmt.Fprint(w, ": keep-alive\n\n")  // comment line — keeps proxies open
            if stream.Flush() != nil { return }
        }
    }
}
```

Key mechanics:

- **`text/event-stream` + no-cache** — tells the browser this is an SSE stream.
- **`Flush()` after every write** — without it, Go buffers the response and the
  browser sees nothing. Flushing pushes each frame out immediately.
- **Client disconnect** via `r.Context()` — when the tab closes, `ctx.Done()`
  fires, the handler returns, and `defer Unsubscribe` cleans up. **No goroutine
  leak.**
- **Heartbeat** — a `: keep-alive` comment every 15s stops proxies/load balancers
  from killing an idle connection during quiet periods.

`heartbeatInterval` is a package `var` (not a const) so tests can shorten it.

### The SSE wire format

Each frame is plain text terminated by a blank line:

```
retry: 3000
id: 1
event: price
data: {"usd":4023.06,"at":"2026-07-20T15:15:49+04:00"}

: keep-alive

id: 2
event: price
data: {"usd":4024.10,"at":"2026-07-20T15:16:49+04:00"}
```

- `retry:` — the reconnect delay in **milliseconds**, sent once at the top of the
  stream (see §8a).
- `id:` — event id (restarts at 1 per connection; see the note below).
- `event:` — the named event type; the browser listens for `"price"`.
- `data:` — the payload (JSON here, but SSE data is just a string).
- `: …` — a comment (ignored by clients), used for keep-alive.

See it yourself: `curl -N localhost:8080/events`.

> **On resumption / `Last-Event-ID`:** ids restart per connection and the server
> does *not* read the client's `Last-Event-ID` header. Gap-free replay is
> deliberately out of scope — this stream only ever shows the *latest* price, and
> the broker replays the current price on subscribe, so a reconnecting client
> loses nothing meaningful.

### 8a. Reconnection

Reconnection is handled by the browser's built-in `EventSource` — the server is
stateless per connection and does nothing special to resume one.

1. **On drop** (server restart, network blip, proxy timeout), `EventSource` fires
   `onerror`, waits the retry delay, then automatically re-opens `GET /events`.
   No client code required.
2. **The server cleans up the old connection**: the closed TCP socket cancels
   `r.Context()` → the handler returns → `defer Unsubscribe` removes it from the
   broker. No goroutine leak.
3. **The reconnect is a brand-new request** → fresh handler → fresh
   `broker.Subscribe()` → the broker **replays the current price instantly**
   (measured ~0.5 ms, versus up to `POLL_INTERVAL` if it had to wait for the next
   poll). So the client never sits blank.
4. **The retry delay is server-controlled.** The `retry: <ms>` line sets how long
   the browser waits before step 1's retry. It comes from `RECONNECT_DELAY`
   (default `3s`). Sending it means the reconnect timing is explicit and tunable
   rather than left to each browser's default.

The browser UI reflects this: the status pill is green **"● live"** while
connected and amber **"● reconnecting"** between `onerror` and the next `onopen`.

---

## 9. Routes & the embedded UI: `internal/httpserver/server.go`

```go
func New(b *broker.Broker) http.Handler {
    s := &server{broker: b, mux: http.NewServeMux()}
    s.mux.HandleFunc("GET /events", s.handleEvents)     // SSE
    s.mux.HandleFunc("GET /healthz", ok)                // returns "ok"
    s.mux.Handle("GET /", http.FileServer(http.FS(sub))) // embedded web/index.html
    return s.mux
}
```

- Uses the Go 1.22+ **method+path mux** (`"GET /events"`) — no third-party
  router.
- `web/index.html` is compiled **into the binary** with `//go:embed`, so the
  server is a single self-contained artifact — no files to ship alongside it.
- `GET /healthz` returns `ok` for liveness checks.

---

## 10. The browser: `web/index.html`

The client is thin because the browser's built-in `EventSource` does the heavy
lifting.

```js
const es = new EventSource("/events");
es.onopen  = () => setStatus(true);
es.onerror = () => setStatus(false);   // EventSource auto-reconnects on drop
es.addEventListener("price", (e) => {
    const d = JSON.parse(e.data);      // { usd, at }
    // update price, change-since-open, session high/low, sparkline
});
```

`EventSource`:

- opens the long-lived connection to `/events`,
- parses `event:`/`data:` frames and dispatches them as JS events,
- **auto-reconnects** if the connection drops. On reconnect the broker's
  replay-on-subscribe means the client immediately gets the current price again.

The UI (a dark trading-terminal card) tracks state **client-side** from the
stream: the first price seen (for "change since open"), running high/low, and a
rolling window of the last ~60 prices for a hand-drawn SVG sparkline. All inline,
zero dependencies. The `curl` hint in the footer is rewritten at runtime to the
actual `location.host` so it's always copy-paste-correct.

---

## 11. Configuration: `internal/config/config.go`

Config comes from the environment, with fail-fast validation. **No secret is
required** — the upstream (gold-api.com) is keyless.

| Var               | Required | Default | Notes                                          |
| ----------------- | -------- | ------- | ---------------------------------------------- |
| `PORT`            | no       | `8080`  | HTTP listen port                               |
| `POLL_INTERVAL`   | no       | `60s`   | Go duration; parsed & validated                |
| `RECONNECT_DELAY` | no       | `3s`    | Browser reconnect wait; sent as SSE `retry:`   |
| `LOG_LEVEL`       | no       | `debug` | `debug`\|`info`\|`warn`\|`error`; debug traces every step |

- Every var is optional, so the service runs out of the box. Invalid values
  (a bad duration, an unknown log level) fail fast at boot rather than silently
  falling back.
- `make run` sources a local `.env` into the shell (stripping shell quoting the
  same way Docker Compose does) so the values reach the process cleanly.

---

## 12. Concurrency model — why it's race-free

- **All shared state is owned by exactly one goroutine.** The subscriber set and
  last price live inside `broker.Run`; nothing else touches them. Every mutation
  arrives as a channel message and is processed serially in the `select` loop. No
  mutex needed, and `go test -race` is clean.
- **Each SSE handler is independent.** It owns its own channel; the broker only
  ever sends to it or closes it.
- **Backpressure is bounded.** Buffered channels + non-blocking send mean one
  slow client can never block the poller or the other clients — it just misses a
  tick.
- **Cancellation is unified.** One root context, derived from OS signals, cascades
  to the broker, poller, and every handler. Shutdown is deterministic: signal →
  `ctx.Done()` → goroutines return → `srv.Shutdown` drains connections.

---

## 12a. Logging & observability

Every component logs its steps through the standard-library `log/slog`, so you can
watch a price travel from the upstream fetch all the way to a browser frame in the
terminal — no external tooling.

- **One logger, threaded everywhere.** `main` builds a single `slog.Logger` and
  passes it into the broker, goldapi client, poller, and HTTP server. Each tags its
  own lines with `component=<name>` via `log.With(...)`, so you can tell at a glance
  which layer emitted a line (`component=broker`, `component=goldapi`, …).
- **Per-connection ids.** Each `/events` connection is stamped with `conn=<n>` and
  `remote=<addr>`, so with several browsers open you can still follow one client's
  frames end-to-end. The count comes from an `atomic.Int64` on the server.
- **Level-controlled via `LOG_LEVEL`** (default `debug`). Debug prints every step;
  `info` narrows to lifecycle only; a slow-client drop is surfaced at `warn`:

  | Level   | What you see                                                                 |
  | ------- | ---------------------------------------------------------------------------- |
  | `info`  | config loaded · broker/poller started · client connected/disconnected · listening · shutting down |
  | `debug` | + poll tick · goldapi requesting/responded/fetched · publishing · publish received · fanned out · subscriber added/removed · replaying · sent retry hint · subscribed · sent price frame · sent keep-alive |
  | `warn`  | dropped tick for a slow subscriber (the visible signal of the non-blocking-send back-pressure) |

- **Errors are logged once.** A failed upstream fetch is *not* logged inside the
  goldapi client; it is returned to the poller, which logs it a single time at
  `error` (and keeps the last price). No double-reporting.
- **`config loaded` prints the effective settings** at startup, so a misconfigured
  run is obvious from the first line. (There is no secret to redact — the upstream
  is keyless.)

A disconnect line always reports a `reason` — `context canceled` (browser closed),
`broker shutdown`, or `write error` — plus how many `frames` that connection sent.

---

## 13. End-to-end trace of one price

1. Ticker fires in `poller.Run` → `PollOnce(ctx)`.
2. `PollOnce` sets an 8s deadline and calls `goldapi.Fetch`.
3. `Fetch` GETs gold-api.com (no auth header), decodes JSON → `gold.Price`.
4. `PollOnce` calls `publish(price)` → `broker.Publish` → sends on `publishCh`.
5. `broker.Run` receives it, stores it as `last`, and `send`s it to every
   subscriber channel.
6. Each `handleEvents` goroutine receives the price, writes an
   `id/event/data` frame, and flushes.
7. Each browser's `EventSource` fires a `"price"` event; the JS updates the
   price, change, high/low, and sparkline.
8. A new browser that connects at any point gets `last` replayed instantly on
   subscribe — no wait for the next tick.

---

## 14. Running, testing, building

```bash
make run                    # sources .env, runs the server (or: go run ./cmd/goldstream)
open http://localhost:8080  # the live UI
curl -N localhost:8080/events   # the raw SSE stream

go test ./...               # unit tests — no network; upstream faked with httptest
go test -race ./...         # verifies broker concurrency
go build -o goldstream ./cmd/goldstream
```

Tests never hit the real API: the goldapi client is tested against an
`httptest.Server`, and the poller/SSE tests use fakes and the in-memory broker.

---

## 15. Design decisions & trade-offs

| Decision | Why | Trade-off |
| --- | --- | --- |
| **Poll once, fan out to many** | Upstream calls stay flat regardless of viewer count → kind to the upstream, scales to many viewers | Everyone sees the same price; not per-client customizable |
| **SSE, not WebSockets** | One-way server→client is all we need; SSE is simpler, plain HTTP, and auto-reconnects for free | No client→server messaging (fine here) |
| **Single-goroutine broker** | No mutexes; state is trivially race-free and easy to reason about | All fan-out is serialized through one goroutine (ample for this scale) |
| **Non-blocking send** | One slow client can't stall the system | A stuck client may skip a tick (acceptable — only latest matters) |
| **Keep-last on fetch error** | Transient upstream blips don't break the stream | Clients may briefly see a slightly stale price |
| **Embedded UI (`go:embed`)** | Single self-contained binary; nothing to deploy alongside | Changing the UI requires a rebuild |
| **Env config, fail-fast** | Twelve-factor; a misconfig fails loudly at boot, not at request time | — |
