package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// registerForTest registers s until the test ends.
func registerForTest(t *testing.T, s Schema) {
	t.Helper()
	Register(s)
	t.Cleanup(func() {
		registryMu.Lock()
		defer registryMu.Unlock()
		registry = slices.DeleteFunc(registry, func(r Schema) bool { return r.Module == s.Module })
	})
}

func TestOpenAppliesSchemaSeedsNewTablesAndSnapshots(t *testing.T) {
	registerForTest(t, Schema{
		Module: "storetest",
		SQL:    `CREATE TABLE IF NOT EXISTS storetest_items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		Seed: func(ctx context.Context, tx *sql.Tx, created bool) error {
			name := "reopened"
			if created {
				name = "seed"
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO storetest_items (name) VALUES (?)`, name)
			return err
		},
	})

	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, Config{}, dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got, want := db.Path(), filepath.Join(dir, "taproot.db"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO storetest_items (name) VALUES ('a')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(ctx, Config{}, dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = db.Close() }()
	var names []string
	rows, err := db.QueryContext(ctx, `SELECT name FROM storetest_items ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"seed", "a", "reopened"}) {
		t.Fatalf("rows after reopen = %q, want the created seed, the inserted row, then the reopened seed", names)
	}
	var tracked int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'schema_migrations'`).Scan(&tracked); err != nil {
		t.Fatal(err)
	}
	if tracked != 0 {
		t.Fatal("open created schema_migrations")
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
	var count int
	if err := snap.QueryRowContext(ctx, `SELECT COUNT(*) FROM storetest_items`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("snapshot rows = %d, %v; want the 3 snapshotted rows and one reopened seed", count, err)
	}
}

func TestOpenRejectsBrokenSchema(t *testing.T) {
	registerForTest(t, Schema{Module: "storetest_broken", SQL: `CREATE TABLE nope (`})
	if _, err := Open(context.Background(), Config{}, t.TempDir()); err == nil {
		t.Fatal("open with a broken schema succeeded, want error")
	}
}

// TestOpenRefusesMissingDeclaredColumns declares two columns, one indexed,
// that an existing table lacks, and checks Open names both and changes nothing.
func TestOpenRefusesMissingDeclaredColumns(t *testing.T) {
	registerForTest(t, Schema{Module: "storetest_drift", SQL: `
CREATE TABLE IF NOT EXISTS storetest_drift (id INTEGER PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL DEFAULT '', note TEXT);
CREATE INDEX IF NOT EXISTS storetest_drift_kind ON storetest_drift (kind);
CREATE TABLE IF NOT EXISTS storetest_drift_new (id INTEGER PRIMARY KEY);`})
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "taproot.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`CREATE TABLE storetest_drift (id INTEGER PRIMARY KEY, name TEXT NOT NULL, extra TEXT)`); err != nil {
		t.Fatal(err)
	}

	db, err := Open(context.Background(), Config{}, dir)
	if err == nil {
		_ = db.Close()
		t.Fatal("open succeeded on a table missing declared columns")
	}
	if msg := err.Error(); !strings.Contains(msg, "storetest_drift (kind, note)") || !strings.Contains(msg, "by hand") {
		t.Fatalf("error = %q, want it to name storetest_drift (kind, note) and say to apply the change by hand", msg)
	}
	var objects int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	if objects != 1 {
		t.Fatalf("refused open left %d schema objects, want only the original table", objects)
	}
}

func TestOpenKeepsTempStructuresInMemory(t *testing.T) {
	db, err := Open(context.Background(), Config{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(3)
	conns := make([]*sql.Conn, 0, 3)
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for n := 0; n < 3; n++ {
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var mode int
		if err := c.QueryRowContext(context.Background(), `PRAGMA temp_store`).Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if mode != 2 {
			t.Fatalf("connection %d temp_store = %d, want 2 (memory)", n, mode)
		}
	}
}

func TestCheckFileReportsMissingColumnsWithoutChangingTheFile(t *testing.T) {
	registerForTest(t, Schema{Module: "storetest_check", SQL: `CREATE TABLE IF NOT EXISTS storetest_check (id INTEGER PRIMARY KEY, kind TEXT)`})
	path := filepath.Join(t.TempDir(), "snapshot.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE storetest_check (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := CheckFile(ctx, path); err == nil || !strings.Contains(err.Error(), "storetest_check (kind)") {
		t.Fatalf("CheckFile = %v, want it to name storetest_check (kind)", err)
	}
	// SQLite names are case-insensitive, so a column added by hand in another
	// case satisfies the declaration.
	if _, err := raw.Exec(`ALTER TABLE storetest_check ADD COLUMN Kind TEXT`); err != nil {
		t.Fatal(err)
	}
	if err := CheckFile(ctx, path); err != nil {
		t.Fatalf("CheckFile after adding the column: %v", err)
	}
	var objects int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&objects); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if objects != 1 {
		t.Fatalf("CheckFile left %d schema objects, want only the original table", objects)
	}
}
