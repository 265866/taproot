package access

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// RequestPath is where a tailnet device without Admin asks for access, from
// the dashboard's connection gate. Like ClaimPath, it is the one admin route
// such a device reaches; it records a request and grants nothing.
const RequestPath = "/taproot/api/v1/access/request"

const (
	// requestWriteEvery spaces a device's refusal writes; the refusals in
	// between are counted in memory and added on the next write.
	requestWriteEvery = time.Minute
	// selfRequestLimit requests per selfRequestWindow per device.
	selfRequestLimit  = 10
	selfRequestWindow = time.Hour
	// maxOpenRequests bounds the Access page's list.
	maxOpenRequests = 200
	// keepDecidedRequests is how long an allowed or denied request is kept
	// after its device was last refused.
	keepDecidedRequests = 30 * 24 * time.Hour
)

// Ask is the permission a device was refused for.
type Ask string

const (
	// AskProxy is a refused model request.
	AskProxy Ask = "proxy"
	// AskAdmin is a refused dashboard or management request. It implies
	// Proxy.
	AskAdmin Ask = "admin"
)

// max returns the wider of a and b.
func (a Ask) max(b Ask) Ask {
	if a == AskAdmin || b == AskAdmin {
		return AskAdmin
	}
	return cmp.Or(a, b)
}

func init() {
	store.Register(store.Schema{Module: "access", SQL: `CREATE TABLE IF NOT EXISTS access_requests (
		node_id TEXT PRIMARY KEY,
		login TEXT NOT NULL DEFAULT '',
		owner_name TEXT NOT NULL DEFAULT '',
		node_name TEXT NOT NULL DEFAULT '',
		outside INTEGER NOT NULL DEFAULT 0,
		first_seen INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		count INTEGER NOT NULL DEFAULT 1,
		client TEXT NOT NULL DEFAULT '',
		name_hint TEXT,
		dismissed_at INTEGER,
		asked TEXT,
		allowed_at INTEGER
	) WITHOUT ROWID`})
}

// RequestInput is a device asking for access: a refusal the guard recorded,
// or the device's own request from the connection gate.
type RequestInput struct {
	NodeID    string
	Login     string
	OwnerName string
	// Node is the device's Tailscale name, "" when Tailscale hides it.
	Node    string
	Outside bool
	// Client is a short name for the program that asked, such as "Claude
	// Code", or "".
	Client string
	// NameHint is the name the device gave itself, "" to keep the last one.
	NameHint string
	// Asked is what the device was refused, "" when unknown.
	Asked Ask
	// Self marks the device's own request from the connection gate. Unlike
	// a refusal, it reopens a request the owner already allowed.
	Self bool
}

// AccessRequest is one open request, as the Access page lists it.
type AccessRequest struct {
	NodeID    string
	Login     string
	OwnerName string
	Node      string
	Outside   bool
	FirstSeen time.Time
	LastSeen  time.Time
	Count     int
	Client    string
	NameHint  string
	Asked     Ask
}

// RequestNotice is a request that was just opened or reopened, for the
// alerts module.
type RequestNotice struct {
	NodeID string
	// Name is the name the device gave itself, else its Tailscale name, else "".
	Name   string
	Login  string
	Client string
	At     time.Time
}

// Requests stores access requests in the access_requests table.
type Requests struct {
	db     *store.DB
	notify func(RequestNotice)

	mu sync.Mutex
	// pending holds each device's refusals since its last write.
	pending map[string]*pendingRefusals
	// selfAsks holds each device's recent self-requests, for the rate limit.
	selfAsks map[string][]time.Time
	// missing tracks request rows whose device successful tailnet reads
	// no longer list, for Prune.
	missing missingDevices
}

type pendingRefusals struct {
	written time.Time
	count   int
	asked   Ask
}

// NewRequests stores requests in db. notify, which may be nil, runs after a
// request opens or reopens, never for a repeated refusal.
func NewRequests(db *store.DB, notify func(RequestNotice)) *Requests {
	return &Requests{db: db, notify: notify, pending: map[string]*pendingRefusals{}, selfAsks: map[string][]time.Time{}}
}

// AttachRequests makes the guard record refusals and serve the request
// endpoints. Call it before the server starts.
func (g *Guard) AttachRequests(r *Requests) {
	g.asks.Store(r)
}

// Refused records a refusal at now. Writes are spaced requestWriteEvery per
// device; refusals in between only count. It returns whether it wrote.
func (r *Requests) Refused(ctx context.Context, in RequestInput, now time.Time) (bool, error) {
	r.mu.Lock()
	p := r.pending[in.NodeID]
	if p == nil {
		if len(r.pending) >= 1024 {
			for id, old := range r.pending {
				if now.Sub(old.written) >= requestWriteEvery {
					delete(r.pending, id)
				}
			}
		}
		p = &pendingRefusals{}
		r.pending[in.NodeID] = p
	}
	p.count++
	p.asked = p.asked.max(in.Asked)
	if !p.written.IsZero() && now.Sub(p.written) < requestWriteEvery {
		r.mu.Unlock()
		return false, nil
	}
	count := p.count
	in.Asked = p.asked
	p.written, p.count, p.asked = now, 0, ""
	r.mu.Unlock()
	in.NameHint, in.Self = "", false
	if err := r.Record(ctx, in, count, now); err != nil {
		return false, err
	}
	return true, nil
}

// Record opens, reopens, or updates in's request at now, counting count
// refusals, and announces a request that opened. A request is open until the
// owner allows or denies it. A refusal reopens a denied request, and only
// the device's own request (in.Self) reopens an allowed one; otherwise an
// allowed request is updated silently, so a device that got less than it
// asked for is not announced again on every visit. The demo seeds requests
// through it.
func (r *Requests) Record(ctx context.Context, in RequestInput, count int, now time.Time) error {
	var opened bool
	var hint sql.NullString
	err := store.WithImmediateTx(ctx, r.db.DB, func(c *sql.Conn) error {
		var dismissed, allowed sql.NullInt64
		var asked sql.NullString
		errRow := c.QueryRowContext(ctx, `SELECT dismissed_at, allowed_at, name_hint, asked FROM access_requests WHERE node_id = ?`, in.NodeID).
			Scan(&dismissed, &allowed, &hint, &asked)
		at := now.UnixMilli()
		var err error
		switch {
		case errors.Is(errRow, sql.ErrNoRows):
			opened = true
			_, err = c.ExecContext(ctx, `INSERT INTO access_requests
				(node_id, login, owner_name, node_name, outside, first_seen, last_seen, count, client, name_hint, asked)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))`,
				in.NodeID, in.Login, in.OwnerName, in.Node, in.Outside, at, at, count, in.Client, in.NameHint, string(in.Asked))
		case errRow != nil:
			return fmt.Errorf("read access request: %w", errRow)
		case dismissed.Valid || (allowed.Valid && in.Self):
			opened = true
			_, err = c.ExecContext(ctx, `UPDATE access_requests SET login = ?, owner_name = ?, node_name = ?, outside = ?,
				first_seen = ?, last_seen = ?, count = ?, client = CASE WHEN ? = '' THEN client ELSE ? END,
				name_hint = COALESCE(NULLIF(?, ''), name_hint), asked = COALESCE(NULLIF(?, ''), asked),
				dismissed_at = NULL, allowed_at = NULL WHERE node_id = ?`,
				in.Login, in.OwnerName, in.Node, in.Outside, at, at, count, in.Client, in.Client, in.NameHint, string(in.Asked), in.NodeID)
		default:
			_, err = c.ExecContext(ctx, `UPDATE access_requests SET login = ?, owner_name = ?, node_name = ?, outside = ?,
				last_seen = ?, count = count + ?, client = CASE WHEN ? = '' THEN client ELSE ? END,
				name_hint = COALESCE(NULLIF(?, ''), name_hint), asked = NULLIF(?, '') WHERE node_id = ?`,
				in.Login, in.OwnerName, in.Node, in.Outside, at, count, in.Client, in.Client, in.NameHint, string(Ask(asked.String).max(in.Asked)), in.NodeID)
		}
		if err != nil {
			return fmt.Errorf("write access request: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record access request: %w", err)
	}
	if opened && r.notify != nil {
		r.notify(RequestNotice{
			NodeID: in.NodeID, Name: cmp.Or(in.NameHint, hint.String, in.Node), Login: in.Login, Client: in.Client, At: now,
		})
	}
	return nil
}

// allowSelf counts a self-request at now and returns how long to wait when
// the device made selfRequestLimit within selfRequestWindow.
func (r *Requests) allowSelf(nodeID string, now time.Time) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	recent := r.selfAsks[nodeID][:0]
	for _, at := range r.selfAsks[nodeID] {
		if now.Sub(at) < selfRequestWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) >= selfRequestLimit {
		r.selfAsks[nodeID] = recent
		return recent[0].Add(selfRequestWindow).Sub(now), false
	}
	if len(r.selfAsks) >= 1024 {
		for id, times := range r.selfAsks {
			if len(times) == 0 || now.Sub(times[len(times)-1]) >= selfRequestWindow {
				delete(r.selfAsks, id)
			}
		}
	}
	r.selfAsks[nodeID] = append(recent, now)
	return 0, true
}

// openRequest is the condition for an open request: neither allowed nor
// denied.
const openRequest = `dismissed_at IS NULL AND allowed_at IS NULL`

// Open lists the open requests, newest first.
func (r *Requests) Open(ctx context.Context) ([]AccessRequest, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT node_id, login, owner_name, node_name, outside, first_seen, last_seen, count, client,
		COALESCE(name_hint, ''), COALESCE(asked, '')
		FROM access_requests WHERE `+openRequest+` ORDER BY last_seen DESC, node_id LIMIT ?`, maxOpenRequests)
	if err != nil {
		return nil, fmt.Errorf("list access requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []AccessRequest
	for rows.Next() {
		var a AccessRequest
		var first, last int64
		if err := rows.Scan(&a.NodeID, &a.Login, &a.OwnerName, &a.Node, &a.Outside, &first, &last, &a.Count, &a.Client, &a.NameHint, &a.Asked); err != nil {
			return nil, fmt.Errorf("read access request: %w", err)
		}
		a.FirstSeen, a.LastSeen = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

// IsOpen reports whether nodeID has an open request.
func (r *Requests) IsOpen(ctx context.Context, nodeID string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_requests WHERE node_id = ? AND `+openRequest, nodeID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("read access request: %w", err)
	}
	return n > 0, nil
}

// Dismiss denies nodeID's open request at now and reports whether it had
// one. The device's next refusal reopens it.
func (r *Requests) Dismiss(ctx context.Context, nodeID string, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE access_requests SET dismissed_at = ? WHERE node_id = ? AND `+openRequest, now.UnixMilli(), nodeID)
	if err != nil {
		return false, fmt.Errorf("dismiss access request: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("dismiss access request: %w", err)
	}
	return n > 0, nil
}

// Allow marks nodeID's request allowed at now, whether it was open, denied,
// or already allowed, and reports whether the device has a request. Only the
// device's own request reopens it.
func (r *Requests) Allow(ctx context.Context, nodeID string, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE access_requests SET allowed_at = COALESCE(allowed_at, ?), dismissed_at = NULL WHERE node_id = ?`,
		now.UnixMilli(), nodeID)
	if err != nil {
		return false, fmt.Errorf("allow access request: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("allow access request: %w", err)
	}
	return n > 0, nil
}

// Prune deletes the requests of devices that successful tailnet reads have
// missed for ForgetAfter, and allowed or denied requests whose decision and
// last refusal are both older than keepDecidedRequests. tn is the last
// read; one without peers counts for nothing, as for granted devices.
func (r *Requests) Prune(ctx context.Context, tn Tailnet, now time.Time) error {
	cutoff := now.Add(-keepDecidedRequests).UnixMilli()
	return store.WithImmediateTx(ctx, r.db.DB, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, `DELETE FROM access_requests
			WHERE COALESCE(allowed_at, dismissed_at) < ? AND last_seen < ?`, cutoff, cutoff); err != nil {
			return fmt.Errorf("prune decided access requests: %w", err)
		}
		if len(tn.Peers) == 0 {
			return nil
		}
		ids, err := queryNodeIDs(ctx, c)
		if err != nil {
			return err
		}
		since := r.missing.track(ids, tn.ids(), now)
		for id, first := range since {
			if now.Sub(first) < ForgetAfter {
				continue
			}
			if _, err := c.ExecContext(ctx, `DELETE FROM access_requests WHERE node_id = ?`, id); err != nil {
				return fmt.Errorf("prune access request: %w", err)
			}
		}
		return nil
	})
}

type rowsQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryNodeIDs(ctx context.Context, q rowsQuerier) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT node_id FROM access_requests`)
	if err != nil {
		return nil, fmt.Errorf("list access requests: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read access request: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// clientName names the program behind a User-Agent in a word or two, or "".
func clientName(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "claude-cli") || strings.Contains(ua, "claude-code"):
		return "Claude Code"
	case strings.Contains(ua, "codex"):
		return "Codex"
	case strings.HasPrefix(ua, "curl/"):
		return "curl"
	case strings.HasPrefix(ua, "mozilla/"):
		return "Browser"
	}
	return ""
}

func requestInput(id Identity, node, client string, asked Ask) RequestInput {
	return RequestInput{
		NodeID: id.NodeID, Login: id.Login, OwnerName: id.DisplayName, Node: node, Outside: id.Outside, Client: client, Asked: asked,
	}
}

// recordRefusal records a refused request from a tailnet device as an
// access request. A failure is logged and never changes the refusal.
func (g *Guard) recordRefusal(r *http.Request, id Identity, asked Ask) {
	asks := g.asks.Load()
	if asks == nil || id.NodeID == "" {
		return
	}
	self, _ := g.cachedSelf()
	in := requestInput(id, shortNodeName(id.Node, self), clientName(r.UserAgent()), asked)
	// A client that hung up was still refused.
	if _, err := asks.Refused(context.WithoutCancel(r.Context()), in, g.now()); err != nil {
		log.WithError(err).Warn("taproot access: record an access request")
	}
}

// HasOpenRequest reports whether nodeID has an open access request; false
// when requests are not attached or the read fails.
func (g *Guard) HasOpenRequest(ctx context.Context, nodeID string) bool {
	asks := g.asks.Load()
	if asks == nil {
		return false
	}
	open, err := asks.IsOpen(ctx, nodeID)
	if err != nil {
		log.WithError(err).Warn("taproot access: read an access request")
	}
	return open
}

// ResolveRequests marks allowed the request of every device that after
// gives a permission before did not, once a settings change granting it is
// saved.
func (g *Guard) ResolveRequests(ctx context.Context, before, after Grants) {
	asks := g.asks.Load()
	if asks == nil {
		return
	}
	had := make(map[string]DeviceGrant, len(before.Devices))
	for _, d := range before.Devices {
		had[d.ID] = d
	}
	now := g.now()
	for _, d := range after.Devices {
		old := had[d.ID]
		if (d.Proxy && !old.Proxy) || (d.Admin && !old.Admin) {
			if _, err := asks.Allow(ctx, d.ID, now); err != nil {
				log.WithError(err).Warn("taproot access: close a granted access request")
			}
		}
	}
}

// PruneRequests runs Requests.Prune on tn, the tailnet the sweep just read.
func (g *Guard) PruneRequests(ctx context.Context, tn Tailnet) {
	asks := g.asks.Load()
	if asks == nil {
		return
	}
	if err := asks.Prune(ctx, tn, g.now()); err != nil {
		log.WithError(err).Warn("taproot access: prune access requests")
	}
}

type selfRequestBody struct {
	Name string `json:"name"`
}

func apiError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// handleRequest serves POST /access/request: the calling tailnet device asks
// for access and may name itself. It grants nothing.
func (g *Guard) handleRequest(c *gin.Context) {
	asks := g.asks.Load()
	if !g.enabled || asks == nil {
		apiError(c, http.StatusConflict, "requests_unavailable", "this server does not take access requests")
		return
	}
	if c.GetHeader(RequestHeader) != "1" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": requestHeaderDenial()})
		return
	}
	id, ok := IdentityFrom(c)
	if !ok || !id.device || id.NodeID == "" {
		apiError(c, http.StatusForbidden, "request_tailnet_only", "only a tailnet device can ask for access")
		return
	}
	if id.Admin {
		apiError(c, http.StatusConflict, "has_access", "this device already has Admin")
		return
	}
	var body selfRequestBody
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			apiError(c, http.StatusBadRequest, "invalid_body", fmt.Sprintf("request body: %v", err))
			return
		}
	}
	name, err := CleanLabel(body.Name)
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid_name", "the name is "+err.Error())
		return
	}
	now := g.now()
	if wait, ok := asks.allowSelf(id.NodeID, now); !ok {
		retryAfter := int(math.Ceil(wait.Seconds()))
		c.Header("Retry-After", strconv.Itoa(retryAfter))
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{
			"code": "rate_limited", "message": "too many access requests from this device; try again later", "retryAfter": retryAfter,
		}})
		return
	}
	self, _ := g.cachedSelf()
	// The gate is the dashboard, so the device asks for Admin.
	in := requestInput(id, shortNodeName(id.Node, self), clientName(c.Request.UserAgent()), AskAdmin)
	in.NameHint, in.Self = name, true
	if err := asks.Record(c.Request.Context(), in, 1, now); err != nil {
		log.WithError(err).Warn("taproot access: record an access request")
		apiError(c, http.StatusInternalServerError, "internal", "could not save the request")
		return
	}
	log.Infof("taproot access: %s (%s, %s) asked for access", cmp.Or(quoteHeader(name), quoteHeader(in.Node)), id.Login, id.NodeID)
	c.JSON(http.StatusOK, gin.H{"status": "pending"})
}

// handleDismiss serves POST /access/requests/:nodeId/dismiss for admins.
func (g *Guard) handleDismiss(c *gin.Context) {
	asks := g.asks.Load()
	if asks == nil {
		apiError(c, http.StatusConflict, "requests_unavailable", "this server does not take access requests")
		return
	}
	nodeID := c.Param("nodeId")
	found, err := asks.Dismiss(c.Request.Context(), nodeID, g.now())
	if err != nil {
		log.WithError(err).Warn("taproot access: dismiss an access request")
		apiError(c, http.StatusInternalServerError, "internal", "could not dismiss the request")
		return
	}
	if !found {
		apiError(c, http.StatusNotFound, "not_found", "no open request from that device")
		return
	}
	c.JSON(http.StatusOK, gin.H{"dismissed": true})
}

// handleAllow serves POST /access/requests/:nodeId/allow for admins, after
// the Allow form saved the device's grant: it closes the request even when
// the grant gave nothing new, so the device's later refusals stay quiet.
func (g *Guard) handleAllow(c *gin.Context) {
	asks := g.asks.Load()
	if asks == nil {
		apiError(c, http.StatusConflict, "requests_unavailable", "this server does not take access requests")
		return
	}
	found, err := asks.Allow(c.Request.Context(), c.Param("nodeId"), g.now())
	if err != nil {
		log.WithError(err).Warn("taproot access: allow an access request")
		apiError(c, http.StatusInternalServerError, "internal", "could not close the request")
		return
	}
	if !found {
		apiError(c, http.StatusNotFound, "not_found", "no request from that device")
		return
	}
	c.JSON(http.StatusOK, gin.H{"allowed": true})
}
