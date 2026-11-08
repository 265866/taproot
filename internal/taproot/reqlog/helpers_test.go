package reqlog

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
)

// clock is a settable time source shared by the logger and engine.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

type harness struct {
	t       *testing.T
	dataDir string
	db      *store.DB
	clock   *clock
	logs    *Logs
	cancel  context.CancelFunc
}

func openStore(t *testing.T, dataDir string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Config{}, dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return db
}

// newHarness opens a real SQLite store and a logger whose writer runs; sealing
// and retention are driven by the test.
func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	h := newIdleHarness(t, cfg)
	h.startWriter()
	return h
}

// newIdleHarness is newHarness with the writer not yet running, so records
// queue up until startWriter.
func newIdleHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	dataDir := t.TempDir()
	db := openStore(t, dataDir)
	h := &harness{t: t, dataDir: dataDir, db: db, clock: &clock{t: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}}
	h.prepare(cfg)
	t.Cleanup(func() {
		h.stop()
		_ = h.db.Close()
	})
	return h
}

func (h *harness) open(cfg Config) {
	h.t.Helper()
	h.prepare(cfg)
	h.startWriter()
}

func (h *harness) prepare(cfg Config) {
	h.t.Helper()
	logs, err := newLogs(context.Background(), cfg, h.db, h.dataDir, h.clock.now)
	if err != nil {
		h.t.Fatalf("new logs: %v", err)
	}
	logs.logger.SetEnabled(true)
	h.logs = logs
}

func (h *harness) startWriter() {
	ctx, cancel := context.WithCancel(context.Background())
	h.logs.engine.startWriter(ctx)
	h.cancel = cancel
}

// stop ends the writer the way a shutdown would, leaving open segments raw.
func (h *harness) stop() {
	if h.cancel != nil {
		h.cancel()
		h.logs.engine.wait()
		h.cancel = nil
	}
}

func (h *harness) flush() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := h.logs.engine.flush(ctx); err != nil {
		h.t.Fatalf("flush: %v", err)
	}
}

// sealAll seals every open segment and compresses it.
func (h *harness) sealAll() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := h.logs.engine.do(ctx, h.logs.engine.sealAll); err != nil {
		h.t.Fatalf("seal: %v", err)
	}
	h.logs.engine.compactQueued(ctx)
}

func (h *harness) segmentStates() map[string]int {
	h.t.Helper()
	rows, err := h.db.Query(`SELECT state, COUNT(*) FROM reqlog_segments GROUP BY state`)
	if err != nil {
		h.t.Fatalf("segment states: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			h.t.Fatalf("scan: %v", err)
		}
		out[state] = n
	}
	return out
}

func (l *Logs) locateForTest(requestID string) (recordLocation, error) {
	_, loc, err := l.locate(context.Background(), requestID)
	return loc, err
}
