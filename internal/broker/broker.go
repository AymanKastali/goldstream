package broker

import (
	"context"

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
}

// New returns a Broker. Call Run in its own goroutine before using it.
func New() *Broker {
	return &Broker{
		subscribeCh:   make(chan chan gold.Price),
		unsubscribeCh: make(chan chan gold.Price),
		publishCh:     make(chan gold.Price),
		done:          make(chan struct{}),
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

	for {
		select {
		case <-ctx.Done():
			for ch := range subs {
				delete(subs, ch)
				close(ch)
			}
			return
		case ch := <-b.subscribeCh:
			subs[ch] = struct{}{}
			if hasLast {
				send(ch, last) // replay the current price to a fresh viewer
			}
		case ch := <-b.unsubscribeCh:
			if _, ok := subs[ch]; ok {
				delete(subs, ch)
				close(ch)
			}
		case p := <-b.publishCh:
			last, hasLast = p, true
			for ch := range subs {
				send(ch, p)
			}
		}
	}
}

// send is non-blocking: a slow client that hasn't drained its buffer simply
// misses this tick rather than stalling every other subscriber.
func send(ch chan gold.Price, p gold.Price) {
	select {
	case ch <- p:
	default:
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
