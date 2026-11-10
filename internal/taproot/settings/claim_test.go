package settings

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
)

// unclaimedYAML has a device with Proxy only, so no device has Admin.
const unclaimedYAML = `# keep this comment
access:
  devices:
    - id: nworkerCNTRL
      name: worker
      proxy: true
      admin: false
`

var worker = access.DeviceGrant{ID: "nworkerCNTRL", Name: "worker", Proxy: true}

// newDevice is a tailnet device with no permission that claims the server.
var newDevice = access.Identity{Via: access.ViaTailnet, Node: "new-laptop.tailnet-0000.ts.net.", NodeID: "nnew-laptopCNTRL", Login: "alice@github"}

func newUnclaimed(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t, options{yaml: unclaimedYAML})
	code := startClaim(t, f.guard)
	if code == "" {
		t.Fatal("StartClaim made no code")
	}
	f.caller = newDevice
	return f, code
}

// startClaim arms the guard's setup code as Setup does and returns the code
// it announced, or "".
func startClaim(t *testing.T, g *access.Guard) string {
	t.Helper()
	var code string
	if err := g.StartClaim(t.Context(), func(c string) { code = c }); err != nil {
		t.Fatalf("StartClaim: %v", err)
	}
	return code
}

func (f *fixture) postClaim(t *testing.T, code string) (int, string, http.Header) {
	t.Helper()
	body, err := json.Marshal(claimRequest{Code: code})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/taproot/api/v1/claim", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.engine.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header()
}

// The right code gives the calling device Proxy and Admin, live and in
// taproot.yaml, keeps the file's other entries and comments, and is then
// used up.
func TestClaimGrantsProxyAndAdmin(t *testing.T) {
	f, code := newUnclaimed(t)
	status, body, _ := f.postClaim(t, access.FormatClaimCode(code))
	if status != http.StatusOK {
		t.Fatalf("claim = %d %s", status, body)
	}
	var resp claimResponse
	want := access.DeviceGrant{ID: "nnew-laptopCNTRL", Name: "new-laptop", Proxy: true, Admin: true}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || !resp.Claimed || resp.Device != want {
		t.Fatalf("claim response = %s", body)
	}
	if got := f.guard.Grants().Devices; !slices.Equal(got, []access.DeviceGrant{worker, want}) {
		t.Fatalf("guard devices = %+v", got)
	}
	if got := f.parsed(t).Access.Devices; !slices.Equal(got, []access.DeviceGrant{worker, want}) {
		t.Fatalf("taproot.yaml devices = %+v\n%s", got, f.file(t))
	}
	if !strings.Contains(f.file(t), "# keep this comment") {
		t.Errorf("the claim dropped the file's comment:\n%s", f.file(t))
	}
	if f.guard.Claimable() {
		t.Fatal("still claimable after the claim")
	}

	// The same code from another device is refused.
	f.caller = access.Identity{Via: access.ViaTailnet, Node: "bob-pc.tailnet-0000.ts.net.", NodeID: "nbob-pcCNTRL"}
	if status, body, _ := f.postClaim(t, code); status != http.StatusConflict || errorCode(t, body) != "claimed" {
		t.Fatalf("second claim = %d %s, want 409 claimed", status, body)
	}
	if got := f.guard.Grants().Devices; len(got) != 2 {
		t.Fatalf("second claim changed devices: %+v", got)
	}
}

// Loopback, access.addresses, and the server's own node cannot claim, and
// their attempts do not count against the limit.
func TestClaimRefusesCallersThatAreNotTailnetDevices(t *testing.T) {
	f, code := newUnclaimed(t)
	for _, caller := range []access.Identity{
		{Via: access.ViaLoopback, Node: "127.0.0.1", Proxy: true, Admin: true},
		{Via: access.ViaCIDR, Node: "192.0.2.20", Proxy: true},
		{Via: access.ViaTailnet, Node: "server.tailnet-0000.ts.net.", NodeID: serverID, Proxy: true},
		// A tailnet caller without a stable ID.
		{Via: access.ViaTailnet, Node: "laptop.tailnet-0000.ts.net."},
	} {
		f.caller = caller
		for range 6 {
			status, body, _ := f.postClaim(t, code)
			if status != http.StatusForbidden || errorCode(t, body) != "claim_tailnet_only" {
				t.Fatalf("claim from %+v = %d %s, want 403 claim_tailnet_only", caller, status, body)
			}
		}
	}
	if f.file(t) != unclaimedYAML || !f.guard.Claimable() {
		t.Fatalf("refused claims changed state:\n%s", f.file(t))
	}
	f.caller = newDevice
	if status, body, _ := f.postClaim(t, code); status != http.StatusOK {
		t.Fatalf("claim from the device after refused callers = %d %s", status, body)
	}
}

// Wrong codes count against 5 attempts a minute for the server; once that is
// reached, every code gets 429 with Retry-After until a minute has passed.
func TestClaimWrongCodeRateLimit(t *testing.T) {
	f, code := newUnclaimed(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	f.s.now = func() time.Time { return now }
	wrong := "AAAA-AAAA"
	if code == "AAAAAAAA" {
		wrong = "BBBB-BBBB"
	}
	for i := range 5 {
		if status, body, _ := f.postClaim(t, wrong); status != http.StatusForbidden || errorCode(t, body) != "wrong_code" {
			t.Fatalf("wrong code %d = %d %s, want 403 wrong_code", i+1, status, body)
		}
		now = now.Add(5 * time.Second)
	}
	// 25 s after the first failure, the right code waits 35 s more.
	status, body, header := f.postClaim(t, code)
	if status != http.StatusTooManyRequests || errorCode(t, body) != "rate_limited" || header.Get("Retry-After") != "35" || !strings.Contains(body, `"retryAfter":35`) {
		t.Fatalf("right code while locked = %d %s (Retry-After %q), want 429 with 35 s", status, body, header.Get("Retry-After"))
	}
	if f.guard.Claimable() != true || f.file(t) != unclaimedYAML {
		t.Fatal("a locked claim changed state")
	}
	now = now.Add(35 * time.Second)
	if status, body, _ := f.postClaim(t, code); status != http.StatusOK {
		t.Fatalf("right code after the minute = %d %s", status, body)
	}
}

// A failed taproot.yaml write rolls the guard back and keeps the code, so the
// claim can be retried.
func TestClaimWriteFailureKeepsTheCode(t *testing.T) {
	f, code := newUnclaimed(t)
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.path, 0o700); err != nil {
		t.Fatal(err)
	}
	if status, body, _ := f.postClaim(t, code); status != http.StatusInternalServerError {
		t.Fatalf("claim with an unwritable file = %d %s, want 500", status, body)
	}
	if got := f.guard.Grants().Devices; !slices.Equal(got, []access.DeviceGrant{worker}) || !f.guard.Claimable() {
		t.Fatalf("after a failed write: devices %+v, claimable %t", got, f.guard.Claimable())
	}
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if status, body, _ := f.postClaim(t, code); status != http.StatusOK {
		t.Fatalf("retried claim = %d %s", status, body)
	}
}

// Giving a device Admin through PUT /settings, as loopback can, uses up the
// code too. Taking Admin away again starts a new claim with a new code.
func TestAdminEditEndsTheClaim(t *testing.T) {
	f := newFixture(t, options{yaml: unclaimedYAML})
	var codes []string
	if err := f.guard.StartClaim(t.Context(), func(c string) { codes = append(codes, c) }); err != nil || len(codes) != 1 {
		t.Fatalf("StartClaim = %v, codes %q", err, codes)
	}
	f.caller = access.Identity{Via: access.ViaLoopback}
	f.access.tailnet = &access.Tailnet{Self: access.TailnetDevice{ID: serverID}, Peers: []access.TailnetDevice{{ID: "nworkerCNTRL", Name: "worker"}}}
	if status, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nworkerCNTRL", "name": "worker", "proxy": true, "admin": true}]}}`); status != http.StatusOK {
		t.Fatalf("PUT = %d %s", status, body)
	}
	f.caller = newDevice
	if f.guard.Claimable() {
		t.Fatal("claimable after Admin was given")
	}
	if status, body, _ := f.postClaim(t, codes[0]); status != http.StatusConflict {
		t.Fatalf("claim after an admin edit = %d %s, want 409", status, body)
	}
	f.caller = access.Identity{Via: access.ViaLoopback}
	if status, _, body := f.do(t, http.MethodPut, `{"access": {"devices": []}}`); status != http.StatusOK {
		t.Fatalf("PUT = %d %s", status, body)
	}
	f.caller = newDevice
	if !f.guard.Claimable() || len(codes) != 2 || codes[1] == codes[0] {
		t.Fatalf("after the last Admin was removed: claimable %t, codes %q; want a new code", f.guard.Claimable(), codes)
	}
	if status, body, _ := f.postClaim(t, codes[0]); status != http.StatusForbidden || errorCode(t, body) != "wrong_code" {
		t.Fatalf("claim with the used-up code = %d %s, want 403 wrong_code", status, body)
	}
	if status, body, _ := f.postClaim(t, codes[1]); status != http.StatusOK {
		t.Fatalf("claim with the new code = %d %s, want 200", status, body)
	}
}

func TestClaimRefusesAMalformedBody(t *testing.T) {
	f, _ := newUnclaimed(t)
	req := httptest.NewRequest(http.MethodPost, "/taproot/api/v1/claim", strings.NewReader(`{"code": 7}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec.Body.String()) != "invalid_body" {
		t.Fatalf("claim with a number = %d %s", rec.Code, rec.Body)
	}
}

// When auto-forget removes the last device with Admin, the sweep's grant
// change starts a new claim, so a new device can claim the server without a
// restart.
func TestSweepForgettingTheLastAdminStartsAClaim(t *testing.T) {
	f := newFixture(t, options{yaml: unclaimedYAML})
	f.caller = access.Identity{Via: access.ViaLoopback}
	f.access.tailnet = &access.Tailnet{Self: access.TailnetDevice{ID: serverID}, Peers: []access.TailnetDevice{{ID: "nworkerCNTRL", Name: "worker"}}}
	if status, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nworkerCNTRL", "name": "worker", "proxy": true, "admin": true}]}}`); status != http.StatusOK {
		t.Fatalf("PUT = %d %s", status, body)
	}
	var codes []string
	if err := f.guard.StartClaim(t.Context(), func(c string) { codes = append(codes, c) }); err != nil || len(codes) != 0 {
		t.Fatalf("StartClaim with an admin = %v, codes %q", err, codes)
	}
	f.access.reconcile = func(access.Tailnet) (access.Grants, []access.DeviceGrant, bool) {
		return access.Grants{Devices: []access.DeviceGrant{}}, f.guard.Grants().Devices, true
	}
	if err := f.s.sweep(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(codes) != 1 || !f.guard.Claimable() {
		t.Fatalf("after forgetting the last admin: codes %q, claimable %t", codes, f.guard.Claimable())
	}
	f.caller = newDevice
	if status, body, _ := f.postClaim(t, codes[0]); status != http.StatusOK {
		t.Fatalf("claim with the new code = %d %s, want 200", status, body)
	}
}
