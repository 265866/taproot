package taproot

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/265866/taproot/internal/httpwire"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/logging"
	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/265866/taproot/internal/taproot/store"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/265866/taproot/sdk/cliproxy/usage"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type clientRequestsHarness struct {
	engine *gin.Engine
	sink   *ingest.Ingest
	db     *store.DB
	stop   func()
}

// newClientRequestsHarness serves routes behind upstream's request-ID and
// recovery middleware, then clientRequests and the no-accounts middleware in
// Setup's order.
func newClientRequestsHarness(t *testing.T, routes func(e *gin.Engine, sink *ingest.Ingest)) *clientRequestsHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := store.Open(context.Background(), store.Config{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sink, err := ingest.New(ingest.Config{PollInterval: "0"}, db)
	if err != nil {
		t.Fatal(err)
	}
	sink.AttachProviderGuess(modelProvider)
	ctx, cancel := context.WithCancel(context.Background())
	sink.Start(ctx)
	stopped := sync.OnceFunc(func() {
		cancel()
		sink.Wait()
	})
	t.Cleanup(stopped)
	accounts := &noAccounts{}
	accounts.attach(coreauth.NewManager(nil, nil, nil))
	e := gin.New()
	e.Use(logging.GinLogrusLogger(), logging.GinLogrusRecovery(), clientRequests(sink), accounts.middleware())
	routes(e, sink)
	return &clientRequestsHarness{engine: e, sink: sink, db: db, stop: stopped}
}

func (h *clientRequestsHarness) do(method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.engine.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

type storedRequest struct {
	path, model, provider, code, message string
	status                               int
	failed, stream                       bool
	attempts                             sql.NullInt64
	requestID                            string
}

// rows stops the writer, whose final flush writes every queued request, and
// reads them back.
func (h *clientRequestsHarness) rows(t *testing.T) []storedRequest {
	t.Helper()
	h.stop()
	rows, err := h.db.Query(`SELECT path, model, provider, error_code, error_message, status, failed, stream, attempts, request_id
		FROM client_requests ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []storedRequest
	for rows.Next() {
		var r storedRequest
		if err := rows.Scan(&r.path, &r.model, &r.provider, &r.code, &r.message, &r.status, &r.failed, &r.stream, &r.attempts, &r.requestID); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func okJSON(c *gin.Context) {
	_, _ = c.GetRawData()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func TestClientRequestsRecordModelRoutesOnly(t *testing.T) {
	h := newClientRequestsHarness(t, func(e *gin.Engine, _ *ingest.Ingest) {
		e.POST("/v1/messages", okJSON)
		e.POST("/v1/messages/count_tokens", okJSON)
		e.GET("/v1/models", okJSON)
		e.POST("/v1beta/models/*action", okJSON)
		e.POST("/v0/management/thing", okJSON)
	})
	h.do(http.MethodPost, "/v1/messages", `{"model":"claude-opus-5-5","messages":[]}`)
	h.do(http.MethodPost, "/v1/messages/count_tokens", `{"model":"claude-opus-5-5"}`)
	h.do(http.MethodGet, "/v1/models", "")
	h.do(http.MethodPost, "/v1beta/models/gemini-3-pro:generateContent", `{}`)
	h.do(http.MethodPost, "/v1beta/models/gemini-3-pro:countTokens", `{}`)
	h.do(http.MethodPost, "/v0/management/thing", `{}`)
	h.do(http.MethodPost, "/v1/no-such-route", `{}`)
	got := h.rows(t)
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want /v1/messages and generateContent only", got)
	}
	if got[0].path != "/v1/messages" || got[0].model != "claude-opus-5-5" || got[0].provider != "claude" || got[0].status != 200 ||
		got[0].failed || got[0].requestID == "" || !got[0].attempts.Valid || got[0].attempts.Int64 != 0 {
		t.Fatalf("messages row = %+v", got[0])
	}
	if got[1].path != "/v1beta/models/*action" || got[1].model != "gemini-3-pro" || got[1].provider != "" {
		t.Fatalf("gemini row = %+v", got[1])
	}
}

func TestClientRequestsLinkUsageAttempts(t *testing.T) {
	h := newClientRequestsHarness(t, func(e *gin.Engine, sink *ingest.Ingest) {
		e.POST("/v1/messages", func(c *gin.Context) {
			_, _ = c.GetRawData()
			at := time.Now()
			id := logging.GetGinRequestID(c)
			sink.HandleUsage(c, usage.Record{TraceID: id, Provider: "claude", Model: "claude-sonnet-5-5-20261001", RequestedAt: at,
				Failed: true, Fail: usage.Failure{StatusCode: 429}})
			sink.HandleUsage(c, usage.Record{TraceID: id, Provider: "claude", Model: "claude-sonnet-5-5-20261001", RequestedAt: at.Add(time.Millisecond), Stream: true})
			c.Header("Content-Type", "text/event-stream")
			c.Status(http.StatusOK)
			_, _ = c.Writer.WriteString("event: message_start\ndata: {}\n\n")
			c.Writer.Flush()
		})
	})
	h.do(http.MethodPost, "/v1/messages", `{"model":"claude-sonnet-5-5","stream":true}`)
	got := h.rows(t)
	if len(got) != 1 || got[0].attempts.Int64 != 2 || got[0].model != "claude-sonnet-5-5-20261001" || !got[0].stream || got[0].failed {
		t.Fatalf("rows = %+v, want one stream with 2 attempts and the usage record's model", got)
	}
}

func TestClientRequestsRecordRequestsNoAccountServes(t *testing.T) {
	h := newClientRequestsHarness(t, func(e *gin.Engine, _ *ingest.Ingest) {
		e.POST("/v1/messages", func(c *gin.Context) {
			// Upstream's request logger swaps in its own copy of the body
			// while logging is on, so this middleware never sees it.
			c.Request.Body = io.NopCloser(strings.NewReader(`{"model":"claude-opus-5-5"}`))
			_, _ = c.GetRawData()
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": unknownProvider + "claude-opus-5-5"}})
		})
	})
	rec := h.do(http.MethodPost, "/v1/messages", `{"max_tokens":10,"model":"claude-opus-5-5"}`)
	if !strings.Contains(rec.Body.String(), "no accounts configured") {
		t.Fatalf("client got %q, want the rewritten message", rec.Body)
	}
	got := h.rows(t)
	if len(got) != 1 {
		t.Fatalf("rows = %+v", got)
	}
	r := got[0]
	if !r.failed || r.status != 400 || r.code != codeNoAccounts || r.model != "claude-opus-5-5" || r.provider != "claude" ||
		r.attempts.Int64 != 0 || !strings.HasPrefix(r.message, "no accounts configured") {
		t.Fatalf("row = %+v", r)
	}
}

func TestClientRequestsClassifyEndings(t *testing.T) {
	h := newClientRequestsHarness(t, func(e *gin.Engine, _ *ingest.Ingest) {
		e.POST("/v1/stream-error", func(c *gin.Context) {
			c.Header("Content-Type", "text/event-stream")
			c.Status(http.StatusOK)
			_, _ = c.Writer.WriteString("data: {}\n\n")
			c.Set("API_RESPONSE_ERROR", []*interfaces.ErrorMessage{{StatusCode: 529,
				Error: errors.New(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)}})
		})
		e.POST("/v1/rate-limited", func(c *gin.Context) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "all accounts are cooling down"}})
		})
		e.POST("/v1/gone", func(c *gin.Context) { c.Status(http.StatusOK) })
		e.POST("/v1/closed", func(c *gin.Context) { c.Status(ingest.StatusClientClosed) })
		e.POST("/v1/panic", func(*gin.Context) { panic("boom") })
	})
	h.do(http.MethodPost, "/v1/stream-error", `{}`)
	h.do(http.MethodPost, "/v1/rate-limited", `{}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/gone", strings.NewReader(`{}`)).WithContext(ctx))
	h.do(http.MethodPost, "/v1/closed", `{}`)
	h.do(http.MethodPost, "/v1/panic", `{}`)
	got := h.rows(t)
	want := []struct {
		status        int
		failed        bool
		code, message string
	}{
		{529, true, "overloaded", "Overloaded"},
		{429, true, "rate_limited", "all accounts are cooling down"},
		{ingest.StatusClientClosed, false, "client_closed", ""},
		{ingest.StatusClientClosed, false, "client_closed", ""},
		{500, true, "internal_error", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v", got)
	}
	for k, w := range want {
		if g := got[k]; g.status != w.status || g.failed != w.failed || g.code != w.code || g.message != w.message {
			t.Errorf("%s: row = %+v, want %+v", g.path, g, w)
		}
	}
}

// A streamed chunk reaches the client before the handler returns, and the
// connection's flush errors and controller reach the handler.
func TestClientRequestsPassStreamsThrough(t *testing.T) {
	transport := &goneTransport{ResponseRecorder: httptest.NewRecorder()}
	var flushErr, deadlineErr error
	h := newClientRequestsHarness(t, func(e *gin.Engine, _ *ingest.Ingest) {
		e.POST("/v1/messages", func(c *gin.Context) {
			c.Header("Content-Type", "text/event-stream")
			c.Status(http.StatusOK)
			_, _ = c.Writer.WriteString("data: one\n\n")
			c.Writer.Flush()
			if !transport.Flushed || transport.Body.String() != "data: one\n\n" {
				t.Errorf("first chunk held back: flushed %t, body %q", transport.Flushed, transport.Body)
			}
			flushErr = httpwire.FlushResponse(c.Writer)
			deadlineErr = http.NewResponseController(c.Writer).SetReadDeadline(time.Unix(1, 0))
		})
	})
	h.engine.ServeHTTP(transport, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`)))
	if !errors.Is(flushErr, errClientGone) || deadlineErr != nil || !transport.readDeadline.Equal(time.Unix(1, 0)) {
		t.Fatalf("flush %v, deadline %v (%v)", flushErr, deadlineErr, transport.readDeadline)
	}
	if got := h.rows(t); len(got) != 1 || !got[0].stream {
		t.Fatalf("rows = %+v", got)
	}
}

// While the database is locked, requests still finish: the middleware only
// queues, and the writer catches up once the lock is gone.
func TestClientRequestsDoNotWaitForTheDatabase(t *testing.T) {
	h := newClientRequestsHarness(t, func(e *gin.Engine, _ *ingest.Ingest) { e.POST("/v1/messages", okJSON) })
	conn, err := h.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	// A full batch of usage records wakes the writer, whose flush then waits
	// on the lock while the requests run.
	for range 300 {
		h.sink.HandleUsage(context.Background(), usage.Record{Provider: "claude", Model: "claude-opus-5-5", RequestedAt: time.Now()})
	}
	const n = 200
	started := time.Now()
	for range n {
		if rec := h.do(http.MethodPost, "/v1/messages", `{"model":"claude-opus-5-5"}`); rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	}
	// The store's busy timeout is 10 s; a request that waited on the lock
	// would take at least that.
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("%d requests took %s with the database locked", n, elapsed)
	}
	if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if got := h.rows(t); len(got) != n {
		t.Fatalf("rows = %d, want %d", len(got), n)
	}
	var events int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM usage_events`).Scan(&events); err != nil || events != 300 {
		t.Fatalf("usage events = %d (%v), want 300", events, err)
	}
}

func TestBodyModel(t *testing.T) {
	for body, want := range map[string]string{
		`{"model":"claude-opus-5-5","messages":[]}`:              "claude-opus-5-5",
		`{"messages":[{"content":"hi"}], "model" : "gpt-6-sol"}`: "gpt-6-sol",
		`{"model_reasoning":"x","model":"m1"}`:                   "m1",
		`{"model": 5, "x": {"model": "nested"}}`:                 "nested",
		`{"messages":[]}`:                                        "",
		`{"model":"esc\"aped"}`:                                  "",
		`{"model":"` + strings.Repeat("a", 300) + `"}`:           "",
	} {
		if got := bodyModel([]byte(body)); got != want {
			t.Errorf("bodyModel(%.40q) = %q, want %q", body, got, want)
		}
	}
	// Only the first bytes the handler reads are kept.
	b := &bodyPrefix{ReadCloser: readCloser{bytes.NewReader(bytes.Repeat([]byte("x"), bodyPrefixLimit+100))}}
	buf := make([]byte, 4096)
	for {
		if _, err := b.Read(buf); err != nil {
			break
		}
	}
	if len(b.buf) != bodyPrefixLimit {
		t.Fatalf("kept %d bytes, want %d", len(b.buf), bodyPrefixLimit)
	}
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }

type recentJSON struct {
	DeviceID, Path, Model, Provider string
	Status, Attempts                int
	Failed                          bool
}

// TestClientRequestsThroughTheServer drives the real server: requests from
// devices with Proxy and a Codex websocket turn are recorded with their
// device and attempts, a refused request is not, and the endpoints are
// admin-only.
func TestClientRequestsThroughTheServer(t *testing.T) {
	upstream := fakeUpstream(t)
	h := newProxyServer(t, upstream.URL)
	f := fixture{h: h}
	for _, c := range []struct {
		remote string
		stream bool
	}{{adminIP, false}, {workerIP, true}, {newIP, false}} {
		body := fmt.Sprintf(`{"model":%q,"max_tokens":16,"stream":%t,"messages":[{"role":"user","content":"hi"}]}`, claudeModel, c.stream)
		rec := f.do(req{method: http.MethodPost, remote: c.remote, path: "/v1/messages", body: body, headers: map[string]string{"Content-Type": "application/json"}})
		if want := map[bool]int{true: 403, false: 200}[c.remote == newIP]; rec.Code != want {
			t.Fatalf("from %s = %d, want %d", c.remote, rec.Code, want)
		}
	}
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = adminIP + ":40001"
		h.ServeHTTP(w, r)
	}))
	defer live.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(live.URL, "http")+"/v1/responses", nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer func() { _ = conn.Close() }()
	turn := fmt.Sprintf(`{"type":"response.create","model":%q,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`, codexModel)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(turn)); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket: %v", err)
		}
		if strings.Contains(string(msg), `"response.completed"`) {
			break
		}
	}

	// Rows appear once their usage records settle, about two seconds on.
	var got []recentJSON
	deadline := time.Now().Add(10 * time.Second)
	for len(got) < 3 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		rec := f.do(req{remote: "127.0.0.1", path: "/taproot/api/v1/requests/recent?limit=100"})
		var body struct{ Requests []recentJSON }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %d %.200s: %v", rec.Code, rec.Body, err)
		}
		got = body.Requests
	}
	byKey := map[string]recentJSON{}
	for _, r := range got {
		byKey[r.DeviceID+" "+r.Path] = r
	}
	for key, w := range map[string]recentJSON{
		"node:ndesktopCNTRL /v1/messages":  {Model: claudeModel, Provider: "claude", Status: 200, Attempts: 1},
		"node:nworkerCNTRL /v1/messages":   {Model: claudeModel, Provider: "claude", Status: 200, Attempts: 1},
		"node:ndesktopCNTRL /v1/responses": {Model: codexModel, Provider: "codex", Status: 200, Attempts: 1},
	} {
		g, ok := byKey[key]
		if !ok || g.Model != w.Model || g.Provider != w.Provider || g.Status != w.Status || g.Attempts != w.Attempts || g.Failed {
			t.Errorf("%s = %+v (found %t), want %+v", key, g, ok, w)
		}
	}
	if len(got) != 3 {
		t.Errorf("recorded %d requests, want 3 (the refused one is not a client request): %+v", len(got), got)
	}
	for _, path := range []string{"/taproot/api/v1/requests/summary", "/taproot/api/v1/requests/recent"} {
		if rec := f.do(req{remote: workerIP, path: path}); rec.Code != http.StatusForbidden {
			t.Errorf("%s from a device without Admin = %d, want 403", path, rec.Code)
		}
		if rec := f.do(req{remote: adminIP, path: path}); rec.Code != http.StatusOK {
			t.Errorf("%s from an admin = %d %.200s", path, rec.Code, rec.Body)
		}
	}
}
