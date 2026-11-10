package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

// captureLog records what the standard logger writes until the test ends.
func captureLog(t *testing.T) *test.Hook {
	t.Helper()
	hook := test.NewGlobal()
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	return hook
}

func logged(hook *test.Hook, substr string) []string {
	var out []string
	for _, e := range hook.AllEntries() {
		if strings.Contains(e.Message, substr) {
			out = append(out, e.Message)
		}
	}
	return out
}

// Any caller can put a percent-encoded newline in the path; the denial line
// must not let it start a forged log line.
func TestDenialLogQuotesThePath(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	off := false
	disabled, _ := testEngine(newTestGuard(t, Config{Enabled: &off}))
	hook := captureLog(t)
	forged := "/v1/x%0Ataproot%20access:%20setup%20code%20ABCD-EFGH"
	assertForbidden(t, do(e, "203.0.113.9:40000", forged, nil))
	assertForbidden(t, do(disabled, "203.0.113.9:40000", "/taproot"+forged, nil))
	lines := logged(hook, "denied")
	if len(lines) != 2 {
		t.Fatalf("denial lines = %q, want 2", lines)
	}
	for _, line := range lines {
		if strings.ContainsAny(line, "\r\n") || !strings.Contains(line, `x\ntaproot access: setup code`) {
			t.Errorf("denial line %q does not quote the path", line)
		}
	}
}

// admit unmaps a 4-in-6 caller, so a 4-in-6 entry in access.addresses is
// stored as the IPv4 prefix it means, and a tailnet one is refused like its
// IPv4 form.
func TestMappedAddressGrantsMatch(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Addresses: []AddressGrant{{CIDR: "::ffff:192.0.2.20/128"}, {CIDR: "::ffff:198.51.100.7"}}})
	if got := g.Grants().Addresses; got[0].CIDR != "192.0.2.20/32" || got[1].CIDR != "198.51.100.7/32" {
		t.Fatalf("stored addresses = %+v, want IPv4 prefixes", got)
	}
	e, _ := testEngine(g)
	for _, remote := range []string{"192.0.2.20:1", "[::ffff:192.0.2.20]:1", "198.51.100.7:1"} {
		if rec := do(e, remote, "/v1/models", nil); rec.Code != http.StatusOK {
			t.Errorf("proxy from %s = %d %s, want 200", remote, rec.Code, rec.Body)
		}
	}
	if err := CheckGrants(Grants{Addresses: []AddressGrant{{CIDR: "::ffff:100.64.0.5/128"}}}); err == nil || !strings.Contains(err.Error(), "covers Tailscale addresses") {
		t.Fatalf("a 4-in-6 tailnet address = %v, want refused", err)
	}
}

// When the server's own node changes, as after tailscale switch, an address
// cached from before does not keep its old device's identity.
func TestWhoisCacheClearedWhenServerNodeChanges(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, CacheTTL: 10 * time.Minute})
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	self := selfNode{StableID: serverID, DNSName: "server." + tailnetSuffix, IPs: []netip.Addr{mustAddr(t, selfIP)}}
	g.status = func(context.Context) (selfNode, error) { return self, nil }
	e, _ := testEngine(g)
	g.ServerNodeID(t.Context())
	// Cached until 15 minutes in, while the server's node is refreshed at 10.
	now = now.Add(5 * time.Minute)
	if m := me(t, e, adminIP+":40000", nil); !m.Admin {
		t.Fatalf("laptop /me = %+v, want admin", m)
	}
	ts.setPeer(adminIP, fakePeer{login: "bob@example.org", node: "bob-pc"})

	now = now.Add(time.Minute)
	if m := me(t, e, adminIP+":40000", nil); !m.Admin {
		t.Fatalf("cached laptop /me = %+v, want admin", m)
	}
	// The server moves to another tailnet: the refresh clears the cache.
	self = selfNode{StableID: "nserver2CNTRL", DNSName: "server.tailnet-1111.ts.net", IPs: []netip.Addr{mustAddr(t, selfIP)}}
	now = now.Add(4 * time.Minute)
	g.ServerNodeID(t.Context())
	assertDenied(t, do(e, adminIP+":40000", "/v8/management/config", nil), "not_admin")
	if n := ts.callCount(adminIP); n != 2 {
		t.Fatalf("whois calls = %d, want 2", n)
	}
}

func TestCacheTTLIsBounded(t *testing.T) {
	t.Setenv(managementEnv, "test")
	for ttl, ok := range map[time.Duration]bool{0: true, maxCacheTTL: true, maxCacheTTL + time.Second: false, time.Hour: false, -time.Second: false} {
		_, err := New(Config{CacheTTL: ttl})
		if (err == nil) != ok {
			t.Errorf("cache-ttl %s: %v, want ok %t", ttl, err, ok)
		}
	}
}

// The server's own node, and an admin device while the server's node is
// unknown, are told why with their own codes, not that they are off the
// tailnet.
func TestServerNodeDenials(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	e, _ := testEngine(g)
	ts.failStatus(1000)
	body := assertDenied(t, do(e, adminIP+":40000", "/taproot/api/v1/me", nil), "server_node_unknown")
	if !strings.Contains(body.Error.Message, "try again") {
		t.Errorf("server_node_unknown message %q", body.Error.Message)
	}
	ts.failStatus(0)
	now = now.Add(selfRetry)
	body = assertDenied(t, do(e, selfIP+":40000", "/taproot/api/v1/me", nil), "server_node")
	if !strings.Contains(body.Error.Message, "another device") {
		t.Errorf("server_node message %q", body.Error.Message)
	}
	// A device off the tailnet still learns nothing.
	assertForbidden(t, do(e, "203.0.113.9:40000", "/taproot/api/v1/me", nil))
}

// A browser that opens taproot by a name it does not answer to gets a page
// that says what to do; an API client gets JSON with its own code. Neither
// names the server's real names, which a DNS-rebinding page could read.
func TestUnknownHostDenial(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	e, got := testEngine(g)
	browser := map[string]string{"Host": "taproot.home.arpa:8317", "Accept": "text/html,application/xhtml+xml,*/*;q=0.8"}
	for _, remote := range []string{"127.0.0.1:1", adminIP + ":40000"} {
		got.authorization = ""
		rec := do(e, remote, "/", browser)
		page := rec.Body.String()
		if rec.Code != http.StatusForbidden || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("browser from %s = %d %s %q", remote, rec.Code, rec.Header().Get("Content-Type"), page)
		}
		if !strings.Contains(page, "<code>taproot.home.arpa:8317</code>") || !strings.Contains(page, "access.extra-hosts") {
			t.Errorf("page does not name the host and access.extra-hosts: %s", page)
		}
		if strings.Contains(page, tailnetSuffix) || strings.Contains(page, selfIP) || strings.Contains(page, "tailscaled") {
			t.Errorf("page names the server or says its names are unknown: %s", page)
		}
		if got.authorization != "" || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("an unknown host reached a handler or got CORS headers")
		}
	}
	rec := do(e, "127.0.0.1:1", "/", map[string]string{"Host": "<b>x</b>", "Accept": "text/html"})
	if strings.Contains(rec.Body.String(), "<b>") {
		t.Errorf("page does not escape the host: %s", rec.Body)
	}
	body := assertDenied(t, do(e, "127.0.0.1:1", "/v8/management/config", map[string]string{"Host": "taproot.home.arpa"}), "unknown_host")
	if !strings.Contains(body.Error.Message, "access.extra-hosts") {
		t.Errorf("unknown_host message %q", body.Error.Message)
	}

	// While the server's names cannot be read, the page says so.
	unread := newTestGuard(t, Config{TailscaleSocket: filepath.Join(t.TempDir(), "absent.sock")})
	e, _ = testEngine(unread)
	rec = do(e, "127.0.0.1:1", "/", map[string]string{"Host": "server:8317", "Accept": "text/html"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "cannot read its own Tailscale name") {
		t.Errorf("page while tailscaled is unreadable = %d %s", rec.Code, rec.Body)
	}
	// With the guard off, the admin surface still refuses the name, and the
	// page does not blame tailscaled.
	off := false
	e, _ = testEngine(newTestGuard(t, Config{Enabled: &off}))
	rec = do(e, "127.0.0.1:1", "/", map[string]string{"Host": "taproot.home.arpa", "Accept": "text/html"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "access.extra-hosts") || strings.Contains(rec.Body.String(), "tailscaled") {
		t.Errorf("page with the guard off = %d %s", rec.Code, rec.Body)
	}
}

// Plugin OAuth providers redirect the browser to the management callbacks on
// 127.0.0.1, which a browser on the server reaches from loopback with
// Sec-Fetch-Site: cross-site.
func TestPluginOAuthCallbacksAcceptNavigation(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	e, _ := testEngine(newTestGuard(t, Config{TailscaleSocket: ts.socket}))
	reached := 0
	for _, path := range []string{"/v0/management/oauth-callback", "/v8/management/oauth/callback"} {
		e.GET(path, func(c *gin.Context) { reached++; c.Status(http.StatusOK) })
	}
	navigate := map[string]string{"Host": "127.0.0.1:8317", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
	for _, path := range []string{"/v0/management/oauth-callback?state=s&code=c", "/v8/management/oauth/callback?state=s&code=c"} {
		if rec := do(e, "127.0.0.1:1", path, navigate); rec.Code != http.StatusOK {
			t.Errorf("navigation to %s = %d %s, want 200", path, rec.Code, rec.Body)
		}
	}
	if reached != 2 {
		t.Fatalf("callbacks reached %d times, want 2", reached)
	}
	// Only a top-level navigation: a cross-site fetch is still refused.
	fetch := map[string]string{"Host": "127.0.0.1:8317", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}
	assertForbidden(t, do(e, "127.0.0.1:1", "/v0/management/oauth-callback?state=s", fetch))
}

// A LocalAPI response over the limit is an error that says so, not a
// truncated body that fails to decode.
func TestLocalAPIResponseOverLimit(t *testing.T) {
	dir, err := os.MkdirTemp("", "ts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "ts.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	size := localAPIMaxBody + 1
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"Pad":"%s"}`, strings.Repeat("x", size-10))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	client := newLocalClient(socket)

	if _, err := client.get(t.Context(), "/localapi/v0/status?peers=false", localAPIMaxBody); err == nil || !strings.Contains(err.Error(), "longer than 1 MiB") {
		t.Fatalf("body of %d bytes with a 1 MiB limit = %v, want a length error", size, err)
	}
	body, err := client.get(t.Context(), "/localapi/v0/status?peers=true", localAPIMaxPeerList)
	if err != nil || len(body) != size {
		t.Fatalf("peer list of %d bytes = %d bytes, %v", size, len(body), err)
	}
	size = localAPIMaxBody
	if body, err := client.get(t.Context(), "/localapi/v0/status?peers=false", localAPIMaxBody); err != nil || len(body) != size {
		t.Fatalf("body of exactly 1 MiB = %d bytes, %v", len(body), err)
	}
	if _, err := client.tailnet(t.Context()); errors.Is(err, errPeerNotFound) {
		t.Fatal("unexpected 404")
	}
}

// One device's wrong codes lock only that device out; the server-wide
// ceiling still bounds guessing across many devices.
func TestClaimLimitIsPerDevice(t *testing.T) {
	g := newTestGuard(t, Config{TailscaleSocket: newFakeTailscaled(t, defaultPeers()).socket, Devices: []DeviceGrant{}})
	code := startClaim(t, g).last()
	wrong := "AAAAAAAA"
	if code == wrong {
		wrong = "BBBBBBBB"
	}
	start := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	// Another device sends five wrong codes every minute; the owner's right
	// code half a minute in still works.
	for i := range claimMaxFailures {
		if err := g.CheckClaimCode(bobID, wrong, start.Add(time.Duration(i)*time.Second)); !errors.Is(err, ErrClaimWrongCode) {
			t.Fatalf("wrong code %d = %v", i+1, err)
		}
	}
	var limited *ClaimRateLimitedError
	if err := g.CheckClaimCode(bobID, code, start.Add(30*time.Second)); !errors.As(err, &limited) {
		t.Fatalf("right code from the locked device = %v, want a rate limit", err)
	}
	if err := g.CheckClaimCode(laptopID, code, start.Add(30*time.Second)); err != nil {
		t.Fatalf("right code from another device = %v, want accepted", err)
	}

	// Many devices together reach the server-wide ceiling.
	g = newTestGuard(t, Config{TailscaleSocket: newFakeTailscaled(t, defaultPeers()).socket, Devices: []DeviceGrant{}})
	code = startClaim(t, g).last()
	for i := range claimMaxTotalFailures {
		device := fmt.Sprintf("n%dCNTRL", i/claimMaxFailures)
		if err := g.CheckClaimCode(device, wrong, start.Add(time.Duration(i)*time.Second)); !errors.Is(err, ErrClaimWrongCode) {
			t.Fatalf("wrong code %d from %s = %v", i+1, device, err)
		}
	}
	if err := g.CheckClaimCode(laptopID, code, start.Add(40*time.Second)); !errors.As(err, &limited) || limited.RetryAfter != 20*time.Second {
		t.Fatalf("right code at the ceiling = %v, want a rate limit with 20s left", err)
	}
	if err := g.CheckClaimCode(laptopID, code, start.Add(time.Minute)); err != nil {
		t.Fatalf("right code once the oldest failure is a minute old = %v", err)
	}
}

// When the last device with Admin is removed at runtime, as auto-forget or a
// loopback edit does, a new setup code is made and announced.
func TestClaimRestartsWhenTheLastAdminIsRemoved(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	codes := startClaim(t, g)
	if codes.count() != 0 || g.Claimable() {
		t.Fatal("a claim started while a device has Admin")
	}
	if err := g.SetGrants(Grants{Devices: []DeviceGrant{{ID: workerID, Proxy: true}}}); err != nil {
		t.Fatal(err)
	}
	if codes.count() != 1 || !g.Claimable() {
		t.Fatalf("after the last Admin was removed: %d codes, claimable %t", codes.count(), g.Claimable())
	}
	e, _ := testEngine(g)
	var denial struct{ Error map[string]any }
	rec := do(e, outsiderIP+":1", "/v8/management/config", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &denial); err != nil || denial.Error["claimable"] != true {
		t.Fatalf("not_admin after the restart = %s, want claimable", rec.Body)
	}
	// Further edits without Admin keep the same code.
	if err := g.SetGrants(Grants{Devices: []DeviceGrant{}}); err != nil {
		t.Fatal(err)
	}
	if codes.count() != 1 || g.CheckClaimCode(bobID, codes.last(), time.Now()) != nil {
		t.Fatalf("an edit without Admin changed the code: %d codes", codes.count())
	}
	// Without StartClaim, as before Setup runs, nothing is announced.
	idle := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	if err := idle.SetGrants(Grants{}); err != nil || idle.Claimable() {
		t.Fatalf("SetGrants before StartClaim = %v, claimable %t", err, idle.Claimable())
	}
}

// admin: true on the server's own node grants nothing, so it does not count
// as claimed, whether the node is known at start or only later.
func TestAdminOnServerNodeDoesNotClaim(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	hook := captureLog(t)
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{{ID: serverID, Name: "server", Admin: true}}})
	codes := startClaim(t, g)
	if codes.count() != 1 || !g.Claimable() {
		t.Fatalf("with Admin only on the server's node: %d codes, claimable %t", codes.count(), g.Claimable())
	}
	if lines := logged(hook, "this server's own node and never gets Admin"); len(lines) != 1 || !strings.Contains(lines[0], serverID) {
		t.Fatalf("warning lines = %q", lines)
	}

	// tailscaled is down at start: the grant cannot be told apart, so it
	// counts until the node is read.
	ts.failStatus(1000)
	g = newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{{ID: serverID, Admin: true}}})
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	codes = startClaim(t, g)
	if codes.count() != 0 {
		t.Fatal("a code while the server's node is unknown")
	}
	ts.failStatus(0)
	now = now.Add(selfRetry)
	if g.ServerNodeID(t.Context()) != serverID || codes.count() != 1 || !g.Claimable() {
		t.Fatalf("once the node is read: %d codes, claimable %t", codes.count(), g.Claimable())
	}
}

// A device that asks for access without a name is logged by its Tailscale name.
func TestSelfRequestLogNamesTheDevice(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	hook := captureLog(t)
	rec := postRequest(e, outsiderIP+":1", `{}`, map[string]string{RequestHeader: "1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("request = %d %s", rec.Code, rec.Body)
	}
	if lines := logged(hook, "asked for access"); len(lines) != 1 || !strings.HasPrefix(lines[0], `taproot access: "bob-pc" (`) {
		t.Fatalf("log lines = %q", lines)
	}
}
