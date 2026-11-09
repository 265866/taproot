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

// A device outside the tailnet gets Admin only from its own request; Proxy
// needs none, and Admin it already had stays.
func TestOutsideAdminNeedsARequest(t *testing.T) {
	f := newFixture(t, options{yaml: outsideYAML})
	tn := outsideTailnet
	f.access.tailnet = &tn

	code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, `+workEntry+`, {"id": "ndanaCNTRL", "proxy": true, "admin": true}]}}`)
	if code != http.StatusBadRequest || errorCode(t, body) != "outside_admin_needs_request" {
		t.Fatalf("Admin for a sharee's device = %d %s", code, body)
	}
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, `+workEntry+`, {"id": "ndanaCNTRL", "proxy": true}, {"id": "ndana2CNTRL", "proxy": true}]}}`); code != http.StatusOK {
		t.Fatalf("Proxy for an outside device = %d %s", code, body)
	}

	f.openRequest(t, "ndanaCNTRL")
	code, v, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, `+workEntry+`, {"id": "ndanaCNTRL", "label": "Dana's PC", "proxy": true, "admin": true}]}}`)
	if code != http.StatusOK || len(v.Access.Devices) != 3 || v.Access.Devices[2] != (access.DeviceGrant{ID: "ndanaCNTRL", Label: "Dana's PC", Proxy: true, Admin: true}) {
		t.Fatalf("Admin from a request = %d %s", code, body)
	}
	if f.isOpen(t, "ndanaCNTRL") {
		t.Fatal("the granted request is still open")
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

// When the tailnet cannot be read, a new Admin grant needs a request, so a
// failed read never widens access.
func TestOutsideCheckFailsClosed(t *testing.T) {
	f := newFixture(t, options{yaml: outsideYAML})
	put := `{"access": {"devices": [` + laptopEntry + `, ` + workEntry + `, {"id": "ndanaCNTRL", "proxy": true}, {"id": "nphoneCNTRL", "admin": true}]}}`
	if code, _, body := f.do(t, http.MethodPut, put); code != http.StatusBadRequest || errorCode(t, body) != "outside_admin_needs_request" {
		t.Fatalf("Admin without a tailnet read = %d %s", code, body)
	}
	f.openRequest(t, "nphoneCNTRL")
	if code, _, body := f.do(t, http.MethodPut, put); code != http.StatusOK {
		t.Fatalf("Admin from a request without a tailnet read = %d %s", code, body)
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
