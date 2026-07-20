package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"goldstream/internal/gold"
)

type fakeFetcher struct {
	price gold.Price
	err   error
	calls int
}

func (f *fakeFetcher) Fetch(context.Context) (gold.Price, error) {
	f.calls++
	return f.price, f.err
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestPollOncePublishesOnSuccess(t *testing.T) {
	f := &fakeFetcher{price: gold.Price{USDPerOunce: 2400}}
	var got gold.Price
	p := New(f, func(pr gold.Price) { got = pr }, time.Minute, time.Second, discardLogger())
	p.PollOnce(context.Background())
	if got.USDPerOunce != 2400 {
		t.Fatalf("published %v want 2400", got.USDPerOunce)
	}
}

func TestPollOnceDoesNotPublishOnError(t *testing.T) {
	f := &fakeFetcher{err: errors.New("boom")}
	published := false
	p := New(f, func(gold.Price) { published = true }, time.Minute, time.Second, discardLogger())
	p.PollOnce(context.Background())
	if published {
		t.Fatal("should not publish on fetch error (keep last)")
	}
}
