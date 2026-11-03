package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/access"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

// event is one usage_events row. Input excludes cache reads and writes for
// every provider, so token columns add up without double counting.
type event struct {
	Key             string
	TS              time.Time
	RequestID       string
	TraceID         string
	SessionID       string
	ParentSessionID string
	Provider        string
	Model           string
	Alias           string
	AuthIndex       string
	AuthID          string
	Stream          bool
	Failed          bool
	StatusCode      int
	LatencyMs       int64
	TTFTMs          int64 // 0 means unknown
	Tokens          tokenSums
	ServiceTier     string
	Source          string
	Device          access.Device
}

func eventFromRecord(r usage.Record, now time.Time) event {
	ts := r.RequestedAt
	if ts.IsZero() {
		ts = now
	}
	key := strings.TrimSpace(r.RequestID)
	if key == "" {
		key = uuid.NewString()
	}
	status := r.Fail.StatusCode
	if status == 0 && !r.Failed {
		status = 200
	}
	tier := firstNonEmpty(r.ResponseServiceTier, r.EffectiveServiceTier, r.ServiceTier)
	return event{
		Key:             key,
		TS:              ts.UTC(),
		RequestID:       r.RequestID,
		TraceID:         r.TraceID,
		SessionID:       r.SessionID,
		ParentSessionID: r.ParentSessionID,
		Provider:        strings.ToLower(strings.TrimSpace(r.Provider)),
		Model:           r.Model,
		Alias:           r.Alias,
		AuthIndex:       r.AuthIndex,
		AuthID:          r.AuthID,
		Stream:          r.Stream,
		Failed:          r.Failed,
		StatusCode:      status,
		LatencyMs:       r.Latency.Milliseconds(),
		TTFTMs:          r.TTFT.Milliseconds(),
		Tokens:          tokensFromDetail(r.Provider, r.Detail),
		ServiceTier:     tier,
		Source:          "live",
	}
}

func tokensFromDetail(provider string, d usage.Detail) tokenSums {
	var t tokenSums
	if b := d.TokenBreakdown; b.Valid() && b.Quality == usage.TokenAccountingQualityComplete {
		t.Input = b.Input.UncachedTokens
		t.CacheRead = b.Input.CacheReadTokens
		t.CacheWrite = b.Input.CacheWriteTokens
		t.Output = b.Output.TotalTokens
		t.Reasoning = b.Output.ReasoningTokens
	} else {
		t.CacheRead = d.CacheReadTokens
		if t.CacheRead == 0 && !strings.EqualFold(provider, "claude") {
			t.CacheRead = d.CachedTokens
		}
		t.CacheWrite = d.CacheCreationTokens
		t.Input = d.InputTokens
		if !strings.EqualFold(provider, "claude") {
			// OpenAI-style input includes cached tokens.
			t.Input = max(0, d.InputTokens-t.CacheRead-t.CacheWrite)
		}
		t.Output = d.OutputTokens
		t.Reasoning = d.ReasoningTokens
	}
	t.CacheWrite5m = derefInt(d.CacheCreation5mTokens) + derefInt(d.CacheCreation30mTokens)
	t.CacheWrite1h = derefInt(d.CacheCreation1hTokens) + derefInt(d.CacheCreation12hTokens)
	if t.CacheWrite < t.CacheWrite5m+t.CacheWrite1h {
		t.CacheWrite = t.CacheWrite5m + t.CacheWrite1h
	}
	return t
}

func derefInt(p *int64) int64 {
	if p == nil || *p < 0 {
		return 0
	}
	return *p
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func nullableMs(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

type rollupKey struct {
	Hour                       int64
	Provider, AuthIndex, Model string
}

type rollupDelta struct {
	Requests, Failures int64
	Tokens             tokenSums
	Latency, TTFT      histogram
}

func newRollupDelta() *rollupDelta {
	return &rollupDelta{Latency: histogram{}, TTFT: histogram{}}
}

func (d *rollupDelta) addEvent(e event) {
	d.Requests++
	if e.Failed {
		d.Failures++
	}
	d.Tokens.add(e.Tokens)
	d.Latency.add(e.LatencyMs)
	if e.TTFTMs > 0 {
		d.TTFT.add(e.TTFTMs)
	}
}

// insertEvents writes events that are not already stored (by event key) and
// folds the newly inserted ones into the hourly rollup and account first/last
// seen. It returns how many rows were inserted.
func insertEvents(ctx context.Context, c *sql.Conn, events []event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	stmt, err := c.PrepareContext(ctx, `INSERT OR IGNORE INTO usage_events (
		event_key, ts, request_id, trace_id, session_id, parent_session_id, provider, model, alias,
		auth_index, auth_id, stream, failed, status_code, latency_ms, ttft_ms,
		input_tokens, output_tokens, reasoning_tokens, cache_read_tokens, cache_write_tokens,
		cache_write_5m_tokens, cache_write_1h_tokens, service_tier, source,
		device_id, device_name, device_login, device_via
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, fmt.Errorf("prepare event insert: %w", err)
	}
	defer func() {
		if errClose := stmt.Close(); errClose != nil {
			log.WithError(errClose).Warn("taproot ingest: close statement")
		}
	}()

	deltas := map[rollupKey]*rollupDelta{}
	deviceDeltas := deviceRollup{}
	type seen struct {
		provider    string
		first, last int64
	}
	accounts := map[string]*seen{}
	inserted := 0
	for _, e := range events {
		var ttft any
		if e.TTFTMs > 0 {
			ttft = e.TTFTMs
		}
		res, err := stmt.ExecContext(ctx, e.Key, ms(e.TS), e.RequestID, e.TraceID, e.SessionID, e.ParentSessionID,
			e.Provider, e.Model, e.Alias, e.AuthIndex, e.AuthID, boolInt(e.Stream), boolInt(e.Failed), e.StatusCode,
			e.LatencyMs, ttft, e.Tokens.Input, e.Tokens.Output, e.Tokens.Reasoning, e.Tokens.CacheRead,
			e.Tokens.CacheWrite, e.Tokens.CacheWrite5m, e.Tokens.CacheWrite1h, e.ServiceTier, e.Source,
			e.Device.ID, e.Device.Name, e.Device.Login, string(e.Device.Via))
		if err != nil {
			return inserted, fmt.Errorf("insert event: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return inserted, fmt.Errorf("insert event rows affected: %w", err)
		}
		if n == 0 {
			continue
		}
		inserted++
		k := rollupKey{Hour: ms(e.TS) / hourMs, Provider: e.Provider, AuthIndex: e.AuthIndex, Model: e.Model}
		d := deltas[k]
		if d == nil {
			d = newRollupDelta()
			deltas[k] = d
		}
		d.addEvent(e)
		deviceDeltas.add(e)
		if e.AuthIndex != "" {
			a := accounts[e.AuthIndex]
			t := ms(e.TS)
			if a == nil {
				accounts[e.AuthIndex] = &seen{provider: e.Provider, first: t, last: t}
			} else {
				a.first = min(a.first, t)
				a.last = max(a.last, t)
			}
		}
	}
	for k, d := range deltas {
		if err := applyRollupDelta(ctx, c, k, d); err != nil {
			return inserted, err
		}
	}
	if err := deviceDeltas.apply(ctx, c); err != nil {
		return inserted, err
	}
	for idx, a := range accounts {
		if _, err := c.ExecContext(ctx, `INSERT INTO accounts (auth_index, provider, first_seen, last_seen) VALUES (?,?,?,?)
			ON CONFLICT(auth_index) DO UPDATE SET
				provider = CASE WHEN accounts.provider = '' THEN excluded.provider ELSE accounts.provider END,
				first_seen = MIN(COALESCE(accounts.first_seen, excluded.first_seen), excluded.first_seen),
				last_seen = MAX(COALESCE(accounts.last_seen, excluded.last_seen), excluded.last_seen)`,
			idx, a.provider, a.first, a.last); err != nil {
			return inserted, fmt.Errorf("upsert account seen: %w", err)
		}
	}
	return inserted, nil
}

const hourMs = int64(time.Hour / time.Millisecond)

func applyRollupDelta(ctx context.Context, c *sql.Conn, k rollupKey, d *rollupDelta) error {
	var latBlob, ttftBlob []byte
	err := c.QueryRowContext(ctx, `SELECT latency_hist, ttft_hist FROM usage_rollup_hourly WHERE hour=? AND provider=? AND auth_index=? AND model=?`,
		k.Hour, k.Provider, k.AuthIndex, k.Model).Scan(&latBlob, &ttftBlob)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read rollup: %w", err)
	}
	lat, ttft := histogram{}, histogram{}
	decodeHistogram(latBlob, lat)
	decodeHistogram(ttftBlob, ttft)
	lat.merge(d.Latency)
	ttft.merge(d.TTFT)
	t := d.Tokens
	_, err = c.ExecContext(ctx, `INSERT INTO usage_rollup_hourly (hour, provider, auth_index, model, requests, failures,
		input_tokens, output_tokens, reasoning_tokens, cache_read_tokens, cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens,
		latency_hist, ttft_hist) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(hour, provider, auth_index, model) DO UPDATE SET
			requests = requests + excluded.requests,
			failures = failures + excluded.failures,
			input_tokens = input_tokens + excluded.input_tokens,
			output_tokens = output_tokens + excluded.output_tokens,
			reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
			cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
			cache_write_tokens = cache_write_tokens + excluded.cache_write_tokens,
			cache_write_5m_tokens = cache_write_5m_tokens + excluded.cache_write_5m_tokens,
			cache_write_1h_tokens = cache_write_1h_tokens + excluded.cache_write_1h_tokens,
			latency_hist = excluded.latency_hist,
			ttft_hist = excluded.ttft_hist`,
		k.Hour, k.Provider, k.AuthIndex, k.Model, d.Requests, d.Failures,
		t.Input, t.Output, t.Reasoning, t.CacheRead, t.CacheWrite, t.CacheWrite5m, t.CacheWrite1h,
		lat.encode(), ttft.encode())
	if err != nil {
		return fmt.Errorf("upsert rollup: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// quotaHeartbeat is how long an unchanged window goes without a new row.
const quotaHeartbeat = 5 * time.Minute

// resetJitter absorbs reset times derived from relative seconds, which move
// by a second or two between responses without the window actually changing.
const resetJitter = 2 * time.Minute

type quotaKey struct{ AuthIndex, Window string }

type quotaLast struct {
	Utilization float64
	ResetsAt    time.Time
	Status      string
	ObservedAt  time.Time
}

// quotaTracker decides which observations deserve a row: a changed value, or
// a heartbeat after quotaHeartbeat. Only the single writer goroutine uses it.
type quotaTracker struct {
	last map[quotaKey]*quotaLast
}

func newQuotaTracker() *quotaTracker { return &quotaTracker{last: map[quotaKey]*quotaLast{}} }

func (q *quotaTracker) load(ctx context.Context, c querier, k quotaKey) (*quotaLast, error) {
	if l, ok := q.last[k]; ok {
		return l, nil
	}
	var (
		l        quotaLast
		resetsAt sql.NullInt64
		observed int64
	)
	err := c.QueryRowContext(ctx, `SELECT utilization, resets_at, status, observed_at FROM quota_observations
		WHERE auth_index=? AND window_name=? ORDER BY observed_at DESC LIMIT 1`, k.AuthIndex, k.Window).
		Scan(&l.Utilization, &resetsAt, &l.Status, &observed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load last quota observation: %w", err)
	}
	if resetsAt.Valid {
		l.ResetsAt = time.UnixMilli(resetsAt.Int64).UTC()
	}
	l.ObservedAt = time.UnixMilli(observed).UTC()
	q.last[k] = &l
	return &l, nil
}

func (q *quotaTracker) shouldWrite(prev *quotaLast, o observation) bool {
	if prev == nil {
		return true
	}
	if o.ObservedAt.Before(prev.ObservedAt) {
		return false
	}
	if math.Abs(prev.Utilization-o.Utilization) > 1e-9 || prev.Status != o.Status {
		return true
	}
	if prev.ResetsAt.IsZero() != o.ResetsAt.IsZero() {
		return true
	}
	if d := o.ResetsAt.Sub(prev.ResetsAt); d > resetJitter || d < -resetJitter {
		return true
	}
	return o.ObservedAt.Sub(prev.ObservedAt) >= quotaHeartbeat
}

func (q *quotaTracker) write(ctx context.Context, c *sql.Conn, obs []observation) (int, error) {
	written := 0
	for _, o := range obs {
		if o.AuthIndex == "" || o.Window == "" {
			continue
		}
		k := quotaKey{o.AuthIndex, o.Window}
		prev, err := q.load(ctx, c, k)
		if err != nil {
			return written, err
		}
		if !q.shouldWrite(prev, o) {
			continue
		}
		if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO quota_observations
			(auth_index, provider, window_name, utilization, resets_at, status, period_seconds, observed_at, source)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			o.AuthIndex, o.Provider, o.Window, o.Utilization, nullableMs(o.ResetsAt), o.Status, o.PeriodSeconds, ms(o.ObservedAt), o.Source); err != nil {
			return written, fmt.Errorf("insert quota observation: %w", err)
		}
		q.last[k] = &quotaLast{Utilization: o.Utilization, ResetsAt: o.ResetsAt, Status: o.Status, ObservedAt: o.ObservedAt}
		written++
	}
	return written, nil
}

// forget drops cached state so the next write re-reads the database. A failed
// transaction may have left the cache ahead of what was committed.
func (q *quotaTracker) forget() { q.last = map[quotaKey]*quotaLast{} }
