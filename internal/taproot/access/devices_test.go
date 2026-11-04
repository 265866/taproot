package access

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"
)

// TestRequestDevicesOutliveTheRequestBriefly checks a running request stays
// known however long it runs, and a finished one for requestGrace only.
func TestRequestDevicesOutliveTheRequestBriefly(t *testing.T) {
	var r requestDevices
	start := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	laptop := Device{ID: "node:n1", Name: "laptop"}
	r.start("ws", laptop)
	r.start("http", laptop)
	r.finish("http", start)

	// Pruning runs at most once a minute, on a finish.
	r.finish("other", start.Add(requestGrace+2*time.Minute))
	if _, ok := r.get("http"); ok {
		t.Error("finished request still known after the grace period")
	}
	if d, ok := r.get("ws"); !ok || d != laptop {
		t.Errorf("running websocket = %+v %t, want laptop", d, ok)
	}

	r.finish("ws", start.Add(requestGrace+3*time.Minute))
	if _, ok := r.get("ws"); !ok {
		t.Error("websocket forgotten as soon as it finished")
	}
	r.finish("other", start.Add(2*requestGrace+5*time.Minute))
	if _, ok := r.get("ws"); ok {
		t.Error("websocket still known after the grace period")
	}
}

// interfaces returns a fixed interface address list, as net.InterfaceAddrs
// reports it, and counts the calls.
func interfaces(calls *int, cidrs ...string) func() ([]net.Addr, error) {
	return func() ([]net.Addr, error) {
		*calls++
		out := make([]net.Addr, 0, len(cidrs))
		for _, c := range cidrs {
			ip, ipnet, err := net.ParseCIDR(c)
			if err != nil {
				return nil, err
			}
			ipnet.IP = ip
			out = append(out, ipnet)
		}
		return out, nil
	}
}

// serverInterfaces is what a taproot container under rootless Podman lists: loopback and
// the LAN address pasta copies from the host (measured in /proc/net/fib_trie).
var serverInterfaces = []string{"127.0.0.1/8", "::1/128", "192.168.0.88/24", "fe80::5054:ff:fe12:3456/64"}

func TestDeviceNames(t *testing.T) {
	var calls int
	g := &Guard{now: time.Now, own: ownAddrs{list: interfaces(&calls, serverInterfaces...)}}
	g.selfNode = &selfNode{StableID: "nserver", DNSName: "server.tailnet-0000.ts.net"}
	g.selfExpires = time.Now().Add(time.Hour)
	for _, tc := range []struct {
		id   Identity
		want Device
	}{
		{Identity{Via: ViaLoopback, Node: "127.0.0.1"}, Device{ID: "local", Name: "server (local)", Via: ViaLoopback}},
		{
			Identity{Via: ViaTailnet, Login: "a@github", Node: "server.tailnet-0000.ts.net", NodeID: "nserver", self: true},
			Device{ID: "local", Name: "server (local)", Login: "a@github", Via: ViaTailnet},
		},
		// The request path stores the bare address; DeviceName names it at read time.
		{Identity{Via: ViaCIDR, Node: "192.168.0.88"}, Device{ID: "cidr:192.168.0.88", Name: "192.168.0.88", Via: ViaCIDR}},
		{Identity{Via: ViaCIDR, Node: "192.168.0.20"}, Device{ID: "cidr:192.168.0.20", Name: "192.168.0.20", Via: ViaCIDR}},
		{
			Identity{Via: ViaTailnet, Login: "a@github", Node: "desktop.tailnet-0000.ts.net", NodeID: "nDesk"},
			Device{ID: "node:nDesk", Name: "desktop", Login: "a@github", Via: ViaTailnet},
		},
		{
			Identity{Via: ViaTailnet, Login: "b@github", Node: "shared.other.ts.net", NodeID: "nShared"},
			Device{ID: "node:nShared", Name: "shared.other.ts.net", Login: "b@github", Via: ViaTailnet},
		},
	} {
		if got := g.deviceFor(tc.id); got != tc.want {
			t.Errorf("deviceFor(%+v) = %+v, want %+v", tc.id, got, tc.want)
		}
	}

	// Stored names: trusted-cidrs devices take the current name, whatever
	// was stored; other devices keep theirs.
	for _, tc := range []struct{ id, stored, want string }{
		{"cidr:192.168.0.88", "192.168.0.88", "server (via 192.168.0.88)"},
		{"cidr:192.168.0.20", "192.168.0.20", "192.168.0.20"},
		{"cidr:not-an-address", "x", "not-an-address"},
		{"node:nDesk", "desktop", "desktop"},
		{"local", "server (local)", "server (local)"},
		{"plugin:claude-keep-rolling", "Claude Keep Rolling", "Claude Keep Rolling"},
	} {
		if got := g.DeviceName(tc.id, tc.stored); got != tc.want {
			t.Errorf("DeviceName(%q, %q) = %q, want %q", tc.id, tc.stored, got, tc.want)
		}
	}
	if calls != 1 {
		t.Errorf("listed interfaces %d times within a minute, want 1", calls)
	}
}

func TestPluginDevice(t *testing.T) {
	for _, tc := range []struct {
		id, name string
		want     Device
	}{
		{"claude-keep-rolling", "Claude Keep Rolling", Device{ID: "plugin:claude-keep-rolling", Name: "Claude Keep Rolling", Via: ViaPlugin}},
		{"unloaded", "", Device{ID: "plugin:unloaded", Name: "unloaded", Via: ViaPlugin}},
		{"", "ignored", Device{ID: "plugins", Name: "In-process plugins", Via: ViaPlugin}},
	} {
		if got := PluginDevice(tc.id, tc.name); got != tc.want {
			t.Errorf("PluginDevice(%q, %q) = %+v, want %+v", tc.id, tc.name, got, tc.want)
		}
	}
}

// TestOwnAddressesRefreshAndSurviveFailures checks the interface list is
// reread after ownAddrTTL, and a failed read keeps the last good list.
func TestOwnAddressesRefreshAndSurviveFailures(t *testing.T) {
	start := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	lan := netip.MustParseAddr("192.168.0.88")
	current := []string{"192.168.0.88/24"}
	var calls int
	fail := false
	o := ownAddrs{list: func() ([]net.Addr, error) {
		if fail {
			calls++
			return nil, errors.New("netlink unavailable")
		}
		return interfaces(&calls, current...)()
	}}
	if !o.contains(lan, start) || !o.contains(netip.MustParseAddr("::ffff:192.168.0.88"), start) {
		t.Fatal("own LAN address (plain or IPv4-mapped) not recognized")
	}
	current = []string{"192.168.0.90/24"}
	if !o.contains(lan, start.Add(ownAddrTTL-time.Second)) {
		t.Error("list reread before ownAddrTTL")
	}
	if o.contains(lan, start.Add(ownAddrTTL)) || !o.contains(netip.MustParseAddr("192.168.0.90"), start.Add(ownAddrTTL)) {
		t.Error("changed addresses not picked up after ownAddrTTL")
	}
	fail = true
	if !o.contains(netip.MustParseAddr("192.168.0.90"), start.Add(2*ownAddrTTL)) {
		t.Error("a failed read dropped the last good list")
	}
	if calls != 3 {
		t.Errorf("interface reads = %d, want 3", calls)
	}
	var empty ownAddrs
	empty.list = func() ([]net.Addr, error) { return nil, errors.New("down") }
	if empty.contains(lan, start) {
		t.Error("address counted as own with no list ever read")
	}
}

// TestOwnAddressNamingLeavesAccessUnchanged sends the same requests through
// guards that differ only in which addresses are the server's own, and checks
// every admit, deny, and admin decision is the same and matches the security
// model. Own addresses only change a device's name.
func TestOwnAddressNamingLeavesAccessUnchanged(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	cfg := Config{TailscaleSocket: ts.socket, AdminNodes: []string{"laptop"}, TrustedCIDRs: []string{"192.168.0.88/32"}}
	callers := []struct {
		name, remote string
		proxy, admin int
	}{
		{"loopback", "127.0.0.1:1", 200, 200},
		{"trusted own LAN address", "192.168.0.88:40000", 200, 403},
		{"own address outside trusted-cidrs", "10.0.2.15:40000", 403, 403},
		{"other LAN host", "192.168.0.20:40000", 403, 403},
		{"admin node", adminIP + ":40000", 200, 200},
		{"server's own node", selfIP + ":40000", 200, 403},
		{"unlisted node", otherNodeIP + ":40000", 200, 403},
		{"login not allowed", outsiderIP + ":40000", 403, 403},
		{"unknown tailnet address", strayIP + ":40000", 403, 403},
		{"internet", "203.0.113.9:5000", 403, 403},
	}
	paths := []struct {
		path  string
		admin bool
	}{{"/v1/models", false}, {"/v8/management/config", true}, {"/v0/resource/plugins/demo/page", true}, {"/taproot/api/v1/me", true}}

	results := map[string][]string{}
	for _, own := range [][]string{nil, {"127.0.0.1/8", "192.168.0.88/24", "10.0.2.15/24", "100.64.0.1/32"}} {
		g := newTestGuard(t, cfg)
		var calls int
		g.own.list = interfaces(&calls, own...)
		e, _ := testEngine(g)
		label := fmt.Sprintf("own=%v", own)
		for _, c := range callers {
			for _, p := range paths {
				rec := do(e, c.remote, p.path, nil)
				want := c.proxy
				if p.admin {
					want = c.admin
				}
				if rec.Code != want {
					t.Errorf("%s: %s %s = %d, want %d", label, c.name, p.path, rec.Code, want)
				}
				results[c.name] = append(results[c.name], fmt.Sprintf("%s %d %s", p.path, rec.Code, rec.Body))
			}
		}
		if len(own) > 0 {
			// The naming did look: the trusted own address was named after the server.
			if name := g.DeviceName("cidr:192.168.0.88", "192.168.0.88"); name != "server (via 192.168.0.88)" {
				t.Errorf("%s: trusted own address named %q", label, name)
			}
		}
	}
	for name, r := range results {
		half := len(r) / 2
		for n := range half {
			if r[n] != r[half+n] {
				t.Errorf("%s: without own addresses %q, with them %q", name, r[n], r[half+n])
			}
		}
	}
}
