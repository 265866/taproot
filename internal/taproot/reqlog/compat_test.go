package reqlog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

func upstreamLogText(t *testing.T, write func(l *logging.FileRequestLogger)) string {
	t.Helper()
	dir := t.TempDir()
	write(logging.NewFileRequestLogger(true, dir, "", 0))
	files, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil || len(files) != 1 {
		t.Fatalf("upstream log files: %v %v", files, err)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var requestInfoTimestamp = regexp.MustCompile(`(?m)^(=== REQUEST INFO ===\n(?:.*\n)*?)Timestamp: .*\n`)

// TestRawTextMatchesUpstreamLayout feeds identical captures to upstream's
// FileRequestLogger and to taproot, and requires the same text. Headers are
// single-valued and non-sensitive so upstream's random map order and partial
// masking do not differ.
func TestRawTextMatchesUpstreamLayout(t *testing.T) {
	h := newHarness(t, Config{})
	ts := time.Date(2026, 11, 2, 11, 26, 52, 854180923, time.UTC)
	reqHeaders := map[string][]string{"Content-Type": {"application/json"}}
	respHeaders := map[string][]string{"Content-Type": {"application/json"}}
	body := []byte(`{"model":"claude-haiku-5-5","messages":[]}`)
	apiReq := []byte("=== API REQUEST 1 ===\nTimestamp: 2026-11-02T11:26:52Z\nUpstream URL: https://api.anthropic.com/v1/messages\n\nHeaders:\nContent-Type: application/json\n\nBody:\n" + string(body) + "\n\n")
	apiResp := []byte("=== API RESPONSE 1 ===\nTimestamp: 2026-11-02T11:26:53Z\n\nStatus: 200\nHeaders:\nContent-Type: application/json\n\nBody:\n{\"id\":\"msg\"}\n")
	errs := []*interfaces.ErrorMessage{{StatusCode: 529, Error: errors.New("overloaded")}}

	cases := []struct {
		name      string
		id        string
		upstream  func(l *logging.FileRequestLogger)
		taproot   func()
		streaming bool
	}{
		{
			name: "non-streaming with error",
			id:   "0190a0b0-0000-7000-8000-00000000c0a1",
			upstream: func(l *logging.FileRequestLogger) {
				if err := l.LogRequest("/v1/messages?beta=true", "POST", reqHeaders, body, 200, respHeaders, []byte(`{"id":"msg"}`), nil, apiReq, apiResp, nil, errs, "0190a0b0-0000-7000-8000-00000000c0a1", ts, ts.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			},
			taproot: func() {
				if err := h.logs.logger.LogRequest("/v1/messages?beta=true", "POST", reqHeaders, body, 200, respHeaders, []byte(`{"id":"msg"}`), nil, apiReq, apiResp, nil, errs, "0190a0b0-0000-7000-8000-00000000c0a1", ts, ts.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "non-streaming without upstream",
			id:   "0190a0b0-0000-7000-8000-00000000c0a2",
			upstream: func(l *logging.FileRequestLogger) {
				_ = l.LogRequest("/v1/messages", "POST", reqHeaders, nil, 401, respHeaders, []byte("{\"error\":\"no\"}\n"), nil, nil, nil, nil, nil, "0190a0b0-0000-7000-8000-00000000c0a2", ts, time.Time{})
			},
			taproot: func() {
				_ = h.logs.logger.LogRequest("/v1/messages", "POST", reqHeaders, nil, 401, respHeaders, []byte("{\"error\":\"no\"}\n"), nil, nil, nil, nil, nil, "0190a0b0-0000-7000-8000-00000000c0a2", ts, time.Time{})
			},
		},
		{
			name: "websocket transcript",
			id:   "0190a0b0-0000-7000-8000-00000000c0a4",
			upstream: func(l *logging.FileRequestLogger) {
				_ = l.LogRequest("/v1/responses", "GET", map[string][]string{"Upgrade": {"websocket"}}, nil, 101, respHeaders, nil,
					[]byte("> {\"type\":\"response.create\"}\n< {\"type\":\"response.completed\"}"), nil, nil, []byte("> upstream frame\n< upstream reply\n"),
					nil, "0190a0b0-0000-7000-8000-00000000c0a4", ts, time.Time{})
			},
			taproot: func() {
				_ = h.logs.logger.LogRequest("/v1/responses", "GET", map[string][]string{"Upgrade": {"websocket"}}, nil, 101, respHeaders, nil,
					[]byte("> {\"type\":\"response.create\"}\n< {\"type\":\"response.completed\"}"), nil, nil, []byte("> upstream frame\n< upstream reply\n"),
					nil, "0190a0b0-0000-7000-8000-00000000c0a4", ts, time.Time{})
			},
		},
		{
			name:      "streaming",
			id:        "0190a0b0-0000-7000-8000-00000000c0a3",
			streaming: true,
			upstream: func(l *logging.FileRequestLogger) {
				streamThrough(t, l, "0190a0b0-0000-7000-8000-00000000c0a3", reqHeaders, body, apiReq, apiResp)
			},
			taproot: func() {
				streamThrough(t, h.logs.logger, "0190a0b0-0000-7000-8000-00000000c0a3", reqHeaders, body, apiReq, apiResp)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := upstreamLogText(t, tc.upstream)
			tc.taproot()
			h.flush()
			_, got, err := h.logs.Raw(context.Background(), tc.id)
			if err != nil {
				t.Fatal(err)
			}
			if tc.streaming {
				// Upstream stamps streaming records with time.Now().
				want = requestInfoTimestamp.ReplaceAllString(want, "${1}Timestamp: X\n")
				got = []byte(requestInfoTimestamp.ReplaceAllString(string(got), "${1}Timestamp: X\n"))
			}
			if string(got) != want {
				t.Fatalf("layout differs\n--- upstream\n%s\n--- taproot\n%s", want, got)
			}
		})
	}
}

func streamThrough(t *testing.T, l logging.RequestLogger, id string, headers map[string][]string, body, apiReq, apiResp []byte) {
	t.Helper()
	w, err := l.LogStreamingRequest("/v1/messages?beta=true", "POST", headers, body, id)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteStatus(200, map[string][]string{"Content-Type": {"text/event-stream"}})
	w.WriteChunkAsync([]byte("event: message_start\ndata: {}\n\n"))
	w.WriteChunkAsync([]byte("event: message_stop\ndata: {}\n\n"))
	_ = w.WriteAPIRequest(apiReq)
	_ = w.WriteAPIResponse(apiResp)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newHarness(t, Config{})
	ts := h.clock.now()
	logSimple(t, h.logs.logger, "route-a", "/v1/messages", "sess-route", 200, ts, []byte(`{"model":"claude-x"}`))
	logSimple(t, h.logs.logger, "route-b", "/v1/messages", "sess-route", 502, ts.Add(time.Second), []byte(`{"model":"claude-y"}`))
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
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header, body
	}

	status, _, raw := get("/request-logs?limit=1&model=claude-y")
	var list struct {
		Logs       []LogMeta `json:"logs"`
		NextCursor *string   `json:"nextCursor"`
	}
	if status != 200 || json.Unmarshal(raw, &list) != nil || len(list.Logs) != 1 || list.Logs[0].RequestID != "route-b" || list.NextCursor != nil {
		t.Fatalf("list: %d %s", status, raw)
	}
	status, _, raw = get("/request-logs?limit=1")
	if json.Unmarshal(raw, &list) != nil || list.NextCursor == nil || list.Logs[0].RequestID != "route-b" {
		t.Fatalf("first page: %d %s", status, raw)
	}
	status, _, raw = get("/request-logs?limit=1&cursor=" + *list.NextCursor)
	if json.Unmarshal(raw, &list) != nil || len(list.Logs) != 1 || list.Logs[0].RequestID != "route-a" {
		t.Fatalf("second page: %d %s", status, raw)
	}

	status, _, raw = get("/request-logs/route-a")
	var detail Detail
	if status != 200 || json.Unmarshal(raw, &detail) != nil || detail.Request.Body != `{"model":"claude-x"}` || detail.Meta.SessionID != "claude:sess-route" {
		t.Fatalf("detail: %d %s", status, raw)
	}
	status, header, raw := get("/request-logs/route-a/raw")
	if status != 200 || !strings.HasPrefix(string(raw), "=== REQUEST INFO ===") || !strings.Contains(header.Get("Content-Disposition"), "request-route-a.log") {
		t.Fatalf("raw: %d %s", status, raw)
	}
	status, _, raw = get("/request-logs/stats")
	var stats Stats
	if status != 200 || json.Unmarshal(raw, &stats) != nil || stats.Count != 2 || stats.Oldest == nil || stats.BytesOnDisk <= 0 {
		t.Fatalf("stats: %d %s", status, raw)
	}

	for path, want := range map[string]int{
		"/request-logs/missing":        404,
		"/request-logs/missing/raw":    404,
		"/request-logs?cursor=@@":      400,
		"/request-logs?status=teapot":  400,
		"/request-logs?from=yesterday": 400,
		"/request-logs?limit=-3":       400,
	} {
		status, _, raw := get(path)
		var body struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if status != want || json.Unmarshal(raw, &body) != nil || body.Error.Code == "" {
			t.Fatalf("%s: %d %s", path, status, raw)
		}
	}
}
