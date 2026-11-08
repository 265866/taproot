package ingest

import (
	"context"
	"testing"

	"github.com/265866/taproot/internal/taproot/store"
)

func TestPriceSeedRunsOnlyOnAFreshDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, store.Config{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	prices, err := loadPrices(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != len(seedPrices) || len(prices) == 0 {
		t.Fatalf("fresh database has %d prices, want the %d seeded", len(prices), len(seedPrices))
	}
	deleted := seedPrices[0].ModelPattern
	if _, err := db.ExecContext(ctx, `DELETE FROM prices WHERE model_pattern = ?`, deleted); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = store.Open(ctx, store.Config{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	prices, err = loadPrices(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != len(seedPrices)-1 {
		t.Fatalf("reopened database has %d prices, want %d", len(prices), len(seedPrices)-1)
	}
	for _, p := range prices {
		if p.ModelPattern == deleted {
			t.Fatalf("deleted price %q came back on reopen", deleted)
		}
	}
}
