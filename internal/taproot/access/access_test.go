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

// defaultDevices is the test tailnet's access: laptop has both permissions,
// worker has Proxy only, and bob-pc and the server are not listed.
var defaultDevices = []DeviceGrant{
	{ID: laptopID, Name: "laptop", Proxy: true, Admin: true},
	{ID: workerID, Name: "worker", Proxy: true},
}

// newTestGuard builds a guard with New and leaves MANAGEMENT_PASSWORD as it
// was when the test ends. A config with nil Devices gets defaultDevices; pass
// an empty list for none.
func newTestGuard(t *testing.T, cfg Config) *Guard {
	t.Helper()
	t.Setenv(managementEnv, "")
	if err := os.Unsetenv(managementEnv); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	if cfg.Devices == nil {
		cfg.Devices = defaultDevices
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
	e.GET("/v1/models/:id", record)
	e.POST("/v1/models", record)
	e.POST("/v1/messages", record)
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
	body := assertDenied(t, rec, "forbidden")
	if body.Error.Node != "" || body.Error.NodeID != "" || body.Error.Login != "" {
		t.Fatalf("a plain denial names a device: %s", rec.Body)
	}
}

type deniedBody struct {
	Error struct{ Code, Message, Node, NodeID, Login string }
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

// assertDeviceDenied checks a denial that names the refused device and the
// switch to turn on.
func assertDeviceDenied(t *testing.T, rec *httptest.ResponseRecorder, code, node, nodeID, login string) {
	t.Helper()
	body := assertDenied(t, rec, code)
	if body.Error.Node != node || body.Error.NodeID != nodeID || body.Error.Login != login {
		t.Fatalf("%s names %q %q %q, want %q %q %q", code, body.Error.Node, body.Error.NodeID, body.Error.Login, node, nodeID, login)
	}
	want := map[string]string{"no_proxy": "Open http://", "not_admin": "turn on Admin for this one on the Access page"}[code]
	if !strings.Contains(body.Error.Message, node) || !strings.Contains(body.Error.Message, nodeID) || !strings.Contains(body.Error.Message, want) {
		t.Fatalf("%s message %q does not name the device and the switch", code, body.Error.Message)
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
			assertForbidden(t, do(e, remote, "/", nil))
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
		assertForbidden(t, do(e, adminIP+":40000", "/", nil))
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
		assertForbidden(t, do(e, strayIP+":40000", "/", nil))
	})
	t.Run("no node ID", func(t *testing.T) {
		// Permissions attach to the stable ID, so a whois without one admits nothing.
		peers := defaultPeers()
		peers[adminIP] = fakePeer{login: testLogin, node: "laptop", noID: true}
		ts := newFakeTailscaled(t, peers)
		e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
		assertForbidden(t, do(e, adminIP+":40000", "/v1/models", nil))
		assertForbidden(t, do(e, adminIP+":40000", "/v8/management/config", nil))
	})
}

// A device nobody turned on is refused on both surfaces, and each denial
// tells it its own name, login, and ID, and which switch to turn on. It may
// load the static dashboard page, which shows the same.
func TestUnlistedDeviceIsToldWhatToTurnOn(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, got := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	got.authorization = "unset"
	assertDeviceDenied(t, do(e, outsiderIP+":40000", "/v1/models", nil), "no_proxy", "bob-pc", bobID, "bob@example.org")
	assertDeviceDenied(t, do(e, outsiderIP+":40000", "/v8/management/config", nil), "not_admin", "bob-pc", bobID, "bob@example.org")
	assertDeviceDenied(t, do(e, outsiderIP+":40000", "/taproot/api/v1/me", nil), "not_admin", "bob-pc", bobID, "bob@example.org")
	if got.authorization != "unset" {
		t.Fatal("a refused device reached a handler")
	}
	// Another site's page in that device's browser learns nothing.
	assertForbidden(t, do(e, outsiderIP+":40000", "/v1/models", map[string]string{"Origin": "http://evil.example"}))
	assertForbidden(t, do(e, outsiderIP+":40000", "/v8/management/config", map[string]string{"Host": "evil.example"}))
}

func TestSpoofedIdentityHeadersChangeNothing(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, got := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
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
	got.authorization = "unset"
	for _, remote := range []string{"203.0.113.9:5000", outsiderIP + ":40000", strayIP + ":40000"} {
		for _, path := range []string{"/v1/models", "/v8/management/config", "/taproot/api/v1/me"} {
			rec := do(e, remote, path, spoof)
			if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "alice") {
				t.Fatalf("%s from %s with spoofed headers = %d %s", path, remote, rec.Code, rec.Body)
			}
		}
	}
	if got.authorization != "unset" {
		t.Fatal("a spoofed request reached a handler")
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
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: missing, Devices: []DeviceGrant{}}))
	for _, remote := range []string{"127.0.0.1:5000", "127.5.6.7:5000", "[::1]:5000", "[::ffff:127.0.0.1]:5000"} {
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", remote, rec.Code)
		}
		if got := me(t, e, remote, nil); got.Via != "loopback" || !got.Admin || !got.Proxy {
			t.Fatalf("%s /me = %+v, want loopback with both permissions", remote, got)
		}
	}
}

func TestAdmitsGrantedDevice(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	if rec := do(e, adminIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := me(t, e, adminIP+":40000", nil)
	want := meResponse{Login: "alice@github", DisplayName: "Alice", Node: "laptop.tailnet-0000.ts.net", NodeID: laptopID, Via: "tailnet", Proxy: true, Admin: true}
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

// Each switch works on its own: Proxy without Admin, and Admin without Proxy.
// Admin alone also reads the model list, which the dashboard shows.
func TestPermissionMatrix(t *testing.T) {
	peers := defaultPeers()
	peers["100.64.0.30"] = fakePeer{login: testLogin, displayName: "Alice", node: "phone"}
	ts := newFakeTailscaled(t, peers)
	devices := []DeviceGrant{
		{ID: laptopID, Proxy: true, Admin: true},
		{ID: workerID, Proxy: true},
		{ID: stableID("phone"), Admin: true},
	}
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: devices}))
	for _, c := range []struct {
		name, remote         string
		proxy, models, admin string // "" admits; otherwise the denial code
	}{
		{"both", adminIP, "", "", ""},
		{"proxy only", otherNodeIP, "", "", "not_admin"},
		{"admin only", "100.64.0.30", "no_proxy", "", ""},
		{"neither", outsiderIP, "no_proxy", "no_proxy", "not_admin"},
		{"server", selfIP, "", "", "forbidden"},
		{"loopback", "127.0.0.1", "", "", ""},
	} {
		for _, r := range []struct{ method, path, want string }{
			{http.MethodPost, "/v1/messages", c.proxy},
			{http.MethodGet, "/v1/models", c.models},
			{http.MethodGet, "/v8/management/config", c.admin},
		} {
			rec := doMethod(e, r.method, c.remote+":40000", r.path, nil)
			surface, want := r.path, r.want
			if want == "" {
				if rec.Code != http.StatusOK {
					t.Errorf("%s %s = %d %s, want 200", c.name, surface, rec.Code, rec.Body)
				}
				continue
			}
			assertDenied(t, rec, want)
		}
		// Every caller with an identity loads the dashboard page.
		if rec := do(e, c.remote+":40000", "/", nil); rec.Code != http.StatusNotFound && rec.Code != http.StatusOK {
			t.Errorf("%s / = %d, want past the guard", c.name, rec.Code)
		}
	}
}

func TestAdmitsAddressesWithoutWhois(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	addresses := []AddressGrant{{CIDR: "192.168.10.0/24"}, {CIDR: " 10.88.0.0/16 ", Note: "podman"}, {CIDR: "192.168.0.88"}}
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Addresses: addresses})
	e, _ := testEngine(g)
	for _, remote := range []string{"192.168.10.44:1", "10.88.3.2:1", "192.168.0.88:1"} {
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", remote, rec.Code)
		}
		// Addresses hold host-local addresses that every local process
		// shares, so they never reach the admin surface.
		assertForbidden(t, do(e, remote, "/v8/management/config", nil))
		assertForbidden(t, do(e, remote, "/taproot/api/v1/me", nil))
	}
	assertForbidden(t, do(e, "192.168.0.89:1", "/v1/models", nil))
	assertForbidden(t, do(e, "192.168.11.1:1", "/v1/models", nil))
	if n := ts.callCount("192.168.10.44"); n != 0 {
		t.Fatalf("whois called %d times for an address in access.addresses", n)
	}
	want := []AddressGrant{{CIDR: "192.168.10.0/24"}, {CIDR: "10.88.0.0/16", Note: "podman"}, {CIDR: "192.168.0.88/32"}}
	if got := g.Grants().Addresses; !slices.Equal(got, want) {
		t.Fatalf("addresses = %+v, want %+v", got, want)
	}
}

func TestSelfNodeReachesProxyOnly(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	// Even listed with Admin, the server's own node never gets it.
	devices := append(slices.Clone(defaultDevices), DeviceGrant{ID: serverID, Admin: true})
	e, got := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: devices}))
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
		t.Fatalf("other granted node /me = %+v, want admin", got)
	}
	// Unlisted, it still has Proxy.
	e, _ = testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{}}))
	if rec := do(e, selfIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("unlisted self node proxy status = %d, want 200", rec.Code)
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

// ServerNodeID reads tailscaled when no request has looked the node up yet,
// so the settings checks never compare against an empty cache.
func TestServerNodeIDReadsTailscaled(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	if got := g.ServerNodeID(t.Context()); got != serverID {
		t.Fatalf("ServerNodeID with an empty cache = %q, want %q", got, serverID)
	}
	if got := g.ServerNodeID(t.Context()); got != serverID || ts.statusCount() != 1 {
		t.Fatalf("ServerNodeID = %q after %d status calls, want %q from the cache", got, ts.statusCount(), serverID)
	}

	// A failed refresh keeps the last known node.
	ts.failStatus(1000)
	now = now.Add(defaultCacheTTL)
	if got := g.ServerNodeID(t.Context()); got != serverID || ts.statusCount() != 2 {
		t.Fatalf("ServerNodeID after a failed refresh = %q (%d status calls), want %q", got, ts.statusCount(), serverID)
	}

	// Never read: unknown.
	unread := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	if got := unread.ServerNodeID(t.Context()); got != "" {
		t.Fatalf("ServerNodeID while tailscaled fails = %q, want empty", got)
	}
	off := false
	if got := newTestGuard(t, Config{Enabled: &off}).ServerNodeID(t.Context()); got != "" {
		t.Fatalf("ServerNodeID with the guard off = %q, want empty", got)
	}
}

func TestUnknownSelfFailsClosedForAdmin(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	e, _ := testEngine(g)
	ts.failStatus(1000)

	// The proxy still works for granted devices using a local name, and
	// loopback keeps admin access.
	if rec := do(e, adminIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, want 200", rec.Code)
	}
	assertForbidden(t, do(e, adminIP+":40000", "/v8/management/config", nil))
	// Until the server's node is known, it is just a device without a grant.
	assertDenied(t, do(e, selfIP+":40000", "/v1/models", nil), "no_proxy")
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
	if rec := do(e, selfIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("known self node proxy status = %d, want 200", rec.Code)
	}
	assertForbidden(t, do(e, selfIP+":40000", "/v8/management/config", nil))
	if got := g.ServerNodeID(t.Context()); got != serverID {
		t.Fatalf("ServerNodeID = %q, want %q", got, serverID)
	}

	// A later failure keeps the last known node.
	ts.failStatus(1000)
	now = now.Add(2 * time.Minute)
	assertForbidden(t, do(e, selfIP+":40000", "/v8/management/config", nil))
	if rec := do(e, adminIP+":40000", "/v8/management/config", map[string]string{"Host": "server:8317"}); rec.Code != http.StatusOK {
		t.Fatalf("with a stale self node admin status = %d, want 200", rec.Code)
	}
}

// Names grant nothing: the stable ID decides, so a device that takes the
// name of an admin device gets nothing, and a renamed admin device keeps
// Admin.
func TestStableIDDecidesNotName(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{{ID: laptopID, Name: "worker", Proxy: true, Admin: true}}})
	e, _ := testEngine(g)
	if rec := do(e, adminIP+":1", "/v8/management/config", nil); rec.Code != http.StatusOK {
		t.Fatalf("granted ID admin status = %d, want 200", rec.Code)
	}
	// worker is the name in the entry, but its ID is not listed.
	assertDeviceDenied(t, do(e, otherNodeIP+":1", "/v1/models", nil), "no_proxy", "worker", workerID, testLogin)

	// The laptop is renamed, and the worker takes its old name.
	ts.setPeer(adminIP, fakePeer{login: testLogin, displayName: "Alice", node: "new-laptop"})
	ts.setPeer(otherNodeIP, fakePeer{login: testLogin, displayName: "Alice", node: "laptop"})
	g2 := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{{ID: "nnew-laptopCNTRL", Name: "laptop", Proxy: true, Admin: true}}})
	e, _ = testEngine(g2)
	assertDeviceDenied(t, do(e, otherNodeIP+":1", "/v8/management/config", nil), "not_admin", "laptop", laptopID, testLogin)
	if rec := do(e, adminIP+":1", "/v8/management/config", nil); rec.Code != http.StatusOK {
		t.Fatalf("renamed granted node admin status = %d, want 200", rec.Code)
	}

	// IDs are case-sensitive.
	if err := g.SetGrants(Grants{Devices: []DeviceGrant{{ID: strings.ToUpper(laptopID), Admin: true, Proxy: true}}}); err != nil {
		t.Fatal(err)
	}
	e, _ = testEngine(g)
	assertDenied(t, do(e, adminIP+":1", "/v8/management/config", nil), "not_admin")
}

// A tagged device has no user login. It is granted by ID like any other
// device; the server's own node keeps Proxy whether tagged or not.
func TestTaggedDevices(t *testing.T) {
	peers := defaultPeers()
	tagged := fakePeer{login: "tagged-devices", displayName: "Tagged Devices", node: "server"}
	peers[selfIP], peers[selfIPv6] = tagged, tagged
	peers["100.64.0.20"] = fakePeer{login: "tagged-devices", displayName: "Tagged Devices", node: "ci-runner"}
	ts := newFakeTailscaled(t, peers)
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	e, _ := testEngine(g)

	for _, remote := range []string{selfIP + ":40000", "[" + selfIPv6 + "]:40000"} {
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s proxy status = %d, want 200; body %s", remote, rec.Code, rec.Body)
		}
		assertForbidden(t, do(e, remote, "/v8/management/config", nil))
		assertForbidden(t, do(e, remote, "/taproot/api/v1/me", nil))
	}
	id, reason := g.admit(t.Context(), selfIP+":40000", func() *selfNode { return g.self(t.Context()) })
	if reason != "" || !id.self || id.Admin {
		t.Fatalf("admit(self) = %+v, %q; want the proxy-only own node", id, reason)
	}
	if d := g.deviceFor(id); d.ID != LocalDeviceID {
		t.Fatalf("own node device = %+v, want %s", d, LocalDeviceID)
	}
	assertDeviceDenied(t, do(e, "100.64.0.20:40000", "/v1/models", nil), "no_proxy", "ci-runner", stableID("ci-runner"), "tagged-devices")
	if err := g.SetGrants(Grants{Devices: []DeviceGrant{{ID: stableID("ci-runner"), Proxy: true}}}); err != nil {
		t.Fatal(err)
	}
	if rec := do(e, "100.64.0.20:40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("granted tagged device proxy status = %d, want 200; body %s", rec.Code, rec.Body)
	}

	// When whois fails, the own address in status still admits the node. A
	// fresh guard has no cached whois answer, so the lookup really fails.
	fresh := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	freshEngine, _ := testEngine(fresh)
	before := ts.callCount(selfIP)
	ts.failNext(selfIP, 1)
	if rec := do(freshEngine, selfIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("own node with whois failing: proxy status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	if ts.callCount(selfIP) != before+1 {
		t.Fatalf("whois calls for the own address = %d, want %d", ts.callCount(selfIP), before+1)
	}
}

// An address the console moved from the server to another node is still in
// the cached status list until the next refresh. whois names the node that
// holds it now, so that node gets no own-node pass and no admin access.
func TestReassignedOwnAddressIsNotTheServer(t *testing.T) {
	peers := defaultPeers()
	peers[selfIP] = fakePeer{login: "mallory@example.org", displayName: "Mallory", node: "intruder"}
	ts := newFakeTailscaled(t, peers)
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	assertDenied(t, do(e, selfIP+":40000", "/v1/models", nil), "no_proxy")

	// Granted, the node reaches the proxy as itself, not as the server, and
	// is kept off the admin surface while status still lists the address as
	// the server's.
	devices := append(slices.Clone(defaultDevices), DeviceGrant{ID: stableID("intruder"), Proxy: true, Admin: true})
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: devices})
	e, _ = testEngine(g)
	if rec := do(e, selfIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("granted device on a reassigned address: proxy status = %d, want 200", rec.Code)
	}
	assertForbidden(t, do(e, selfIP+":40000", "/v8/management/config", nil))
	id, reason := g.admit(t.Context(), selfIP+":40000", func() *selfNode { return g.self(t.Context()) })
	if reason != "" || id.self || id.NodeID != "nintruderCNTRL" {
		t.Fatalf("admit(reassigned) = %+v, %q; want node nintruderCNTRL, not the server", id, reason)
	}
}

// With no device holding Admin, no tailnet caller is an admin. Loopback
// still is, and a grant made at runtime applies at once.
func TestNoAdminDevices(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{{ID: laptopID, Proxy: true}, {ID: workerID, Proxy: true}}})
	e, _ := testEngine(g)
	for _, remote := range []string{adminIP + ":40000", otherNodeIP + ":40000"} {
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Fatalf("%s proxy status = %d, want 200", remote, rec.Code)
		}
		for _, path := range []string{"/v8/management/config", "/v0/management/config", "/v0/resource/plugins/demo/page", "/taproot/api/v1/me"} {
			assertDenied(t, do(e, remote, path, nil), "not_admin")
		}
	}
	if got := me(t, e, "127.0.0.1:1", nil); !got.Admin || got.Via != string(ViaLoopback) {
		t.Fatalf("loopback /me = %+v, want admin", got)
	}
	if err := g.SetGrants(Grants{Devices: []DeviceGrant{{ID: " " + laptopID + " ", Admin: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := me(t, e, adminIP+":40000", nil); !got.Admin || got.Proxy {
		t.Fatalf("admin-only device /me = %+v, want admin without proxy", got)
	}
	assertDenied(t, doMethod(e, http.MethodPost, adminIP+":40000", "/v1/messages", nil), "no_proxy")
}

// Every caller with an identity may load the dashboard page; nobody else may.
func TestDashboardShellOpenToIdentifiedCallers(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e := gin.New()
	e.Use(newTestGuard(t, Config{TailscaleSocket: ts.socket, Addresses: []AddressGrant{{CIDR: "192.168.0.88/32"}}}).Middleware())
	e.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "shell") })
	e.HEAD("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	e.POST("/", func(c *gin.Context) { c.String(http.StatusOK, "post") })
	for _, remote := range []string{adminIP + ":1", otherNodeIP + ":1", outsiderIP + ":1", selfIP + ":1", "192.168.0.88:1", "127.0.0.1:1"} {
		if rec := do(e, remote, "/", nil); rec.Code != http.StatusOK {
			t.Fatalf("GET / from %s = %d, want 200", remote, rec.Code)
		}
		if rec := doMethod(e, http.MethodHead, remote, "/", nil); rec.Code != http.StatusOK {
			t.Fatalf("HEAD / from %s = %d, want 200", remote, rec.Code)
		}
	}
	assertDenied(t, doMethod(e, http.MethodPost, otherNodeIP+":1", "/", nil), "not_admin")
	assertDenied(t, doMethod(e, http.MethodPost, outsiderIP+":1", "/", nil), "not_admin")
	for _, remote := range []string{strayIP + ":1", "203.0.113.9:1"} {
		assertForbidden(t, do(e, remote, "/", nil))
	}
}

// The dashboard's chunks answer exactly the callers the page does, in both
// guard modes. With the guard off, "/" is loopback-only, so the chunks must
// not fall to the proxy surface, which the switch opens to every address.
func TestDashboardAssetsFollowThePage(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	off := false
	crossSite := map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "script"}
	rebound := map[string]string{"Host": "rebind.evil.example"}
	for mode, cfg := range map[string]Config{
		"enabled":  {TailscaleSocket: ts.socket, Addresses: []AddressGrant{{CIDR: "192.168.0.88/32"}}},
		"disabled": {Enabled: &off},
	} {
		t.Run(mode, func(t *testing.T) {
			e := gin.New()
			e.Use(newTestGuard(t, cfg).Middleware())
			for _, path := range []string{"/", "/assets/index-abc.js"} {
				e.GET(path, func(c *gin.Context) { c.String(http.StatusOK, "static") })
				e.HEAD(path, func(c *gin.Context) { c.Status(http.StatusOK) })
				e.POST(path, func(c *gin.Context) { c.String(http.StatusOK, "post") })
			}
			admitted := 0
			for _, remote := range []string{"127.0.0.1:1", adminIP + ":1", selfIP + ":1", "192.168.0.88:1", outsiderIP + ":1", strayIP + ":1", "203.0.113.9:1"} {
				for _, req := range []struct {
					method  string
					headers map[string]string
				}{{http.MethodGet, nil}, {http.MethodHead, nil}, {http.MethodPost, nil}, {http.MethodGet, crossSite}, {http.MethodGet, rebound}} {
					page := doMethod(e, req.method, remote, "/", req.headers).Code
					asset := doMethod(e, req.method, remote, "/assets/index-abc.js", req.headers).Code
					if page != asset {
						t.Errorf("%s from %s with %v: / = %d, asset = %d", req.method, remote, req.headers, page, asset)
					}
					if page == http.StatusOK && req.method == http.MethodGet && req.headers == nil {
						admitted++
					}
				}
			}
			// Loopback, the admin device, the server's own node, the address,
			// and the unlisted device when enabled; loopback alone when disabled.
			if want := map[string]int{"enabled": 5, "disabled": 1}[mode]; admitted != want {
				t.Errorf("%d callers loaded the page, want %d", admitted, want)
			}
		})
	}
}

// GETs that change state need RequestHeader from every caller, loopback too.
func TestSideEffectGETsNeedRequestHeader(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
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
		"/taproot/api/v1/access/devices",
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
		// Review finding 9: loopback does not depend on the browser sending
		// Sec-Fetch-* either.
		assertDenied(t, do(e, "127.0.0.1:1", path, nil), "request_header_required")
		if rec := do(e, "127.0.0.1:1", path, map[string]string{RequestHeader: "1"}); rec.Code != http.StatusOK {
			t.Fatalf("loopback %s with header = %d, want 200", path, rec.Code)
		}
		// A device without Admin is told about Admin first.
		assertDenied(t, do(e, otherNodeIP+":1", path, nil), "not_admin")
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
		"/taproot/api/v1/access/devices":   surfaceAdmin,
		"/taproot":                         surfaceAdmin,
		"/v0/resource/plugins/x/index.htm": surfaceAdmin,
		"/assets/index-abc.js":             surfaceAdmin,
		"/assetsx":                         surfaceProxy,
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
	// The address now belongs to another device; after the TTL the guard sees it.
	ts.setPeer(adminIP, fakePeer{login: "bob@example.org", node: "bob-pc"})
	now = now.Add(time.Second)
	assertDenied(t, do(e, adminIP+":40000", "/v1/models", nil), "no_proxy")
	if n := ts.callCount(adminIP); n != 2 {
		t.Fatalf("whois calls after TTL = %d, want 2", n)
	}
}

func TestSetGrantsAppliesImmediately(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	e, _ := testEngine(g)
	assertDenied(t, do(e, outsiderIP+":40000", "/v1/models", nil), "no_proxy")

	with := Grants{Devices: append(slices.Clone(defaultDevices), DeviceGrant{ID: bobID, Name: " bob-pc ", Proxy: true})}
	if err := g.SetGrants(with); err != nil {
		t.Fatalf("SetGrants: %v", err)
	}
	if rec := do(e, outsiderIP+":40000", "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("after grant status = %d, want 200", rec.Code)
	}
	if got := g.Grants().Devices; len(got) != 3 || got[2] != (DeviceGrant{ID: bobID, Name: "bob-pc", Proxy: true}) {
		t.Fatalf("Devices = %+v", got)
	}

	// A device with neither permission is dropped from the list.
	if err := g.SetGrants(Grants{Devices: append(slices.Clone(defaultDevices), DeviceGrant{ID: bobID})}); err != nil {
		t.Fatalf("SetGrants: %v", err)
	}
	assertDenied(t, do(e, outsiderIP+":40000", "/v1/models", nil), "no_proxy")
	if n := ts.callCount(outsiderIP); n != 1 {
		t.Fatalf("whois calls = %d, want 1 (identity cached, decision not)", n)
	}
	if got := g.Grants().Devices; !slices.Equal(got, defaultDevices) {
		t.Fatalf("Devices = %+v, want %+v", got, defaultDevices)
	}

	for name, bad := range map[string]Grants{
		"blank id":      {Devices: []DeviceGrant{{ID: "  ", Proxy: true}}},
		"id with space": {Devices: []DeviceGrant{{ID: "n1 2", Proxy: true}}},
		"id with colon": {Devices: []DeviceGrant{{ID: "id:n1", Proxy: true}}},
		"duplicate id":  {Devices: []DeviceGrant{{ID: laptopID, Proxy: true}, {ID: laptopID, Admin: true}}},
		"bad cidr":      {Addresses: []AddressGrant{{CIDR: "10.0.0.0/33"}}},
		"not an addr":   {Addresses: []AddressGrant{{CIDR: "laptop"}}},
		"duplicate":     {Addresses: []AddressGrant{{CIDR: "10.0.0.1"}, {CIDR: "10.0.0.1/32"}}},
		"tailnet range": {Addresses: []AddressGrant{{CIDR: "100.64.0.0/10"}}},
		"tailnet IPv6":  {Addresses: []AddressGrant{{CIDR: "fd7a:115c:a1e0::5"}}},
		"wider":         {Addresses: []AddressGrant{{CIDR: "64.0.0.0/2"}}},
	} {
		if err := g.SetGrants(bad); err == nil {
			t.Fatalf("SetGrants(%s) = nil, want error", name)
		}
	}
	if got := g.Grants().Devices; !slices.Equal(got, defaultDevices) {
		t.Fatalf("rejected update changed the list: %+v", got)
	}
	g.Grants().Devices[0].Admin = false
	if got := g.Grants().Devices; !got[0].Admin {
		t.Fatalf("Grants exposes internal state: %+v", got)
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
		for _, path := range []string{"/v8/management/config", "/v0/management/config", "/v0/resource/plugins/demo/page", "/taproot/api/v1/me", "/taproot/api/v1/access/devices"} {
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
  devices:
    - id: nQ7v2xK8CNTRL
      name: alice-desktop
      proxy: true
      admin: true
    - {id: nH3m9pL2CNTRL, admin: true}
  addresses:
    - cidr: 192.0.2.20/32
      note: Agents on the server host
  tailscale-socket: /run/ts.sock
  cache-ttl: 30s
`
	dec := yaml.NewDecoder(strings.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	a := cfg.Access
	wantDevices := []DeviceGrant{{ID: "nQ7v2xK8CNTRL", Name: "alice-desktop", Proxy: true, Admin: true}, {ID: "nH3m9pL2CNTRL", Admin: true}}
	if a.Enabled == nil || !*a.Enabled || !slices.Equal(a.Devices, wantDevices) || a.Addresses[0] != (AddressGrant{CIDR: "192.0.2.20/32", Note: "Agents on the server host"}) ||
		a.TailscaleSocket != "/run/ts.sock" || a.CacheTTL != 30*time.Second {
		t.Fatalf("parsed %+v", a)
	}

	// A device entry is strict too.
	dec = yaml.NewDecoder(strings.NewReader("access:\n  devices:\n    - {id: n1, proxy: true, login: alice@github}\n"))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err == nil || !strings.Contains(err.Error(), "login") {
		t.Fatalf("unknown device key: %v", err)
	}

	g := newTestGuard(t, Config{})
	if !g.enabled || g.ttl != time.Minute {
		t.Fatalf("defaults: enabled=%v ttl=%s", g.enabled, g.ttl)
	}

	for name, bad := range map[string]Config{
		"bad cidr":     {Addresses: []AddressGrant{{CIDR: "10.0.0.0/33"}}},
		"negative ttl": {CacheTTL: -time.Second},
		"blank id":     {Devices: []DeviceGrant{{ID: " ", Proxy: true}}},
	} {
		t.Setenv(managementEnv, "x")
		if _, err := New(bad); err == nil {
			t.Fatalf("%s: New = nil error", name)
		}
	}
}

// The keys from before per-device permissions name their replacement.
func TestRemovedKeysNameTheirReplacement(t *testing.T) {
	for key, want := range map[string]string{
		"allow-logins":  "access.devices",
		"admin-nodes":   "admin: true",
		"trusted-cidrs": "access.addresses",
	} {
		err := RemovedKey(key)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "access."+key) {
			t.Errorf("RemovedKey(%s) = %v, want a message naming %s", key, err, want)
		}
	}
	for _, key := range []string{"devices", "addresses", "enabled", "extra-hosts"} {
		if err := RemovedKey(key); err != nil {
			t.Errorf("RemovedKey(%s) = %v", key, err)
		}
	}
}

// An enabled guard starts with no devices listed: no tailnet device gets in,
// and loopback is still an admin.
func TestEnabledGuardStartsWithoutDevices(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{}}))
	for _, remote := range []string{adminIP, otherNodeIP, outsiderIP} {
		assertDenied(t, do(e, remote+":1", "/v1/models", nil), "no_proxy")
		assertDenied(t, do(e, remote+":1", "/v8/management/config", nil), "not_admin")
	}
	if got := me(t, e, "127.0.0.1:1", nil); !got.Admin {
		t.Fatalf("loopback /me = %+v, want admin", got)
	}
}

func TestConcurrentRequestsAndGrantUpdates(t *testing.T) {
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
					t.Errorf("granted caller got %d", rec.Code)
					return
				}
				devices := slices.Clone(defaultDevices)
				if i%2 == 0 {
					devices = append(devices, DeviceGrant{ID: bobID, Proxy: true})
				}
				if err := g.SetGrants(Grants{Devices: devices}); err != nil {
					t.Errorf("SetGrants: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
