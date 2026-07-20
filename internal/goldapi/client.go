// Package goldapi is a thin client for goldapi.io spot-price endpoint.
package goldapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"goldstream/internal/gold"
)

// DefaultBaseURL is the public goldapi.io API root.
const DefaultBaseURL = "https://www.goldapi.io/api"

// Client fetches spot gold (XAU/USD) from goldapi.io.
type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

// New returns a Client. baseURL is the API root (no trailing slash),
// e.g. DefaultBaseURL. Tests use this to point at a stub server.
func New(baseURL, key string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, key: key, http: httpClient}
}

// NewDefault returns a Client pointed at the public goldapi.io endpoint.
func NewDefault(key string, httpClient *http.Client) *Client {
	return New(DefaultBaseURL, key, httpClient)
}

type response struct {
	Price     float64 `json:"price"`
	Timestamp int64   `json:"timestamp"`
}

// Fetch returns the current spot price. The caller sets the deadline via ctx.
func (c *Client) Fetch(ctx context.Context) (gold.Price, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/XAU/USD", nil)
	if err != nil {
		return gold.Price{}, err
	}
	req.Header.Set("x-access-token", c.key)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return gold.Price{}, fmt.Errorf("goldapi request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return gold.Price{}, fmt.Errorf("goldapi status %d: %s", resp.StatusCode, body)
	}

	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return gold.Price{}, fmt.Errorf("goldapi decode: %w", err)
	}
	at := time.Now()
	if r.Timestamp > 0 {
		at = time.Unix(r.Timestamp, 0)
	}
	return gold.Price{USDPerOunce: r.Price, At: at}, nil
}
