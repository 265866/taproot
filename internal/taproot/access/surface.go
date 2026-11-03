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
