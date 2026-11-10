package ingest

import (
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// refreshInterval is the shortest gap between two manual refreshes of one
// account, so the dashboard cannot hammer a provider's usage endpoint.
const refreshInterval = 30 * time.Second

// sourceRefresh marks observations read on request. They are always stored,
// so the account's observedAt moves even when nothing else changed.
const sourceRefresh = "refresh"

const (
	codeNotFound            = "not_found"
	codeRateLimited         = "rate_limited"
	codeUnsupportedProvider = "unsupported_provider"
	codeUpstreamFailed      = "upstream_failed"
)

// Development overrides for the usage endpoints, so a local dashboard can
// exercise quota refresh against a fake server. Only loopback URLs are
// accepted, which keeps a stray variable from sending tokens off the machine.
const (
	devClaudeUsageURLEnv = "TAPROOT_DEV_CLAUDE_USAGE_URL"
	devCodexUsageURLEnv  = "TAPROOT_DEV_CODEX_USAGE_URL"
)

func devUsageURL(env, fallback string) string {
	raw := strings.TrimSpace(os.Getenv(env))
	if raw == "" {
		return fallback
	}
	if !IsLoopbackURL(raw) {
		log.WithField("env", env).Warn("taproot ingest: ignoring non-loopback development usage URL")
		return fallback
	}
	log.WithField("env", env).WithField("url", raw).Warn("taproot ingest: development usage URL in use")
	return raw
}

// IsLoopbackURL reports whether raw is an http(s) URL on 127.0.0.1, localhost, or ::1.
func IsLoopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

func (i *Ingest) findAuth(authIndex string) *coreauth.Auth {
	for _, a := range i.authList() {
		if a != nil && a.EnsureIndex() == authIndex {
			return a
		}
	}
	return nil
}

// takeRefresh records a refresh attempt for the account, or returns how long
// the caller must wait before the next one.
func (i *Ingest) takeRefresh(authIndex string, now time.Time) (time.Duration, bool) {
	i.refreshMu.Lock()
	defer i.refreshMu.Unlock()
	if last, ok := i.lastRefresh[authIndex]; ok {
		if wait := refreshInterval - now.Sub(last); wait > 0 {
			return wait, false
		}
	}
	i.lastRefresh[authIndex] = now
	return 0, true
}

type rateLimitedBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		// RetryAfter is in whole seconds, rounded up.
		RetryAfter int `json:"retryAfter"`
	} `json:"error"`
}

// handleQuotaRefresh polls one account's usage endpoint now, stores what it
// reports, and returns the account's current windows. Disabled accounts can
// be refreshed on request; only the background poller skips them.
func (i *Ingest) handleQuotaRefresh(c *gin.Context) {
	var body struct {
		AuthIndex string `json:"authIndex"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.AuthIndex) == "" {
		badRequest(c, "body: want {\"authIndex\": \"...\"}")
		return
	}
	authIndex := strings.TrimSpace(body.AuthIndex)
	a := i.findAuth(authIndex)
	if a == nil {
		writeError(c, http.StatusNotFound, codeNotFound, "no credential has that auth index")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(a.Provider))
	if provider != "claude" && provider != "codex" {
		writeError(c, http.StatusBadRequest, codeUnsupportedProvider, "quota refresh supports Claude and Codex credentials only")
		return
	}
	now := i.now()
	if wait, ok := i.takeRefresh(authIndex, now); !ok {
		seconds := int(math.Ceil(wait.Seconds()))
		var out rateLimitedBody
		out.Error.Code = codeRateLimited
		out.Error.Message = "this account was refreshed moments ago; try again in " + strconv.Itoa(seconds) + "s"
		out.Error.RetryAfter = seconds
		c.Header("Retry-After", strconv.Itoa(seconds))
		c.AbortWithStatusJSON(http.StatusTooManyRequests, out)
		return
	}
	ctx := c.Request.Context()
	obs, err := i.pollAccount(ctx, a, provider)
	if err != nil {
		log.WithError(err).WithField("auth_index", authIndex).Info("taproot ingest: manual quota refresh failed")
		writeError(c, http.StatusBadGateway, codeUpstreamFailed, "usage read failed: "+err.Error())
		return
	}
	for k := range obs {
		obs[k].AuthIndex = authIndex
		obs[k].Source = sourceRefresh
	}
	i.enqueueObservations(obs)
	if err := i.flush(ctx); err != nil {
		internalError(c, err)
		return
	}
	byAccount, err := i.currentWindows(ctx, authIndex)
	if err != nil {
		internalError(c, err)
		return
	}
	account := byAccount[authIndex]
	if account == nil {
		account = &quotaAccountJSON{AuthIndex: authIndex, Provider: provider, Windows: []quotaWindowJSON{}}
	}
	c.JSON(http.StatusOK, account)
}
