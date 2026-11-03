// Package ingest records every usage record, prices it, and keeps per-account
// quota history from upstream rate-limit headers and usage endpoints.
package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

// Config is the ingest section of taproot.yaml.
type Config struct {
	// PollInterval is how often accounts without recent quota observations are
	// polled, as a Go duration ("15m"). Empty means 15m; "0" disables polling.
	PollInterval string `yaml:"poll-interval"`
}

const (
	defaultPollInterval = 15 * time.Minute
	flushInterval       = time.Second
	flushBatch          = 256
	maxPending          = 100_000
	maintenanceInterval = time.Minute
)

func (c Config) pollInterval() (time.Duration, error) {
	raw := strings.TrimSpace(c.PollInterval)
	switch raw {
	case "":
		return defaultPollInterval, nil
	case "0":
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("ingest poll-interval %q: want a non-negative duration such as 15m", raw)
	}
	return d, nil
}

type authLister interface {
	List() []*coreauth.Auth
}

// Ingest owns usage, quota, and pricing data.
type Ingest struct {
	db           *store.DB
	pollInterval time.Duration
	now          func() time.Time

	mu         sync.Mutex
	pending    []usage.Record
	pendingObs []observation
	dropped    int64
	wake       chan struct{}

	// writeMu serializes flushes so the quota tracker has a single user.
	writeMu sync.Mutex
	tracker *quotaTracker

	quotaHook quotaHook

	authMu sync.RWMutex
	auths  authLister

	claudeUsageURL string
	codexUsageURL  string
	lastPoll       map[string]time.Time

	refreshMu   sync.Mutex
	lastRefresh map[string]time.Time
}

// New prepares the module. It must register its usage sink before the service starts.
func New(cfg Config, db *store.DB) (*Ingest, error) {
	if db == nil {
		return nil, fmt.Errorf("ingest: nil store")
	}
	interval, err := cfg.pollInterval()
	if err != nil {
		return nil, err
	}
	i := &Ingest{
		db:             db,
		pollInterval:   interval,
		now:            time.Now,
		wake:           make(chan struct{}, 1),
		tracker:        newQuotaTracker(),
		claudeUsageURL: devUsageURL(devClaudeUsageURLEnv, claudeUsageURL),
		codexUsageURL:  devUsageURL(devCodexUsageURLEnv, codexUsageURL),
		lastPoll:       map[string]time.Time{},
		lastRefresh:    map[string]time.Time{},
	}
	usage.RegisterNamedPlugin("taproot", i)
	return i, nil
}

// HandleUsage implements usage.Plugin. It only queues the record; SQLite
// writes happen on the flush goroutine so the proxy never waits on disk.
func (i *Ingest) HandleUsage(_ context.Context, record usage.Record) {
	i.mu.Lock()
	if len(i.pending) >= maxPending {
		i.dropped++
		dropped := i.dropped
		i.mu.Unlock()
		if dropped == 1 || dropped%1000 == 0 {
			log.WithField("dropped", dropped).Warn("taproot ingest: usage queue full, dropping records")
		}
		return
	}
	i.pending = append(i.pending, record)
	n := len(i.pending)
	i.mu.Unlock()
	if n >= flushBatch {
		i.signal()
	}
}

func (i *Ingest) enqueueObservations(obs []observation) {
	if len(obs) == 0 {
		return
	}
	i.mu.Lock()
	i.pendingObs = append(i.pendingObs, obs...)
	i.mu.Unlock()
	i.signal()
}

func (i *Ingest) signal() {
	select {
	case i.wake <- struct{}{}:
	default:
	}
}

// AttachAuthManager receives the runtime credential manager once the server is built.
func (i *Ingest) AttachAuthManager(m *coreauth.Manager) {
	if m == nil {
		return
	}
	i.authMu.Lock()
	i.auths = m
	i.authMu.Unlock()
}

func (i *Ingest) authList() []*coreauth.Auth {
	i.authMu.RLock()
	src := i.auths
	i.authMu.RUnlock()
	if src == nil {
		return nil
	}
	return src.List()
}

// Start runs background work until ctx ends.
func (i *Ingest) Start(ctx context.Context) {
	go i.runWriter(ctx)
	go i.runMaintenance(ctx)
}

func (i *Ingest) runWriter(ctx context.Context) {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := i.flush(context.WithoutCancel(ctx)); err != nil {
				log.WithError(err).Error("taproot ingest: final flush")
			}
			return
		case <-ticker.C:
		case <-i.wake:
		}
		if err := i.flush(ctx); err != nil {
			log.WithError(err).Error("taproot ingest: flush usage")
		}
	}
}

func (i *Ingest) runMaintenance(ctx context.Context) {
	ticker := time.NewTicker(maintenanceInterval)
	defer ticker.Stop()
	for {
		if err := i.refreshAccounts(ctx); err != nil {
			log.WithError(err).Warn("taproot ingest: refresh accounts")
		}
		if i.pollInterval > 0 {
			i.pollIdle(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// flush writes everything queued so far in one transaction. On failure the
// batch goes back to the front of the queue for the next attempt.
func (i *Ingest) flush(ctx context.Context) error {
	i.writeMu.Lock()
	defer i.writeMu.Unlock()

	i.mu.Lock()
	records, obs := i.pending, i.pendingObs
	i.pending, i.pendingObs = nil, nil
	i.mu.Unlock()
	if len(records) == 0 && len(obs) == 0 {
		return nil
	}

	now := i.now()
	events := make([]event, 0, len(records))
	allObs := make([]observation, 0, len(obs))
	for _, r := range records {
		e := eventFromRecord(r, now)
		events = append(events, e)
		if e.AuthIndex == "" || len(r.ResponseHeaders) == 0 {
			continue
		}
		for _, o := range parseQuotaHeaders(e.Provider, r.ResponseHeaders, headersObservedAt(r, now).UTC()) {
			o.AuthIndex = e.AuthIndex
			allObs = append(allObs, o)
		}
	}
	allObs = append(allObs, obs...)

	err := withImmediateTx(ctx, i.db.DB, func(c *sql.Conn) error {
		if _, err := insertEvents(ctx, c, events); err != nil {
			return err
		}
		_, err := i.tracker.write(ctx, c, allObs)
		return err
	})
	if err != nil {
		i.tracker.forget()
		i.mu.Lock()
		if len(i.pending)+len(records) <= maxPending {
			i.pending = append(records, i.pending...)
		} else {
			i.dropped += int64(len(records))
			log.WithField("dropped", len(records)).Warn("taproot ingest: usage queue full after failed flush, dropping batch")
		}
		i.pendingObs = append(obs, i.pendingObs...)
		i.mu.Unlock()
		return err
	}
	i.quotaHook.publish(allObs)
	return nil
}

// headersObservedAt estimates when the response headers arrived. A long
// stream's headers are as old as its first token; stamping them at stream end
// would let them overwrite newer readings from requests that overlapped it.
func headersObservedAt(r usage.Record, now time.Time) time.Time {
	switch {
	case r.RequestedAt.IsZero():
		return now
	case r.TTFT > 0:
		return r.RequestedAt.Add(r.TTFT)
	case r.Stream:
		return r.RequestedAt
	}
	return r.RequestedAt.Add(r.Latency)
}

// Routes registers ingest endpoints under the taproot API group.
func (i *Ingest) Routes(r *gin.RouterGroup) {
	r.GET("/accounts", i.handleAccounts)
	r.GET("/quota/current", i.handleQuotaCurrent)
	r.GET("/quota/series", i.handleQuotaSeries)
	r.POST("/quota/refresh", i.handleQuotaRefresh)
	r.GET("/usage/summary", i.handleUsageSummary)
	r.GET("/usage/events", i.handleUsageEvents)
	r.GET("/usage/sessions", i.handleUsageSessions)
	r.GET("/prices", i.handleGetPrices)
	r.PUT("/prices", i.handlePutPrices)
}
