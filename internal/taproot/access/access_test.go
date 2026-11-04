package access

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

// newTestGuard builds a guard with New and leaves MANAGEMENT_PASSWORD as it
// was when the test ends. A config without allow-logins admits testLogin.
func newTestGuard(t *testing.T, cfg Config) *Guard {
	t.Helper()
	t.Setenv(managementEnv, "")
	if err := os.Unsetenv(managementEnv); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	if cfg.AllowLogins == nil {
		cfg.AllowLogins = []string{testLogin}
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
	assertDenied(t, rec, "forbidden")
}

type deniedBody struct {
	Error struct{ Code, Message, Node string }
}

func assertDenied(t *testing.T, rec *httptest.ResponseRecorder, code string) deniedBody {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body)
	}
	var body deniedBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != code {
		t.Fatalf("body = %s, want {error:{code:%s}}", rec.Body, code)
	}
	if h := rec.Header().Get("Access-Control-Allow-Origin"); h != "" {
		t.Fatalf("denial carries Access-Control-Allow-Origin %q", h)
	}
	return body
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
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"laptop"}}))
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
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"laptop"}}))
	if rec := do(e, adminIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := me(t, e, adminIP+":40000", nil)
	want := meResponse{Login: "alice@github", DisplayName: "Alice", Node: "laptop.tailnet-0000.ts.net", NodeID: "nlaptopCNTRL", Via: "tailnet", Admin: true}
	if got != want {
		t.Fatalf("/me = %+v, want %+v", got, want)
	}
}

func TestAdmitsIPv6TailnetAddress(t *testing.T) {
	ts := newFakeTailscaled(t, map[string]fakePeer{"fd7a:115c:a1e0::2": {login: "alice@github", node: "laptop"}})
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	if rec := do(e, "[fd7a:115c:a1e0::2]:40000", "/v1/models", nil); rec.Code != http.StatusOK {
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
	e, got := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"laptop"}}))
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
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"laptop"}})
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
	// An unlisted node of an allowed login learns which node to list.
	body := assertDenied(t, do(e, otherNodeIP+":40000", "/v8/management/config", nil), "not_admin_node")
	if body.Error.Node != "worker."+tailnetSuffix || !strings.Contains(body.Error.Message, "access.admin-nodes") {
		t.Fatalf("not_admin_node body = %+v", body)
	}
	assertDenied(t, do(e, otherNodeIP+":40000", "/taproot/api/v1/me", nil), "not_admin_node")
	// A browser request from another site gets no detail.
	assertForbidden(t, do(e, otherNodeIP+":40000", "/v8/management/config", map[string]string{"Origin": "http://evil.example"}))
	// The server's own node is not told to list itself.
	assertForbidden(t, do(e, selfIP+":40000", "/v8/management/config", nil))

	full := newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"worker." + tailnetSuffix}})
	e, _ = testEngine(full)
	if rec := do(e, otherNodeIP+":40000", "/v8/management/config", nil); rec.Code != http.StatusOK {
		t.Fatalf("full-name entry admin status = %d, want 200", rec.Code)
	}
	assertDenied(t, do(e, adminIP+":40000", "/v8/management/config", nil), "not_admin_node")

	// A short entry does not match a node from another tailnet.
	peers := defaultPeers()
	peers["100.90.0.1"] = fakePeer{login: "alice@github", node: "laptop.other.ts.net."}
	ts2 := newFakeTailscaled(t, peers)
	e, _ = testEngine(newTestGuard(t, Config{TailscaleSocket: ts2.socket, AdminNodes: []string{"laptop"}}))
	assertDenied(t, do(e, "100.90.0.1:1", "/v8/management/config", nil), "not_admin_node")
}

// Without admin-nodes no tailnet caller is an admin, whatever its login.
// Loopback still is.
func TestEmptyAdminNodesDenyEveryTailnetCaller(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	e, _ := testEngine(g)
	for _, remote := range []string{adminIP + ":40000", otherNodeIP + ":40000"} {
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s proxy status = %d, want 200", remote, rec.Code)
		}
		for _, path := range []string{"/v8/management/config", "/v0/management/config", "/v0/resource/plugins/demo/page", "/taproot/api/v1/me"} {
			assertDenied(t, do(e, remote, path, nil), "not_admin_node")
		}
	}
	if got := me(t, e, "127.0.0.1:1", nil); !got.Admin || got.Via != string(ViaLoopback) {
		t.Fatalf("loopback /me = %+v, want admin", got)
	}

	// Listing the node at runtime applies at once.
	if err := g.SetAdminNodes([]string{" Laptop. "}); err != nil {
		t.Fatal(err)
	}
	if got := g.AdminNodes(); len(got) != 1 || got[0] != "laptop" {
		t.Fatalf("AdminNodes = %v", got)
	}
	if got := me(t, e, adminIP+":40000", nil); !got.Admin {
		t.Fatalf("listed node /me = %+v, want admin", got)
	}
	if err := g.SetAdminNodes([]string{"bad name"}); err == nil {
		t.Fatal("SetAdminNodes accepted a name with a space")
	}
	laptop := Identity{Node: "laptop." + tailnetSuffix}
	if !g.AdminNodesInclude([]string{"laptop"}, laptop) || g.AdminNodesInclude([]string{"worker"}, laptop) || g.AdminNodesInclude(nil, laptop) {
		t.Fatal("AdminNodesInclude does not match the guard's rule")
	}
}

// An "id:" entry pins a node by stable ID, so another node that later takes
// the same MagicDNS name is not an admin.
func TestAdminNodesByStableID(t *testing.T) {
	peers := defaultPeers()
	laptopID := "nlaptopCNTRL"
	ts := newFakeTailscaled(t, peers)
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"id:" + laptopID}})
	e, _ := testEngine(g)
	if rec := do(e, adminIP+":1", "/v8/management/config", nil); rec.Code != http.StatusOK {
		t.Fatalf("id-pinned node admin status = %d, want 200", rec.Code)
	}
	if got := me(t, e, adminIP+":1", nil); got.NodeID != laptopID {
		t.Fatalf("/me nodeId = %q, want %q", got.NodeID, laptopID)
	}
	assertDenied(t, do(e, otherNodeIP+":1", "/v8/management/config", nil), "not_admin_node")

	// The pinned device was renamed and another node took its old name: the
	// pin follows the device, not the name.
	peers[adminIP] = fakePeer{login: "alice@github", displayName: "Alice", node: "old-laptop"}
	peers[otherNodeIP] = fakePeer{login: "alice@github", displayName: "Alice", node: "laptop"}
	ts2 := newFakeTailscaled(t, peers)
	e, _ = testEngine(newTestGuard(t, Config{TailscaleSocket: ts2.socket, AdminNodes: []string{"id:nold-laptopCNTRL"}}))
	assertDenied(t, do(e, otherNodeIP+":1", "/v8/management/config", nil), "not_admin_node")
	if rec := do(e, adminIP+":1", "/v8/management/config", nil); rec.Code != http.StatusOK {
		t.Fatalf("renamed pinned node admin status = %d, want 200", rec.Code)
	}
	e, _ = testEngine(g)

	// IDs are case-sensitive and kept as written.
	if err := g.SetAdminNodes([]string{"ID:" + strings.ToUpper(laptopID)}); err != nil {
		t.Fatal(err)
	}
	if got := g.AdminNodes(); len(got) != 1 || got[0] != "id:"+strings.ToUpper(laptopID) {
		t.Fatalf("AdminNodes = %v", got)
	}
	assertDenied(t, do(e, adminIP+":1", "/v8/management/config", nil), "not_admin_node")
	if !g.AdminNodesInclude([]string{"id:" + laptopID}, Identity{Node: "renamed", NodeID: laptopID}) {
		t.Fatal("AdminNodesInclude ignores the stable ID")
	}
	if err := g.SetAdminNodes([]string{"id:"}); err == nil {
		t.Fatal("SetAdminNodes accepted an empty id")
	}
}

// admin-nodes matches only the control-assigned name. A whois without one
// (ComputedName comes from the device's own hostname) matches nothing.
func TestAdminNodesIgnoreComputedName(t *testing.T) {
	peers := defaultPeers()
	peers[adminIP] = fakePeer{login: "alice@github", node: "laptop", noName: true}
	ts := newFakeTailscaled(t, peers)
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"laptop"}}))
	if rec := do(e, adminIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, want 200", rec.Code)
	}
	assertDenied(t, do(e, adminIP+":40000", "/v8/management/config", nil), "not_admin_node")
}

// Every admitted caller may load the dashboard page; nobody else may.
func TestDashboardShellOpenToAdmittedCallers(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e := gin.New()
	e.Use(newTestGuard(t, Config{TailscaleSocket: ts.socket, TrustedCIDRs: []string{"192.168.0.88/32"}}).Middleware())
	e.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "shell") })
	e.HEAD("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	e.POST("/", func(c *gin.Context) { c.String(http.StatusOK, "post") })
	for _, remote := range []string{adminIP + ":1", selfIP + ":1", "192.168.0.88:1", "127.0.0.1:1"} {
		if rec := do(e, remote, "/", nil); rec.Code != http.StatusOK {
			t.Fatalf("GET / from %s = %d, want 200", remote, rec.Code)
		}
		if rec := doMethod(e, http.MethodHead, remote, "/", nil); rec.Code != http.StatusOK {
			t.Fatalf("HEAD / from %s = %d, want 200", remote, rec.Code)
		}
	}
	assertDenied(t, doMethod(e, http.MethodPost, adminIP+":1", "/", nil), "not_admin_node")
	for _, remote := range []string{outsiderIP + ":1", strayIP + ":1", "203.0.113.9:1"} {
		assertForbidden(t, do(e, remote, "/", nil))
	}
}

// GETs that change state need RequestHeader from every caller but loopback.
func TestSideEffectGETsNeedRequestHeader(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"laptop"}})
	e := gin.New()
	e.Use(g.Middleware())
	ran := 0
	handler := func(c *gin.Context) { ran++; c.String(http.StatusOK, "ok") }
	stateful := []string{
		"/v0/management/usage-queue",
		"/v8/management/observability/usage/queue",
		"/v0/management/anthropic-auth-url",
		"/v0/management/codex-auth-url",
		"/v0/management/some-plugin-auth-url",
		"/v8/management/oauth/auth-url",
		"/v0/management/get-auth-status",
		"/v8/management/oauth/status",
		"/v0/management/plugin-store",
		"/v8/management/plugins/store",
		"/v0/management/latest-version",
		"/v8/management/server/latest-version",
		"/v0/management/plugins/demo/quota",
		"/v8/management/plugins/demo/quota",
	}
	readOnly := []string{
		"/v0/management/config",
		"/v0/management/auth-files/download",
		"/v0/management/claude-keep-rolling/state",
		"/v0/resource/plugins/claude-keep-rolling/status",
		"/v0/management/oauth-callback",
		"/taproot/api/v1/settings",
	}
	for _, path := range append(slices.Clone(stateful), readOnly...) {
		e.GET(path, handler)
	}
	for _, path := range stateful {
		ran = 0
		assertDenied(t, do(e, adminIP+":1", path, nil), "request_header_required")
		assertDenied(t, do(e, adminIP+":1", path, map[string]string{RequestHeader: "yes"}), "request_header_required")
		if ran != 0 {
			t.Fatalf("%s ran without the header", path)
		}
		if rec := do(e, adminIP+":1", path, map[string]string{RequestHeader: "1"}); rec.Code != http.StatusOK {
			t.Fatalf("%s with header = %d, want 200", path, rec.Code)
		}
		if rec := do(e, "127.0.0.1:1", path, nil); rec.Code != http.StatusOK {
			t.Fatalf("loopback %s without header = %d, want 200", path, rec.Code)
		}
		// An unlisted node is told about admin-nodes first.
		assertDenied(t, do(e, otherNodeIP+":1", path, nil), "not_admin_node")
	}
	for _, path := range readOnly {
		if rec := do(e, adminIP+":1", path, nil); rec.Code != http.StatusOK {
			t.Fatalf("read-only %s without header = %d, want 200", path, rec.Code)
		}
	}
}

func TestRESPFilterAdmitsLoopbackOnly(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:5000":          true,
		"[::1]:5000":              true,
		"[::ffff:127.0.0.1]:5000": true,
		adminIP + ":5000":         false,
		selfIP + ":5000":          false,
		"192.168.0.88:5000":       false,
		"[" + selfIPv6 + "]:5000": false,
	} {
		ap := netip.MustParseAddrPort(addr)
		if got := RESPFilter(net.TCPAddrFromAddrPort(ap)); got != want {
			t.Errorf("RESPFilter(%s) = %v, want %v", addr, got, want)
		}
	}
	if RESPFilter(nil) {
		t.Error("RESPFilter(nil) = true")
	}
}

func TestBrowserChecks(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, ExtraHosts: []string{"proxy.home.arpa"}, AdminNodes: []string{"laptop"}}))
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
	kept, err := New(Config{AllowLogins: []string{testLogin}})
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
	// The proxy is open to any address, including one the guard would refuse.
	for _, remote := range []string{"203.0.113.9:5000", outsiderIP + ":40000"} {
		if rec := do(e, remote, "/v1/models", map[string]string{"Host": "rebind.evil.example"}); rec.Code != http.StatusOK {
			t.Fatalf("disabled guard proxy from %s = %d, want 200", remote, rec.Code)
		}
	}
	// Loopback reaches the admin surface; management auth is left to upstream.
	rec := do(e, "127.0.0.1:5000", "/v8/management/config", map[string]string{"Authorization": "Bearer mine"})
	if rec.Code != http.StatusOK || got.authorization != "Bearer mine" {
		t.Fatalf("disabled guard loopback: status %d, Authorization %q", rec.Code, got.authorization)
	}
	if got := me(t, e, "[::1]:5000", nil); got.Via != "disabled" {
		t.Fatalf("/me via = %q, want disabled", got.Via)
	}
}

// Turning the guard off opens the proxy only: every other address, tailnet
// or not, gets 403 on the admin surface and never reaches a handler.
func TestDisabledGuardKeepsAdminSurfaceLoopbackOnly(t *testing.T) {
	off := false
	e, got := testEngine(newTestGuard(t, Config{Enabled: &off}))
	for _, remote := range []string{"203.0.113.9:5000", adminIP + ":40000", selfIP + ":40000", "192.168.10.44:1"} {
		for _, path := range []string{"/v8/management/config", "/v0/management/config", "/v0/resource/plugins/demo/page", "/taproot/api/v1/me"} {
			got.authorization = "unset"
			assertForbidden(t, do(e, remote, path, map[string]string{"Authorization": "Bearer mine"}))
			if got.authorization != "unset" {
				t.Fatalf("denied %s from %s reached the handler", path, remote)
			}
		}
	}
	if rec := do(e, "203.0.113.9:5000", "/healthz", nil); rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", rec.Code)
	}
}

// Review finding 2: with the guard off, a browser on the same host connects
// from loopback, so a page on another site, or one reached by DNS rebinding,
// must not drive the admin surface.
func TestDisabledGuardRefusesBrowserRequestsFromOtherSites(t *testing.T) {
	off := false
	g := newTestGuard(t, Config{Enabled: &off, ExtraHosts: []string{"proxy.home.arpa"}})
	e, got := testEngine(g)
	e.GET("/v0/management/usage-queue", func(c *gin.Context) { got.authorization = "drained"; c.String(http.StatusOK, "ok") })
	for name, tc := range map[string]struct {
		path    string
		headers map[string]string
		code    string
	}{
		"dns rebinding":           {"/taproot/api/v1/me", map[string]string{"Host": "rebind.evil.example:8317"}, "forbidden"},
		"foreign origin":          {"/v8/management/config", map[string]string{"Origin": "http://evil.example"}, "forbidden"},
		"cross-site fetch":        {"/v8/management/config", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors"}, "forbidden"},
		"state-changing GET":      {"/v0/management/usage-queue", nil, "request_header_required"},
		"cross-site plugin frame": {"/v0/resource/plugins/demo/page", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Dest": "iframe"}, "forbidden"},
	} {
		got.authorization = "unset"
		assertDenied(t, do(e, "127.0.0.1:5000", tc.path, tc.headers), tc.code)
		if got.authorization != "unset" {
			t.Fatalf("%s reached the handler", name)
		}
	}
	for _, tc := range []struct {
		path    string
		headers map[string]string
	}{
		{"/v8/management/config", map[string]string{"Origin": "http://localhost:8317", "Sec-Fetch-Site": "same-origin"}},
		{"/v8/management/config", map[string]string{"Host": "proxy.home.arpa:8317"}},
		{"/v0/management/usage-queue", map[string]string{RequestHeader: "1"}},
		{"/taproot/api/v1/me", map[string]string{"Host": "127.0.0.1:8317"}},
	} {
		if rec := do(e, "127.0.0.1:5000", tc.path, tc.headers); rec.Code != http.StatusOK {
			t.Fatalf("loopback %s %v = %d, want 200", tc.path, tc.headers, rec.Code)
		}
	}
	// The proxy surface stays open, as the switch documents.
	if rec := do(e, "203.0.113.9:5000", "/v1/models", map[string]string{"Host": "rebind.evil.example"}); rec.Code != http.StatusOK {
		t.Fatalf("proxy = %d, want 200", rec.Code)
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

	g := newTestGuard(t, Config{AllowLogins: []string{" Someone@Example.com "}})
	if !g.enabled || g.ttl != time.Minute || strings.Join(g.AllowLogins(), ",") != "someone@example.com" {
		t.Fatalf("defaults: enabled=%v ttl=%s allow=%v", g.enabled, g.ttl, g.AllowLogins())
	}

	logins := []string{testLogin}
	for name, bad := range map[string]Config{
		"bad cidr":     {AllowLogins: logins, TrustedCIDRs: []string{"10.0.0.0/33"}},
		"negative ttl": {AllowLogins: logins, CacheTTL: -time.Second},
		"blank login":  {AllowLogins: []string{" "}},
	} {
		t.Setenv(managementEnv, "x")
		if _, err := New(bad); err == nil {
			t.Fatalf("%s: New = nil error", name)
		}
	}
}

// An enabled guard has no built-in login: without allow-logins it refuses to
// start rather than admit someone the operator never named.
func TestEnabledGuardRequiresAllowLogins(t *testing.T) {
	on := true
	for name, cfg := range map[string]Config{
		"absent":          {},
		"empty":           {AllowLogins: []string{}},
		"absent, enabled": {Enabled: &on},
	} {
		t.Setenv(managementEnv, "x")
		g, err := New(cfg)
		if err == nil || !strings.Contains(err.Error(), "access.allow-logins is empty") {
			t.Fatalf("%s: New = %v, %v; want the allow-logins error", name, g, err)
		}
	}
	off := false
	t.Setenv(managementEnv, "x")
	if _, err := New(Config{Enabled: &off}); err != nil {
		t.Fatalf("disabled guard without allow-logins: %v", err)
	}
}

func TestConcurrentRequestsAndAllowlistUpdates(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, AdminNodes: []string{"laptop"}})
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
