package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// localAPIHost is the Host tailscaled's LocalAPI requires on unix-socket requests.
const localAPIHost = "local-tailscaled.sock"

// localAPITimeout bounds one LocalAPI call so a stuck tailscaled cannot stall every request.
const localAPITimeout = 3 * time.Second

var errPeerNotFound = errors.New("tailscale: no peer with that address")

// peer is the part of tailscaled's apitype.WhoIsResponse the guard uses.
type peer struct {
	Login       string
	DisplayName string
	// Node is the MagicDNS name control assigned, without the trailing dot.
	Node     string
	StableID string
}

// selfNode is the server's own tailnet node, from LocalAPI status.
type selfNode struct {
	StableID string
	// DNSName is the MagicDNS name without the trailing dot, for example server.tailnet-0000.ts.net.
	DNSName string
	IPs     []netip.Addr
}

// localClient queries tailscaled's LocalAPI over its unix socket. It speaks
// the same endpoints as tailscale.com/client/local and decodes only the fields
// it needs, so the tailscale.com module is not a dependency.
type localClient struct {
	http *http.Client
}

func newLocalClient(socketPath string) *localClient {
	var dialer net.Dialer
	return &localClient{http: &http.Client{
		Transport: &http.Transport{
			// On Windows there is no tailscaled socket at this path, so the dial
			// fails, every tailnet lookup errors, and only loopback and
			// trusted-cidrs callers are admitted.
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			},
			DisableCompression: true,
		},
		Timeout: localAPITimeout,
	}}
}

// get returns the body of a 200 response, errPeerNotFound for a 404, or an error.
func (l *localClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+localAPIHost+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, errPeerNotFound
	default:
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

type whoisResponse struct {
	Node *struct {
		StableID     string
		Name         string
		ComputedName string
	}
	UserProfile *struct {
		LoginName   string
		DisplayName string
	}
}

func (l *localClient) whois(ctx context.Context, addr netip.Addr) (peer, error) {
	body, err := l.get(ctx, "/localapi/v0/whois?addr="+url.QueryEscape(addr.String()))
	if err != nil {
		return peer{}, fmt.Errorf("tailscale whois %s: %w", addr, err)
	}
	var parsed whoisResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return peer{}, fmt.Errorf("decode tailscale whois %s: %w", addr, err)
	}
	if parsed.UserProfile == nil || parsed.UserProfile.LoginName == "" {
		return peer{}, fmt.Errorf("tailscale whois %s: response has no login", addr)
	}
	p := peer{Login: parsed.UserProfile.LoginName, DisplayName: parsed.UserProfile.DisplayName}
	if parsed.Node != nil {
		p.StableID = parsed.Node.StableID
		p.Node = strings.TrimSuffix(parsed.Node.Name, ".")
		if p.Node == "" {
			p.Node = parsed.Node.ComputedName
		}
	}
	return p, nil
}

type statusResponse struct {
	Self *struct {
		ID           string
		DNSName      string
		TailscaleIPs []string
	}
}

// status returns the server's own node from GET /localapi/v0/status.
func (l *localClient) status(ctx context.Context) (selfNode, error) {
	body, err := l.get(ctx, "/localapi/v0/status?peers=false")
	if err != nil {
		return selfNode{}, fmt.Errorf("tailscale status: %w", err)
	}
	var parsed statusResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return selfNode{}, fmt.Errorf("decode tailscale status: %w", err)
	}
	if parsed.Self == nil || len(parsed.Self.TailscaleIPs) == 0 {
		return selfNode{}, errors.New("tailscale status: no self node with addresses")
	}
	self := selfNode{StableID: parsed.Self.ID, DNSName: strings.ToLower(strings.TrimSuffix(parsed.Self.DNSName, "."))}
	for _, raw := range parsed.Self.TailscaleIPs {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			return selfNode{}, fmt.Errorf("tailscale status: self address %q: %w", raw, err)
		}
		self.IPs = append(self.IPs, addr.Unmap())
	}
	return self, nil
}
