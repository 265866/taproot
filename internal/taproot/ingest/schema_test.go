package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

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

// writeVersion1Table makes db look like one version 1 seeded: the version-1
// rows, written the way that release's seed SQL wrote them.
func writeVersion1Table(t *testing.T, db *store.DB) {
	t.Helper()
	exec(t, db, `DELETE FROM prices`)
	for _, p := range priceSeedV1 {
		exec(t, db, fmt.Sprintf("INSERT INTO prices (model_pattern, input, output, cache_read, cache_write_5m, cache_write_1h, source, updated_at) VALUES (?, %g, %g, %g, %g, %g, ?, '2026-11-02T00:00:00Z')",
			p.Input, p.Output, p.CacheRead, p.CacheWrite5m, p.CacheWrite1h), p.ModelPattern, p.Source)
	}
}

// A database from version 1 gets the rows added since and the corrected
// rows, once, and keeps its edits, additions, and deletions.
func TestPriceSeedUpgradeFromVersion1(t *testing.T) {
	for _, tc := range []struct {
		name string
		// downgrade sets the seed version a version-1 database has.
		downgrade string
	}{
		{"version 1", `UPDATE price_seed SET version = 1`},
		{"before seed versions", `DELETE FROM price_seed`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			db := openStore(t, dir)
			writeVersion1Table(t, db)
			exec(t, db, tc.downgrade)

			deleted, edited, editedBack, custom := "gpt-5", "gpt-6-sol", "claude-sonnet-5-5*", "my-model"
			exec(t, db, `DELETE FROM prices WHERE model_pattern = ?`, deleted)
			table, err := loadPrices(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			editedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := range table {
				switch table[i].ModelPattern {
				case edited:
					table[i].Output = 77
				case editedBack:
					table[i].CacheRead = 0.3
				}
			}
			table = append(table, Price{ModelPattern: custom, Input: 1, Output: 2})
			if err := replacePrices(ctx, db.DB, table, editedAt); err != nil {
				t.Fatal(err)
			}
			for i := range table {
				if table[i].ModelPattern == editedBack {
					table[i].CacheRead = 0.2
				}
			}
			if err := replacePrices(ctx, db.DB, table, editedAt); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			db = openStore(t, dir)
			t.Cleanup(func() { _ = db.Close() })
			prices := pricesByPattern(t, db)
			for _, want := range seedPrices {
				got, ok := prices[want.ModelPattern]
				switch want.ModelPattern {
				case deleted:
					if ok {
						t.Fatalf("upgrade restored deleted row %q", deleted)
					}
					continue
				case edited, editedBack:
					continue
				}
				if !ok {
					t.Fatalf("upgrade lost or did not add %q", want.ModelPattern)
				}
				if got.UpdatedAt == "" {
					t.Fatalf("%q has no updatedAt", want.ModelPattern)
				}
				got.UpdatedAt = ""
				if got != want {
					t.Fatalf("upgraded %q = %+v, want the shipped %+v", want.ModelPattern, got, want)
				}
			}
			if p := prices[edited]; p.Output != 77 || p.Source != "edited in dashboard" || p.UpdatedAt != "2026-01-01T00:00:00Z" {
				t.Fatalf("upgrade changed edited row: %+v", p)
			}
			if p := prices[editedBack]; p.CacheRead != 0.2 || p.Source != "edited in dashboard" || p.UpdatedAt != "2026-01-01T00:00:00Z" {
				t.Fatalf("upgrade changed a row edited back to its version-1 values: %+v", p)
			}
			if p := prices[custom]; p.Input != 1 || p.Output != 2 {
				t.Fatalf("upgrade changed an added row: %+v", p)
			}
			if len(prices) != len(seedPrices) {
				t.Fatalf("upgraded database has %d prices, want %d", len(prices), len(seedPrices))
			}
			if v := seedVersion(t, db); v != priceSeedVersion {
				t.Fatalf("seed version after upgrade %d, want %d", v, priceSeedVersion)
			}
		})
	}
}

// A database from version 2 gets the gpt-image-2.6 row added in version 3,
// unless its user already added a row with that pattern.
func TestPriceSeedUpgradeFromVersion2(t *testing.T) {
	const added = "gpt-image-2.6*"
	for _, tc := range []struct {
		name string
		own  *Price
	}{
		{"new row", nil},
		{"user row kept", &Price{ModelPattern: added, Input: 1, Output: 2, Source: "edited in dashboard"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			db := openStore(t, dir)
			exec(t, db, `DELETE FROM prices`)
			for _, p := range priceSeedV2 {
				if err := insertPrice(context.Background(), db, `INSERT`, p, "2026-11-05T00:00:00Z"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.own != nil {
				if err := insertPrice(context.Background(), db, `INSERT`, *tc.own, "2026-11-06T00:00:00Z"); err != nil {
					t.Fatal(err)
				}
			}
			exec(t, db, `UPDATE price_seed SET version = 2`)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			db = openStore(t, dir)
			t.Cleanup(func() { _ = db.Close() })
			prices := pricesByPattern(t, db)
			if len(prices) != len(seedPrices) {
				t.Fatalf("upgraded database has %d prices, want %d", len(prices), len(seedPrices))
			}
			for _, want := range seedPrices {
				got := prices[want.ModelPattern]
				if want.ModelPattern == added && tc.own != nil {
					want = *tc.own
				}
				got.UpdatedAt = ""
				if got != want {
					t.Fatalf("upgraded %q = %+v, want %+v", want.ModelPattern, got, want)
				}
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
}

// shippedPriceDigests pins each seed version's table. A database records the
// version it has, so a version that already shipped must not change: an
// install at that version would never see the change, and an upgrade would
// no longer recognize its unedited rows.
var shippedPriceDigests = []string{
	"932ba1541edc9fc7925ec77a68de64a838bddff4a8d8ee691c81e0f8410abae0",
	"a0321398a83e41292cd14ce1bc63df6ba9eeac8f7574047ffead28896ef9c4d5",
	"283393d5725c314a4844c882b2ef3e31f1d855651f59c0453bb0251be2d34b45",
}

func TestShippedPriceVersionsAreFrozen(t *testing.T) {
	if len(shippedPriceDigests) != priceSeedVersion {
		t.Fatalf("%d digests for %d seed versions; add the new version's digest", len(shippedPriceDigests), priceSeedVersion)
	}
	for i, table := range priceSeeds {
		sum := sha256.Sum256([]byte(fmt.Sprint(table)))
		if got := hex.EncodeToString(sum[:]); got != shippedPriceDigests[i] {
			t.Errorf("seed version %d changed (digest %s). If it shipped, restore it, freeze it as priceSeedV%d, append the changed table to priceSeeds as version %d, and add its digest; update this digest only for a version that never shipped",
				i+1, got, i+1, i+2)
		}
	}
}
