package access

import (
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeTailscaled serves the LocalAPI whois endpoint on a real unix socket.
// tailscaled itself cannot run inside a unit test; the live check against a
// real tailscaled is TestLiveTailscaled.
type fakeTailscaled struct {
	socket string

	mu          sync.Mutex
	peers       map[string]fakePeer
	fail        map[string]int
	calls       map[string]int
	statusFails int
	statusCalls int
}

type fakePeer struct {
	login, displayName, node string
	// noName leaves Node.Name empty, so only ComputedName carries the node.
	noName bool
}

func newFakeTailscaled(t *testing.T, peers map[string]fakePeer) *fakeTailscaled {
	t.Helper()
	// Short path: unix socket paths are limited to about 108 bytes.
	dir, err := os.MkdirTemp("", "ts")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeTailscaled{
		socket: filepath.Join(dir, "ts.sock"),
		peers:  peers,
		fail:   map[string]int{},
		calls:  map[string]int{},
	}
	ln, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

// serve mirrors ipn/localapi serveWhoIsWithBackend: Host must be the LocalAPI
// host, addr is required, unknown peers get 404 "no match for IP:port".
func (f *fakeTailscaled) serve(w http.ResponseWriter, r *http.Request) {
	if r.Host != localAPIHost {
		http.Error(w, "invalid localapi request", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/localapi/v0/status" {
		f.serveStatus(w)
		return
	}
	if r.URL.Path != "/localapi/v0/whois" {
		http.NotFound(w, r)
		return
	}
	addr := r.FormValue("addr")
	if addr == "" {
		http.Error(w, "missing 'addr' parameter", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.calls[addr]++
	failing := f.fail[addr] > 0
	if failing {
		f.fail[addr]--
	}
	p, ok := f.peers[addr]
	f.mu.Unlock()
	if failing {
		http.Error(w, "backend unavailable", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "no match for IP:port", http.StatusNotFound)
		return
	}
	// A node ending in a dot is a full name from another tailnet.
	name := p.node + "." + tailnetSuffix + "."
	if p.noName {
		name = ""
	} else if strings.HasSuffix(p.node, ".") {
		name = p.node
	}
	// Field set taken from apitype.WhoIsResponse with tailcfg.Node and tailcfg.UserProfile.
	resp := map[string]any{
		"Node": map[string]any{
			"ID":           12345,
			"StableID":     "n" + p.node + "CNTRL",
			"Name":         name,
			"ComputedName": p.node,
			"User":         777,
			"Addresses":    []string{addr + "/32"},
			"Hostinfo":     map[string]any{"Hostname": p.node, "OS": "linux"},
		},
		"UserProfile": map[string]any{
			"ID":            777,
			"LoginName":     p.login,
			"DisplayName":   p.displayName,
			"ProfilePicURL": "https://example.invalid/pic.png",
		},
		"CapMap": map[string]any{},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// serveStatus mirrors /localapi/v0/status?peers=false with the fields of
// ipnstate.Status and ipnstate.PeerStatus that tailscaled reports.
func (f *fakeTailscaled) serveStatus(w http.ResponseWriter) {
	f.mu.Lock()
	f.statusCalls++
	failing := f.statusFails > 0
	if failing {
		f.statusFails--
	}
	f.mu.Unlock()
	if failing {
		http.Error(w, "backend unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"Version":        "1.90.0",
		"BackendState":   "Running",
		"TailscaleIPs":   []string{selfIP, selfIPv6},
		"MagicDNSSuffix": tailnetSuffix,
		"Self": map[string]any{
			"ID":           "nserverCNTRL",
			"PublicKey":    "nodekey:0000",
			"HostName":     "server",
			"DNSName":      "server." + tailnetSuffix + ".",
			"OS":           "linux",
			"UserID":       777,
			"TailscaleIPs": []string{selfIP, selfIPv6},
			"Online":       true,
		},
	})
}

func (f *fakeTailscaled) failStatus(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusFails = n
}

func (f *fakeTailscaled) statusCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusCalls
}

func (f *fakeTailscaled) callCount(addr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[addr]
}

func (f *fakeTailscaled) failNext(addr string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[addr] = n
}

func (f *fakeTailscaled) setPeer(addr string, p fakePeer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.peers[addr] = p
}

// Example addresses and names, in the shape tailscale status and whois report
// them.
const (
	tailnetSuffix = "tailnet-0000.ts.net"
	selfIP        = "100.64.0.1"
	selfIPv6      = "fd7a:115c:a1e0::1"
	adminIP       = "100.64.0.2"
	otherNodeIP   = "100.101.1.2"
	outsiderIP    = "100.64.0.9"
	strayIP       = "100.100.1.1"
	testLogin     = "alice@github"
)

func defaultPeers() map[string]fakePeer {
	return map[string]fakePeer{
		selfIP:      {login: "alice@github", displayName: "Alice", node: "server"},
		selfIPv6:    {login: "alice@github", displayName: "Alice", node: "server"},
		adminIP:     {login: "alice@github", displayName: "Alice", node: "laptop"},
		otherNodeIP: {login: "alice@github", displayName: "Alice", node: "worker"},
		outsiderIP:  {login: "bob@example.org", displayName: "Bob", node: "bob-pc"},
	}
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return addr
}
