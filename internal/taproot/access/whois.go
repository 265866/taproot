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

// whoisTimeout bounds one LocalAPI call so a stuck tailscaled cannot stall every request.
const whoisTimeout = 3 * time.Second

var errPeerNotFound = errors.New("tailscale: no peer with that address")

// peer is the part of tailscaled's apitype.WhoIsResponse the guard uses.
type peer struct {
	Login       string
	DisplayName string
	Node        string
}

// whoisClient queries tailscaled's LocalAPI over its unix socket. It speaks
// GET /localapi/v0/whois?addr=<ip>, as tailscale.com/client/local does, and
// decodes only the fields it needs, so the tailscale.com module is not a dependency.
type whoisClient struct {
	http *http.Client
}

func newWhoisClient(socketPath string) *whoisClient {
	var dialer net.Dialer
	return &whoisClient{http: &http.Client{
		Transport: &http.Transport{
			// On Windows there is no tailscaled socket at this path, so the dial
			// fails, every tailnet lookup errors, and only loopback and
			// trusted-cidrs callers are admitted.
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			},
			DisableCompression: true,
		},
		Timeout: whoisTimeout,
	}}
}

type whoisResponse struct {
	Node *struct {
		Name         string
		ComputedName string
	}
	UserProfile *struct {
		LoginName   string
		DisplayName string
	}
}

func (w *whoisClient) whois(ctx context.Context, addr netip.Addr) (peer, error) {
	reqURL := "http://" + localAPIHost + "/localapi/v0/whois?addr=" + url.QueryEscape(addr.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return peer{}, fmt.Errorf("build whois request: %w", err)
	}
	resp, err := w.http.Do(req)
	if err != nil {
		return peer{}, fmt.Errorf("tailscale whois %s: %w", addr, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return peer{}, fmt.Errorf("read tailscale whois %s: %w", addr, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return peer{}, fmt.Errorf("tailscale whois %s: %w", addr, errPeerNotFound)
	default:
		return peer{}, fmt.Errorf("tailscale whois %s: status %d: %s", addr, resp.StatusCode, strings.TrimSpace(string(body)))
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
		p.Node = strings.TrimSuffix(parsed.Node.Name, ".")
		if p.Node == "" {
			p.Node = parsed.Node.ComputedName
		}
	}
	return p, nil
}
