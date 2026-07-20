package broker

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"goldstream/internal/gold"
)

func recv(t *testing.T, ch chan gold.Price) gold.Price {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for price")
		return gold.Price{}
	}
}

func TestPublishReachesSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(slog.New(slog.DiscardHandler))
	go b.Run(ctx)

	ch := b.Subscribe()
	b.Publish(gold.Price{USDPerOunce: 2400})
	if got := recv(t, ch); got.USDPerOunce != 2400 {
		t.Fatalf("got %v want 2400", got.USDPerOunce)
	}
}

func TestFanOutToAllSubscribers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(slog.New(slog.DiscardHandler))
	go b.Run(ctx)

	a, c := b.Subscribe(), b.Subscribe()
	b.Publish(gold.Price{USDPerOunce: 2401})
	if recv(t, a).USDPerOunce != 2401 || recv(t, c).USDPerOunce != 2401 {
		t.Fatal("both subscribers should receive the price")
	}
}

func TestNewSubscriberGetsLastPrice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(slog.New(slog.DiscardHandler))
	go b.Run(ctx)

	b.Publish(gold.Price{USDPerOunce: 2402})
	time.Sleep(20 * time.Millisecond) // let Run process the publish
	late := b.Subscribe()
	if recv(t, late).USDPerOunce != 2402 {
		t.Fatal("late subscriber should replay the last price")
	}
}

func TestSlowSubscriberDropsTickWithoutBlocking(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(slog.New(slog.DiscardHandler))
	go b.Run(ctx)

	ch := b.Subscribe() // buffered depth 1, deliberately not drained until after the publishes

	// Publishes are serialized through Run, so send order is deterministic: the
	// first fills the (depth-1) buffer, the rest hit a full buffer and are
	// dropped by the non-blocking send. None of these must block.
	b.Publish(gold.Price{USDPerOunce: 1})
	b.Publish(gold.Price{USDPerOunce: 2})
	b.Publish(gold.Price{USDPerOunce: 3})
	time.Sleep(20 * time.Millisecond) // let Run finish fanning out all three before we drain

	if got := recv(t, ch); got.USDPerOunce != 1 {
		t.Fatalf("first price = %v want 1 (only the buffered tick survives)", got.USDPerOunce)
	}
	select {
	case p := <-ch:
		t.Fatalf("expected later ticks to be dropped, got %v", p.USDPerOunce)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := New(slog.New(slog.DiscardHandler))
	go b.Run(ctx)

	ch := b.Subscribe()
	b.Unsubscribe(ch)
	time.Sleep(20 * time.Millisecond)
	b.Publish(gold.Price{USDPerOunce: 2403})
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("unsubscribed channel should not receive a price")
		}
	case <-time.After(100 * time.Millisecond):
	}
}
