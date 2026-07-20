package goldapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchParsesPriceAndSendsAuth(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("x-access-token")
		_, _ = w.Write([]byte(`{"price":2401.55,"timestamp":1700000000}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "secret", srv.Client())
	p, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.USDPerOunce != 2401.55 {
		t.Fatalf("price = %v want 2401.55", p.USDPerOunce)
	}
	if !p.At.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("At = %v", p.At)
	}
	if gotToken != "secret" {
		t.Fatalf("token = %q want secret", gotToken)
	}
}

func TestFetchErrorsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "k", srv.Client()).Fetch(context.Background()); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestFetchRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New("http://example.invalid", "k", http.DefaultClient).Fetch(ctx); err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
