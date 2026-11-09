package reqlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/interfaces"
)

// restoreSnapshot stops the harness, replaces its database with snapshot the
// way ApplyPendingRestore does, and reopens the logs on it.
func (h *harness) restoreSnapshot(snapshot string) {
	h.t.Helper()
	h.stop()
	dbPath := h.db.Path()
	if err := h.db.Close(); err != nil {
		h.t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
	if err := os.Rename(snapshot, dbPath); err != nil {
		h.t.Fatal(err)
	}
	h.db = openStore(h.t, h.dataDir)
	h.open(Config{})
}

// TestRestoreDoesNotReuseSegmentIDs covers a backup restore while sessions
// were open: segment files newer than the snapshot stay on disk, and new
// segments must not collide with them.
func TestRestoreDoesNotReuseSegmentIDs(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	logSimple(t, h.logs.logger, "before", "/v1/messages", "s-before", 200, ts, []byte(`{}`))
	h.flush()
	h.sealAll()
	snapshot := filepath.Join(t.TempDir(), "snap.db")
	if err := h.db.Snapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("lost-%d", i), "/v1/messages", fmt.Sprintf("s-lost-%d", i), 200, ts, []byte(`{}`))
	}
	h.flush()

	h.restoreSnapshot(snapshot)
	h.logs.engine.compactQueued(context.Background())
	for i := 0; i < 3; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("after-%d", i), "/v1/messages", fmt.Sprintf("s-after-%d", i), 200, ts, []byte(`{}`))
	}
	h.flush()
	if n := h.logs.engine.stats.writeErrors.Load(); n != 0 {
		t.Fatalf("write errors after restore: %d", n)
	}
	for _, id := range []string{"before", "after-0", "after-1", "after-2"} {
		if _, err := h.logs.Get(context.Background(), id, true); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	// The snapshot predates the lost records, so their files are unowned.
	raws, _ := filepath.Glob(filepath.Join(h.dataDir, "request-logs", "*", "*.raw"))
	if len(raws) != 3 {
		t.Fatalf("raw files after restore: %v, want the 3 new open segments", raws)
	}
}

// TestRestoreKeepsSegmentSealedAfterSnapshot covers a segment the snapshot
// lists as open but that sealed before the restore: its sealed file is the
// only copy of its records.
func TestRestoreKeepsSegmentSealedAfterSnapshot(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	logSimple(t, h.logs.logger, "open-at-snapshot", "/v1/messages", "s", 200, ts, []byte(`{"n":1}`))
	h.flush()
	snapshot := filepath.Join(t.TempDir(), "snap.db")
	if err := h.db.Snapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	h.sealAll()
	if raws, _ := filepath.Glob(filepath.Join(h.dataDir, "request-logs", "*", "*.raw")); len(raws) != 0 {
		t.Fatalf("raw files left after seal: %v", raws)
	}

	h.restoreSnapshot(snapshot)
	h.logs.engine.compactQueued(context.Background())
	d, err := h.logs.Get(context.Background(), "open-at-snapshot", true)
	if err != nil {
		t.Fatalf("record sealed after the snapshot lost: %v", err)
	}
	if d.Request.Body != `{"n":1}` {
		t.Fatalf("body %q", d.Request.Body)
	}
	if states := h.segmentStates(); states[segmentSealed] != 1 || len(states) != 1 {
		t.Fatalf("segments after restore: %v", states)
	}
}

// TestStreamBudgetReleasedWhenLoggingTurnsOff covers upstream's Finalize,
// which skips Close when request-log was switched off mid-stream.
func TestStreamBudgetReleasedWhenLoggingTurnsOff(t *testing.T) {
	h := newHarness(t, Config{})
	lg := h.logs.logger
	w, err := lg.LogStreamingRequest("/v1/messages", "POST", nil, []byte(`{"model":"m"}`), "abandoned")
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteStatus(200, map[string][]string{"Content-Type": {"text/event-stream"}})
	for i := 0; i < 8; i++ {
		w.WriteChunkAsync(bytes.Repeat([]byte("c"), 4<<10))
	}
	if used := h.logs.engine.budget.used.Load(); used == 0 {
		t.Fatal("chunks reserved no budget")
	}
	lg.SetEnabled(false)
	if used := h.logs.engine.budget.used.Load(); used != 0 {
		t.Fatalf("budget held after logging turned off: %d bytes", used)
	}
	// A Close that still arrives stores nothing and releases nothing twice.
	w.WriteChunkAsync([]byte("late"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	h.flush()
	if used := h.logs.engine.budget.used.Load(); used != 0 {
		t.Fatalf("budget after late close: %d", used)
	}
	if _, err := h.logs.Get(context.Background(), "abandoned", true); !errors.Is(err, errNotFound) {
		t.Fatalf("abandoned stream stored: %v", err)
	}
	lg.streamsMu.Lock()
	live := len(lg.streams)
	lg.streamsMu.Unlock()
	if live != 0 {
		t.Fatalf("%d stream writers still tracked", live)
	}

	// Streams opened after logging is back on are stored as usual.
	lg.SetEnabled(true)
	w2, _ := lg.LogStreamingRequest("/v1/messages", "POST", nil, []byte(`{"model":"m"}`), "kept")
	_ = w2.WriteStatus(200, map[string][]string{"Content-Type": {"text/event-stream"}})
	w2.WriteChunkAsync([]byte("data: ok\n\n"))
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	h.flush()
	if _, err := h.logs.Get(context.Background(), "kept", true); err != nil {
		t.Fatal(err)
	}
}

// TestStreamKeepsAPIResponseErrors covers WriteAPIResponseErrors, which
// upstream's Finalize calls after a stream whose upstream failed.
func TestStreamKeepsAPIResponseErrors(t *testing.T) {
	h := newHarness(t, Config{})
	lg := h.logs.logger

	// A 404 whose only upstream evidence is the error still reached the
	// upstream, so the unrouted-404 rule must not drop it.
	w, err := lg.LogStreamingRequest("/v1/messages", "POST", nil, []byte(`{"model":"m"}`), "failed")
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteStatus(200, map[string][]string{"Content-Type": {"text/event-stream"}})
	apiErrors := []*interfaces.ErrorMessage{{StatusCode: 404, Error: errors.New("model not found")}}
	if err := w.WriteAPIResponseErrors(apiErrors); err != nil {
		t.Fatal(err)
	}
	// The writer keeps its own copy, and an empty call keeps what it has.
	apiErrors[0] = &interfaces.ErrorMessage{StatusCode: 500, Error: errors.New("caller reused its slice")}
	if err := w.WriteAPIResponseErrors(nil); err != nil {
		t.Fatal(err)
	}
	_ = w.WriteStatus(404, nil)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Without errors or other upstream evidence, a streamed 404 is unrouted.
	w2, _ := lg.LogStreamingRequest("/v1/messages", "POST", nil, []byte(`{"model":"m"}`), "unrouted")
	_ = w2.WriteStatus(404, nil)
	_ = w2.WriteAPIResponseErrors(nil)
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	h.flush()

	d, err := h.logs.Get(context.Background(), "failed", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Meta.Status != 404 || !d.Meta.Streaming || len(d.Upstream.Errors) != 1 ||
		d.Upstream.Errors[0].Status != 404 || d.Upstream.Errors[0].Message != "model not found" {
		t.Fatalf("failed stream: meta %+v errors %+v", d.Meta, d.Upstream.Errors)
	}
	_, raw, err := h.logs.Raw(context.Background(), "failed")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("=== API ERROR RESPONSE ===\nHTTP Status: 404\nmodel not found")) {
		t.Fatalf("raw text has no API error section:\n%s", raw)
	}
	if _, err := h.logs.Get(context.Background(), "unrouted", true); !errors.Is(err, errNotFound) {
		t.Fatalf("unrouted streamed 404 stored: %v", err)
	}
}

// TestFailedCompactionIsRetried makes one compaction fail and checks that the
// compactor's tick seals the segment once its backoff passes.
func TestFailedCompactionIsRetried(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	logSimple(t, h.logs.logger, "r", "/v1/messages", "s", 200, ts, []byte(`{}`))
	h.flush()
	var id int64
	var day string
	if err := h.db.QueryRow(`SELECT id, day FROM reqlog_segments`).Scan(&id, &day); err != nil {
		t.Fatal(err)
	}
	// A directory where the temp file goes makes the compressed write fail.
	blocker := h.logs.engine.segmentPath(day, id, ".zst") + ".tmp"
	if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.sealAll()
	if states := h.segmentStates(); states[segmentSealing] != 1 {
		t.Fatalf("segment states after failed compaction: %v", states)
	}
	if err := os.RemoveAll(blocker); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	e := h.logs.engine
	e.requeueFailed(ctx)
	e.compactQueued(ctx)
	if states := h.segmentStates(); states[segmentSealing] != 1 {
		t.Fatalf("retried before backoff: %v", states)
	}
	h.clock.set(ts.Add(compactRetryBase))
	e.requeueFailed(ctx)
	e.compactQueued(ctx)
	if states := h.segmentStates(); states[segmentSealed] != 1 || len(states) != 1 {
		t.Fatalf("segment states after retry: %v", states)
	}
	if len(e.retries) != 0 {
		t.Fatalf("retries left: %v", e.retries)
	}
	if _, err := h.logs.Get(ctx, "r", true); err != nil {
		t.Fatal(err)
	}
}

// TestPendingRowsForGoneSegmentsDoNotBlockIndexing covers a failed index
// commit whose rows point at a segment that no longer exists: the foreign key
// would fail every later commit if those rows were retried.
func TestPendingRowsForGoneSegmentsDoNotBlockIndexing(t *testing.T) {
	h := newHarness(t, Config{})
	e := h.logs.engine
	ghost := indexRow{meta: &recordMeta{RequestID: "ghost", TimeMs: h.clock.now().UnixMilli(), Method: "POST", Path: "/v1/messages"}, segment: 1 << 40, length: 10}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := e.do(ctx, func() { e.pending = append(e.pending, ghost) }); err != nil {
		t.Fatal(err)
	}
	logSimple(t, h.logs.logger, "after", "/v1/messages", "s", 200, h.clock.now(), []byte(`{}`))
	h.flush()
	if _, err := h.logs.Get(context.Background(), "after", true); err != nil {
		t.Fatalf("indexing stuck behind a row for a missing segment: %v", err)
	}
	if err := e.do(ctx, func() {
		if len(e.pending) != 0 {
			t.Errorf("pending rows left: %d", len(e.pending))
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScanFramesRejectsOversizedHeaders(t *testing.T) {
	meta := []byte(`{"requestId":"a","ts":1,"durationMs":0,"method":"GET","url":"/","path":"/","status":200,"requestBytes":0,"responseBytes":0}`)
	good := []byte(fmt.Sprintf("%s0 %d 0 0 2 0\n%sok\n", frameMagic, len(meta), meta))
	for name, tail := range map[string]string{
		"huge meta":         frameMagic + "0 9223372036854775807 0 0 0 0\n{}",
		"meta past end":     frameMagic + "0 1000000 0 0 0 0\n{}",
		"huge skeleton":     frameMagic + fmt.Sprintf("0 %d 0 0 9223372036854775807 0\n%s", len(meta), meta),
		"skeleton past end": frameMagic + fmt.Sprintf("0 %d 0 0 100 0\n%sok\n", len(meta), meta),
		"meta over limit":   frameMagic + fmt.Sprintf("0 %d 0 0 0 0\n", maxFrameMeta+1),
		"unknown flags":     frameMagic + fmt.Sprintf("8 %d 0 0 2 0\n%sok\n", len(meta), meta),
		"torn refs":         frameMagic + fmt.Sprintf("0 %d 3 0 2 0\n%s\x01abok\n", len(meta), meta),
		"too few fields":    frameMagic + fmt.Sprintf("%d 2\n%sok\n", len(meta), meta),
		"overflowing sum":   frameMagic + fmt.Sprintf("0 %d 0 4611686018427387904 4611686018427387904 4611686018427387904\n%s", len(meta), meta),
	} {
		t.Run(name, func(t *testing.T) {
			data := append(append([]byte(nil), good...), tail...)
			var seen int
			end, err := scanFrames(bytes.NewReader(data), 0, int64(len(data)), func(int64, int, *recordMeta, []blobHash) { seen++ })
			if err != nil || end != int64(len(good)) || seen != 1 {
				t.Fatalf("end %d seen %d err %v; want %d 1 nil", end, seen, err, len(good))
			}
			if _, _, _, err := decodeFrame([]byte(tail)); err == nil {
				t.Fatal("decodeFrame accepted a bad frame")
			}
		})
	}
}

func TestDeniedRequestsAreNotStored(t *testing.T) {
	h := newHarness(t, Config{})
	lg := h.logs.logger
	ts := h.clock.now()
	denied := []byte(`{"error":{"code":"forbidden","message":"caller is not allowed"}}`)
	if err := lg.LogRequest("/v1/messages", "POST", map[string][]string{"X-Anything": {"attacker"}}, []byte(`{"model":"m"}`), 403,
		map[string][]string{"Content-Type": {"application/json"}}, denied, nil, nil, nil, nil, nil, "denied", ts, ts); err != nil {
		t.Fatal(err)
	}
	// An upstream 403 reached the provider and is a real answer.
	if err := lg.LogRequest("/v1/messages", "POST", nil, []byte(`{"model":"m"}`), 403, nil, []byte(`{"error":"nope"}`), nil,
		apiRequestText([]byte(`{"model":"m"}`)), apiResponseText(`{"error":"nope"}`), nil, nil, "upstream-403", ts, ts); err != nil {
		t.Fatal(err)
	}
	h.flush()
	if _, err := h.logs.Get(context.Background(), "denied", true); !errors.Is(err, errNotFound) {
		t.Fatalf("denied request stored: %v", err)
	}
	if _, err := h.logs.Get(context.Background(), "upstream-403", true); err != nil {
		t.Fatalf("upstream 403 dropped: %v", err)
	}
}

func TestAPIWebsocketTimelineIsRedacted(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	timeline := []byte("Timestamp: 2026-11-02T12:00:00Z\nEvent: api.websocket.request\nUpstream URL: wss://chatgpt.com/x\nHeaders:\n" +
		"Authorization: Bear****cret\nChatgpt-Account-Id: acct\nCookie: __cf_bm=" + secretToken + "\n\nBody:\n{\"type\":\"response.create\"}\n" +
		"Timestamp: 2026-11-02T12:00:01Z\nEvent: api.websocket.handshake\nStatus: 101\nHeaders:\nSet-Cookie: __cf_bm=" + secretToken + "\nX-Codex-Primary-Used-Percent: 12\n\n")
	if err := h.logs.logger.LogRequest("/v1/responses", "GET", nil, nil, 101, nil, nil, nil, nil, nil, timeline, nil, "ws", ts, ts); err != nil {
		t.Fatal(err)
	}
	h.flush()
	d, err := h.logs.Get(context.Background(), "ws", true)
	if err != nil {
		t.Fatal(err)
	}
	got := d.Upstream.APIWebsocketTimeline
	for _, secret := range []string{secretToken, "Bear****cret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("timeline keeps %q:\n%s", secret, got)
		}
	}
	for _, kept := range []string{"Chatgpt-Account-Id: acct", "X-Codex-Primary-Used-Percent: 12", `{"type":"response.create"}`, "Cookie: " + redacted, "Set-Cookie: " + redacted} {
		if !strings.Contains(got, kept) {
			t.Fatalf("timeline lost %q:\n%s", kept, got)
		}
	}
}
