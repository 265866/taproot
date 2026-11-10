package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/egress"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
)

type seenRequest struct {
	auth, beta, account, agent string
}

func usageServer(t *testing.T, status int, body string) (*httptest.Server, func() []seenRequest) {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []seenRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, seenRequest{r.Header.Get("Authorization"), r.Header.Get("anthropic-beta"), r.Header.Get("Chatgpt-Account-Id"), r.Header.Get("User-Agent")})
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []seenRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]seenRequest(nil), seen...)
	}
}

func registerAuth(t *testing.T, m *coreauth.Manager, a *coreauth.Auth) string {
	t.Helper()
	got, err := m.Register(context.Background(), a)
	if err != nil {
		t.Fatalf("register %s: %v", a.ID, err)
	}
	return got.EnsureIndex()
}

func TestPollIdleReadsUsageEndpointsWithoutRefreshing(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	claudeSrv, claudeSeen := usageServer(t, http.StatusOK,
		`{"five_hour":{"utilization":20.0,"resets_at":"2026-11-02T15:00:00+00:00"},"seven_day":{"utilization":55.0,"resets_at":"2026-11-06T00:00:00+00:00"}}`)
	codexSrv, codexSeen := usageServer(t, http.StatusOK,
		`{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_at":1793617200},"secondary_window":{"used_percent":30,"limit_window_seconds":604800,"reset_after_seconds":3600}}}`)
	i.claudeUsageURL, i.codexUsageURL = claudeSrv.URL, codexSrv.URL

	m := coreauth.NewManager(nil, nil, nil)
	idle := registerAuth(t, m, &coreauth.Auth{ID: "claude-idle.json", FileName: "claude-idle.json", Provider: "claude", Label: "Idle",
		Metadata: map[string]any{"access_token": "tok-idle", "refresh_token": "never-used", "email": "idle@example.test"}})
	busy := registerAuth(t, m, &coreauth.Auth{ID: "claude-busy.json", FileName: "claude-busy.json", Provider: "claude",
		Metadata: map[string]any{"access_token": "tok-busy"}})
	registerAuth(t, m, &coreauth.Auth{ID: "claude-off.json", FileName: "claude-off.json", Provider: "claude", Disabled: true,
		Metadata: map[string]any{"access_token": "tok-off"}})
	registerAuth(t, m, &coreauth.Auth{ID: "gemini.json", FileName: "gemini.json", Provider: "gemini",
		Metadata: map[string]any{"access_token": "tok-gemini"}})
	codexIdx := registerAuth(t, m, &coreauth.Auth{ID: "codex.json", FileName: "codex.json", Provider: "codex",
		Metadata: map[string]any{"access_token": "tok-codex", "account_id": "acct-1"}})
	i.AttachAuthManager(m)

	// busy saw traffic two minutes ago, so it is not idle.
	r := claudeRecord("recent", clock.t.Add(-2*time.Minute), hdr("Anthropic-Ratelimit-Unified-5h-Utilization", "0.5"))
	r.AuthIndex, r.Latency = busy, 0
	i.HandleUsage(context.Background(), r)
	mustFlush(t, i)

	i.pollIdle(context.Background())
	mustFlush(t, i)

	cs := claudeSeen()
	if len(cs) != 1 || cs[0].auth != "Bearer tok-idle" || cs[0].beta != "oauth-2025-04-20" || cs[0].agent != claudeUserAgent {
		t.Fatalf("claude requests %+v, want one for the idle account", cs)
	}
	xs := codexSeen()
	if len(xs) != 1 || xs[0].auth != "Bearer tok-codex" || xs[0].account != "acct-1" {
		t.Fatalf("codex requests %+v", xs)
	}
	if n := queryInt(t, i, `SELECT COUNT(*) FROM quota_observations WHERE auth_index=? AND source='poll'`, idle); n != 2 {
		t.Fatalf("idle claude poll rows = %d, want 2", n)
	}
	if n := queryInt(t, i, `SELECT CAST(utilization*100 AS INTEGER) FROM quota_observations WHERE auth_index=? AND window_name='seven_day'`, idle); n != 55 {
		t.Fatalf("idle seven_day utilization = %d%%", n)
	}
	if n := queryInt(t, i, `SELECT period_seconds FROM quota_observations WHERE auth_index=? AND window_name='codex_secondary'`, codexIdx); n != 604800 {
		t.Fatalf("codex secondary period = %d", n)
	}
	// Polling read the token only; credentials are untouched.
	for _, a := range m.List() {
		if a.ID == "claude-idle.json" && (a.Metadata["access_token"] != "tok-idle" || a.Metadata["refresh_token"] != "never-used") {
			t.Fatalf("credentials changed: %+v", a.Metadata)
		}
	}

	// Within the interval nothing is polled again.
	clock.t = clock.t.Add(5 * time.Minute)
	i.pollIdle(context.Background())
	if len(claudeSeen()) != 1 || len(codexSeen()) != 1 {
		t.Fatal("re-polled inside the interval")
	}
	clock.t = clock.t.Add(16 * time.Minute)
	i.pollIdle(context.Background())
	if len(claudeSeen()) != 3 {
		t.Fatalf("after the interval claude polls = %d, want 3 (idle and busy)", len(claudeSeen()))
	}
}

func TestPollFailureIsRetriedOnlyAfterInterval(t *testing.T) {
	i, clock := newTestIngest(t, Config{PollInterval: "10m"})
	srv, seen := usageServer(t, http.StatusUnauthorized, `{"error":"expired"}`)
	i.claudeUsageURL = srv.URL
	m := coreauth.NewManager(nil, nil, nil)
	idx := registerAuth(t, m, &coreauth.Auth{ID: "a.json", FileName: "a.json", Provider: "claude", Metadata: map[string]any{"access_token": "t"}})
	registerAuth(t, m, &coreauth.Auth{ID: "notoken.json", FileName: "notoken.json", Provider: "claude", Metadata: map[string]any{}})
	i.AttachAuthManager(m)

	i.pollIdle(context.Background())
	clock.t = clock.t.Add(time.Minute)
	i.pollIdle(context.Background())
	mustFlush(t, i)
	if len(seen()) != 1 {
		t.Fatalf("requests = %d, want 1 (no token means no request; failure waits for the interval)", len(seen()))
	}
	if n := queryInt(t, i, `SELECT COUNT(*) FROM quota_observations WHERE auth_index=?`, idx); n != 0 {
		t.Fatalf("rows from a failed poll = %d", n)
	}
	clock.t = clock.t.Add(10 * time.Minute)
	i.pollIdle(context.Background())
	if len(seen()) != 2 {
		t.Fatalf("requests after interval = %d, want 2", len(seen()))
	}
}

func TestRefreshAccountsCopiesManagerState(t *testing.T) {
	i, _ := newTestIngest(t, Config{})
	m := coreauth.NewManager(nil, nil, nil)
	idx := registerAuth(t, m, &coreauth.Auth{ID: "c.json", FileName: "auths/c.json", Provider: "Codex", Disabled: true,
		Attributes: map[string]string{"plan_type": "pro"}, Metadata: map[string]any{"email": "c@example.test"}})
	plain := registerAuth(t, m, &coreauth.Auth{ID: "p.json", FileName: "auths/p.json", Provider: "claude"})

	if err := i.refreshAccounts(context.Background()); err != nil {
		t.Fatalf("refresh without manager: %v", err)
	}
	i.AttachAuthManager(m)
	i.AttachAuthManager(nil)
	if err := i.refreshAccounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	var provider, label, email, plan string
	var disabled int
	if err := i.db.QueryRow(`SELECT provider, label, email, plan, disabled FROM accounts WHERE auth_index=?`, idx).Scan(&provider, &label, &email, &plan, &disabled); err != nil {
		t.Fatal(err)
	}
	if provider != "codex" || label != "c@example.test" || email != "c@example.test" || plan != "pro" || disabled != 1 {
		t.Fatalf("account = %q %q %q %q %d", provider, label, email, plan, disabled)
	}
	if err := i.db.QueryRow(`SELECT label FROM accounts WHERE auth_index=?`, plain).Scan(&label); err != nil || label != "p" {
		t.Fatalf("file-name label = %q, %v", label, err)
	}
}

// TestPollStampsEachAccountAtItsResponse polls accounts one after another
// against an endpoint that takes 700 ms to answer.
func TestPollStampsEachAccountAtItsResponse(t *testing.T) {
	i, _ := newTestIngest(t, Config{})
	var (
		mu  sync.Mutex
		now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	)
	i.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		now = now.Add(700 * time.Millisecond)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_after_seconds":3600}}}`))
	}))
	t.Cleanup(srv.Close)
	i.codexUsageURL = srv.URL
	m := coreauth.NewManager(nil, nil, nil)
	var accounts []string
	for _, id := range []string{"codex-1.json", "codex-2.json", "codex-3.json"} {
		accounts = append(accounts, registerAuth(t, m, &coreauth.Auth{ID: id, FileName: id, Provider: "codex",
			Metadata: map[string]any{"access_token": "tok"}}))
	}
	i.AttachAuthManager(m)

	i.pollIdle(context.Background())
	mustFlush(t, i)
	var stamps []int64
	for _, idx := range accounts {
		observed := queryInt(t, i, `SELECT observed_at FROM quota_observations WHERE auth_index=?`, idx)
		// The reset is relative to the response, so it must follow the stamp.
		if resets := queryInt(t, i, `SELECT resets_at FROM quota_observations WHERE auth_index=?`, idx); resets != observed+3_600_000 {
			t.Fatalf("%s: resets %d, observed %d", idx, resets, observed)
		}
		stamps = append(stamps, observed)
	}
	start := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	got := map[int64]bool{}
	for _, s := range stamps {
		got[s-ms(start)] = true
	}
	if len(got) != 3 || !got[700] || !got[1400] || !got[2100] {
		t.Fatalf("stamps after start %v, want 700, 1400, and 2100 ms", got)
	}
}

func TestRefreshAccountsMarksLastCredentialRemoved(t *testing.T) {
	i, _ := newTestIngest(t, Config{})
	m := coreauth.NewManager(nil, nil, nil)
	idx := registerAuth(t, m, &coreauth.Auth{ID: "only.json", FileName: "only.json", Provider: "claude"})
	i.AttachAuthManager(m)
	if err := i.refreshAccounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	i.enqueueObservations([]observation{{AuthIndex: idx, Provider: "claude", Window: "five_hour", Utilization: 0.4, ObservedAt: time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)}})
	mustFlush(t, i)

	m.Remove(context.Background(), "only.json")
	if err := i.refreshAccounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, i, `SELECT COUNT(*) FROM accounts WHERE auth_index=? AND removed_at IS NOT NULL`, idx); n != 1 {
		t.Fatal("the only credential was not marked removed")
	}
	if latest, err := i.LatestQuota(context.Background()); err != nil || len(latest) != 0 {
		t.Fatalf("latest = %+v, err = %v", latest, err)
	}
}

func TestPollUsesTheGlobalProxyAndRefusesAnInvalidOne(t *testing.T) {
	i, _ := newTestIngest(t, Config{})
	direct, directSeen := usageServer(t, http.StatusOK, `{"five_hour":{"utilization":20.0}}`)
	proxied, proxiedSeen := usageServer(t, http.StatusOK, `{"five_hour":{"utilization":30.0}}`)
	i.claudeUsageURL = direct.URL
	global := proxied.URL
	p := &egress.Proxy{}
	p.SetGlobal(func() string { return global })
	i.UseProxy(p)

	a := &coreauth.Auth{ID: "a.json", Index: "idx-a", Provider: "claude", Metadata: map[string]any{"access_token": "tok-a"}}
	obs, err := i.pollAccount(context.Background(), a, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(directSeen()) != 0 || len(proxiedSeen()) != 1 || proxiedSeen()[0].auth != "Bearer tok-a" {
		t.Fatalf("direct %v, proxy %v; want the poll through the global proxy", directSeen(), proxiedSeen())
	}
	if len(obs) != 1 || obs[0].Utilization != 0.3 {
		t.Fatalf("observations %+v", obs)
	}

	a.ProxyURL = "socks5://user:pa ss@[::1"
	if _, err := i.pollAccount(context.Background(), a, "claude"); err == nil {
		t.Fatal("polled with an invalid credential proxy")
	}
	global = "not a url"
	a.ProxyURL = ""
	if _, err := i.pollAccount(context.Background(), a, "claude"); err == nil {
		t.Fatal("polled with an invalid global proxy")
	}
	if len(directSeen()) != 0 || len(proxiedSeen()) != 1 {
		t.Fatalf("direct %v, proxy %v; an invalid proxy must send nothing", directSeen(), proxiedSeen())
	}
}
