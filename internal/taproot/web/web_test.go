package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func newServer(t *testing.T, mw gin.HandlerFunc) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(mw)
	// Upstream owns these routes; the dashboard must win on "/" only.
	engine.GET("/", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"message": "CLI Proxy API Server"}) })
	engine.POST("/", func(c *gin.Context) { c.String(http.StatusTeapot, "upstream post") })
	engine.GET("/v1/models", func(c *gin.Context) { c.String(http.StatusOK, "models") })
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	return srv
}

// fetch sends the request with the given headers and returns the body as
// sent, without the client's transparent gzip handling.
func fetch(t *testing.T, method, url string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "identity")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
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

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRootServesEmbeddedDashboard(t *testing.T) {
	want, err := os.ReadFile("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(t, Middleware())

	resp, body := fetch(t, http.MethodGet, srv.URL+"/")
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, want) {
		t.Fatalf("GET /: %d, body matches dist/index.html: %v", resp.StatusCode, bytes.Equal(body, want))
	}
	for header, value := range map[string]string{
		"Cache-Control":           "no-cache",
		"Content-Type":            "text/html; charset=utf-8",
		"X-Frame-Options":         "DENY",
		"Content-Security-Policy": "frame-ancestors 'none'",
	} {
		if got := resp.Header.Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}

	resp, body = fetch(t, http.MethodHead, srv.URL+"/")
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("HEAD /: %d with %d body bytes", resp.StatusCode, len(body))
	}
	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(len(want)) {
		t.Errorf("HEAD Content-Length = %q, want %d", got, len(want))
	}
}

// A build whose index and chunks compress, like a real one.
var testBuild = fstest.MapFS{
	"dist/index.html":           {Data: []byte(`<!doctype html><script type="module" src="/assets/index-abc.js"></script>` + strings.Repeat("<!-- pad -->", 200))},
	"dist/assets/index-abc.js":  {Data: []byte(strings.Repeat("console.log('taproot');\n", 200))},
	"dist/assets/style-abc.css": {Data: []byte(strings.Repeat("body{color:#fff}\n", 200))},
	"dist/other.txt":            {Data: []byte("not served")},
}

func TestGzipFollowsAcceptEncoding(t *testing.T) {
	srv := newServer(t, handler(testBuild, "dist"))
	want := testBuild["dist/index.html"].Data
	for _, tc := range []struct {
		accept string
		gzip   bool
	}{
		{"gzip, deflate", true},
		{"gzip;q=0.5, identity", true},
		{"*", true},
		{"gzip;q=0", false},
		{"br, zstd", false},
		{"identity", false},
		{"*;q=0, identity", false},
	} {
		resp, body := fetch(t, http.MethodGet, srv.URL+"/", "Accept-Encoding", tc.accept)
		gotGzip := resp.Header.Get("Content-Encoding") == "gzip"
		if gotGzip != tc.gzip {
			t.Errorf("Accept-Encoding %q: gzip = %v, want %v", tc.accept, gotGzip, tc.gzip)
			continue
		}
		if gotGzip {
			body = gunzip(t, body)
		}
		if !bytes.Equal(body, want) {
			t.Errorf("Accept-Encoding %q: body differs from the build", tc.accept)
		}
		if got := resp.Header.Get("Vary"); got != "Accept-Encoding" {
			t.Errorf("Vary = %q", got)
		}
		if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options = %q with Accept-Encoding %q", got, tc.accept)
		}
	}
}

func TestUnchangedBuildRevalidatesTo304(t *testing.T) {
	srv := newServer(t, handler(testBuild, "dist"))
	resp, _ := fetch(t, http.MethodGet, srv.URL+"/", "Accept-Encoding", "gzip")
	etag := resp.Header.Get("ETag")
	if !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("ETag = %q", etag)
	}
	// Either representation's validator matches: the browser keeps whichever it got.
	for _, inm := range []string{etag, strings.TrimPrefix(etag, "W/"), `"other", ` + etag, "*"} {
		resp, body := fetch(t, http.MethodGet, srv.URL+"/", "If-None-Match", inm)
		if resp.StatusCode != http.StatusNotModified || len(body) != 0 {
			t.Errorf("If-None-Match %s: %d with %d bytes, want an empty 304", inm, resp.StatusCode, len(body))
		}
		if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("304 X-Frame-Options = %q", got)
		}
	}
	resp, _ = fetch(t, http.MethodGet, srv.URL+"/", "If-None-Match", `W/"an-older-build"`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a stale validator got %d, want 200", resp.StatusCode)
	}
}

func TestAssetsAreImmutable(t *testing.T) {
	srv := newServer(t, handler(testBuild, "dist"))
	resp, body := fetch(t, http.MethodGet, srv.URL+"/assets/index-abc.js", "Accept-Encoding", "gzip")
	if resp.StatusCode != http.StatusOK || !bytes.Equal(gunzip(t, body), testBuild["dist/assets/index-abc.js"].Data) {
		t.Fatalf("GET chunk: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("chunk Cache-Control = %q", got)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Errorf("chunk Content-Type = %q", got)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("chunk X-Content-Type-Options = %q", got)
	}
	resp, _ = fetch(t, http.MethodGet, srv.URL+"/assets/style-abc.css")
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Errorf("stylesheet Content-Type = %q", got)
	}
	for _, path := range []string{"/assets/missing.js", "/assets/", "/other.txt"} {
		resp, _ := fetch(t, http.MethodGet, srv.URL+path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestOtherRequestsPassThrough(t *testing.T) {
	srv := newServer(t, handler(testBuild, "dist"))
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
