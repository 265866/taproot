// Package access admits requests by the caller's Tailscale identity instead of
// API keys or the management key.
//
// # Security model
//
// The server has two surfaces. The admin surface reads or changes
// credentials, settings, backups, and plugins: the dashboard (GET and HEAD /),
// /v0/management/**, /v8/management/**, /taproot/**, plugin pages and assets
// under /v0/resource/plugins/**, the OAuth callbacks on the main port, and
// the /v1/ws provider gateway. Everything else is the proxy surface.
//
// Callers are identified by the connection's source address only:
//
//   - Loopback reaches both surfaces. Only processes inside the container, or
//     on the development machine, connect from loopback.
//   - trusted-cidrs reach the proxy only. They hold host-local addresses, such
//     as the LAN address that server's 127.0.1.1 publish arrives from.
//   - A tailnet address reaches the proxy when tailscaled's whois login is in
//     allow-logins. It reaches the admin surface when, in addition, the node is
//     not the server's own node, and, if admin-nodes is set, the node is listed
//     there. The server's own node is excluded because every process and
//     container on the host connects through it, and they all resolve to the
//     host owner's login.
//
// The server's own node comes from tailscaled's LocalAPI status. Until it has
// been read once, tailnet callers get no admin access and only local host
// names pass the Host check.
//
// The admin surface is keyless for admitted callers, so a browser is its weak
// point: any page the admin visits could send requests with the admin's
// tailnet address. The guard therefore rejects, on both surfaces and before
// upstream's wildcard CORS middleware runs:
//
//   - a Host that is not one of the server's names (DNS rebinding),
//   - an Origin whose host and port differ from the request's Host, which
//     covers CORS preflights and websocket upgrades,
//   - Sec-Fetch-Site cross-site or same-site, except top-level navigations to
//     the dashboard and the OAuth callbacks.
//
// Command-line clients send none of these headers. The dashboard response
// also forbids framing (see package web).
package access

import (
	"context"
	"crypto/rand"
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

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	defaultSocket   = "/var/run/tailscale/tailscaled.sock"
	defaultCacheTTL = time.Minute
	// selfRetry spaces LocalAPI status attempts while the last one failed.
	selfRetry     = 5 * time.Second
	managementEnv = "MANAGEMENT_PASSWORD"
	identityKey   = "taproot.access.identity"
)

// DefaultAllowLogins is used when allow-logins is absent from taproot.yaml.
var DefaultAllowLogins = []string{"alice@github"}

var tailnetPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

// Config is the access section of taproot.yaml.
type Config struct {
	// Enabled defaults to true. When false the guard admits everything and
	// leaves management auth to upstream.
	Enabled *bool `yaml:"enabled"`
	// AllowLogins lists admitted Tailscale login names. Absent means DefaultAllowLogins.
	AllowLogins []string `yaml:"allow-logins"`
	// AdminNodes, when set, limits the admin surface to these tailnet nodes.
	// An entry is a MagicDNS name, either the full name
	// (desktop.tailnet-0000.ts.net) or its first label for nodes in the
	// server's own tailnet (desktop). Control assigns these names; the
	// device-reported hostname is not used.
	AdminNodes []string `yaml:"admin-nodes"`
	// TrustedCIDRs reach the proxy surface without a whois lookup. Use single
	// host-local addresses (/32), never a whole LAN.
	TrustedCIDRs []string `yaml:"trusted-cidrs"`
	// ExtraHosts are additional Host names or addresses the server answers to,
	// besides localhost, loopback, and its own tailnet names and addresses.
	ExtraHosts      []string      `yaml:"extra-hosts"`
	TailscaleSocket string        `yaml:"tailscale-socket"`
	CacheTTL        time.Duration `yaml:"cache-ttl"`
}

// Via says how a caller was admitted.
type Via string

const (
	ViaLoopback Via = "loopback"
	ViaTailnet  Via = "tailnet"
	ViaCIDR     Via = "cidr"
)

// Identity is an admitted caller.
type Identity struct {
	Login       string
	DisplayName string
	Node        string
	Via         Via
	// Admin is true when the caller may reach the admin surface.
	Admin bool
	// notAdmin says why Admin is false.
	notAdmin string
}

// Guard decides whether a request may reach the proxy or the admin surface.
type Guard struct {
	enabled    bool
	trusted    []netip.Prefix
	allow      atomic.Pointer[[]string]
	adminNodes []string
	extraHosts map[string]bool
	secret     string
	whois      func(context.Context, netip.Addr) (peer, error)
	status     func(context.Context) (selfNode, error)
	ttl        time.Duration
	now        func() time.Time

	mu    sync.Mutex
	cache map[netip.Addr]cacheEntry

	fetchMu     sync.Mutex
	selfMu      sync.Mutex
	selfNode    *selfNode
	selfExpires time.Time
	selfRetryAt time.Time
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
	if !g.enabled {
		log.Warn("taproot access: guard disabled; every caller reaches the proxy")
		return g, nil
	}
	if g.ttl < 0 {
		return nil, fmt.Errorf("cache-ttl must not be negative, got %s", g.ttl)
	}
	if g.ttl == 0 {
		g.ttl = defaultCacheTTL
	}
	for _, raw := range cfg.TrustedCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("trusted-cidrs %q: %w", raw, err)
		}
		if !prefix.IsSingleIP() {
			log.Warnf("taproot access: trusted-cidrs %s covers more than one address; every host in it reaches the proxy without a login", prefix.Masked())
		}
		g.trusted = append(g.trusted, prefix.Masked())
	}
	for _, raw := range cfg.AdminNodes {
		node := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if node == "" || strings.ContainsAny(node, " /:") {
			return nil, fmt.Errorf("admin-nodes: invalid node name %q", raw)
		}
		g.adminNodes = append(g.adminNodes, node)
	}
	for _, raw := range cfg.ExtraHosts {
		host := hostOnly(raw)
		if host == "" || strings.ContainsAny(host, " /") {
			return nil, fmt.Errorf("extra-hosts: invalid host %q", raw)
		}
		g.extraHosts[host] = true
	}
	logins := cfg.AllowLogins
	if logins == nil {
		logins = DefaultAllowLogins
	}
	if err := g.SetAllowLogins(logins); err != nil {
		return nil, fmt.Errorf("allow-logins: %w", err)
	}
	socket := cfg.TailscaleSocket
	if socket == "" {
		socket = defaultSocket
	}
	client := newLocalClient(socket)
	g.whois = client.whois
	g.status = client.status

	secret, err := managementSecret()
	if err != nil {
		return nil, err
	}
	g.secret = secret
	log.Infof("taproot access: guard enabled with %d allowed logins, %d admin nodes, %d trusted CIDRs, %d extra hosts, tailscaled socket %s",
		len(logins), len(g.adminNodes), len(g.trusted), len(g.extraHosts), socket)
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

// SetAllowLogins replaces the admitted login list at runtime. Matching is
// case-insensitive. An empty list is rejected so a settings change cannot lock
// out every tailnet caller.
func (g *Guard) SetAllowLogins(logins []string) error {
	normalized := make([]string, 0, len(logins))
	for _, login := range logins {
		login = strings.ToLower(strings.TrimSpace(login))
		if login == "" {
			return errors.New("login must not be blank")
		}
		if !slices.Contains(normalized, login) {
			normalized = append(normalized, login)
		}
	}
	if len(normalized) == 0 {
		return errors.New("at least one login is required")
	}
	g.allow.Store(&normalized)
	return nil
}

// AllowLogins returns the current admitted login list.
func (g *Guard) AllowLogins() []string {
	if p := g.allow.Load(); p != nil {
		return slices.Clone(*p)
	}
	return nil
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
		if !g.enabled || path == "/healthz" {
			c.Next()
			return
		}
		surf := classify(path)
		self := sync.OnceValue(func() *selfNode { return g.self(context.WithoutCancel(r.Context())) })
		id, reason := g.admit(r.Context(), r.RemoteAddr, self)
		if reason == "" && surf == surfaceAdmin && !id.Admin {
			reason = id.notAdmin
		}
		if reason == "" {
			reason = g.browserCheck(r, self)
		}
		if reason != "" {
			// Upstream's log formatter drops fields it does not know, so the details go in the message.
			log.Infof("taproot access: denied %s %s (%s) from %s: %s", r.Method, path, surf, r.RemoteAddr, reason)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
				"code":    "forbidden",
				"message": "caller is not allowed",
			}})
			return
		}
		c.Set(identityKey, id)
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

// admit returns the caller's identity, or a non-empty denial reason.
func (g *Guard) admit(ctx context.Context, remoteAddr string, self func() *selfNode) (Identity, string) {
	addrPort, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return Identity{}, "unparseable remote address"
	}
	addr := addrPort.Addr().Unmap().WithZone("")
	if addr.IsLoopback() {
		return Identity{Via: ViaLoopback, Node: addr.String(), Admin: true}, ""
	}
	for _, prefix := range g.trusted {
		if prefix.Contains(addr) {
			return Identity{Via: ViaCIDR, Node: addr.String(), notAdmin: "trusted-cidrs callers reach the proxy only"}, ""
		}
	}
	if !isTailnet(addr) {
		return Identity{}, "not a tailnet address"
	}
	p, err := g.lookup(ctx, addr)
	if err != nil {
		return Identity{}, err.Error()
	}
	if !slices.Contains(*g.allow.Load(), strings.ToLower(p.Login)) {
		return Identity{}, "login " + p.Login + " not in allow-logins"
	}
	id := Identity{Login: p.Login, DisplayName: p.DisplayName, Node: p.Node, Via: ViaTailnet}
	s := self()
	switch {
	case s == nil:
		id.notAdmin = "this server's own tailnet node is unknown, so tailnet callers reach the proxy only"
	case containsAddr(s.IPs, addr) || (p.StableID != "" && p.StableID == s.StableID):
		id.notAdmin = "this server's own node reaches the proxy only"
	case !g.isAdminNode(p.Node, s):
		id.notAdmin = "node " + p.Node + " not in admin-nodes"
	default:
		id.Admin = true
	}
	return id, ""
}

// isAdminNode matches a whois MagicDNS name against admin-nodes. A short
// entry matches only nodes in the server's own tailnet.
func (g *Guard) isAdminNode(node string, self *selfNode) bool {
	if len(g.adminNodes) == 0 {
		return true
	}
	node = strings.ToLower(node)
	short := ""
	if _, suffix, ok := strings.Cut(self.DNSName, "."); ok && suffix != "" {
		short, _ = strings.CutSuffix(node, "."+suffix)
		if short == node {
			short = ""
		}
	}
	for _, entry := range g.adminNodes {
		if entry == node || (short != "" && entry == short) {
			return true
		}
	}
	return false
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
	defer g.selfMu.Unlock()
	if err != nil {
		g.selfRetryAt = now.Add(selfRetry)
		if g.selfNode == nil {
			log.Warnf("taproot access: cannot read this server's tailnet node, so tailnet callers get no admin access and only local host names are accepted: %v", err)
		} else {
			log.Warnf("taproot access: cannot refresh this server's tailnet node, keeping %s: %v", g.selfNode.DNSName, err)
		}
		return g.selfNode
	}
	g.selfNode = &s
	g.selfExpires = now.Add(g.ttl)
	return g.selfNode
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
}

type meResponse struct {
	Login       string `json:"login"`
	DisplayName string `json:"displayName"`
	Node        string `json:"node"`
	Via         string `json:"via"`
	Admin       bool   `json:"admin"`
}

func (g *Guard) handleMe(c *gin.Context) {
	if !g.enabled {
		c.JSON(http.StatusOK, meResponse{Via: "disabled", Admin: true})
		return
	}
	id, ok := IdentityFrom(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "forbidden", "message": "no admitted identity"}})
		return
	}
	c.JSON(http.StatusOK, meResponse{Login: id.Login, DisplayName: id.DisplayName, Node: id.Node, Via: string(id.Via), Admin: id.Admin})
}
