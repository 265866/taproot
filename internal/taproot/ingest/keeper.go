package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	log "github.com/sirupsen/logrus"
)

// KeeperImportStats counts what one Usage Keeper import read and wrote.
// Rerunning an import over the same database writes nothing new.
type KeeperImportStats struct {
	EventsRead           int
	EventsInserted       int
	ObservationsRead     int
	ObservationsInserted int
	PricesInserted       int
	AccountsUpdated      int
}

const (
	keeperSource     = "usage-keeper"
	keeperBatch      = 2000
	keeperPriceLabel = "CPA Usage Keeper model_price_settings (imported)"
)

// keeperTimeLayouts covers what Usage Keeper (GORM on SQLite) has written.
var keeperTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
}

func parseKeeperTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	for _, layout := range keeperTimeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q", raw)
}

// ImportUsageKeeper copies a CPA Usage Keeper app.db into taproot's tables.
// The keeper database is opened read-only. Events are keyed by the keeper's
// event_key plus timestamp, because the keeper reuses event keys across
// restarts for short Codex request ids.
func ImportUsageKeeper(ctx context.Context, db *store.DB, keeperPath string) (KeeperImportStats, error) {
	var stats KeeperImportStats
	if _, err := os.Stat(keeperPath); err != nil {
		return stats, fmt.Errorf("usage keeper database: %w", err)
	}
	abs, err := filepath.Abs(keeperPath)
	if err != nil {
		return stats, fmt.Errorf("resolve usage keeper path: %w", err)
	}
	src, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return stats, fmt.Errorf("open usage keeper database: %w", err)
	}
	defer func() {
		if errClose := src.Close(); errClose != nil {
			log.WithError(errClose).Warn("taproot ingest: close usage keeper database")
		}
	}()

	tables, err := keeperTables(ctx, src)
	if err != nil {
		return stats, err
	}
	if !tables["usage_events"] {
		return stats, errors.New("not a Usage Keeper database: usage_events table missing")
	}
	for _, table := range []string{"usage_events", "usage_events_archive"} {
		if tables[table] {
			if err := importKeeperEvents(ctx, db, src, table, &stats); err != nil {
				return stats, err
			}
		}
	}
	if tables["quota_cycles"] && tables["quota_percent_segments"] {
		if err := importKeeperQuota(ctx, db, src, &stats); err != nil {
			return stats, err
		}
	}
	if tables["model_price_settings"] {
		if err := importKeeperPrices(ctx, db, src, &stats); err != nil {
			return stats, err
		}
	}
	if tables["usage_identities"] {
		if err := importKeeperIdentities(ctx, db, src, &stats); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func keeperTables(ctx context.Context, src *sql.DB) (map[string]bool, error) {
	rows, err := src.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, fmt.Errorf("list usage keeper tables: %w", err)
	}
	defer closeRows(rows)
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan table name: %w", err)
		}
		out[name] = true
	}
	return out, rows.Err()
}

func importKeeperEvents(ctx context.Context, db *store.DB, src *sql.DB, table string, stats *KeeperImportStats) error {
	rows, err := src.QueryContext(ctx, `SELECT id, COALESCE(event_key, ''), COALESCE(timestamp, ''), COALESCE(request_id, ''),
		COALESCE(trace_id, ''), COALESCE(session_id, ''), COALESCE(parent_session_id, ''), COALESCE(provider, ''),
		COALESCE(model, ''), COALESCE(model_alias, ''), COALESCE(auth_index, ''), COALESCE(stream, 0), COALESCE(failed, 0),
		status_code, COALESCE(latency_ms, 0), COALESCE(ttft_ms, 0), COALESCE(input_tokens, 0), COALESCE(output_tokens, 0),
		COALESCE(reasoning_tokens, 0), COALESCE(cached_tokens, 0), COALESCE(cache_read_tokens, 0), COALESCE(cache_creation_tokens, 0),
		COALESCE(response_service_tier, ''), COALESCE(effective_service_tier, ''), COALESCE(service_tier, '')
		FROM `+table+` ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read keeper %s: %w", table, err)
	}
	defer closeRows(rows)
	batch := make([]event, 0, keeperBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := withImmediateTx(ctx, db.DB, func(c *sql.Conn) error {
			n, err := insertEvents(ctx, c, batch)
			stats.EventsInserted += n
			return err
		})
		batch = batch[:0]
		return err
	}
	for rows.Next() {
		var (
			id                                         int64
			key, rawTS                                 string
			e                                          event
			stream, failed                             int
			status                                     sql.NullInt64
			input, cached, cacheRead, cacheWrite       int64
			responseTier, effectiveTier, requestedTier string
		)
		if err := rows.Scan(&id, &key, &rawTS, &e.RequestID, &e.TraceID, &e.SessionID, &e.ParentSessionID, &e.Provider,
			&e.Model, &e.Alias, &e.AuthIndex, &stream, &failed, &status, &e.LatencyMs, &e.TTFTMs, &input, &e.Tokens.Output,
			&e.Tokens.Reasoning, &cached, &cacheRead, &cacheWrite, &responseTier, &effectiveTier, &requestedTier); err != nil {
			return fmt.Errorf("scan keeper event: %w", err)
		}
		stats.EventsRead++
		ts, err := parseKeeperTime(rawTS)
		if err != nil {
			return fmt.Errorf("keeper %s id %d: %w", table, id, err)
		}
		if key == "" {
			key = fmt.Sprintf("id-%d", id)
		}
		e.Key = keeperSource + ":" + key + "@" + ts.Format(time.RFC3339Nano)
		e.TS = ts
		e.Provider = strings.ToLower(e.Provider)
		e.Stream, e.Failed = stream != 0, failed != 0
		switch {
		case status.Valid:
			e.StatusCode = int(status.Int64)
		case !e.Failed:
			e.StatusCode = 200
		}
		if cacheRead == 0 {
			cacheRead = cached
		}
		// Keeper input_tokens include cache reads and writes for every provider.
		e.Tokens.CacheRead = cacheRead
		e.Tokens.CacheWrite = cacheWrite
		e.Tokens.Input = max(0, input-cacheRead-cacheWrite)
		e.ServiceTier = firstNonEmpty(responseTier, effectiveTier, requestedTier)
		e.Source = keeperSource
		batch = append(batch, e)
		if len(batch) >= keeperBatch {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read keeper %s: %w", table, err)
	}
	return flush()
}

// keeperWindow maps Usage Keeper quota keys to taproot window names.
func keeperWindow(provider, key string) string {
	switch key {
	case "rate_limit.primary_window":
		return "codex_primary"
	case "rate_limit.secondary_window":
		return "codex_secondary"
	}
	if strings.EqualFold(provider, "codex") && !strings.HasPrefix(key, "codex_") {
		return "codex_" + normalizeWindowName(strings.TrimSuffix(key, "_window"))
	}
	return normalizeWindowName(key)
}

// importKeeperQuota turns each remaining-percent segment into observations at
// its first and last observed times, with utilization = 1 - remaining/100.
func importKeeperQuota(ctx context.Context, db *store.DB, src *sql.DB, stats *KeeperImportStats) error {
	rows, err := src.QueryContext(ctx, `SELECT c.provider, c.auth_index, c.quota_key, c.window_seconds, c.reset_at,
		s.remaining_percent, s.first_observed_at, s.last_observed_at
		FROM quota_percent_segments s JOIN quota_cycles c ON c.id = s.cycle_id ORDER BY s.id`)
	if err != nil {
		return fmt.Errorf("read keeper quota: %w", err)
	}
	defer closeRows(rows)
	var obs []observation
	for rows.Next() {
		var (
			provider, authIndex, key, rawReset, rawFirst, rawLast string
			windowSeconds                                         int64
			remaining                                             float64
		)
		if err := rows.Scan(&provider, &authIndex, &key, &windowSeconds, &rawReset, &remaining, &rawFirst, &rawLast); err != nil {
			return fmt.Errorf("scan keeper quota: %w", err)
		}
		resetAt, err := parseKeeperTime(rawReset)
		if err != nil {
			return fmt.Errorf("keeper quota reset: %w", err)
		}
		first, err := parseKeeperTime(rawFirst)
		if err != nil {
			return fmt.Errorf("keeper quota first observed: %w", err)
		}
		last, err := parseKeeperTime(rawLast)
		if err != nil {
			return fmt.Errorf("keeper quota last observed: %w", err)
		}
		o := observation{
			AuthIndex:     authIndex,
			Provider:      strings.ToLower(provider),
			Window:        keeperWindow(provider, key),
			Utilization:   math.Round((1-remaining/100)*1e4) / 1e4,
			ResetsAt:      resetAt,
			PeriodSeconds: windowSeconds,
			ObservedAt:    first,
			Source:        keeperSource,
		}
		if remaining <= 0 {
			o.Status = "rejected"
		}
		obs = append(obs, o)
		if last.UnixMilli() != first.UnixMilli() {
			o.ObservedAt = last
			obs = append(obs, o)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read keeper quota: %w", err)
	}
	stats.ObservationsRead = len(obs)
	for start := 0; start < len(obs); start += keeperBatch {
		chunk := obs[start:min(start+keeperBatch, len(obs))]
		err := withImmediateTx(ctx, db.DB, func(c *sql.Conn) error {
			for _, o := range chunk {
				res, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO quota_observations
					(auth_index, provider, window_name, utilization, resets_at, status, period_seconds, observed_at, source)
					VALUES (?,?,?,?,?,?,?,?,?)`,
					o.AuthIndex, o.Provider, o.Window, o.Utilization, nullableMs(o.ResetsAt), o.Status, o.PeriodSeconds, ms(o.ObservedAt), o.Source)
				if err != nil {
					return fmt.Errorf("insert keeper observation: %w", err)
				}
				if n, _ := res.RowsAffected(); n > 0 {
					stats.ObservationsInserted++
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// importKeeperPrices adds keeper prices for models taproot has no row for;
// seeded and edited rows win.
func importKeeperPrices(ctx context.Context, db *store.DB, src *sql.DB, stats *KeeperImportStats) error {
	rows, err := src.QueryContext(ctx, `SELECT COALESCE(model, ''), COALESCE(prompt_price_per1_m, 0), COALESCE(completion_price_per1_m, 0),
		COALESCE(cache_read_price_per1_m, 0), COALESCE(cache_creation_price_per1_m, 0), COALESCE(price_multiplier, 1), COALESCE(updated_at, '')
		FROM model_price_settings`)
	if err != nil {
		return fmt.Errorf("read keeper prices: %w", err)
	}
	defer closeRows(rows)
	var prices []Price
	for rows.Next() {
		var (
			p          Price
			multiplier float64
			updated    string
		)
		if err := rows.Scan(&p.ModelPattern, &p.Input, &p.Output, &p.CacheRead, &p.CacheWrite5m, &multiplier, &updated); err != nil {
			return fmt.Errorf("scan keeper price: %w", err)
		}
		if strings.TrimSpace(p.ModelPattern) == "" {
			continue
		}
		p.Input *= multiplier
		p.Output *= multiplier
		p.CacheRead *= multiplier
		p.CacheWrite5m *= multiplier
		p.Source = keeperPriceLabel
		if t, err := parseKeeperTime(updated); err == nil {
			p.UpdatedAt = t.Format(time.RFC3339)
		}
		prices = append(prices, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read keeper prices: %w", err)
	}
	return withImmediateTx(ctx, db.DB, func(c *sql.Conn) error {
		for _, p := range prices {
			res, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO prices (model_pattern, input, output, cache_read, cache_write_5m, cache_write_1h, source, updated_at)
				VALUES (?,?,?,?,?,0,?,?)`, strings.TrimSpace(p.ModelPattern), p.Input, p.Output, p.CacheRead, p.CacheWrite5m, p.Source, p.UpdatedAt)
			if err != nil {
				return fmt.Errorf("insert keeper price: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				stats.PricesInserted++
			}
		}
		return nil
	})
}

// importKeeperIdentities labels historical accounts. Values the auth manager
// already wrote are kept.
func importKeeperIdentities(ctx context.Context, db *store.DB, src *sql.DB, stats *KeeperImportStats) error {
	rows, err := src.QueryContext(ctx, `SELECT COALESCE(identity, ''), COALESCE(provider, ''), COALESCE(name, ''), COALESCE(alias, ''),
		COALESCE(plan_type, ''), COALESCE(disabled, 0), COALESCE(is_deleted, 0) FROM usage_identities`)
	if err != nil {
		return fmt.Errorf("read keeper identities: %w", err)
	}
	defer closeRows(rows)
	type identity struct {
		index, provider, label, email, plan string
		disabled                            bool
	}
	var ids []identity
	for rows.Next() {
		var (
			id                identity
			name, alias       string
			disabled, deleted int
		)
		if err := rows.Scan(&id.index, &id.provider, &name, &alias, &id.plan, &disabled, &deleted); err != nil {
			return fmt.Errorf("scan keeper identity: %w", err)
		}
		if id.index == "" {
			continue
		}
		id.label = firstNonEmpty(alias, name)
		if strings.Contains(name, "@") {
			id.email = name
		}
		id.disabled = disabled != 0 || deleted != 0
		id.provider = strings.ToLower(id.provider)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read keeper identities: %w", err)
	}
	return withImmediateTx(ctx, db.DB, func(c *sql.Conn) error {
		for _, id := range ids {
			res, err := c.ExecContext(ctx, `INSERT INTO accounts (auth_index, provider, label, email, plan, disabled) VALUES (?,?,?,?,?,?)
				ON CONFLICT(auth_index) DO UPDATE SET
					provider = CASE WHEN accounts.provider = '' THEN excluded.provider ELSE accounts.provider END,
					label = CASE WHEN accounts.label = '' THEN excluded.label ELSE accounts.label END,
					email = CASE WHEN accounts.email = '' THEN excluded.email ELSE accounts.email END,
					plan = CASE WHEN accounts.plan = '' THEN excluded.plan ELSE accounts.plan END,
					disabled = CASE WHEN accounts.label = '' THEN excluded.disabled ELSE accounts.disabled END
				WHERE (accounts.provider = '' AND excluded.provider <> '') OR (accounts.label = '' AND excluded.label <> '')
					OR (accounts.email = '' AND excluded.email <> '') OR (accounts.plan = '' AND excluded.plan <> '')
					OR (accounts.label = '' AND accounts.disabled <> excluded.disabled)`,
				id.index, id.provider, id.label, id.email, id.plan, boolInt(id.disabled))
			if err != nil {
				return fmt.Errorf("upsert keeper identity: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				stats.AccountsUpdated++
			}
		}
		return nil
	})
}
