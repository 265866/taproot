package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(Middleware())
	// Upstream owns these routes; the dashboard must win on "/" only.
	engine.GET("/", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"message": "CLI Proxy API Server"}) })
	engine.POST("/", func(c *gin.Context) { c.String(http.StatusTeapot, "upstream post") })
	engine.GET("/v1/models", func(c *gin.Context) { c.String(http.StatusOK, "models") })
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	return srv
}

func fetch(t *testing.T, method, url string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestRootServesEmbeddedDashboard(t *testing.T) {
	want, err := os.ReadFile("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(t)

	resp, body := fetch(t, http.MethodGet, srv.URL+"/")
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, want) {
		t.Fatalf("GET /: %d, body matches dist/index.html: %v", resp.StatusCode, bytes.Equal(body, want))
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
		t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", got)
	}

	resp, body = fetch(t, http.MethodHead, srv.URL+"/")
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("HEAD /: %d with %d body bytes", resp.StatusCode, len(body))
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(want)) {
		t.Errorf("HEAD Content-Length = %q, want %d", got, len(want))
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("HEAD Cache-Control = %q", got)
	}
}

func TestOtherRequestsPassThrough(t *testing.T) {
	srv := newServer(t)
	for _, tc := range []struct {
		method, path string
		code         int
		body         string
	}{
		{http.MethodGet, "/v1/models", http.StatusOK, "models"},
		{http.MethodPost, "/", http.StatusTeapot, "upstream post"},
		{http.MethodGet, "/index.html", http.StatusNotFound, "404 page not found"},
	} {
		resp, body := fetch(t, tc.method, srv.URL+tc.path)
		if resp.StatusCode != tc.code || string(body) != tc.body {
			t.Errorf("%s %s: %d %q, want %d %q", tc.method, tc.path, resp.StatusCode, body, tc.code, tc.body)
		}
	}
}
