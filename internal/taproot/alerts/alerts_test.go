package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/ingest"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func init() { gin.SetMode(gin.TestMode) }

type fakeFeed struct {
	latest []ingest.QuotaObservation
	hook   func([]ingest.QuotaObservation)
}

func (f *fakeFeed) OnQuota(fn func([]ingest.QuotaObservation)) { f.hook = fn }
func (f *fakeFeed) LatestQuota(context.Context) ([]ingest.QuotaObservation, error) {
	return f.latest, nil
}

type fakeAuths struct {
	mu   sync.Mutex
	list []*coreauth.Auth
}

func (f *fakeAuths) List() []*coreauth.Auth {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*coreauth.Auth, len(f.list))
	for i, a := range f.list {
		out[i] = a.Clone()
	}
	return out
}

type harness struct {
	svc   *Service
	feed  *fakeFeed
	auths *fakeAuths
	fake  *fakeDiscord
	now   time.Time
}

func openDB(t *testing.T, dir string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Config{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newHarness(t *testing.T, db *store.DB, fake *fakeDiscord, auths ...*coreauth.Auth) *harness {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	t.Setenv(OriginEnv, srv.URL)
	h := &harness{feed: &fakeFeed{}, auths: &fakeAuths{list: auths}, fake: fake, now: t0}
	svc, err := New(Config{DiscordWebhook: testWebhook}, db, h.feed)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return h.now }
	svc.sender.sleep = func(context.Context, time.Duration) error { return nil }
	svc.auths = h.auths
	// No Claude grant endpoint in these tests unless one sets it.
	svc.grants.url = srv.URL + "/no-grants"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := svc.loadState(ctx); err != nil {
		t.Fatal(err)
	}
	go svc.sender.Run(ctx)
	h.svc = svc
	return h
}

// step advances the clock and runs one evaluation, waiting for any grant read
// it started.
func (h *harness) step(t *testing.T, d time.Duration) {
	t.Helper()
	h.now = h.now.Add(d)
	h.svc.evaluate(context.Background())
	waitFor(t, func() bool {
		h.svc.grantMu.Lock()
		defer h.svc.grantMu.Unlock()
		return !h.svc.grantPolling
	})
}

func (h *harness) titles(t *testing.T, want int) []string {
	t.Helper()
	waitFor(t, func() bool { return h.fake.count() >= want })
	time.Sleep(20 * time.Millisecond) // catch anything posted beyond want
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	var out []string
	for _, b := range h.fake.bodies {
		var p webhookPayload
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Embeds[0].Title)
	}
	if len(out) != want {
		t.Fatalf("posts = %q, want %d", out, want)
	}
	return out
}

func claudeAuth(id, label string) *coreauth.Auth {
	return &coreauth.Auth{ID: id, Provider: "claude", Label: label, Status: coreauth.StatusActive, Metadata: map[string]any{"access_token": "tok-" + id}}
}

func TestServiceLimitAlertSurvivesRestartAndRecovers(t *testing.T) {
	dir := t.TempDir()
	db := openDB(t, dir)
	fake := &fakeDiscord{}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, db, fake, a, b)
	idx := a.EnsureIndex()

	h.feed.hook([]ingest.QuotaObservation{{AuthIndex: idx, Provider: "claude", Window: "five_hour", Utilization: 1, Status: "rejected", ResetsAt: t0.Add(time.Hour), ObservedAt: t0, AccountWide: true}})
	h.step(t, 0)
	if got := h.titles(t, 1); got[0] != "Claude account hit its 5-hour limit" {
		t.Fatalf("titles = %q", got)
	}
	h.step(t, time.Minute)
	h.titles(t, 1)

	// A restarted service reads the stored state and the stored quota, and
	// does not post the same limit again.
	h2 := newHarness(t, db, fake, a, b)
	h2.feed.latest = []ingest.QuotaObservation{{AuthIndex: idx, Provider: "claude", Window: "five_hour", Utilization: 1, Status: "rejected", ResetsAt: t0.Add(time.Hour), ObservedAt: t0, AccountWide: true}}
	h2.svc.observe(h2.feed.latest)
	h2.step(t, 2*time.Minute)
	h2.titles(t, 1)

	// The reset passes; the recovery waits for the settle time.
	h2.step(t, time.Hour)
	h2.titles(t, 1)
	h2.step(t, settle)
	if got := h2.titles(t, 2); got[1] != "Claude account usable again" {
		t.Fatalf("titles = %q", got)
	}
}

func TestServicePoolEmptyAndLoginBroken(t *testing.T) {
	fake := &fakeDiscord{}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	b.RequiresSignIn = true
	b.Status, b.StatusMessage = coreauth.StatusError, "refresh_token_reused: token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig"
	h := newHarness(t, openDB(t, t.TempDir()), fake, a, b)

	h.feed.hook([]ingest.QuotaObservation{{AuthIndex: a.EnsureIndex(), Provider: "claude", Window: "seven_day", Utilization: 1, Status: "rejected", ResetsAt: t0.Add(48 * time.Hour), ObservedAt: t0, AccountWide: true}})
	h.step(t, 0)
	got := h.titles(t, 2)
	if got[0] != "Claude account hit its weekly limit" || got[1] != "Claude pool is empty" {
		t.Fatalf("titles = %q", got)
	}
	h.step(t, loginDelay)
	if got := h.titles(t, 3); got[2] != "Claude login broken" {
		t.Fatalf("titles = %q", got)
	}
	h.fake.mu.Lock()
	login := string(h.fake.bodies[2])
	h.fake.mu.Unlock()
	if strings.Contains(login, "eyJhbGci") || !strings.Contains(login, "refresh\\\\_token\\\\_reused") {
		t.Fatalf("login alert should carry the sanitized reason: %s", login)
	}

	// Signing in again fixes b, which also refills the pool.
	h.auths.mu.Lock()
	h.auths.list[1] = claudeAuth("b", "spare")
	h.auths.mu.Unlock()
	h.step(t, time.Minute)
	h.step(t, settle)
	got = h.titles(t, 5)
	if got[3] != "Claude login works again" || got[4] != "Claude pool has capacity again" {
		t.Fatalf("titles = %q", got)
	}
}

func TestServiceStaysQuietWhenDisabled(t *testing.T) {
	fake := &fakeDiscord{}
	a := claudeAuth("a", "main")
	h := newHarness(t, openDB(t, t.TempDir()), fake, a)
	off := false
	if err := h.svc.SetConfig(Config{DiscordWebhook: testWebhook, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	h.feed.hook([]ingest.QuotaObservation{{AuthIndex: a.EnsureIndex(), Provider: "claude", Window: "five_hour", Utilization: 1, ResetsAt: t0.Add(time.Hour), ObservedAt: t0, AccountWide: true}})
	h.step(t, 0)
	h.titles(t, 0)
}

func TestServiceReadsGrantsReadOnly(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	grants := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization")+" "+r.Header.Get("anthropic-beta"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":10},"cedar_ember":{"eligible":true,"at_limit":false,"grants":[
			{"id":"grant_1","label":"Launch *bonus*","resets_total":3,"resets_left":2,"ends_at":"` + t0.Add(20*time.Hour).Format(time.RFC3339) + `"},
			{"id":"grant_2","resets_total":1,"resets_left":1,"ends_at":"` + t0.Add(72*time.Hour).Format(time.RFC3339) + `"}]}}`))
	}))
	defer grants.Close()
	fake := &fakeDiscord{}
	h := newHarness(t, openDB(t, t.TempDir()), fake, claudeAuth("a", "main"))
	h.svc.grants.url = grants.URL + "/api/oauth/usage?cedar_ember=1&skip_spend=1"

	h.step(t, 0)
	h.step(t, 0)
	got := h.titles(t, 1)
	if got[0] != "Claude reset grant expires soon" {
		t.Fatalf("titles = %q", got)
	}
	h.fake.mu.Lock()
	body := string(h.fake.bodies[0])
	h.fake.mu.Unlock()
	if !strings.Contains(body, `Launch \\*bonus\\*`) {
		t.Fatalf("grant label should be escaped: %s", body)
	}

	// The next read waits for the grant interval.
	h.step(t, time.Hour)
	h.step(t, grantInterval)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("grant reads = %q, want 2", seen)
	}
	if seen[0] != "GET /api/oauth/usage?cedar_ember=1&skip_spend=1 Bearer tok-a oauth-2025-04-20" {
		t.Fatalf("grant read = %q", seen[0])
	}
}

func TestTestEndpoint(t *testing.T) {
	fake := &fakeDiscord{}
	h := newHarness(t, openDB(t, t.TempDir()), fake)
	r := gin.New()
	h.svc.Routes(r.Group("/taproot/api/v1"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/taproot/api/v1/alerts/test", nil))
	if w.Code != http.StatusOK || h.titles(t, 1)[0] != "taproot test alert" {
		t.Fatalf("test send = %d %s", w.Code, w.Body)
	}

	fake.mu.Lock()
	fake.statuses = append(fake.statuses, status(http.StatusNotFound, `{"message":"Unknown Webhook"}`))
	fake.mu.Unlock()
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/taproot/api/v1/alerts/test", nil))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "discord answered 404") || strings.Contains(w.Body.String(), fakeToken) {
		t.Fatalf("failed test send = %d %s", w.Code, w.Body)
	}

	if err := h.svc.SetConfig(Config{}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/taproot/api/v1/alerts/test", nil))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "no_webhook") {
		t.Fatalf("no webhook = %d %s", w.Code, w.Body)
	}
}

func TestNewRejectsAnInvalidWebhook(t *testing.T) {
	_, err := New(Config{DiscordWebhook: "https://example.com/hook/" + fakeToken}, openDB(t, t.TempDir()), &fakeFeed{})
	if err == nil || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseGrantStatus(t *testing.T) {
	grants, err := parseGrantStatus([]byte(`{"cedar_ember":{"eligible":true,"grants":[
		{"id":"ok","resets_left":1,"ends_at":"2026-11-04T00:00:00Z"},
		{"id":"BAD ID","resets_left":1},
		{"id":"no_count"},
		{"id":"bad_time","resets_left":1,"ends_at":"tomorrow"},
		{"id":"open","resets_left":2}]}}`))
	if err != nil || len(grants) != 2 || grants[0].ID != "ok" || grants[1].ID != "open" || !grants[1].EndsAt.IsZero() {
		t.Fatalf("grants = %+v, err = %v", grants, err)
	}
	if g, err := parseGrantStatus([]byte(`{"cedar_ember":{"eligible":false,"grants":[{"id":"x","resets_left":1}]}}`)); err != nil || g != nil {
		t.Fatalf("ineligible = %+v, %v", g, err)
	}
	if _, err := parseGrantStatus([]byte(`{"five_hour":{}}`)); err == nil {
		t.Fatal("a response without cedar_ember should fail")
	}
}
