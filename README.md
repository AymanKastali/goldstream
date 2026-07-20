# 🥇 goldstream

A tiny, self-contained Go server that streams **live gold prices** (XAU/USD) to the
browser using **Server-Sent Events (SSE)**. Built with **Go 1.26** and the **standard
library only** — zero third-party dependencies.

It exists to demonstrate, in as little code as possible, how SSE works in Go:
one-way server → client streaming over a long-lived HTTP connection, with named
events, event IDs, heartbeats, and automatic browser reconnection.

## How it works

The server polls [goldapi.io](https://www.goldapi.io) once per interval and **fans each
update out to every connected browser** through an in-process broker. Because the poll
is shared, upstream API calls stay flat no matter how many people are watching — which
keeps you inside free-tier rate limits.

```
                          ┌─────────────┐   SSE    ┌─────────┐
 goldapi.io ── poll ──▶  │   broker    │ ───────▶ │ browser │
 (1 call / interval)     │  (fan-out)  │ ───────▶ │ browser │
                          └─────────────┘ ───────▶ │  curl   │
```

- **`internal/gold`** — the `Price` value type (domain).
- **`internal/broker`** — a single-goroutine pub/sub hub; the poller publishes, every SSE handler subscribes.
- **`internal/poller`** — fetches on a ticker, with a per-call timeout; keeps the last value on error.
- **`internal/goldapi`** — the goldapi.io HTTP client.
- **`internal/httpserver`** — the SSE handler (`internal/httpserver/sse.go`) and routes.
- **`cmd/goldstream`** — wiring and graceful shutdown.

## Run it

You need a free API key from [goldapi.io](https://www.goldapi.io) and Go 1.26+.

```bash
export GOLDAPI_KEY=your-key-here      # required
export PORT=8080                      # optional (default 8080)
export POLL_INTERVAL=60s              # optional (default 60s)

go run ./cmd/goldstream        # or: make run
```

There's a `Makefile` for the common tasks — `make help` lists them (`run`, `build`,
`test`, `race`, `vet`, `fmt`, `docker`, `compose`). It auto-loads a local `.env`, so
once your key is there, `make run` is all you need.

Then open <http://localhost:8080> — the price updates live, green when it ticks up,
red when it ticks down. No page refresh, ever.

## Run it with Docker

The image is a static binary on a distroless base (~a few MB, non-root, CA
certs included for the HTTPS call to goldapi.io).

```bash
docker build -t goldstream .
docker run --rm -p 8080:8080 -e GOLDAPI_KEY=your-key-here goldstream
```

Or with Compose (put `GOLDAPI_KEY=...` in a local `.env` file, then):

```bash
docker compose up --build
```

## See the raw SSE stream

SSE is just text over a kept-open HTTP connection. Watch the frames directly:

```bash
curl -N http://localhost:8080/events
```

```
id: 1
event: price
data: {"usd":2401.55,"at":"2026-07-20T14:41:00Z"}

: keep-alive

id: 2
event: price
data: {"usd":2402.10,"at":"2026-07-20T14:42:00Z"}
```

Each event has an `id:`, a named `event:` type, and a `data:` payload, terminated by a
blank line. The `: keep-alive` comment lines hold the connection open during quiet
periods.

## Test

```bash
go test ./...          # unit tests (no network — upstream is faked with httptest)
go test -race ./...    # verify the broker's concurrency
```
