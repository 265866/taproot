// Package status answers "is everything OK, and what needs me?": the verdict
// on serving right now, the Needs-you list, and each provider's quota pool,
// projected the way the dashboard's Accounts page projects it.
package status

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/alerts"
	"github.com/265866/taproot/internal/taproot/backup"
	"github.com/265866/taproot/internal/taproot/ingest"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// evalInterval spaces the background evaluations that keep the verdict's
// and items' start times accurate while nobody reads them.
const evalInterval = 30 * time.Second

// Usage is what the status module reads from ingest.
type Usage interface {
	LatestQuota(ctx context.Context) ([]ingest.QuotaObservation, error)
	QuotaHistory(ctx context.Context, from, to time.Time) ([]ingest.QuotaSeries, error)
	ClientRequestCounts(ctx context.Context, from, to time.Time) (ingest.RequestCounts, error)
}

// Requests lists the open access requests.
type Requests interface {
	OpenRequests(ctx context.Context) ([]access.AccessRequest, error)
}

// Backups is what the status module reads from the backup module.
type Backups interface {
	List() ([]backup.Entry, error)
	LastScheduledRun(ctx context.Context) (backup.Run, bool, error)
	NextRun() (time.Time, bool)
}

// Alerts reports Discord delivery health.
type Alerts interface {
	DeliveryHealth() alerts.DeliveryHealth
}

type authLister interface {
	List() []*coreauth.Auth
}

// Deps are the modules the status module reads.
type Deps struct {
	Usage    Usage
	Requests Requests
	Backups  Backups
	Alerts   Alerts
}

// Service serves /status, /attention, and /pools.
type Service struct {
	deps      Deps
	now       func() time.Time
	startedAt time.Time

	authMu sync.RWMutex
	auths  authLister

	tsMu       sync.Mutex
	tailscaled *tailscaledDown

	// trackMu runs one evaluation at a time, each at a later instant than
	// the last, so the start times this process has seen (the verdict's and
	// each listed item's) only move forward. It also guards the sources the
	// last evaluation could not read, so each failure is logged once.
	trackMu    sync.Mutex
	state      string
	stateFrom  time.Time
	seen       map[string]time.Time
	unreadable map[string]string
}

// New prepares the service.
func New(deps Deps) (*Service, error) {
	if deps.Usage == nil || deps.Requests == nil || deps.Backups == nil || deps.Alerts == nil {
		return nil, errors.New("status needs ingest, access requests, backups, and alerts")
	}
	s := &Service{deps: deps, now: time.Now, seen: map[string]time.Time{}, unreadable: map[string]string{}}
	s.startedAt = s.now()
	return s, nil
}

// AttachAuthManager receives the runtime credential manager once the server is built.
func (s *Service) AttachAuthManager(m *coreauth.Manager) {
	if m == nil {
		return
	}
	s.authMu.Lock()
	s.auths = m
	s.authMu.Unlock()
}

// Tailscaled records an outage the access guard's monitor reported, or the
// recovery after one.
func (s *Service) Tailscaled(n access.TailscaledNotice) {
	s.tsMu.Lock()
	defer s.tsMu.Unlock()
	if !n.Down {
		s.tailscaled = nil
		return
	}
	s.tailscaled = &tailscaledDown{socket: n.Socket, err: n.Err, since: n.Since, stale: n.StaleSocket}
}

// Start evaluates every evalInterval until ctx ends, so a state that begins
// while no dashboard is open still gets its start time.
func (s *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(evalInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if _, err := s.evaluate(ctx); err != nil && ctx.Err() == nil {
				log.WithError(err).Warn("taproot status: evaluate")
			}
		}
	}()
}

// Routes registers the endpoints under the taproot API group.
func (s *Service) Routes(r *gin.RouterGroup) {
	r.GET("/status", s.handle(func(e evaluation) any { return e.status }))
	r.GET("/attention", s.handle(func(e evaluation) any { return e.attention }))
	r.GET("/pools", s.handle(func(e evaluation) any { return gin.H{"pools": e.pools, "at": e.status.At} }))
}

func (s *Service) handle(pick func(evaluation) any) gin.HandlerFunc {
	return func(c *gin.Context) {
		e, err := s.evaluate(c.Request.Context())
		if err != nil {
			if c.Request.Context().Err() != nil {
				c.Abort()
				return
			}
			log.WithError(err).Warn("taproot status: evaluate")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "internal_error", "message": "could not read the server's state; see the server log"}})
			return
		}
		c.JSON(http.StatusOK, pick(e))
	}
}

type evaluation struct {
	status    Status
	attention Attention
	pools     []Pool
}

// evaluate reads every source and judges them at one instant. Quota and
// client requests are essential; a failure to read the access requests or
// the backups becomes a Needs-you item, so the verdict and pools still show.
func (s *Service) evaluate(ctx context.Context) (evaluation, error) {
	s.trackMu.Lock()
	defer s.trackMu.Unlock()
	now := s.now().UTC()
	latest, err := s.deps.Usage.LatestQuota(ctx)
	if err != nil {
		return evaluation{}, err
	}
	history, err := s.deps.Usage.QuotaHistory(ctx, now.Add(-historySpan), now.Add(time.Millisecond))
	if err != nil {
		return evaluation{}, err
	}
	window, err := s.deps.Usage.ClientRequestCounts(ctx, now.Add(-verdictWindow), now.Add(time.Millisecond))
	if err != nil {
		return evaluation{}, err
	}
	hour, err := s.deps.Usage.ClientRequestCounts(ctx, now.Add(-trafficWindow), now.Add(time.Millisecond))
	if err != nil {
		return evaluation{}, err
	}
	failed := map[string]error{}
	requests, err := s.deps.Requests.OpenRequests(ctx)
	if err != nil {
		failed[sourceAccess] = err
	}
	bs, err := s.backupState(ctx)
	if err != nil {
		failed[sourceBackups] = err
	}
	// A request whose client left fails every read; it judges nothing.
	if errCtx := ctx.Err(); errCtx != nil {
		return evaluation{}, errCtx
	}
	s.noteUnreadable(failed)
	s.tsMu.Lock()
	ts := s.tailscaled
	s.tsMu.Unlock()

	accounts := accountsFrom(s.authList(), latest, now)
	pools := buildPools(accounts, latest, history, now)
	rw := RequestWindow{From: now.Add(-verdictWindow), To: now, Requests: window.Requests, Failed: window.Failed}
	if window.Requests > 0 {
		rate := float64(window.Failed) / float64(window.Requests)
		rw.FailureRate = &rate
	}

	st := verdict(pools, accounts, rw, hour.ByProvider, now)
	if st.State != s.state {
		s.state, s.stateFrom = st.State, now
	}
	st.Since = s.stateFrom
	listed := map[string]bool{}
	att := attention(attentionInputs{
		now: now, accounts: accounts, pools: pools, recent: hour.ByProvider, requests: requests,
		backup: bs, alerts: s.deps.Alerts.DeliveryHealth(), tailscaled: ts, unreadable: failed,
		firstSeen: func(id string) time.Time {
			listed[id] = true
			if t, ok := s.seen[id]; ok {
				return t
			}
			s.seen[id] = now
			return now
		},
	})
	for id := range s.seen {
		if !listed[id] {
			delete(s.seen, id)
		}
	}
	return evaluation{status: st, attention: att, pools: pools}, nil
}

// noteUnreadable logs each source that newly fails, or fails differently,
// and each that reads again. The caller holds trackMu.
func (s *Service) noteUnreadable(failed map[string]error) {
	for source, err := range failed {
		if s.unreadable[source] != err.Error() {
			s.unreadable[source] = err.Error()
			log.WithError(err).Warnf("taproot status: read %s", source)
		}
	}
	for source := range s.unreadable {
		if failed[source] == nil {
			delete(s.unreadable, source)
			log.Infof("taproot status: %s read again", source)
		}
	}
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

func (s *Service) backupState(ctx context.Context) (backupState, error) {
	bs := backupState{watchingSince: s.startedAt}
	_, bs.scheduled = s.deps.Backups.NextRun()
	list, err := s.deps.Backups.List()
	if err != nil {
		return bs, err
	}
	for _, e := range list {
		if e.CreatedAt.After(bs.last) {
			bs.last = e.CreatedAt
		}
	}
	bs.run, bs.hasRun, err = s.deps.Backups.LastScheduledRun(ctx)
	return bs, err
}
