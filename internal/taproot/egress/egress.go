// Package egress picks the route for taproot's own requests to providers,
// such as the quota poller and the reset-grant read. They are not proxied
// traffic, but they carry a credential's token, so they leave the way that
// credential's proxied requests do.
package egress

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/265866/taproot/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

// Proxy resolves a credential's route in upstream's order: the credential's
// proxy-url, then the global proxy-url, then the default transport. The zero
// value has no global proxy-url.
type Proxy struct {
	mu     sync.RWMutex
	global func() string
}

// SetGlobal makes fn the source of the global proxy-url. It is read on every
// request, so a config reload applies to the next one.
func (p *Proxy) SetGlobal(fn func() string) {
	p.mu.Lock()
	p.global = fn
	p.mu.Unlock()
}

func (p *Proxy) globalURL() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	fn := p.global
	p.mu.RUnlock()
	if fn == nil {
		return ""
	}
	return strings.TrimSpace(fn())
}

// Client returns the HTTP client for a request on behalf of a. A configured
// proxy-url that does not parse is an error rather than a direct request, so
// the token never leaves from an address the operator routed away from.
func (p *Proxy) Client(a *coreauth.Auth) (*http.Client, error) {
	setting, raw := "global", p.globalURL()
	if own := strings.TrimSpace(a.ProxyURL); own != "" {
		setting, raw = "credential", own
	}
	if raw == "" {
		return &http.Client{}, nil
	}
	t, _, err := proxyutil.BuildHTTPTransport(raw)
	if err != nil {
		log.WithFields(log.Fields{"setting": setting, "auth_index": a.Index}).WithError(err).Warn("taproot: invalid proxy-url; not sending the provider request")
		return nil, fmt.Errorf("invalid %s proxy-url: %w", setting, err)
	}
	return &http.Client{Transport: t}, nil
}
