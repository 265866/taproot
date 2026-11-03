// Package access admits requests by the caller's Tailscale identity instead of
// API keys or the management key.
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
	managementEnv   = "MANAGEMENT_PASSWORD"
	identityKey     = "taproot.access.identity"
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
	// TrustedCIDRs are admitted without a whois lookup.
	TrustedCIDRs    []string      `yaml:"trusted-cidrs"`
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
}

// Guard decides whether a request may reach the proxy, the management API, or the dashboard.
type Guard struct {
	enabled bool
	trusted []netip.Prefix
	allow   atomic.Pointer[[]string]
	secret  string
	whois   func(context.Context, netip.Addr) (peer, error)
	ttl     time.Duration
	now     func() time.Time

	mu    sync.Mutex
	cache map[netip.Addr]cacheEntry
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
		enabled: cfg.Enabled == nil || *cfg.Enabled,
		ttl:     cfg.CacheTTL,
		now:     time.Now,
		cache:   make(map[netip.Addr]cacheEntry),
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
		g.trusted = append(g.trusted, prefix.Masked())
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
	client := newWhoisClient(socket)
	g.whois = client.whois

	secret, err := managementSecret()
	if err != nil {
		return nil, err
	}
	g.secret = secret
	log.Infof("taproot access: guard enabled with %d allowed logins, %d trusted CIDRs, tailscaled socket %s", len(logins), len(g.trusted), socket)
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

// Middleware runs on every route. It identifies callers by the connection's
// source address only; forwarding and Tailscale-* headers are client-controlled
// on this port and are ignored.
func (g *Guard) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !g.enabled || c.Request.URL.Path == "/healthz" {
			c.Next()
			return
		}
		id, reason := g.admit(c.Request.Context(), c.Request.RemoteAddr)
		if reason != "" {
			// Upstream's log formatter drops fields it does not know, so the details go in the message.
			log.Infof("taproot access: denied %s %s from %s: %s", c.Request.Method, c.Request.URL.Path, c.Request.RemoteAddr, reason)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": gin.H{
				"code":    "forbidden",
				"message": "caller is not allowed",
			}})
			return
		}
		c.Set(identityKey, id)
		if isManagementPath(c.Request.URL.Path) {
			c.Request.Header.Set("Authorization", "Bearer "+g.secret)
			c.Request.Header.Del("X-Management-Key")
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
func (g *Guard) admit(ctx context.Context, remoteAddr string) (Identity, string) {
	addrPort, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return Identity{}, "unparseable remote address"
	}
	addr := addrPort.Addr().Unmap().WithZone("")
	if addr.IsLoopback() {
		return Identity{Via: ViaLoopback, Node: addr.String()}, ""
	}
	for _, prefix := range g.trusted {
		if prefix.Contains(addr) {
			return Identity{Via: ViaCIDR, Node: addr.String()}, ""
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
	return Identity{Login: p.Login, DisplayName: p.DisplayName, Node: p.Node, Via: ViaTailnet}, ""
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

// Routes registers access endpoints under the taproot API group.
func (g *Guard) Routes(r *gin.RouterGroup) {
	r.GET("/me", g.handleMe)
}

type meResponse struct {
	Login       string `json:"login"`
	DisplayName string `json:"displayName"`
	Node        string `json:"node"`
	Via         string `json:"via"`
}

func (g *Guard) handleMe(c *gin.Context) {
	if !g.enabled {
		c.JSON(http.StatusOK, meResponse{Via: "disabled"})
		return
	}
	id, ok := IdentityFrom(c)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"code": "forbidden", "message": "no admitted identity"}})
		return
	}
	c.JSON(http.StatusOK, meResponse{Login: id.Login, DisplayName: id.DisplayName, Node: id.Node, Via: string(id.Via)})
}
