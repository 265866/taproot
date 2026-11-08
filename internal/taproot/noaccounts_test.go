package taproot

import (
	"net/http"
	"net/http/httptest"
	"testing"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
)

func noAccountsEngine(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	n := &noAccounts{}
	n.attach(coreauth.NewManager(nil, nil, nil))
	e := gin.New()
	e.Use(n.middleware())
	e.Any("/x", handler)
	return e
}

// A streamed success reaches the client chunk by chunk, before the handler returns.
func TestNoAccountsPassesStreamsThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	e := noAccountsEngine(func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString("data: one\n\n")
		c.Writer.Flush()
		if !rec.Flushed || rec.Body.String() != "data: one\n\n" {
			t.Errorf("first chunk held back: flushed %t, body %q", rec.Flushed, rec.Body)
		}
		_, _ = c.Writer.WriteString("data: two\n\n")
	})
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "data: one\n\ndata: two\n\n" {
		t.Fatalf("stream = %d %q", rec.Code, rec.Body)
	}
}

func TestNoAccountsLeavesOtherResponsesAlone(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		status       int
		body         string
	}{
		{"another 400", http.MethodPost, http.StatusBadRequest, `{"error":{"message":"messages: field required"}}`},
		{"not JSON", http.MethodPost, http.StatusBadRequest, "unknown provider for model x"},
		{"a 404", http.MethodPost, http.StatusNotFound, `{"error":{"message":"unknown provider for model x"}}`},
		{"a GET", http.MethodGet, http.StatusBadRequest, `{"error":{"message":"unknown provider for model x"}}`},
		{"empty 400", http.MethodPost, http.StatusBadRequest, ""},
	} {
		e := noAccountsEngine(func(c *gin.Context) {
			c.Header("X-Upstream", "kept")
			c.Data(tc.status, "application/json", []byte(tc.body))
		})
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(tc.method, "/x", nil))
		if rec.Code != tc.status || rec.Body.String() != tc.body || rec.Header().Get("X-Upstream") != "kept" {
			t.Errorf("%s: %d %q %v, want %d %q unchanged", tc.name, rec.Code, rec.Body, rec.Header(), tc.status, tc.body)
		}
	}
}

// A handler that sets 400 and then another status before writing gets the later status.
func TestNoAccountsFollowsAChangedStatus(t *testing.T) {
	e := noAccountsEngine(func(c *gin.Context) {
		c.Status(http.StatusBadRequest)
		c.Status(http.StatusAccepted)
		_, _ = c.Writer.WriteString("ok")
	})
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rec.Code != http.StatusAccepted || rec.Body.String() != "ok" {
		t.Fatalf("= %d %q, want 202 ok", rec.Code, rec.Body)
	}
}

// Before the server attaches its auth manager, nothing is rewritten.
func TestNoAccountsWithoutManager(t *testing.T) {
	if (&noAccounts{}).poolEmpty() {
		t.Fatal("poolEmpty() = true without an auth manager")
	}
}
