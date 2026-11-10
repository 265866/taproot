package settings

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
)

// outsideYAML has laptop as the admin, a device from another tailnet that
// already has Admin, and a sharee's device with Proxy.
const outsideYAML = `access:
  devices:
    - {id: nlaptopCNTRL, name: laptop, proxy: true, admin: true}
    - {id: nworkCNTRL, name: work-laptop.tailnet-1111.ts.net, label: Work laptop, proxy: true, admin: true}
    - {id: ndanaCNTRL, proxy: true}
`

var outsideTailnet = access.Tailnet{
	Self: access.TailnetDevice{ID: serverID, Name: "server"},
	Peers: []access.TailnetDevice{
		{ID: "nlaptopCNTRL", Name: "laptop"},
		{ID: "nphoneCNTRL", Name: "phone"},
		{ID: "nworkCNTRL", Name: "work-laptop.tailnet-1111.ts.net", Outside: true},
		{ID: "ndanaCNTRL", Outside: true},
		{ID: "ndana2CNTRL", Outside: true},
	},
}

func (f *fixture) openRequest(t *testing.T, nodeID string) {
	t.Helper()
	if err := f.requests.Record(context.Background(), access.RequestInput{NodeID: nodeID, Login: "dana@example.com"}, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) isOpen(t *testing.T, nodeID string) bool {
	t.Helper()
	open, err := f.requests.IsOpen(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	return open
}

const (
	laptopEntry = `{"id": "nlaptopCNTRL", "name": "laptop", "proxy": true, "admin": true}`
	workEntry   = `{"id": "nworkCNTRL", "name": "work-laptop.tailnet-1111.ts.net", "label": "Work laptop", "proxy": true, "admin": true}`
)

// A device outside the tailnet gets Proxy and Admin from its switches, with
// no access request, and Admin it already had stays.
func TestOutsideAdminFromTheSwitch(t *testing.T) {
	f := newFixture(t, options{yaml: outsideYAML})
	tn := outsideTailnet
	f.access.tailnet = &tn

	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, `+workEntry+`, {"id": "ndanaCNTRL", "proxy": true}, {"id": "ndana2CNTRL", "proxy": true}]}}`); code != http.StatusOK {
		t.Fatalf("Proxy for an outside device = %d %s", code, body)
	}
	code, v, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, `+workEntry+`, {"id": "ndanaCNTRL", "label": "Dana's PC", "proxy": true, "admin": true}, {"id": "ndana2CNTRL", "admin": true}]}}`)
	if code != http.StatusOK || len(v.Access.Devices) != 4 ||
		v.Access.Devices[2] != (access.DeviceGrant{ID: "ndanaCNTRL", Label: "Dana's PC", Proxy: true, Admin: true}) ||
		v.Access.Devices[3] != (access.DeviceGrant{ID: "ndana2CNTRL", Admin: true}) {
		t.Fatalf("Admin for sharee devices without a request = %d %s", code, body)
	}
	if got := f.parsed(t).Access.Devices[2]; !got.Admin || got.Label != "Dana's PC" {
		t.Fatalf("file entry = %+v, want dana's Admin and label saved", got)
	}
	if f.isOpen(t, "ndanaCNTRL") || f.isOpen(t, "ndana2CNTRL") {
		t.Fatal("a save without requests opened one")
	}
	if got := f.parsed(t).Access.Devices[1]; got.Label != "Work laptop" || !got.Admin {
		t.Fatalf("file entry = %+v, want the work laptop's label and Admin kept", got)
	}
}

// A request closes only when a save gives its device something new.
func TestSaveClosesAnsweredRequestsOnly(t *testing.T) {
	f := newFixture(t, options{yaml: outsideYAML})
	tn := outsideTailnet
	f.access.tailnet = &tn
	f.openRequest(t, "nphoneCNTRL")
	f.openRequest(t, "ndanaCNTRL")
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, `+workEntry+`, {"id": "ndanaCNTRL", "proxy": true}, {"id": "nphoneCNTRL", "proxy": true}]}}`); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if f.isOpen(t, "nphoneCNTRL") || !f.isOpen(t, "ndanaCNTRL") {
		t.Fatalf("open: phone %t, dana %t; want dana's only, which got nothing new", f.isOpen(t, "nphoneCNTRL"), f.isOpen(t, "ndanaCNTRL"))
	}
}

// A save does not read the tailnet, so Admin saves while tailscaled cannot
// be read, and the server's own node is still refused it.
func TestAdminWithoutATailnetRead(t *testing.T) {
	f := newFixture(t, options{yaml: outsideYAML})
	put := `{"access": {"devices": [` + laptopEntry + `, ` + workEntry + `, {"id": "ndanaCNTRL", "proxy": true}, {"id": "nphoneCNTRL", "admin": true}]}}`
	if code, _, body := f.do(t, http.MethodPut, put); code != http.StatusOK {
		t.Fatalf("Admin without a tailnet read = %d %s", code, body)
	}
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, {"id": "`+serverID+`", "proxy": true, "admin": true}]}}`); code != http.StatusBadRequest || errorCode(t, body) != "server_admin" {
		t.Fatalf("Admin for the server's own node = %d %s", code, body)
	}
	// Labels are checked like the rest of the entry.
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, {"id": "nphoneCNTRL", "label": "a\u0000b", "proxy": true}]}}`); code != http.StatusBadRequest || errorCode(t, body) != "invalid_access" {
		t.Fatalf("label with a control character = %d %s", code, body)
	}
}

// Only a device in the server's own tailnet can claim it, and an outside
// device's attempt does not count against the code.
func TestClaimRefusesOutsideDevices(t *testing.T) {
	f, code := newUnclaimed(t)
	outside := newDevice
	outside.Outside = true
	f.caller = outside
	for range 6 {
		status, body, _ := f.postClaim(t, "WRONGWRONG")
		if status != http.StatusForbidden || errorCode(t, body) != "claim_own_tailnet_only" {
			t.Fatalf("outside claim = %d %s", status, body)
		}
	}
	if status, body, _ := f.postClaim(t, code); status != http.StatusForbidden {
		t.Fatalf("outside claim with the right code = %d %s", status, body)
	}
	f.caller = newDevice
	if status, body, _ := f.postClaim(t, code); status != http.StatusOK {
		t.Fatalf("own-tailnet claim = %d %s", status, body)
	}
}

// The Allow form saves the grant, then closes the request, and Allow with
// Admin works for an outside device.
func TestAllowFormOrder(t *testing.T) {
	f := newFixture(t, options{yaml: outsideYAML})
	tn := outsideTailnet
	f.access.tailnet = &tn
	ctx := context.Background()
	danaAdmin := `{"access": {"devices": [` + laptopEntry + `, ` + workEntry + `, {"id": "ndanaCNTRL", "proxy": true, "admin": true}]}}`
	danaProxy := `{"access": {"devices": [` + laptopEntry + `, ` + workEntry + `, {"id": "ndanaCNTRL", "proxy": true}]}}`

	// Allow with the defaults gives dana's Proxy-only device nothing new, so
	// the save leaves the request open and the allow call closes it.
	f.openRequest(t, "ndanaCNTRL")
	if code, _, body := f.do(t, http.MethodPut, danaProxy); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if !f.isOpen(t, "ndanaCNTRL") {
		t.Fatal("a save that granted nothing closed the request")
	}
	if found, err := f.requests.Allow(ctx, "ndanaCNTRL", time.Now()); err != nil || !found {
		t.Fatalf("allow = %t, %v", found, err)
	}
	if f.isOpen(t, "ndanaCNTRL") {
		t.Fatal("the allowed request is still open")
	}
	// Asked again for Admin: Admin saves, then allow still finds the request.
	if err := f.requests.Record(ctx, access.RequestInput{NodeID: "ndanaCNTRL", Self: true, Asked: access.AskAdmin}, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, body := f.do(t, http.MethodPut, danaAdmin); code != http.StatusOK {
		t.Fatalf("Admin from the reopened request = %d %s", code, body)
	}
	if found, err := f.requests.Allow(ctx, "ndanaCNTRL", time.Now()); err != nil || !found || f.isOpen(t, "ndanaCNTRL") {
		t.Fatalf("allow after the save = %t, %v, open %t", found, err, f.isOpen(t, "ndanaCNTRL"))
	}

	// With the request closed, the switches still take Admin away and give
	// it back.
	if code, _, body := f.do(t, http.MethodPut, danaProxy); code != http.StatusOK {
		t.Fatalf("Admin off = %d %s", code, body)
	}
	if code, _, body := f.do(t, http.MethodPut, danaAdmin); code != http.StatusOK {
		t.Fatalf("Admin on without a request = %d %s", code, body)
	}
}

// A save whose client hung up after the file was written still closes the
// requests it answered.
func TestSaveClosesRequestsAfterCancel(t *testing.T) {
	f := newFixture(t, options{yaml: outsideYAML})
	tn := outsideTailnet
	f.access.tailnet = &tn
	f.openRequest(t, "nphoneCNTRL")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.s.mu.Lock()
	devices := append(f.guard.Grants().Devices, access.DeviceGrant{ID: "nphoneCNTRL", Proxy: true})
	err := f.s.apply(ctx, update{Access: &accessUpdate{Devices: &devices}}, access.Identity{Via: access.ViaLoopback})
	f.s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if f.isOpen(t, "nphoneCNTRL") {
		t.Fatal("a cancelled save left the granted request open")
	}
}
