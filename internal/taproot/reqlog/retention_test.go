package reqlog

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// imageBody is a request carrying size bytes of incompressible base64, which
// the writer stores as a blob.
func imageBody(seed uint64, size int) []byte {
	raw := make([]byte, size)
	r := rand.NewChaCha8([32]byte{byte(seed), byte(seed >> 8)})
	_, _ = r.Read(raw)
	return []byte(`{"model":"m","image":"` + base64.StdEncoding.EncodeToString(raw) + `"}`)
}

func setMaxBytes(t *testing.T, l *Logs, n int64) {
	t.Helper()
	r := l.Retention()
	r.MaxBytes = ByteSize(n)
	if err := l.SetRetention(r); err != nil {
		t.Fatal(err)
	}
}

func recordIDs(t *testing.T, h *harness) []string {
	t.Helper()
	rows, err := h.db.Query(`SELECT request_id FROM reqlog_records ORDER BY ts`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestSizeRetentionDeletesOldestFirst covers a long session whose segment
// holds both the oldest and the newest record. Blob GC cannot pass the oldest
// record, so that segment must go first; deleting the newer short segments
// ahead of it frees no blobs and empties the store.
func TestSizeRetentionDeletesOldestFirst(t *testing.T) {
	h := newHarness(t, Config{MaxDays: -1, MaxBytes: -1})
	t0 := h.clock.now().Add(-10 * time.Hour)
	logSimple(t, h.logs.logger, "A-old", "/v1/messages", "A", 200, t0, imageBody(0, 200<<10))
	for i := 1; i <= 4; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("B%d", i), "/v1/messages", fmt.Sprintf("B%d", i), 200, t0.Add(time.Duration(i)*time.Hour), imageBody(uint64(i), 200<<10))
	}
	logSimple(t, h.logs.logger, "A-new", "/v1/messages", "A", 200, t0.Add(5*time.Hour), []byte(`{"model":"m"}`))
	h.flush()
	h.sealAll()
	ctx := context.Background()
	total, err := h.logs.engine.storedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	limit := total - 150<<10
	setMaxBytes(t, h.logs, limit)
	h.logs.engine.applyRetention(ctx)

	if got, want := recordIDs(t, h), []string{"B1", "B2", "B3", "B4"}; !slices.Equal(got, want) {
		t.Fatalf("records after a 150 KiB overage: %v, want %v", got, want)
	}
	after, err := h.logs.engine.storedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after > limit {
		t.Fatalf("stored %d bytes, over the %d limit", after, limit)
	}
}

// TestSizeRetentionDropsABatch halves max-bytes over many single-record
// segments and checks that one pass deletes exactly the oldest ones.
func TestSizeRetentionDropsABatch(t *testing.T) {
	h := newHarness(t, Config{MaxDays: -1, MaxBytes: -1})
	t0 := h.clock.now().Add(-5 * time.Hour)
	const n = 40
	for i := 0; i < n; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("r%02d", i), "/v1/messages", fmt.Sprintf("s%d", i), 200, t0.Add(time.Duration(i)*time.Minute), imageBody(uint64(100+i), 4<<10))
	}
	h.flush()
	h.sealAll()
	ctx := context.Background()
	total, err := h.logs.engine.storedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	setMaxBytes(t, h.logs, total/2)
	h.logs.engine.applyRetention(ctx)
	after, err := h.logs.engine.storedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := recordIDs(t, h)
	if after > total/2 || len(ids) < n/2-2 {
		t.Fatalf("kept %d records in %d bytes for a %d limit", len(ids), after, total/2)
	}
	if ids[len(ids)-1] != fmt.Sprintf("r%02d", n-1) || ids[0] != fmt.Sprintf("r%02d", n-len(ids)) {
		t.Fatalf("kept %v, want the newest", ids)
	}
}

// TestSizeRetentionWaitsForBlobGC covers blobs older than every record,
// left by a GC that was skipped: they are already garbage, so retention must
// free them rather than delete history.
func TestSizeRetentionWaitsForBlobGC(t *testing.T) {
	h := newHarness(t, Config{MaxDays: -1, MaxBytes: -1})
	t0 := h.clock.now().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("r%d", i), "/v1/messages", fmt.Sprintf("s%d", i), 200, t0.Add(time.Duration(i)*time.Minute), imageBody(uint64(200+i), 64<<10))
	}
	h.flush()
	h.sealAll()
	ctx := context.Background()
	total, err := h.logs.engine.storedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// One blob outlived the records that used it.
	if _, err := h.db.Exec(`UPDATE reqlog_blobs SET last_ts = ? WHERE hash = (SELECT hash FROM reqlog_blobs LIMIT 1)`, t0.Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	setMaxBytes(t, h.logs, total-(32<<10))
	h.logs.engine.applyRetention(ctx)
	if got := len(recordIDs(t, h)); got != 3 {
		t.Fatalf("%d records left, want all 3", got)
	}
}

// TestOpenSegmentsCountCompressed covers a busy open session at the limit:
// its raw bytes shrink when it seals, so they must not push sealed history
// out at raw size.
func TestOpenSegmentsCountCompressed(t *testing.T) {
	h := newHarness(t, Config{MaxDays: -1, MaxBytes: -1})
	t0 := h.clock.now().Add(-3 * time.Hour)
	small := []byte(`{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("hello world ", 200) + `"}]}`)
	for i := 0; i < 5; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("old%d", i), "/v1/messages", fmt.Sprintf("old%d", i), 200, t0.Add(time.Duration(i)*time.Minute), small)
	}
	h.flush()
	h.sealAll()
	big := []byte(`{"model":"m","messages":[{"role":"user","content":"` + strings.Repeat("the quick brown fox jumps over the lazy dog ", 25000) + `"}]}`)
	logSimple(t, h.logs.logger, "busy", "/v1/messages", "busy", 200, h.clock.now(), big)
	h.flush()
	setMaxBytes(t, h.logs, 1<<20)
	ctx := context.Background()
	h.logs.engine.applyRetention(ctx)
	if got := len(recordIDs(t, h)); got != 6 {
		t.Fatalf("%d records left, want all 6", got)
	}
	h.sealAll()
	if after, err := h.logs.engine.storedBytes(ctx); err != nil || after > 1<<20 {
		t.Fatalf("stored %d bytes after the busy segment sealed (err %v)", after, err)
	}
}

// TestStoredBytesCountsDictionaries checks that dictionaries, which live in
// the request log directory, count toward max-bytes.
func TestStoredBytesCountsDictionaries(t *testing.T) {
	h := newHarness(t, Config{})
	ctx := context.Background()
	before, err := h.logs.engine.storedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.logs.engine.dicts.add(bytes.Repeat([]byte("d"), 8192)); err != nil {
		t.Fatal(err)
	}
	after, err := h.logs.engine.storedBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after-before != 8192 {
		t.Fatalf("a dictionary added %d bytes, want 8192", after-before)
	}
}

// TestOpenSegmentCountIsCapped sends one record each for many distinct
// session ids. Each open segment holds a file descriptor, so their number is
// bounded; the least recently written seal first.
func TestOpenSegmentCountIsCapped(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	const sessions = maxOpenSegments + 40
	for i := 0; i < sessions; i++ {
		h.clock.set(ts.Add(time.Duration(i) * time.Second))
		logSimple(t, h.logs.logger, fmt.Sprintf("r%d", i), "/v1/messages", fmt.Sprintf("sess-%d", i), 200, h.clock.now(), []byte(`{"model":"m"}`))
		if i%100 == 99 {
			h.flush()
		}
	}
	h.flush()
	e := h.logs.engine
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var open int
	var oldestOpen bool
	if err := e.do(ctx, func() {
		open = len(e.open)
		_, oldestOpen = e.open["sess-0\x00"+utcDay(ts.UnixMilli())]
	}); err != nil {
		t.Fatal(err)
	}
	if open > maxOpenSegments || oldestOpen {
		t.Fatalf("%d open segments (cap %d), least recent still open: %v", open, maxOpenSegments, oldestOpen)
	}
	e.compactQueued(ctx)
	if n := e.stats.writeErrors.Load(); n != 0 {
		t.Fatalf("write errors: %d", n)
	}
	for _, id := range []string{"r0", fmt.Sprintf("r%d", sessions-1)} {
		if _, err := h.logs.Get(ctx, id, true); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
}

// TestRestoreKeepsSealedCopyWhenDecodeFails covers a restored database that
// lists as open a segment sealed after its snapshot, when the sealed copy
// cannot be decoded yet: it is the only copy, so it must stay for a later
// start.
func TestRestoreKeepsSealedCopyWhenDecodeFails(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	dictID, err := h.logs.engine.dicts.add(bytes.Repeat([]byte(`{"model":"m","messages":[]}`), 400))
	if err != nil {
		t.Fatal(err)
	}
	logSimple(t, h.logs.logger, "open-at-snapshot", "/v1/messages", "s", 200, ts, []byte(`{"n":1}`))
	h.flush()
	snapshot := filepath.Join(t.TempDir(), "snap.db")
	if err := h.db.Snapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	h.sealAll()
	zsts, _ := filepath.Glob(filepath.Join(h.dataDir, "request-logs", "*", "*.zst"))
	if len(zsts) != 1 {
		t.Fatalf("sealed files %v", zsts)
	}
	dictPath := h.logs.engine.dicts.path(dictID)
	hidden := dictPath + ".hidden"
	if err := os.Rename(dictPath, hidden); err != nil {
		t.Fatal(err)
	}

	h.restoreSnapshot(snapshot)
	h.logs.engine.compactQueued(context.Background())
	if _, err := os.Stat(zsts[0]); err != nil {
		t.Fatalf("only sealed copy removed: %v", err)
	}

	if err := os.Rename(hidden, dictPath); err != nil {
		t.Fatal(err)
	}
	h.stop()
	h.open(Config{})
	h.logs.engine.compactQueued(context.Background())
	d, err := h.logs.Get(context.Background(), "open-at-snapshot", true)
	if err != nil {
		t.Fatalf("record lost: %v", err)
	}
	if d.Request.Body != `{"n":1}` {
		t.Fatalf("body %q", d.Request.Body)
	}
	if states := h.segmentStates(); states[segmentSealed] != 1 || len(states) != 1 {
		t.Fatalf("segments: %v", states)
	}
}

// TestFailedCompactionRetriedWhenNeverMarkedSealing covers a seal whose
// state update failed, so the row still says open, followed by a failed
// compaction: the retry must still run.
func TestFailedCompactionRetriedWhenNeverMarkedSealing(t *testing.T) {
	h := newHarness(t, Config{})
	logSimple(t, h.logs.logger, "r", "/v1/messages", "s", 200, h.clock.now(), []byte(`{}`))
	h.flush()
	e := h.logs.engine
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var id int64
	if err := e.do(ctx, func() {
		for _, seg := range e.open {
			id = seg.id
			delete(e.open, seg.sessionID+"\x00"+seg.day)
			_ = seg.file.Close()
		}
	}); err != nil {
		t.Fatal(err)
	}
	e.retries[id] = compactRetry{attempts: 1, next: h.clock.now()}
	e.requeueFailed(ctx)
	e.compactQueued(ctx)
	if states := h.segmentStates(); states[segmentSealed] != 1 || len(states) != 1 {
		t.Fatalf("segment states after retry: %v", states)
	}
	if _, err := h.logs.Get(ctx, "r", true); err != nil {
		t.Fatal(err)
	}
}

// TestDictionaryExpiresWithRetention covers record text kept in a
// dictionary: past max-days the dictionary stops being used for new
// segments and goes once no segment uses it, and with no segments left no
// dictionary stays.
func TestDictionaryExpiresWithRetention(t *testing.T) {
	h := newHarness(t, Config{MaxDays: 2})
	e := h.logs.engine
	ctx := context.Background()
	now := h.clock.now()
	old, err := e.dicts.add(bytes.Repeat([]byte(`{"model":"m","messages":[]}`), 400))
	if err != nil {
		t.Fatal(err)
	}
	ageDict := func(id int64, age time.Duration) {
		t.Helper()
		at := now.Add(-age)
		if err := os.Chtimes(e.dicts.path(id), at, at); err != nil {
			t.Fatal(err)
		}
	}
	ageDict(old, 3*24*time.Hour)

	logSimple(t, h.logs.logger, "r", "/v1/messages", "s", 200, now, []byte(`{"model":"m"}`))
	h.flush()
	h.sealAll()
	var used int64
	if err := h.db.QueryRow(`SELECT dict_id FROM reqlog_segments`).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatalf("new segment sealed with dictionary %d older than max-days", used)
	}
	if err := e.dicts.prune(ctx, now, 2*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.dicts.ids(); len(ids) != 0 {
		t.Fatalf("expired unused dictionaries kept: %v", ids)
	}

	// A current dictionary stays while segments exist and goes with the last.
	fresh, err := e.dicts.add(bytes.Repeat([]byte(`{"model":"n","messages":[]}`), 400))
	if err != nil {
		t.Fatal(err)
	}
	ageDict(fresh, time.Hour)
	if err := e.dicts.prune(ctx, now, 2*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.dicts.ids(); len(ids) != 1 {
		t.Fatalf("current dictionary pruned while segments exist: %v", ids)
	}
	if err := e.deleteSegments(ctx, mustIDs(t, h, `SELECT id FROM reqlog_segments`)); err != nil {
		t.Fatal(err)
	}
	if err := e.dicts.prune(ctx, now, 0); err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.dicts.ids(); len(ids) != 0 {
		t.Fatalf("dictionaries kept with no segments left: %v", ids)
	}
}

func mustIDs(t *testing.T, h *harness, query string) []int64 {
	t.Helper()
	ids, err := queryIDs(context.Background(), h.db, query)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// TestCompressStopsWhenCanceled checks that shutdown does not wait for a
// segment's compression to finish; the raw file stays authoritative.
func TestCompressStopsWhenCanceled(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "1.raw")
	content := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 1<<16)
	if err := os.WriteFile(raw, content, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := compressFile(ctx, raw, filepath.Join(dir, "1.zst"), int64(len(content)), 0, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("compress after cancel: %v", err)
	}
}
