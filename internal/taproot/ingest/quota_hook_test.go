package ingest

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/265866/taproot/sdk/cliproxy/usage"
)

func TestQuotaHookPublishesCommittedObservations(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	var got []QuotaObservation
	i.OnQuota(func(obs []QuotaObservation) { got = append(got, obs...) })

	reset := clock.t.Add(time.Hour).Truncate(time.Second)
	record := usage.Record{
		RequestID: "r1", Provider: "claude", AuthIndex: "auth-a", RequestedAt: clock.t, Latency: time.Second,
		ResponseHeaders: http.Header{
			"Anthropic-Ratelimit-Unified-5h-Utilization": {"1.0"},
			"Anthropic-Ratelimit-Unified-5h-Status":      {"rejected"},
			"Anthropic-Ratelimit-Unified-5h-Reset":       {strconv.FormatInt(reset.Unix(), 10)},
		},
	}
	i.HandleUsage(context.Background(), record)
	mustFlush(t, i)
	if len(got) != 1 {
		t.Fatalf("published %+v, want one observation", got)
	}
	o := got[0]
	if o.AuthIndex != "auth-a" || o.Window != "five_hour" || o.Utilization != 1 || o.Status != "rejected" || !o.ResetsAt.Equal(reset) || !o.AccountWide {
		t.Fatalf("observation = %+v", o)
	}

	// An unchanged reading is not stored again but is still published, so
	// subscribers see every response.
	record.RequestID = "r2"
	i.HandleUsage(context.Background(), record)
	mustFlush(t, i)
	if len(got) != 2 || queryInt(t, i, `SELECT COUNT(*) FROM quota_observations`) != 1 {
		t.Fatalf("published %d, stored %d", len(got), queryInt(t, i, `SELECT COUNT(*) FROM quota_observations`))
	}

	latest, err := i.LatestQuota(context.Background())
	if err != nil || len(latest) != 1 || latest[0].AuthIndex != "auth-a" || !latest[0].ResetsAt.Equal(reset) || latest[0].Provider != "claude" {
		t.Fatalf("latest = %+v, err = %v", latest, err)
	}
}

// TestQuotaAfterClockStepsBack stores readings from a clock that ran an hour
// ahead, then from the corrected clock.
func TestQuotaAfterClockStepsBack(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	corrected := clock.t
	reading := func(id string, at time.Time, util string) {
		r := claudeRecord(id, at, hdr("Anthropic-Ratelimit-Unified-5h-Utilization", util))
		r.Latency, r.TTFT = 0, 0
		i.HandleUsage(context.Background(), r)
		mustFlush(t, i)
	}
	clock.t = corrected.Add(time.Hour)
	reading("ahead", clock.t, "0.25")
	clock.t = corrected
	reading("corrected", clock.t, "0.5")
	// A reading older than the stored one still loses when the clock is right.
	clock.t = corrected.Add(time.Minute)
	reading("late", corrected.Add(-10*time.Minute), "0.75")

	if n := queryInt(t, i, `SELECT COUNT(*) FROM quota_observations`); n != 2 {
		t.Fatalf("stored %d rows, want 2", n)
	}
	latest, err := i.LatestQuota(context.Background())
	if err != nil || len(latest) != 1 || latest[0].Utilization != 0.5 || !latest[0].ObservedAt.Equal(corrected) {
		t.Fatalf("latest = %+v, err = %v", latest, err)
	}
	byAccount, err := i.currentWindows(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range byAccount {
		if len(a.Windows) != 1 || a.Windows[0].Utilization != 0.5 || a.Windows[0].ObservedAt != rfc(corrected) {
			t.Fatalf("current %+v", a.Windows)
		}
	}
}
