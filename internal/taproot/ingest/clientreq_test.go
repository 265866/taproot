package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/store"
	"github.com/265866/taproot/sdk/cliproxy/usage"
)

type clientRow struct {
	ts                                           int64
	requestID, deviceID, deviceName, model, prov string
	status                                       int
	failed                                       bool
	latency                                      int64
	ttft, attempts                               sql.NullInt64
	code, message                                string
}

func clientRows(t *testing.T, i *Ingest) []clientRow {
	t.Helper()
	rows, err := i.db.Query(`SELECT ts, request_id, device_id, device_name, model, provider, status, failed, latency_ms, ttft_ms,
		attempts, error_code, error_message FROM client_requests ORDER BY ts, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRows(rows)
	var out []clientRow
	for rows.Next() {
		var r clientRow
		if err := rows.Scan(&r.ts, &r.requestID, &r.deviceID, &r.deviceName, &r.model, &r.prov, &r.status, &r.failed, &r.latency,
			&r.ttft, &r.attempts, &r.code, &r.message); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type stubDevices map[string]access.Device

func (s stubDevices) RequestDevice(id string) (access.Device, bool) {
	d, ok := s[id]
	return d, ok
}

func (s stubDevices) DeviceName(_, stored string) string { return stored }

var laptopDevice = access.Device{ID: "node:nLAPTOP", Name: "laptop", Via: access.ViaTailnet}

func TestClientRequestCountsAttemptsFromUsage(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	i.AttachDevices(stubDevices{"abc12345": laptopDevice})
	ctx := context.Background()
	start := clock.t.Add(-10 * time.Second)

	link := i.TrackRequest("abc12345", false, "/v1/messages")
	// A 429 on one account, then a success on another, which Codex's image
	// tool follows with a second record for the same attempt.
	first := usage.Record{RequestID: "u1", TraceID: "abc12345", Provider: "claude", Model: "claude-opus-5-5", AuthIndex: "a1",
		RequestedAt: start, Latency: 300 * time.Millisecond, Failed: true, Fail: usage.Failure{StatusCode: 429}, Stream: true}
	second := first
	second.RequestID, second.AuthIndex, second.RequestedAt, second.Failed, second.Fail = "u2", "a2", start.Add(time.Second), false, usage.Failure{}
	extra := second
	extra.RequestID, extra.Model = "u3", "gpt-image-2"
	for _, r := range []usage.Record{first, second, extra} {
		i.HandleUsage(ctx, r)
	}
	i.FinishRequest(link, ClientRequest{Start: start, RequestID: "abc12345", Path: "/v1/messages", Status: 200,
		Latency: 4 * time.Second, TTFT: 1500 * time.Millisecond, Stream: true}, nil)

	mustFlush(t, i)
	if got := clientRows(t, i); len(got) != 0 {
		t.Fatalf("rows before the settle delay = %+v, want none", got)
	}
	clock.t = clock.t.Add(clientSettle)
	mustFlush(t, i)
	got := clientRows(t, i)
	if len(got) != 1 {
		t.Fatalf("rows = %+v, want 1", got)
	}
	r := got[0]
	if r.attempts.Int64 != 2 || !r.attempts.Valid || r.failed || r.status != 200 || r.deviceID != laptopDevice.ID || r.deviceName != "laptop" ||
		r.model != "claude-opus-5-5" || r.prov != "claude" || r.ttft.Int64 != 1500 || r.latency != 4000 || r.code != "" {
		t.Fatalf("row = %+v", r)
	}
	if n := queryInt(t, i, `SELECT retries FROM client_request_slots`); n != 1 {
		t.Fatalf("retries = %d, want 1", n)
	}
	if n := len(i.links); n != 0 {
		t.Fatalf("links left after flush = %d, want 0", n)
	}
}

func TestClientRequestRefusedBeforeAnyAccount(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	i.AttachProviderGuess(func(model string) string {
		if model == "claude-opus-5-5" {
			return "Claude"
		}
		return ""
	})
	link := i.TrackRequest("noacct01", false, "/v1/messages")
	body := []byte(`{"error":{"type":"invalid_request_error","message":"no enabled Claude account serves model claude-opus-5-5: add or enable one"}}`)
	i.FinishRequest(link, ClientRequest{Start: clock.t, RequestID: "noacct01", Model: "claude-opus-5-5", Status: 400,
		Failed: true, ErrorCode: "unknown_provider", Latency: 3 * time.Millisecond}, body)
	clock.t = clock.t.Add(clientSettle)
	mustFlush(t, i)
	got := clientRows(t, i)
	if len(got) != 1 {
		t.Fatalf("rows = %+v", got)
	}
	r := got[0]
	if !r.failed || r.code != "unknown_provider" || r.prov != "claude" || !r.attempts.Valid || r.attempts.Int64 != 0 ||
		r.message != "no enabled Claude account serves model claude-opus-5-5: add or enable one" || r.ttft.Valid {
		t.Fatalf("row = %+v", r)
	}
	// Never usage: no account was called.
	if n := queryInt(t, i, `SELECT COUNT(*) FROM usage_events`); n != 0 {
		t.Fatalf("usage events = %d", n)
	}
}

func TestClientRequestWebsocketTurns(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	i.AttachDevices(stubDevices{"ws000001": laptopDevice})
	ctx := context.Background()
	link := i.TrackRequest("ws000001", true, "/v1/responses")
	for n, failed := range []bool{false, true} {
		r := usage.Record{RequestID: fmt.Sprint("t", n), TraceID: "ws000001", Provider: "codex", Model: "gpt-6-sol",
			RequestedAt: clock.t.Add(time.Duration(n) * time.Minute), Latency: 3 * time.Second, TTFT: 800 * time.Millisecond, Stream: true}
		if failed {
			r.Failed, r.Fail = true, usage.Failure{StatusCode: 502, Body: `{"error":{"message":"upstream connection reset"}}`}
		}
		i.HandleUsage(ctx, r)
	}
	// The connection ends after hours; it is not a request of its own.
	i.FinishRequest(link, ClientRequest{Start: clock.t, RequestID: "ws000001", Status: 200, Latency: 3 * time.Hour}, nil)
	mustFlush(t, i)
	got := clientRows(t, i)
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want one per turn", got)
	}
	if got[0].failed || got[0].ttft.Int64 != 800 || got[0].latency != 3000 || got[0].deviceID != laptopDevice.ID ||
		!got[1].failed || got[1].status != 502 || got[1].code != "bad_gateway" || got[1].message != "upstream connection reset" {
		t.Fatalf("rows = %+v", got)
	}
	clock.t = clock.t.Add(clientSettle)
	mustFlush(t, i)
	if n := len(i.links); n != 0 {
		t.Fatalf("links left after the connection = %d", n)
	}
	if n := len(clientRows(t, i)); n != 2 {
		t.Fatalf("rows = %d after the connection ended, want 2", n)
	}
}

func TestClientRequestRefusedWebsocketIsOneRequest(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	link := i.TrackRequest("ws000002", true, "/v1/responses")
	i.FinishRequest(link, ClientRequest{Start: clock.t, RequestID: "ws000002", Status: 503, Failed: true}, []byte("service unavailable"))
	clock.t = clock.t.Add(clientSettle)
	mustFlush(t, i)
	got := clientRows(t, i)
	if len(got) != 1 || got[0].code != "unavailable" || got[0].message != "service unavailable" {
		t.Fatalf("rows = %+v", got)
	}
}

func TestClientRequestQueueDropsWhenFull(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	for range maxClientPending + 3 {
		i.RecordClientRequest(ClientRequest{Start: clock.t, Status: 200})
	}
	if len(i.pendingRequests) != maxClientPending || i.droppedRequests != 3 {
		t.Fatalf("pending %d dropped %d", len(i.pendingRequests), i.droppedRequests)
	}
	// A dropped request does not leave its link behind.
	link := i.TrackRequest("full0001", false, "/v1/messages")
	i.FinishRequest(link, ClientRequest{Start: clock.t, Status: 200}, nil)
	if _, ok := i.links["full0001"]; ok {
		t.Fatal("dropped request kept its link")
	}
}

func TestClientRequestFinalFlushSkipsSettle(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	i.Start(ctx)
	link := i.TrackRequest("drain001", false, "/v1/messages")
	i.FinishRequest(link, ClientRequest{Start: clock.t, RequestID: "drain001", Status: 200, Latency: time.Second}, nil)
	cancel()
	i.Wait()
	if got := clientRows(t, i); len(got) != 1 || got[0].requestID != "drain001" {
		t.Fatalf("rows after shutdown = %+v", got)
	}
}

func TestClientRequestFailedFlushKeepsRequests(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	i.RecordClientRequest(ClientRequest{Start: clock.t, Status: 200, Latency: time.Second})
	// Hold the write lock so the flush fails after its busy timeout.
	conn, err := i.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := i.flush(ctx); err == nil {
		t.Fatal("flush with the database locked succeeded")
	}
	if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, i)
	if n := len(clientRows(t, i)); n != 1 {
		t.Fatalf("rows = %d after the retry, want 1", n)
	}
}

func TestErrorMessageAndCode(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{`{"error":{"message":"rate limited"}}`, "rate limited"},
		{`{"error":"bad key"}`, "bad key"},
		{`{"message":"plain"}`, "plain"},
		{"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n", "Overloaded"},
		{"upstream timed out\nmore", "upstream timed out"},
		{`{"other":1}`, ""},
	} {
		if got := errorMessage([]byte(c.body)); got != c.want {
			t.Errorf("errorMessage(%q) = %q, want %q", c.body, got, c.want)
		}
	}
	long := shorten(string(make([]rune, 400)) + "x")
	if n := len([]rune(long)); n > errorMessageMax {
		t.Errorf("shortened to %d runes", n)
	}
	for status, want := range map[int]string{429: "rate_limited", 529: "overloaded", 502: "bad_gateway", 499: "client_closed",
		400: "bad_request", 418: "http_4xx", 500: "http_5xx", 200: "stream_error"} {
		if got := errorCode(ClientRequest{Status: status, Failed: true}); got != want {
			t.Errorf("errorCode(%d) = %q, want %q", status, got, want)
		}
	}
	if got := errorCode(ClientRequest{Status: 200}); got != "" {
		t.Errorf("success code = %q", got)
	}
}

// seedClientRows writes rows straight to the table and its slots, as the
// writer does, without the settle delay.
func seedClientRows(t *testing.T, i *Ingest, reqs []ClientRequest) {
	t.Helper()
	if err := store.WithImmediateTx(context.Background(), i.db.DB, func(c *sql.Conn) error {
		return insertClientRequests(context.Background(), c, reqs)
	}); err != nil {
		t.Fatal(err)
	}
}

type requestsSummary struct {
	Totals     requestTotals   `json:"totals"`
	Buckets    []requestBucket `json:"buckets"`
	TopDevices []topDevice     `json:"topDevices"`
	TopModels  []topModel      `json:"topModels"`
}

func randomClientRequests(rng *rand.Rand, from, to time.Time, n int) []ClientRequest {
	devices := []string{"node:a", "node:b", "local", ""}
	models := []string{"claude-opus-5-5", "gpt-6-sol", "claude-haiku-5-5"}
	out := make([]ClientRequest, n)
	span := to.Sub(from)
	for k := range out {
		failed := rng.IntN(10) == 0
		attempts := 1 + rng.IntN(3)
		status := 200
		if failed {
			status = []int{429, 529, 502, 400}[rng.IntN(4)]
		}
		out[k] = ClientRequest{
			Start: from.Add(time.Duration(rng.Int64N(int64(span)))), Device: access.Device{ID: devices[rng.IntN(len(devices))]},
			Model: models[rng.IntN(len(models))], Status: status, Failed: failed, Attempts: &attempts,
			Latency: time.Duration(50+rng.IntN(20_000)) * time.Millisecond, TTFT: time.Duration(rng.IntN(3000)) * time.Millisecond,
		}
	}
	return out
}

// TestRequestsSummaryMatchesRawCount checks, in zones with DST, half-hour,
// and 45-minute offsets, that every request lands in the local day (or UTC
// hour) that contains it, that the buckets sum to the totals, and that the
// slot rollup gives the same answer as counting rows.
func TestRequestsSummaryMatchesRawCount(t *testing.T) {
	i, r, _ := newAPI(t)
	rng := rand.New(rand.NewPCG(7, 11))
	// Covers the US fall-back on 2026-11-01 and the Kolkata and Kathmandu day boundaries.
	from := time.Date(2026, 10, 28, 3, 7, 0, 0, time.UTC)
	to := time.Date(2026, 11, 4, 19, 52, 13, 0, time.UTC)
	reqs := randomClientRequests(rng, from.Add(-time.Hour), to.Add(time.Hour), 4000)
	seedClientRows(t, i, reqs)

	for _, c := range []struct{ tz, bucket string }{
		{"America/Los_Angeles", "1d"}, {"Asia/Kolkata", "1d"}, {"Asia/Kathmandu", "1d"}, {"UTC", "1h"}, {"Asia/Kolkata", "5m"},
	} {
		t.Run(c.tz+"/"+c.bucket, func(t *testing.T) {
			var got requestsSummary
			if code := call(t, r, http.MethodGet, "/api/requests/summary"+q("from", rfc(from), "to", rfc(to), "bucket", c.bucket, "tz", c.tz), nil, &got); code != http.StatusOK {
				t.Fatalf("status %d", code)
			}
			loc, err := time.LoadLocation(c.tz)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]requestCounts{}
			var total requestCounts
			var wantRetries int64
			for _, q := range reqs {
				if q.Start.Before(from) || !q.Start.Before(to) {
					continue
				}
				var key time.Time
				switch c.bucket {
				case "1d":
					y, m, d := q.Start.In(loc).Date()
					key = time.Date(y, m, d, 0, 0, 0, 0, loc)
				case "1h":
					key = q.Start.Truncate(time.Hour)
				default:
					key = q.Start.Truncate(5 * time.Minute)
				}
				k := formatTime(key)
				w := want[k]
				w.add(1, int64(boolInt(q.Failed)))
				want[k] = w
				total.add(1, int64(boolInt(q.Failed)))
				wantRetries += retries(q.Attempts)
			}
			var sum requestCounts
			for _, b := range got.Buckets {
				if w := want[b.T]; w.requests != b.Requests || w.failed != b.Failed {
					t.Errorf("bucket %s = %d/%d, want %d/%d", b.T, b.Requests, b.Failed, w.requests, w.failed)
				}
				sum.add(b.Requests, b.Failed)
				delete(want, b.T)
			}
			if len(want) > 0 {
				t.Errorf("requests outside every bucket: %v", want)
			}
			if sum != total || got.Totals.Requests != total.requests || got.Totals.Failed != total.failed || got.Totals.Retries != wantRetries {
				t.Errorf("totals %+v, bucket sum %+v, want %+v with %d retries", got.Totals, sum, total, wantRetries)
			}
		})
	}
}

func TestRequestsSummaryMediansTopListsAndCost(t *testing.T) {
	i, r, clock := newAPI(t)
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	clock.t = base.Add(2 * time.Hour)
	two, one, zero := 2, 1, 0
	reqs := []ClientRequest{
		{Start: base.Add(time.Minute), Device: laptopDevice, Model: "claude-opus-5-5", Status: 200, Latency: 1000 * time.Millisecond, TTFT: 400 * time.Millisecond, Attempts: &two},
		{Start: base.Add(2 * time.Minute), Device: laptopDevice, Model: "claude-opus-5-5", Status: 200, Latency: 2000 * time.Millisecond, TTFT: 500 * time.Millisecond, Attempts: &one},
		{Start: base.Add(20 * time.Minute), Device: laptopDevice, Model: "gpt-6-sol", Status: 529, Failed: true, Latency: 3000 * time.Millisecond, Attempts: &one},
		{Start: base.Add(50 * time.Minute), Device: access.Device{ID: "local", Name: "server (local)"}, Model: "claude-opus-5-5", Status: 400, Failed: true, ErrorCode: "unknown_provider", Latency: 5 * time.Millisecond, Attempts: &zero},
	}
	seedClientRows(t, i, reqs)
	ctx := context.Background()
	rec := claudeRecord("u-1", base.Add(time.Minute), nil)
	rec.TraceID = "laptop-trace"
	i.AttachDevices(stubDevices{"laptop-trace": laptopDevice})
	i.HandleUsage(ctx, rec)
	mustFlush(t, i)

	var got requestsSummary
	if code := call(t, r, http.MethodGet, "/api/requests/summary"+q("from", rfc(base), "to", rfc(base.Add(2*time.Hour))), nil, &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	tot := got.Totals
	if tot.Requests != 4 || tot.Failed != 2 || tot.Retries != 1 || tot.FailureRate == nil || *tot.FailureRate != 0.5 {
		t.Fatalf("totals = %+v", tot)
	}
	// Four latencies: 5, 1000, 2000, 3000 ms; the median is the second.
	if tot.MedianLatencyMs == nil || relErr(*tot.MedianLatencyMs, 1000) > 0.06 || tot.MedianTTFTMs == nil || relErr(*tot.MedianTTFTMs, 400) > 0.06 {
		t.Fatalf("medians = %v %v", derefFloat(tot.MedianLatencyMs), derefFloat(tot.MedianTTFTMs))
	}
	if len(got.Buckets) != 2 || got.Buckets[0].Requests != 4 || got.Buckets[1].Requests != 0 || got.Buckets[1].FailureRate != nil {
		t.Fatalf("buckets = %+v", got.Buckets)
	}
	if len(got.TopDevices) != 2 || got.TopDevices[0].DeviceID != laptopDevice.ID || got.TopDevices[0].Name != "laptop" || got.TopDevices[0].Requests != 3 ||
		got.TopDevices[0].CostUSD <= 0 || got.TopDevices[1].DeviceID != "local" || got.TopDevices[1].CostUSD != 0 {
		t.Fatalf("top devices = %+v", got.TopDevices)
	}
	if len(got.TopModels) != 2 || got.TopModels[0].Model != "claude-opus-5-5" || got.TopModels[0].Requests != 3 || got.TopModels[0].CostUSD != tot.CostUSD || tot.CostUSD <= 0 {
		t.Fatalf("top models = %+v, cost %v", got.TopModels, tot.CostUSD)
	}
	var one1 requestsSummary
	call(t, r, http.MethodGet, "/api/requests/summary"+q("from", rfc(base), "to", rfc(base.Add(2*time.Hour)), "top", "1"), nil, &one1)
	if len(one1.TopDevices) != 1 || len(one1.TopModels) != 1 {
		t.Fatalf("top=1 gave %d devices and %d models", len(one1.TopDevices), len(one1.TopModels))
	}
	for _, bad := range []string{q("bucket", "2h"), q("top", "0"), q("top", "51"), q("tz", "Mars/Base"), q("from", "yesterday")} {
		expectBadRequest(t, r, http.MethodGet, "/api/requests/summary"+bad, nil)
	}
}

func relErr(got, want float64) float64 {
	d := (got - want) / want
	if d < 0 {
		return -d
	}
	return d
}

func derefFloat(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

type fakeLogs map[string]bool

func (f fakeLogs) Logged(_ context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = f[id]
	}
	return out, nil
}

type brokenLogs struct{}

func (brokenLogs) Logged(context.Context, []string) (map[string]bool, error) {
	return nil, errors.New("database is locked")
}

func TestRequestsRecent(t *testing.T) {
	i, r, clock := newAPI(t)
	var reqs []ClientRequest
	for n := range 12 {
		failed := n%3 == 0
		status := 200
		if failed {
			status = 429
		}
		one := 1
		reqs = append(reqs, ClientRequest{Start: clock.t.Add(time.Duration(n) * time.Minute), RequestID: fmt.Sprintf("req%05d", n),
			Device: laptopDevice, Model: "claude-opus-5-5", Provider: "claude", Status: status, Failed: failed, Attempts: &one,
			ErrorCode: map[bool]string{true: "rate_limited"}[failed], ErrorMessage: map[bool]string{true: "rate limited"}[failed]})
	}
	// Two at the same instant keep insertion order newest first.
	reqs = append(reqs, ClientRequest{Start: reqs[11].Start, RequestID: "req-tie", Device: laptopDevice, Status: 502, Failed: true, ErrorCode: "bad_gateway"})
	seedClientRows(t, i, reqs)
	i.AttachRequestLogs(fakeLogs{"req00009": true, "req-tie": true})

	var got struct{ Requests []recentRequest }
	if code := call(t, r, http.MethodGet, "/api/requests/recent"+q("failed", "true"), nil, &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	var ids []string
	for _, x := range got.Requests {
		ids = append(ids, derefString(x.RequestID)+"@"+x.T)
	}
	want := []string{"req-tie", "req00009", "", "", ""}
	if len(got.Requests) != 5 {
		t.Fatalf("recent failed = %v, want 5 (the default)", ids)
	}
	for k, w := range want {
		if derefString(got.Requests[k].RequestID) != w {
			t.Fatalf("recent failed = %v, want ids %v (only logged requests link)", ids, want)
		}
	}
	if got.Requests[1].T != formatTime(reqs[9].Start) || got.Requests[1].ErrorCode != "rate_limited" || got.Requests[1].ErrorMessage != "rate limited" ||
		got.Requests[1].DeviceName != "laptop" || got.Requests[1].Attempts == nil || *got.Requests[1].Attempts != 1 {
		t.Fatalf("second = %+v", got.Requests[1])
	}
	if !slices.IsSortedFunc(got.Requests, func(a, b recentRequest) int { return strings.Compare(b.T, a.T) }) {
		t.Fatalf("not newest first: %v", ids)
	}
	call(t, r, http.MethodGet, "/api/requests/recent"+q("limit", "100"), nil, &got)
	if len(got.Requests) != 13 {
		t.Fatalf("all recent = %d, want 13", len(got.Requests))
	}
	call(t, r, http.MethodGet, "/api/requests/recent"+q("failed", "false", "limit", "3"), nil, &got)
	if len(got.Requests) != 3 || got.Requests[0].Failed || got.Requests[0].T != formatTime(reqs[11].Start) {
		t.Fatalf("recent successes = %+v", got.Requests)
	}
	// A failed lookup keeps the links rather than failing the list.
	i.AttachRequestLogs(brokenLogs{})
	call(t, r, http.MethodGet, "/api/requests/recent"+q("limit", "1"), nil, &got)
	if len(got.Requests) != 1 || derefString(got.Requests[0].RequestID) != "req-tie" {
		t.Fatalf("with broken logs = %+v", got.Requests)
	}
	for _, bad := range []string{q("limit", "0"), q("limit", "101"), q("failed", "yes")} {
		expectBadRequest(t, r, http.MethodGet, "/api/requests/recent"+bad, nil)
	}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func TestClientSlotsRebuildWhenStale(t *testing.T) {
	db := openTestDB(t)
	i, err := New(Config{}, db)
	if err != nil {
		t.Fatal(err)
	}
	reqs := randomClientRequests(rand.New(rand.NewPCG(1, 2)), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), 500)
	seedClientRows(t, i, reqs)
	var want [3]int64
	if err := db.QueryRow(`SELECT SUM(requests), SUM(failed), SUM(retries) FROM client_request_slots`).Scan(&want[0], &want[1], &want[2]); err != nil {
		t.Fatal(err)
	}
	// A release that wrote rows without the rollup leaves it short.
	if _, err := db.Exec(`DELETE FROM client_request_slots WHERE slot % 2 = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{}, db); err != nil {
		t.Fatal(err)
	}
	var got [3]int64
	if err := db.QueryRow(`SELECT SUM(requests), SUM(failed), SUM(retries) FROM client_request_slots`).Scan(&got[0], &got[1], &got[2]); err != nil {
		t.Fatal(err)
	}
	if got != want || want[0] != 500 {
		t.Fatalf("rebuilt slots = %v, want %v", got, want)
	}
}
