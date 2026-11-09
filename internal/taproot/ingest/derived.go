package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
	log "github.com/sirupsen/logrus"
)

// tableDelta collects what events add to one derived table.
type tableDelta interface {
	add(e event)
	apply(ctx context.Context, c *sql.Conn) error
}

// derivedTable is a table derived from usage_events and what events add to it.
type derivedTable struct {
	name string
	// source selects the usage_events rows that give the table rows.
	source string
	// covered counts the source events the table already reflects.
	covered string
	delta   tableDelta
}

// derivedTables returns every table derived from usage_events with an empty
// delta: the writer fills them from one flush, rebuildDerived from history.
func derivedTables() []derivedTable {
	return []derivedTable{
		{"usage_rollup_hourly", "1", requestsIn("usage_rollup_hourly", "1"), hourlyRollup{}},
		{"usage_device_hourly", "1", requestsIn("usage_device_hourly", "1"), deviceRollup{}},
		{"usage_session_hourly", "1", requestsIn("usage_session_hourly", "1"), sessionRollup{}},
		// usage_sessions has no counts, and the writer updates it in the same
		// transaction as usage_session_hourly, so it goes stale with it.
		{"usage_sessions", "session_id <> ''", requestsIn("usage_session_hourly", "session_id <> ''"), sessionSpans{}},
	}
}

func requestsIn(table, where string) string {
	return `SELECT COALESCE(SUM(requests), 0) FROM ` + table + ` WHERE ` + where
}

// rebuildDerived refills, in one transaction, each derived table that is
// empty while usage_events has rows it derives from, or that reflects fewer
// or more events than usage_events holds. store.Open creates a new derived
// table empty, so this covers the first start of a release that adds one and
// a restored backup from before it; the count covers a previous release that
// wrote events without maintaining the table, after a rollback. It must
// finish before the writer starts, or the rebuild would miss or double-count
// a flush.
func rebuildDerived(ctx context.Context, db *sql.DB) error {
	return store.WithImmediateTx(ctx, db, func(c *sql.Conn) error {
		started := time.Now()
		var stale []derivedTable
		for _, t := range derivedTables() {
			var rebuild bool
			if err := c.QueryRowContext(ctx, `SELECT (NOT EXISTS (SELECT 1 FROM `+t.name+`)
				AND EXISTS (SELECT 1 FROM usage_events WHERE `+t.source+`))
				OR (SELECT COUNT(*) FROM usage_events WHERE `+t.source+`) <> (`+t.covered+`)`).Scan(&rebuild); err != nil {
				return fmt.Errorf("check %s: %w", t.name, err)
			}
			if rebuild {
				stale = append(stale, t)
			}
		}
		if len(stale) == 0 {
			return nil
		}
		events, err := scanAllEvents(ctx, c, func(e event) {
			for _, t := range stale {
				t.delta.add(e)
			}
		})
		if err != nil {
			return err
		}
		names := make([]string, len(stale))
		for n, t := range stale {
			if _, err := c.ExecContext(ctx, `DELETE FROM `+t.name); err != nil {
				return fmt.Errorf("clear %s: %w", t.name, err)
			}
			if err := t.delta.apply(ctx, c); err != nil {
				return fmt.Errorf("rebuild %s: %w", t.name, err)
			}
			names[n] = t.name
		}
		log.Infof("taproot ingest: rebuilt %s from %d events in %s",
			strings.Join(names, ", "), events, time.Since(started).Round(time.Millisecond))
		return nil
	})
}

// scanAllEvents passes every stored event, with the fields the derived
// tables read, to fn and returns how many there were.
func scanAllEvents(ctx context.Context, q querier, fn func(event)) (int, error) {
	rows, err := q.QueryContext(ctx, `SELECT ts, provider, auth_index, model, session_id, parent_session_id, device_id,
		failed, latency_ms, COALESCE(ttft_ms, 0), input_tokens, output_tokens, reasoning_tokens,
		cache_read_tokens, cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens FROM usage_events`)
	if err != nil {
		return 0, fmt.Errorf("read events: %w", err)
	}
	defer closeRows(rows)
	n := 0
	for rows.Next() {
		var (
			e  event
			ts int64
			t  = &e.Tokens
		)
		if err := rows.Scan(&ts, &e.Provider, &e.AuthIndex, &e.Model, &e.SessionID, &e.ParentSessionID, &e.Device.ID,
			&e.Failed, &e.LatencyMs, &e.TTFTMs, &t.Input, &t.Output, &t.Reasoning,
			&t.CacheRead, &t.CacheWrite, &t.CacheWrite5m, &t.CacheWrite1h); err != nil {
			return n, fmt.Errorf("scan event: %w", err)
		}
		e.TS = time.UnixMilli(ts).UTC()
		fn(e)
		n++
	}
	if err := rows.Err(); err != nil {
		return n, fmt.Errorf("read events: %w", err)
	}
	return n, nil
}
