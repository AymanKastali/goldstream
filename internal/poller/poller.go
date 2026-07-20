// Package poller periodically fetches a gold price and hands it to a sink.
package poller

import (
	"context"
	"log/slog"
	"time"

	"goldstream/internal/gold"
)

// Fetcher retrieves the current gold price. Implemented by goldapi.Client.
type Fetcher interface {
	Fetch(ctx context.Context) (gold.Price, error)
}

// Poller fetches the price on a fixed interval and hands each successful
// result to publish. One upstream call per tick, regardless of how many
// viewers are connected.
type Poller struct {
	fetcher  Fetcher
	publish  func(gold.Price)
	interval time.Duration
	timeout  time.Duration
	log      *slog.Logger
}

// New returns a Poller. interval is the gap between fetches; timeout bounds
// each individual fetch.
func New(f Fetcher, publish func(gold.Price), interval, timeout time.Duration, log *slog.Logger) *Poller {
	return &Poller{fetcher: f, publish: publish, interval: interval, timeout: timeout, log: log.With("component", "poller")}
}

// Run fetches immediately, then every interval, until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	p.log.Info("poller started", "interval", p.interval)
	p.PollOnce(ctx) // fetch immediately so the first viewer isn't blank
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.log.Debug("poll tick") // fires once per interval, so it stays at debug
			p.PollOnce(ctx)
		}
	}
}

// PollOnce fetches under a per-call deadline; on error it logs and keeps the
// last published value rather than crashing the stream.
func (p *Poller) PollOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	price, err := p.fetcher.Fetch(ctx)
	if err != nil {
		p.log.Error("gold price fetch failed", "err", err)
		return
	}
	p.log.Info("gold price", "usd_per_oz", price.USDPerOunce)
	p.log.Debug("publishing to broker", "usd", price.USDPerOunce)
	p.publish(price)
}
