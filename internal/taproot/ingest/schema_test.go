package ingest

import (
	"context"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/taproot/store"
)

func openStore(t *testing.T, dir string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.Config{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func exec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func pricesByPattern(t *testing.T, db *store.DB) map[string]Price {
	t.Helper()
	prices, err := loadPrices(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Price{}
	for _, p := range prices {
		out[p.ModelPattern] = p
	}
	return out
}

func seedVersion(t *testing.T, db *store.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRowContext(context.Background(), `SELECT version FROM price_seed WHERE id = 1`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPriceSeedKeepsDeletedAndEditedRowsOnReopen(t *testing.T) {
	dir := t.TempDir()
	db := openStore(t, dir)
	prices := pricesByPattern(t, db)
	if len(prices) != len(seedPrices) || len(prices) == 0 {
		t.Fatalf("fresh database has %d prices, want the %d shipped", len(prices), len(seedPrices))
	}
	if v := seedVersion(t, db); v != priceSeedVersion {
		t.Fatalf("fresh database seed version %d, want %d", v, priceSeedVersion)
	}
	deleted, edited := seedPrices[0].ModelPattern, seedPrices[1].ModelPattern
	exec(t, db, `DELETE FROM prices WHERE model_pattern = ?`, deleted)
	exec(t, db, `UPDATE prices SET input = 99, source = 'edited in dashboard' WHERE model_pattern = ?`, edited)
	exec(t, db, `DELETE FROM prices WHERE model_pattern = ?`, "claude-opus-4-5*")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openStore(t, dir)
	t.Cleanup(func() { _ = db.Close() })
	prices = pricesByPattern(t, db)
	if len(prices) != len(seedPrices)-2 {
		t.Fatalf("reopened database has %d prices, want %d", len(prices), len(seedPrices)-2)
	}
	for _, gone := range []string{deleted, "claude-opus-4-5*"} {
		if _, ok := prices[gone]; ok {
			t.Fatalf("deleted price %q came back on reopen", gone)
		}
	}
	if p := prices[edited]; p.Input != 99 || p.Source != "edited in dashboard" {
		t.Fatalf("edited price changed on reopen: %+v", p)
	}
}

// A database from an earlier seed version gets the rows added since, once,
// and keeps its edits and deletions of older rows.
func TestPriceSeedUpgradeAddsNewRowsOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		// downgrade makes a fresh database look like one from version 1.
		downgrade string
	}{
		{"version 1", `UPDATE price_seed SET version = 1`},
		{"before seed versions", `DELETE FROM price_seed`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			db := openStore(t, dir)
			exec(t, db, tc.downgrade)
			added := map[string]Price{}
			for _, p := range seedPrices {
				if seedPriceAddedIn[p.ModelPattern] > 1 {
					added[p.ModelPattern] = p
					exec(t, db, `DELETE FROM prices WHERE model_pattern = ?`, p.ModelPattern)
				}
			}
			if len(added) == 0 {
				t.Fatal("no shipped row is newer than version 1")
			}
			deleted, edited := "gpt-5", "gpt-6-sol"
			exec(t, db, `DELETE FROM prices WHERE model_pattern = ?`, deleted)
			exec(t, db, `UPDATE prices SET output = 77, source = 'edited in dashboard', updated_at = '2026-01-01T00:00:00Z' WHERE model_pattern = ?`, edited)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			db = openStore(t, dir)
			t.Cleanup(func() { _ = db.Close() })
			prices := pricesByPattern(t, db)
			for pattern, want := range added {
				got, ok := prices[pattern]
				if !ok {
					t.Fatalf("upgrade did not add %q", pattern)
				}
				if got.Input != want.Input || got.Output != want.Output || got.CacheRead != want.CacheRead ||
					got.CacheWrite5m != want.CacheWrite5m || got.CacheWrite1h != want.CacheWrite1h || got.Source != want.Source || got.UpdatedAt == "" {
					t.Fatalf("added %q = %+v, want %+v", pattern, got, want)
				}
			}
			if _, ok := prices[deleted]; ok {
				t.Fatalf("upgrade restored deleted version-1 row %q", deleted)
			}
			if p := prices[edited]; p.Output != 77 || p.Source != "edited in dashboard" || p.UpdatedAt != "2026-01-01T00:00:00Z" {
				t.Fatalf("upgrade changed edited row: %+v", p)
			}
			if len(prices) != len(seedPrices)-1 {
				t.Fatalf("upgraded database has %d prices, want %d", len(prices), len(seedPrices)-1)
			}
			if v := seedVersion(t, db); v != priceSeedVersion {
				t.Fatalf("seed version after upgrade %d, want %d", v, priceSeedVersion)
			}
		})
	}
}

// A database a newer release seeded keeps its version and rows when an
// older release starts on it.
func TestPriceSeedLeavesNewerVersionAlone(t *testing.T) {
	dir := t.TempDir()
	db := openStore(t, dir)
	exec(t, db, `UPDATE price_seed SET version = ?`, priceSeedVersion+1)
	exec(t, db, `DELETE FROM prices`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openStore(t, dir)
	t.Cleanup(func() { _ = db.Close() })
	if n := len(pricesByPattern(t, db)); n != 0 {
		t.Fatalf("older seed added %d rows to a newer database", n)
	}
	if v := seedVersion(t, db); v != priceSeedVersion+1 {
		t.Fatalf("seed version %d, want %d", v, priceSeedVersion+1)
	}
}

func TestShippedPricesAreConsistent(t *testing.T) {
	patterns := map[string]bool{}
	for _, p := range seedPrices {
		if patterns[p.ModelPattern] {
			t.Fatalf("duplicate shipped pattern %q", p.ModelPattern)
		}
		patterns[p.ModelPattern] = true
		if !strings.Contains(p.Source, "platform.claude.com/docs/en/about-claude/pricing") &&
			!strings.Contains(p.Source, "developers.openai.com/api/docs/pricing") {
			t.Errorf("%s source %q does not cite a provider pricing page", p.ModelPattern, p.Source)
		}
		if strings.Contains(strings.ToLower(p.Source), "promo") {
			t.Errorf("%s source %q cites a promotional rate", p.ModelPattern, p.Source)
		}
	}
	if err := validatePrices(seedPrices); err != nil {
		t.Fatal(err)
	}
	newest := 1
	for pattern, v := range seedPriceAddedIn {
		if !patterns[pattern] {
			t.Errorf("seedPriceAddedIn names %q, which does not ship", pattern)
		}
		if v < 2 || v > priceSeedVersion {
			t.Errorf("%q added in version %d, want 2..%d", pattern, v, priceSeedVersion)
		}
		newest = max(newest, v)
	}
	if newest != priceSeedVersion {
		t.Errorf("priceSeedVersion is %d, but the newest row is from version %d", priceSeedVersion, newest)
	}
}
