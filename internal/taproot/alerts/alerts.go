package alerts

import (
	"context"
	"crypto/sha256"
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

	"github.com/265866/taproot/internal/taproot/egress"
	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/265866/taproot/internal/taproot/store"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	evalInterval = 30 * time.Second
	cooldown     = 30 * time.Minute
	settle       = 2 * time.Minute
	// grantInterval is how often one Claude account's reset grants are read.
	grantInterval = 3 * time.Hour
	// grantMaxAge drops grants that failed reads left unrefreshed, so a grant
	// redeemed since the last read does not keep warning.
	grantMaxAge = 2 * grantInterval
	// OriginEnv points deliveries at a local fake Discord for end-to-end tests.
	OriginEnv = "TAPROOT_ALERTS_DISCORD_ORIGIN"
)

func init() {
	store.Register(store.Schema{Module: "alerts", SQL: `CREATE TABLE IF NOT EXISTS alert_state (
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

// grantRead is the last read attempt of one account's grants, and the
// grants from the last successful read.
type grantRead struct {
	at     time.Time
	readAt time.Time
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

	// deliveries holds the sender's outcomes for detector messages until the
	// evaluation goroutine, which owns the detector, applies them. rearm asks
	// it to treat the open alerts as unsent after the webhook changed.
	deliveryMu sync.Mutex
	deliveries []delivery
	rearm      bool

	healthMu sync.Mutex
	health   DeliveryHealth

	// startedAt and restoredLogins belong to the evaluation goroutine;
	// loadState sets them before that goroutine starts.
	startedAt time.Time
	// restoredLogins holds the login alerts alert_state had open, by auth
	// index. Upstream keeps RequiresSignIn in memory only, so after a restart
	// a broken login looks healthy until a refresh fails again. These stay
	// broken until the credential shows it works.
	restoredLogins map[string]restoredLogin
}

type delivery struct {
	msg Message
	err error
}

type restoredLogin struct {
	seen  bool
	token [sha256.Size]byte
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
		db:             db,
		feed:           feed,
		detector:       NewDetector(cooldown, settle),
		grants:         grantReader{url: grantStatusURL},
		now:            time.Now,
		wake:           make(chan struct{}, 1),
		cfg:            cfg,
		webhookInvalid: invalid,
		quota:          map[quotaKey]ingest.QuotaObservation{},
		grantReads:     map[string]grantRead{},
	}
	s.sender = NewSender(s.webhook, discordOrigin())
	s.sender.onResult = s.delivered
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

// SetConfig replaces the webhook and switch after validating the webhook. An
// unchanged webhook keeps its state, so the switch can change while
// taproot.yaml holds an invalid webhook.
func (s *Service) SetConfig(cfg Config) error {
	cfg.DiscordWebhook = strings.TrimSpace(cfg.DiscordWebhook)
	s.cfgMu.Lock()
	unchanged := cfg.DiscordWebhook == s.cfg.DiscordWebhook
	if cfg.DiscordWebhook != "" && !unchanged {
		if err := ValidateWebhook(cfg.DiscordWebhook); err != nil {
			s.cfgMu.Unlock()
			return err
		}
	}
	newWebhook := cfg.DiscordWebhook != "" && !unchanged
	restarted := !unchanged || cfg.Active() != s.cfg.Active()
	s.cfg = cfg
	if !unchanged {
		s.webhookInvalid = false
	}
	s.cfgMu.Unlock()
	if restarted {
		s.healthMu.Lock()
		s.health = DeliveryHealth{}
		s.healthMu.Unlock()
	}
	if newWebhook {
		s.deliveryMu.Lock()
		s.rearm = true
		s.deliveryMu.Unlock()
	}
	// AnnounceWebhook wakes the evaluation for a new webhook, so the
	// connected message is posted before any alert.
	if !newWebhook {
		s.signal()
	}
	return nil
}

// SnapshotConfig returns a function that puts back the current webhook,
// switch, and invalid mark exactly, without validating them, to undo a
// SetConfig.
func (s *Service) SnapshotConfig() (restore func()) {
	s.cfgMu.RLock()
	cfg, invalid := s.cfg, s.webhookInvalid
	s.cfgMu.RUnlock()
	return func() {
		s.cfgMu.Lock()
		s.cfg, s.webhookInvalid = cfg, invalid
		s.cfgMu.Unlock()
		s.signal()
	}
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

// AnnounceWebhook queues a "Webhook connected" message after settings save
// a new webhook, then wakes the evaluation that SetConfig held back. While
// alerts are off it queues nothing.
func (s *Service) AnnounceWebhook() {
	if s.Active() {
		s.sender.Enqueue(Message{
			Key: "connected", Severity: SeverityInfo, At: s.now(),
			Title:       "Webhook connected",
			Description: "Taproot will post here when an account hits a limit, a provider runs out of accounts, a login stops working, or a reset grant is about to expire.",
		})
	}
	s.signal()
}

// webhook is the URL deliveries use, or "" when alerts are off.
func (s *Service) webhook() string {
	if !s.Active() {
		return ""
	}
	return s.Config().DiscordWebhook
}

// UseProxy routes the reset-grant read through p. It must run before Start.
func (s *Service) UseProxy(p *egress.Proxy) {
	s.grants.proxy = p
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

// DeliveryHealth is how the latest deliveries to Discord went.
type DeliveryHealth struct {
	// Active is false while alerts are off, when nothing is delivered.
	Active bool
	// Failures counts the failed deliveries since the last success.
	Failures int
	// FailingSince is the first of those failures.
	FailingSince time.Time
	// LastError is the last failure, without the webhook URL.
	LastError   string
	LastSuccess time.Time
}

// DeliveryHealth reports the queue's recent delivery outcomes. A successful
// test send counts as a delivery and a failed one does not; messages the
// queue skipped do not count. A new webhook, or turning alerts off or on,
// starts the count again.
func (s *Service) DeliveryHealth() DeliveryHealth {
	s.healthMu.Lock()
	h := s.health
	s.healthMu.Unlock()
	h.Active = s.Active()
	return h
}

func (s *Service) recordDelivery(err error) {
	if errors.Is(err, errNotSent) {
		return
	}
	// This runs on the sender goroutine, so it reads the wall clock: s.now
	// belongs to the evaluation.
	now := time.Now()
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if err == nil {
		s.health = DeliveryHealth{LastSuccess: now}
		return
	}
	if s.health.Failures == 0 {
		s.health.FailingSince = now
	}
	s.health.Failures++
	s.health.LastError = err.Error()
}

// delivered is the sender's result hook; it runs on the sender goroutine.
func (s *Service) delivered(m Message, err error) {
	s.recordDelivery(err)
	if !m.tracked {
		return
	}
	s.deliveryMu.Lock()
	s.deliveries = append(s.deliveries, delivery{m, err})
	s.deliveryMu.Unlock()
	s.signal()
}

// applyDeliveries hands the pending delivery outcomes, and a webhook change,
// to the detector.
func (s *Service) applyDeliveries(now time.Time) (changed bool) {
	s.deliveryMu.Lock()
	results, rearm := s.deliveries, s.rearm
	s.deliveries, s.rearm = nil, false
	s.deliveryMu.Unlock()
	for _, r := range results {
		changed = s.detector.Delivered(now, r.msg, r.err) || changed
	}
	if rearm {
		changed = s.detector.Rearm() || changed
	}
	return changed
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
	now := s.now()
	changed := s.applyDeliveries(now)
	if auths := s.authList(); len(auths) > 0 {
		accounts := accountsFrom(auths, now, s.heldLogins(auths))
		s.pollGrants(ctx, auths, now)
		forgot := s.detector.Forget(accounts)
		msgs, stepped := s.detector.Step(now, Conditions(s.snapshot(now, accounts)))
		changed = changed || forgot || stepped
		for _, m := range msgs {
			s.sender.Enqueue(m)
		}
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
		if now.Sub(r.readAt) < grantMaxAge {
			snap.Grants[idx] = r.grants
		}
	}
	s.grantMu.Unlock()
	return snap
}

// accountsFrom keeps the accounts in scope, with their login state. held
// marks restored login alerts that stay broken (see heldLogins).
func accountsFrom(auths []*coreauth.Auth, now time.Time, held map[string]bool) []Account {
	out := make([]Account, 0, len(auths))
	for _, a := range auths {
		if !inScope(a) {
			continue
		}
		idx := a.EnsureIndex()
		if idx == "" {
			continue
		}
		rejected := coreauth.HasUnauthorizedAuthFailure(a)
		broken := a.RequiresSignIn || rejected || held[idx]
		acct := Account{AuthIndex: idx, Provider: strings.ToLower(strings.TrimSpace(a.Provider)), Label: safeLabel(authLabel(a)), LoginBroken: broken}
		if broken {
			acct.LoginReason = loginReason(a, now)
			// Upstream keeps serving a sign-in-required account while its access
			// token is valid (canServeWithAccessToken in conductor_lifecycle.go).
			if !rejected && a.HasValidAccessToken(now) {
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

// inScope reports whether alerts watch a. Upstream sets Disabled and
// StatusDisabled only from the auth file's "disabled" flag, which the user
// sets, so a disabled account is out of scope whatever its last error.
func inScope(a *coreauth.Auth) bool {
	return a != nil && !a.Disabled && a.Status != coreauth.StatusDisabled
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
		if !inScope(a) || !strings.EqualFold(strings.TrimSpace(a.Provider), "claude") || a.RequiresSignIn {
			continue
		}
		idx := a.EnsureIndex()
		present[idx] = true
		if r, ok := s.grantReads[idx]; !ok || now.Sub(r.at) >= grantInterval {
			due = append(due, a)
			// Record the attempt now so a failure waits a full interval too.
			r.at = now
			s.grantReads[idx] = r
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
				s.grantReads[idx] = grantRead{at: r.at, readAt: r.at, grants: grants}
			}
			s.grantMu.Unlock()
		}
		s.signal()
	}()
}

// loadState restores the detector's memory. Login alerts that were open
// stay open until the credential shows it works (see restoredLogins); one
// whose recovery was already pending recovers as usual.
func (s *Service) loadState(ctx context.Context) error {
	s.startedAt = s.now()
	s.restoredLogins = map[string]restoredLogin{}
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
		// The queue lived in memory, so an alert that was waiting in it was
		// never delivered, and a recovery that was is sent again.
		if t.Queued {
			t.undeliver()
		}
		t.Recovering = false
		s.detector.tracks[key] = &t
		if idx, ok := strings.CutPrefix(key, "login:"); ok && t.Active && t.Notified {
			s.restoredLogins[idx] = restoredLogin{}
		}
	}
	return rows.Err()
}

// heldLogins returns the restored login alerts that still lack evidence the
// credential works: a successful refresh after startup, or an access token
// other than the one first seen after startup. Accounts no longer in scope
// are let go; Forget drops their tracks.
func (s *Service) heldLogins(auths []*coreauth.Auth) map[string]bool {
	if len(s.restoredLogins) == 0 {
		return nil
	}
	present := map[string]bool{}
	for _, a := range auths {
		if !inScope(a) {
			continue
		}
		idx := a.EnsureIndex()
		r, ok := s.restoredLogins[idx]
		if !ok {
			continue
		}
		present[idx] = true
		token, _ := a.Metadata["access_token"].(string)
		sum := sha256.Sum256([]byte(token))
		switch {
		case a.LastRefreshedAt.After(s.startedAt), r.seen && sum != r.token:
			delete(s.restoredLogins, idx)
		case !r.seen:
			s.restoredLogins[idx] = restoredLogin{seen: true, token: sum}
		}
	}
	held := map[string]bool{}
	for idx := range s.restoredLogins {
		if present[idx] {
			held[idx] = true
		} else {
			delete(s.restoredLogins, idx)
		}
	}
	return held
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
		s.recordDelivery(nil)
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
