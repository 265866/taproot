package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrationsOnceAndSnapshots(t *testing.T) {
	Register(Migration{Module: "storetest", Version: 1, SQL: `CREATE TABLE storetest_items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`})
	Register(Migration{Module: "storetest", Version: 2, SQL: `ALTER TABLE storetest_items ADD COLUMN note TEXT NOT NULL DEFAULT ''`})

	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, Config{}, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got, want := db.Path(), filepath.Join(dir, "taproot.db"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO storetest_items (name, note) VALUES ('a', 'b')`); err != nil {
		t.Fatalf("insert after migrations: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening must not reapply version 1, which would fail on the existing table.
	db, err = Open(ctx, Config{}, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db.Close() }()
	var applied int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE module = 'storetest'`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 2 {
		t.Fatalf("applied migrations = %d, want 2", applied)
	}

	dest := filepath.Join(t.TempDir(), "snap.db")
	if err := db.Snapshot(ctx, dest); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := db.Snapshot(ctx, dest); err == nil {
		t.Fatal("snapshot over an existing file succeeded, want error")
	}
	snap, err := Open(ctx, Config{Path: dest}, dir)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = snap.Close() }()
	var name string
	if err := snap.QueryRowContext(ctx, `SELECT name FROM storetest_items`).Scan(&name); err != nil || name != "a" {
		t.Fatalf("snapshot row = %q, %v; want a", name, err)
	}
}

func TestOpenRejectsBrokenMigration(t *testing.T) {
	Register(Migration{Module: "storetest_broken", Version: 1, SQL: `CREATE TABLE nope (`})
	t.Cleanup(func() {
		registryMu.Lock()
		defer registryMu.Unlock()
		kept := registry[:0]
		for _, m := range registry {
			if m.Module != "storetest_broken" {
				kept = append(kept, m)
			}
		}
		registry = kept
	})
	dir := t.TempDir()
	if _, err := Open(context.Background(), Config{}, dir); err == nil {
		t.Fatal("open with a broken migration succeeded, want error")
	}
	if _, err := os.Stat(filepath.Join(dir, "taproot.db")); err != nil {
		t.Fatalf("db file missing after failed migration: %v", err)
	}
}
