package ingest

import (
	"net/http"
	"testing"
	"time"
)

type wantObs struct {
	window   string
	util     float64
	resetsAt time.Time
	status   string
	period   int64
}

func checkObs(t *testing.T, got []observation, want []wantObs) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d observations %+v, want %d", len(got), got, len(want))
	}
	byWindow := map[string]observation{}
	for _, o := range got {
		byWindow[o.Window] = o
	}
	for _, w := range want {
		o, ok := byWindow[w.window]
		if !ok {
			t.Fatalf("missing window %q in %+v", w.window, got)
		}
		if diff := o.Utilization - w.util; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("%s utilization = %v, want %v", w.window, o.Utilization, w.util)
		}
		if !o.ResetsAt.Equal(w.resetsAt) {
			t.Errorf("%s resetsAt = %v, want %v", w.window, o.ResetsAt, w.resetsAt)
		}
		if o.Status != w.status {
			t.Errorf("%s status = %q, want %q", w.window, o.Status, w.status)
		}
		if o.PeriodSeconds != w.period {
			t.Errorf("%s period = %d, want %d", w.window, o.PeriodSeconds, w.period)
		}
	}
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		// Set the raw key so lowercase and canonical spellings are both exercised.
		h[kv[i]] = append(h[kv[i]], kv[i+1])
	}
	return h
}

func TestParseQuotaHeaders(t *testing.T) {
	observed := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	reset5h := time.Unix(1793650000, 0).UTC()
	reset7d := time.Unix(1793880000, 0).UTC()
	cases := []struct {
		name     string
		provider string
		headers  http.Header
		want     []wantObs
	}{
		{
			name:     "claude account with 5h and 7d windows (shape from real request logs)",
			provider: "claude",
			headers: hdr(
				"Anthropic-Ratelimit-Unified-Status", "allowed_warning",
				"Anthropic-Ratelimit-Unified-Representative-Claim", "seven_day",
				"Anthropic-Ratelimit-Unified-Reset", "1793880000",
				"Anthropic-Ratelimit-Unified-Fallback-Percentage", "0.5",
				"Anthropic-Ratelimit-Unified-5h-Status", "allowed",
				"Anthropic-Ratelimit-Unified-5h-Reset", "1793650000",
				"Anthropic-Ratelimit-Unified-5h-Utilization", "0.01",
				"Anthropic-Ratelimit-Unified-7d-Status", "allowed_warning",
				"Anthropic-Ratelimit-Unified-7d-Reset", "1793880000",
				"Anthropic-Ratelimit-Unified-7d-Utilization", "0.99",
				"Anthropic-Ratelimit-Unified-7d-Surpassed-Threshold", "0.75",
				"Anthropic-Ratelimit-Unified-Overage-Status", "rejected",
				"Anthropic-Ratelimit-Unified-Overage-Disabled-Reason", "org_level_disabled",
			),
			want: []wantObs{
				{"five_hour", 0.01, reset5h, "allowed", 18000},
				{"seven_day", 0.99, reset7d, "allowed_warning", 604800},
			},
		},
		{
			name:     "claude account without a 5h limit",
			provider: "claude",
			headers: hdr(
				"anthropic-ratelimit-unified-status", "allowed",
				"anthropic-ratelimit-unified-7d-status", "allowed",
				"anthropic-ratelimit-unified-7d-reset", "1793880000",
				"anthropic-ratelimit-unified-7d-utilization", "0.42",
			),
			want: []wantObs{{"seven_day", 0.42, reset7d, "allowed", 604800}},
		},
		{
			name:     "claude 7d rejected at utilization 1.0",
			provider: "claude",
			headers: hdr(
				"Anthropic-Ratelimit-Unified-Status", "rejected",
				"Anthropic-Ratelimit-Unified-5h-Status", "allowed",
				"Anthropic-Ratelimit-Unified-5h-Utilization", "0.0",
				"Anthropic-Ratelimit-Unified-5h-Reset", "1793650000",
				"Anthropic-Ratelimit-Unified-7d-Status", "rejected",
				"Anthropic-Ratelimit-Unified-7d-Utilization", "1.0",
				"Anthropic-Ratelimit-Unified-7d-Reset", "1793880000",
				"Retry-After", "3600",
			),
			want: []wantObs{
				{"five_hour", 0, reset5h, "allowed", 18000},
				{"seven_day", 1, reset7d, "rejected", 604800},
			},
		},
		{
			name:     "claude fable and per-model weekly claims",
			provider: "claude",
			headers: hdr(
				"Anthropic-Ratelimit-Unified-7d_oi-Utilization", "0.3",
				"Anthropic-Ratelimit-Unified-7d_oi-Status", "allowed",
				"Anthropic-Ratelimit-Unified-7d_opus-Utilization", "0.5",
			),
			want: []wantObs{
				{"seven_day_fable", 0.3, time.Time{}, "allowed", 604800},
				{"seven_day_opus", 0.5, time.Time{}, "", 604800},
			},
		},
		{
			name:     "claude malformed values are skipped",
			provider: "claude",
			headers: hdr(
				"Anthropic-Ratelimit-Unified-5h-Utilization", "NaN",
				"Anthropic-Ratelimit-Unified-7d-Utilization", "abc",
				"Anthropic-Ratelimit-Unified-7d-Reset", "1793880000",
			),
			want: nil,
		},
		{
			name:     "codex primary and secondary windows",
			provider: "codex",
			headers: hdr(
				"X-Codex-Plan-Type", "plus",
				"X-Codex-Active-Limit", "codex",
				"X-Codex-Primary-Used-Percent", "47",
				"X-Codex-Primary-Window-Minutes", "300",
				"X-Codex-Primary-Reset-At", "1793650000",
				"X-Codex-Secondary-Used-Percent", "100",
				"X-Codex-Secondary-Window-Minutes", "10080",
				"X-Codex-Secondary-Reset-After-Seconds", "7200",
				"X-Codex-Credits-Has-Credits", "false",
			),
			want: []wantObs{
				{"codex_primary", 0.47, reset5h, "allowed", 18000},
				{"codex_secondary", 1, observed.Add(2 * time.Hour), "rejected", 604800},
			},
		},
		{
			name:     "codex namespaced additional limit",
			provider: "codex",
			headers: hdr(
				"x-codex-bengalfox-primary-used-percent", "12.5",
				"x-codex-bengalfox-primary-window-minutes", "300",
				"x-codex-bengalfox-primary-reset-at", "1793650000",
			),
			want: []wantObs{{"codex_bengalfox_primary", 0.125, reset5h, "allowed", 18000}},
		},
		{
			name:     "codex headers on a claude response are ignored",
			provider: "claude",
			headers:  hdr("X-Codex-Primary-Used-Percent", "50"),
			want:     nil,
		},
		{
			name:     "other providers yield nothing",
			provider: "gemini",
			headers:  hdr("Anthropic-Ratelimit-Unified-5h-Utilization", "0.5"),
			want:     nil,
		},
		{
			name:     "no headers",
			provider: "claude",
			headers:  nil,
			want:     nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseQuotaHeaders(tc.provider, tc.headers, observed)
			checkObs(t, got, tc.want)
			for _, o := range got {
				if !o.ObservedAt.Equal(observed) || o.Source != "header" {
					t.Errorf("observation %+v: want observedAt %v and source header", o, observed)
				}
			}
		})
	}
}

func TestParseClaudeUsagePayload(t *testing.T) {
	observed := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	body := []byte(`{
		"five_hour": {"utilization": 37.0, "resets_at": "2026-11-02T15:00:00.123456+00:00"},
		"seven_day": {"utilization": 100.0, "resets_at": "2026-11-05T12:00:00+00:00"},
		"seven_day_opus": null,
		"seven_day_oauth_apps": {"utilization": 0, "resets_at": null},
		"iguana_necktie": {"utilization": 12, "resets_at": "2026-11-06T00:00:00Z"},
		"cloud_credits": {"limit_dollars": 50, "used_dollars": 5, "remaining_dollars": 45, "utilization": 10, "resets_at": null},
		"extra_usage": {"is_enabled": false, "utilization": null}
	}`)
	got := parseClaudeUsagePayload(body, observed)
	checkObs(t, got, []wantObs{
		{"five_hour", 0.37, time.Date(2026, 11, 2, 15, 0, 0, 123456000, time.UTC), "allowed", 18000},
		{"seven_day", 1, time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC), "rejected", 604800},
		{"seven_day_oauth_apps", 0, time.Time{}, "allowed", 604800},
		{"seven_day_fable", 0.12, time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), "allowed", 604800},
	})
	if got := parseClaudeUsagePayload([]byte(`not json`), observed); got != nil {
		t.Fatalf("malformed payload: got %+v", got)
	}
}

func TestParseCodexUsagePayload(t *testing.T) {
	observed := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	body := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"allowed": true, "limit_reached": false,
			"primary_window": {"used_percent": 25, "limit_window_seconds": 18000, "reset_after_seconds": 600, "reset_at": 1793650000},
			"secondary_window": {"used_percent": 60, "limit_window_seconds": 604800, "reset_after_seconds": 86400}
		},
		"code_review_rate_limit": null,
		"additional_rate_limits": [
			{"limit_name": "GPT-5.3-Codex-Spark", "metered_feature": "codex_bengalfox",
			 "rate_limit": {"primary_window": {"used_percent": 5, "limit_window_seconds": 18000, "reset_at": 1793650000}}}
		]
	}`)
	checkObs(t, parseCodexUsagePayload(body, observed), []wantObs{
		{"codex_primary", 0.25, time.Unix(1793650000, 0).UTC(), "allowed", 18000},
		{"codex_secondary", 0.6, observed.Add(24 * time.Hour), "allowed", 604800},
		{"codex_gpt_5_3_codex_spark_primary", 0.05, time.Unix(1793650000, 0).UTC(), "allowed", 18000},
	})
}
