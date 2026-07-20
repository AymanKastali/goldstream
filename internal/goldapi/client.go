// Package goldapi is a thin client for the gold-api.com spot-price endpoint.
// gold-api.com is keyless and unmetered, so no auth token is needed.
package goldapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"goldstream/internal/gold"
)

// DefaultBaseURL is the public gold-api.com API root.
const DefaultBaseURL = "https://api.gold-api.com"

// Client fetches spot gold (XAU/USD) from gold-api.com.
type Client struct {
	baseURL string
	http    *http.Client
	log     *slog.Logger
}

// New returns a Client. baseURL is the API root (no trailing slash),
// e.g. DefaultBaseURL. Tests use this to point at a stub server.
func New(baseURL string, httpClient *http.Client, log *slog.Logger) *Client {
	return &Client{baseURL: baseURL, http: httpClient, log: log.With("component", "goldapi")}
}

// NewDefault returns a Client pointed at the public gold-api.com endpoint.
func NewDefault(httpClient *http.Client, log *slog.Logger) *Client {
	return New(DefaultBaseURL, httpClient, log)
}

// response mirrors the gold-api.com price payload, e.g.
// {"price":4017.6,"symbol":"XAU","updatedAt":"2026-07-20T12:06:25Z"}.
type response struct {
	Price     float64 `json:"price"`
	UpdatedAt string  `json:"updatedAt"` // RFC3339 timestamp
}

// Fetch returns the current spot price. The caller sets the deadline via ctx.
func (c *Client) Fetch(ctx context.Context) (gold.Price, error) {
	url := c.baseURL + "/price/XAU"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return gold.Price{}, err
	}
	req.Header.Set("Accept", "application/json") // gold-api.com is keyless — no auth header

	// Fetch errors below are returned, not logged here: the poller logs them
	// once at Error. Logging in both places would double-report every failure.
	c.log.Debug("requesting", "url", url)
	resp, err := c.http.Do(req)
	if err != nil {
		return gold.Price{}, fmt.Errorf("goldapi request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return gold.Price{}, fmt.Errorf("goldapi status %d: %s", resp.StatusCode, body)
	}
	c.log.Debug("responded", "status", resp.StatusCode)

	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return gold.Price{}, fmt.Errorf("goldapi decode: %w", err)
	}
	c.log.Debug("fetched", "usd", r.Price)
	at := time.Now() // fall back to now if the upstream timestamp is missing or unparseable
	if t, err := time.Parse(time.RFC3339, r.UpdatedAt); err == nil {
		at = t
	} else {
		// Keep serving (stamped now) but leave a trail: if gold-api.com ever
		// renames the field or changes the format, this is the only signal that
		// the timestamp is synthetic rather than the real upstream time.
		c.log.Debug("no usable timestamp; stamping now", "updatedAt", r.UpdatedAt, "err", err)
	}
	return gold.Price{USDPerOunce: r.Price, At: at}, nil
}
