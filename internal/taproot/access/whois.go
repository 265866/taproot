package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
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
			// access.addresses callers are admitted.
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socketPath)
			},
			DisableCompression: true,
		},
		Timeout: localAPITimeout,
	}}
}

// hostSocket is where tailscaled puts its socket on a Linux host.
const hostSocket = "/var/run/tailscale/tailscaled.sock"

// CheckTailscaled fails when the guard is enabled and tailscaled's socket is
// missing or refuses a connection. The guard would then refuse every tailnet
// caller, so Setup stops at startup instead of serving a server only loopback
// can reach. It checks that the socket accepts a connection, not what
// tailscaled answers: the guard keeps retrying LocalAPI calls while tailscaled
// starts or logs in.
func (g *Guard) CheckTailscaled(ctx context.Context) error {
	if !g.enabled {
		return nil
	}
	fix := fmt.Sprintf("mount the host's tailscaled socket there (podman or docker: -v %s:%s; a quadlet: Volume=%s:%s), set access.tailscale-socket in taproot.yaml to where it is, or set access.enabled: false",
		hostSocket, g.socket, hostSocket, g.socket)
	if _, err := os.Stat(g.socket); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("tailscaled socket %s does not exist: %s", g.socket, fix)
	}
	dialer := net.Dialer{Timeout: localAPITimeout}
	conn, err := dialer.DialContext(ctx, "unix", g.socket)
	if err != nil {
		return fmt.Errorf("tailscaled socket %s is unreachable (%w): check that tailscaled runs on the host and that taproot may open the socket, or %s", g.socket, err, fix)
	}
	if errClose := conn.Close(); errClose != nil {
		log.WithError(errClose).Debug("taproot access: close tailscaled probe connection")
	}
	return nil
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
		StableID string
		Name     string
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
		// Only the control-assigned name: ComputedName derives from the
		// device-reported hostname, which the device controls.
		p.Node = strings.TrimSuffix(parsed.Node.Name, ".")
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

// TailnetDevice is one device from tailscaled's status list.
type TailnetDevice struct {
	// ID is the stable node ID.
	ID string
	// Name is the first label of the MagicDNS name, or the device's hostname
	// when it has none.
	Name string
	// DNSName is the MagicDNS name without the trailing dot.
	DNSName string
	// Login is the owner's login, or "" for a tagged device.
	Login string
	Tags  []string
	OS    string
	// Created is when the device joined the tailnet, or zero when unknown.
	Created time.Time
}

// Tailnet is the server's own node and every peer tailscaled lists.
type Tailnet struct {
	Self  TailnetDevice
	Peers []TailnetDevice
}

type peerStatus struct {
	ID       string
	HostName string
	DNSName  string
	OS       string
	UserID   int64
	Tags     []string
	Created  string
}

type tailnetStatus struct {
	BackendState string
	Self         *peerStatus
	Peer         map[string]*peerStatus
	User         map[string]struct{ LoginName string }
}

// tailnet returns the server's node and its peers from GET
// /localapi/v0/status?peers=true. A tailscaled that is not running is an
// error, because its peer list is empty or stale.
func (l *localClient) tailnet(ctx context.Context) (Tailnet, error) {
	body, err := l.get(ctx, "/localapi/v0/status?peers=true")
	if err != nil {
		return Tailnet{}, fmt.Errorf("tailscale status: %w", err)
	}
	var parsed tailnetStatus
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Tailnet{}, fmt.Errorf("decode tailscale status: %w", err)
	}
	if parsed.BackendState != "Running" {
		return Tailnet{}, fmt.Errorf("tailscale status: tailscaled is %q, not Running", parsed.BackendState)
	}
	if parsed.Self == nil || parsed.Self.ID == "" {
		return Tailnet{}, errors.New("tailscale status: no self node")
	}
	device := func(p *peerStatus) TailnetDevice {
		d := TailnetDevice{ID: p.ID, DNSName: strings.ToLower(strings.TrimSuffix(p.DNSName, ".")), OS: p.OS, Tags: p.Tags}
		d.Name, _, _ = strings.Cut(d.DNSName, ".")
		if d.Name == "" {
			d.Name = p.HostName
		}
		if len(p.Tags) == 0 {
			d.Login = parsed.User[strconv.FormatInt(p.UserID, 10)].LoginName
		}
		if created, err := time.Parse(time.RFC3339Nano, p.Created); err == nil {
			d.Created = created
		}
		return d
	}
	tn := Tailnet{Self: device(parsed.Self)}
	for _, p := range parsed.Peer {
		if p != nil && p.ID != "" {
			tn.Peers = append(tn.Peers, device(p))
		}
	}
	slices.SortFunc(tn.Peers, func(a, b TailnetDevice) int { return strings.Compare(a.ID, b.ID) })
	return tn, nil
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
