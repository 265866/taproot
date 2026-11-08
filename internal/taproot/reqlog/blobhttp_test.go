package reqlog

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/gin-gonic/gin"
)

// htmlPayload is a non-image blob a browser would render as a page if it
// were served with a sniffed type.
var htmlPayload = append([]byte("<!DOCTYPE html><html><script>alert(document.domain)</script>"), bytes.Repeat([]byte(" "), 2048)...)

func blobURL(b []byte) string {
	h := sha256.Sum256(b)
	return "/request-logs/blobs/" + hex.EncodeToString(h[:])
}

// TestBlobAndDeferredRoutes reads a record with images as the dashboard does:
// placeholders, upstream sections deferred and fetched separately, and each
// payload from the blob route.
func TestBlobAndDeferredRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newHarness(t, Config{})
	jpg := testJPEG(t, 7)
	body := imageTurn([][]byte{jpg, htmlPayload})
	logSimple(t, h.logs.logger, "img", "/v1/messages", "sess", 200, h.clock.now(), body)
	h.flush()

	engine := gin.New()
	h.logs.Routes(engine.Group("/taproot/api/v1"))
	srv := httptest.NewServer(engine)
	defer srv.Close()
	get := func(path string) (int, http.Header, []byte) {
		t.Helper()
		resp, err := http.Get(srv.URL + "/taproot/api/v1" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header, raw
	}
	decode := func(path string, into any) {
		t.Helper()
		status, _, raw := get(path)
		if status != http.StatusOK || json.Unmarshal(raw, into) != nil {
			t.Fatalf("%s: %d %.300s", path, status, raw)
		}
	}

	var full, ref, deferred Detail
	decode("/request-logs/img", &full)
	decode("/request-logs/img?blobs=ref", &ref)
	decode("/request-logs/img?blobs=ref&upstream=defer", &deferred)
	if full.Request.Body != string(body) || full.Blobs != nil {
		t.Fatalf("full detail body differs or carries blobs: %v", full.Blobs)
	}
	jpgHash, htmlHash := sha256.Sum256(jpg), sha256.Sum256(htmlPayload)
	wantBlobs := map[string]BlobInfo{
		hex.EncodeToString(jpgHash[:]):  {Bytes: len(jpg), Type: "image/jpeg"},
		hex.EncodeToString(htmlHash[:]): {Bytes: len(htmlPayload)},
	}
	for hash, want := range wantBlobs {
		if ref.Blobs[hash] != want || !strings.Contains(ref.Request.Body, "[taproot-blob:"+hash+":") {
			t.Fatalf("blob %s: %+v in the detail, want %+v", hash[:8], ref.Blobs[hash], want)
		}
	}
	if len(ref.Request.Body) >= len(body)/2 || strings.Contains(ref.Request.Body, base64.StdEncoding.EncodeToString(jpg)) {
		t.Fatalf("ref body still carries payloads: %d bytes of %d", len(ref.Request.Body), len(body))
	}

	u := deferred.Upstream
	// The deferred sizes are the original sections', images inline.
	if u.Request != "" || u.Response != "" || u.RequestBytes != len(full.Upstream.Request) || u.ResponseBytes != len(full.Upstream.Response) ||
		deferred.Request.Body != ref.Request.Body || u.RequestBytes <= len(ref.Upstream.Request) {
		t.Fatalf("deferred upstream: %+v", u)
	}
	var upRef, upFull UpstreamSections
	decode("/request-logs/img/upstream?blobs=ref", &upRef)
	decode("/request-logs/img/upstream", &upFull)
	if upRef.Request != ref.Upstream.Request || upRef.Response != ref.Upstream.Response || len(upRef.Blobs) != len(wantBlobs) {
		t.Fatalf("upstream with blobs=ref differs from the ref detail")
	}
	if upFull.Request != full.Upstream.Request || upFull.Response != full.Upstream.Response || upFull.Blobs != nil {
		t.Fatalf("upstream differs from the full detail")
	}

	for _, c := range []struct {
		payload     []byte
		contentType string
		attachment  bool
	}{
		{jpg, "image/jpeg", false},
		{htmlPayload, "application/octet-stream", true},
	} {
		status, header, raw := get(blobURL(c.payload))
		if status != http.StatusOK || !bytes.Equal(raw, c.payload) {
			t.Fatalf("blob %s: %d, %d bytes", c.contentType, status, len(raw))
		}
		if header.Get("Content-Type") != c.contentType || header.Get("X-Content-Type-Options") != "nosniff" || header.Get("Cross-Origin-Resource-Policy") != "same-origin" ||
			header.Get("Cache-Control") != "private, max-age=31536000, immutable" ||
			strings.HasPrefix(header.Get("Content-Disposition"), "attachment") != c.attachment {
			t.Fatalf("blob %s headers: %v", c.contentType, header)
		}
	}

	for path, want := range map[string]int{
		blobURL([]byte("never stored")):  http.StatusNotFound,
		"/request-logs/blobs/zz":         http.StatusBadRequest,
		"/request-logs/blobs/" + "abcd":  http.StatusBadRequest,
		"/request-logs/missing/upstream": http.StatusNotFound,
	} {
		status, header, raw := get(path)
		var e struct {
			Error struct{ Code string } `json:"error"`
		}
		if status != want || json.Unmarshal(raw, &e) != nil || e.Error.Code == "" || header.Get("Cache-Control") != "" {
			t.Fatalf("%s: %d %s %v", path, status, raw, header)
		}
	}
}

// TestBlobRouteIsAdminOnly mounts the routes behind the access guard, as the
// server does: an access.addresses caller reaches the proxy surface but not
// the blob route, and loopback reaches both.
func TestBlobRouteIsAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("MANAGEMENT_PASSWORD", "test-secret")
	h := newHarness(t, Config{})
	jpg := testJPEG(t, 8)
	logSimple(t, h.logs.logger, "img", "/v1/messages", "sess", 200, h.clock.now(), imageTurn([][]byte{jpg}))
	h.flush()

	guard, err := access.New(access.Config{
		Addresses:       []access.AddressGrant{{CIDR: "10.9.8.7/32"}},
		TailscaleSocket: filepath.Join(t.TempDir(), "no-tailscaled.sock"),
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.Use(guard.Middleware())
	h.logs.Routes(engine.Group("/taproot/api/v1"))
	engine.GET("/v1/models", func(c *gin.Context) { c.Status(http.StatusOK) })

	serve := func(remote, path string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr, r.Host = remote, "localhost:8317"
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w.Code
	}
	blob := "/taproot/api/v1" + blobURL(jpg)
	for _, c := range []struct {
		remote, path string
		want         int
	}{
		{"10.9.8.7:40000", "/v1/models", http.StatusOK},
		{"10.9.8.7:40000", blob, http.StatusForbidden},
		{"10.9.8.7:40000", "/taproot/api/v1/request-logs/img/upstream", http.StatusForbidden},
		{"127.0.0.1:40000", blob, http.StatusOK},
	} {
		if got := serve(c.remote, c.path); got != c.want {
			t.Errorf("%s %s: %d, want %d", c.remote, c.path, got, c.want)
		}
	}
}
