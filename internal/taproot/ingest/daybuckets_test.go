package ingest

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/sdk/cliproxy/usage"
)

// requireDaysPartition checks that consecutive day buckets in loc advance, and
// that every instant in [from, to) belongs to the bucket that starts it.
func requireDaysPartition(t *testing.T, loc *time.Location, from, to time.Time) {
	t.Helper()
	s := &summaryBuilder{bucketMs: bucketSizes["1d"].Milliseconds(), loc: loc}
	toMs := ms(to)
	for b := s.bucketStart(ms(from)); b < toMs; {
		next := s.nextBucket(b)
		if next <= b {
			t.Fatalf("%s: day starting %s does not advance (next %s)", loc, time.UnixMilli(b).In(loc), time.UnixMilli(next).In(loc))
		}
		if got := s.bucketStart(b); got != b {
			t.Fatalf("%s: day starting %s maps to %s", loc, time.UnixMilli(b).In(loc), time.UnixMilli(got).In(loc))
		}
		if got := s.bucketStart(next - 1); got != b {
			t.Fatalf("%s: last instant of day %s maps to %s", loc, time.UnixMilli(b).In(loc), time.UnixMilli(got).In(loc))
		}
		if next-b < 22*hourMs || next-b > 48*hourMs {
			t.Fatalf("%s: day starting %s lasts %v", loc, time.UnixMilli(b).In(loc), time.Duration(next-b)*time.Millisecond)
		}
		b = next
	}
}

// Midnight does not exist on these dates; the clocks jump from 00:00 to 01:00.
var skippedMidnights = []struct {
	zone string
	day  time.Time
}{
	{"America/Santiago", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)},
	{"Atlantic/Azores", time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)},
	{"America/Havana", time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)},
}

func TestDayBucketsAdvanceOverSkippedMidnight(t *testing.T) {
	for _, c := range skippedMidnights {
		loc, err := time.LoadLocation(c.zone)
		if err != nil {
			t.Fatal(err)
		}
		requireDaysPartition(t, loc, c.day.AddDate(0, 0, -3), c.day.AddDate(0, 0, 3))
		s := &summaryBuilder{bucketMs: bucketSizes["1d"].Milliseconds(), loc: loc}
		// The day starts at the transition, whose local time is 01:00.
		start := time.UnixMilli(s.bucketStart(ms(c.day.Add(18 * time.Hour)))).In(loc)
		if y, m, d := start.Date(); y != c.day.Year() || m != c.day.Month() || d != c.day.Day() || start.Hour() != 1 {
			t.Fatalf("%s: day starts %s", c.zone, start)
		}
	}
}

// TestDayBucketsPartitionEveryZone walks two years of days in every zone the
// system knows.
func TestDayBucketsPartitionEveryZone(t *testing.T) {
	root := "/usr/share/zoneinfo"
	if _, err := os.Stat(filepath.Join(root, "UTC")); err != nil {
		t.Skipf("no zoneinfo directory: %v", err)
	}
	from, to := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 11, 1, 0, 0, 0, 0, time.UTC)
	zones := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, _ := filepath.Rel(root, path)
		if d.IsDir() {
			// posix/ and right/ repeat the zones, right/ with leap seconds.
			if name == "posix" || name == "right" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.Contains(name, ".") || !strings.ContainsAny(name[:1], "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return nil
		}
		loc, err := time.LoadLocation(filepath.ToSlash(name))
		if err != nil {
			return nil
		}
		zones++
		requireDaysPartition(t, loc, from, to)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if zones < 300 {
		t.Fatalf("checked only %d zones", zones)
	}
}

func TestUsageSummaryOnSkippedMidnight(t *testing.T) {
	i, r, _ := newAPI(t)
	day := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	i.HandleUsage(context.Background(), usage.Record{
		RequestID: "r1", Provider: "claude", Model: "claude-opus-5-5", AuthIndex: "auth-a",
		RequestedAt: day, Latency: time.Second, Detail: usage.Detail{InputTokens: 10, OutputTokens: 20},
	})
	mustFlush(t, i)

	var resp struct {
		Totals  Totals `json:"totals"`
		Buckets []struct {
			T      string            `json:"t"`
			Groups map[string]Totals `json:"groups"`
		} `json:"buckets"`
	}
	path := "/api/usage/summary" + q("from", rfc(day.Add(-36*time.Hour)), "to", rfc(day.Add(36*time.Hour)),
		"bucket", "1d", "groupBy", "model", "tz", "Atlantic/Azores")
	done := make(chan int, 1)
	go func() { done <- call(t, r, http.MethodGet, path, nil, &resp) }()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("summary did not return")
	}
	// Days start at 01:00Z while the zone is -01. 2026-03-29 starts at the
	// 01:00Z switch to +00, and 2026-03-30 at 00:00Z.
	want := []string{"2026-03-27T01:00:00Z", "2026-03-28T01:00:00Z", "2026-03-29T01:00:00Z", "2026-03-30T00:00:00Z"}
	if len(resp.Buckets) != len(want) {
		t.Fatalf("buckets %+v", resp.Buckets)
	}
	for n, b := range resp.Buckets {
		if b.T != want[n] {
			t.Fatalf("bucket %d starts %s, want %s", n, b.T, want[n])
		}
	}
	if resp.Totals.Requests != 1 || resp.Buckets[2].Groups["claude-opus-5-5"].Requests != 1 {
		t.Fatalf("totals %+v buckets %+v", resp.Totals, resp.Buckets)
	}
}
