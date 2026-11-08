package ingest

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/store"
	"github.com/265866/taproot/sdk/cliproxy/usage"
)

// seedMixed writes 600 varied requests over three days in several flushes, so
// rollup rows are merged across flushes. It returns the first request time.
func seedMixed(t *testing.T, i *Ingest) time.Time {
	t.Helper()
	base := time.Date(2026, 10, 30, 7, 13, 0, 0, time.UTC)
	devices := fakeDevices{}
	for n, id := range []string{"node:a", "node:b", "local"} {
		devices[fmt.Sprintf("dev-%d", n)] = access.Device{ID: id, Name: id, Via: access.ViaTailnet}
	}
	i.AttachDevices(devices)
	rng := rand.New(rand.NewPCG(1, 2))
	models := []string{"claude-opus-5-5", "claude-haiku-4-5", "gpt-6-sol", "unpriced-model"}
	ctx := context.Background()
	at := base
	for n := range 600 {
		at = at.Add(time.Duration(rng.IntN(13*60)) * time.Second)
		session := fmt.Sprintf("s%d", rng.IntN(25))
		parent := ""
		switch rng.IntN(6) {
		case 0:
			session = ""
		case 1:
			parent, session = session, session+":agent"
		}
		r := usage.Record{
			RequestID: fmt.Sprintf("r%d", n), TraceID: fmt.Sprintf("dev-%d", rng.IntN(4)),
			Provider: []string{"claude", "claude", "codex"}[rng.IntN(3)], Model: models[rng.IntN(len(models))],
			AuthIndex: fmt.Sprintf("auth-%d", rng.IntN(3)), SessionID: session, ParentSessionID: parent,
			RequestedAt: at, Latency: time.Duration(rng.IntN(90_000)) * time.Millisecond, Failed: rng.IntN(10) == 0,
			Detail: usage.Detail{InputTokens: int64(rng.IntN(5000)), OutputTokens: int64(rng.IntN(900)), CacheReadTokens: int64(rng.IntN(40_000))},
		}
		if rng.IntN(4) > 0 {
			r.TTFT = time.Duration(rng.IntN(9000)+1) * time.Millisecond
		}
		if w := int64(rng.IntN(3000)); rng.IntN(2) == 0 {
			r.Detail.CacheCreation5mTokens = &w
		}
		i.HandleUsage(ctx, r)
		if n%97 == 0 {
			mustFlush(t, i)
		}
	}
	mustFlush(t, i)
	return base
}

func collectSummary(t *testing.T, i *Ingest, groupBy, bucket string, fromMs, toMs int64, useRollup bool) map[int64]map[string]Totals {
	t.Helper()
	s := &summaryBuilder{bucketMs: bucketSizes[bucket].Milliseconds(), groupBy: groupBy, buckets: map[int64]map[string]*agg{}, total: newAgg()}
	if err := s.collect(context.Background(), i.db, fromMs, toMs, useRollup); err != nil {
		t.Fatal(err)
	}
	pb := newPriceBook(seedPrices)
	out := map[int64]map[string]Totals{-1: {"total": s.total.totals(pb)}}
	for b, groups := range s.buckets {
		out[b] = map[string]Totals{}
		for k, a := range groups {
			out[b][k] = a.totals(pb)
		}
	}
	return out
}

func sameTotals(a, b Totals) bool {
	ptr := func(x, y *float64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	costA, costB := a.CostUSD, b.CostUSD
	a.CostUSD, b.CostUSD = 0, 0
	return near(costA, costB) && ptr(a.LatencyP50Ms, b.LatencyP50Ms) && ptr(a.LatencyP95Ms, b.LatencyP95Ms) &&
		ptr(a.TTFTP50Ms, b.TTFTP50Ms) && ptr(a.TTFTP95Ms, b.TTFTP95Ms) &&
		a.Requests == b.Requests && a.Failures == b.Failures && a.InputTokens == b.InputTokens && a.OutputTokens == b.OutputTokens &&
		a.ReasoningTokens == b.ReasoningTokens && a.CacheReadTokens == b.CacheReadTokens && a.CacheWriteTokens == b.CacheWriteTokens &&
		a.CacheWrite5mTokens == b.CacheWrite5mTokens && a.CacheWrite1hTokens == b.CacheWrite1hTokens
}

// requireRollupMatchesRaw checks every grouping reads the same totals,
// percentiles included, from the rollups as from raw events.
func requireRollupMatchesRaw(t *testing.T, i *Ingest, base time.Time, requests int64) {
	t.Helper()
	fromMs, toMs := ms(base.Add(-17*time.Minute)), ms(base.Add(80*time.Hour+29*time.Minute))
	for groupBy := range groupByValues {
		for _, bucket := range []string{"1h", "1d"} {
			raw := collectSummary(t, i, groupBy, bucket, fromMs, toMs, false)
			rolled := collectSummary(t, i, groupBy, bucket, fromMs, toMs, true)
			if raw[-1]["total"].Requests != requests {
				t.Fatalf("%s/%s: raw total %+v, want %d requests", groupBy, bucket, raw[-1]["total"], requests)
			}
			if len(raw) != len(rolled) {
				t.Fatalf("%s/%s: %d buckets from rollup, %d from events", groupBy, bucket, len(rolled), len(raw))
			}
			for b, groups := range raw {
				if len(groups) != len(rolled[b]) {
					t.Fatalf("%s/%s bucket %d: groups %d from rollup, %d from events", groupBy, bucket, b, len(rolled[b]), len(groups))
				}
				for k, want := range groups {
					if got := rolled[b][k]; !sameTotals(got, want) {
						t.Fatalf("%s/%s bucket %d group %q: rollup %+v, events %+v", groupBy, bucket, b, k, got, want)
					}
				}
			}
		}
	}
}

func TestRollupSummariesMatchRawEvents(t *testing.T) {
	i, _ := newTestIngest(t, Config{})
	requireRollupMatchesRaw(t, i, seedMixed(t, i), 600)
}

var derivedTableNames = func() []string {
	var names []string
	for _, t := range derivedTables() {
		names = append(names, t.name)
	}
	return names
}()

// dumpDerived returns every derived table's rows, sorted.
func dumpDerived(t *testing.T, db *store.DB) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, table := range derivedTableNames {
		rows, err := db.QueryContext(context.Background(), `SELECT * FROM `+table)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for k := range vals {
				ptrs[k] = &vals[k]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			out[table] = append(out[table], fmt.Sprint(vals...))
		}
		closeRows(rows)
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(out[table])
	}
	return out
}

func requireSameDump(t *testing.T, stage string, got, want map[string][]string) {
	t.Helper()
	for _, table := range derivedTableNames {
		if len(want[table]) == 0 {
			t.Fatalf("%s: the writer left %s empty; the case tests nothing", stage, table)
		}
		if fmt.Sprint(got[table]) != fmt.Sprint(want[table]) {
			t.Errorf("%s: %s has %d rows, the writer built %d, or their values differ", stage, table, len(got[table]), len(want[table]))
		}
	}
}

// TestStartupRebuildsEmptyDerivedTables opens a database whose derived tables
// are missing, as on the first start of a release that adds them or after
// restoring an older backup, and expects ingest's startup to rebuild exactly
// what the writer built. A table that already has rows is left alone.
func TestStartupRebuildsEmptyDerivedTables(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	open := func() *store.DB {
		t.Helper()
		db, err := store.Open(ctx, store.Config{}, dir)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	start := func(db *store.DB) *Ingest {
		t.Helper()
		i, err := New(Config{}, db)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	db := open()
	base := seedMixed(t, start(db))
	want := dumpDerived(t, db)
	for _, table := range derivedTableNames {
		if _, err := db.ExecContext(ctx, `DROP TABLE `+table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = open()
	t.Cleanup(func() { _ = db.Close() })
	for table, rows := range dumpDerived(t, db) {
		t.Fatalf("store.Open recreated %s with %d rows, want it empty", table, len(rows))
	}
	i := start(db)
	requireSameDump(t, "rebuilt", dumpDerived(t, db), want)
	requireRollupMatchesRaw(t, i, base, 600)

	if _, err := db.ExecContext(ctx, `DELETE FROM usage_sessions`); err != nil {
		t.Fatal(err)
	}
	start(db)
	requireSameDump(t, "rebuilt one table", dumpDerived(t, db), want)
	start(db)
	requireSameDump(t, "restarted", dumpDerived(t, db), want)
}

// TestListSessionsMatchesEventGrouping compares the sessions list with
// grouping the events themselves, for ranges that cut sessions on both
// sides, and pages through each with the cursor.
func TestListSessionsMatchesEventGrouping(t *testing.T) {
	i, _ := newTestIngest(t, Config{})
	base := seedMixed(t, i)
	ctx := context.Background()
	reference := func(fromMs, toMs int64) []sessionRef {
		rows, err := i.db.QueryContext(ctx, `SELECT session_id, MAX(ts) FROM usage_events
			WHERE ts >= ? AND ts < ? AND session_id <> '' GROUP BY session_id ORDER BY MAX(ts) DESC, session_id DESC`, fromMs, toMs)
		if err != nil {
			t.Fatal(err)
		}
		defer closeRows(rows)
		var out []sessionRef
		for rows.Next() {
			var r sessionRef
			if err := rows.Scan(&r.id, &r.last); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}
	spanning := 0
	for _, span := range [][2]time.Duration{{0, 90 * time.Hour}, {5 * time.Hour, 9 * time.Hour}, {30 * time.Hour, 31 * time.Hour}, {-time.Hour, 2 * time.Hour}, {50 * time.Hour, 200 * time.Hour}} {
		fromMs, toMs := ms(base.Add(span[0])), ms(base.Add(span[1]))
		want := reference(fromMs, toMs)
		if len(want) == 0 {
			t.Errorf("range %v has no sessions; the case tests nothing", span)
		}
		spanning += int(queryInt(t, i, `SELECT COUNT(*) FROM usage_sessions WHERE first_ts < ? AND last_ts >= ?`, toMs, toMs))
		var got []sessionRef
		var cur *sessionCursor
		for {
			page, err := listSessions(ctx, i.db, fromMs, toMs, cur, 4)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, page...)
			if len(page) < 4 {
				break
			}
			cur = &sessionCursor{last: page[3].last, id: page[3].id}
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("sessions in %v:\n got %v\nwant %v", span, got, want)
		}
	}
	if spanning == 0 {
		t.Error("no range ends inside a session; the spanning path is untested")
	}
}

// TestTTLEstimateFollowsEachSession interleaves two sessions: each session's
// re-write follows its own previous request, not the row before it in time.
func TestTTLEstimateFollowsEachSession(t *testing.T) {
	i, r, _ := newAPI(t)
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	i.AttachDevices(fakeDevices{"t": laptop})
	ctx := context.Background()
	w := int64(1000)
	for n, step := range []struct {
		session string
		at      time.Duration
	}{{"a", 0}, {"b", time.Minute}, {"b", 7 * time.Minute}, {"a", 10 * time.Minute}, {"a", 11 * time.Minute}, {"b", 70 * time.Minute}} {
		i.HandleUsage(ctx, usage.Record{
			RequestID: fmt.Sprintf("r%d", n), TraceID: "t", Provider: "claude", Model: "claude-opus-5-5", AuthIndex: "auth-a",
			SessionID: step.session, RequestedAt: base.Add(step.at), Detail: usage.Detail{CacheCreation5mTokens: &w},
		})
	}
	mustFlush(t, i)
	var resp struct{ Devices []deviceJSON }
	call(t, r, "GET", "/api/usage/devices"+q("from", rfc(base), "to", rfc(base.Add(2*time.Hour))), nil, &resp)
	// a: 0 -> 10m is a re-write; b: 1m -> 7m is one, 7m -> 70m is too long.
	if len(resp.Devices) != 1 || resp.Devices[0].Main.Estimate.RewriteRequests != 2 || resp.Devices[0].Main.Estimate.RewriteTokens != 2000 {
		t.Fatalf("devices = %+v, want two re-writes of 1000 tokens", resp.Devices)
	}
}

// TestCurrentWindowsIsLatestPerWindow checks the loose index scan against
// grouping every observation, with and without an account filter, and that
// removed accounts stay out.
func TestCurrentWindowsIsLatestPerWindow(t *testing.T) {
	i, _ := newTestIngest(t, Config{})
	ctx := context.Background()
	rng := rand.New(rand.NewPCG(3, 4))
	for n := range 400 {
		auth := fmt.Sprintf("auth-%d", rng.IntN(5))
		if _, err := i.db.ExecContext(ctx, `INSERT OR IGNORE INTO quota_observations
			(auth_index, provider, window_name, utilization, resets_at, status, period_seconds, observed_at)
			VALUES (?, 'claude', ?, ?, ?, 'allowed', 18000, ?)`,
			auth, []string{"five_hour", "seven_day", "seven_day_opus"}[rng.IntN(3)], rng.Float64(),
			[]any{nil, int64(n) * 1000}[rng.IntN(2)], rng.Int64N(1_000_000)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := i.db.ExecContext(ctx, `INSERT INTO accounts (auth_index, removed_at) VALUES ('auth-3', 1), ('auth-1', NULL)`); err != nil {
		t.Fatal(err)
	}
	reference := func(where string, args ...any) string {
		rows, err := i.db.QueryContext(ctx, `SELECT auth_index, window_name, utilization, resets_at, MAX(observed_at)
			FROM quota_observations WHERE auth_index NOT IN (SELECT auth_index FROM accounts WHERE removed_at IS NOT NULL)`+where+`
			GROUP BY auth_index, window_name ORDER BY auth_index, window_name`, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer closeRows(rows)
		var out []string
		for rows.Next() {
			var (
				auth, window string
				util         float64
				resets       *int64
				observed     int64
			)
			if err := rows.Scan(&auth, &window, &util, &resets, &observed); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%s %s %v %s %s", auth, window, util, deref(formatNullableMs(resets)), formatMs(observed)))
		}
		return fmt.Sprint(out)
	}
	current := func(authIndex string) string {
		byAccount, err := i.currentWindows(ctx, authIndex)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range byAccount {
			for _, w := range a.Windows {
				out = append(out, fmt.Sprintf("%s %s %v %s %s", a.AuthIndex, w.Window, w.Utilization, deref(w.ResetsAt), w.ObservedAt))
			}
		}
		sort.Strings(out)
		return fmt.Sprint(out)
	}
	if got, want := current(""), reference(""); got != want || len(want) < 100 {
		t.Errorf("all accounts:\n got %s\nwant %s", got, want)
	}
	for _, auth := range []string{"auth-1", "auth-3", "missing"} {
		if got, want := current(auth), reference(" AND auth_index = ?", auth); got != want {
			t.Errorf("%s:\n got %s\nwant %s", auth, got, want)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

// TestStartupRebuildsDerivedTablesAnOlderReleaseLeftBehind covers a rollback:
// the previous release records events without maintaining the newer derived
// tables, so the next start must notice the counts apart and rebuild them.
func TestStartupRebuildsDerivedTablesAnOlderReleaseLeftBehind(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, store.Config{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	start := func() *Ingest {
		t.Helper()
		i, err := New(Config{}, db)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	base := seedMixed(t, start())

	// What the previous release writes: an event in a new session, and
	// nothing in the derived tables it does not know.
	if _, err := db.ExecContext(ctx, `INSERT INTO usage_events (event_key, request_id, ts, provider, auth_index, model,
		session_id, failed, latency_ms, input_tokens, output_tokens)
		SELECT 'rollback-1', 'rollback-1', ts + 1, provider, auth_index, model, 'rollback-session', 0, 1234, 10, 20
		FROM usage_events ORDER BY ts LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	i := start()
	afterRollback := dumpDerived(t, db)
	requireRollupMatchesRaw(t, i, base, 601)
	if n := queryInt(t, i, `SELECT COUNT(*) FROM usage_sessions WHERE session_id = 'rollback-session'`); n != 1 {
		t.Fatalf("usage_sessions has %d rows for the session recorded during the rollback, want 1", n)
	}

	for _, table := range derivedTableNames {
		if _, err := db.ExecContext(ctx, `DELETE FROM `+table); err != nil {
			t.Fatal(err)
		}
	}
	start()
	requireSameDump(t, "stale rebuild vs empty rebuild", afterRollback, dumpDerived(t, db))
}
