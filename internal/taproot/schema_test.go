package taproot

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/265866/taproot/internal/taproot/store"
)

// Every module's schema must apply again to a database it already built, or
// the second start of a real deployment fails.
func TestModuleSchemasReopenUnchanged(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	var before []string
	for i := range 2 {
		db, err := store.Open(ctx, store.Config{}, dir)
		if err != nil {
			t.Fatalf("open %d: %v", i+1, err)
		}
		got := schemaSQL(t, db.DB)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			before = got
			continue
		}
		if !slices.Equal(got, before) {
			t.Fatalf("reopen changed the schema:\nfirst  %q\nsecond %q", before, got)
		}
	}
	for _, table := range []string{"usage_events", "reqlog_records", "alert_state", "prices"} {
		if !slices.ContainsFunc(before, func(s string) bool { return s == "table "+table }) {
			t.Errorf("schema has no %s table", table)
		}
	}
}

func schemaSQL(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT type || ' ' || name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name, text string
		if err := rows.Scan(&name, &text); err != nil {
			t.Fatal(err)
		}
		out = append(out, name, text)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
