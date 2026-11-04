package reqlog

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/api/middleware"
	"github.com/265866/taproot/internal/logging"
	"github.com/gin-gonic/gin"
)

// TestThroughUpstreamMiddleware runs real HTTP requests through upstream's
// request logging middleware with taproot's logger installed, the same way
// the server wires the factory's logger.
func TestThroughUpstreamMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newHarness(t, Config{})
	lg := h.logs.logger

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		logging.SetGinRequestID(c, c.GetHeader("X-Test-Id"))
		c.Next()
	})
	engine.Use(middleware.RequestLoggingMiddleware(lg))
	engine.POST("/v1/messages", func(c *gin.Context) {
		middleware.CaptureAuthenticatedRequestBody(c)
		body, _ := io.ReadAll(c.Request.Body)
		c.Set("API_REQUEST", []byte("=== API REQUEST 1 ===\nHeaders:\nAuthorization: Bearer "+secretToken+"\n\nBody:\n"+string(body)+"\n\n"))
		if strings.Contains(string(body), `"stream":true`) {
			c.Header("Content-Type", "text/event-stream")
			c.Status(http.StatusOK)
			for _, event := range []string{"event: message_start\ndata: {}\n\n", "event: message_stop\ndata: {}\n\n"} {
				_, _ = c.Writer.WriteString(event)
				c.Writer.Flush()
			}
			return
		}
		if strings.Contains(string(body), "fail") {
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": "msg_1"})
	})
	srv := httptest.NewServer(engine)
	defer srv.Close()

	send := func(method, path, id, body string) int {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Test-Id", id)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+secretToken)
		req.Header.Set("X-Claude-Code-Session-Id", "mw-session")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	lg.SetEnabled(true)
	send(http.MethodPost, "/v1/messages", "mw-stream", `{"model":"claude-opus-5-5","stream":true}`)
	send(http.MethodPost, "/v1/messages", "mw-json", `{"model":"claude-opus-5-5"}`)
	if status := send(http.MethodHead, "/api/hello", "mw-hello", ""); status != http.StatusNotFound {
		t.Fatalf("HEAD /api/hello returned %d", status)
	}
	lg.SetEnabled(false)
	send(http.MethodPost, "/v1/messages", "mw-off-ok", `{"model":"claude-opus-5-5"}`)
	send(http.MethodPost, "/v1/messages", "mw-off-fail", `{"model":"claude-opus-5-5","note":"fail"}`)
	h.flush()

	ctx := context.Background()
	stream, err := h.logs.Get(ctx, "mw-stream")
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Meta.Streaming || stream.Response.Body != "event: message_start\ndata: {}\n\nevent: message_stop\ndata: {}\n\n" ||
		stream.Request.Body != `{"model":"claude-opus-5-5","stream":true}` || stream.Meta.SessionID != "claude:mw-session" {
		t.Fatalf("streaming record: %+v", stream)
	}
	plain, err := h.logs.Get(ctx, "mw-json")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Meta.Streaming || plain.Response.Body != `{"id":"msg_1"}` || plain.Response.Status != 200 {
		t.Fatalf("json record: %+v", plain)
	}
	for _, d := range []*Detail{stream, plain} {
		_, raw, err := h.logs.Raw(ctx, d.Meta.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secretToken) {
			t.Fatalf("%s stored a credential", d.Meta.RequestID)
		}
	}
	for _, id := range []string{"mw-hello", "mw-off-ok"} {
		if _, err := h.logs.Get(ctx, id); err == nil {
			t.Fatalf("%s should not be stored", id)
		}
	}
	failed, err := h.logs.Get(ctx, "mw-off-fail")
	if err != nil {
		t.Fatalf("error with request-log off should be stored: %v", err)
	}
	if !failed.Meta.ErrorOnly || failed.Meta.Status != http.StatusBadGateway {
		t.Fatalf("error-only record: %+v", failed.Meta)
	}
}
