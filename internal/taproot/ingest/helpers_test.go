package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Config{}, t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return db
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestIngest(t *testing.T, cfg Config) (*Ingest, *fakeClock) {
	t.Helper()
	i, err := New(cfg, openTestDB(t))
	if err != nil {
		t.Fatalf("new ingest: %v", err)
	}
	clock := &fakeClock{t: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	i.now = clock.now
	return i, clock
}

func mustFlush(t *testing.T, i *Ingest) {
	t.Helper()
	if err := i.flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func queryInt(t *testing.T, i *Ingest, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := i.db.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}
