package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Totals is the contract's aggregate. InputTokens excludes cache reads and
// writes. Latency and TTFT percentiles come from log-bucketed histograms
// (within about 5%) and are null when there are no samples.
type Totals struct {
	Requests           int64    `json:"requests"`
	Failures           int64    `json:"failures"`
	InputTokens        int64    `json:"inputTokens"`
	OutputTokens       int64    `json:"outputTokens"`
	ReasoningTokens    int64    `json:"reasoningTokens"`
	CacheReadTokens    int64    `json:"cacheReadTokens"`
	CacheWriteTokens   int64    `json:"cacheWriteTokens"`
	CacheWrite5mTokens int64    `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens int64    `json:"cacheWrite1hTokens"`
	CostUSD            float64  `json:"costUSD"`
	LatencyP50Ms       *float64 `json:"latencyP50Ms"`
	LatencyP95Ms       *float64 `json:"latencyP95Ms"`
	TTFTP50Ms          *float64 `json:"ttftP50Ms"`
	TTFTP95Ms          *float64 `json:"ttftP95Ms"`
}

// agg accumulates rows. Tokens stay per model so cost can be priced at the
// end, which is what makes a price edit reprice all history.
type agg struct {
	requests, failures int64
	byModel            map[string]*tokenSums
	lat, ttft          histogram
}

func newAgg() *agg {
	return &agg{byModel: map[string]*tokenSums{}, lat: histogram{}, ttft: histogram{}}
}

func (a *agg) addTokens(model string, t tokenSums) {
	s := a.byModel[model]
	if s == nil {
		s = &tokenSums{}
		a.byModel[model] = s
	}
	s.add(t)
}

func (a *agg) totals(pb *priceBook) Totals {
	var out Totals
	out.Requests, out.Failures = a.requests, a.failures
	for model, t := range a.byModel {
		out.InputTokens += t.Input
		out.OutputTokens += t.Output
		out.ReasoningTokens += t.Reasoning
		out.CacheReadTokens += t.CacheRead
		out.CacheWriteTokens += t.CacheWrite
		out.CacheWrite5mTokens += t.CacheWrite5m
		out.CacheWrite1hTokens += t.CacheWrite1h
		if usd, ok := pb.cost(model, *t); ok {
			out.CostUSD += usd
		}
	}
	out.LatencyP50Ms, out.LatencyP95Ms = a.lat.percentile(0.5), a.lat.percentile(0.95)
	out.TTFTP50Ms, out.TTFTP95Ms = a.ttft.percentile(0.5), a.ttft.percentile(0.95)
	return out
}

var bucketSizes = map[string]time.Duration{"5m": 5 * time.Minute, "1h": time.Hour, "1d": 24 * time.Hour}

var groupByValues = map[string]bool{"model": true, "account": true, "provider": true, "session": true, "device": true}

// summaryBuilder keys buckets by their start. Fixed-size buckets start on
// multiples of their size; days start at local midnight in loc, so they can be
// 23 or 25 hours long.
type summaryBuilder struct {
	bucketMs int64
	loc      *time.Location
	groupBy  string
	buckets  map[int64]map[string]*agg
	total    *agg
}

func (s *summaryBuilder) groupKey(provider, authIndex, model, session, device string) string {
	var k string
	switch s.groupBy {
	case "device":
		k = device
	case "account":
		k = authIndex
	case "provider":
		k = provider
	case "session":
		k = session
	default:
		k = model
	}
	if k == "" {
		return noneGroupKey
	}
	return k
}

func (s *summaryBuilder) bucketStart(tsMs int64) int64 {
	if s.loc == nil {
		return tsMs - tsMs%s.bucketMs
	}
	y, m, d := time.UnixMilli(tsMs).In(s.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, s.loc).UnixMilli()
}

func (s *summaryBuilder) nextBucket(start int64) int64 {
	if s.loc == nil {
		return start + s.bucketMs
	}
	y, m, d := time.UnixMilli(start).In(s.loc).Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, s.loc).UnixMilli()
}

// hourAligned reports whether every bucket boundary in [fromMs, toMs) falls on
// a UTC hour, which the hourly rollup needs. Zones with half-hour offsets fail.
func (s *summaryBuilder) hourAligned(fromMs, toMs int64) bool {
	for b := s.bucketStart(fromMs); b < toMs; b = s.nextBucket(b) {
		if b%hourMs != 0 {
			return false
		}
	}
	return true
}

func (s *summaryBuilder) target(tsMs int64, key string) *agg {
	b := s.bucketStart(tsMs)
	groups := s.buckets[b]
	if groups == nil {
		groups = map[string]*agg{}
		s.buckets[b] = groups
	}
	a := groups[key]
	if a == nil {
		a = newAgg()
		groups[key] = a
	}
	return a
}

func (s *summaryBuilder) scanRaw(ctx context.Context, q querier, fromMs, toMs int64) error {
	if fromMs >= toMs {
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT ts, provider, auth_index, model, session_id, device_id, failed, latency_ms, COALESCE(ttft_ms, 0),
		input_tokens, output_tokens, reasoning_tokens, cache_read_tokens, cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens
		FROM usage_events WHERE ts >= ? AND ts < ?`, fromMs, toMs)
	if err != nil {
		return fmt.Errorf("query events: %w", err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var (
			ts, latency, ttft                               int64
			provider, authIndex, model, sessionID, deviceID string
			failed                                          int
			t                                               tokenSums
		)
		if err := rows.Scan(&ts, &provider, &authIndex, &model, &sessionID, &deviceID, &failed, &latency, &ttft,
			&t.Input, &t.Output, &t.Reasoning, &t.CacheRead, &t.CacheWrite, &t.CacheWrite5m, &t.CacheWrite1h); err != nil {
			return fmt.Errorf("scan event: %w", err)
		}
		for _, a := range []*agg{s.target(ts, s.groupKey(provider, authIndex, model, sessionID, deviceID)), s.total} {
			a.requests++
			if failed != 0 {
				a.failures++
			}
			a.addTokens(model, t)
			a.lat.add(latency)
			if ttft > 0 {
				a.ttft.add(ttft)
			}
		}
	}
	return rows.Err()
}

func (s *summaryBuilder) scanRollup(ctx context.Context, q querier, fromHour, toHour int64) error {
	rows, err := q.QueryContext(ctx, `SELECT hour, provider, auth_index, model, requests, failures,
		input_tokens, output_tokens, reasoning_tokens, cache_read_tokens, cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens,
		latency_hist, ttft_hist FROM usage_rollup_hourly WHERE hour >= ? AND hour < ?`, fromHour, toHour)
	if err != nil {
		return fmt.Errorf("query rollup: %w", err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var (
			hour, requests, failures   int64
			provider, authIndex, model string
			t                          tokenSums
			latBlob, ttftBlob          []byte
		)
		if err := rows.Scan(&hour, &provider, &authIndex, &model, &requests, &failures,
			&t.Input, &t.Output, &t.Reasoning, &t.CacheRead, &t.CacheWrite, &t.CacheWrite5m, &t.CacheWrite1h, &latBlob, &ttftBlob); err != nil {
			return fmt.Errorf("scan rollup: %w", err)
		}
		for _, a := range []*agg{s.target(hour*hourMs, s.groupKey(provider, authIndex, model, "", "")), s.total} {
			a.requests += requests
			a.failures += failures
			a.addTokens(model, t)
			decodeHistogram(latBlob, a.lat)
			decodeHistogram(ttftBlob, a.ttft)
		}
	}
	return rows.Err()
}

type summaryBucket struct {
	T      string            `json:"t"`
	Groups map[string]Totals `json:"groups"`
}

func (i *Ingest) handleUsageSummary(c *gin.Context) {
	from, to, ok := i.parseRange(c, defaultRange)
	if !ok {
		return
	}
	bucketName := c.DefaultQuery("bucket", "1h")
	bucket, ok := bucketSizes[bucketName]
	if !ok {
		badRequest(c, "bucket: want 5m, 1h, or 1d")
		return
	}
	groupBy := c.DefaultQuery("groupBy", "model")
	if !groupByValues[groupBy] {
		badRequest(c, "groupBy: want model, account, provider, session, or device")
		return
	}
	s := &summaryBuilder{bucketMs: bucket.Milliseconds(), groupBy: groupBy, buckets: map[int64]map[string]*agg{}, total: newAgg()}
	// tz is an IANA zone name; days run from its midnight. Shorter buckets
	// stay on UTC boundaries.
	if raw := strings.TrimSpace(c.Query("tz")); raw != "" {
		loc, err := time.LoadLocation(raw)
		if err != nil || strings.EqualFold(raw, "local") {
			badRequest(c, "tz: want an IANA time zone such as America/Los_Angeles")
			return
		}
		if bucketName == "1d" {
			s.loc = loc
		}
	}
	fromMs, toMs := ms(from), ms(to)
	firstBucket := s.bucketStart(fromMs)
	// Days are at least 23 hours, so this bounds the count in any zone.
	if (toMs-firstBucket)/min(s.bucketMs, 23*hourMs) >= maxBucketCount {
		badRequest(c, "range holds more than %d %s buckets", maxBucketCount, bucketName)
		return
	}
	pb, ok := i.priceBook(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	// The hourly rollup has no session or device column.
	useRollup := bucketName != "5m" && groupBy != "session" && groupBy != "device" && s.hourAligned(fromMs, toMs)
	if err := s.collect(ctx, i.db, fromMs, toMs, useRollup); err != nil {
		internalError(c, err)
		return
	}
	out := make([]summaryBucket, 0, (toMs-firstBucket)/s.bucketMs+2)
	for b := firstBucket; b < toMs; b = s.nextBucket(b) {
		groups := map[string]Totals{}
		for k, a := range s.buckets[b] {
			groups[k] = a.totals(pb)
		}
		out = append(out, summaryBucket{T: formatMs(b), Groups: groups})
	}
	c.JSON(http.StatusOK, gin.H{"totals": s.total.totals(pb), "buckets": out})
}

// collect reads whole hours from the rollup and the partial hours at either
// edge from raw events, so any from/to gives exact counts.
func (s *summaryBuilder) collect(ctx context.Context, q querier, fromMs, toMs int64, useRollup bool) error {
	if !useRollup {
		return s.scanRaw(ctx, q, fromMs, toMs)
	}
	fullStart := (fromMs + hourMs - 1) / hourMs * hourMs
	fullEnd := toMs / hourMs * hourMs
	if fullStart >= fullEnd {
		return s.scanRaw(ctx, q, fromMs, toMs)
	}
	if err := s.scanRollup(ctx, q, fullStart/hourMs, fullEnd/hourMs); err != nil {
		return err
	}
	if err := s.scanRaw(ctx, q, fromMs, fullStart); err != nil {
		return err
	}
	return s.scanRaw(ctx, q, fullEnd, toMs)
}

// UsageEvent is one usage record as returned by /usage/events.
type UsageEvent struct {
	ID                 int64   `json:"id"`
	EventKey           string  `json:"eventKey"`
	T                  string  `json:"t"`
	RequestID          string  `json:"requestId"`
	TraceID            string  `json:"traceId"`
	SessionID          string  `json:"sessionId"`
	ParentSessionID    string  `json:"parentSessionId"`
	Provider           string  `json:"provider"`
	Model              string  `json:"model"`
	Alias              string  `json:"alias"`
	AuthIndex          string  `json:"authIndex"`
	AuthID             string  `json:"authId"`
	Stream             bool    `json:"stream"`
	Failed             bool    `json:"failed"`
	StatusCode         int     `json:"statusCode"`
	LatencyMs          int64   `json:"latencyMs"`
	TTFTMs             *int64  `json:"ttftMs"`
	InputTokens        int64   `json:"inputTokens"`
	OutputTokens       int64   `json:"outputTokens"`
	ReasoningTokens    int64   `json:"reasoningTokens"`
	CacheReadTokens    int64   `json:"cacheReadTokens"`
	CacheWriteTokens   int64   `json:"cacheWriteTokens"`
	CacheWrite5mTokens int64   `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens int64   `json:"cacheWrite1hTokens"`
	ServiceTier        string  `json:"serviceTier"`
	CostUSD            float64 `json:"costUSD"`
	Priced             bool    `json:"priced"`
	Source             string  `json:"source"`
	DeviceID           string  `json:"deviceId"`
	DeviceName         string  `json:"deviceName"`
	DeviceLogin        string  `json:"deviceLogin"`
}

const eventColumns = `id, event_key, ts, request_id, trace_id, session_id, parent_session_id, provider, model, alias,
	auth_index, auth_id, stream, failed, status_code, latency_ms, ttft_ms, input_tokens, output_tokens, reasoning_tokens,
	cache_read_tokens, cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens, service_tier, source,
	device_id, device_name, device_login`

func scanEvent(rows *sql.Rows, pb *priceBook) (UsageEvent, int64, error) {
	var (
		e              UsageEvent
		ts             int64
		stream, failed int
	)
	err := rows.Scan(&e.ID, &e.EventKey, &ts, &e.RequestID, &e.TraceID, &e.SessionID, &e.ParentSessionID, &e.Provider,
		&e.Model, &e.Alias, &e.AuthIndex, &e.AuthID, &stream, &failed, &e.StatusCode, &e.LatencyMs, &e.TTFTMs,
		&e.InputTokens, &e.OutputTokens, &e.ReasoningTokens, &e.CacheReadTokens, &e.CacheWriteTokens,
		&e.CacheWrite5mTokens, &e.CacheWrite1hTokens, &e.ServiceTier, &e.Source,
		&e.DeviceID, &e.DeviceName, &e.DeviceLogin)
	if err != nil {
		return e, 0, fmt.Errorf("scan event: %w", err)
	}
	e.T = formatMs(ts)
	e.Stream, e.Failed = stream != 0, failed != 0
	e.CostUSD, e.Priced = pb.cost(e.Model, e.tokens())
	return e, ts, nil
}

func (e UsageEvent) tokens() tokenSums {
	return tokenSums{
		Input: e.InputTokens, Output: e.OutputTokens, Reasoning: e.ReasoningTokens, CacheRead: e.CacheReadTokens,
		CacheWrite: e.CacheWriteTokens, CacheWrite5m: e.CacheWrite5mTokens, CacheWrite1h: e.CacheWrite1hTokens,
	}
}

func (i *Ingest) handleUsageEvents(c *gin.Context) {
	from, to, ok := i.parseRange(c, defaultRange)
	if !ok {
		return
	}
	limit, ok := parseLimit(c)
	if !ok {
		return
	}
	where := []string{"ts >= ?", "ts < ?"}
	args := []any{ms(from), ms(to)}
	for param, column := range map[string]string{"authIndex": "auth_index", "model": "model", "sessionId": "session_id"} {
		if v := strings.TrimSpace(c.Query(param)); v != "" {
			where = append(where, column+" = ?")
			args = append(args, v)
		}
	}
	// "(none)", the summary's key for events without a device, selects them.
	if v := strings.TrimSpace(c.Query("deviceId")); v != "" {
		if v == noneGroupKey {
			v = ""
		}
		where = append(where, "device_id = ?")
		args = append(args, v)
	}
	switch strings.TrimSpace(c.Query("failed")) {
	case "":
	case "true":
		where = append(where, "failed = 1")
	case "false":
		where = append(where, "failed = 0")
	default:
		badRequest(c, "failed: want true or false")
		return
	}
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		cts, rest, err := decodeCursor(raw)
		var cid int64
		if err == nil {
			_, err = fmt.Sscan(rest, &cid)
		}
		if err != nil {
			badRequest(c, "cursor: malformed")
			return
		}
		where = append(where, "(ts < ? OR (ts = ? AND id < ?))")
		args = append(args, cts, cts, cid)
	}
	pb, ok := i.priceBook(c)
	if !ok {
		return
	}
	args = append(args, limit+1)
	rows, err := i.db.QueryContext(c.Request.Context(), `SELECT `+eventColumns+` FROM usage_events WHERE `+
		strings.Join(where, " AND ")+` ORDER BY ts DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		internalError(c, err)
		return
	}
	defer closeRows(rows)
	events := []UsageEvent{}
	var stamps []int64
	for rows.Next() {
		e, ts, err := scanEvent(rows, pb)
		if err != nil {
			internalError(c, err)
			return
		}
		e.DeviceName = i.deviceName(e.DeviceID, e.DeviceName)
		events = append(events, e)
		stamps = append(stamps, ts)
	}
	if err := rows.Err(); err != nil {
		internalError(c, err)
		return
	}
	var next *string
	if len(events) > limit {
		events = events[:limit]
		cur := encodeCursor(stamps[limit-1], fmt.Sprint(events[limit-1].ID))
		next = &cur
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "nextCursor": next})
}

type sessionJSON struct {
	SessionID       string   `json:"sessionId"`
	FirstAt         string   `json:"firstAt"`
	LastAt          string   `json:"lastAt"`
	Requests        int64    `json:"requests"`
	Accounts        []string `json:"accounts"`
	Models          []string `json:"models"`
	Totals          Totals   `json:"totals"`
	AccountSwitches int      `json:"accountSwitches"`
}

func (i *Ingest) handleUsageSessions(c *gin.Context) {
	from, to, ok := i.parseRange(c, defaultRange)
	if !ok {
		return
	}
	limit, ok := parseLimit(c)
	if !ok {
		return
	}
	having := ""
	args := []any{ms(from), ms(to)}
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		cts, cid, err := decodeCursor(raw)
		if err != nil {
			badRequest(c, "cursor: malformed")
			return
		}
		having = " HAVING MAX(ts) < ? OR (MAX(ts) = ? AND session_id < ?)"
		args = append(args, cts, cts, cid)
	}
	args = append(args, limit+1)
	ctx := c.Request.Context()
	rows, err := i.db.QueryContext(ctx, `SELECT session_id, MAX(ts) FROM usage_events
		WHERE ts >= ? AND ts < ? AND session_id <> '' GROUP BY session_id`+having+`
		ORDER BY MAX(ts) DESC, session_id DESC LIMIT ?`, args...)
	if err != nil {
		internalError(c, err)
		return
	}
	type page struct {
		id   string
		last int64
	}
	var ids []page
	for rows.Next() {
		var p page
		if err := rows.Scan(&p.id, &p.last); err != nil {
			closeRows(rows)
			internalError(c, err)
			return
		}
		ids = append(ids, p)
	}
	closeRows(rows)
	if err := rows.Err(); err != nil {
		internalError(c, err)
		return
	}
	var next *string
	if len(ids) > limit {
		ids = ids[:limit]
		cur := encodeCursor(ids[limit-1].last, ids[limit-1].id)
		next = &cur
	}
	pb, ok := i.priceBook(c)
	if !ok {
		return
	}
	sessions := make([]sessionJSON, 0, len(ids))
	for _, p := range ids {
		s, err := i.loadSession(ctx, p.id, ms(from), ms(to), pb)
		if err != nil {
			internalError(c, err)
			return
		}
		sessions = append(sessions, s)
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions, "nextCursor": next})
}

func (i *Ingest) loadSession(ctx context.Context, id string, fromMs, toMs int64, pb *priceBook) (sessionJSON, error) {
	rows, err := i.db.QueryContext(ctx, `SELECT ts, auth_index, model, failed, latency_ms, COALESCE(ttft_ms, 0),
		input_tokens, output_tokens, reasoning_tokens, cache_read_tokens, cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens
		FROM usage_events WHERE session_id = ? AND ts >= ? AND ts < ? ORDER BY ts, id LIMIT ?`, id, fromMs, toMs, sessionsMaxRows)
	if err != nil {
		return sessionJSON{}, fmt.Errorf("query session: %w", err)
	}
	defer closeRows(rows)
	s := sessionJSON{SessionID: id, Accounts: []string{}, Models: []string{}}
	a := newAgg()
	accounts, models := map[string]bool{}, map[string]bool{}
	var first, last int64
	prevAccount := ""
	for rows.Next() {
		var (
			ts, latency, ttft int64
			authIndex, model  string
			failed            int
			t                 tokenSums
		)
		if err := rows.Scan(&ts, &authIndex, &model, &failed, &latency, &ttft,
			&t.Input, &t.Output, &t.Reasoning, &t.CacheRead, &t.CacheWrite, &t.CacheWrite5m, &t.CacheWrite1h); err != nil {
			return s, fmt.Errorf("scan session event: %w", err)
		}
		if a.requests == 0 {
			first = ts
		} else if authIndex != prevAccount {
			s.AccountSwitches++
		}
		prevAccount = authIndex
		last = ts
		a.requests++
		if failed != 0 {
			a.failures++
		}
		a.addTokens(model, t)
		a.lat.add(latency)
		if ttft > 0 {
			a.ttft.add(ttft)
		}
		if authIndex != "" && !accounts[authIndex] {
			accounts[authIndex] = true
			s.Accounts = append(s.Accounts, authIndex)
		}
		if model != "" && !models[model] {
			models[model] = true
			s.Models = append(s.Models, model)
		}
	}
	if err := rows.Err(); err != nil {
		return s, err
	}
	sort.Strings(s.Models)
	s.FirstAt, s.LastAt = formatMs(first), formatMs(last)
	s.Requests = a.requests
	s.Totals = a.totals(pb)
	return s, nil
}
