package access

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestLiveWhois runs against a real tailscaled. It is skipped unless
// TAPROOT_LIVE_TAILSCALE_SOCKET names the socket and TAPROOT_LIVE_WHOIS lists
// expectations as "ip=login,ip=login"; an empty login expects no such peer.
// Build it for a host with `GOOS=linux go test -c ./internal/taproot/access`.
func TestLiveWhois(t *testing.T) {
	socket := os.Getenv("TAPROOT_LIVE_TAILSCALE_SOCKET")
	cases := os.Getenv("TAPROOT_LIVE_WHOIS")
	if socket == "" || cases == "" {
		t.Skip("TAPROOT_LIVE_TAILSCALE_SOCKET and TAPROOT_LIVE_WHOIS not set")
	}
	g := newTestGuard(t, Config{TailscaleSocket: socket})
	e, _ := testEngine(g)
	for _, pair := range strings.Split(cases, ",") {
		ip, wantLogin, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			t.Fatalf("bad TAPROOT_LIVE_WHOIS entry %q", pair)
		}
		t.Run(ip, func(t *testing.T) {
			p, err := newWhoisClient(socket).whois(context.Background(), mustAddr(t, ip))
			if wantLogin == "" {
				if !errors.Is(err, errPeerNotFound) {
					t.Fatalf("whois err = %v, want errPeerNotFound", err)
				}
				assertForbidden(t, do(e, ip+":40000", "/v1/models", nil))
				t.Logf("ip=%s not found, denied", ip)
				return
			}
			if err != nil {
				t.Fatalf("whois: %v", err)
			}
			if p.Login != wantLogin {
				t.Fatalf("login = %q, want %q", p.Login, wantLogin)
			}
			rec := do(e, ip+":40000", "/v1/models", nil)
			admitted := rec.Code == http.StatusOK
			wantAdmitted := strings.EqualFold(wantLogin, DefaultAllowLogins[0])
			t.Logf("ip=%s login=%s node=%s status=%d admitted=%v", ip, p.Login, p.Node, rec.Code, admitted)
			if admitted != wantAdmitted {
				t.Fatalf("admitted = %v, want %v under the default allowlist", admitted, wantAdmitted)
			}
		})
	}
}
