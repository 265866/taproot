package ingest

import (
	"context"
	"net/http"
	"testing"
	"time"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/265866/taproot/sdk/cliproxy/usage"
)

type fakeLister struct{ auths []*coreauth.Auth }

func (f *fakeLister) List() []*coreauth.Auth { return f.auths }

type currentResp struct {
	Accounts []quotaAccountJSON `json:"accounts"`
}

func currentWindow(t *testing.T, resp currentResp, authIndex, window string) *quotaWindowJSON {
	t.Helper()
	for _, a := range resp.Accounts {
		if a.AuthIndex != authIndex {
			continue
		}
		for n := range a.Windows {
			if a.Windows[n].Window == window {
				return &a.Windows[n]
			}
		}
	}
	return nil
}

func TestHeadersObservedAt(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	for name, tc := range map[string]struct {
		r    usage.Record
		want time.Time
	}{
		"stream with first token": {usage.Record{RequestedAt: start, Stream: true, TTFT: 2 * time.Second, Latency: 5 * time.Minute}, start.Add(2 * time.Second)},
		"stream without ttft":     {usage.Record{RequestedAt: start, Stream: true, Latency: 5 * time.Minute}, start},
		"non-streaming":           {usage.Record{RequestedAt: start, Latency: 3 * time.Second}, start.Add(3 * time.Second)},
		"no request time":         {usage.Record{Latency: time.Second}, now},
	} {
		if got := headersObservedAt(tc.r, now); !got.Equal(tc.want) {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

// TestLongStreamDoesNotOverwriteNewerQuota covers a long stream whose headers
// arrived before a short request that finished first.
func TestLongStreamDoesNotOverwriteNewerQuota(t *testing.T) {
	i, r, _ := newAPI(t)
	t0 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	header := func(u string) http.Header { return hdr("Anthropic-Ratelimit-Unified-5h-Utilization", u) }
	long := claudeRecord("long", t0, header("0.50"))
	long.TTFT, long.Latency = time.Second, 300*time.Second
	short := claudeRecord("short", t0.Add(100*time.Second), header("0.55"))
	short.Stream, short.TTFT, short.Latency = false, 0, time.Second

	ctx := context.Background()
	i.HandleUsage(ctx, short)
	mustFlush(t, i)
	i.HandleUsage(ctx, long)
	mustFlush(t, i)

	var resp currentResp
	call(t, r, http.MethodGet, "/api/quota/current", nil, &resp)
	w := currentWindow(t, resp, "auth-a", "five_hour")
	if w == nil || w.Utilization != 0.55 {
		t.Fatalf("current five_hour = %+v, want the newer 0.55", w)
	}
}

// TestRemovedAccountsLeaveQuotaViews covers a credential deleted from the
// auth manager: it leaves the quota pool but keeps its label.
func TestRemovedAccountsLeaveQuotaViews(t *testing.T) {
	i, r, _ := newAPI(t)
	keep := &coreauth.Auth{ID: "keep.json", Provider: "claude", Label: "keep", Index: "idx-keep"}
	gone := &coreauth.Auth{ID: "gone.json", Provider: "claude", Label: "gone", Index: "idx-gone"}
	idle := &coreauth.Auth{ID: "idle.json", Provider: "codex", Label: "idle", Index: "idx-idle"}
	lister := &fakeLister{auths: []*coreauth.Auth{keep, gone, idle}}
	i.authMu.Lock()
	i.auths = lister
	i.authMu.Unlock()
	ctx := context.Background()
	if err := i.refreshAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	at := i.now()
	i.enqueueObservations([]observation{
		{AuthIndex: "idx-keep", Provider: "claude", Window: "five_hour", Utilization: 0.1, ObservedAt: at},
		{AuthIndex: "idx-gone", Provider: "claude", Window: "five_hour", Utilization: 0.9, ObservedAt: at},
	})
	mustFlush(t, i)

	lister.auths = []*coreauth.Auth{keep}
	if err := i.refreshAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	var resp currentResp
	call(t, r, http.MethodGet, "/api/quota/current", nil, &resp)
	if len(resp.Accounts) != 1 || resp.Accounts[0].AuthIndex != "idx-keep" {
		t.Fatalf("current after removal: %+v", resp.Accounts)
	}
	var series struct {
		Series []quotaSeriesJSON `json:"series"`
	}
	call(t, r, http.MethodGet, "/api/quota/series"+q("from", rfc(at.Add(-time.Hour)), "to", rfc(at.Add(time.Hour))), nil, &series)
	if len(series.Series) != 1 || series.Series[0].AuthIndex != "idx-keep" {
		t.Fatalf("series after removal: %+v", series.Series)
	}
	var accounts struct {
		Accounts []accountJSON `json:"accounts"`
	}
	call(t, r, http.MethodGet, "/api/accounts", nil, &accounts)
	removed := map[string]bool{}
	for _, a := range accounts.Accounts {
		removed[a.Label] = a.Removed
		if a.Removed != (a.RemovedAt != nil) {
			t.Fatalf("removedAt mismatch: %+v", a)
		}
	}
	if len(removed) != 3 || removed["keep"] || !removed["gone"] || !removed["idle"] {
		t.Fatalf("accounts after removal: %+v", accounts.Accounts)
	}

	// The attached manager has loaded, so an empty list removes the last one.
	lister.auths = nil
	if err := i.refreshAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, i, `SELECT COUNT(*) FROM accounts WHERE removed_at IS NULL`); n != 0 {
		t.Fatalf("live accounts after empty list: %d", n)
	}

	// A credential that comes back rejoins the pool.
	lister.auths = []*coreauth.Auth{keep, gone}
	if err := i.refreshAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	call(t, r, http.MethodGet, "/api/quota/current", nil, &resp)
	if w := currentWindow(t, resp, "idx-gone", "five_hour"); w == nil || w.Utilization != 0.9 {
		t.Fatalf("restored account window: %+v", resp.Accounts)
	}
}

// TestCodexAdditionalLimitHasOneName covers the three spellings of one Codex
// additional limit: HTTP namespace, websocket limit name, and the poller.
func TestCodexAdditionalLimitHasOneName(t *testing.T) {
	at := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	const want = "codex_gpt_5_3_codex_spark_primary"
	httpObs := parseQuotaHeaders("codex", hdr(
		"X-Codex-Bengalfox-Primary-Used-Percent", "40",
		"X-Codex-Bengalfox-Limit-Name", "GPT-5.3-Codex-Spark",
		"X-Codex-Primary-Used-Percent", "10",
	), at)
	wsObs := parseQuotaHeaders("codex", hdr(
		"X-Codex-Additional-GPT-5.3-Codex-Spark-Primary-Used-Percent", "41",
		"X-Codex-Additional-GPT-5.3-Codex-Spark-Limit-Name", "GPT-5.3-Codex-Spark",
		"X-Codex-Code-Review-Primary-Used-Percent", "5",
	), at)
	wsNoName := parseQuotaHeaders("codex", hdr("X-Codex-Additional-GPT-5.3-Codex-Spark-Primary-Used-Percent", "42"), at)
	pollObs := parseCodexUsagePayload([]byte(`{
		"rate_limit": {"primary_window": {"used_percent": 10}},
		"code_review_rate_limit": {"primary_window": {"used_percent": 5}},
		"additional_rate_limits": [{"limit_name": "GPT-5.3-Codex-Spark", "rate_limit": {"primary_window": {"used_percent": 43}}}]
	}`), at)

	names := func(obs []observation) map[string]bool {
		out := map[string]bool{}
		for _, o := range obs {
			out[o.Window] = true
		}
		return out
	}
	for label, got := range map[string]map[string]bool{"http": names(httpObs), "websocket": names(wsObs), "websocket without name": names(wsNoName), "poll": names(pollObs)} {
		if !got[want] {
			t.Errorf("%s windows %v, want %s", label, got, want)
		}
	}
	if !names(wsObs)["codex_code_review_primary"] || !names(pollObs)["codex_code_review_primary"] {
		t.Errorf("code review windows differ: ws %v poll %v", names(wsObs), names(pollObs))
	}
	if !names(httpObs)["codex_primary"] || !names(pollObs)["codex_primary"] {
		t.Errorf("primary windows: http %v poll %v", names(httpObs), names(pollObs))
	}
	// Without a limit name the short namespace is all there is.
	if got := names(parseQuotaHeaders("codex", hdr("X-Codex-Bengalfox-Secondary-Used-Percent", "1"), at)); !got["codex_bengalfox_secondary"] {
		t.Errorf("unnamed namespace windows %v", got)
	}

	for window, wide := range map[string]bool{
		"five_hour": true, "seven_day": true, "codex_primary": true, "codex_secondary": true,
		"seven_day_opus": false, "seven_day_sonnet": false, "five_hour_fable": false, want: false, "codex_code_review_primary": false,
	} {
		if accountWideWindow(window) != wide {
			t.Errorf("accountWide(%s) = %v, want %v", window, !wide, wide)
		}
	}
}

func TestUsageSummaryDaysFollowTimeZone(t *testing.T) {
	i, r, _ := newAPI(t)
	ctx := context.Background()
	for id, at := range map[string]time.Time{
		// America/Los_Angeles: Nov 1 2026 is 25 hours long (DST ends).
		"la-nov1-late":  time.Date(2026, 11, 2, 6, 0, 0, 0, time.UTC), // Nov 1 22:00 PST
		"la-nov2-early": time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC), // Nov 2 01:00 PST
		// Asia/Kolkata (UTC+5:30): the same UTC hour straddles midnight.
		"in-nov2-late":  time.Date(2026, 11, 2, 18, 15, 0, 0, time.UTC), // Nov 2 23:45 IST
		"in-nov3-early": time.Date(2026, 11, 2, 18, 45, 0, 0, time.UTC), // Nov 3 00:15 IST
	} {
		rec := claudeRecord(id, at, nil)
		i.HandleUsage(ctx, rec)
	}
	mustFlush(t, i)

	type summary struct {
		Totals  Totals `json:"totals"`
		Buckets []struct {
			T      string            `json:"t"`
			Groups map[string]Totals `json:"groups"`
		} `json:"buckets"`
	}
	counts := func(s summary) map[string]int64 {
		out := map[string]int64{}
		for _, b := range s.Buckets {
			for _, g := range b.Groups {
				out[b.T] += g.Requests
			}
		}
		return out
	}

	var la summary
	path := "/api/usage/summary" + q("from", "2026-10-31T07:00:00Z", "to", "2026-11-03T08:00:00Z", "bucket", "1d", "tz", "America/Los_Angeles")
	if code := call(t, r, http.MethodGet, path, nil, &la); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	var starts []string
	for _, b := range la.Buckets {
		starts = append(starts, b.T)
	}
	if len(starts) != 3 || starts[0] != "2026-10-31T07:00:00Z" || starts[1] != "2026-11-01T07:00:00Z" || starts[2] != "2026-11-02T08:00:00Z" {
		t.Fatalf("LA day starts %v", starts)
	}
	if got := counts(la); got["2026-11-01T07:00:00Z"] != 1 || got["2026-11-02T08:00:00Z"] != 3 || la.Totals.Requests != 4 {
		t.Fatalf("LA day counts %v totals %d", got, la.Totals.Requests)
	}

	var in summary
	path = "/api/usage/summary" + q("from", "2026-11-01T18:30:00Z", "to", "2026-11-03T18:30:00Z", "bucket", "1d", "tz", "Asia/Kolkata")
	call(t, r, http.MethodGet, path, nil, &in)
	if got := counts(in); got["2026-11-01T18:30:00Z"] != 3 || got["2026-11-02T18:30:00Z"] != 1 {
		t.Fatalf("Kolkata day counts %v", got)
	}

	var utc summary
	call(t, r, http.MethodGet, "/api/usage/summary"+q("from", "2026-11-01T00:00:00Z", "to", "2026-11-03T00:00:00Z", "bucket", "1d"), nil, &utc)
	if got := counts(utc); got["2026-11-02T00:00:00Z"] != 4 {
		t.Fatalf("UTC day counts %v", got)
	}
	expectBadRequest(t, r, http.MethodGet, "/api/usage/summary"+q("bucket", "1d", "tz", "Mars/Olympus"), nil)
	expectBadRequest(t, r, http.MethodGet, "/api/usage/summary"+q("bucket", "1d", "tz", "Local"), nil)
}
