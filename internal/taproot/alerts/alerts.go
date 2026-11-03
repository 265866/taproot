package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/ingest"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	evalInterval = 30 * time.Second
	cooldown     = 30 * time.Minute
	settle       = 2 * time.Minute
	// grantInterval is how often one Claude account's reset grants are read.
	grantInterval = 3 * time.Hour
	// OriginEnv points deliveries at a local fake Discord for end-to-end tests.
	OriginEnv = "TAPROOT_ALERTS_DISCORD_ORIGIN"
)

func init() {
	store.Register(store.Migration{Module: "alerts", Version: 1, SQL: `CREATE TABLE alert_state (
		key TEXT PRIMARY KEY,
		state TEXT NOT NULL
	) WITHOUT ROWID`})
}

// QuotaFeed is ingest's quota hook.
type QuotaFeed interface {
	OnQuota(func([]ingest.QuotaObservation))
	LatestQuota(ctx context.Context) ([]ingest.QuotaObservation, error)
}

type authLister interface {
	List() []*coreauth.Auth
}

type quotaKey struct{ authIndex, window string }

type grantRead struct {
	at     time.Time
	grants []Grant
}

// Service watches quota and credentials and posts alerts.
type Service struct {
	db       *store.DB
	feed     QuotaFeed
	sender   *Sender
	detector *Detector
	grants   grantReader
	now      func() time.Time
	wake     chan struct{}

	cfgMu sync.RWMutex
	cfg   Config
	// webhookInvalid marks a webhook from taproot.yaml that fails
	// validation. Alerts stay off until settings replace or clear it.
	webhookInvalid bool

	authMu sync.RWMutex
	auths  authLister

	quotaMu sync.Mutex
	quota   map[quotaKey]ingest.QuotaObservation

	grantMu      sync.Mutex
	grantReads   map[string]grantRead
	grantPolling bool
}

// New prepares the service. Start runs it. An invalid webhook keeps alerts
// off instead of failing, so a typo in taproot.yaml cannot stop the proxy.
func New(cfg Config, db *store.DB, feed QuotaFeed) (*Service, error) {
	if db == nil || feed == nil {
		return nil, errors.New("alerts need the store and the ingest quota feed")
	}
	cfg.DiscordWebhook = strings.TrimSpace(cfg.DiscordWebhook)
	invalid := cfg.DiscordWebhook != "" && ValidateWebhook(cfg.DiscordWebhook) != nil
	if invalid {
		log.Error("taproot alerts: alerts.discord-webhook in taproot.yaml is not a Discord webhook URL; alerts are off until settings replace it")
	}
	s := &Service{
		db:       db,
		feed:     feed,
		detector: NewDetector(cooldown, settle),
		grants:   grantReader{url: grantStatusURL},
		now:      time.Now,
		wake:     make(chan struct{}, 1),
		cfg:      cfg,
		// Set before any goroutine can read it.
		webhookInvalid: invalid,
		quota:          map[quotaKey]ingest.QuotaObservation{},
		grantReads:     map[string]grantRead{},
	}
	s.sender = NewSender(s.webhook, discordOrigin())
	feed.OnQuota(s.observe)
	return s, nil
}

// discordOrigin reads OriginEnv. Only a loopback origin is accepted, because
// the webhook token travels in the path and must not leave the machine.
func discordOrigin() string {
	raw := strings.TrimSpace(os.Getenv(OriginEnv))
	if raw == "" {
		return ""
	}
	if !ingest.IsLoopbackURL(raw) {
		log.WithField("env", OriginEnv).Warn("taproot alerts: ignoring non-loopback Discord origin")
		return ""
	}
	log.WithField("env", OriginEnv).WithField("origin", raw).Warn("taproot alerts: local Discord origin in use; alerts do not reach Discord")
	return raw
}

// Config returns the alerts section as taproot.yaml should hold it.
func (s *Service) Config() Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// SetConfig replaces the webhook and switch after validating the webhook.
func (s *Service) SetConfig(cfg Config) error {
	cfg.DiscordWebhook = strings.TrimSpace(cfg.DiscordWebhook)
	if cfg.DiscordWebhook != "" {
		if err := ValidateWebhook(cfg.DiscordWebhook); err != nil {
			return err
		}
	}
	s.cfgMu.Lock()
	s.cfg, s.webhookInvalid = cfg, false
	s.cfgMu.Unlock()
	s.signal()
	return nil
}

// Active reports whether alerts are sent: a valid webhook is set and the
// switch is on.
func (s *Service) Active() bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.Active() && !s.webhookInvalid
}

// WebhookInvalid reports whether taproot.yaml holds a webhook that is not a
// Discord webhook URL.
func (s *Service) WebhookInvalid() bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.webhookInvalid
}

// webhook is the URL deliveries use, or "" when alerts are off.
func (s *Service) webhook() string {
	if !s.Active() {
		return ""
	}
	return s.Config().DiscordWebhook
}

// AttachAuthManager receives the runtime credential manager once the server is built.
func (s *Service) AttachAuthManager(m *coreauth.Manager) {
	if m == nil {
		return
	}
	s.authMu.Lock()
	s.auths = m
	s.authMu.Unlock()
	s.signal()
}

func (s *Service) authList() []*coreauth.Auth {
	s.authMu.RLock()
	src := s.auths
	s.authMu.RUnlock()
	if src == nil {
		return nil
	}
	return src.List()
}

// observe is ingest's hook; it runs on ingest's flush goroutine.
func (s *Service) observe(obs []ingest.QuotaObservation) {
	s.quotaMu.Lock()
	for _, o := range obs {
		k := quotaKey{o.AuthIndex, o.Window}
		if prev, ok := s.quota[k]; !ok || !o.ObservedAt.Before(prev.ObservedAt) {
			s.quota[k] = o
		}
	}
	s.quotaMu.Unlock()
	s.signal()
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Start loads saved alert state and the latest stored quota, then runs until ctx ends.
func (s *Service) Start(ctx context.Context) {
	if err := s.loadState(ctx); err != nil {
		log.WithError(err).Warn("taproot alerts: load alert state")
	}
	latest, err := s.feed.LatestQuota(ctx)
	if err != nil {
		log.WithError(err).Warn("taproot alerts: load latest quota")
	}
	s.observe(latest)
	go s.sender.Run(ctx)
	go s.run(ctx)
}

func (s *Service) run(ctx context.Context) {
	ticker := time.NewTicker(evalInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
		s.evaluate(ctx)
	}
}

// evaluate runs one detector step. It does nothing while alerts are off or
// before the auth manager has loaded, so neither state produces false alerts.
func (s *Service) evaluate(ctx context.Context) {
	if !s.Active() {
		return
	}
	auths := s.authList()
	if len(auths) == 0 {
		return
	}
	now := s.now()
	accounts := accountsFrom(auths, now)
	s.pollGrants(ctx, auths, now)
	forgot := s.detector.Forget(accounts)
	msgs, changed := s.detector.Step(now, Conditions(s.snapshot(now, accounts)))
	changed = changed || forgot
	for _, m := range msgs {
		s.sender.Enqueue(m)
	}
	if changed {
		if err := s.saveState(ctx); err != nil {
			log.WithError(err).Warn("taproot alerts: save alert state")
		}
	}
}

func (s *Service) snapshot(now time.Time, accounts []Account) Snapshot {
	snap := Snapshot{Now: now, Accounts: accounts, Grants: map[string][]Grant{}}
	s.quotaMu.Lock()
	for _, o := range s.quota {
		snap.Quota = append(snap.Quota, o)
	}
	s.quotaMu.Unlock()
	s.grantMu.Lock()
	for idx, r := range s.grantReads {
		snap.Grants[idx] = r.grants
	}
	s.grantMu.Unlock()
	return snap
}

// accountsFrom keeps credentials that serve traffic, plus those upstream
// disabled because their refresh grant was rejected.
func accountsFrom(auths []*coreauth.Auth, now time.Time) []Account {
	out := make([]Account, 0, len(auths))
	for _, a := range auths {
		if a == nil {
			continue
		}
		disabledBroken := coreauth.HasDisabledInvalidGrantFailure(a)
		if (a.Disabled || a.Status == coreauth.StatusDisabled) && !disabledBroken {
			continue
		}
		idx := a.EnsureIndex()
		if idx == "" {
			continue
		}
		rejected := coreauth.HasUnauthorizedAuthFailure(a)
		broken := disabledBroken || a.RequiresSignIn || rejected
		acct := Account{AuthIndex: idx, Provider: strings.ToLower(strings.TrimSpace(a.Provider)), Label: safeLabel(authLabel(a)), LoginBroken: broken}
		if broken {
			acct.LoginReason = loginReason(a, now)
			// Upstream keeps serving a sign-in-required account while its access
			// token is valid (canServeWithAccessToken in conductor_lifecycle.go).
			if !disabledBroken && !rejected && a.HasValidAccessToken(now) {
				acct.StillServes = true
				if exp, ok := a.AccessTokenExpirationTime(); ok {
					acct.ServesUntil = exp
				}
			}
		}
		out = append(out, acct)
	}
	return out
}

func authLabel(a *coreauth.Auth) string {
	if l := strings.TrimSpace(a.Label); l != "" {
		return l
	}
	if v, ok := a.Metadata["email"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v := strings.TrimSpace(a.Attributes["email"]); v != "" {
		return v
	}
	if a.FileName != "" {
		return strings.TrimSuffix(filepath.Base(a.FileName), filepath.Ext(a.FileName))
	}
	return a.EnsureIndex()
}

// loginReason is upstream's sanitized failure text, which already has
// tokens, codes, and URLs redacted.
func loginReason(a *coreauth.Auth, now time.Time) string {
	msg := ""
	switch {
	case a.RefreshError != nil && strings.TrimSpace(a.RefreshError.Message) != "":
		msg = a.RefreshError.Message
	case strings.TrimSpace(a.StatusMessage) != "":
		msg = a.StatusMessage
	case a.LastError != nil:
		msg = a.LastError.Message
	}
	if msg = strings.TrimSpace(coreauth.SanitizeRenewalMessage(msg)); msg == "" {
		msg = a.RenewalStateAt(now)
	}
	return safeLabel(msg)
}

// pollGrants starts a background read of reset grants for Claude accounts
// not read within grantInterval. One read runs at a time.
func (s *Service) pollGrants(ctx context.Context, auths []*coreauth.Auth, now time.Time) {
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	if s.grantPolling {
		return
	}
	var due []*coreauth.Auth
	present := map[string]bool{}
	for _, a := range auths {
		if a == nil || a.Disabled || !strings.EqualFold(strings.TrimSpace(a.Provider), "claude") || a.RequiresSignIn {
			continue
		}
		idx := a.EnsureIndex()
		present[idx] = true
		if r, ok := s.grantReads[idx]; !ok || now.Sub(r.at) >= grantInterval {
			due = append(due, a)
			// Record the attempt now so a failure waits a full interval too.
			s.grantReads[idx] = grantRead{at: now, grants: r.grants}
		}
	}
	for idx := range s.grantReads {
		if !present[idx] {
			delete(s.grantReads, idx)
		}
	}
	if len(due) == 0 {
		return
	}
	s.grantPolling = true
	go func() {
		defer func() {
			s.grantMu.Lock()
			s.grantPolling = false
			s.grantMu.Unlock()
		}()
		for _, a := range due {
			if ctx.Err() != nil {
				return
			}
			idx := a.EnsureIndex()
			grants, err := s.grants.read(ctx, a)
			if err != nil {
				log.WithError(err).WithField("auth_index", idx).Debug("taproot alerts: read reset grants")
				continue
			}
			s.grantMu.Lock()
			if r, ok := s.grantReads[idx]; ok {
				s.grantReads[idx] = grantRead{at: r.at, grants: grants}
			}
			s.grantMu.Unlock()
		}
		s.signal()
	}()
}

func (s *Service) loadState(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT key, state FROM alert_state`)
	if err != nil {
		return err
	}
	defer func() {
		if errClose := rows.Close(); errClose != nil {
			log.WithError(errClose).Debug("taproot alerts: close rows")
		}
	}()
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return err
		}
		var t track
		if err := json.Unmarshal([]byte(raw), &t); err != nil {
			continue
		}
		s.detector.tracks[key] = &t
	}
	return rows.Err()
}

// saveState replaces the stored detector memory, so a restart neither
// repeats alerts nor loses pending recoveries.
func (s *Service) saveState(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if errRollback := tx.Rollback(); errRollback != nil && !errors.Is(errRollback, sql.ErrTxDone) {
			log.WithError(errRollback).Debug("taproot alerts: rollback")
		}
	}()
	if _, err := tx.ExecContext(ctx, `DELETE FROM alert_state`); err != nil {
		return err
	}
	for key, t := range s.detector.tracks {
		raw, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO alert_state (key, state) VALUES (?, ?)`, key, string(raw)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Routes registers the alerts endpoints under the taproot API group.
func (s *Service) Routes(r *gin.RouterGroup) {
	r.POST("/alerts/test", s.handleTest)
}

func (s *Service) handleTest(c *gin.Context) {
	hook := s.Config().DiscordWebhook
	if hook == "" {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": gin.H{"code": "no_webhook", "message": "set a Discord webhook first"}})
		return
	}
	if s.WebhookInvalid() {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": gin.H{"code": "invalid_webhook", "message": "the webhook in taproot.yaml is not a Discord webhook URL; replace it first"}})
		return
	}
	msg := Message{
		Key: "test", Severity: SeverityInfo, At: s.now(),
		Title:       "taproot test alert",
		Description: "Alerts from taproot reach this channel.",
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), testSendTimeout)
	defer cancel()
	err := s.sender.SendOnce(ctx, hook, msg)
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"sent": true})
		return
	}
	log.WithError(err).Warn("taproot alerts: test delivery failed")
	var de *DeliveryError
	if errors.As(err, &de) && de.Status == http.StatusTooManyRequests {
		secs := int(math.Ceil(de.RetryAfter.Seconds()))
		c.Header("Retry-After", strconv.Itoa(secs))
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{
			"code": "rate_limited", "retryAfter": secs,
			"message": fmt.Sprintf("Discord is rate limiting this webhook; try again in %d s", secs),
		}})
		return
	}
	c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": gin.H{"code": "delivery_failed", "message": err.Error()}})
}
