package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Taproot's access guard refuses state-changing GETs without
// X-Taproot-Request, from loopback too, and the TUI's OAuth tab calls them.
func TestClientSendsTaprootRequestHeader(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("X-Taproot-Request"))
		_, _ = w.Write([]byte(`{"status":"ok","url":"https://example.com"}`))
	}))
	defer srv.Close()
	c := NewClientWithBaseURL(srv.URL, "secret")
	if _, err := c.get("/v0/management/anthropic-auth-url"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := c.get("/v0/management/get-auth-status?state=x"); err != nil {
		t.Fatalf("get: %v", err)
	}
	for i, v := range got {
		if v != "1" {
			t.Fatalf("request %d sent X-Taproot-Request %q, want 1", i, v)
		}
	}
	if len(got) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(got))
	}
}
