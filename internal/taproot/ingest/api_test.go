package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/sdk/cliproxy/usage"
	"github.com/gin-gonic/gin"
)

func newAPI(t *testing.T) (*Ingest, *gin.Engine, *fakeClock) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	i, clock := newTestIngest(t, Config{})
	r := gin.New()
	i.Routes(r.Group("/api"))
	return i, r, clock
}

func call(t *testing.T, r http.Handler, method, path string, body any, out any) int {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, w.Body.String(), err)
		}
	}
	return w.Code
}

func expectBadRequest(t *testing.T, r http.Handler, method, path string, body any) {
	t.Helper()
	var e apiErrorBody
	if code := call(t, r, method, path, body, &e); code != http.StatusBadRequest || e.Error.Code != codeBadRequest || e.Error.Message == "" {
		t.Errorf("%s %s: status %d error %+v, want 400 bad_request", method, path, code, e)
	}
}

func q(params ...string) string {
	v := url.Values{}
	for n := 0; n+1 < len(params); n += 2 {
		v.Set(params[n], params[n+1])
	}
	return "?" + v.Encode()
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// seedUsage writes three Claude requests on two accounts in one session and
// one failed Codex request, all inside 10:00-12:00 UTC on 2026-11-02.
func seedUsage(t *testing.T, i *Ingest) time.Time {
	t.Helper()
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()
	quota := hdr(
		"Anthropic-Ratelimit-Unified-5h-Utilization", "0.25",
		"Anthropic-Ratelimit-Unified-5h-Reset", "1793617200", // 2026-11-02T11:00:00Z
		"Anthropic-Ratelimit-Unified-5h-Status", "allowed",
	)
	for n, at := range []time.Duration{10 * time.Minute, 50 * time.Minute, 70 * time.Minute} {
		r := claudeRecord("c"+string(rune('1'+n)), base.Add(at), quota)
		if n == 1 {
			r.AuthIndex = "auth-b"
		}
		i.HandleUsage(ctx, r)
	}
	i.HandleUsage(ctx, usage.Record{
		RequestID: "x1", Provider: "codex", Model: "gpt-6-astra", AuthIndex: "auth-c", SessionID: "session-2",
		RequestedAt: base.Add(80 * time.Minute), Latency: time.Second, Failed: true, Fail: usage.Failure{StatusCode: 500},
		Detail: usage.Detail{InputTokens: 2000, OutputTokens: 100},
	})
	mustFlush(t, i)
	return base
}

func TestUsageSummary(t *testing.T) {
	i, r, _ := newAPI(t)
	base := seedUsage(t, i)

	var resp struct {
		Totals  Totals `json:"totals"`
		Buckets []struct {
			T      string            `json:"t"`
			Groups map[string]Totals `json:"groups"`
		} `json:"buckets"`
	}
	path := "/api/usage/summary" + q("from", rfc(base), "to", rfc(base.Add(2*time.Hour)), "bucket", "1h", "groupBy", "model")
	if code := call(t, r, http.MethodGet, path, nil, &resp); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if resp.Totals.Requests != 4 || resp.Totals.Failures != 1 || len(resp.Buckets) != 2 {
		t.Fatalf("totals %+v buckets %d", resp.Totals, len(resp.Buckets))
	}
	if resp.Buckets[0].T != "2026-11-02T10:00:00Z" || resp.Buckets[0].Groups["claude-opus-5-5"].Requests != 2 {
		t.Fatalf("first bucket %+v", resp.Buckets[0])
	}
	if resp.Buckets[1].Groups["gpt-6-astra"].Failures != 1 {
		t.Fatalf("second bucket %+v", resp.Buckets[1])
	}
	// Three Claude events at seeded opus-5-5 prices plus one astra event.
	claude := 3 * (10*4 + 200*20 + 5000*0.2 + 100*5 + 200*8) / 1e6
	astra := (2000*10 + 100*50) / 1e6
	if math.Abs(resp.Totals.CostUSD-(claude+astra)) > 1e-9 {
		t.Fatalf("cost %v, want %v", resp.Totals.CostUSD, claude+astra)
	}
	if resp.Totals.TTFTP50Ms == nil || math.Abs(*resp.Totals.TTFTP50Ms-400) > 20 || resp.Totals.LatencyP95Ms == nil {
		t.Fatalf("percentiles %+v", resp.Totals)
	}

	// Edges that are not hour-aligned read raw events; whole hours read the
	// rollup. Both paths must agree with an all-raw 5m query.
	from, to := base.Add(30*time.Minute), base.Add(75*time.Minute)
	var mixed, raw struct {
		Totals Totals `json:"totals"`
	}
	call(t, r, http.MethodGet, "/api/usage/summary"+q("from", rfc(from), "to", rfc(to), "bucket", "1h"), nil, &mixed)
	call(t, r, http.MethodGet, "/api/usage/summary"+q("from", rfc(from), "to", rfc(to), "bucket", "5m"), nil, &raw)
	mixedJSON, _ := json.Marshal(mixed.Totals)
	rawJSON, _ := json.Marshal(raw.Totals)
	if mixed.Totals.Requests != 2 || string(mixedJSON) != string(rawJSON) {
		t.Fatalf("mixed %s raw %s", mixedJSON, rawJSON)
	}
	// Wider than the rollup's hour boundaries.
	call(t, r, http.MethodGet, "/api/usage/summary"+q("from", rfc(base.Add(-90*time.Minute)), "to", rfc(base.Add(150*time.Minute)), "bucket", "1d", "groupBy", "account"), nil, &resp)
	if resp.Totals.Requests != 4 || len(resp.Buckets) != 1 || resp.Buckets[0].Groups["auth-b"].Requests != 1 {
		t.Fatalf("1d by account %+v", resp)
	}
	call(t, r, http.MethodGet, "/api/usage/summary"+q("from", rfc(base), "to", rfc(base.Add(2*time.Hour)), "groupBy", "session"), nil, &resp)
	if resp.Buckets[0].Groups["session-1"].Requests != 2 || resp.Buckets[1].Groups["session-2"].Requests != 1 {
		t.Fatalf("by session %+v", resp.Buckets)
	}
}

func TestUsageSummaryEmptyAndBadParams(t *testing.T) {
	_, r, clock := newAPI(t)
	var resp struct {
		Totals  Totals `json:"totals"`
		Buckets []struct {
			Groups map[string]Totals `json:"groups"`
		} `json:"buckets"`
	}
	if code := call(t, r, http.MethodGet, "/api/usage/summary", nil, &resp); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if resp.Totals.Requests != 0 || resp.Totals.LatencyP50Ms != nil || len(resp.Buckets) != 24 || len(resp.Buckets[0].Groups) != 0 {
		t.Fatalf("empty default range: %+v (buckets %d)", resp.Totals, len(resp.Buckets))
	}
	now := clock.t
	for _, path := range []string{
		"/api/usage/summary" + q("bucket", "2h"),
		"/api/usage/summary" + q("groupBy", "colour"),
		"/api/usage/summary" + q("from", "yesterday"),
		"/api/usage/summary" + q("to", "2026-13-01T00:00:00Z"),
		"/api/usage/summary" + q("from", rfc(now), "to", rfc(now)),
		"/api/usage/summary" + q("from", rfc(now.AddDate(-1, 0, 0)), "to", rfc(now), "bucket", "5m"),
	} {
		expectBadRequest(t, r, http.MethodGet, path, nil)
	}
}

func TestUsageEvents(t *testing.T) {
	i, r, _ := newAPI(t)
	base := seedUsage(t, i)
	rangeQ := []string{"from", rfc(base), "to", rfc(base.Add(2 * time.Hour))}

	type page struct {
		Events     []UsageEvent `json:"events"`
		NextCursor *string      `json:"nextCursor"`
	}
	var seen []string
	cursor := ""
	for n := 0; n < 10; n++ {
		params := append(append([]string{}, rangeQ...), "limit", "3")
		if cursor != "" {
			params = append(params, "cursor", cursor)
		}
		var p page
		if code := call(t, r, http.MethodGet, "/api/usage/events"+q(params...), nil, &p); code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		for _, e := range p.Events {
			seen = append(seen, e.RequestID)
		}
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	if strings.Join(seen, ",") != "x1,c3,c2,c1" {
		t.Fatalf("paged order %v, want newest first without repeats", seen)
	}

	var p page
	call(t, r, http.MethodGet, "/api/usage/events"+q(append(rangeQ, "failed", "true")...), nil, &p)
	if len(p.Events) != 1 || p.Events[0].StatusCode != 500 || !p.Events[0].Failed || p.Events[0].TTFTMs != nil {
		t.Fatalf("failed filter %+v", p.Events)
	}
	call(t, r, http.MethodGet, "/api/usage/events"+q(append(rangeQ, "authIndex", "auth-b", "model", "claude-opus-5-5")...), nil, &p)
	if len(p.Events) != 1 || p.Events[0].RequestID != "c2" || !p.Events[0].Priced || p.Events[0].CostUSD <= 0 {
		t.Fatalf("authIndex filter %+v", p.Events)
	}
	call(t, r, http.MethodGet, "/api/usage/events"+q(append(rangeQ, "sessionId", "nope")...), nil, &p)
	if len(p.Events) != 0 || p.NextCursor != nil {
		t.Fatalf("empty filter %+v", p)
	}
	for _, bad := range [][]string{{"limit", "0"}, {"limit", "5000"}, {"cursor", "%%%"}, {"failed", "maybe"}} {
		expectBadRequest(t, r, http.MethodGet, "/api/usage/events"+q(append(append([]string{}, rangeQ...), bad...)...), nil)
	}
}

func TestUsageSessions(t *testing.T) {
	i, r, _ := newAPI(t)
	base := seedUsage(t, i)
	rangeQ := []string{"from", rfc(base), "to", rfc(base.Add(2 * time.Hour))}
	type page struct {
		Sessions []struct {
			SessionID       string   `json:"sessionId"`
			FirstAt         string   `json:"firstAt"`
			LastAt          string   `json:"lastAt"`
			Requests        int64    `json:"requests"`
			Accounts        []string `json:"accounts"`
			Models          []string `json:"models"`
			Totals          Totals   `json:"totals"`
			AccountSwitches int      `json:"accountSwitches"`
		} `json:"sessions"`
		NextCursor *string `json:"nextCursor"`
	}
	var p page
	if code := call(t, r, http.MethodGet, "/api/usage/sessions"+q(append(rangeQ, "limit", "1")...), nil, &p); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(p.Sessions) != 1 || p.Sessions[0].SessionID != "session-2" || p.NextCursor == nil {
		t.Fatalf("first page %+v", p)
	}
	var p2 page
	call(t, r, http.MethodGet, "/api/usage/sessions"+q(append(rangeQ, "limit", "1", "cursor", *p.NextCursor)...), nil, &p2)
	if len(p2.Sessions) != 1 || p2.NextCursor != nil {
		t.Fatalf("second page %+v", p2)
	}
	s := p2.Sessions[0]
	// c1 on auth-a, c2 on auth-b, c3 on auth-a: two switches.
	if s.SessionID != "session-1" || s.Requests != 3 || s.AccountSwitches != 2 || strings.Join(s.Accounts, ",") != "auth-a,auth-b" ||
		s.FirstAt != "2026-11-02T10:10:00Z" || s.LastAt != "2026-11-02T11:10:00Z" || s.Totals.Requests != 3 || s.Totals.CostUSD <= 0 {
		t.Fatalf("session-1 %+v", s)
	}

	call(t, r, http.MethodGet, "/api/usage/sessions"+q("from", rfc(base.Add(-48*time.Hour)), "to", rfc(base.Add(-24*time.Hour))), nil, &p)
	if len(p.Sessions) != 0 || p.NextCursor != nil {
		t.Fatalf("empty range %+v", p)
	}
	expectBadRequest(t, r, http.MethodGet, "/api/usage/sessions"+q(append(rangeQ, "cursor", "!!")...), nil)
	expectBadRequest(t, r, http.MethodGet, "/api/usage/sessions"+q(append(rangeQ, "limit", "x")...), nil)
}

func TestQuotaRoutes(t *testing.T) {
	i, r, _ := newAPI(t)
	var current struct {
		Accounts []quotaAccountJSON `json:"accounts"`
	}
	if code := call(t, r, http.MethodGet, "/api/quota/current", nil, &current); code != http.StatusOK || len(current.Accounts) != 0 {
		t.Fatalf("empty current: %d %+v", code, current)
	}
	base := seedUsage(t, i)
	call(t, r, http.MethodGet, "/api/quota/current", nil, &current)
	var a *quotaAccountJSON
	for n := range current.Accounts {
		if current.Accounts[n].AuthIndex == "auth-a" {
			a = &current.Accounts[n]
		}
	}
	if a == nil || len(a.Windows) != 1 {
		t.Fatalf("current %+v", current)
	}
	w := a.Windows[0]
	if w.Window != "five_hour" || w.Utilization != 0.25 || w.PeriodSeconds != 18000 || w.Status != "allowed" ||
		w.ResetsAt == nil || *w.ResetsAt != "2026-11-02T11:00:00Z" || w.ObservedAt != "2026-11-02T11:10:00.4Z" || !w.AccountWide {
		t.Fatalf("window %+v", w)
	}
	// auth-c has usage but no quota headers: listed with no windows.
	found := false
	for _, acc := range current.Accounts {
		found = found || (acc.AuthIndex == "auth-c" && len(acc.Windows) == 0)
	}
	if !found {
		t.Fatalf("auth-c missing from %+v", current.Accounts)
	}

	var series struct {
		Series []quotaSeriesJSON `json:"series"`
	}
	path := "/api/quota/series" + q("from", rfc(base), "to", rfc(base.Add(2*time.Hour)), "provider", "claude")
	if code := call(t, r, http.MethodGet, path, nil, &series); code != http.StatusOK {
		t.Fatalf("series status %d", code)
	}
	if len(series.Series) != 2 {
		t.Fatalf("series %+v", series)
	}
	sa := series.Series[0]
	if sa.AuthIndex != "auth-a" || len(sa.Points) != 2 || sa.PeriodSeconds != 18000 || len(sa.Resets) != 1 || sa.Resets[0] != "2026-11-02T11:00:00Z" {
		t.Fatalf("auth-a series %+v", sa)
	}
	call(t, r, http.MethodGet, "/api/quota/series"+q("from", rfc(base), "to", rfc(base.Add(2*time.Hour)), "provider", "codex"), nil, &series)
	if len(series.Series) != 0 {
		t.Fatalf("codex series %+v", series)
	}
	expectBadRequest(t, r, http.MethodGet, "/api/quota/series"+q("from", "nope"), nil)
	expectBadRequest(t, r, http.MethodGet, "/api/quota/series"+q("from", rfc(base), "to", rfc(base.Add(-time.Hour))), nil)
}

func TestAccountsRoute(t *testing.T) {
	i, r, _ := newAPI(t)
	var resp struct {
		Accounts []accountJSON `json:"accounts"`
	}
	if code := call(t, r, http.MethodGet, "/api/accounts", nil, &resp); code != http.StatusOK || len(resp.Accounts) != 0 {
		t.Fatalf("empty accounts: %d %+v", code, resp)
	}
	seedUsage(t, i)
	call(t, r, http.MethodGet, "/api/accounts", nil, &resp)
	if len(resp.Accounts) != 3 {
		t.Fatalf("accounts %+v", resp.Accounts)
	}
	for _, a := range resp.Accounts {
		if a.AuthIndex == "auth-c" && (a.Provider != "codex" || a.FirstSeen == nil || *a.FirstSeen != "2026-11-02T11:20:00Z") {
			t.Fatalf("auth-c %+v", a)
		}
	}
}

func TestPricesRoutesRepriceHistory(t *testing.T) {
	i, r, _ := newAPI(t)
	base := seedUsage(t, i)
	var prices struct {
		Prices []Price `json:"prices"`
	}
	if code := call(t, r, http.MethodGet, "/api/prices", nil, &prices); code != http.StatusOK || len(prices.Prices) != len(seedPrices) {
		t.Fatalf("seeded prices: %d, %d rows", code, len(prices.Prices))
	}
	var astra *Price
	for n := range prices.Prices {
		if prices.Prices[n].ModelPattern == "gpt-6-astra" {
			astra = &prices.Prices[n]
		}
	}
	if astra == nil || astra.Source != openAISource {
		t.Fatalf("astra seed %+v", astra)
	}

	edited := []Price{{ModelPattern: "claude-*", Input: 1}, *astra}
	if code := call(t, r, http.MethodPut, "/api/prices", gin.H{"prices": edited}, &prices); code != http.StatusOK || len(prices.Prices) != 2 {
		t.Fatalf("put: %d %+v", code, prices)
	}
	for _, p := range prices.Prices {
		switch p.ModelPattern {
		case "claude-*":
			if p.Source != "edited in dashboard" || p.UpdatedAt != "2026-11-02T12:00:00Z" {
				t.Fatalf("edited row %+v", p)
			}
		case "gpt-6-astra":
			if p.Source != openAISource || p.UpdatedAt != astra.UpdatedAt {
				t.Fatalf("unchanged row lost provenance: %+v", p)
			}
		}
	}
	var summary struct {
		Totals Totals `json:"totals"`
	}
	call(t, r, http.MethodGet, "/api/usage/summary"+q("from", rfc(base), "to", rfc(base.Add(2*time.Hour))), nil, &summary)
	want := 3*10*1/1e6 + (2000*10+100*50)/1e6
	if math.Abs(summary.Totals.CostUSD-want) > 1e-12 {
		t.Fatalf("repriced cost %v, want %v", summary.Totals.CostUSD, want)
	}

	expectBadRequest(t, r, http.MethodPut, "/api/prices", gin.H{"prices": []Price{{ModelPattern: "a", Input: -1}}})
	expectBadRequest(t, r, http.MethodPut, "/api/prices", gin.H{"other": 1})
	expectBadRequest(t, r, http.MethodPut, "/api/prices", "not an object")
	call(t, r, http.MethodGet, "/api/prices", nil, &prices)
	if len(prices.Prices) != 2 {
		t.Fatalf("rejected PUT changed the table: %+v", prices.Prices)
	}
	if code := call(t, r, http.MethodPut, "/api/prices", gin.H{"prices": []Price{}}, &prices); code != http.StatusOK || len(prices.Prices) != 0 {
		t.Fatalf("empty put: %d %+v", code, prices)
	}
}

func TestResetPricesRestoresShippedRowsAndRepricesHistory(t *testing.T) {
	i, r, clock := newAPI(t)
	base := seedUsage(t, i)
	summaryPath := "/api/usage/summary" + q("from", rfc(base), "to", rfc(base.Add(2*time.Hour)))
	var summary struct {
		Totals Totals `json:"totals"`
	}
	call(t, r, http.MethodGet, summaryPath, nil, &summary)
	shippedCost := summary.Totals.CostUSD
	if shippedCost == 0 {
		t.Fatal("seeded usage has no cost at shipped prices")
	}

	var prices struct {
		Prices []Price `json:"prices"`
	}
	edited := []Price{{ModelPattern: "claude-*", Input: 1}, {ModelPattern: "gpt-6-astra", Input: 1}}
	if code := call(t, r, http.MethodPut, "/api/prices", gin.H{"prices": edited}, &prices); code != http.StatusOK {
		t.Fatalf("put: %d", code)
	}
	call(t, r, http.MethodGet, summaryPath, nil, &summary)
	if summary.Totals.CostUSD == shippedCost {
		t.Fatal("edited prices did not reprice history")
	}
	if _, err := i.db.ExecContext(context.Background(), `UPDATE price_seed SET version = 1`); err != nil {
		t.Fatal(err)
	}

	clock.t = clock.t.Add(time.Hour)
	if code := call(t, r, http.MethodPost, "/api/prices/reset", nil, &prices); code != http.StatusOK {
		t.Fatalf("reset: %d", code)
	}
	if len(prices.Prices) != len(seedPrices) {
		t.Fatalf("reset returned %d prices, want %d", len(prices.Prices), len(seedPrices))
	}
	got := map[string]Price{}
	for _, p := range prices.Prices {
		got[p.ModelPattern] = p
	}
	for _, want := range seedPrices {
		want.UpdatedAt = "2026-11-02T13:00:00Z"
		if got[want.ModelPattern] != want {
			t.Fatalf("reset row %+v, want %+v", got[want.ModelPattern], want)
		}
	}
	if _, ok := got["claude-*"]; ok {
		t.Fatal("reset kept a row that does not ship")
	}
	if v := queryInt(t, i, `SELECT version FROM price_seed`); v != int64(priceSeedVersion) {
		t.Fatalf("seed version after reset %d, want %d", v, priceSeedVersion)
	}
	call(t, r, http.MethodGet, summaryPath, nil, &summary)
	if summary.Totals.CostUSD != shippedCost {
		t.Fatalf("cost after reset %v, want the shipped-price cost %v", summary.Totals.CostUSD, shippedCost)
	}
}

func TestLongSessionIsMarkedTruncated(t *testing.T) {
	i, _, clock := newAPI(t)
	ctx := context.Background()
	start := clock.t.Add(-24 * time.Hour)
	seed := func(session string, n int) {
		t.Helper()
		if _, err := i.db.ExecContext(ctx, `WITH RECURSIVE seq(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM seq WHERE x < ?)
			INSERT INTO usage_events (event_key, ts, session_id, provider, model, auth_index, latency_ms, input_tokens, output_tokens)
			SELECT ?||x, ? + x, ?, 'claude', 'claude-opus-5-5', 'auth-a', 100, 10, 20 FROM seq`, n, session, ms(start), session); err != nil {
			t.Fatal(err)
		}
	}
	seed("full", sessionsMaxRows)
	seed("long", sessionsMaxRows+1)
	pb := newPriceBook(seedPrices)
	for session, truncated := range map[string]bool{"full": false, "long": true} {
		s, err := i.loadSession(ctx, session, ms(start), ms(clock.t), pb)
		if err != nil {
			t.Fatal(err)
		}
		if s.Truncated != truncated || s.Requests != sessionsMaxRows || s.Totals.InputTokens != 10*sessionsMaxRows {
			t.Fatalf("%s: truncated %v, requests %d, input %d", session, s.Truncated, s.Requests, s.Totals.InputTokens)
		}
	}
}

func TestUsageSummaryNamesUnpricedModels(t *testing.T) {
	i, r, clock := newAPI(t)
	ctx := context.Background()
	for n, model := range []string{"claude-opus-5-5", "unpriced-model", "unpriced-model", "no-tokens-model"} {
		rec := usage.Record{RequestID: fmt.Sprint(n), Provider: "claude", Model: model, AuthIndex: "auth-a",
			RequestedAt: clock.t.Add(-time.Hour), Latency: time.Second, Detail: usage.Detail{InputTokens: 1000}}
		if model == "no-tokens-model" {
			rec.Detail, rec.Failed = usage.Detail{}, true
		}
		i.HandleUsage(ctx, rec)
	}
	mustFlush(t, i)
	var resp struct {
		Totals Totals `json:"totals"`
	}
	call(t, r, http.MethodGet, "/api/usage/summary"+q("from", rfc(clock.t.Add(-2*time.Hour)), "to", rfc(clock.t), "bucket", "1h"), nil, &resp)
	if resp.Totals.Requests != 4 || strings.Join(resp.Totals.UnpricedModels, ",") != "unpriced-model" || math.Abs(resp.Totals.CostUSD-1000*4/1e6) > 1e-12 {
		t.Fatalf("totals %+v", resp.Totals)
	}
	raw, err := json.Marshal(Totals{})
	if err != nil || strings.Contains(string(raw), "unpricedModels") {
		t.Fatalf("priced totals carry unpricedModels: %s", raw)
	}
}
