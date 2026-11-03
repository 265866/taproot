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
	return doMethod(e, http.MethodGet, remote, path, headers)
}

// doMethod sends a request the way a command-line client does: Host is the
// server name it was given (localhost unless headers set Host), with no
// Origin or Sec-Fetch-* headers unless headers add them.
func doMethod(e http.Handler, method, remote, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	req.Host = "localhost:8317"
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
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
	want := meResponse{Login: "alice@github", DisplayName: "Alice", Node: "laptop.tailnet-0000.ts.net", Via: "tailnet", Admin: true}
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
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", remote, rec.Code)
		}
		// trusted-cidrs hold host-local addresses that every local process
		// shares, so they never reach the admin surface.
		assertForbidden(t, do(e, remote, "/v8/management/config", nil))
		assertForbidden(t, do(e, remote, "/taproot/api/v1/me", nil))
	}
	assertForbidden(t, do(e, "192.168.11.1:1", "/v1/models", nil))
	if n := ts.callCount("192.168.10.44"); n != 0 {
		t.Fatalf("whois called %d times for a trusted CIDR", n)
	}
}

func TestSelfNodeReachesProxyOnly(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, got := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	for _, remote := range []string{selfIP + ":40000", "[" + selfIPv6 + "]:40000"} {
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("self node %s proxy status = %d, want 200", remote, rec.Code)
		}
		got.authorization = ""
		for _, path := range []string{"/v8/management/config", "/v0/management/config", "/v0/resource/plugins/demo/page", "/taproot/api/v1/me"} {
			assertForbidden(t, do(e, remote, path, nil))
		}
		if got.authorization != "" {
			t.Fatalf("a denied self-node request reached a handler")
		}
	}
	if got := me(t, e, adminIP+":40000", nil); !got.Admin {
		t.Fatalf("other allowlisted node /me = %+v, want admin", got)
	}
}

func TestSelfNodeMatchedByStableIDToo(t *testing.T) {
	// A second address on the server's node, which status did not list,
	// still resolves to the node's StableID.
	peers := defaultPeers()
	peers["100.127.0.9"] = fakePeer{login: "alice@github", node: "server"}
	ts := newFakeTailscaled(t, peers)
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	if rec := do(e, "100.127.0.9:1", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, want 200", rec.Code)
	}
	assertForbidden(t, do(e, "100.127.0.9:1", "/v8/management/config", nil))
}

func TestUnknownSelfFailsClosedForAdmin(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	e, _ := testEngine(g)
	ts.failStatus(1000)

	// The proxy still works for allowlisted tailnet callers using a local
	// name, and loopback keeps admin access.
	for _, remote := range []string{adminIP + ":40000", selfIP + ":40000"} {
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s proxy status = %d, want 200", remote, rec.Code)
		}
		assertForbidden(t, do(e, remote, "/v8/management/config", nil))
	}
	if rec := do(e, "127.0.0.1:1", "/v8/management/config", nil); rec.Code != http.StatusOK {
		t.Fatalf("loopback admin status = %d, want 200", rec.Code)
	}
	// Without the server's own names, a tailnet Host cannot be verified.
	assertForbidden(t, do(e, adminIP+":40000", "/v1/models", map[string]string{"Host": "server:8317"}))
	if n := ts.statusCount(); n != 1 {
		t.Fatalf("status calls within the retry interval = %d, want 1", n)
	}

	// Once status answers, the decision follows it.
	ts.failStatus(0)
	now = now.Add(selfRetry)
	if rec := do(e, adminIP+":40000", "/v8/management/config", map[string]string{"Host": "server:8317"}); rec.Code != http.StatusOK {
		t.Fatalf("after recovery admin status = %d, want 200", rec.Code)
	}
	assertForbidden(t, do(e, selfIP+":40000", "/v8/management/config", nil))

	// A later failure keeps the last known node.
	ts.failStatus(1000)
	now = now.Add(2 * time.Minute)
	assertForbidden(t, do(e, selfIP+":40000", "/v8/management/config", nil))
	if rec := do(e, adminIP+":40000", "/v8/management/config", map[string]string{"Host": "server:8317"}); rec.Code != http.StatusOK {
		t.Fatalf("with a stale self node admin status = %d, want 200", rec.Code)
	}
}

func TestAdminNodes(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"Laptop", "nowhere.other.ts.net."}}))
	if rec := do(e, adminIP+":40000", "/v8/management/config", nil); rec.Code != http.StatusOK {
		t.Fatalf("listed node admin status = %d, want 200", rec.Code)
	}
	if rec := do(e, otherNodeIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("unlisted node proxy status = %d, want 200", rec.Code)
	}
	assertForbidden(t, do(e, otherNodeIP+":40000", "/v8/management/config", nil))
	assertForbidden(t, do(e, otherNodeIP+":40000", "/taproot/api/v1/me", nil))

	full := newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"worker." + tailnetSuffix}})
	e, _ = testEngine(full)
	if rec := do(e, otherNodeIP+":40000", "/v8/management/config", nil); rec.Code != http.StatusOK {
		t.Fatalf("full-name entry admin status = %d, want 200", rec.Code)
	}
	assertForbidden(t, do(e, adminIP+":40000", "/v8/management/config", nil))

	// A short entry does not match a node from another tailnet.
	peers := defaultPeers()
	peers["100.90.0.1"] = fakePeer{login: "alice@github", node: "laptop.other.ts.net."}
	ts2 := newFakeTailscaled(t, peers)
	e, _ = testEngine(newTestGuard(t, Config{TailscaleSocket: ts2.socket, AdminNodes: []string{"laptop"}}))
	assertForbidden(t, do(e, "100.90.0.1:1", "/v8/management/config", nil))
}

func TestBrowserChecks(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, ExtraHosts: []string{"proxy.home.arpa"}}))
	admin := adminIP + ":40000"
	for _, tc := range []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		want    int
	}{
		{"cli short name", "GET", "/v1/models", map[string]string{"Host": "server:8317"}, 200},
		{"cli full name", "GET", "/v1/models", map[string]string{"Host": "server." + tailnetSuffix + ":8317"}, 200},
		{"cli full name trailing dot", "GET", "/v1/models", map[string]string{"Host": "SERVER." + tailnetSuffix + ".:8317"}, 200},
		{"cli tailnet ip", "GET", "/v1/models", map[string]string{"Host": selfIP + ":8317"}, 200},
		{"cli tailnet ipv6", "GET", "/v1/models", map[string]string{"Host": "[" + selfIPv6 + "]:8317"}, 200},
		{"cli loopback", "GET", "/v1/models", map[string]string{"Host": "127.0.0.1:8317"}, 200},
		{"cli ipv6 loopback", "GET", "/v1/models", map[string]string{"Host": "[::1]:8317"}, 200},
		{"extra host", "GET", "/v1/models", map[string]string{"Host": "proxy.home.arpa"}, 200},
		{"rebound name", "GET", "/v1/models", map[string]string{"Host": "evil.example:8317"}, 403},
		{"rebound name admin", "GET", "/v8/management/config", map[string]string{"Host": "server.evil.example:8317"}, 403},
		{"other tailnet ip", "GET", "/v1/models", map[string]string{"Host": adminIP + ":8317"}, 403},
		{"empty host", "GET", "/v1/models", map[string]string{"Host": ""}, 403},
		{"same origin", "PUT", "/v8/management/config", map[string]string{"Host": "server:8317", "Origin": "http://server:8317", "Sec-Fetch-Site": "same-origin"}, 200},
		{"same origin https", "GET", "/v8/management/config", map[string]string{"Host": "server." + tailnetSuffix, "Origin": "https://server." + tailnetSuffix}, 200},
		{"user typed url", "GET", "/taproot/api/v1/me", map[string]string{"Host": "server:8317", "Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, 200},
		{"foreign origin", "GET", "/v8/management/config", map[string]string{"Host": "server:8317", "Origin": "http://evil.example"}, 403},
		{"foreign origin proxy", "POST", "/v1/models", map[string]string{"Host": "server:8317", "Origin": "http://evil.example"}, 403},
		{"other port same host", "GET", "/v8/management/config", map[string]string{"Host": "server:8317", "Origin": "http://server:9925"}, 403},
		{"other own name", "GET", "/v8/management/config", map[string]string{"Host": "server:8317", "Origin": "http://localhost:8317"}, 403},
		{"null origin", "GET", "/v8/management/config", map[string]string{"Host": "server:8317", "Origin": "null"}, 403},
		{"cross-site no origin", "GET", "/v8/management/config", map[string]string{"Host": "server:8317", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors"}, 403},
		{"same-site", "GET", "/v1/models", map[string]string{"Host": "server:8317", "Sec-Fetch-Site": "same-site"}, 403},
		{"cross-site iframe of a navigable path", "GET", "/taproot/api/v1/me", map[string]string{"Host": "server:8317", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}, 403},
		{"cross-site navigation to an api", "GET", "/taproot/api/v1/me", map[string]string{"Host": "server:8317", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, 403},
		{"foreign websocket", "GET", "/v1/models", map[string]string{"Host": "server:8317", "Origin": "http://evil.example", "Connection": "Upgrade", "Upgrade": "websocket"}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doMethod(e, tc.method, admin, tc.path, tc.headers)
			if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
				rec.Code = http.StatusOK // past the guard; the test engine has no such route
			}
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestManagementHTMLIsGone(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.sock")
	g := newTestGuard(t, Config{TailscaleSocket: missing})
	e, _ := testEngine(g)
	e.GET("/management.html", func(c *gin.Context) { c.String(http.StatusOK, "panel") })
	for _, remote := range []string{"127.0.0.1:1", "203.0.113.9:1"} {
		if rec := do(e, remote, "/management.html", nil); rec.Code != http.StatusNotFound {
			t.Fatalf("/management.html from %s = %d, want 404", remote, rec.Code)
		}
	}
}

func TestClassify(t *testing.T) {
	for path, want := range map[string]surface{
		"/":                                surfaceAdmin,
		"/v0/management":                   surfaceAdmin,
		"/v0/management/auth-files":        surfaceAdmin,
		"/v8/management/oauth/callback":    surfaceAdmin,
		"/taproot/api/v1/me":               surfaceAdmin,
		"/taproot":                         surfaceAdmin,
		"/v0/resource/plugins/x/index.htm": surfaceAdmin,
		"/anthropic/callback":              surfaceAdmin,
		"/callback":                        surfaceAdmin,
		"/v1/ws":                           surfaceAdmin,
		"/v1/models":                       surfaceProxy,
		"/v1/messages":                     surfaceProxy,
		"/v1/responses":                    surfaceProxy,
		"/backend-api/codex/responses":     surfaceProxy,
		"/v8/managementx":                  surfaceProxy,
		"/taprootx":                        surfaceProxy,
		"/api/hello":                       surfaceProxy,
	} {
		if got := classify(path); got != want {
			t.Errorf("classify(%q) = %s, want %s", path, got, want)
		}
	}
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
