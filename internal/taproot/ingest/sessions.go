package ingest

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"
)

type sessionRollupKey struct {
	Hour             int64
	SessionID, Model string
}

// sessionRollup collects additions to usage_session_hourly.
type sessionRollup map[sessionRollupKey]*rollupDelta

func (s sessionRollup) add(e event) {
	k := sessionRollupKey{Hour: ms(e.TS) / hourMs, SessionID: e.SessionID, Model: e.Model}
	d := s[k]
	if d == nil {
		d = newRollupDelta()
		s[k] = d
	}
	d.addEvent(e)
}

func (s sessionRollup) apply(ctx context.Context, c *sql.Conn) error {
	w, err := prepareRollup(ctx, c, "usage_session_hourly", []string{"hour", "session_id", "model"})
	if err != nil {
		return err
	}
	defer w.close()
	for _, k := range keysInOrder(s, func(a, b sessionRollupKey) int {
		return cmp.Or(cmp.Compare(a.Hour, b.Hour), strings.Compare(a.SessionID, b.SessionID), strings.Compare(a.Model, b.Model))
	}) {
		if err := w.upsert(ctx, []any{k.Hour, k.SessionID, k.Model}, s[k]); err != nil {
			return err
		}
	}
	return nil
}

type sessionSpan struct{ first, last int64 }

// sessionSpans collects additions to usage_sessions.
type sessionSpans map[string]*sessionSpan

func (s sessionSpans) add(e event) {
	if e.SessionID == "" {
		return
	}
	t := ms(e.TS)
	if span := s[e.SessionID]; span == nil {
		s[e.SessionID] = &sessionSpan{first: t, last: t}
	} else {
		span.first, span.last = min(span.first, t), max(span.last, t)
	}
}

func (s sessionSpans) apply(ctx context.Context, c *sql.Conn) error {
	stmt, err := c.PrepareContext(ctx, `INSERT INTO usage_sessions (session_id, first_ts, last_ts) VALUES (?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET first_ts = MIN(first_ts, excluded.first_ts), last_ts = MAX(last_ts, excluded.last_ts)`)
	if err != nil {
		return fmt.Errorf("prepare session upsert: %w", err)
	}
	defer func() {
		if errClose := stmt.Close(); errClose != nil {
			log.WithError(errClose).Warn("taproot ingest: close statement")
		}
	}()
	for _, id := range keysInOrder(s, strings.Compare) {
		span := s[id]
		if _, err := stmt.ExecContext(ctx, id, span.first, span.last); err != nil {
			return fmt.Errorf("upsert session: %w", err)
		}
	}
	return nil
}

// sessionRef is a session and its last request in the queried range.
type sessionRef struct {
	id   string
	last int64
}

// sessionCursor resumes after the session with this last request and ID.
type sessionCursor struct {
	last int64
	id   string
}

func (c *sessionCursor) after(r sessionRef) bool {
	return c == nil || r.last < c.last || (r.last == c.last && r.id < c.id)
}

// listSessions returns up to n sessions with requests in [fromMs, toMs),
// ordered by their last request in the range, newest first, then by ID
// descending. A session whose last request is before toMs has its last
// request in range at last_ts; the few that span toMs are measured from their
// events.
func listSessions(ctx context.Context, q querier, fromMs, toMs int64, cur *sessionCursor, n int) ([]sessionRef, error) {
	ids, err := querySessionIDs(ctx, q, `SELECT session_id FROM usage_sessions WHERE last_ts >= ? AND first_ts < ?`, toMs, toMs)
	if err != nil {
		return nil, fmt.Errorf("query spanning sessions: %w", err)
	}
	var out []sessionRef
	for _, id := range ids {
		var last sql.NullInt64
		if err := q.QueryRowContext(ctx, `SELECT MAX(ts) FROM usage_events WHERE session_id = ? AND ts >= ? AND ts < ?`,
			id, fromMs, toMs).Scan(&last); err != nil {
			return nil, fmt.Errorf("read session %q: %w", id, err)
		}
		if r := (sessionRef{id: id, last: last.Int64}); last.Valid && cur.after(r) {
			out = append(out, r)
		}
	}

	where, args := "last_ts >= ? AND last_ts < ?", []any{fromMs, toMs}
	if cur != nil {
		where += " AND (last_ts < ? OR (last_ts = ? AND session_id < ?))"
		args = append(args, cur.last, cur.last, cur.id)
	}
	rows, err := q.QueryContext(ctx, `SELECT session_id, last_ts FROM usage_sessions WHERE `+where+`
		ORDER BY last_ts DESC, session_id DESC LIMIT ?`, append(args, n)...)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var r sessionRef
		if err := rows.Scan(&r.id, &r.last); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].last != out[b].last {
			return out[a].last > out[b].last
		}
		return out[a].id > out[b].id
	})
	return out[:min(n, len(out))], nil
}

func querySessionIDs(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
