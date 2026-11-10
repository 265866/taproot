package access

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func getDevices(t *testing.T, g *Guard) devicesResponse {
	t.Helper()
	e, _ := testEngine(g)
	rec := do(e, "127.0.0.1:1", "/taproot/api/v1/access/devices", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /access/devices = %d %s", rec.Code, rec.Body)
	}
	var resp devicesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func findDevice(resp devicesResponse, id string) (deviceView, bool) {
	for _, d := range resp.Devices {
		if d.ID == id {
			return d, true
		}
	}
	return deviceView{}, false
}

// The device list joins tailscaled's peers with access.devices: every device
// once, the server first, with its owner, OS, created time, and permissions.
func TestDevicesEndpointJoinsTailnetAndGrants(t *testing.T) {
	peers := defaultPeers()
	joined := time.Date(2026, 11, 8, 9, 50, 0, 0, time.UTC)
	peers["100.64.0.20"] = fakePeer{login: "tagged-devices", displayName: "Tagged Devices", node: "ci-runner", os: "linux"}
	peers["100.64.0.21"] = fakePeer{login: testLogin, displayName: "Alice", node: "work-laptop", os: "macOS", created: joined}
	peers["fd7a:115c:a1e0::21"] = peers["100.64.0.21"]
	ts := newFakeTailscaled(t, peers)
	devices := append(slices.Clone(defaultDevices), DeviceGrant{ID: stableID("ci-runner"), Proxy: true}, DeviceGrant{ID: "ngoneCNTRL", Name: "old-laptop", Proxy: true})
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: devices, Addresses: []AddressGrant{{CIDR: "192.0.2.20", Note: "agents"}}})
	resp := getDevices(t, g)

	if !resp.Enabled || resp.TailnetError != "" || resp.ForgetAfterHours != 24 {
		t.Fatalf("response = %+v", resp)
	}
	if !slices.Equal(resp.Addresses, []AddressGrant{{CIDR: "192.0.2.20/32", Note: "agents"}}) {
		t.Fatalf("addresses = %+v", resp.Addresses)
	}
	if len(resp.Devices) != 7 || !resp.Devices[0].Server {
		t.Fatalf("devices = %+v, want 7 with the server first", resp.Devices)
	}
	for id, want := range map[string]deviceView{
		serverID:              {Addresses: []string{selfIP, selfIPv6}, Name: "server", DNSName: "server." + tailnetSuffix, Login: testLogin, OwnerName: "Alice", OS: "linux", Server: true, OnTailnet: true, Online: true, Proxy: true},
		laptopID:              {Addresses: []string{adminIP}, Name: "laptop", DNSName: "laptop." + tailnetSuffix, Login: testLogin, OwnerName: "Alice", OS: "macOS", OnTailnet: true, Online: true, Proxy: true, Admin: true},
		workerID:              {Addresses: []string{otherNodeIP}, Name: "worker", DNSName: "worker." + tailnetSuffix, Login: testLogin, OwnerName: "Alice", OS: "linux", OnTailnet: true, Online: true, Proxy: true},
		bobID:                 {Addresses: []string{outsiderIP}, Name: "bob-pc", DNSName: "bob-pc." + tailnetSuffix, Login: "bob@example.org", OwnerName: "Bob", OS: "windows", OnTailnet: true, Online: true},
		stableID("ci-runner"): {Addresses: []string{"100.64.0.20"}, Name: "ci-runner", DNSName: "ci-runner." + tailnetSuffix, OS: "linux", OnTailnet: true, Online: true, Proxy: true},
		"ngoneCNTRL":          {Addresses: []string{}, Name: "old-laptop", Proxy: true},
	} {
		got, ok := findDevice(resp, id)
		if !ok {
			t.Fatalf("device %s missing", id)
		}
		got.ID, got.Created, got.MissingSince, got.Tags = "", nil, nil, nil
		if !reflect.DeepEqual(got, want) {
			t.Errorf("device %s = %+v, want %+v", id, got, want)
		}
	}
	ci, _ := findDevice(resp, stableID("ci-runner"))
	if !slices.Equal(ci.Tags, []string{"tag:server"}) || ci.Login != "" {
		t.Errorf("tagged device = %+v, want its tags and no login", ci)
	}
	work, _ := findDevice(resp, stableID("work-laptop"))
	if work.Created == nil || !work.Created.Equal(joined) || work.Proxy || work.Admin {
		t.Errorf("new device = %+v, want created %s with no permissions", work, joined)
	}
	// tailscaled lists work-laptop's addresses in any order here; IPv4 comes first.
	if want := []string{"100.64.0.21", "fd7a:115c:a1e0::21"}; !slices.Equal(work.Addresses, want) {
		t.Errorf("work-laptop addresses = %v, want %v", work.Addresses, want)
	}
	// The read started old-laptop's 24 hours.
	gone, _ := findDevice(resp, "ngoneCNTRL")
	if gone.OnTailnet || gone.MissingSince == nil {
		t.Errorf("missing device = %+v, want missingSince", gone)
	}
}

// When tailscaled cannot be read, the page still lists the devices that have
// a permission, and says why the rest are missing.
func TestDevicesEndpointWithoutTailscaled(t *testing.T) {
	g := newTestGuard(t, Config{TailscaleSocket: filepath.Join(t.TempDir(), "absent.sock")})
	resp := getDevices(t, g)
	if resp.TailnetError == "" || len(resp.Devices) != 2 || resp.Devices[0].ID != laptopID || resp.Devices[0].OnTailnet || resp.Devices[0].MissingSince != nil {
		t.Fatalf("response = %+v", resp)
	}

	off := false
	g = newTestGuard(t, Config{Enabled: &off})
	resp = getDevices(t, g)
	if resp.Enabled || !strings.Contains(resp.TailnetError, "disabled") || len(resp.Devices) != 2 {
		t.Fatalf("disabled guard response = %+v", resp)
	}
}

// A granted device that successful reads miss for 24 hours is forgotten. A
// failed, empty, or not-running read never counts, and a device that comes
// back starts over.
func TestAutoForget(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	gone := DeviceGrant{ID: "ngoneCNTRL", Name: "old-laptop", Proxy: true}
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: append(slices.Clone(defaultDevices), gone)})
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	g.now = func() time.Time { return now }
	read := func() (Tailnet, error) { return g.ReadTailnet(t.Context()) }
	forgotten := func(tn Tailnet) []string {
		next, forgot, changed := g.Reconcile(tn)
		var ids []string
		for _, d := range forgot {
			ids = append(ids, d.ID)
		}
		if changed != (len(forgot) > 0) || len(next.Devices)+len(forgot) != len(g.Grants().Devices) {
			t.Fatalf("Reconcile = %+v, %v, %v", next, forgot, changed)
		}
		if changed {
			if err := g.SetGrants(next); err != nil {
				t.Fatal(err)
			}
		}
		return ids
	}

	// Failed, empty, and not-running reads start no clock.
	ts.setList(1, "", false)
	if _, err := read(); err == nil {
		t.Fatal("failed read returned no error")
	}
	ts.setList(0, "", true)
	tn, err := read()
	if err != nil || len(tn.Peers) != 0 {
		t.Fatalf("empty read = %+v, %v", tn, err)
	}
	ts.setList(0, "Starting", false)
	if _, err := read(); err == nil || !strings.Contains(err.Error(), "Starting") {
		t.Fatalf("not-running read = %v", err)
	}
	if _, ok := g.missingSince(gone.ID); ok {
		t.Fatal("a failed or empty read started the clock")
	}

	// The first successful read starts it.
	ts.setList(0, "", false)
	tn, err = read()
	if err != nil {
		t.Fatal(err)
	}
	if ids := forgotten(tn); ids != nil {
		t.Fatalf("forgot %v at once", ids)
	}
	start := now

	// Failed and empty reads past 24 hours forget nothing.
	now = start.Add(30 * time.Hour)
	ts.setList(0, "", true)
	tn, _ = read()
	if ids := forgotten(tn); ids != nil {
		t.Fatalf("an empty read forgot %v", ids)
	}
	ts.setList(0, "", false)

	// Just short of 24 hours, a successful read keeps it.
	now = start.Add(ForgetAfter - time.Second)
	tn, _ = read()
	if ids := forgotten(tn); ids != nil {
		t.Fatalf("forgot %v before 24 hours", ids)
	}
	// At 24 hours it is gone, and only it.
	now = start.Add(ForgetAfter)
	tn, _ = read()
	if ids := forgotten(tn); !slices.Equal(ids, []string{gone.ID}) {
		t.Fatalf("forgot %v, want %s", ids, gone.ID)
	}
	if got := g.Grants().Devices; !slices.Equal(got, defaultDevices) {
		t.Fatalf("devices after forgetting = %+v", got)
	}

	// A device that leaves and comes back starts over.
	ts.removePeer(otherNodeIP)
	tn, _ = read()
	left := now
	forgotten(tn)
	now = left.Add(12 * time.Hour)
	ts.setPeer(otherNodeIP, defaultPeers()[otherNodeIP])
	tn, _ = read()
	forgotten(tn)
	if _, ok := g.missingSince(workerID); ok {
		t.Fatal("a device back on the tailnet is still missing")
	}
	ts.removePeer(otherNodeIP)
	now = left.Add(20 * time.Hour)
	_, _ = read()
	now = left.Add(40 * time.Hour)
	tn, _ = read()
	if ids := forgotten(tn); ids != nil {
		t.Fatalf("forgot %v 20 hours after it left again", ids)
	}
	now = left.Add(44 * time.Hour)
	tn, _ = read()
	if ids := forgotten(tn); !slices.Equal(ids, []string{workerID}) {
		t.Fatalf("forgot %v, want %s", ids, workerID)
	}
}

// Names in access.devices follow the tailnet; IDs and permissions stay.
func TestReconcileRefreshesNames(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{{ID: laptopID, Name: "old-name", Proxy: true, Admin: true}, {ID: workerID, Proxy: true}}})
	tn, err := g.ReadTailnet(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next, forgot, changed := g.Reconcile(tn)
	want := []DeviceGrant{{ID: laptopID, Name: "laptop", Proxy: true, Admin: true}, {ID: workerID, Name: "worker", Proxy: true}}
	if !changed || forgot != nil || !slices.Equal(next.Devices, want) {
		t.Fatalf("Reconcile = %+v, %v, %v; want %+v", next.Devices, forgot, changed, want)
	}
	if err := g.SetGrants(next); err != nil {
		t.Fatal(err)
	}
	if _, _, changed := g.Reconcile(tn); changed {
		t.Fatal("a second Reconcile changed the grants again")
	}
}
