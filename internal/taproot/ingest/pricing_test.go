package ingest

import (
	"math"
	"testing"
)

func TestPriceBookLookup(t *testing.T) {
	pb := newPriceBook(seedPrices)
	cases := map[string]string{
		"claude-opus-5-5":           "claude-opus-5-5*",
		"claude-opus-5":             "claude-opus-5*",
		"claude-fable-5-1":          "claude-fable-5-1*",
		"claude-fable-5":            "claude-fable-5*",
		"claude-haiku-4-5-20251001": "claude-haiku-4-5*",
		"gpt-5":                     "gpt-5",
		"gpt-5.6-terra":             "gpt-5.6-terra",
	}
	for model, want := range cases {
		p := pb.lookup(model)
		if p == nil || p.ModelPattern != want {
			t.Errorf("%s matched %+v, want %s", model, p, want)
		}
	}
	for _, model := range []string{"gpt-5-turbo-unknown", "llama", ""} {
		if p := pb.lookup(model); p != nil {
			t.Errorf("%s matched %s, want none", model, p.ModelPattern)
		}
	}
}

// Each image model a Codex account routes to gets its own row; the rows do
// not overlap, so a later image version is not priced by an older row.
func TestPriceBookLookupImageModels(t *testing.T) {
	pb := newPriceBook(seedPrices)
	cases := map[string]string{
		"gpt-image-2":            "gpt-image-2",
		"gpt-image-2.5":          "gpt-image-2.5*",
		"gpt-image-2.5-flare":    "gpt-image-2.5*",
		"gpt-image-2.5-sunburst": "gpt-image-2.5*",
		"gpt-image-2.6":          "gpt-image-2.6*",
		"gpt-image-2.6-flare":    "gpt-image-2.6*",
		"gpt-image-2.6-sunburst": "gpt-image-2.6*",
	}
	for model, want := range cases {
		p := pb.lookup(model)
		if p == nil || p.ModelPattern != want {
			t.Errorf("%s matched %+v, want %s", model, p, want)
			continue
		}
		if p.Input != 8 || p.Output != 30 || p.CacheRead != 2 {
			t.Errorf("%s priced %+v, want input 8, output 30, cached input 2", model, p)
		}
	}
	for _, model := range []string{"gpt-image-2.7", "gpt-image-2-mini", "gpt-image-1.5"} {
		if p := pb.lookup(model); p != nil {
			t.Errorf("%s matched %s, want none", model, p.ModelPattern)
		}
	}
}

func TestPriceBookCost(t *testing.T) {
	pb := newPriceBook([]Price{{ModelPattern: "m", Input: 4, Output: 20, CacheRead: 0.2, CacheWrite5m: 5, CacheWrite1h: 8}})
	// 1M of each kind; 400k of the write has no TTL split and is priced at 5m.
	usd, ok := pb.cost("m", tokenSums{Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite: 1e6, CacheWrite5m: 2e5, CacheWrite1h: 4e5})
	want := 4 + 20 + 0.2 + 0.6*5 + 0.4*8
	if !ok || math.Abs(usd-want) > 1e-9 {
		t.Fatalf("cost = %v, %v; want %v", usd, ok, want)
	}
	if _, ok := pb.cost("other", tokenSums{Input: 1}); ok {
		t.Fatal("unpriced model reported as priced")
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"a*", "abc", true},
		{"a*c", "abc", true},
		{"a*c", "ab", false},
		{"*-mini", "gpt-5-mini", true},
		{"a*b*c", "a1b2c", true},
		{"a*b*c", "a1c2b", false},
		{"abc", "abc", true},
		{"abc", "abcd", false},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v", c.pattern, c.s, got)
		}
	}
}

func TestValidatePrices(t *testing.T) {
	if err := validatePrices([]Price{{ModelPattern: "a", Input: 1}}); err != nil {
		t.Fatal(err)
	}
	bad := [][]Price{
		{{ModelPattern: " "}},
		{{ModelPattern: "a"}, {ModelPattern: "a"}},
		{{ModelPattern: "a", Output: -1}},
		{{ModelPattern: "a", CacheRead: math.Inf(1)}},
	}
	for n, prices := range bad {
		if err := validatePrices(prices); err == nil {
			t.Errorf("case %d accepted", n)
		}
	}
}

func TestHistogramPercentile(t *testing.T) {
	h := histogram{}
	if h.percentile(0.5) != nil {
		t.Fatal("empty histogram has a percentile")
	}
	for v := int64(1); v <= 1000; v++ {
		h.add(v)
	}
	decoded := histogram{}
	decodeHistogram(h.encode(), decoded)
	for _, c := range []struct{ p, exact float64 }{{0.5, 500}, {0.95, 950}} {
		got := decoded.percentile(c.p)
		if got == nil || math.Abs(*got-c.exact)/c.exact > 0.06 {
			t.Errorf("p%v = %v, want within 6%% of %v", c.p, got, c.exact)
		}
	}
	zero := histogram{}
	zero.add(0)
	if got := zero.percentile(0.5); got == nil || *got != 0 {
		t.Errorf("zero-only p50 = %v", got)
	}
}
