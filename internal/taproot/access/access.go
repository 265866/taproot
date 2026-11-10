// Package access admits requests by the caller's Tailscale device instead of
// API keys or the management key.
//
// # Security model
//
// The server has two surfaces. The admin surface reads or changes
// credentials, settings, backups, and plugins: the dashboard (GET and HEAD /
// and its chunks under /assets/), /v0/management/**, /v8/management/**,
// /taproot/**, plugin pages and assets under /v0/resource/plugins/**, the
// OAuth callbacks on the main port, and the /v1/ws provider gateway.
// Everything else is the proxy surface.
//
// Callers are identified by the connection's source address only:
//
//   - Loopback reaches both surfaces. Only processes inside the container, or
//     on the development machine, connect from loopback.
//   - access.addresses reach the proxy only. They hold host-local addresses,
//     such as the LAN address that a host-local publish arrives from.
//   - The server's own tailnet node reaches the proxy only, whatever
//     access.devices says. Every process and container on the host connects
//     through it, so it never reaches the admin surface. whois decides which
//     node an address belongs to; the status address list is the fallback
//     when whois fails.
//   - Any other tailnet address is resolved with tailscaled's whois to a
//     device's stable ID, and access.devices decides: proxy: true reaches the
//     proxy, admin: true the admin surface. A device that is not listed has
//     neither. Names never grant anything, because a stable ID is never
//     reused and a name can be. A device with Admin also reads GET
//     /v1/models without Proxy: the dashboard shows the model list, which
//     comes from the local model registry and costs no quota.
//
// Every caller with an identity may load the dashboard page and its chunks,
// which are static, so a device without Admin can ask for access there.
// A tailnet device that is refused is told its own name, login, and ID, and
// unless it has Admin, its refusal is recorded as an access request (see
// requests.go); nobody else gets more than "forbidden". A device without
// Admin may also POST to RequestPath, which records a request and grants
// nothing.
//
// While no device has Admin, the server is unclaimed: Setup logs a one-time
// setup code, and a tailnet device other than the server, without Admin, may
// POST it to ClaimPath to become the first admin (see claim.go). That route
// is the only admin route such a device reaches, and only while unclaimed.
//
// The server's own node comes from tailscaled's LocalAPI status. Until it has
// been read once, tailnet callers get no admin access, only local host names
// pass the Host check, and the server's own node is just another device.
//
// The admin surface is keyless for admitted callers, so a browser is its weak
// point: any page the admin visits could send requests with the admin's
// tailnet address. The guard therefore rejects, on both surfaces and before
// upstream's wildcard CORS middleware runs:
//
//   - a Host that is not one of the server's names (DNS rebinding),
//   - an Origin whose host and port differ from the request's Host, which
//     covers CORS preflights, websocket upgrades, and every cross-site
//     request a browser sends with a method other than GET or HEAD,
//   - Sec-Fetch-Site cross-site or same-site, except top-level navigations to
//     the dashboard and the OAuth callbacks. Browsers send Sec-Fetch-* only to
//     https and loopback targets, so over plain http to a tailnet name this
//     layer does nothing,
//   - a GET that changes state without RequestHeader, from any caller. This
//     closes the gap the previous point leaves: a cross-site no-cors GET
//     carries no Origin and cannot read the response, but it would still run
//     the handler.
//
// Command-line clients send none of these headers. The dashboard response
// also forbids framing (see package web).
//
// Upstream also serves a Redis-protocol usage queue on the proxy port, outside
// gin. RESPFilter limits it to loopback.
//
// With access.enabled false the guard checks nothing on the proxy surface,
// but the admin surface still answers loopback only, with the browser checks
// above, because a browser on the host also connects from loopback. Turning
// the guard off never hands tokens, settings, or backups to the network or to
// another site's page.
package access

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/265866/taproot/internal/logging"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	defaultSocket   = "/var/run/tailscale/tailscaled.sock"
	defaultCacheTTL = time.Minute
	maxCacheTTL     = 10 * time.Minute
	// selfRetry spaces LocalAPI status attempts while the last one failed.
	selfRetry     = 5 * time.Second
	managementEnv = "MANAGEMENT_PASSWORD"
	identityKey   = "taproot.access.identity"
)

var tailnetPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

// Config is the access section of taproot.yaml.
type Config struct {
	// Enabled defaults to true. When false the guard admits every caller to
	// the proxy surface, keeps the admin surface loopback-only, and leaves
	// management auth to upstream.
	Enabled *bool `yaml:"enabled"`
	// Devices grants Proxy and Admin to tailnet devices by stable ID. A device
	// that is not listed has neither. The Access page edits this list.
	Devices []DeviceGrant `yaml:"devices"`
	// Addresses reach the proxy surface without Tailscale, and never the
	// admin surface. Use single host-local addresses (/32), never a whole LAN.
	Addresses []AddressGrant `yaml:"addresses"`
	// ExtraHosts are additional Host names or addresses the server answers to,
	// besides localhost, loopback, and its own tailnet names and addresses.
	ExtraHosts      []string      `yaml:"extra-hosts"`
	TailscaleSocket string        `yaml:"tailscale-socket"`
	CacheTTL        time.Duration `yaml:"cache-ttl"`
}

// DeviceGrant is one tailnet device's permissions.
type DeviceGrant struct {
	// ID is the device's stable node ID, such as nQ7v2xK8CNTRL (tailscale
	// status --json shows it as ID). Tailscale never reuses one.
	ID string `yaml:"id" json:"id"`
	// Name is the device's Tailscale name, for people reading the file.
	// Taproot refreshes it from the tailnet, and it grants nothing.
	Name string `yaml:"name,omitempty" json:"name"`
	// Label is the owner's own name for the device. Taproot never changes
	// it, and it grants nothing.
	Label string `yaml:"label,omitempty" json:"label"`
	// Proxy lets the device send model requests.
	Proxy bool `yaml:"proxy" json:"proxy"`
	// Admin lets the device open the dashboard and the management API.
	Admin bool `yaml:"admin" json:"admin"`
}

// AddressGrant is an address outside Tailscale that reaches the proxy.
type AddressGrant struct {
	// CIDR is an address or prefix, such as 192.0.2.20/32. A bare address
	// means just that address.
	CIDR string `yaml:"cidr" json:"cidr"`
	Note string `yaml:"note,omitempty" json:"note"`
}

// Grants are the access lists the guard enforces, as taproot.yaml holds them.
type Grants struct {
	Devices   []DeviceGrant
	Addresses []AddressGrant
}

// removedKeys are access keys from before per-device permissions. taproot.yaml
// is strict, so they would fail anyway; this names what replaces them.
var removedKeys = map[string]string{
	"allow-logins":  "access.allow-logins was removed: access is granted per device now. List each device that may use taproot under access.devices, with its stable ID (tailscale status --json shows it as ID), a name, and proxy: true",
	"admin-nodes":   "access.admin-nodes was removed: access is granted per device now. Give each admin device an entry under access.devices with its stable ID (tailscale status --json shows it as ID), a name, and admin: true",
	"trusted-cidrs": "access.trusted-cidrs was removed: list each address under access.addresses with cidr (such as 192.0.2.20/32) and an optional note",
}

// RemovedKey returns an error naming the replacement when key is an access
// key taproot no longer reads, and nil otherwise.
func RemovedKey(key string) error {
	if msg, ok := removedKeys[key]; ok {
		return errors.New(msg + "; see examples/taproot/taproot.yaml")
	}
	return nil
}

// Via says how a caller was admitted, or for a device, how its requests
// reached the server.
type Via string

const (
	ViaLoopback Via = "loopback"
	ViaTailnet  Via = "tailnet"
	ViaCIDR     Via = "cidr"
	// ViaPlugin marks a device for a plugin's in-process requests. The guard
	// never admits a caller with it.
	ViaPlugin Via = "plugin"
)

// Identity is a caller the guard identified. Proxy and Admin say which
// surfaces it reaches; a tailnet device may have neither.
type Identity struct {
	Login       string
	DisplayName string
	Node        string
	// NodeID is the node's stable ID from whois.
	NodeID string
	Via    Via
	// Outside marks a tailnet device outside the server's own tailnet: whois
	// names no node (Tailscale hides the devices of a person the server is
	// shared with), or a node in another tailnet.
	Outside bool
	// Proxy is true when the caller may reach the proxy surface.
	Proxy bool
	// Admin is true when the caller may reach the admin surface.
	Admin bool
	// notAdmin says why Admin is false.
	notAdmin string
	// device is true for a tailnet device other than the server, whose
	// grant decides, so a denial may tell it its name and which switch to
	// turn on.
	device bool
	// noAdminGrant is true when Admin is false because the device's grant
	// lacks it.
	noAdminGrant bool
	// self is true for a tailnet caller on the server's own node.
	self bool
}

// adminGrant reports whether the caller is a tailnet device whose grant has
// Admin, including while the guard withholds it because the server's own node
// is unknown. Such a device can turn on anything itself on the Access page.
func (id Identity) adminGrant() bool {
	return id.device && !id.noAdminGrant
}

// Guard decides whether a request may reach the proxy or the admin surface.
type Guard struct {
	enabled    bool
	grants     atomic.Pointer[grantSet]
	extraHosts map[string]bool
	secret     string
	socket     string
	whois      func(context.Context, netip.Addr) (peer, error)
	status     func(context.Context) (selfNode, error)
	tailnet    func(context.Context) (Tailnet, error)
	ttl        time.Duration
	now        func() time.Time

	mu    sync.Mutex
	cache map[netip.Addr]cacheEntry

	fetchMu     sync.Mutex
	selfMu      sync.Mutex
	selfNode    *selfNode
	selfExpires time.Time
	selfRetryAt time.Time

	// missing tracks granted devices absent from tailscaled's list.
	missing missingDevices

	requests requestDevices
	// own names access.addresses devices only; admission never reads it.
	own ownAddrs

	claim setupClaim

	sockets openSockets

	// asks stores access requests; nil until AttachRequests.
	asks atomic.Pointer[Requests]
}

// grantSet is Grants checked and indexed for admission.
type grantSet struct {
	Grants
	byID     map[string]DeviceGrant
	prefixes []netip.Prefix
}

type cacheEntry struct {
	peer    peer
	expires time.Time
}

// New builds the guard. It must run before the management handler is
// constructed, because upstream reads MANAGEMENT_PASSWORD at construction and
// only then registers the management routes.
func New(cfg Config) (*Guard, error) {
	g := &Guard{
		enabled:    cfg.Enabled == nil || *cfg.Enabled,
		ttl:        cfg.CacheTTL,
		now:        time.Now,
		cache:      make(map[netip.Addr]cacheEntry),
		extraHosts: make(map[string]bool),
	}
	// The Host check needs extra-hosts with the guard off too.
	for _, raw := range cfg.ExtraHosts {
		host := hostOnly(raw)
		if host == "" || strings.ContainsAny(host, " /") {
			return nil, fmt.Errorf("extra-hosts: invalid host %q", raw)
		}
		g.extraHosts[host] = true
	}
	// The Access page lists the configured grants with the guard off too.
	if err := g.SetGrants(Grants{Devices: cfg.Devices, Addresses: cfg.Addresses}); err != nil {
		return nil, err
	}
	if !g.enabled {
		log.Warn("taproot access: guard disabled (access.enabled: false). Every caller that reaches the port, from any address and without a login, can use the proxy routes (/v1/..., /v1beta/..., /backend-api/codex/...) and spend every account's quota, and no Host, Origin, or cross-site check runs there. The dashboard, /taproot/**, /v0/management/**, /v8/management/**, plugin pages, the OAuth callbacks, and /v1/ws stay loopback-only and refuse browser requests from other sites.")
		return g, nil
	}
	if g.ttl < 0 || g.ttl > maxCacheTTL {
		return nil, fmt.Errorf("cache-ttl must be between 0 and %s, got %s: a device that takes over another's tailnet address keeps the old device's access for up to that long", maxCacheTTL, g.ttl)
	}
	if g.ttl == 0 {
		g.ttl = defaultCacheTTL
	}
	socket := cfg.TailscaleSocket
	if socket == "" {
		socket = defaultSocket
	}
	g.socket = socket
	client := newLocalClient(socket)
	g.whois = client.whois
	g.status = client.status
	g.tailnet = client.tailnet

	secret, err := managementSecret()
	if err != nil {
		return nil, err
	}
	g.secret = secret
	set := g.grants.Load()
	proxy, admin := 0, 0
	for _, d := range set.Devices {
		if d.Proxy {
			proxy++
		}
		if d.Admin {
			admin++
		}
	}
	log.Infof("taproot access: guard enabled with %d devices (%d with Proxy, %d with Admin), %d addresses, %d extra hosts, tailscaled socket %s",
		len(set.Devices), proxy, admin, len(set.Addresses), len(g.extraHosts), socket)
	if admin == 0 {
		log.Warn("taproot access: no device has Admin, so only loopback reaches the dashboard and the management API until a tailnet device claims the server with the setup code logged at startup")
	}
	return g, nil
}

// managementSecret returns MANAGEMENT_PASSWORD, generating and exporting a
// random one when the operator left it unset or blank.
func managementSecret() (string, error) {
	if v := strings.TrimSpace(os.Getenv(managementEnv)); v != "" {
		return v, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate management secret: %w", err)
	}
	secret := hex.EncodeToString(buf)
	if err := os.Setenv(managementEnv, secret); err != nil {
		return "", fmt.Errorf("set %s: %w", managementEnv, err)
	}
	return secret, nil
}

// SetGrants checks and replaces the device and address lists at runtime.
// Entries are trimmed, a device with neither permission is dropped, and a bare
// address becomes a single-address prefix.
func (g *Guard) SetGrants(in Grants) error {
	set, err := newGrantSet(in)
	if err != nil {
		return err
	}
	g.grants.Store(set)
	g.sockets.revoke(set)
	g.restartClaim()
	return nil
}

// CheckGrants reports what SetGrants would refuse in, without applying it.
func CheckGrants(in Grants) error {
	_, err := newGrantSet(in)
	return err
}

func newGrantSet(in Grants) (*grantSet, error) {
	set := &grantSet{Grants: Grants{Devices: []DeviceGrant{}, Addresses: []AddressGrant{}}, byID: map[string]DeviceGrant{}}
	seen := map[string]bool{}
	for _, d := range in.Devices {
		d.ID = strings.TrimSpace(d.ID)
		d.Name = strings.TrimSpace(d.Name)
		label, err := CleanLabel(d.Label)
		if err != nil {
			return nil, fmt.Errorf("access.devices: label of device %s: %w", d.ID, err)
		}
		d.Label = label
		// Stable IDs are case-sensitive, so they keep their case.
		if d.ID == "" || strings.ContainsFunc(d.ID, func(r rune) bool { return r <= ' ' || strings.ContainsRune("/:@", r) }) {
			return nil, fmt.Errorf("access.devices: invalid device id %q", d.ID)
		}
		if seen[d.ID] {
			return nil, fmt.Errorf("access.devices: device %s is listed twice", d.ID)
		}
		seen[d.ID] = true
		if !d.Proxy && !d.Admin {
			continue
		}
		set.byID[d.ID] = d
		set.Devices = append(set.Devices, d)
	}
	for _, a := range in.Addresses {
		raw := strings.TrimSpace(a.CIDR)
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			addr, errAddr := netip.ParseAddr(raw)
			if errAddr != nil {
				return nil, fmt.Errorf("access.addresses: %q is not an address or CIDR", a.CIDR)
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		// admit unmaps the caller, so a 4-in-6 prefix must be IPv4 to match.
		if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		prefix = prefix.Masked()
		// admit matches addresses before whois, so a covered tailnet device
		// would lose its identity: it would get Proxy without a grant, and an
		// admin device would lose Admin.
		for _, tailnet := range tailnetPrefixes {
			if prefix.Overlaps(tailnet) {
				return nil, fmt.Errorf("access.addresses: %s covers Tailscale addresses (%s); give tailnet devices Proxy under access.devices instead, or, to open the proxy to every caller, remove the entry and set access.enabled: false", prefix, tailnet)
			}
		}
		if slices.Contains(set.prefixes, prefix) {
			return nil, fmt.Errorf("access.addresses: %s is listed twice", prefix)
		}
		if !prefix.IsSingleIP() {
			log.Warnf("taproot access: access.addresses %s covers more than one address; every host in it reaches the proxy without Tailscale", prefix)
		}
		set.prefixes = append(set.prefixes, prefix)
		set.Addresses = append(set.Addresses, AddressGrant{CIDR: prefix.String(), Note: strings.TrimSpace(a.Note)})
	}
	return set, nil
}

// MaxLabelRunes bounds a device label and a requested device name.
const MaxLabelRunes = 64

// CleanLabel trims a device label or requested name and refuses one that is
// too long or holds control or format characters. Format characters (Cf)
// include bidi overrides and zero-width characters, which can make a name
// read as another.
func CleanLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if utf8.RuneCountInString(label) > MaxLabelRunes {
		return "", fmt.Errorf("longer than %d characters", MaxLabelRunes)
	}
	if !utf8.ValidString(label) || strings.ContainsFunc(label, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return "", errors.New("holds control or format characters")
	}
	return label, nil
}

// Version identifies the lists' content. A client that edits a copy of the
// lists sends it back, so a save made from a stale copy is refused instead of
// undoing changes saved since.
func (gr Grants) Version() string {
	var b []byte
	for _, d := range gr.Devices {
		b = fmt.Appendf(b, "d %q %q %q %t %t\n", d.ID, d.Name, d.Label, d.Proxy, d.Admin)
	}
	for _, a := range gr.Addresses {
		b = fmt.Appendf(b, "a %q %q\n", a.CIDR, a.Note)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// Grants returns a copy of the current device and address lists.
func (g *Guard) Grants() Grants {
	set := g.grants.Load()
	return Grants{Devices: slices.Clone(set.Devices), Addresses: slices.Clone(set.Addresses)}
}

// ServerNodeID returns the server's own stable node ID through the same
// lookup admission uses, reading tailscaled when the cached node is stale. It
// returns "" while the node was never read or the guard is off.
func (g *Guard) ServerNodeID(ctx context.Context) string {
	if !g.enabled {
		return ""
	}
	// Other callers may wait on this fetch, so it outlives a caller that hangs up.
	if self := g.self(context.WithoutCancel(ctx)); self != nil {
		return self.StableID
	}
	return ""
}

// IdentityFrom returns the identity the middleware admitted for this request.
func IdentityFrom(c *gin.Context) (Identity, bool) {
	v, ok := c.Get(identityKey)
	if !ok {
		return Identity{}, false
	}
	id, ok := v.(Identity)
	return id, ok
}

// Middleware runs on every route, ahead of upstream's CORS middleware. It
// identifies callers by the connection's source address only; forwarding and
// Tailscale-* headers are client-controlled on this port and are ignored.
func (g *Guard) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		r := c.Request
		path := r.URL.Path
		// Upstream's control panel is third-party HTML that would run
		// same-origin with keyless management.
		if path == "/management.html" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if path == "/healthz" {
			c.Next()
			return
		}
		surf := classify(path)
		if !g.enabled {
			// The switch opens the proxy only; tokens, settings, and backups stay
			// local. A browser on the same host still connects from loopback, so
			// the admin surface keeps the browser checks.
			if surf == surfaceAdmin {
				reason := "the guard is disabled, so only loopback reaches the admin surface"
				denial := gin.H{"code": "forbidden", "message": "the access guard is disabled, so only loopback reaches this route"}
				if isLoopback(r.RemoteAddr) {
					reason, denial = g.browserGuard(r, func() *selfNode { return nil })
				}
				if reason != "" {
					log.Infof("taproot access: denied %s %s (%s) from %s: %s", r.Method, quoteHeader(path), surf, r.RemoteAddr, reason)
					deny(c, denial, false)
					return
				}
			}
			c.Next()
			return
		}
		self := sync.OnceValue(func() *selfNode { return g.self(context.WithoutCancel(r.Context())) })
		id, reason := g.admit(r.Context(), r.RemoteAddr, self)
		denial := forbiddenDenial()
		if reason == "" {
			var browserDenial gin.H
			if reason, browserDenial = g.browserCheck(r, self); reason != "" {
				denial = browserDenial
			}
		}
		// Only a tailnet device on a request that passed the browser checks
		// gets a denial naming it, which tells it nothing it does not know.
		switch {
		case reason != "":
		case surf == surfaceProxy && !id.Proxy:
			if id.Admin && isModelList(r) {
				break
			}
			reason = "device " + quoteHeader(id.Node) + " (" + id.NodeID + ") has no Proxy"
			if id.device {
				denial = g.noProxyDenial(r, id)
				g.recordRefusal(r, id, AskProxy)
			}
		case surf == surfaceAdmin && !id.Admin && !isDashboardShell(r):
			if id.device && r.Method == http.MethodPost && path == ClaimPath && g.Claimable() {
				// The settings module checks the code and the caller.
				break
			}
			if id.device && r.Method == http.MethodPost && path == RequestPath {
				// handleRequest records the request; it grants nothing.
				if r.Header.Get(RequestHeader) != "1" {
					reason, denial = "access request without "+RequestHeader, requestHeaderDenial()
				}
				break
			}
			reason = id.notAdmin
			switch {
			case id.self:
				denial = gin.H{"code": "server_node", "message": "this is the taproot server's own tailnet node, which never gets Admin: open the dashboard from another device on the tailnet"}
			case id.adminGrant():
				// The grant has Admin, but the guard withholds it until it
				// knows which node is the server.
				denial = gin.H{"code": "server_node_unknown", "message": "taproot cannot tell yet whether this device is the server itself, because tailscaled's status is unavailable or out of date; try again in a few seconds"}
			case id.noAdminGrant:
				self, _ := g.cachedSelf()
				name := shortNodeName(id.Node, self)
				denial = deviceDenial("not_admin", id, name, fmt.Sprintf("%s (%s, ID %s): this device has no Admin permission. On a device that has it, turn on Admin for this one on the Access page of taproot's dashboard", cmp.Or(name, "this device"), id.Login, id.NodeID))
				// The connection gate words its advice by whether the device has Proxy.
				denial["proxy"] = id.Proxy
				// Read before this refusal is recorded, so a first visit gets
				// the gate's request form rather than its sent state.
				denial["requested"] = g.HasOpenRequest(r.Context(), id.NodeID)
				// While unclaimed, the gate offers the setup code form instead,
				// to a device that may claim the server. Any other device is
				// told the server has no admin yet, since no one could allow a
				// request from it.
				if g.Claimable() {
					denial["unclaimed"] = true
					if !id.Outside {
						denial["claimable"] = true
					}
				}
				g.recordRefusal(r, id, AskAdmin)
			}
		}
		if reason == "" && sideEffectGET(r) && r.Header.Get(RequestHeader) != "1" {
			reason = "state-changing GET without " + RequestHeader
			denial = requestHeaderDenial()
		}
		if reason != "" {
			// Upstream's log formatter drops fields it does not know, so the details go in the message.
			log.Infof("taproot access: denied %s %s (%s) from %s: %s", r.Method, quoteHeader(path), surf, r.RemoteAddr, reason)
			deny(c, denial, self() == nil)
			return
		}
		c.Set(identityKey, id)
		g.trackSocket(c, id, surf)
		if requestID := logging.GetGinRequestID(c); requestID != "" {
			g.requests.start(requestID, g.deviceFor(id))
			defer func() { g.requests.finish(requestID, g.now()) }()
		}
		if surf == surfaceAdmin {
			c.Header("X-Frame-Options", "SAMEORIGIN")
		}
		if isManagementPath(path) {
			r.Header.Set("Authorization", "Bearer "+g.secret)
			r.Header.Del("X-Management-Key")
		}
		c.Next()
	}
}

// deviceDenial is the 403 body for a tailnet device that lacks a permission.
// The message names the device, because command-line clients show only it.
func deviceDenial(code string, id Identity, name, message string) gin.H {
	return gin.H{
		"code":    code,
		"message": message,
		"node":    name,
		"nodeId":  id.NodeID,
		"login":   id.Login,
	}
}

// noProxyDenial is the 403 body for a model request from a device without
// Proxy. Claude Code prints the message after "Please run /login", which
// would not help, so the message says so and points at the dashboard on the
// name the client used, where the device can name itself in a request.
func (g *Guard) noProxyDenial(r *http.Request, id Identity) gin.H {
	self, _ := g.cachedSelf()
	name := shortNodeName(id.Node, self)
	scheme := "http"
	// Upstream's buffered connection reports an empty TLS state on plain
	// http, so net/http sets r.TLS there too; only a handshake means https.
	if r.TLS != nil && r.TLS.HandshakeComplete {
		scheme = "https"
	}
	// The Host passed the browser checks, so it is one of the server's names.
	format := "This device needs approval on taproot. Don't run /login. Open %s://%s on this device to name it, or ask the server's admin. (%s, %s, ID %s)"
	if id.adminGrant() {
		format = "This device has Admin but not Proxy on taproot. Don't run /login. Open %s://%s and turn on Proxy for this device on the Access page. (%s, %s, ID %s)"
	}
	message := fmt.Sprintf(format, scheme, r.Host, cmp.Or(name, "unnamed device"), id.Login, id.NodeID)
	return deviceDenial("no_proxy", id, name, message)
}

// browserGuard runs browserCheck and requires RequestHeader on state-changing
// GETs. It returns the denial reason and response body, or "" when the request
// passes.
func (g *Guard) browserGuard(r *http.Request, self func() *selfNode) (string, gin.H) {
	if reason, denial := g.browserCheck(r, self); reason != "" {
		return reason, denial
	}
	if sideEffectGET(r) && r.Header.Get(RequestHeader) != "1" {
		return "state-changing GET without " + RequestHeader, requestHeaderDenial()
	}
	return "", nil
}

func forbiddenDenial() gin.H {
	return gin.H{"code": "forbidden", "message": "caller is not allowed"}
}

func requestHeaderDenial() gin.H {
	return gin.H{"code": "request_header_required", "message": "this request changes state; send " + RequestHeader + ": 1"}
}

// isManagementPath covers the routes upstream authenticates with the management
// key, including plugin management routes served from NoRoute under /v0/management.
// Plugin resource routes (/v0/resource/plugins/*) are guarded but get no key:
// upstream serves them unauthenticated and forwards request headers to plugin code.
func isManagementPath(path string) bool {
	for _, base := range []string{"/v0/management", "/v8/management"} {
		if path == base || strings.HasPrefix(path, base+"/") {
			return true
		}
	}
	return false
}

// admit returns the caller's identity, or a non-empty denial reason when the
// caller has none. A tailnet device with an identity may still have neither
// permission; the middleware decides per surface.
func (g *Guard) admit(ctx context.Context, remoteAddr string, self func() *selfNode) (Identity, string) {
	addrPort, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return Identity{}, "unparseable remote address"
	}
	addr := addrPort.Addr().Unmap().WithZone("")
	if addr.IsLoopback() {
		return Identity{Via: ViaLoopback, Node: addr.String(), Proxy: true, Admin: true}, ""
	}
	set := g.grants.Load()
	for _, prefix := range set.prefixes {
		if prefix.Contains(addr) {
			return Identity{Via: ViaCIDR, Node: addr.String(), Proxy: true, notAdmin: "access.addresses callers reach the proxy only"}, ""
		}
	}
	if !isTailnet(addr) {
		return Identity{}, "not a tailnet address"
	}
	s := self()
	p, err := g.lookup(ctx, addr)
	// whois names the node behind the address now; the status address list can
	// be a refresh behind a reassignment, so it decides only when whois cannot.
	own := false
	if s != nil {
		if err == nil && p.StableID != "" {
			own = p.StableID == s.StableID
		} else {
			own = containsAddr(s.IPs, addr)
		}
	}
	if err != nil && !own {
		return Identity{}, err.Error()
	}
	if own {
		return Identity{
			Login: p.Login, DisplayName: p.DisplayName,
			Node: cmp.Or(p.Node, s.DNSName), NodeID: cmp.Or(p.StableID, s.StableID),
			Via: ViaTailnet, Proxy: true, notAdmin: "this server's own node reaches the proxy only", self: true,
		}, ""
	}
	if p.StableID == "" {
		return Identity{}, "whois for " + addr.String() + " names no node ID"
	}
	grant := set.byID[p.StableID]
	id := Identity{
		Login: p.Login, DisplayName: p.DisplayName, Node: p.Node, NodeID: p.StableID, Via: ViaTailnet,
		Proxy: grant.Proxy, device: true,
		Outside: p.Node == "" || (s != nil && otherTailnet(p.Node, s.DNSName)),
	}
	switch {
	case !grant.Admin:
		id.notAdmin = "device " + quoteHeader(p.Node) + " (" + p.StableID + ") has no Admin"
		id.noAdminGrant = true
	case s == nil:
		id.notAdmin = "this server's own tailnet node is unknown, so tailnet callers reach the proxy only"
	case containsAddr(s.IPs, addr):
		id.notAdmin = "the address is still this server's own in tailscaled status, so it reaches the proxy only"
	default:
		id.Admin = true
	}
	return id, ""
}

func isTailnet(addr netip.Addr) bool {
	for _, prefix := range tailnetPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// lookup caches the peer, not the decision, so allowlist changes apply at once.
// Failures are not cached.
func (g *Guard) lookup(ctx context.Context, addr netip.Addr) (peer, error) {
	now := g.now()
	g.mu.Lock()
	entry, ok := g.cache[addr]
	g.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.peer, nil
	}
	p, err := g.whois(ctx, addr)
	if err != nil {
		return peer{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.cache) >= 1024 {
		for key, e := range g.cache {
			if !now.Before(e.expires) {
				delete(g.cache, key)
			}
		}
	}
	g.cache[addr] = cacheEntry{peer: p, expires: now.Add(g.ttl)}
	return p, nil
}

// self returns the server's own tailnet node, refreshed every cache TTL. A
// failed refresh keeps the last known node; nil means it was never read.
func (g *Guard) self(ctx context.Context) *selfNode {
	cur, settled := g.cachedSelf()
	if settled {
		return cur
	}
	// Until the node is known, callers wait for the one fetch in flight
	// instead of being refused admin access; after that a refresh never blocks.
	if cur == nil {
		g.fetchMu.Lock()
	} else if !g.fetchMu.TryLock() {
		return cur
	}
	defer g.fetchMu.Unlock()
	if cur, settled = g.cachedSelf(); settled {
		return cur
	}

	s, err := g.status(ctx)
	now := g.now()
	g.selfMu.Lock()
	prev := g.selfNode
	if err != nil {
		g.selfRetryAt = now.Add(selfRetry)
		g.selfMu.Unlock()
		if prev == nil {
			log.Warnf("taproot access: cannot read this server's tailnet node, so tailnet callers get no admin access and only local host names are accepted: %v", err)
		} else {
			log.Warnf("taproot access: cannot refresh this server's tailnet node, keeping %s: %v", prev.DNSName, err)
		}
		return prev
	}
	g.selfNode = &s
	g.selfExpires = now.Add(g.ttl)
	g.selfMu.Unlock()
	if prev != nil && (prev.StableID != s.StableID || otherTailnet(prev.DNSName, s.DNSName)) {
		// The server moved to another node or tailnet, where the cached
		// addresses may belong to other devices.
		g.mu.Lock()
		clear(g.cache)
		g.mu.Unlock()
	}
	if prev == nil || prev.StableID != s.StableID {
		// A grant on the server's own node stops counting as an admin.
		g.restartClaim()
	}
	return &s
}

// cachedSelf returns the cached node and whether it may be used without a
// fetch: it is fresh, or the last fetch failed less than selfRetry ago.
func (g *Guard) cachedSelf() (*selfNode, bool) {
	now := g.now()
	g.selfMu.Lock()
	defer g.selfMu.Unlock()
	return g.selfNode, now.Before(g.selfExpires) || now.Before(g.selfRetryAt)
}

// Routes registers access endpoints under the taproot API group.
func (g *Guard) Routes(r *gin.RouterGroup) {
	r.GET("/me", g.handleMe)
	r.GET("/access/devices", g.handleDevices)
	r.POST("/access/request", g.handleRequest)
	r.POST("/access/requests/:nodeId/dismiss", g.handleDismiss)
	r.POST("/access/requests/:nodeId/allow", g.handleAllow)
}

type meResponse struct {
	Login       string `json:"login"`
	DisplayName string `json:"displayName"`
	Node        string `json:"node"`
	NodeID      string `json:"nodeId"`
	Via         string `json:"via"`
	Proxy       bool   `json:"proxy"`
	Admin       bool   `json:"admin"`
}

func (g *Guard) handleMe(c *gin.Context) {
	if !g.enabled {
		c.JSON(http.StatusOK, meResponse{Via: "disabled", Proxy: true, Admin: true})
		return
	}
	id, ok := IdentityFrom(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "forbidden", "message": "no admitted identity"}})
		return
	}
	c.JSON(http.StatusOK, meResponse{Login: id.Login, DisplayName: id.DisplayName, Node: id.Node, NodeID: id.NodeID, Via: string(id.Via), Proxy: id.Proxy, Admin: id.Admin})
}
