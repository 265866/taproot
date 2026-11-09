package access

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestClaimCodeFormat(t *testing.T) {
	valid := regexp.MustCompile(`^[ABCDEFGHJKMNPQRSTUVWXYZ23456789]{8}$`)
	seen := map[string]bool{}
	for range 200 {
		code, err := newClaimCode()
		if err != nil {
			t.Fatal(err)
		}
		if !valid.MatchString(code) {
			t.Fatalf("code %q is not 8 characters of the unambiguous alphabet", code)
		}
		seen[code] = true
	}
	// 31^8 codes: 200 draws repeat with probability about 2e-8.
	if len(seen) != 200 {
		t.Fatalf("200 codes had %d distinct values", len(seen))
	}
	if got := FormatClaimCode("7KQ4M2XD"); got != "7KQ4-M2XD" {
		t.Fatalf("FormatClaimCode = %q, want 7KQ4-M2XD", got)
	}
	for _, typed := range []string{"7KQ4-M2XD", "7kq4m2xd", " 7KQ4 M2XD "} {
		if got := normalizeClaimCode(typed); got != "7KQ4M2XD" {
			t.Errorf("normalizeClaimCode(%q) = %q", typed, got)
		}
	}
}

// A code is made only while the guard is on and no device has Admin, so
// removing every admin entry and restarting brings the claim back.
func TestStartClaimOnlyWhileUnclaimed(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	if code, err := newTestGuard(t, Config{TailscaleSocket: ts.socket}).StartClaim(); err != nil || code != "" {
		t.Fatalf("StartClaim with an admin device = %q, %v; want no code", code, err)
	}
	off := false
	if code, _ := newTestGuard(t, Config{Enabled: &off, Devices: []DeviceGrant{}}).StartClaim(); code != "" {
		t.Fatalf("StartClaim with the guard off = %q, want no code", code)
	}
	// Recovery: the same devices without admin: true.
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{{ID: laptopID, Name: "laptop", Proxy: true}}})
	if g.Claimable() {
		t.Fatal("claimable before StartClaim")
	}
	code, err := g.StartClaim()
	if err != nil || len(code) != claimCodeLen || !g.Claimable() {
		t.Fatalf("StartClaim without admin = %q, %v, claimable %t", code, err, g.Claimable())
	}
	// A device given Admin by any means closes the claim, even before EndClaim.
	if err := g.SetGrants(Grants{Devices: []DeviceGrant{{ID: laptopID, Admin: true}}}); err != nil {
		t.Fatal(err)
	}
	if g.Claimable() || !errors.Is(g.CheckClaimCode(code, time.Now()), ErrClaimClosed) {
		t.Fatal("claim still open with an admin device")
	}
}

// Five wrong codes a minute lock the claim for the whole server, the right
// code included, until the oldest wrong one is a minute old.
func TestClaimCodeRateLimit(t *testing.T) {
	g := newTestGuard(t, Config{TailscaleSocket: newFakeTailscaled(t, defaultPeers()).socket, Devices: []DeviceGrant{}})
	code, err := g.StartClaim()
	if err != nil {
		t.Fatal(err)
	}
	wrong := "AAAAAAAA"
	if code == wrong {
		wrong = "BBBBBBBB"
	}
	start := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for i := range claimMaxFailures {
		if err := g.CheckClaimCode(wrong, start.Add(time.Duration(i)*10*time.Second)); !errors.Is(err, ErrClaimWrongCode) {
			t.Fatalf("wrong code %d = %v, want ErrClaimWrongCode", i+1, err)
		}
	}
	// 40 s after the first failure: locked for another 20 s, right code or not.
	var limited *ClaimRateLimitedError
	if err := g.CheckClaimCode(FormatClaimCode(code), start.Add(40*time.Second)); !errors.As(err, &limited) || limited.RetryAfter != 20*time.Second {
		t.Fatalf("right code while locked = %v, want a rate limit with 20s left", err)
	}
	// Refused attempts are not counted, so the lock ends a minute after the first failure.
	if err := g.CheckClaimCode(wrong, start.Add(59*time.Second)); !errors.As(err, &limited) {
		t.Fatalf("wrong code while locked = %v, want a rate limit", err)
	}
	if err := g.CheckClaimCode(strings.ToLower(code), start.Add(time.Minute)); err != nil {
		t.Fatalf("right code once the first failure is a minute old = %v", err)
	}
	g.EndClaim()
	if g.Claimable() || !errors.Is(g.CheckClaimCode(code, start.Add(2*time.Minute)), ErrClaimClosed) {
		t.Fatal("the code still works after EndClaim")
	}
}

// While unclaimed, a tailnet device without Admin reaches POST ClaimPath and
// nothing else on the admin surface, and its not_admin denial says it may
// claim. The server's own node, addresses, and devices once an admin exists
// are refused by the guard.
func TestGuardOpensOnlyTheClaimRouteWhileUnclaimed(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{}, Addresses: []AddressGrant{{CIDR: "192.0.2.20"}}})
	e, _ := testEngine(g)
	reached := 0
	e.POST(ClaimPath, func(c *gin.Context) {
		reached++
		c.Status(http.StatusNoContent)
	})
	post := func(remote string) int {
		return doMethod(e, http.MethodPost, remote, ClaimPath, nil).Code
	}
	claimable := func(remote string) bool {
		t.Helper()
		rec := do(e, remote, "/v8/management/config", nil)
		var body struct{ Error map[string]any }
		if rec.Code != http.StatusForbidden || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("admin GET from %s = %d %s, want 403", remote, rec.Code, rec.Body)
		}
		return body.Error["claimable"] == true
	}

	// No code yet: closed.
	if code := post(outsiderIP + ":1"); code != http.StatusForbidden || claimable(outsiderIP+":1") {
		t.Fatalf("claim before StartClaim = %d, want the guard's 403 without claimable", code)
	}
	if _, err := g.StartClaim(); err != nil {
		t.Fatal(err)
	}
	if code := post(outsiderIP + ":1"); code != http.StatusNoContent || reached != 1 {
		t.Fatalf("claim from a tailnet device = %d, want the handler", code)
	}
	if !claimable(outsiderIP + ":1") {
		t.Fatal("not_admin denial while unclaimed lacks claimable")
	}
	// Only POST to the claim route opens.
	if rec := do(e, outsiderIP+":1", ClaimPath, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("GET %s from a device = %d, want 403", ClaimPath, rec.Code)
	}
	if code := post(selfIP + ":1"); code != http.StatusForbidden {
		t.Fatalf("claim from the server's own node = %d, want 403", code)
	}
	if code := post("192.0.2.20:1"); code != http.StatusForbidden {
		t.Fatalf("claim from an access.addresses caller = %d, want 403", code)
	}
	// Loopback is an admin and reaches the handler, which refuses it.
	if code := post("127.0.0.1:1"); code != http.StatusNoContent {
		t.Fatalf("claim from loopback = %d, want the handler", code)
	}

	if err := g.SetGrants(Grants{Devices: []DeviceGrant{{ID: laptopID, Admin: true}}}); err != nil {
		t.Fatal(err)
	}
	before := reached
	if code := post(outsiderIP + ":1"); code != http.StatusForbidden || reached != before || claimable(outsiderIP+":1") {
		t.Fatalf("claim once a device has Admin = %d, want the guard's 403 without claimable", code)
	}
}
