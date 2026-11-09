package access

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// surface is the part of the server a request targets. See the package
// documentation for who may reach each one.
type surface int

const (
	surfaceProxy surface = iota
	surfaceAdmin
)

func (s surface) String() string {
	if s == surfaceAdmin {
		return "admin"
	}
	return "proxy"
}

// adminPrefixes are route trees that read or change credentials, settings,
// backups, or plugins.
var adminPrefixes = []string{
	"/v0/management",
	"/v8/management",
	"/taproot",
	// Plugin pages and assets, served from upstream's NoRoute. The dashboard
	// frames them same-origin.
	"/v0/resource/plugins",
	// The dashboard's chunks. They hold no data, but they follow the page's
	// access (isDashboardShell), so they answer exactly the callers "/" does.
	"/assets",
}

// adminPaths are single admin routes. The OAuth callbacks write the code for
// a pending login. /v1/ws registers a websocket client as an AI Studio
// credential.
var adminPaths = map[string]bool{
	"/":                     true,
	"/anthropic/callback":   true,
	"/codex/callback":       true,
	"/antigravity/callback": true,
	"/callback":             true,
	"/devin/callback":       true,
	"/v1/ws":                true,
}

// navigablePaths accept a cross-site top-level navigation: following a link
// to the dashboard, and a provider redirecting the browser to an OAuth
// callback. Neither response is readable by the other site, the dashboard
// cannot be framed, and a callback only completes a login whose random state
// the caller already holds.
var navigablePaths = map[string]bool{
	"/":                     true,
	"/anthropic/callback":   true,
	"/codex/callback":       true,
	"/antigravity/callback": true,
	"/callback":             true,
	"/devin/callback":       true,
}

// RequestHeader must be "1" on GETs that change state, from every caller. A
// cross-site page cannot add a custom header without a CORS preflight, and the
// guard rejects the preflight's Origin. Browsers send no Sec-Fetch-* headers to
// plain-http tailnet names, so without this a cross-site no-cors GET would
// reach these handlers. Loopback needs it too, so the check does not depend on
// the browser sending Sec-Fetch-*; the dashboard and the TUI client send it.
const RequestHeader = "X-Taproot-Request"

// sideEffectGETs are admin GET routes that change state or reach out: draining the usage
// queue, starting or adopting an OAuth login, and outbound fetches. Read-only GETs, including
// plugin pages and plugin management routes, do not need RequestHeader.
// The OAuth callbacks stay out: providers reach them by navigation, and they
// only act on a pending login whose random state the caller must know.
var sideEffectGETs = map[string]bool{
	"/v0/management/usage-queue":               true,
	"/v8/management/observability/usage/queue": true,
	"/v8/management/oauth/auth-url":            true,
	"/v0/management/get-auth-status":           true,
	"/v8/management/oauth/status":              true,
	// Outbound fetches: plugin registries, the latest release, and plugin
	// quota probes that use stored credentials.
	"/v0/management/plugin-store":          true,
	"/v8/management/plugins/store":         true,
	"/v0/management/latest-version":        true,
	"/v8/management/server/latest-version": true,
}

// sideEffectGET also matches /v0/management/<provider>-auth-url, which covers
// the built-in providers and the ones plugins add.
func sideEffectGET(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.Path
	return sideEffectGETs[path] ||
		(strings.HasPrefix(path, "/v0/management/") && strings.HasSuffix(path, "-auth-url")) ||
		(strings.Contains(path, "/management/plugins/") && strings.HasSuffix(path, "/quota"))
}

// isDashboardShell is the dashboard page and its chunks under /assets/. Every
// identified caller may load them: they are static and hold no data, and they
// are how a device without Admin learns why the API refuses it.
func isDashboardShell(r *http.Request) bool {
	return (r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/assets/")) &&
		(r.Method == http.MethodGet || r.Method == http.MethodHead)
}

// classify matches on URL.Path, the same value gin routes on.
func classify(path string) surface {
	if adminPaths[path] {
		return surfaceAdmin
	}
	for _, prefix := range adminPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return surfaceAdmin
		}
	}
	return surfaceProxy
}

// browserCheck rejects requests a browser makes on behalf of another site, and
// requests addressed to a name that is not this server's (DNS rebinding).
// Command-line clients send no Origin or Sec-Fetch-Site, so only the Host
// check applies to them.
func (g *Guard) browserCheck(r *http.Request, self func() *selfNode) string {
	if !g.ownHost(r.Host, self) {
		return "host " + quoteHeader(r.Host) + " is not one of this server's names"
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, r) {
		return "origin " + quoteHeader(origin) + " is not this server"
	}
	switch strings.ToLower(r.Header.Get("Sec-Fetch-Site")) {
	case "cross-site", "same-site":
		if !isTopLevelNavigation(r) || !navigablePaths[r.URL.Path] {
			return "browser request from another site"
		}
	}
	return ""
}

func isTopLevelNavigation(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		strings.EqualFold(r.Header.Get("Sec-Fetch-Mode"), "navigate") &&
		strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "document")
}

// ownHost accepts localhost, loopback and the server's tailnet addresses, its
// MagicDNS name with or without the tailnet suffix, and extra-hosts, with any
// port.
func (g *Guard) ownHost(hostport string, self func() *selfNode) bool {
	host := hostOnly(hostport)
	if host == "" {
		return false
	}
	if host == "localhost" || g.extraHosts[host] {
		return true
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		if addr.IsLoopback() {
			return true
		}
		s := self()
		return s != nil && containsAddr(s.IPs, addr)
	}
	s := self()
	if s == nil || s.DNSName == "" {
		return false
	}
	short, _, _ := strings.Cut(s.DNSName, ".")
	return host == s.DNSName || host == short
}

// hostOnly lowercases a Host header value and strips its port, IPv6
// brackets, and a trailing dot.
func hostOnly(hostport string) string {
	host := strings.ToLower(strings.TrimSpace(hostport))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	return strings.TrimSuffix(host, ".")
}

// sameOrigin reports whether an http or https Origin header names the host and
// port the request was sent to. The scheme is not compared, so TLS terminated
// in front of the server still matches. Host has already passed ownHost.
func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.User != nil {
		return false
	}
	return authority(u.Host, u.Scheme) == authority(r.Host, u.Scheme)
}

// authority normalizes host[:port] to host:port with the scheme's default port.
func authority(hostport, scheme string) string {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host = strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(strings.TrimSuffix(strings.ToLower(host), "."), port)
}

func containsAddr(addrs []netip.Addr, addr netip.Addr) bool {
	for _, a := range addrs {
		if a == addr {
			return true
		}
	}
	return false
}

// quoteHeader bounds and escapes a client-supplied value before it reaches a log line.
func quoteHeader(v string) string {
	if len(v) > 100 {
		v = v[:100]
	}
	return strconv.Quote(v)
}

// RESPFilter admits Redis-protocol connections on the proxy port from loopback
// only. Upstream serves its usage queue there outside gin, where the guard
// never runs, and taproot does not use the queue.
func RESPFilter(remote net.Addr) bool {
	return remote != nil && isLoopback(remote.String())
}

// isLoopback reports whether an ip:port remote address is a loopback address.
func isLoopback(remoteAddr string) bool {
	addrPort, err := netip.ParseAddrPort(remoteAddr)
	return err == nil && addrPort.Addr().Unmap().IsLoopback()
}
