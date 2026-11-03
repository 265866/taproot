package access

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

// newTestGuard builds a guard with New and leaves MANAGEMENT_PASSWORD as it
// was when the test ends.
func newTestGuard(t *testing.T, cfg Config) *Guard {
	t.Helper()
	t.Setenv(managementEnv, "")
	if err := os.Unsetenv(managementEnv); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	g, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

type seen struct {
	authorization string
	managementKey string
}

// testEngine mounts the guard in front of a few routes that echo what the
// handler received.
func testEngine(g *Guard) (*gin.Engine, *seen) {
	gin.SetMode(gin.TestMode)
	got := &seen{}
	e := gin.New()
	e.Use(g.Middleware())
	record := func(c *gin.Context) {
		got.authorization = c.GetHeader("Authorization")
		got.managementKey = c.GetHeader("X-Management-Key")
		c.String(http.StatusOK, "ok")
	}
	e.GET("/healthz", record)
	e.GET("/v1/models", record)
	e.GET("/v8/management/config", record)
	e.GET("/v0/management/config", record)
	e.GET("/v0/resource/plugins/demo/page", record)
	g.Routes(e.Group("/taproot/api/v1"))
	return e, got
}

func do(e http.Handler, remote, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = remote
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func assertForbidden(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body)
	}
	var body struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "forbidden" {
		t.Fatalf("body = %s, want {error:{code:forbidden}}", rec.Body)
	}
}

func me(t *testing.T, e http.Handler, remote string, headers map[string]string) meResponse {
	t.Helper()
	rec := do(e, remote, "/taproot/api/v1/me", headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("/me from %s = %d, want 200; body %s", remote, rec.Code, rec.Body)
	}
	var got meResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	return got
}

func TestDeniesNonTailnetAddresses(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	for _, remote := range []string{"203.0.113.9:5000", "192.168.1.20:5000", "10.0.0.5:1", "[2001:db8::1]:443", "not-an-address", ""} {
		t.Run(remote, func(t *testing.T) {
			assertForbidden(t, do(e, remote, "/v1/models", nil))
			assertForbidden(t, do(e, remote, "/v8/management/config", nil))
		})
	}
	if n := ts.callCount("203.0.113.9"); n != 0 {
		t.Fatalf("whois called %d times for a non-tailnet address", n)
	}
}

func TestDeniesWhenWhoisFails(t *testing.T) {
	t.Run("socket missing", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "absent.sock")
		e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: missing}))
		assertForbidden(t, do(e, adminIP+":40000", "/v1/models", nil))
	})
	t.Run("tailscaled error then recovery", func(t *testing.T) {
		ts := newFakeTailscaled(t, defaultPeers())
		e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
		ts.failNext(adminIP, 1)
		assertForbidden(t, do(e, adminIP+":40000", "/v1/models", nil))
		// The failure was not cached: the next request asks again and is admitted.
		if rec := do(e, adminIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("after recovery status = %d, want 200", rec.Code)
		}
		if n := ts.callCount(adminIP); n != 2 {
			t.Fatalf("whois calls = %d, want 2", n)
		}
	})
	t.Run("unknown peer", func(t *testing.T) {
		ts := newFakeTailscaled(t, defaultPeers())
		e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
		assertForbidden(t, do(e, strayIP+":40000", "/v1/models", nil))
	})
}

func TestDeniesUnknownLogin(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	assertForbidden(t, do(e, outsiderIP+":40000", "/v1/models", nil))
	assertForbidden(t, do(e, outsiderIP+":40000", "/taproot/api/v1/me", nil))
	assertForbidden(t, do(e, outsiderIP+":40000", "/v8/management/config", nil))
}

func TestSpoofedIdentityHeadersChangeNothing(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	spoof := map[string]string{
		"X-Forwarded-For":            "127.0.0.1, " + adminIP,
		"X-Real-IP":                  adminIP,
		"Forwarded":                  "for=127.0.0.1",
		"True-Client-IP":             "127.0.0.1",
		"CF-Connecting-IP":           "127.0.0.1",
		"Tailscale-User-Login":       "alice@github",
		"Tailscale-User-Name":        "Alice",
		"Tailscale-User-Profile-Pic": "https://example.invalid",
	}
	for _, remote := range []string{"203.0.113.9:5000", outsiderIP + ":40000", strayIP + ":40000"} {
		for _, path := range []string{"/v1/models", "/v8/management/config", "/taproot/api/v1/me"} {
			assertForbidden(t, do(e, remote, path, spoof))
		}
	}
	// An admitted caller stays who its address says, whatever the headers claim.
	if got := me(t, e, "127.0.0.1:5000", map[string]string{"Tailscale-User-Login": "bob@example.org", "X-Forwarded-For": outsiderIP}); got.Via != "loopback" || got.Login != "" {
		t.Fatalf("loopback /me = %+v, want via loopback with no login", got)
	}
	if got := me(t, e, adminIP+":40000", map[string]string{"Tailscale-User-Login": "bob@example.org"}); got.Login != "alice@github" {
		t.Fatalf("tailnet /me login = %q, want alice@github", got.Login)
	}
}

func TestAdmitsLoopback(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.sock")
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: missing}))
	for _, remote := range []string{"127.0.0.1:5000", "127.5.6.7:5000", "[::1]:5000", "[::ffff:127.0.0.1]:5000"} {
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", remote, rec.Code)
		}
		if got := me(t, e, remote, nil); got.Via != "loopback" {
			t.Fatalf("%s via = %q, want loopback", remote, got.Via)
		}
	}
}

func TestAdmitsAllowlistedLogin(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	if rec := do(e, adminIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := me(t, e, adminIP+":40000", nil)
	want := meResponse{Login: "alice@github", DisplayName: "Alice", Node: "laptop.tail1234.ts.net", Via: "tailnet"}
	if got != want {
		t.Fatalf("/me = %+v, want %+v", got, want)
	}
}

func TestAdmitsIPv6TailnetAddress(t *testing.T) {
	ts := newFakeTailscaled(t, map[string]fakePeer{"fd7a:115c:a1e0::1": {login: "alice@github", node: "laptop"}})
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	if rec := do(e, "[fd7a:115c:a1e0::1]:40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestAdmitsTrustedCIDRWithoutWhois(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, TrustedCIDRs: []string{"192.168.10.0/24", " 10.88.0.0/16 "}}))
	for _, remote := range []string{"192.168.10.44:1", "10.88.3.2:1"} {
		if rec := do(e, remote, "/v8/management/config", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", remote, rec.Code)
		}
		if got := me(t, e, remote, nil); got.Via != "cidr" {
			t.Fatalf("%s via = %q, want cidr", remote, got.Via)
		}
	}
	assertForbidden(t, do(e, "192.168.11.1:1", "/v1/models", nil))
}

func TestHealthzIsExempt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.sock")
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: missing}))
	if rec := do(e, "203.0.113.9:5000", "/healthz", nil); rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", rec.Code)
	}
	assertForbidden(t, do(e, "203.0.113.9:5000", "/healthz/../v1/models", nil))
}

func TestWhoisCacheExpires(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, CacheTTL: time.Minute})
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	e, _ := testEngine(g)
	for range 3 {
		if rec := do(e, adminIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	}
	if n := ts.callCount(adminIP); n != 1 {
		t.Fatalf("whois calls within TTL = %d, want 1", n)
	}
	now = now.Add(59 * time.Second)
	do(e, adminIP+":40000", "/v1/models", nil)
	if n := ts.callCount(adminIP); n != 1 {
		t.Fatalf("whois calls at 59s = %d, want 1", n)
	}
	// The address now belongs to someone else; after the TTL the guard sees it.
	ts.setPeer(adminIP, fakePeer{login: "bob@example.org", node: "bob-pc"})
	now = now.Add(time.Second)
	assertForbidden(t, do(e, adminIP+":40000", "/v1/models", nil))
	if n := ts.callCount(adminIP); n != 2 {
		t.Fatalf("whois calls after TTL = %d, want 2", n)
	}
}

func TestSetAllowLoginsAppliesImmediately(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	e, _ := testEngine(g)
	assertForbidden(t, do(e, outsiderIP+":40000", "/v1/models", nil))

	if err := g.SetAllowLogins([]string{"alice@github", " Bob@Example.org "}); err != nil {
		t.Fatalf("SetAllowLogins: %v", err)
	}
	if rec := do(e, outsiderIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("after allow status = %d, want 200", rec.Code)
	}
	if got := g.AllowLogins(); strings.Join(got, ",") != "alice@github,bob@example.org" {
		t.Fatalf("AllowLogins = %v", got)
	}

	if err := g.SetAllowLogins([]string{"alice@github"}); err != nil {
		t.Fatalf("SetAllowLogins: %v", err)
	}
	assertForbidden(t, do(e, outsiderIP+":40000", "/v1/models", nil))
	if n := ts.callCount(outsiderIP); n != 1 {
		t.Fatalf("whois calls = %d, want 1 (identity cached, decision not)", n)
	}

	for _, bad := range [][]string{nil, {}, {"  "}, {"alice@github", ""}} {
		if err := g.SetAllowLogins(bad); err == nil {
			t.Fatalf("SetAllowLogins(%q) = nil, want error", bad)
		}
	}
	if got := g.AllowLogins(); len(got) != 1 || got[0] != "alice@github" {
		t.Fatalf("rejected update changed the list: %v", got)
	}
	g.AllowLogins()[0] = "mutated"
	if got := g.AllowLogins(); got[0] != "alice@github" {
		t.Fatalf("AllowLogins exposes internal state: %v", got)
	}
}

func TestManagementKeyInjection(t *testing.T) {
	g := newTestGuard(t, Config{TailscaleSocket: filepath.Join(t.TempDir(), "absent.sock")})
	e, got := testEngine(g)
	client := map[string]string{"Authorization": "Bearer client-supplied", "X-Management-Key": "client-key"}

	for _, path := range []string{"/v8/management/config", "/v0/management/config"} {
		if rec := do(e, "127.0.0.1:5000", path, client); rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		if got.authorization != "Bearer "+g.secret || got.managementKey != "" {
			t.Fatalf("%s handler saw Authorization=%q X-Management-Key=%q, want injected secret only", path, got.authorization, got.managementKey)
		}
	}
	// Proxy routes keep the client's own header, and plugin resource routes,
	// which upstream serves without a key, never see the secret.
	for _, path := range []string{"/v1/models", "/v0/resource/plugins/demo/page"} {
		do(e, "127.0.0.1:5000", path, client)
		if got.authorization != "Bearer client-supplied" {
			t.Fatalf("%s handler saw Authorization=%q, want the client's header", path, got.authorization)
		}
	}
	for _, path := range []string{"/v8/managementx", "/v0/management-evil"} {
		if isManagementPath(path) {
			t.Fatalf("isManagementPath(%q) = true", path)
		}
	}
}

func TestManagementSecretGeneration(t *testing.T) {
	g := newTestGuard(t, Config{})
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(g.secret) {
		t.Fatalf("generated secret is not 32 random bytes in hex")
	}
	if os.Getenv(managementEnv) != g.secret {
		t.Fatalf("%s was not exported", managementEnv)
	}
	other := newTestGuard(t, Config{})
	if other.secret == g.secret {
		t.Fatalf("two guards generated the same secret")
	}

	t.Setenv(managementEnv, "operator-chosen")
	kept, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if kept.secret != "operator-chosen" || os.Getenv(managementEnv) != "operator-chosen" {
		t.Fatalf("operator MANAGEMENT_PASSWORD was replaced")
	}
}

func TestDisabledGuardPassesThrough(t *testing.T) {
	off := false
	g := newTestGuard(t, Config{Enabled: &off})
	if _, set := os.LookupEnv(managementEnv); set {
		t.Fatalf("disabled guard set %s", managementEnv)
	}
	e, got := testEngine(g)
	rec := do(e, "203.0.113.9:5000", "/v8/management/config", map[string]string{"Authorization": "Bearer mine"})
	if rec.Code != http.StatusOK || got.authorization != "Bearer mine" {
		t.Fatalf("disabled guard: status %d, Authorization %q", rec.Code, got.authorization)
	}
	if got := me(t, e, "203.0.113.9:5000", nil); got.Via != "disabled" {
		t.Fatalf("/me via = %q, want disabled", got.Via)
	}
}

func TestConfigParsing(t *testing.T) {
	var cfg struct {
		Access Config `yaml:"access"`
	}
	raw := `
access:
  enabled: true
  allow-logins: ["alice@github", "someone@example.com"]
  trusted-cidrs: ["10.88.0.0/16"]
  tailscale-socket: /run/ts.sock
  cache-ttl: 30s
`
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	a := cfg.Access
	if a.Enabled == nil || !*a.Enabled || len(a.AllowLogins) != 2 || a.TrustedCIDRs[0] != "10.88.0.0/16" || a.TailscaleSocket != "/run/ts.sock" || a.CacheTTL != 30*time.Second {
		t.Fatalf("parsed %+v", a)
	}

	g := newTestGuard(t, Config{})
	if !g.enabled || g.ttl != time.Minute || strings.Join(g.AllowLogins(), ",") != "alice@github" {
		t.Fatalf("defaults: enabled=%v ttl=%s allow=%v", g.enabled, g.ttl, g.AllowLogins())
	}

	for name, bad := range map[string]Config{
		"bad cidr":     {TrustedCIDRs: []string{"10.0.0.0/33"}},
		"negative ttl": {CacheTTL: -time.Second},
		"empty logins": {AllowLogins: []string{}},
	} {
		t.Setenv(managementEnv, "x")
		if _, err := New(bad); err == nil {
			t.Fatalf("%s: New = nil error", name)
		}
	}
}

func TestConcurrentRequestsAndAllowlistUpdates(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	e, _ := testEngine(g)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if rec := do(e, adminIP+":40000", "/taproot/api/v1/me", nil); rec.Code != http.StatusOK {
					t.Errorf("allowlisted caller got %d", rec.Code)
					return
				}
				logins := []string{"alice@github"}
				if i%2 == 0 {
					logins = append(logins, "bob@example.org")
				}
				if err := g.SetAllowLogins(logins); err != nil {
					t.Errorf("SetAllowLogins: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
