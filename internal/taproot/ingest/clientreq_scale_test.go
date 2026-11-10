package ingest

import (
	"context"
	"database/sql"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
)

// TestClientRequestsAtScale times the summary and recent endpoints on 1M
// client requests over 30 days. It is slow, so it runs only with
// TAPROOT_CLIENT_REQUESTS_SCALE=1; set TMPDIR to a disk-backed directory.
func TestClientRequestsAtScale(t *testing.T) {
	if os.Getenv("TAPROOT_CLIENT_REQUESTS_SCALE") == "" {
		t.Skip("set TAPROOT_CLIENT_REQUESTS_SCALE=1 to run")
	}
	i, r, clock := newAPI(t)
	end := time.Date(2026, 11, 10, 15, 7, 0, 0, time.UTC)
	clock.t = end
	from := end.Add(-30 * 24 * time.Hour)
	rng := rand.New(rand.NewPCG(3, 5))
	const total, batch = 1_000_000, 20_000
	started := time.Now()
	for n := 0; n < total; n += batch {
		reqs := randomClientRequests(rng, from, end, batch)
		if err := store.WithImmediateTx(context.Background(), i.db.DB, func(c *sql.Conn) error {
			return insertClientRequests(context.Background(), c, reqs)
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("seeded %d rows in %s", total, time.Since(started).Round(time.Millisecond))

	median := func(name, path string) {
		var runs []time.Duration
		for range 5 {
			var out map[string]any
			s := time.Now()
			if code := call(t, r, http.MethodGet, path, nil, &out); code != http.StatusOK {
				t.Fatalf("%s: status %d", name, code)
			}
			runs = append(runs, time.Since(s))
			if tot, ok := out["totals"].(map[string]any); ok && tot["requests"].(float64) == 0 {
				t.Fatalf("%s: no requests counted", name)
			}
		}
		slices.Sort(runs)
		t.Logf("%-34s median %8s  range %s to %s", name, runs[2].Round(10*time.Microsecond), runs[0].Round(10*time.Microsecond), runs[4].Round(10*time.Microsecond))
	}
	window := func(d time.Duration, bucket, tz string) string {
		return "/api/requests/summary" + q("from", rfc(end.Add(-d)), "to", rfc(end), "bucket", bucket, "tz", tz)
	}
	median("summary 1h, 5m buckets", window(time.Hour, "5m", "America/Los_Angeles"))
	median("summary 24h, 1h buckets", window(24*time.Hour, "1h", "America/Los_Angeles"))
	median("summary 7d, 1d buckets (LA)", window(7*24*time.Hour, "1d", "America/Los_Angeles"))
	median("summary 30d, 1d buckets (LA)", window(30*24*time.Hour, "1d", "America/Los_Angeles"))
	median("summary 30d, 1d buckets (Kolkata)", window(30*24*time.Hour, "1d", "Asia/Kolkata"))
	median("recent failed, limit 5", "/api/requests/recent"+q("failed", "true"))
	median("recent, limit 100", "/api/requests/recent"+q("limit", "100"))

	// What one status evaluation reads: 30 days of quota observations for
	// 8 accounts with 4 windows each, a row every 5 minutes, and the counts
	// of the last 15 minutes and hour.
	if err := store.WithImmediateTx(context.Background(), i.db.DB, func(c *sql.Conn) error {
		for a := range 8 {
			for _, w := range []string{"five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet"} {
				for at := from; at.Before(end); at = at.Add(5 * time.Minute) {
					if _, err := c.ExecContext(context.Background(), `INSERT INTO quota_observations (auth_index, provider, window_name, utilization, resets_at, period_seconds, observed_at) VALUES (?, 'claude', ?, ?, ?, 604800, ?)`,
						string(rune('a'+a)), w, rng.Float64(), ms(at.Add(time.Hour)), ms(at)); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	timed := func(name string, fn func() error) {
		var runs []time.Duration
		for range 5 {
			s := time.Now()
			if err := fn(); err != nil {
				t.Fatal(err)
			}
			runs = append(runs, time.Since(s))
		}
		slices.Sort(runs)
		t.Logf("%-34s median %8s  range %s to %s", name, runs[2].Round(10*time.Microsecond), runs[0].Round(10*time.Microsecond), runs[4].Round(10*time.Microsecond))
	}
	ctx := context.Background()
	timed("status: counts, last 15 min", func() error { _, err := i.ClientRequestCounts(ctx, end.Add(-15*time.Minute), end); return err })
	timed("status: counts, last hour", func() error { _, err := i.ClientRequestCounts(ctx, end.Add(-time.Hour), end); return err })
	timed("status: latest quota", func() error { _, err := i.LatestQuota(ctx); return err })
	timed("status: quota history, 9 h", func() error { _, err := i.QuotaHistory(ctx, end.Add(-9*time.Hour), end); return err })

	// The same 30 days from raw rows, without the slot rollup.
	var runs []time.Duration
	for range 5 {
		s := time.Now()
		b := newRequestsBuilder(&summaryBuilder{bucketMs: bucketSizes["1d"].Milliseconds(), loc: time.UTC})
		if err := b.collect(context.Background(), i.db, ms(from), ms(end), false); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, time.Since(s))
	}
	slices.Sort(runs)
	t.Logf("%-34s median %8s  range %s to %s", "raw scan 30d (no rollup)", runs[2].Round(10*time.Microsecond), runs[0].Round(10*time.Microsecond), runs[4].Round(10*time.Microsecond))

	started = time.Now()
	if _, err := i.db.Exec(`DELETE FROM client_request_slots`); err != nil {
		t.Fatal(err)
	}
	if err := rebuildClientSlots(context.Background(), i.db.DB); err != nil {
		t.Fatal(err)
	}
	t.Logf("rebuilt slots from %d rows in %s", total, time.Since(started).Round(time.Millisecond))
	var slots int64
	if err := i.db.QueryRow(`SELECT COUNT(*) FROM client_request_slots`).Scan(&slots); err != nil {
		t.Fatal(err)
	}
	t.Logf("slot rows: %d", slots)
}
