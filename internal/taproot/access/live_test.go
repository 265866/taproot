package access

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestLiveTailscaled runs against a real tailscaled. It is skipped unless
// TAPROOT_LIVE_TAILSCALE_SOCKET names the socket, TAPROOT_LIVE_DEVICES lists
// the access.devices grants as "stableID=proxy|admin|both,...", and
// TAPROOT_LIVE_WHOIS lists expectations as "ip=login=surface,...", where
// surface is admin, proxy, or none (denied everywhere); an empty login
// expects no such peer. Build it for a host with
// `GOOS=linux go test -c ./internal/taproot/access`.
func TestLiveTailscaled(t *testing.T) {
	socket := os.Getenv("TAPROOT_LIVE_TAILSCALE_SOCKET")
	cases := os.Getenv("TAPROOT_LIVE_WHOIS")
	devices := os.Getenv("TAPROOT_LIVE_DEVICES")
	if socket == "" || cases == "" || devices == "" {
		t.Skip("TAPROOT_LIVE_TAILSCALE_SOCKET, TAPROOT_LIVE_DEVICES, and TAPROOT_LIVE_WHOIS not set")
	}
	client := newLocalClient(socket)
	self, err := client.status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	t.Logf("self: id=%s dns=%s ips=%v", self.StableID, self.DNSName, self.IPs)
	tn, err := client.tailnet(context.Background())
	if err != nil {
		t.Fatalf("status?peers=true: %v", err)
	}
	t.Logf("tailnet: self %s, %d peers", tn.Self.ID, len(tn.Peers))

	cfg := Config{TailscaleSocket: socket, Devices: []DeviceGrant{}}
	for _, entry := range strings.Split(devices, ",") {
		id, perms, _ := strings.Cut(strings.TrimSpace(entry), "=")
		cfg.Devices = append(cfg.Devices, DeviceGrant{ID: id, Proxy: perms == "proxy" || perms == "both", Admin: perms == "admin" || perms == "both"})
	}
	g := newTestGuard(t, cfg)
	e, _ := testEngine(g)
	short, _, _ := strings.Cut(self.DNSName, ".")
	for _, entry := range strings.Split(cases, ",") {
		parts := strings.Split(strings.TrimSpace(entry), "=")
		if len(parts) != 3 {
			t.Fatalf("bad TAPROOT_LIVE_WHOIS entry %q", entry)
		}
		ip, wantLogin, wantSurface := parts[0], parts[1], parts[2]
		t.Run(ip, func(t *testing.T) {
			p, err := client.whois(context.Background(), mustAddr(t, ip))
			switch {
			case wantLogin == "" && !errors.Is(err, errPeerNotFound):
				t.Fatalf("whois err = %v, want errPeerNotFound", err)
			case wantLogin != "" && err != nil:
				t.Fatalf("whois: %v", err)
			case wantLogin != "" && p.Login != wantLogin:
				t.Fatalf("login = %q, want %q", p.Login, wantLogin)
			}
			// Requests as Claude Code and Codex send them: Host is the base URL's authority.
			hostHeader := map[string]string{"Host": short + ":8317"}
			proxy := do(e, ip+":40000", "/v1/models", hostHeader).Code == http.StatusOK
			admin := do(e, ip+":40000", "/taproot/api/v1/me", hostHeader).Code == http.StatusOK
			got := "none"
			switch {
			case admin:
				got = "admin"
			case proxy:
				got = "proxy"
			}
			t.Logf("ip=%s login=%q node=%q stableID=%s proxy=%v admin=%v", ip, p.Login, p.Node, p.StableID, proxy, admin)
			if got != wantSurface {
				t.Fatalf("surface = %s, want %s under TAPROOT_LIVE_DEVICES", got, wantSurface)
			}
		})
	}
}
