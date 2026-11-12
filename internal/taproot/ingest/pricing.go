package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
)

// Price is one row of the editable price table, in USD per million tokens.
// For OpenAI models the 5m and 1h cache-write columns hold OpenAI's short
// (30m) and long (12h) cache-write prices.
type Price struct {
	ModelPattern string  `json:"modelPattern"`
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheRead    float64 `json:"cacheRead"`
	CacheWrite5m float64 `json:"cacheWrite5m"`
	CacheWrite1h float64 `json:"cacheWrite1h"`
	Source       string  `json:"source"`
	UpdatedAt    string  `json:"updatedAt"`
}

const (
	anthropicSource        = "Anthropic list price, platform.claude.com/docs/en/about-claude/pricing read 2026-11-08"
	anthropicSourceFable52 = "Anthropic list price, platform.claude.com/docs/en/about-claude/pricing read 2026-11-11"
	openAISource           = "OpenAI standard tier, developers.openai.com/api/docs/pricing read 2026-11-08 (5m/1h columns = 30m/12h writes; short-context tier)"
	// The pricing page lists GPT-5.6 Sol's promotional rate until 2026-11-30.
	// The standard rate is from the same page before the promotion, which had
	// one cache-write column.
	gpt56SolSource = "OpenAI standard tier, developers.openai.com/api/docs/pricing as of 2026-08-12 (5m/1h columns = 30m/12h writes; short-context tier; 12h write inferred as 2.5x input)"
	// A price row has one input rate, and usage records do not split text from
	// image tokens, so text prompt tokens are priced at the image input rate.
	gptImageSource = "OpenAI standard tier, developers.openai.com/api/docs/pricing read 2026-11-08 (gpt-image-2, gpt-image-2.5 and gpt-image-2.6 image tokens; text input priced at the image rate)"
)

// priceSeeds is the price table each seed version shipped, oldest first; the
// last one is seedPrices. A database records the version it was seeded to,
// and an upgrade reads the older tables to tell the rows it has from them.
// A release that changes seedPrices after a version shipped first freezes
// that version as a new priceSeedV<N> and appends it here; a shipped
// version never changes. TestShippedPriceVersionsAreFrozen enforces this.
var priceSeeds = [][]Price{priceSeedV1, priceSeedV2, priceSeedV3, seedPrices}

var priceSeedVersion = len(priceSeeds)

var seedPrices = []Price{
	{"claude-fable-5-2*", 8, 40, 0.2, 10, 16, anthropicSourceFable52, ""},
	{"claude-mythos-5-2*", 8, 40, 0.2, 10, 16, anthropicSourceFable52, ""},
	{"claude-fable-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSource, ""},
	{"claude-mythos-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSource, ""},
	{"claude-fable-5*", 10, 50, 1, 12.5, 20, anthropicSource, ""},
	{"claude-mythos-5*", 10, 50, 1, 12.5, 20, anthropicSource, ""},
	{"claude-opus-5-5*", 4, 20, 0.2, 5, 8, anthropicSource, ""},
	{"claude-opus-5*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-8*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-7*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-6*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-5*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-sonnet-5-5*", 2, 10, 0.1, 2.5, 4, anthropicSource, ""},
	{"claude-sonnet-5*", 2, 10, 0.2, 2.5, 4, anthropicSource, ""},
	{"claude-sonnet-4-6*", 3, 15, 0.3, 3.75, 6, anthropicSource, ""},
	{"claude-sonnet-4-5*", 3, 15, 0.3, 3.75, 6, anthropicSource, ""},
	{"claude-haiku-5-5*", 0.1, 0.5, 0.01, 0.125, 0.2, anthropicSource + "; prompts <=100K tokens", ""},
	{"claude-haiku-4-5*", 1, 5, 0.1, 1.25, 2, anthropicSource, ""},
	{"gpt-6-astra", 10, 50, 1, 12.5, 25, openAISource, ""},
	{"gpt-6-sol", 2, 10, 0.2, 2.5, 5, openAISource, ""},
	{"gpt-6-luna", 0.1, 0.5, 0.01, 0.125, 0.25, openAISource, ""},
	{"gpt-6.1-sol", 2, 10, 0.1, 2.5, 5, openAISource, ""},
	{"gpt-6.1-terra", 1, 5, 0.05, 1.25, 2.5, openAISource, ""},
	{"gpt-5.6-sol", 5, 30, 0.5, 6.25, 12.5, gpt56SolSource, ""},
	{"gpt-5.6-terra", 2, 12, 0.2, 2.5, 5, openAISource, ""},
	{"gpt-5.6-luna", 0.2, 1.2, 0.02, 0.25, 0.5, openAISource, ""},
	{"gpt-5.5", 5, 30, 0.5, 0, 0, openAISource, ""},
	{"gpt-5.4", 2.5, 15, 0.25, 0, 0, openAISource, ""},
	{"gpt-5.4-mini", 0.75, 4.5, 0.075, 0, 0, openAISource, ""},
	{"gpt-5.3-codex", 1.75, 14, 0.175, 0, 0, openAISource, ""},
	{"gpt-5.2", 1.75, 14, 0.175, 0, 0, openAISource, ""},
	{"gpt-5.1", 1.25, 10, 0.125, 0, 0, openAISource, ""},
	{"gpt-5", 1.25, 10, 0.125, 0, 0, openAISource, ""},
	{"gpt-5-mini", 0.25, 2, 0.025, 0, 0, openAISource, ""},
	{"gpt-image-2", 8, 30, 2, 0, 0, gptImageSource, ""},
	{"gpt-image-2.5*", 8, 30, 2, 0, 0, gptImageSource, ""},
	{"gpt-image-2.6*", 8, 30, 2, 0, 0, gptImageSource, ""},
}

// priceSeedV3 is the table seed version 3 shipped, in taproot-v0.2.0-rc.5
// through taproot-v0.5.0.
var priceSeedV3 = []Price{
	{"claude-fable-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSource, ""},
	{"claude-mythos-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSource, ""},
	{"claude-fable-5*", 10, 50, 1, 12.5, 20, anthropicSource, ""},
	{"claude-mythos-5*", 10, 50, 1, 12.5, 20, anthropicSource, ""},
	{"claude-opus-5-5*", 4, 20, 0.2, 5, 8, anthropicSource, ""},
	{"claude-opus-5*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-8*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-7*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-6*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-5*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-sonnet-5-5*", 2, 10, 0.1, 2.5, 4, anthropicSource, ""},
	{"claude-sonnet-5*", 2, 10, 0.2, 2.5, 4, anthropicSource, ""},
	{"claude-sonnet-4-6*", 3, 15, 0.3, 3.75, 6, anthropicSource, ""},
	{"claude-sonnet-4-5*", 3, 15, 0.3, 3.75, 6, anthropicSource, ""},
	{"claude-haiku-5-5*", 0.1, 0.5, 0.01, 0.125, 0.2, anthropicSource + "; prompts <=100K tokens", ""},
	{"claude-haiku-4-5*", 1, 5, 0.1, 1.25, 2, anthropicSource, ""},
	{"gpt-6-astra", 10, 50, 1, 12.5, 25, openAISource, ""},
	{"gpt-6-sol", 2, 10, 0.2, 2.5, 5, openAISource, ""},
	{"gpt-6-luna", 0.1, 0.5, 0.01, 0.125, 0.25, openAISource, ""},
	{"gpt-6.1-sol", 2, 10, 0.1, 2.5, 5, openAISource, ""},
	{"gpt-6.1-terra", 1, 5, 0.05, 1.25, 2.5, openAISource, ""},
	{"gpt-5.6-sol", 5, 30, 0.5, 6.25, 12.5, gpt56SolSource, ""},
	{"gpt-5.6-terra", 2, 12, 0.2, 2.5, 5, openAISource, ""},
	{"gpt-5.6-luna", 0.2, 1.2, 0.02, 0.25, 0.5, openAISource, ""},
	{"gpt-5.5", 5, 30, 0.5, 0, 0, openAISource, ""},
	{"gpt-5.4", 2.5, 15, 0.25, 0, 0, openAISource, ""},
	{"gpt-5.4-mini", 0.75, 4.5, 0.075, 0, 0, openAISource, ""},
	{"gpt-5.3-codex", 1.75, 14, 0.175, 0, 0, openAISource, ""},
	{"gpt-5.2", 1.75, 14, 0.175, 0, 0, openAISource, ""},
	{"gpt-5.1", 1.25, 10, 0.125, 0, 0, openAISource, ""},
	{"gpt-5", 1.25, 10, 0.125, 0, 0, openAISource, ""},
	{"gpt-5-mini", 0.25, 2, 0.025, 0, 0, openAISource, ""},
	{"gpt-image-2", 8, 30, 2, 0, 0, gptImageSource, ""},
	{"gpt-image-2.5*", 8, 30, 2, 0, 0, gptImageSource, ""},
	{"gpt-image-2.6*", 8, 30, 2, 0, 0, gptImageSource, ""},
}

// priceSeedV2 is the table seed version 2 shipped, in taproot-v0.2.0-rc.1
// through rc.4.
var priceSeedV2 = []Price{
	{"claude-fable-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSource, ""},
	{"claude-mythos-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSource, ""},
	{"claude-fable-5*", 10, 50, 1, 12.5, 20, anthropicSource, ""},
	{"claude-mythos-5*", 10, 50, 1, 12.5, 20, anthropicSource, ""},
	{"claude-opus-5-5*", 4, 20, 0.2, 5, 8, anthropicSource, ""},
	{"claude-opus-5*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-8*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-7*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-6*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-5*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-sonnet-5-5*", 2, 10, 0.1, 2.5, 4, anthropicSource, ""},
	{"claude-sonnet-5*", 2, 10, 0.2, 2.5, 4, anthropicSource, ""},
	{"claude-sonnet-4-6*", 3, 15, 0.3, 3.75, 6, anthropicSource, ""},
	{"claude-sonnet-4-5*", 3, 15, 0.3, 3.75, 6, anthropicSource, ""},
	{"claude-haiku-5-5*", 0.1, 0.5, 0.01, 0.125, 0.2, anthropicSource + "; prompts <=100K tokens", ""},
	{"claude-haiku-4-5*", 1, 5, 0.1, 1.25, 2, anthropicSource, ""},
	{"gpt-6-astra", 10, 50, 1, 12.5, 25, openAISource, ""},
	{"gpt-6-sol", 2, 10, 0.2, 2.5, 5, openAISource, ""},
	{"gpt-6-luna", 0.1, 0.5, 0.01, 0.125, 0.25, openAISource, ""},
	{"gpt-6.1-sol", 2, 10, 0.1, 2.5, 5, openAISource, ""},
	{"gpt-6.1-terra", 1, 5, 0.05, 1.25, 2.5, openAISource, ""},
	{"gpt-5.6-sol", 5, 30, 0.5, 6.25, 12.5, gpt56SolSource, ""},
	{"gpt-5.6-terra", 2, 12, 0.2, 2.5, 5, openAISource, ""},
	{"gpt-5.6-luna", 0.2, 1.2, 0.02, 0.25, 0.5, openAISource, ""},
	{"gpt-5.5", 5, 30, 0.5, 0, 0, openAISource, ""},
	{"gpt-5.4", 2.5, 15, 0.25, 0, 0, openAISource, ""},
	{"gpt-5.4-mini", 0.75, 4.5, 0.075, 0, 0, openAISource, ""},
	{"gpt-5.3-codex", 1.75, 14, 0.175, 0, 0, openAISource, ""},
	{"gpt-5.2", 1.75, 14, 0.175, 0, 0, openAISource, ""},
	{"gpt-5.1", 1.25, 10, 0.125, 0, 0, openAISource, ""},
	{"gpt-5", 1.25, 10, 0.125, 0, 0, openAISource, ""},
	{"gpt-5-mini", 0.25, 2, 0.025, 0, 0, openAISource, ""},
}

const (
	anthropicSourceV1 = "Anthropic list price (claude-api skill table cached 2026-10-06; writes 1.25x/2x input, reads as listed or 0.1x)"
	openAISourceV1    = "OpenAI standard tier, developers.openai.com/api/docs/pricing fetched 2026-11-02 (5m/1h columns = 30m/12h writes; short-context tier)"
)

// priceSeedV1 is the table seed version 1 shipped, which every database from
// before seed versions has.
var priceSeedV1 = []Price{
	{"claude-fable-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSourceV1, ""},
	{"claude-mythos-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSourceV1, ""},
	{"claude-fable-5*", 10, 50, 1, 12.5, 20, anthropicSourceV1, ""},
	{"claude-mythos-5*", 10, 50, 1, 12.5, 20, anthropicSourceV1, ""},
	{"claude-opus-5-5*", 4, 20, 0.2, 5, 8, anthropicSourceV1, ""},
	{"claude-opus-5*", 5, 25, 0.5, 6.25, 10, anthropicSourceV1, ""},
	{"claude-opus-4-8*", 5, 25, 0.5, 6.25, 10, anthropicSourceV1, ""},
	{"claude-opus-4-7*", 5, 25, 0.5, 6.25, 10, anthropicSourceV1, ""},
	{"claude-opus-4-6*", 5, 25, 0.5, 6.25, 10, anthropicSourceV1, ""},
	{"claude-sonnet-5-5*", 2, 10, 0.2, 2.5, 4, anthropicSourceV1, ""},
	{"claude-sonnet-5*", 2, 10, 0.2, 2.5, 4, anthropicSourceV1, ""},
	{"claude-sonnet-4-6*", 3, 15, 0.3, 3.75, 6, anthropicSourceV1, ""},
	{"claude-haiku-5-5*", 0.1, 0.5, 0.01, 0.125, 0.2, anthropicSourceV1 + "; prompts <=100K tokens", ""},
	{"claude-haiku-4-5*", 1, 5, 0.1, 1.25, 2, anthropicSourceV1, ""},
	{"gpt-6-astra", 10, 50, 1, 12.5, 25, openAISourceV1, ""},
	{"gpt-6-sol", 2, 10, 0.2, 2.5, 5, openAISourceV1, ""},
	{"gpt-6-luna", 0.1, 0.5, 0.01, 0.125, 0.25, openAISourceV1, ""},
	{"gpt-6.1-sol", 2, 10, 0.1, 2.5, 5, openAISourceV1, ""},
	{"gpt-6.1-terra", 1, 5, 0.05, 1.25, 2.5, openAISourceV1, ""},
	{"gpt-5.6-sol", 4, 20, 0.4, 5, 10, openAISourceV1 + "; promo rate through at least 2026-11-21", ""},
	{"gpt-5.6-terra", 2, 12, 0.2, 2.5, 5, openAISourceV1, ""},
	{"gpt-5.6-luna", 0.2, 1.2, 0.02, 0.25, 0.5, openAISourceV1, ""},
	{"gpt-5.5", 5, 30, 0.5, 0, 0, openAISourceV1, ""},
	{"gpt-5.4", 2.5, 15, 0.25, 0, 0, openAISourceV1, ""},
	{"gpt-5.4-mini", 0.75, 4.5, 0.075, 0, 0, openAISourceV1, ""},
	{"gpt-5.3-codex", 1.75, 14, 0.175, 0, 0, openAISourceV1, ""},
	{"gpt-5.2", 1.75, 14, 0.175, 0, 0, openAISourceV1, ""},
	{"gpt-5.1", 1.25, 10, 0.125, 0, 0, openAISourceV1, ""},
	{"gpt-5", 1.25, 10, 0.125, 0, 0, openAISourceV1, ""},
	{"gpt-5-mini", 0.25, 2, 0.025, 0, 0, openAISourceV1, ""},
}

// seedPriceTable is ingest's store seed. It brings a database from an older
// seed version to seedPrices without touching a row its user edited or
// deleted. A pattern no earlier version shipped is inserted with INSERT OR
// IGNORE. A row still equal to one an earlier version shipped, source
// included, is replaced with the current row; replacePrices gives every
// edited row a new source, so an edited row never matches. A database
// without a version predates seed versions and has version 1.
func seedPriceTable(ctx context.Context, tx *sql.Tx, created bool) error {
	version := 1
	if created {
		version = 0
	}
	err := tx.QueryRowContext(ctx, `SELECT version FROM price_seed WHERE id = 1`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read price seed version: %w", err)
	}
	if version >= priceSeedVersion {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, p := range seedPrices {
		shipped, earlier := false, []Price(nil)
		for _, table := range priceSeeds[:version] {
			for _, old := range table {
				if old.ModelPattern != p.ModelPattern {
					continue
				}
				shipped = true
				if old != p {
					earlier = append(earlier, old)
				}
			}
		}
		if !shipped {
			if err := insertPrice(ctx, tx, `INSERT OR IGNORE`, p, now); err != nil {
				return err
			}
			continue
		}
		for _, old := range earlier {
			if _, err := tx.ExecContext(ctx, `UPDATE prices SET input = ?, output = ?, cache_read = ?, cache_write_5m = ?, cache_write_1h = ?, source = ?, updated_at = ?
				WHERE model_pattern = ? AND input = ? AND output = ? AND cache_read = ? AND cache_write_5m = ? AND cache_write_1h = ? AND source = ?`,
				p.Input, p.Output, p.CacheRead, p.CacheWrite5m, p.CacheWrite1h, p.Source, now,
				old.ModelPattern, old.Input, old.Output, old.CacheRead, old.CacheWrite5m, old.CacheWrite1h, old.Source); err != nil {
				return fmt.Errorf("update shipped price %q: %w", p.ModelPattern, err)
			}
		}
	}
	return setPriceSeedVersion(ctx, tx)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertPrice(ctx context.Context, db execer, verb string, p Price, updatedAt string) error {
	if _, err := db.ExecContext(ctx, verb+` INTO prices (model_pattern, input, output, cache_read, cache_write_5m, cache_write_1h, source, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		p.ModelPattern, p.Input, p.Output, p.CacheRead, p.CacheWrite5m, p.CacheWrite1h, p.Source, updatedAt); err != nil {
		return fmt.Errorf("insert price %q: %w", p.ModelPattern, err)
	}
	return nil
}

func setPriceSeedVersion(ctx context.Context, db execer) error {
	if _, err := db.ExecContext(ctx, `INSERT INTO price_seed (id, version) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET version = excluded.version`, priceSeedVersion); err != nil {
		return fmt.Errorf("record price seed version: %w", err)
	}
	return nil
}

// priceBook resolves a model name to its price: an exact pattern wins, then
// the glob ('*' wildcard) with the longest literal text.
type priceBook struct {
	exact map[string]Price
	globs []Price
	cache map[string]*Price
}

func newPriceBook(prices []Price) *priceBook {
	pb := &priceBook{exact: map[string]Price{}, cache: map[string]*Price{}}
	for _, p := range prices {
		if strings.Contains(p.ModelPattern, "*") {
			pb.globs = append(pb.globs, p)
		} else {
			pb.exact[p.ModelPattern] = p
		}
	}
	sort.SliceStable(pb.globs, func(i, j int) bool {
		li := len(strings.ReplaceAll(pb.globs[i].ModelPattern, "*", ""))
		lj := len(strings.ReplaceAll(pb.globs[j].ModelPattern, "*", ""))
		if li != lj {
			return li > lj
		}
		return pb.globs[i].ModelPattern < pb.globs[j].ModelPattern
	})
	return pb
}

func (pb *priceBook) lookup(model string) *Price {
	if p, ok := pb.cache[model]; ok {
		return p
	}
	var found *Price
	if p, ok := pb.exact[model]; ok {
		found = &p
	} else {
		for i := range pb.globs {
			if globMatch(pb.globs[i].ModelPattern, model) {
				p := pb.globs[i]
				found = &p
				break
			}
		}
	}
	pb.cache[model] = found
	return found
}

func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		idx := strings.Index(s, part)
		if idx < 0 {
			return false
		}
		s = s[idx+len(part):]
	}
	return strings.HasSuffix(s, last)
}

// tokenSums are the priced token counts. CacheWrite includes the 5m and 1h
// splits; the remainder has no known TTL and is priced at the 5m rate,
// which is Anthropic's default TTL.
type tokenSums struct {
	Input, Output, Reasoning, CacheRead, CacheWrite, CacheWrite5m, CacheWrite1h int64
}

func (t *tokenSums) add(o tokenSums) {
	t.Input += o.Input
	t.Output += o.Output
	t.Reasoning += o.Reasoning
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.CacheWrite5m += o.CacheWrite5m
	t.CacheWrite1h += o.CacheWrite1h
}

// cost returns USD and whether a price was found.
func (pb *priceBook) cost(model string, t tokenSums) (float64, bool) {
	p := pb.lookup(model)
	if p == nil {
		return 0, false
	}
	unsplit := t.CacheWrite - t.CacheWrite5m - t.CacheWrite1h
	if unsplit < 0 {
		unsplit = 0
	}
	usd := float64(t.Input)*p.Input +
		float64(t.Output)*p.Output +
		float64(t.CacheRead)*p.CacheRead +
		float64(t.CacheWrite5m+unsplit)*p.CacheWrite5m +
		float64(t.CacheWrite1h)*p.CacheWrite1h
	return usd / 1e6, true
}

func loadPrices(ctx context.Context, q querier) ([]Price, error) {
	rows, err := q.QueryContext(ctx, `SELECT model_pattern, input, output, cache_read, cache_write_5m, cache_write_1h, source, updated_at FROM prices ORDER BY model_pattern`)
	if err != nil {
		return nil, fmt.Errorf("query prices: %w", err)
	}
	defer closeRows(rows)
	var out []Price
	for rows.Next() {
		var p Price
		if err := rows.Scan(&p.ModelPattern, &p.Input, &p.Output, &p.CacheRead, &p.CacheWrite5m, &p.CacheWrite1h, &p.Source, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan price: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func validatePrices(prices []Price) error {
	seen := map[string]bool{}
	for i, p := range prices {
		p.ModelPattern = strings.TrimSpace(p.ModelPattern)
		if p.ModelPattern == "" {
			return fmt.Errorf("prices[%d]: modelPattern is required", i)
		}
		if seen[p.ModelPattern] {
			return fmt.Errorf("prices[%d]: duplicate modelPattern %q", i, p.ModelPattern)
		}
		seen[p.ModelPattern] = true
		for _, v := range []float64{p.Input, p.Output, p.CacheRead, p.CacheWrite5m, p.CacheWrite1h} {
			if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("prices[%d]: prices must be finite and non-negative", i)
			}
		}
	}
	return nil
}

// replacePrices swaps the whole table. Rows whose values did not change keep
// their source and timestamp. A changed or new row keeps a source the user
// wrote and is otherwise marked as edited; it never keeps a shipped row's
// source, which the seed reads as an unedited row.
func replacePrices(ctx context.Context, db *sql.DB, prices []Price, now time.Time) error {
	current, err := loadPrices(ctx, db)
	if err != nil {
		return err
	}
	old := map[string]Price{}
	for _, p := range current {
		old[p.ModelPattern] = p
	}
	return store.WithImmediateTx(ctx, db, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, `DELETE FROM prices`); err != nil {
			return fmt.Errorf("clear prices: %w", err)
		}
		for _, p := range prices {
			p.ModelPattern = strings.TrimSpace(p.ModelPattern)
			prev, ok := old[p.ModelPattern]
			source, updated := strings.TrimSpace(p.Source), now.UTC().Format(time.RFC3339)
			if ok && prev.Input == p.Input && prev.Output == p.Output && prev.CacheRead == p.CacheRead &&
				prev.CacheWrite5m == p.CacheWrite5m && prev.CacheWrite1h == p.CacheWrite1h {
				source, updated = prev.Source, prev.UpdatedAt
			} else if source == "" || (ok && source == prev.Source) || isShippedSource(source) {
				source = "edited in dashboard"
			}
			p.Source = source
			if err := insertPrice(ctx, c, `INSERT`, p, updated); err != nil {
				return err
			}
		}
		return nil
	})
}

func isShippedSource(source string) bool {
	for _, table := range priceSeeds {
		for _, p := range table {
			if p.Source == source {
				return true
			}
		}
	}
	return false
}

// resetPrices replaces the whole table with the shipped rows and records the
// shipped seed version.
func resetPrices(ctx context.Context, db *sql.DB, now time.Time) error {
	updated := now.UTC().Format(time.RFC3339)
	return store.WithImmediateTx(ctx, db, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, `DELETE FROM prices`); err != nil {
			return fmt.Errorf("clear prices: %w", err)
		}
		for _, p := range seedPrices {
			if err := insertPrice(ctx, c, `INSERT`, p, updated); err != nil {
				return err
			}
		}
		return setPriceSeedVersion(ctx, c)
	})
}
