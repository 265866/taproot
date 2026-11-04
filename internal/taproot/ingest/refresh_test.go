package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
)

type refreshFixture struct {
	i      *Ingest
	r      *gin.Engine
	clock  *fakeClock
	claude string
	codex  string
	gemini string
	seen   func() []seenRequest
}

func newRefreshFixture(t *testing.T, status int, body string) refreshFixture {
	t.Helper()
	i, r, clock := newAPI(t)
	srv, seen := usageServer(t, status, body)
	i.claudeUsageURL, i.codexUsageURL = srv.URL, srv.URL
	m := coreauth.NewManager(nil, nil, nil)
	f := refreshFixture{i: i, r: r, clock: clock, seen: seen}
	// Disabled on purpose: a manual refresh still reads it.
	f.claude = registerAuth(t, m, &coreauth.Auth{
		ID: "claude-a.json", FileName: "claude-a.json", Provider: "claude", Disabled: true,
		Metadata: map[string]any{"access_token": "tok-a"},
	})
	f.codex = registerAuth(t, m, &coreauth.Auth{
		ID: "codex.json", FileName: "codex.json", Provider: "codex",
		Metadata: map[string]any{"access_token": "tok-codex", "account_id": "acct-1"},
	})
	f.gemini = registerAuth(t, m, &coreauth.Auth{
		ID: "gemini.json", FileName: "gemini.json", Provider: "gemini",
		Metadata: map[string]any{"access_token": "tok-gemini"},
	})
	i.AttachAuthManager(m)
	return f
}

const claudeUsageBody = `{"five_hour":{"utilization":20.0,"resets_at":"2026-11-02T15:00:00+00:00"},"seven_day":{"utilization":100.0,"resets_at":"2026-11-06T00:00:00+00:00"}}`

func refresh(t *testing.T, f refreshFixture, authIndex string, out any) int {
	t.Helper()
	return call(t, f.r, http.MethodPost, "/api/quota/refresh", map[string]string{"authIndex": authIndex}, out)
}

func TestQuotaRefreshStoresAndReturnsWindows(t *testing.T) {
	f := newRefreshFixture(t, http.StatusOK, claudeUsageBody)

	var got quotaAccountJSON
	if code := refresh(t, f, f.claude, &got); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if seen := f.seen(); len(seen) != 1 || seen[0].auth != "Bearer tok-a" {
		t.Fatalf("usage requests %+v, want one with the account's token", seen)
	}
	if got.AuthIndex != f.claude || got.Provider != "claude" || len(got.Windows) != 2 {
		t.Fatalf("response %+v, want the account's two windows", got)
	}
	five, seven := got.Windows[0], got.Windows[1]
	if five.Window != "five_hour" || five.Utilization != 0.2 || five.Status != "allowed" || *five.ResetsAt != "2026-11-02T15:00:00Z" || !five.AccountWide {
		t.Errorf("five_hour %+v", five)
	}
	if seven.Window != "seven_day" || seven.Utilization != 1 || seven.Status != "rejected" || seven.ObservedAt != rfc(f.clock.t) {
		t.Errorf("seven_day %+v", seven)
	}

	// An unchanged reading 31s later is still stored, so observedAt moves.
	f.clock.t = f.clock.t.Add(31 * time.Second)
	if code := refresh(t, f, f.claude, &got); code != http.StatusOK {
		t.Fatalf("second refresh status %d, want 200", code)
	}
	if got.Windows[0].ObservedAt != rfc(f.clock.t) {
		t.Errorf("observedAt %s after second refresh, want %s", got.Windows[0].ObservedAt, rfc(f.clock.t))
	}
	if n := queryInt(t, f.i, `SELECT COUNT(*) FROM quota_observations WHERE auth_index=? AND source='refresh'`, f.claude); n != 4 {
		t.Errorf("%d refresh rows, want 4 (two windows, two refreshes)", n)
	}
}

func TestQuotaRefreshReadsCodex(t *testing.T) {
	f := newRefreshFixture(t, http.StatusOK,
		`{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_after_seconds":600}}}`)
	var got quotaAccountJSON
	if code := refresh(t, f, f.codex, &got); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if seen := f.seen(); len(seen) != 1 || seen[0].account != "acct-1" {
		t.Fatalf("usage requests %+v, want the ChatGPT account header", seen)
	}
	if len(got.Windows) != 1 || got.Windows[0].Window != "codex_primary" || got.Windows[0].Utilization != 0.1 {
		t.Fatalf("windows %+v, want codex_primary at 10%%", got.Windows)
	}
}

func TestQuotaRefreshRejectsUnknownAndUnsupportedAccounts(t *testing.T) {
	f := newRefreshFixture(t, http.StatusOK, claudeUsageBody)
	var e apiErrorBody
	if code := refresh(t, f, "0000000000000000", &e); code != http.StatusNotFound || e.Error.Code != codeNotFound {
		t.Errorf("unknown account: status %d error %+v, want 404 not_found", code, e)
	}
	if code := refresh(t, f, f.gemini, &e); code != http.StatusBadRequest || e.Error.Code != codeUnsupportedProvider {
		t.Errorf("gemini: status %d error %+v, want 400 unsupported_provider", code, e)
	}
	expectBadRequest(t, f.r, http.MethodPost, "/api/quota/refresh", map[string]string{})
	expectBadRequest(t, f.r, http.MethodPost, "/api/quota/refresh", "not an object")
	if seen := f.seen(); len(seen) != 0 {
		t.Errorf("usage requests %+v, want none", seen)
	}
	// Refused requests do not use up the account's refresh.
	if code := refresh(t, f, f.claude, nil); code != http.StatusOK {
		t.Errorf("first real refresh status %d, want 200", code)
	}
}

func TestQuotaRefreshIsRateLimitedPerAccount(t *testing.T) {
	f := newRefreshFixture(t, http.StatusOK, claudeUsageBody)
	if code := refresh(t, f, f.claude, nil); code != http.StatusOK {
		t.Fatalf("first refresh status %d", code)
	}
	f.clock.t = f.clock.t.Add(12*time.Second + 300*time.Millisecond)

	req := httptest.NewRequest(http.MethodPost, "/api/quota/refresh", jsonBody(t, map[string]string{"authIndex": f.claude}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	var limited rateLimitedBody
	decode(t, w, &limited)
	// 17.7s left rounds up to 18.
	if w.Code != http.StatusTooManyRequests || limited.Error.Code != codeRateLimited || limited.Error.RetryAfter != 18 || w.Header().Get("Retry-After") != "18" {
		t.Fatalf("status %d body %+v Retry-After %q, want 429 retryAfter 18", w.Code, limited, w.Header().Get("Retry-After"))
	}
	// Another account is not held back.
	if code := refresh(t, f, f.codex, nil); code != http.StatusOK {
		t.Errorf("other account status %d, want 200", code)
	}
	if n := len(f.seen()); n != 2 {
		t.Errorf("%d usage requests, want 2 (the limited one never left)", n)
	}
	f.clock.t = f.clock.t.Add(18 * time.Second)
	if code := refresh(t, f, f.claude, nil); code != http.StatusOK {
		t.Errorf("refresh after the interval: status %d, want 200", code)
	}
}

func TestQuotaRefreshReportsUpstreamFailure(t *testing.T) {
	f := newRefreshFixture(t, http.StatusUnauthorized, `{"error":"invalid token"}`)
	var e apiErrorBody
	if code := refresh(t, f, f.claude, &e); code != http.StatusBadGateway || e.Error.Code != codeUpstreamFailed || e.Error.Message != "usage read failed: usage endpoint status 401" {
		t.Fatalf("status %d error %+v, want 502 upstream_failed", code, e)
	}
	if n := queryInt(t, f.i, `SELECT COUNT(*) FROM quota_observations`); n != 0 {
		t.Errorf("%d observations stored after a failed read, want 0", n)
	}
	// A failed read still counts, so a broken account cannot be retried in a loop.
	if code := refresh(t, f, f.claude, nil); code != http.StatusTooManyRequests {
		t.Errorf("immediate retry status %d, want 429", code)
	}
}

func TestDevUsageURLAcceptsLoopbackOnly(t *testing.T) {
	const fallback = "https://api.anthropic.com/api/oauth/usage"
	cases := map[string]string{
		"":                                 fallback,
		"http://127.0.0.1:18799/usage":     "http://127.0.0.1:18799/usage",
		"http://localhost:9/u":             "http://localhost:9/u",
		"http://[::1]:9/u":                 "http://[::1]:9/u",
		"https://evil.example/usage":       fallback,
		"http://127.0.0.1.evil.example/u":  fallback,
		"file:///etc/passwd":               fallback,
		"http://user@10.0.0.1:80/usage":    fallback,
		"  http://127.0.0.1:18799/usage  ": "http://127.0.0.1:18799/usage",
	}
	for raw, want := range cases {
		t.Setenv(devClaudeUsageURLEnv, raw)
		if got := devUsageURL(devClaudeUsageURLEnv, fallback); got != want {
			t.Errorf("%q: got %q, want %q", raw, got, want)
		}
	}
}

func jsonBody(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(raw)
}

func decode(t *testing.T, w *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
}
