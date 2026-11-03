package ingest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func claudeRecord(id string, at time.Time, headers http.Header) usage.Record {
	return usage.Record{
		RequestID:   id,
		TraceID:     "trace-" + id,
		SessionID:   "session-1",
		Provider:    "claude",
		Model:       "claude-opus-5-5",
		AuthIndex:   "auth-a",
		AuthID:      "claude-a.json",
		Stream:      true,
		RequestedAt: at,
		Latency:     2 * time.Second,
		TTFT:        400 * time.Millisecond,
		Detail: usage.Detail{
			InputTokens:           10,
			OutputTokens:          200,
			ReasoningTokens:       50,
			CacheReadTokens:       5000,
			CacheCreationTokens:   300,
			CacheCreation5mTokens: usage.Int64Ptr(100),
			CacheCreation1hTokens: usage.Int64Ptr(200),
			TokenBreakdown:        usage.NewIndependentTokenBreakdown(10, 5000, 300, 150, 50, 0),
		},
		ResponseHeaders: headers,
	}
}

func TestFlushStoresEventsRollupAndAccounts(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	ctx := context.Background()
	at := clock.t.Add(-time.Minute)
	i.HandleUsage(ctx, claudeRecord("req-1", at, nil))
	i.HandleUsage(ctx, usage.Record{
		RequestID: "req-2", Provider: "codex", Model: "gpt-6-astra", AuthIndex: "auth-c",
		RequestedAt: at.Add(time.Second), Latency: time.Second, Failed: true,
		Fail:   usage.Failure{StatusCode: 429},
		Detail: usage.Detail{InputTokens: 1000, CachedTokens: 600, OutputTokens: 20},
	})
	// The same request id again must not double count.
	i.HandleUsage(ctx, claudeRecord("req-1", at, nil))
	mustFlush(t, i)

	if n := queryInt(t, i, `SELECT COUNT(*) FROM usage_events`); n != 2 {
		t.Fatalf("events = %d, want 2", n)
	}
	var in, out, reasoning, cr, cw, cw5, cw1, ttft int64
	if err := i.db.QueryRow(`SELECT input_tokens, output_tokens, reasoning_tokens, cache_read_tokens, cache_write_tokens,
		cache_write_5m_tokens, cache_write_1h_tokens, ttft_ms FROM usage_events WHERE event_key='req-1'`).
		Scan(&in, &out, &reasoning, &cr, &cw, &cw5, &cw1, &ttft); err != nil {
		t.Fatal(err)
	}
	if in != 10 || out != 200 || reasoning != 50 || cr != 5000 || cw != 300 || cw5 != 100 || cw1 != 200 || ttft != 400 {
		t.Fatalf("claude tokens = %d %d %d %d %d %d %d ttft %d", in, out, reasoning, cr, cw, cw5, cw1, ttft)
	}
	// OpenAI-style input includes cached tokens; taproot stores uncached input.
	if err := i.db.QueryRow(`SELECT input_tokens, cache_read_tokens, failed, status_code, ttft_ms IS NULL FROM usage_events WHERE event_key='req-2'`).
		Scan(&in, &cr, &out, &cw, &ttft); err != nil {
		t.Fatal(err)
	}
	if in != 400 || cr != 600 || out != 1 || cw != 429 || ttft != 1 {
		t.Fatalf("codex row = input %d cacheRead %d failed %d status %d ttftNull %d", in, cr, out, cw, ttft)
	}
	if n := queryInt(t, i, `SELECT SUM(requests) FROM usage_rollup_hourly`); n != 2 {
		t.Fatalf("rollup requests = %d, want 2", n)
	}
	if n := queryInt(t, i, `SELECT failures FROM usage_rollup_hourly WHERE model='gpt-6-astra'`); n != 1 {
		t.Fatalf("rollup failures = %d, want 1", n)
	}
	if n := queryInt(t, i, `SELECT last_seen FROM accounts WHERE auth_index='auth-c'`); n != at.Add(time.Second).UnixMilli() {
		t.Fatalf("auth-c last_seen = %d", n)
	}
}

func TestQuotaObservationsWriteOnChangeOrHeartbeat(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	ctx := context.Background()
	h := func(util string) http.Header {
		return hdr(
			"Anthropic-Ratelimit-Unified-7d-Utilization", util,
			"Anthropic-Ratelimit-Unified-7d-Reset", "1793880000",
			"Anthropic-Ratelimit-Unified-7d-Status", "allowed",
		)
	}
	base := clock.t
	steps := []struct {
		offset time.Duration
		util   string
		rows   int64
	}{
		{0, "0.40", 1},                           // first sight
		{time.Minute, "0.40", 1},                 // unchanged, within heartbeat
		{2 * time.Minute, "0.41", 2},             // value changed
		{4 * time.Minute, "0.41", 2},             // unchanged
		{7*time.Minute + time.Second, "0.41", 3}, // heartbeat elapsed since last row
	}
	for n, s := range steps {
		// Latency is zero so observedAt equals RequestedAt.
		r := claudeRecord("req-"+string(rune('a'+n)), base.Add(s.offset), h(s.util))
		r.Latency = 0
		i.HandleUsage(ctx, r)
		mustFlush(t, i)
		if got := queryInt(t, i, `SELECT COUNT(*) FROM quota_observations WHERE window_name='seven_day'`); got != s.rows {
			t.Fatalf("step %d: rows = %d, want %d", n, got, s.rows)
		}
	}

	// A fresh tracker (process restart) resumes from the stored last row.
	i.tracker = newQuotaTracker()
	r := claudeRecord("req-z", base.Add(8*time.Minute), h("0.41"))
	r.Latency = 0
	i.HandleUsage(ctx, r)
	mustFlush(t, i)
	if got := queryInt(t, i, `SELECT COUNT(*) FROM quota_observations`); got != 3 {
		t.Fatalf("after restart rows = %d, want 3", got)
	}
}

func TestHandleUsageDropsWhenQueueFull(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	for n := 0; n < maxPending+5; n++ {
		i.HandleUsage(context.Background(), usage.Record{RequestedAt: clock.t})
	}
	i.mu.Lock()
	pending, dropped := len(i.pending), i.dropped
	i.mu.Unlock()
	if pending != maxPending || dropped != 5 {
		t.Fatalf("pending %d dropped %d", pending, dropped)
	}
}

func TestFlushRequeuesOnFailure(t *testing.T) {
	i, clock := newTestIngest(t, Config{})
	i.HandleUsage(context.Background(), claudeRecord("req-1", clock.t, nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := i.flush(ctx); err == nil {
		t.Fatal("flush with cancelled context succeeded")
	}
	mustFlush(t, i)
	if n := queryInt(t, i, `SELECT COUNT(*) FROM usage_events`); n != 1 {
		t.Fatalf("events after retry = %d, want 1", n)
	}
}

func TestStartFlushesOnShutdown(t *testing.T) {
	i, clock := newTestIngest(t, Config{PollInterval: "0"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		i.runWriter(ctx)
		close(done)
	}()
	i.HandleUsage(context.Background(), claudeRecord("req-1", clock.t, nil))
	cancel()
	<-done
	if n := queryInt(t, i, `SELECT COUNT(*) FROM usage_events`); n != 1 {
		t.Fatalf("events after shutdown = %d, want 1", n)
	}
}

func TestConfigPollInterval(t *testing.T) {
	cases := map[string]time.Duration{"": 15 * time.Minute, "0": 0, "5m": 5 * time.Minute}
	for raw, want := range cases {
		got, err := Config{PollInterval: raw}.pollInterval()
		if err != nil || got != want {
			t.Errorf("%q: got %v, %v; want %v", raw, got, err, want)
		}
	}
	for _, bad := range []string{"soon", "-1m"} {
		if _, err := (Config{PollInterval: bad}).pollInterval(); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
	if _, err := New(Config{PollInterval: "soon"}, openTestDB(t)); err == nil {
		t.Error("New accepted a bad poll interval")
	}
}
