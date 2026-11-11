package ingest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return body
}

func TestParseClaudeUsagePayloadReadsLimitsFirst(t *testing.T) {
	observed := time.Date(2026, 11, 10, 23, 38, 0, 0, time.UTC)
	// The Fable item resets on the hour, while the legacy seven_day key resets a
	// fraction of a second earlier, so the reset time shows which source won.
	checkObs(t, parseClaudeUsagePayload(readFixture(t, "claude-usage-acct-B.json"), observed), []wantObs{
		{"five_hour", 0.07, time.Date(2026, 11, 11, 0, 50, 0, 270080000, time.UTC), "allowed", 18000},
		{"seven_day", 0.63, time.Date(2026, 11, 12, 12, 0, 0, 270100000, time.UTC), "allowed", 604800},
		{"seven_day_fable", 0, time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), "allowed", 604800},
	})
	checkObs(t, parseClaudeUsagePayload(readFixture(t, "claude-usage-cedar-acct-B.json"), observed), []wantObs{
		{"five_hour", 0.07, time.Date(2026, 11, 11, 0, 49, 59, 510195000, time.UTC), "allowed", 18000},
		{"seven_day", 0.63, time.Date(2026, 11, 12, 11, 59, 59, 510220000, time.UTC), "allowed", 604800},
		{"seven_day_fable", 0, time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), "allowed", 604800},
	})
}

func TestParseClaudeUsagePayloadWithoutASessionLimit(t *testing.T) {
	observed := time.Date(2026, 11, 10, 23, 38, 0, 0, time.UTC)
	for _, tc := range []struct {
		fixture     string
		weeklyNanos int
	}{{"claude-usage-acct-A.json", 659416000}, {"claude-usage-cedar-acct-A.json", 964604000}} {
		t.Run(tc.fixture, func(t *testing.T) {
			got := parseClaudeUsagePayload(readFixture(t, tc.fixture), observed)
			checkObs(t, got, []wantObs{
				{"five_hour", 0, time.Time{}, statusNoLimit, 18000},
				{"seven_day", 0, time.Date(2026, 11, 17, 0, 59, 59, tc.weeklyNanos, time.UTC), "allowed", 604800},
				{"seven_day_fable", 0, time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC), "allowed", 604800},
			})
		})
	}
	// limits[] decides even when a legacy five_hour key disagrees.
	got := parseClaudeUsagePayload([]byte(`{"five_hour":{"utilization":30},"limits":[{"kind":"weekly_all","percent":5}]}`), observed)
	checkObs(t, got, []wantObs{
		{"five_hour", 0, time.Time{}, statusNoLimit, 18000},
		{"seven_day", 0.05, time.Time{}, "allowed", 604800},
	})
}

func TestParseClaudeUsagePayloadRecordsAClosedSession(t *testing.T) {
	observed := time.Date(2026, 11, 10, 23, 38, 0, 0, time.UTC)
	reset := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	for name, body := range map[string]string{
		"no limits array":    `{"five_hour":null,"seven_day":{"utilization":5,"resets_at":"2026-11-12T12:00:00Z"}}`,
		"empty limits array": `{"five_hour":null,"seven_day":{"utilization":5,"resets_at":"2026-11-12T12:00:00Z"},"limits":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			checkObs(t, parseClaudeUsagePayload([]byte(body), observed), []wantObs{
				{"five_hour", 0, time.Time{}, "allowed", 18000},
				{"seven_day", 0.05, reset, "allowed", 604800},
			})
		})
	}
	// A payload without the key says nothing about the session.
	if got := parseClaudeUsagePayload([]byte(`{"seven_day":{"utilization":5}}`), observed); len(got) != 1 || got[0].Window != "seven_day" {
		t.Fatalf("no five_hour key: got %+v", got)
	}
}

func TestParseClaudeUsagePayloadNamesWindows(t *testing.T) {
	observed := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		body string
		want []wantObs
	}{
		{
			name: "Fable 5.1 and another scoped model",
			body: `{"limits":[
				{"kind":"weekly_scoped","percent":40,"resets_at":"2026-11-12T12:00:00Z","scope":{"model":{"display_name":"Fable 5.1"}}},
				{"kind":"weekly_scoped","percent":100,"resets_at":"2026-11-12T12:00:00Z","scope":{"model":{"display_name":"Opus 5"}}},
				{"kind":"weekly_scoped","percent":10,"resets_at":"2026-11-12T12:00:00Z","scope":{"model":null,"surface":"cowork"}},
				{"kind":"monthly_new","percent":10,"resets_at":"2026-11-12T12:00:00Z"}]}`,
			want: []wantObs{
				{"five_hour", 0, time.Time{}, statusNoLimit, 18000},
				{"seven_day_fable", 0.4, reset, "allowed", 604800},
				{"seven_day_opus_5", 1, reset, "rejected", 604800},
			},
		},
		{
			name: "legacy aliases of the Fable window",
			body: `{"seven_day_overage_included":{"utilization":25,"resets_at":"2026-11-12T12:00:00Z"}}`,
			want: []wantObs{{"seven_day_fable", 0.25, reset, "allowed", 604800}},
		},
		{
			name: "legacy keys yield to limits",
			body: `{"limits":[
				{"kind":"weekly_all","percent":63,"resets_at":"2026-11-12T12:00:00Z"},
				{"kind":"weekly_scoped","percent":64,"resets_at":"2026-11-12T12:00:00Z","scope":{"model":{"display_name":"Fable"}}}],
				"seven_day":{"utilization":10,"resets_at":"2026-11-13T12:00:00Z"},
				"iguana_necktie":{"utilization":11,"resets_at":"2026-11-13T12:00:00Z"},
				"seven_day_overage_included":{"utilization":12,"resets_at":"2026-11-13T12:00:00Z"}}`,
			want: []wantObs{
				{"five_hour", 0, time.Time{}, statusNoLimit, 18000},
				{"seven_day", 0.63, reset, "allowed", 604800},
				{"seven_day_fable", 0.64, reset, "allowed", 604800},
			},
		},
		{
			name: "two legacy Fable keys give one window",
			body: `{"iguana_necktie":{"utilization":11,"resets_at":"2026-11-12T12:00:00Z"},
				"seven_day_overage_included":{"utilization":11,"resets_at":"2026-11-12T12:00:00Z"}}`,
			want: []wantObs{{"seven_day_fable", 0.11, reset, "allowed", 604800}},
		},
		{
			name: "a session item outranks a null five_hour",
			body: `{"five_hour":null,"limits":[{"kind":"session","percent":30,"resets_at":"2026-11-12T12:00:00Z"}]}`,
			want: []wantObs{{"five_hour", 0.3, reset, "allowed", 18000}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkObs(t, parseClaudeUsagePayload([]byte(tc.body), observed), tc.want)
		})
	}
}

func TestFableWindowIsModelScoped(t *testing.T) {
	if accountWideWindow("seven_day_fable") {
		t.Fatal("seven_day_fable must not limit the whole account")
	}
}

// A poll that shows the account has no 5-hour limit hides any older 5-hour
// reading, even one at its limit, and a poll that shows no session open
// replaces it with an empty window.
func TestPollClearsAStaleFiveHourReading(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantFive   bool
	}{
		{"no 5-hour limit", string(readFixture(t, "claude-usage-acct-A.json")), false},
		{"no session open", `{"five_hour":null,"seven_day":{"utilization":5,"resets_at":"2026-11-06T00:00:00Z"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i, r, clock := newAPI(t)
			srv, _ := usageServer(t, http.StatusOK, tc.body)
			i.claudeUsageURL = srv.URL
			m := coreauth.NewManager(nil, nil, nil)
			idx := registerAuth(t, m, &coreauth.Auth{ID: "claude-a.json", FileName: "claude-a.json", Provider: "claude",
				Metadata: map[string]any{"access_token": "tok-a"}})
			i.AttachAuthManager(m)
			var published []QuotaObservation
			i.OnQuota(func(obs []QuotaObservation) { published = append(published, obs...) })

			rec := claudeRecord("old", clock.t.Add(-time.Hour), hdr(
				"Anthropic-Ratelimit-Unified-5h-Utilization", "1.0",
				"Anthropic-Ratelimit-Unified-5h-Status", "rejected",
				"Anthropic-Ratelimit-Unified-5h-Reset", strconv.FormatInt(clock.t.Add(2*time.Hour).Unix(), 10)))
			rec.AuthIndex, rec.Latency = idx, 0
			i.HandleUsage(context.Background(), rec)
			mustFlush(t, i)

			i.pollIdle(context.Background())
			mustFlush(t, i)

			latest, err := i.LatestQuota(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var five *QuotaObservation
			for k := range latest {
				if latest[k].AuthIndex == idx && latest[k].Window == "five_hour" {
					five = &latest[k]
				}
			}
			var resp currentResp
			call(t, r, http.MethodGet, "/api/quota/current", nil, &resp)
			current := currentWindow(t, resp, idx, "five_hour")
			if !tc.wantFive {
				if five != nil || current != nil {
					t.Fatalf("five_hour = %+v, current %+v; want none for an account without the limit", five, current)
				}
				if currentWindow(t, resp, idx, "seven_day") == nil {
					t.Fatalf("seven_day missing from %+v", resp)
				}
				// Subscribers holding the old reading see it end.
				last := published[len(published)-1]
				for _, o := range published {
					if o.Window == "five_hour" && o.ObservedAt.Equal(clock.t) {
						last = o
					}
				}
				if last.Window != "five_hour" || last.Utilization != 0 || last.Status != statusNoLimit {
					t.Fatalf("published %+v, want a no_limit five_hour", published)
				}
				return
			}
			if five == nil || five.Utilization != 0 || !five.ResetsAt.IsZero() || !five.ObservedAt.Equal(clock.t) {
				t.Fatalf("five_hour = %+v, want an empty window observed by the poll", five)
			}
			if current == nil || current.Status != "allowed" {
				t.Fatalf("current five_hour = %+v, want the empty window", current)
			}
		})
	}
}
