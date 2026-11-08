package reqlog

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/logging"
)

const secretToken = "sk-ant-oat01-SECRETSECRETSECRET"

type sentRequest struct {
	id          string
	session     string
	streaming   bool
	status      int
	reqHeaders  map[string][]string
	reqBody     []byte
	respHeaders map[string][]string
	respBody    []byte
	apiRequest  []byte
	apiResponse []byte
	apiErrors   []*interfaces.ErrorMessage
}

func conversation(session string, turn int) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"claude-opus-5-5","stream":true,"messages":[`)
	for i := 0; i <= turn; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"role":"user","content":"session %s turn %d: %s"}`, session, i, strings.Repeat("lorem ipsum ", 40))
	}
	b.WriteString("]}")
	return []byte(b.String())
}

func apiRequestText(body []byte) []byte {
	return []byte("=== API REQUEST 1 ===\nTimestamp: 2026-11-02T12:00:00Z\nUpstream URL: https://api.anthropic.com/v1/messages\nHTTP Method: POST\n\nHeaders:\nAuthorization: Bearer " +
		secretToken + "\nContent-Type: application/json\n\nBody:\n" + string(body) + "\n\n")
}

func apiResponseText(body string) []byte {
	return []byte("=== API RESPONSE 1 ===\nTimestamp: 2026-11-02T12:00:01Z\n\nStatus: 200\nHeaders:\nContent-Type: text/event-stream\nSet-Cookie: __cf_bm=" +
		secretToken + "\n\nBody:\n" + body + "\n")
}

func sseChunks(id string, n int) [][]byte {
	chunks := make([][]byte, n)
	for i := range chunks {
		chunks[i] = []byte(fmt.Sprintf("event: content_block_delta\ndata: {\"id\":%q,\"index\":%d,\"text\":\"%s\"}\n\n", id, i, strings.Repeat("x", i%17)))
	}
	return chunks
}

func redactedExpect(b []byte) string {
	return strings.ReplaceAll(strings.ReplaceAll(string(b), "Bearer "+secretToken, redacted), "__cf_bm="+secretToken, redacted)
}

// TestRoundTripInterleavedSessions writes interleaved streaming and
// non-streaming requests from several sessions concurrently and reads every
// one back exactly, first from open segments and again after sealing.
func TestRoundTripInterleavedSessions(t *testing.T) {
	h := newHarness(t, Config{})
	lg := h.logs.logger
	sessions := []string{"0b31aa90-0000-4000-8000-000000000001", "55de8aa8-0000-4000-8000-000000000002", "ed6af0a7-0000-4000-8000-000000000003", ""}

	var sent []*sentRequest
	for i := 0; i < 64; i++ {
		session := sessions[i%len(sessions)]
		req := &sentRequest{
			id:        fmt.Sprintf("0190a0b0-0000-7000-8000-%012d", i),
			session:   session,
			streaming: i%3 != 0,
			status:    200,
			reqHeaders: map[string][]string{
				"Content-Type":  {"application/json"},
				"Authorization": {"Bearer " + secretToken},
				"Cookie":        {"sid=" + secretToken},
				"X-Api-Key":     {secretToken},
			},
			reqBody: conversation(session, i/len(sessions)),
		}
		if session != "" {
			req.reqHeaders["X-Claude-Code-Session-Id"] = []string{session}
		}
		req.apiRequest = apiRequestText(req.reqBody)
		req.apiResponse = apiResponseText(fmt.Sprintf("event: message_stop\ndata: {\"n\":%d}", i))
		if i%5 == 4 {
			req.status = 529
		}
		if i%5 == 4 && !req.streaming {
			// The streaming writer interface has no error channel.
			req.apiErrors = []*interfaces.ErrorMessage{{StatusCode: 529, Error: errors.New(`{"type":"error","error":{"type":"overloaded_error"}}`)}}
		}
		req.respHeaders = map[string][]string{
			"Content-Type":    {"text/event-stream"},
			"Set-Cookie":      {"session=" + secretToken},
			"X-Cpa-Trace-Id":  {"20261102120000-acct" + fmt.Sprint(i%2) + "-" + req.id},
			"X-Cpa-Version":   {"test"},
			"Cache-Control":   {"no-cache"},
			"Content-Length":  {"0"},
			"X-Request-Index": {fmt.Sprint(i)},
		}
		if !req.streaming {
			req.respHeaders["Content-Type"] = []string{"application/json"}
		}
		sent = append(sent, req)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, len(sent))
	for i, req := range sent {
		wg.Add(1)
		go func(i int, req *sentRequest) {
			defer wg.Done()
			<-start
			if !req.streaming {
				respBody := []byte(fmt.Sprintf(`{"id":"msg_%d","content":[{"type":"text","text":"%s"}]}`, i, strings.Repeat("answer ", i)))
				req.respBody = respBody
				headers := req.respHeaders
				payload := respBody
				if i%2 == 0 {
					var gz bytes.Buffer
					zw := gzip.NewWriter(&gz)
					_, _ = zw.Write(respBody)
					_ = zw.Close()
					payload = gz.Bytes()
					headers = cloneHeaders(headers)
					headers["Content-Encoding"] = []string{"gzip"}
					req.respHeaders = headers
				}
				var apiReqSource *logging.FileBodySource
				apiReq := req.apiRequest
				if i%4 == 0 {
					src, err := lg.NewFileBodySource("api-request")
					if err != nil {
						errs <- err
						return
					}
					if err := src.AppendBytes(req.apiRequest); err != nil {
						errs <- err
						return
					}
					apiReqSource, apiReq = src, nil
				}
				errs <- lg.LogRequestWithOptionsAndAllSources("/v1/messages?beta=true", "POST", req.reqHeaders, req.reqBody, req.status, headers, payload,
					nil, nil, apiReq, apiReqSource, req.apiResponse, nil, nil, nil, req.apiErrors, false, req.id, h.clock.now(), h.clock.now())
				return
			}
			w, err := lg.LogStreamingRequest("/v1/messages?beta=true", "POST", req.reqHeaders, req.reqBody, req.id)
			if err != nil {
				errs <- err
				return
			}
			_ = w.WriteStatus(req.status, req.respHeaders)
			var body bytes.Buffer
			for _, chunk := range sseChunks(req.id, 20+i) {
				w.WriteChunkAsync(chunk)
				body.Write(chunk)
				runtime.Gosched()
			}
			req.respBody = body.Bytes()
			_ = w.WriteAPIRequest(req.apiRequest)
			_ = w.WriteAPIResponse(req.apiResponse)
			w.SetFirstChunkTimestamp(h.clock.now())
			errs <- w.Close()
		}(i, req)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("log request: %v", err)
		}
	}
	h.flush()

	verifyAll := func(stage string) {
		t.Helper()
		ctx := context.Background()
		for _, req := range sent {
			d, err := h.logs.Get(ctx, req.id, true)
			if err != nil {
				t.Fatalf("%s: get %s: %v", stage, req.id, err)
			}
			if d.Request.Body != string(req.reqBody) {
				t.Fatalf("%s: %s request body differs", stage, req.id)
			}
			if d.Response.Body != string(req.respBody) {
				t.Fatalf("%s: %s response body differs:\n got %q\nwant %q", stage, req.id, d.Response.Body, req.respBody)
			}
			if d.Response.Status != req.status || d.Meta.Status != req.status {
				t.Fatalf("%s: %s status %d/%d, want %d", stage, req.id, d.Response.Status, d.Meta.Status, req.status)
			}
			if d.Upstream.Request != redactedExpect(req.apiRequest) {
				t.Fatalf("%s: %s upstream request differs:\n%s", stage, req.id, d.Upstream.Request)
			}
			if d.Upstream.Response != redactedExpect(req.apiResponse) {
				t.Fatalf("%s: %s upstream response differs:\n%s", stage, req.id, d.Upstream.Response)
			}
			if len(req.apiErrors) != len(d.Upstream.Errors) || (len(req.apiErrors) == 1 && d.Upstream.Errors[0].Message != req.apiErrors[0].Error.Error()) {
				t.Fatalf("%s: %s upstream errors %+v", stage, req.id, d.Upstream.Errors)
			}
			for _, name := range []string{"Authorization", "Cookie", "X-Api-Key"} {
				if got := d.Request.Headers[name]; len(got) != 1 || got[0] != redacted {
					t.Fatalf("%s: %s header %s = %v", stage, req.id, name, got)
				}
			}
			if got := d.Response.Headers["Set-Cookie"]; len(got) != 1 || got[0] != redacted {
				t.Fatalf("%s: %s Set-Cookie = %v", stage, req.id, got)
			}
			wantSession := ""
			if req.session != "" {
				wantSession = "claude:" + req.session
			}
			if d.Meta.SessionID != wantSession || d.Meta.Model != "claude-opus-5-5" || d.Meta.Streaming != req.streaming {
				t.Fatalf("%s: %s meta %+v", stage, req.id, d.Meta)
			}
			if !strings.HasPrefix(d.Meta.AuthIndex, "acct") {
				t.Fatalf("%s: %s auth index %q", stage, req.id, d.Meta.AuthIndex)
			}
			_, raw, err := h.logs.Raw(ctx, req.id)
			if err != nil {
				t.Fatalf("%s: raw %s: %v", stage, req.id, err)
			}
			if bytes.Contains(raw, []byte(secretToken)) {
				t.Fatalf("%s: %s raw text leaks a credential", stage, req.id)
			}
			if !bytes.HasPrefix(raw, []byte("=== REQUEST INFO ===\n")) || !bytes.Contains(raw, req.reqBody) || !bytes.Contains(raw, req.respBody) {
				t.Fatalf("%s: %s raw text incomplete", stage, req.id)
			}
		}
	}
	verifyAll("open")
	if states := h.segmentStates(); states[segmentOpen] != len(sessions) {
		t.Fatalf("want one open segment per session, got %v", states)
	}

	h.sealAll()
	if states := h.segmentStates(); states[segmentSealed] != len(sessions) || len(states) != 1 {
		t.Fatalf("want all segments sealed, got %v", states)
	}
	matches, _ := filepath.Glob(filepath.Join(h.dataDir, "request-logs", "*", "*.raw"))
	if len(matches) != 0 {
		t.Fatalf("raw files left after sealing: %v", matches)
	}
	verifyAll("sealed")

	seen := map[string]bool{}
	cursor := ""
	for page := 0; ; page++ {
		logs, next, err := h.logs.List(context.Background(), ListFilter{Limit: 7, Cursor: cursor})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, m := range logs {
			if seen[m.RequestID] {
				t.Fatalf("request %s listed twice", m.RequestID)
			}
			seen[m.RequestID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != len(sent) {
		t.Fatalf("listed %d of %d requests", len(seen), len(sent))
	}
	errorsOnly, _, err := h.logs.List(context.Background(), ListFilter{Status: "5xx", SessionID: "claude:" + sessions[0]})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	for _, m := range errorsOnly {
		if m.Status != 529 || m.SessionID != "claude:"+sessions[0] {
			t.Fatalf("filter returned %+v", m)
		}
	}
	if len(errorsOnly) == 0 {
		t.Fatal("filtered list empty")
	}
}

func cloneHeaders(h map[string][]string) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func logSimple(t *testing.T, lg *requestLogger, id, path, session string, status int, ts time.Time, body []byte) {
	t.Helper()
	headers := map[string][]string{"Content-Type": {"application/json"}}
	if session != "" {
		headers["X-Claude-Code-Session-Id"] = []string{session}
	}
	err := lg.LogRequest(path, "POST", headers, body, status, map[string][]string{"Content-Type": {"application/json"}}, []byte(`{"ok":true}`),
		nil, apiRequestText(body), nil, nil, nil, id, ts, ts)
	if err != nil {
		t.Fatalf("log %s: %v", id, err)
	}
}

func TestRetentionByAgeAndBytes(t *testing.T) {
	h := newHarness(t, Config{MaxDays: 3, MaxBytes: -1})
	now := h.clock.now()
	body := bytes.Repeat([]byte("incompressible? no, but large enough "), 200)
	for day := 0; day < 6; day++ {
		ts := now.Add(-time.Duration(day)*24*time.Hour - time.Minute)
		for i := 0; i < 3; i++ {
			logSimple(t, h.logs.logger, fmt.Sprintf("d%d-r%d", day, i), "/v1/messages", fmt.Sprintf("sess-%d", i), 200, ts, body)
		}
	}
	h.flush()
	h.sealAll()
	if got := h.segmentStates()[segmentSealed]; got != 18 {
		t.Fatalf("want 18 sealed segments, got %d", got)
	}

	h.logs.engine.applyRetention(context.Background())
	stats, err := h.logs.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Records sit one minute past each day mark, so days 0-2 are inside the
	// three-day window and day 3 is just outside it.
	if stats.Count != 9 || stats.Segments != 9 {
		t.Fatalf("after age retention: %+v", stats)
	}
	for day := 0; day < 6; day++ {
		_, err := h.logs.Get(context.Background(), fmt.Sprintf("d%d-r0", day), true)
		if day < 3 && err != nil {
			t.Fatalf("day %d should remain: %v", day, err)
		}
		if day >= 3 && !errors.Is(err, errNotFound) {
			t.Fatalf("day %d should be gone, got %v", day, err)
		}
	}
	files, _ := filepath.Glob(filepath.Join(h.dataDir, "request-logs", "*", "*.zst"))
	if len(files) != 9 {
		t.Fatalf("want 9 segment files on disk, got %d", len(files))
	}

	// Keep only what fits in roughly four segments: the oldest go first.
	var perSegment int64
	if err := h.db.QueryRow(`SELECT MAX(disk_bytes) FROM reqlog_segments`).Scan(&perSegment); err != nil {
		t.Fatal(err)
	}
	if err := h.logs.SetRetention(Retention{MaxDays: 3, MaxBytes: ByteSize(perSegment * 4)}); err != nil {
		t.Fatal(err)
	}
	h.logs.engine.applyRetention(context.Background())
	stats, err = h.logs.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.BytesOnDisk > perSegment*4 || stats.Segments < 3 || stats.Segments > 4 {
		t.Fatalf("after size retention: %+v (per segment %d)", stats, perSegment)
	}
	if _, err := h.logs.Get(context.Background(), "d0-r0", true); err != nil {
		t.Fatalf("newest day must survive size retention: %v", err)
	}
	if _, err := h.logs.Get(context.Background(), "d2-r0", true); !errors.Is(err, errNotFound) {
		t.Fatalf("oldest day must go first, got %v", err)
	}
	files, _ = filepath.Glob(filepath.Join(h.dataDir, "request-logs", "*", "*.zst"))
	if int64(len(files)) != stats.Segments {
		t.Fatalf("files on disk %d, segments %d", len(files), stats.Segments)
	}
}

func TestExclusions(t *testing.T) {
	h := newHarness(t, Config{ExcludePaths: []string{"GET /healthz", "/v1/models*"}})
	lg := h.logs.logger
	ts := h.clock.now()
	// Scanner noise: no route, so no upstream request was recorded.
	if err := lg.LogRequest("/api/hello", "HEAD", nil, nil, 404, nil, nil, nil, nil, nil, nil, nil, "unrouted", ts, ts); err != nil {
		t.Fatal(err)
	}
	// An upstream 404 is a real answer and is kept.
	logSimple(t, lg, "upstream-404", "/v1/messages", "", 404, ts, []byte(`{"model":"x"}`))
	logSimple(t, lg, "models", "/v1/models?x=1", "", 200, ts, []byte(`{}`))
	if err := lg.LogRequest("/healthz", "GET", nil, nil, 200, nil, nil, nil, nil, nil, nil, nil, "health", ts, ts); err != nil {
		t.Fatal(err)
	}
	if err := lg.LogRequest("/healthz", "POST", nil, nil, 200, nil, nil, nil, nil, nil, nil, nil, "health-post", ts, ts); err != nil {
		t.Fatal(err)
	}
	h.flush()
	logSimple(t, lg, "taproot-settings", "/taproot/api/v1/settings", "", 200, ts, []byte(`{"access":{}}`))
	for id, want := range map[string]bool{"unrouted": false, "upstream-404": true, "models": false, "health": false, "health-post": true, "taproot-settings": false} {
		_, err := h.logs.Get(context.Background(), id, true)
		if want && err != nil {
			t.Fatalf("%s should be stored: %v", id, err)
		}
		if !want && !errors.Is(err, errNotFound) {
			t.Fatalf("%s should be excluded, got %v", id, err)
		}
	}
	if _, err := newLogs(context.Background(), Config{ExcludePaths: []string{"no-slash"}}, h.db, t.TempDir(), h.clock.now); err == nil {
		t.Fatal("invalid exclude path accepted")
	}
}

func TestErrorOnlyWhenDisabled(t *testing.T) {
	h := newHarness(t, Config{})
	lg := h.logs.logger
	lg.SetEnabled(false)
	ts := h.clock.now()
	logSimple(t, lg, "plain", "/v1/messages", "", 200, ts, []byte(`{}`))
	if err := lg.LogRequestWithOptions("/v1/messages", "POST", nil, []byte(`{"model":"m"}`), 500, nil, []byte(`boom`), nil, []byte("=== API REQUEST 1 ===\nx\n"), nil, nil,
		[]*interfaces.ErrorMessage{{StatusCode: 500, Error: errors.New("upstream exploded")}}, true, "forced", ts, ts); err != nil {
		t.Fatal(err)
	}
	w, err := lg.LogStreamingRequest("/v1/messages", "POST", nil, nil, "stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := w.(*logging.NoOpStreamingLogWriter); !ok {
		t.Fatalf("disabled streaming writer is %T", w)
	}
	h.flush()
	if _, err := h.logs.Get(context.Background(), "plain", true); !errors.Is(err, errNotFound) {
		t.Fatalf("unforced request stored while disabled: %v", err)
	}
	d, err := h.logs.Get(context.Background(), "forced", true)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Meta.ErrorOnly || d.Meta.Status != 500 || len(d.Upstream.Errors) != 1 || d.Upstream.Errors[0].Message != "upstream exploded" {
		t.Fatalf("forced record %+v", d)
	}

	factory := h.logs.LoggerFactory()
	if got := factory(nil, ""); got.IsEnabled() {
		t.Fatal("nil config must leave logging off")
	}
}

func TestQueueOverflowDropsBodiesWithoutBlocking(t *testing.T) {
	dataDir := t.TempDir()
	db := openStore(t, dataDir)
	t.Cleanup(func() { _ = db.Close() })
	clk := &clock{t: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	logs, err := newLogs(context.Background(), Config{QueueBytes: 64 << 10}, db, dataDir, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	logs.logger.SetEnabled(true)
	lg := logs.logger
	big := bytes.Repeat([]byte("a"), 1<<20)
	headers := map[string][]string{"X-Claude-Code-Session-Id": {"overflow-session"}}

	// The writer is not running, so nothing drains: every call must still
	// return immediately.
	done := make(chan struct{})
	go func() {
		defer close(done)
		ts := clk.now()
		_ = lg.LogRequest("/v1/messages", "POST", headers, big, 200, nil, []byte("ok"), nil, nil, nil, nil, nil, "big", ts, ts)
		w, _ := lg.LogStreamingRequest("/v1/messages", "POST", headers, []byte(`{"model":"m"}`), "stream-big")
		_ = w.WriteStatus(200, map[string][]string{"Content-Type": {"text/event-stream"}})
		for i := 0; i < 64; i++ {
			w.WriteChunkAsync(bytes.Repeat([]byte("b"), 4<<10))
		}
		_ = w.Close()
		for i := 0; i < queueEntries+10; i++ {
			_ = lg.LogRequest("/v1/messages", "POST", nil, nil, 200, nil, nil, nil, nil, nil, nil, nil, fmt.Sprintf("flood-%d", i), ts, ts)
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("logging blocked with a full queue")
	}
	if logs.engine.stats.droppedRecords.Load() == 0 || logs.engine.stats.droppedBodies.Load() == 0 || logs.engine.stats.truncated.Load() != 1 {
		t.Fatalf("drop counters: bodies %d records %d truncated %d", logs.engine.stats.droppedBodies.Load(), logs.engine.stats.droppedRecords.Load(), logs.engine.stats.truncated.Load())
	}

	ctx, cancel := context.WithCancel(context.Background())
	logs.engine.startWriter(ctx)
	defer func() { cancel(); logs.engine.wait() }()
	if err := logs.engine.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, err := logs.Get(context.Background(), "big", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta.Dropped != "log queue full" || !strings.Contains(d.Request.Body, "1048576 bytes dropped") || d.Meta.SessionID != "claude:overflow-session" {
		t.Fatalf("dropped record %+v body %q", d.Meta, d.Request.Body)
	}
	s, err := logs.Get(context.Background(), "stream-big", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Meta.Dropped, "response truncated") || !strings.Contains(s.Response.Body, "bytes dropped, log queue full") {
		t.Fatalf("truncated stream %+v", s.Meta)
	}
	if used := logs.engine.budget.used.Load(); used != 0 {
		t.Fatalf("budget leaked %d bytes", used)
	}
}

// TestCrashRecovery simulates an unclean stop: the index lost its last rows
// and the open segment ends in a torn frame. Restart re-indexes complete
// frames, cuts the tail, seals, and leaves older sealed segments untouched.
func TestCrashRecovery(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	for i := 0; i < 5; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("old-%d", i), "/v1/messages", "s-old", 200, ts, []byte(fmt.Sprintf(`{"model":"m","n":%d}`, i)))
	}
	h.flush()
	h.sealAll()
	sealed, _ := filepath.Glob(filepath.Join(h.dataDir, "request-logs", "*", "*.zst"))
	if len(sealed) != 1 {
		t.Fatalf("want one sealed segment, got %v", sealed)
	}
	before, err := os.ReadFile(sealed[0])
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 6; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("new-%d", i), "/v1/messages", "s-new", 200, ts, []byte(fmt.Sprintf(`{"model":"m","n":%d}`, i)))
	}
	h.flush()
	h.stop()

	raws, _ := filepath.Glob(filepath.Join(h.dataDir, "request-logs", "*", "*.raw"))
	if len(raws) != 1 {
		t.Fatalf("want one open segment, got %v", raws)
	}
	f, err := os.OpenFile(raws[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(frameMagic + "0 500 0 0 9000 0\n{\"requestId\":\"torn\"")
	_ = f.Close()
	if _, err := h.db.Exec(`DELETE FROM reqlog_records WHERE request_id IN ('new-4', 'new-5')`); err != nil {
		t.Fatal(err)
	}

	h.open(Config{})
	h.logs.engine.compactQueued(context.Background())
	for i := 0; i < 6; i++ {
		d, err := h.logs.Get(context.Background(), fmt.Sprintf("new-%d", i), true)
		if err != nil {
			t.Fatalf("new-%d after recovery: %v", i, err)
		}
		if d.Request.Body != fmt.Sprintf(`{"model":"m","n":%d}`, i) {
			t.Fatalf("new-%d body %q", i, d.Request.Body)
		}
	}
	if _, err := h.logs.Get(context.Background(), "torn", true); !errors.Is(err, errNotFound) {
		t.Fatalf("torn frame indexed: %v", err)
	}
	after, err := os.ReadFile(sealed[0])
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("older sealed segment changed (err %v)", err)
	}
	if states := h.segmentStates(); states[segmentSealed] != 2 || len(states) != 1 {
		t.Fatalf("segments after recovery: %v", states)
	}
	// Recording resumes in a fresh segment.
	logSimple(t, h.logs.logger, "after", "/v1/messages", "s-new", 200, ts, []byte(`{}`))
	h.flush()
	if _, err := h.logs.Get(context.Background(), "after", true); err != nil {
		t.Fatal(err)
	}
}

func TestSegmentRollsAtSizeCap(t *testing.T) {
	h := newHarness(t, Config{SegmentMaxBytes: 64 << 10})
	ts := h.clock.now()
	rng := rand.New(rand.NewSource(1))
	bodies := map[string][]byte{}
	for i := 0; i < 20; i++ {
		// Spaces keep the body from being one long base64 run, which would
		// move it to the blob store and keep the segment small.
		body := make([]byte, 16<<10)
		for j := range body {
			body[j] = byte('a' + rng.Intn(26))
			if j%50 == 49 {
				body[j] = ' '
			}
		}
		id := fmt.Sprintf("roll-%d", i)
		bodies[id] = body
		logSimple(t, h.logs.logger, id, "/v1/messages", "one-session", 200, ts, body)
	}
	h.flush()
	h.logs.engine.compactQueued(context.Background())
	states := h.segmentStates()
	if states[segmentSealed] < 4 {
		t.Fatalf("segments did not roll at the size cap: %v", states)
	}
	for id, body := range bodies {
		d, err := h.logs.Get(context.Background(), id, true)
		if err != nil || d.Request.Body != string(body) {
			t.Fatalf("%s after roll: %v", id, err)
		}
	}
}

// TestSealTriggers covers each reason an open segment seals: idle session,
// age, a past day, and the open-bytes budget.
func TestSealTriggers(t *testing.T) {
	h := newHarness(t, Config{SealIdle: 10 * time.Minute, SealAge: time.Hour, OpenMaxBytes: 48 << 10})
	start := h.clock.now()
	body := func(n int) []byte { return bytes.Repeat([]byte("z "), n/2) }
	sealDue := func() {
		t.Helper()
		if err := h.logs.engine.do(context.Background(), func() { h.logs.engine.sealDue(h.clock.now()) }); err != nil {
			t.Fatal(err)
		}
	}
	openSessions := func() map[string]bool {
		t.Helper()
		out := map[string]bool{}
		if err := h.logs.engine.do(context.Background(), func() {
			for _, seg := range h.logs.engine.open {
				out[seg.sessionID] = true
			}
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	logSimple(t, h.logs.logger, "idle-1", "/v1/messages", "idle", 200, start, body(100))
	logSimple(t, h.logs.logger, "busy-1", "/v1/messages", "busy", 200, start, body(100))
	logSimple(t, h.logs.logger, "yesterday-1", "/v1/messages", "old", 200, start.Add(-24*time.Hour), body(100))
	h.flush()

	h.clock.set(start.Add(9 * time.Minute))
	logSimple(t, h.logs.logger, "busy-2", "/v1/messages", "busy", 200, h.clock.now(), body(100))
	h.flush()
	sealDue()
	if got := openSessions(); got["claude:old"] || !got["claude:idle"] || !got["claude:busy"] {
		t.Fatalf("after 9m only the past day should seal: %v", got)
	}

	h.clock.set(start.Add(11 * time.Minute))
	sealDue()
	if got := openSessions(); got["claude:idle"] || !got["claude:busy"] {
		t.Fatalf("after 11m the idle session should seal: %v", got)
	}

	for i := 0; i < 6; i++ {
		h.clock.set(start.Add(time.Duration(50+i) * time.Minute))
		logSimple(t, h.logs.logger, fmt.Sprintf("busy-%d", i+3), "/v1/messages", "busy", 200, h.clock.now(), body(100))
		h.flush()
		sealDue()
	}
	h.clock.set(start.Add(61 * time.Minute))
	sealDue()
	if got := openSessions(); got["claude:busy"] {
		t.Fatalf("a segment open past seal-age should seal: %v", got)
	}

	// Two sessions together pass the 48 KiB open budget; the larger seals.
	logSimple(t, h.logs.logger, "big-1", "/v1/messages", "big", 200, h.clock.now(), body(40<<10))
	logSimple(t, h.logs.logger, "small-1", "/v1/messages", "small", 200, h.clock.now(), body(12<<10))
	h.flush()
	sealDue()
	if got := openSessions(); got["claude:big"] || !got["claude:small"] {
		t.Fatalf("open budget should seal the largest segment: %v", got)
	}

	h.logs.engine.compactQueued(context.Background())
	for _, id := range []string{"idle-1", "busy-1", "busy-8", "yesterday-1", "big-1", "small-1"} {
		if _, err := h.logs.Get(context.Background(), id, true); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
}

func TestSweepRemovesOrphansButNotNewSegments(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	logSimple(t, h.logs.logger, "kept", "/v1/messages", "s", 200, ts, []byte(`{}`))
	h.flush()
	h.sealAll()
	dir := filepath.Join(h.dataDir, "request-logs", utcDay(ts.UnixMilli()))
	var seq int64
	if err := h.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'reqlog_segments'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, fmt.Sprintf("%d.raw", seq-1))
	staleRaw := filepath.Join(dir, fmt.Sprintf("%d.raw", seq))
	future := filepath.Join(dir, fmt.Sprintf("%d.raw", seq+1))
	for _, p := range []string{orphan, staleRaw, future} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h.logs.engine.sweep(context.Background())
	for p, wantGone := range map[string]bool{orphan: true, staleRaw: true, future: false} {
		_, err := os.Stat(p)
		if gone := errors.Is(err, os.ErrNotExist); gone != wantGone {
			t.Fatalf("%s gone=%v, want %v", filepath.Base(p), gone, wantGone)
		}
	}
	if _, err := h.logs.Get(context.Background(), "kept", true); err != nil {
		t.Fatalf("sealed record lost to sweep: %v", err)
	}
}

// TestLifecycle drives the public API the way taproot.Setup does: New, Start,
// the factory's logger, then shutdown and a restart that seals what the first
// run left open.
func TestLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	db := openStore(t, dataDir)
	t.Cleanup(func() { _ = db.Close() })
	logs, err := New(Config{}, db, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	logs.Start(context.Background())
	lg := logs.LoggerFactory()(&config.Config{SDKConfig: config.SDKConfig{RequestLog: true}}, "")
	if !lg.IsEnabled() {
		t.Fatal("request-log: true should enable logging")
	}
	now := time.Now()
	for i := 0; i < 20; i++ {
		if err := lg.LogRequest("/v1/messages", "POST", map[string][]string{"X-Claude-Code-Session-Id": {"life"}}, []byte(fmt.Sprintf(`{"n":%d}`, i)), 200, nil, []byte("ok"),
			nil, []byte("=== API REQUEST 1 ===\n"), nil, nil, nil, fmt.Sprintf("life-%d", i), now, now); err != nil {
			t.Fatal(err)
		}
	}
	logs.Close()

	again, err := New(Config{}, db, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	again.Start(context.Background())
	defer again.Close()
	deadline := time.Now().Add(30 * time.Second)
	for {
		stats, err := again.Stats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if stats.Count == 20 && stats.OpenSegments == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restart did not seal the leftover segment: %+v", stats)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		d, err := again.Get(context.Background(), fmt.Sprintf("life-%d", i), true)
		if err != nil || d.Request.Body != fmt.Sprintf(`{"n":%d}`, i) {
			t.Fatalf("life-%d after restart: %v", i, err)
		}
	}
}

// TestStreamingSpooledSections follows the middleware's order: executors
// spool API sections to sources from NewFileBodySource, the writer reads them
// in Close, and the middleware removes them right after.
func TestStreamingSpooledSections(t *testing.T) {
	h := newHarness(t, Config{})
	lg := h.logs.logger
	body := []byte(`{"model":"claude-opus-5-5"}`)
	reqSrc, err := lg.NewFileBodySource("api-request")
	if err != nil {
		t.Fatal(err)
	}
	resSrc, err := lg.NewFileBodySource("api-response")
	if err != nil {
		t.Fatal(err)
	}
	apiReq := apiRequestText(body)
	apiResp := apiResponseText("event: message_stop\ndata: {}")
	if err := reqSrc.AppendBytes(apiReq); err != nil {
		t.Fatal(err)
	}
	if err := resSrc.AppendBytes(apiResp); err != nil {
		t.Fatal(err)
	}
	w, err := lg.LogStreamingRequest("/v1/messages", "POST", map[string][]string{"X-Claude-Code-Session-Id": {"spool"}}, body, "spooled")
	if err != nil {
		t.Fatal(err)
	}
	sourced, ok := w.(interface {
		WriteAPIRequestSource(*logging.FileBodySource) error
		WriteAPIResponseSource(*logging.FileBodySource) error
	})
	if !ok {
		t.Fatalf("%T does not accept spooled sections", w)
	}
	_ = w.WriteStatus(200, map[string][]string{"Content-Type": {"text/event-stream"}})
	w.WriteChunkAsync([]byte("event: ping\n\n"))
	_ = sourced.WriteAPIRequestSource(reqSrc)
	_ = sourced.WriteAPIResponseSource(resSrc)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	cleanupSources(reqSrc, resSrc)
	h.flush()

	d, err := h.logs.Get(context.Background(), "spooled", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Upstream.Request != redactedExpect(apiReq) || d.Upstream.Response != redactedExpect(apiResp) || d.Response.Body != "event: ping\n\n" {
		t.Fatalf("spooled sections not stored: %+v", d.Upstream)
	}
	left, _ := os.ReadDir(h.logs.engine.tmpDir())
	if len(left) != 0 {
		t.Fatalf("spool files left behind: %d", len(left))
	}
}

func TestBufferedEventStreamIsMarkedStreaming(t *testing.T) {
	h := newHarness(t, Config{})
	lg := h.logs.logger
	ts := h.clock.now()
	body := []byte(`{"model":"claude-x","stream":true}`)
	sse := []byte("event: message_start\ndata: {}\n\n")
	for id, contentType := range map[string]string{"sse": "text/event-stream; charset=utf-8", "json": "application/json"} {
		err := lg.LogRequest("/v1/messages", "POST", map[string][]string{"Content-Type": {"application/json"}}, body, 200,
			map[string][]string{"content-type": {contentType}}, sse, nil, apiRequestText(body), nil, nil, nil, id, ts, ts)
		if err != nil {
			t.Fatalf("log %s: %v", id, err)
		}
	}
	h.flush()
	for id, want := range map[string]bool{"sse": true, "json": false} {
		d, err := h.logs.Get(context.Background(), id, true)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if d.Meta.Streaming != want {
			t.Errorf("%s streaming = %v, want %v", id, d.Meta.Streaming, want)
		}
	}
}
