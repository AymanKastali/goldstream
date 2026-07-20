package broker

import (
	"context"
	"log/slog"

	"goldstream/internal/gold"
)

// Broker is a single-goroutine pub/sub hub: the poller Publishes prices and
// every subscribed SSE handler receives them. One goroutine (Run) owns all
// state, so no mutex is needed — every mutation arrives over a channel.
type Broker struct {
	subscribeCh   chan chan gold.Price
	unsubscribeCh chan chan gold.Price
	publishCh     chan gold.Price
	done          chan struct{} // closed when Run returns
	log           *slog.Logger
}

// New returns a Broker. Call Run in its own goroutine before using it.
func New(log *slog.Logger) *Broker {
	return &Broker{
		subscribeCh:   make(chan chan gold.Price),
		unsubscribeCh: make(chan chan gold.Price),
		publishCh:     make(chan gold.Price),
		done:          make(chan struct{}),
		log:           log.With("component", "broker"),
	}
}

// Run owns the subscriber set and the last price until ctx is cancelled.
// Once it returns, the public methods below become safe no-ops so a still
// running handler never blocks on shutdown.
func (b *Broker) Run(ctx context.Context) {
	defer close(b.done)

	subs := make(map[chan gold.Price]struct{})
	var last gold.Price
	var hasLast bool

	b.log.Info("broker started")
	for {
		select {
		case <-ctx.Done():
			b.log.Info("broker stopping", "subscribers", len(subs))
			for ch := range subs {
				delete(subs, ch)
				close(ch)
			}
			return
		case ch := <-b.subscribeCh:
			subs[ch] = struct{}{}
			b.log.Debug("subscriber added", "subscribers", len(subs))
			if hasLast {
				send(ch, last) // replay the current price to a fresh viewer
				b.log.Debug("replaying last price to new subscriber", "usd", last.USDPerOunce)
			}
		case ch := <-b.unsubscribeCh:
			if _, ok := subs[ch]; ok {
				delete(subs, ch)
				close(ch)
				b.log.Debug("subscriber removed", "subscribers", len(subs))
			}
		case p := <-b.publishCh:
			last, hasLast = p, true
			b.log.Debug("publish received", "usd", p.USDPerOunce, "subscribers", len(subs))
			var delivered, dropped int
			for ch := range subs {
				if send(ch, p) {
					delivered++
				} else {
					dropped++
				}
			}
			b.log.Debug("fanned out", "delivered", delivered, "dropped", dropped)
			if dropped > 0 {
				// A slow client that hasn't drained its buffer misses this tick (the
				// non-blocking-send back-pressure design). At buffer depth 1 this can
				// fire every tick under load, so it stays at debug rather than warn.
				b.log.Debug("dropped tick for slow subscriber", "dropped", dropped)
			}
		}
	}
}

// send is non-blocking: a slow client that hasn't drained its buffer simply
// misses this tick rather than stalling every other subscriber. It reports
// whether the price was delivered (true) or dropped (false).
func send(ch chan gold.Price, p gold.Price) bool {
	select {
	case ch <- p:
		return true
	default:
		return false
	}
}

// Subscribe registers a new listener and returns its channel. The current
// price (if any) is delivered immediately so a fresh viewer isn't blank.
// If the broker has already stopped, the returned channel is closed.
func (b *Broker) Subscribe() chan gold.Price {
	ch := make(chan gold.Price, 1)
	select {
	case b.subscribeCh <- ch:
	case <-b.done:
		close(ch)
	}
	return ch
}

// Unsubscribe removes a listener and closes its channel. Safe to call after
// the broker has stopped (then it is a no-op). Call exactly once.
func (b *Broker) Unsubscribe(ch chan gold.Price) {
	select {
	case b.unsubscribeCh <- ch:
	case <-b.done:
	}
}

// Publish broadcasts a price to every current subscriber. Safe to call after
// the broker has stopped (then it is a no-op).
func (b *Broker) Publish(p gold.Price) {
	select {
	case b.publishCh <- p:
	case <-b.done:
	}
}
