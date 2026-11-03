package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
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
	anthropicSource = "Anthropic list price (claude-api skill table cached 2026-10-06; writes 1.25x/2x input, reads as listed or 0.1x)"
	openAISource    = "OpenAI standard tier, developers.openai.com/api/docs/pricing fetched 2026-11-02 (5m/1h columns = 30m/12h writes; short-context tier)"
)

// seedPrices are the list prices at the time the table was created. Edits
// made through PUT /prices replace them; later list-price changes need a new
// migration so operator edits are never overwritten silently.
var seedPrices = []Price{
	{"claude-fable-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSource, ""},
	{"claude-mythos-5-1*", 10, 50, 0.25, 12.5, 20, anthropicSource, ""},
	{"claude-fable-5*", 10, 50, 1, 12.5, 20, anthropicSource, ""},
	{"claude-mythos-5*", 10, 50, 1, 12.5, 20, anthropicSource, ""},
	{"claude-opus-5-5*", 4, 20, 0.2, 5, 8, anthropicSource, ""},
	{"claude-opus-5*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-8*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-7*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-opus-4-6*", 5, 25, 0.5, 6.25, 10, anthropicSource, ""},
	{"claude-sonnet-5-5*", 2, 10, 0.2, 2.5, 4, anthropicSource, ""},
	{"claude-sonnet-5*", 2, 10, 0.2, 2.5, 4, anthropicSource, ""},
	{"claude-sonnet-4-6*", 3, 15, 0.3, 3.75, 6, anthropicSource, ""},
	{"claude-haiku-5-5*", 0.1, 0.5, 0.01, 0.125, 0.2, anthropicSource + "; prompts <=100K tokens", ""},
	{"claude-haiku-4-5*", 1, 5, 0.1, 1.25, 2, anthropicSource, ""},
	{"gpt-6-astra", 10, 50, 1, 12.5, 25, openAISource, ""},
	{"gpt-6-sol", 2, 10, 0.2, 2.5, 5, openAISource, ""},
	{"gpt-6-luna", 0.1, 0.5, 0.01, 0.125, 0.25, openAISource, ""},
	{"gpt-6.1-sol", 2, 10, 0.1, 2.5, 5, openAISource, ""},
	{"gpt-6.1-terra", 1, 5, 0.05, 1.25, 2.5, openAISource, ""},
	{"gpt-5.6-sol", 4, 20, 0.4, 5, 10, openAISource + "; promo rate through at least 2026-11-21", ""},
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

func seedPricesSQL() string {
	var b strings.Builder
	for _, p := range seedPrices {
		fmt.Fprintf(&b, "INSERT OR IGNORE INTO prices (model_pattern, input, output, cache_read, cache_write_5m, cache_write_1h, source, updated_at) VALUES (%s, %g, %g, %g, %g, %g, %s, strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ','now'));\n",
			sqlQuote(p.ModelPattern), p.Input, p.Output, p.CacheRead, p.CacheWrite5m, p.CacheWrite1h, sqlQuote(p.Source))
	}
	return b.String()
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

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
// their source and timestamp; changed or new rows are marked as edited.
func replacePrices(ctx context.Context, db *sql.DB, prices []Price, now time.Time) error {
	current, err := loadPrices(ctx, db)
	if err != nil {
		return err
	}
	old := map[string]Price{}
	for _, p := range current {
		old[p.ModelPattern] = p
	}
	return withImmediateTx(ctx, db, func(c *sql.Conn) error {
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
			} else if source == "" || (ok && source == prev.Source) {
				source = "edited in dashboard"
			}
			if _, err := c.ExecContext(ctx, `INSERT INTO prices (model_pattern, input, output, cache_read, cache_write_5m, cache_write_1h, source, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
				p.ModelPattern, p.Input, p.Output, p.CacheRead, p.CacheWrite5m, p.CacheWrite1h, source, updated); err != nil {
				return fmt.Errorf("insert price %q: %w", p.ModelPattern, err)
			}
		}
		return nil
	})
}
