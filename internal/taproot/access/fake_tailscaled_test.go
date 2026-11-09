package access

import (
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTailscaled serves the LocalAPI whois and status endpoints on a real
// unix socket. tailscaled itself cannot run inside a unit test; the live
// check against a real tailscaled is TestLiveTailscaled.
type fakeTailscaled struct {
	socket string

	mu          sync.Mutex
	peers       map[string]fakePeer
	fail        map[string]int
	calls       map[string]int
	statusFails int
	statusCalls int
	// listFails fails the next status?peers=true reads, and backend, when
	// set, replaces their BackendState.
	listFails int
	backend   string
	// noPeers answers status?peers=true with no peers, as a tailscaled that
	// is still starting does.
	noPeers bool
}

type fakePeer struct {
	login, displayName, node string
	// noName leaves Node.Name empty, so only ComputedName carries the node.
	noName bool
	// noID leaves Node.StableID empty.
	noID bool
	os   string
	// created is when the node joined; zero means long ago.
	created time.Time
	// sharee is a device of a person the server is shared with: tailscaled
	// hides its name and OS, as measured on tailscaled 1.104.2.
	sharee bool
	// offline devices report lastSeen.
	offline  bool
	lastSeen time.Time
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

// stableID is the fake's stable node ID for a node name.
func stableID(node string) string {
	return "n" + strings.TrimSuffix(node, ".") + "CNTRL"
}

// nodeName is the control-assigned MagicDNS name, with its trailing dot. A
// node ending in a dot is a full name from another tailnet.
func (p fakePeer) nodeName() string {
	if strings.HasSuffix(p.node, ".") {
		return p.node
	}
	return p.node + "." + tailnetSuffix + "."
}

func (p fakePeer) tags() []string {
	if p.login == "tagged-devices" {
		return []string{"tag:server"}
	}
	return nil
}

// serve mirrors ipn/localapi serveWhoIsWithBackend: Host must be the LocalAPI
// host, addr is required, unknown peers get 404 "no match for IP:port".
func (f *fakeTailscaled) serve(w http.ResponseWriter, r *http.Request) {
	if r.Host != localAPIHost {
		http.Error(w, "invalid localapi request", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/localapi/v0/status" {
		if r.FormValue("peers") == "true" {
			f.serveTailnet(w)
		} else {
			f.serveStatus(w)
		}
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
	name, id := p.nodeName(), stableID(p.node)
	computed := p.node
	if p.noName || p.sharee {
		name = ""
	}
	if p.sharee {
		computed = hiddenHostName
	}
	if p.noID {
		id = ""
	}
	// Field set taken from apitype.WhoIsResponse with tailcfg.Node and tailcfg.UserProfile.
	resp := map[string]any{
		"Node": map[string]any{
			"ID":           12345,
			"StableID":     id,
			"Name":         name,
			"ComputedName": computed,
			"User":         777,
			"Tags":         p.tags(),
			"Addresses":    []string{addr + "/32"},
			"Hostinfo":     map[string]any{"Hostname": computed, "OS": "linux"},
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

// selfStatus is the server's own ipnstate.PeerStatus.
func selfStatus() map[string]any {
	return map[string]any{
		"ID":           "nserverCNTRL",
		"PublicKey":    "nodekey:0000",
		"HostName":     "server",
		"DNSName":      "server." + tailnetSuffix + ".",
		"OS":           "linux",
		"UserID":       777,
		"TailscaleIPs": []string{selfIP, selfIPv6},
		"Created":      "2026-01-05T10:00:00Z",
		"Online":       true,
	}
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
		"Self":           selfStatus(),
	})
}

// serveTailnet mirrors /localapi/v0/status?peers=true: Peer is keyed by node
// key, and User by the decimal user ID. Every address of a node in f.peers
// is one peer; the node "server" is Self.
func (f *fakeTailscaled) serveTailnet(w http.ResponseWriter) {
	f.mu.Lock()
	failing := f.listFails > 0
	if failing {
		f.listFails--
	}
	backend := f.backend
	if backend == "" {
		backend = "Running"
	}
	peers := map[string]any{}
	users := map[string]any{"777": map[string]any{"ID": 777, "LoginName": testLogin, "DisplayName": "Alice"}}
	userIDs := map[string]int{testLogin: 777}
	if !f.noPeers {
		addrs := map[string][]string{}
		byNode := map[string]fakePeer{}
		for addr, p := range f.peers {
			if p.node == "server" {
				continue
			}
			addrs[p.node] = append(addrs[p.node], addr)
			byNode[p.node] = p
		}
		for node, p := range byNode {
			uid, ok := userIDs[p.login]
			if !ok {
				uid = 1000 + len(userIDs)
				userIDs[p.login] = uid
				users[strconv.Itoa(uid)] = map[string]any{"ID": uid, "LoginName": p.login, "DisplayName": p.displayName}
			}
			created := p.created
			if created.IsZero() {
				created = time.Date(2025, 3, 1, 9, 0, 0, 0, time.UTC)
			}
			status := map[string]any{
				"ID":           stableID(node),
				"PublicKey":    "nodekey:" + node,
				"HostName":     strings.TrimSuffix(node, "."),
				"DNSName":      p.nodeName(),
				"OS":           p.os,
				"UserID":       uid,
				"Tags":         p.tags(),
				"TailscaleIPs": addrs[node],
				"Created":      created.Format(time.RFC3339Nano),
				"Online":       !p.offline,
				"LastSeen":     p.lastSeen.Format(time.RFC3339Nano),
				"ShareeNode":   false,
			}
			if p.sharee {
				status["HostName"], status["DNSName"], status["OS"], status["Tags"] = hiddenHostName, "", "", nil
				status["Created"], status["ShareeNode"] = "1973-11-11T00:00:00.1Z", true
			}
			peers["nodekey:"+node] = status
		}
	}
	f.mu.Unlock()
	if failing {
		http.Error(w, "backend unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"Version":        "1.90.0",
		"BackendState":   backend,
		"TailscaleIPs":   []string{selfIP, selfIPv6},
		"MagicDNSSuffix": tailnetSuffix,
		"Self":           selfStatus(),
		"Peer":           peers,
		"User":           users,
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

func (f *fakeTailscaled) removePeer(addr string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.peers, addr)
}

// setList controls the next status?peers=true answers: fails failed reads,
// then backend as BackendState ("" for Running), with no peers when noPeers.
func (f *fakeTailscaled) setList(fails int, backend string, noPeers bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listFails, f.backend, f.noPeers = fails, backend, noPeers
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
	laptopID      = "nlaptopCNTRL"
	workerID      = "nworkerCNTRL"
	bobID         = "nbob-pcCNTRL"
	serverID      = "nserverCNTRL"
)

func defaultPeers() map[string]fakePeer {
	return map[string]fakePeer{
		selfIP:      {login: "alice@github", displayName: "Alice", node: "server"},
		selfIPv6:    {login: "alice@github", displayName: "Alice", node: "server"},
		adminIP:     {login: "alice@github", displayName: "Alice", node: "laptop", os: "macOS"},
		otherNodeIP: {login: "alice@github", displayName: "Alice", node: "worker", os: "linux"},
		outsiderIP:  {login: "bob@example.org", displayName: "Bob", node: "bob-pc", os: "windows"},
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
