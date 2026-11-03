package ingest

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/access"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

// mainConversationSQL selects Claude requests that belong to a top-level
// conversation: they carry a session and no parent session. Claude Code
// subagents carry their parent's session ID (see sdk/cliproxy/session), and
// their prompt caching does not follow the main conversation's settings.
// event.mainConversation is the same test in Go.
const mainConversationSQL = `(provider = 'claude' AND session_id <> '' AND parent_session_id = '')`

func (e event) mainConversation() bool {
	return e.Provider == "claude" && e.SessionID != "" && e.ParentSessionID == ""
}

// Events written before device attribution have an empty device_id; the API
// reports them as the "(none)" group so totals still add up. The device rollup
// is backfilled from existing events, so it covers all history.
const schemaV4Devices = `
ALTER TABLE usage_events ADD COLUMN device_id TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_events ADD COLUMN device_name TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_events ADD COLUMN device_login TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_events ADD COLUMN device_via TEXT NOT NULL DEFAULT '';
CREATE INDEX usage_events_device_ts ON usage_events(device_id, ts);
-- Covers the cache TTL estimate's walk over main conversations.
CREATE INDEX usage_events_main_ts ON usage_events(ts, session_id, auth_index, model, cache_write_5m_tokens, device_id)
	WHERE ` + mainConversationSQL + `;

CREATE TABLE usage_rollup_device_hourly (
	hour INTEGER NOT NULL,
	device_id TEXT NOT NULL,
	model TEXT NOT NULL,
	main INTEGER NOT NULL,
	requests INTEGER NOT NULL DEFAULT 0,
	failures INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_5m_tokens INTEGER NOT NULL DEFAULT 0,
	cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0,
	split_requests INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (hour, device_id, model, main)
) WITHOUT ROWID;
INSERT INTO usage_rollup_device_hourly
	SELECT ts / 3600000, ` + deviceAggColumns + `
	FROM usage_events GROUP BY 1, 2, 3, 4;
`

// deviceAggColumns aggregates usage_events into the device rollup's columns
// after hour.
const deviceAggColumns = `device_id, model, ` + mainConversationSQL + `, COUNT(*), SUM(failed),
	SUM(input_tokens), SUM(output_tokens), SUM(reasoning_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens),
	SUM(cache_write_5m_tokens), SUM(cache_write_1h_tokens), SUM(cache_write_5m_tokens + cache_write_1h_tokens > 0)`

func init() {
	store.Register(store.Migration{Module: "ingest", Version: 4, SQL: schemaV4Devices})
}

// deviceSource resolves upstream's per-request ID (usage.Record.TraceID) to
// the device the access guard admitted, and names stored devices.
type deviceSource interface {
	RequestDevice(requestID string) (access.Device, bool)
	// DeviceName returns the name to show for a stored device ID and the
	// name stored with it.
	DeviceName(id, stored string) string
}

// pluginSource identifies plugins that call the model executor in-process,
// through the plugin host, where no HTTP request carries a device.
type pluginSource interface {
	// CallerPlugin reports whether a usage record's context belongs to a
	// plugin's in-process call, and that plugin's ID when known.
	CallerPlugin(ctx context.Context) (pluginID string, ok bool)
	// PluginName is the plugin's display name, or "" when it is not loaded.
	PluginName(pluginID string) string
}

// pendingRecord is a queued usage record with the plugin, if any, that made it.
type pendingRecord struct {
	usage.Record
	pluginID string
	inPlugin bool
}

// AttachDevices sets where usage events get their device from. Without it,
// events have no device.
func (i *Ingest) AttachDevices(src deviceSource) {
	if src == nil {
		return
	}
	i.devices.Store(&src)
}

// AttachPlugins lets usage from in-process plugin calls be attributed to the
// plugin. It must run before the service starts to cover every record.
func (i *Ingest) AttachPlugins(src pluginSource) {
	if src == nil {
		return
	}
	i.plugins.Store(&src)
}

// recordDevice is the admitted HTTP caller behind a record when the guard
// knows one, otherwise the plugin that made it in-process. The HTTP caller
// comes first because a plugin that serves a client's request through the
// host, such as an interceptor's fallback execution, carries that request's
// ID, and the usage belongs to the client. A plugin's own work, such as a
// timer or a dashboard action, runs without a proxy request ID.
func (i *Ingest) recordDevice(p pendingRecord) access.Device {
	if src := i.devices.Load(); src != nil {
		if d, ok := (*src).RequestDevice(p.TraceID); ok {
			return d
		}
	}
	if !p.inPlugin {
		return access.Device{}
	}
	name := ""
	if src := i.plugins.Load(); src != nil && p.pluginID != "" {
		name = (*src).PluginName(p.pluginID)
	}
	return access.PluginDevice(p.pluginID, name)
}

// deviceName is the name to show for a stored device.
func (i *Ingest) deviceName(id, stored string) string {
	if src := i.devices.Load(); src != nil && id != "" {
		return (*src).DeviceName(id, stored)
	}
	return stored
}

type deviceRollupKey struct {
	Hour            int64
	DeviceID, Model string
	Main            bool
}

// deviceRow is one device rollup row, or the same aggregate over raw events.
type deviceRow struct {
	requests, failures, splitRequests int64
	tokens                            tokenSums
}

func (r *deviceRow) add(o deviceRow) {
	r.requests += o.requests
	r.failures += o.failures
	r.splitRequests += o.splitRequests
	r.tokens.add(o.tokens)
}

// deviceRollup collects one flush's additions to usage_rollup_device_hourly.
type deviceRollup map[deviceRollupKey]*deviceRow

func (d deviceRollup) add(e event) {
	k := deviceRollupKey{Hour: ms(e.TS) / hourMs, DeviceID: e.Device.ID, Model: e.Model, Main: e.mainConversation()}
	row := d[k]
	if row == nil {
		row = &deviceRow{}
		d[k] = row
	}
	row.add(deviceRow{
		requests: 1, failures: int64(boolInt(e.Failed)), tokens: e.Tokens,
		splitRequests: int64(boolInt(e.Tokens.CacheWrite5m+e.Tokens.CacheWrite1h > 0)),
	})
}

func (d deviceRollup) apply(ctx context.Context, c querier) error {
	for k, r := range d {
		t := r.tokens
		if _, err := c.ExecContext(ctx, `INSERT INTO usage_rollup_device_hourly (hour, device_id, model, main, requests, failures,
			input_tokens, output_tokens, reasoning_tokens, cache_read_tokens, cache_write_tokens, cache_write_5m_tokens,
			cache_write_1h_tokens, split_requests) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(hour, device_id, model, main) DO UPDATE SET
				requests = requests + excluded.requests,
				failures = failures + excluded.failures,
				input_tokens = input_tokens + excluded.input_tokens,
				output_tokens = output_tokens + excluded.output_tokens,
				reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
				cache_read_tokens = cache_read_tokens + excluded.cache_read_tokens,
				cache_write_tokens = cache_write_tokens + excluded.cache_write_tokens,
				cache_write_5m_tokens = cache_write_5m_tokens + excluded.cache_write_5m_tokens,
				cache_write_1h_tokens = cache_write_1h_tokens + excluded.cache_write_1h_tokens,
				split_requests = split_requests + excluded.split_requests`,
			k.Hour, k.DeviceID, k.Model, boolInt(k.Main), r.requests, r.failures, t.Input, t.Output, t.Reasoning,
			t.CacheRead, t.CacheWrite, t.CacheWrite5m, t.CacheWrite1h, r.splitRequests); err != nil {
			return fmt.Errorf("upsert device rollup: %w", err)
		}
	}
	return nil
}

// TTL estimate bounds: a cache write after a pause in this window would have
// been a cache read with a one-hour TTL.
const (
	fiveMinuteTTL = 5 * time.Minute
	oneHourTTL    = time.Hour
)

type deviceJSON struct {
	// DeviceID is empty for events recorded without a device.
	DeviceID           string   `json:"deviceId"`
	Name               string   `json:"name"`
	Login              string   `json:"login"`
	Via                string   `json:"via"`
	LastSeen           string   `json:"lastSeen"`
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
	CacheHitRatio      *float64 `json:"cacheHitRatio"`
	Main               mainJSON `json:"mainConversation"`
}

// mainJSON covers the device's Claude main-conversation requests only.
type mainJSON struct {
	Requests int64 `json:"requests"`
	// SplitRequests wrote cache with a known 5m/1h split.
	SplitRequests      int64 `json:"splitRequests"`
	CacheWriteTokens   int64 `json:"cacheWriteTokens"`
	CacheWrite5mTokens int64 `json:"cacheWrite5mTokens"`
	CacheWrite1hTokens int64 `json:"cacheWrite1hTokens"`
	// FiveMinuteShare is 5m writes over split-known writes, null without any.
	FiveMinuteShare *float64    `json:"fiveMinuteShare"`
	Estimate        ttlEstimate `json:"estimate"`
}

// ttlEstimate guesses what a one-hour cache TTL would change. Re-writes caused
// by expiry are not reported upstream, so a 5m cache write that follows a
// pause of 5 to 60 minutes in the same session, account, and model counts as
// one. With a one-hour TTL those tokens would have been cache reads, and the
// remaining 5m writes would have cost the one-hour write price instead.
type ttlEstimate struct {
	RewriteRequests int64   `json:"rewriteRequests"`
	RewriteTokens   int64   `json:"rewriteTokens"`
	RewriteCostUSD  float64 `json:"rewriteCostUSD"`
	PremiumCostUSD  float64 `json:"oneHourPremiumUSD"`
	NetSavingsUSD   float64 `json:"netSavingsUSD"`
}

func (i *Ingest) handleUsageDevices(c *gin.Context) {
	from, to, ok := i.parseRange(c, defaultRange)
	if !ok {
		return
	}
	pb, ok := i.priceBook(c)
	if !ok {
		return
	}
	devices, err := loadDevices(c.Request.Context(), i.db, ms(from), ms(to), pb)
	if err != nil {
		internalError(c, err)
		return
	}
	for k := range devices {
		devices[k].Name = i.deviceName(devices[k].DeviceID, devices[k].Name)
	}
	c.JSON(http.StatusOK, gin.H{"devices": devices})
}

func loadDevices(ctx context.Context, q querier, fromMs, toMs int64, pb *priceBook) ([]deviceJSON, error) {
	rows, err := collectDeviceRows(ctx, q, fromMs, toMs)
	if err != nil {
		return nil, err
	}
	byID := map[string]*deviceJSON{}
	// mainWrites holds each device's main-conversation 5m writes per model,
	// which the one-hour premium is priced from.
	mainWrites := map[string]map[string]int64{}
	for k, r := range rows {
		d := byID[k.DeviceID]
		if d == nil {
			d = &deviceJSON{DeviceID: k.DeviceID}
			byID[k.DeviceID] = d
		}
		t := r.tokens
		d.Requests += r.requests
		d.Failures += r.failures
		d.InputTokens += t.Input
		d.OutputTokens += t.Output
		d.ReasoningTokens += t.Reasoning
		d.CacheReadTokens += t.CacheRead
		d.CacheWriteTokens += t.CacheWrite
		d.CacheWrite5mTokens += t.CacheWrite5m
		d.CacheWrite1hTokens += t.CacheWrite1h
		if usd, ok := pb.cost(k.Model, t); ok {
			d.CostUSD += usd
		}
		if k.Main {
			d.Main.Requests += r.requests
			d.Main.SplitRequests += r.splitRequests
			d.Main.CacheWriteTokens += t.CacheWrite
			d.Main.CacheWrite5mTokens += t.CacheWrite5m
			d.Main.CacheWrite1hTokens += t.CacheWrite1h
			if mainWrites[k.DeviceID] == nil {
				mainWrites[k.DeviceID] = map[string]int64{}
			}
			mainWrites[k.DeviceID][k.Model] += t.CacheWrite5m
		}
	}
	if err := loadLastSeen(ctx, q, fromMs, toMs, byID); err != nil {
		return nil, err
	}
	if err := estimateTTL(ctx, q, fromMs, toMs, pb, byID); err != nil {
		return nil, err
	}

	out := make([]deviceJSON, 0, len(byID))
	for id, d := range byID {
		if denom := d.CacheReadTokens + d.CacheWriteTokens + d.InputTokens; denom > 0 {
			r := float64(d.CacheReadTokens) / float64(denom)
			d.CacheHitRatio = &r
		}
		if split := d.Main.CacheWrite5mTokens + d.Main.CacheWrite1hTokens; split > 0 {
			s := float64(d.Main.CacheWrite5mTokens) / float64(split)
			d.Main.FiveMinuteShare = &s
		}
		e := &d.Main.Estimate
		for model, written := range mainWrites[id] {
			if p := pb.lookup(model); p != nil {
				// A one-hour TTL writes what the 5m TTL wrote, minus the
				// re-writes, which estimateTTL already took off.
				e.PremiumCostUSD += float64(written) * (p.CacheWrite1h - p.CacheWrite5m) / 1e6
			}
		}
		e.NetSavingsUSD = e.RewriteCostUSD - e.PremiumCostUSD
		out = append(out, *d)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].CostUSD != out[b].CostUSD {
			return out[a].CostUSD > out[b].CostUSD
		}
		return out[a].DeviceID < out[b].DeviceID
	})
	return out, nil
}

// collectDeviceRows reads whole hours from the device rollup and the partial
// hours at either edge from raw events, like the summary does.
func collectDeviceRows(ctx context.Context, q querier, fromMs, toMs int64) (map[deviceRollupKey]deviceRow, error) {
	out := map[deviceRollupKey]deviceRow{}
	scan := func(query string, args ...any) error {
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("query device usage: %w", err)
		}
		defer closeRows(rows)
		for rows.Next() {
			var (
				k    deviceRollupKey
				main int
				r    deviceRow
				t    = &r.tokens
			)
			if err := rows.Scan(&k.DeviceID, &k.Model, &main, &r.requests, &r.failures, &t.Input, &t.Output, &t.Reasoning,
				&t.CacheRead, &t.CacheWrite, &t.CacheWrite5m, &t.CacheWrite1h, &r.splitRequests); err != nil {
				return fmt.Errorf("scan device usage: %w", err)
			}
			k.Main = main != 0
			cur := out[k]
			cur.add(r)
			out[k] = cur
		}
		return rows.Err()
	}
	raw := func(from, to int64) error {
		if from >= to {
			return nil
		}
		return scan(`SELECT `+deviceAggColumns+` FROM usage_events WHERE ts >= ? AND ts < ? GROUP BY 1, 2, 3`, from, to)
	}
	fullStart := (fromMs + hourMs - 1) / hourMs * hourMs
	fullEnd := toMs / hourMs * hourMs
	if fullStart >= fullEnd {
		return out, raw(fromMs, toMs)
	}
	if err := scan(`SELECT device_id, model, main, SUM(requests), SUM(failures), SUM(input_tokens), SUM(output_tokens),
		SUM(reasoning_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens), SUM(cache_write_5m_tokens),
		SUM(cache_write_1h_tokens), SUM(split_requests)
		FROM usage_rollup_device_hourly WHERE hour >= ? AND hour < ? GROUP BY 1, 2, 3`, fullStart/hourMs, fullEnd/hourMs); err != nil {
		return nil, err
	}
	if err := raw(fromMs, fullStart); err != nil {
		return nil, err
	}
	return out, raw(fullEnd, toMs)
}

// loadLastSeen sets each device's last request time in the range, along with
// its name, login, and route as of that request, so a renamed node shows its
// current name.
func loadLastSeen(ctx context.Context, q querier, fromMs, toMs int64, byID map[string]*deviceJSON) error {
	for _, d := range byID {
		var last int64
		if err := q.QueryRowContext(ctx, `SELECT ts, device_name, device_login, device_via FROM usage_events
			WHERE device_id = ? AND ts >= ? AND ts < ? ORDER BY ts DESC LIMIT 1`, d.DeviceID, fromMs, toMs).
			Scan(&last, &d.Name, &d.Login, &d.Via); err != nil {
			return fmt.Errorf("read device %q: %w", d.DeviceID, err)
		}
		d.LastSeen = formatMs(last)
	}
	return nil
}

// estimateTTL walks each main-conversation session in time order and counts
// the 5m cache writes that followed a pause long enough to expire a 5-minute
// cache but not a one-hour one. The pause runs from the previous request's
// start, when the cache was last read or written.
func estimateTTL(ctx context.Context, q querier, fromMs, toMs int64, pb *priceBook, byID map[string]*deviceJSON) error {
	rows, err := q.QueryContext(ctx, `SELECT device_id, session_id, auth_index, model, ts, cache_write_5m_tokens
		FROM usage_events INDEXED BY usage_events_main_ts WHERE ts >= ? AND ts < ? AND `+mainConversationSQL+`
		ORDER BY session_id, ts`, fromMs, toMs)
	if err != nil {
		return fmt.Errorf("query main conversations: %w", err)
	}
	defer closeRows(rows)
	type step struct {
		session, authIndex, model string
		ts                        int64
	}
	var prev step
	for rows.Next() {
		var (
			cur      step
			deviceID string
			written  int64
		)
		if err := rows.Scan(&deviceID, &cur.session, &cur.authIndex, &cur.model, &cur.ts, &written); err != nil {
			return fmt.Errorf("scan main conversation: %w", err)
		}
		gap := time.Duration(cur.ts-prev.ts) * time.Millisecond
		continued := prev.session == cur.session && prev.authIndex == cur.authIndex && prev.model == cur.model
		prev = cur
		if !continued || written <= 0 || gap <= fiveMinuteTTL || gap > oneHourTTL {
			continue
		}
		d := byID[deviceID]
		if d == nil {
			continue
		}
		e := &d.Main.Estimate
		e.RewriteRequests++
		e.RewriteTokens += written
		if p := pb.lookup(cur.model); p != nil {
			// Read instead of written, and not written again at the one-hour price.
			e.RewriteCostUSD += float64(written) * (p.CacheWrite5m - p.CacheRead) / 1e6
			e.PremiumCostUSD -= float64(written) * (p.CacheWrite1h - p.CacheWrite5m) / 1e6
		}
	}
	return rows.Err()
}
