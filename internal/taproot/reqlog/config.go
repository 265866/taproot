package reqlog

import (
	"fmt"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the request-logs section of taproot.yaml. Zero values take the
// defaults below; a negative MaxDays or MaxBytes disables that limit.
type Config struct {
	// MaxDays drops records older than this many days.
	MaxDays int `yaml:"max-days" json:"maxDays"`
	// MaxBytes caps the request log directory (sealed plus open segments).
	MaxBytes ByteSize `yaml:"max-bytes" json:"maxBytes"`
	// ExcludePaths lists request paths that are never stored. An entry is a
	// path.Match pattern against the URL path, optionally prefixed by a method:
	// "/api/hello", "HEAD /api/*".
	ExcludePaths []string `yaml:"exclude-paths" json:"excludePaths"`
	// KeepUnrouted404 stores 404s for routes the proxy does not serve. They
	// are scanner noise, so they are dropped unless this is set.
	KeepUnrouted404 bool `yaml:"keep-unrouted-404" json:"keepUnrouted404"`

	// QueueBytes bounds memory held by records waiting to be written and by
	// in-flight streaming bodies. Past it, bodies are dropped and the record
	// says so; the proxy never waits on the log.
	QueueBytes ByteSize `yaml:"queue-bytes" json:"queueBytes"`
	// SegmentMaxBytes caps an open segment's stored bytes; a record that
	// would pass it starts a new segment. A lookup decompresses up to one
	// segment, and records compress against earlier ones only within the
	// 128 MiB zstd window, so larger values cost lookup latency and ratio.
	SegmentMaxBytes ByteSize `yaml:"segment-max-bytes" json:"segmentMaxBytes"`
	// OpenMaxBytes bounds raw bytes across all open segments; the largest is
	// sealed early when the total passes it.
	OpenMaxBytes ByteSize `yaml:"open-max-bytes" json:"openMaxBytes"`
	// SealIdle seals a segment whose session has been quiet this long.
	SealIdle time.Duration `yaml:"seal-idle" json:"sealIdle"`
	// SealAge seals a segment this long after it opened.
	SealAge time.Duration `yaml:"seal-age" json:"sealAge"`
}

const (
	defaultMaxDays         = 14
	defaultMaxBytes        = 20 << 30
	defaultQueueBytes      = 256 << 20
	defaultSegmentMaxBytes = zstdWindow
	defaultOpenMaxBytes    = 4 << 30
	defaultSealIdle        = 15 * time.Minute
	defaultSealAge         = 6 * time.Hour
)

func (c Config) withDefaults() Config {
	if c.MaxDays == 0 {
		c.MaxDays = defaultMaxDays
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = defaultMaxBytes
	}
	if c.QueueBytes <= 0 {
		c.QueueBytes = defaultQueueBytes
	}
	if c.SegmentMaxBytes <= 0 {
		c.SegmentMaxBytes = defaultSegmentMaxBytes
	}
	if c.OpenMaxBytes <= 0 {
		c.OpenMaxBytes = defaultOpenMaxBytes
	}
	if c.SealIdle <= 0 {
		c.SealIdle = defaultSealIdle
	}
	if c.SealAge <= 0 {
		c.SealAge = defaultSealAge
	}
	return c
}

func (c Config) validate() error {
	for _, raw := range c.ExcludePaths {
		if _, err := parseExcludeRule(raw); err != nil {
			return err
		}
	}
	return nil
}

// ByteSize is a byte count that reads plain integers or sizes like "20GB",
// "512MiB" from YAML. Decimal and binary suffixes both mean powers of 1024.
type ByteSize int64

// UnmarshalYAML accepts an integer or a size string.
func (b *ByteSize) UnmarshalYAML(node *yaml.Node) error {
	parsed, err := ParseByteSize(node.Value)
	if err != nil {
		return err
	}
	*b = parsed
	return nil
}

// ParseByteSize parses "1073741824", "1GB", "1GiB", "512mb", or "-1".
func ParseByteSize(raw string) (ByteSize, error) {
	s := strings.TrimSpace(strings.ToUpper(raw))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "IB"), "B")
	multiplier := int64(1)
	if s != "" {
		switch s[len(s)-1] {
		case 'K':
			multiplier = 1 << 10
		case 'M':
			multiplier = 1 << 20
		case 'G':
			multiplier = 1 << 30
		case 'T':
			multiplier = 1 << 40
		}
		if multiplier != 1 {
			s = s[:len(s)-1]
		}
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q", raw)
	}
	// float64(math.MaxInt64) rounds up to 2^63, which int64 cannot hold, and
	// a conversion out of range gives an arbitrary value, so NaN, Inf, and
	// 1e30 would otherwise become a negative size, meaning no limit.
	n := math.Trunc(value * float64(multiplier))
	if math.IsNaN(n) || math.Abs(n) >= math.MaxInt64 {
		return 0, fmt.Errorf("byte size %q out of range", raw)
	}
	// 0 means the default, so a fraction of a byte must not round down to it.
	if n == 0 && value != 0 {
		return 0, fmt.Errorf("byte size %q is less than one byte", raw)
	}
	return ByteSize(n), nil
}

type excludeRule struct {
	method  string
	pattern string
}

func parseExcludeRule(raw string) (excludeRule, error) {
	raw = strings.TrimSpace(raw)
	rule := excludeRule{pattern: raw}
	if method, rest, ok := strings.Cut(raw, " "); ok {
		rule.method = strings.ToUpper(strings.TrimSpace(method))
		rule.pattern = strings.TrimSpace(rest)
	}
	if !strings.HasPrefix(rule.pattern, "/") {
		return rule, fmt.Errorf("exclude path %q must start with /", raw)
	}
	if _, err := path.Match(rule.pattern, "/"); err != nil {
		return rule, fmt.Errorf("exclude path %q: %w", raw, err)
	}
	return rule, nil
}

func (r excludeRule) matches(method, urlPath string) bool {
	if r.method != "" && r.method != strings.ToUpper(method) {
		return false
	}
	ok, _ := path.Match(r.pattern, urlPath)
	return ok
}

type excluder struct {
	rules           []excludeRule
	keepUnrouted404 bool
}

func newExcluder(cfg Config) excluder {
	ex := excluder{keepUnrouted404: cfg.KeepUnrouted404}
	for _, raw := range cfg.ExcludePaths {
		if rule, err := parseExcludeRule(raw); err == nil {
			ex.rules = append(ex.rules, rule)
		}
	}
	return ex
}

// excluded decides before any body is formatted. A 404 that never reached an
// upstream (no API request recorded) came from the router's NoRoute handler.
func (e excluder) excluded(method, rawURL string, status int, reachedUpstream bool) bool {
	urlPath := rawURL
	if i := strings.IndexByte(urlPath, '?'); i >= 0 {
		urlPath = urlPath[:i]
	}
	// Taproot's own API is management traffic (settings, prices, backups), and
	// upstream likewise never logs its management routes.
	if urlPath == "/taproot" || strings.HasPrefix(urlPath, "/taproot/") {
		return true
	}
	// The dashboard page and its chunks are static files. Upstream already
	// skips GETs; this drops HEADs, such as an uptime check of "/".
	if (method == http.MethodGet || method == http.MethodHead) && (urlPath == "/" || strings.HasPrefix(urlPath, "/assets/")) {
		return true
	}
	if !e.keepUnrouted404 && status == http.StatusNotFound && !reachedUpstream {
		return true
	}
	// The access guard answers callers it does not admit with a local 403.
	// The logging middleware runs before the guard, so without this rule any
	// tailnet peer could fill the log budget with records it chose.
	if status == http.StatusForbidden && !reachedUpstream {
		return true
	}
	for _, rule := range e.rules {
		if rule.matches(method, urlPath) {
			return true
		}
	}
	return false
}
