package goldapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchParsesPrice(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		// gold-api.com shape: price plus an RFC3339 updatedAt (no unix timestamp).
		_, _ = w.Write([]byte(`{"price":2401.55,"symbol":"XAU","updatedAt":"2023-11-14T22:13:20Z"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, srv.Client(), slog.New(slog.DiscardHandler))
	p, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.USDPerOunce != 2401.55 {
		t.Fatalf("price = %v want 2401.55", p.USDPerOunce)
	}
	if !p.At.Equal(time.Unix(1700000000, 0)) { // 2023-11-14T22:13:20Z
		t.Fatalf("At = %v", p.At)
	}
	if gotPath != "/price/XAU" {
		t.Fatalf("path = %q want /price/XAU", gotPath)
	}
}

func TestFetchFallsBackToNowOnBadTimestamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Valid price, but an unparseable timestamp — the client should keep
		// serving and stamp ~now rather than erroring out.
		_, _ = w.Write([]byte(`{"price":2400,"updatedAt":"not-a-timestamp"}`))
	}))
	defer srv.Close()

	before := time.Now()
	c := New(srv.URL, srv.Client(), slog.New(slog.DiscardHandler))
	p, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.USDPerOunce != 2400 {
		t.Fatalf("price = %v want 2400", p.USDPerOunce)
	}
	if p.At.Before(before) {
		t.Fatalf("At = %v, expected fallback to ~now (>= %v)", p.At, before)
	}
}

func TestFetchErrorsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, srv.Client(), slog.New(slog.DiscardHandler)).Fetch(context.Background()); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestFetchRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New("http://example.invalid", http.DefaultClient, slog.New(slog.DiscardHandler)).Fetch(ctx); err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
