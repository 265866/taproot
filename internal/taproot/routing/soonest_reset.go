// Package routing holds taproot's credential selection strategies.
//
// SoonestResetSelector implements routing.strategy "soonest-reset-first": among the
// credentials the manager already considers eligible for a request, it picks the one
// whose weekly quota window resets soonest, because quota still unused when a window
// resets is lost.
//
// Upstream wires it in sdk/cliproxy/service_config.go, where routing.session-affinity
// wraps it in coreauth.SessionAffinitySelector like any other strategy. Bound sessions
// therefore keep their credential; only new sessions, requests without a session, and
// failovers reach this selector.
package routing

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/265866/taproot/sdk/cliproxy/executor"
)

// Strategy is the canonical routing.strategy value for SoonestResetSelector.
const Strategy = "soonest-reset-first"

const (
	week = 7 * 24 * time.Hour
	// resetTieGranularity groups resets that are practically simultaneous. Codex
	// reports a relative reset, so the same instant observed on two credentials can
	// differ by a second; such credentials share load by round-robin.
	resetTieGranularity = time.Minute
	// probeRetry is how long a probe that left its credential unobserved waits
	// before the credential is probed again, for example after a 5xx or a
	// count_tokens request, which records no quota headers.
	probeRetry      = 10 * time.Minute
	maxRotationKeys = 4096
)

// IsStrategy reports whether a routing.strategy value names this strategy.
func IsStrategy(raw string) bool {
	switch strings.NewReplacer("_", "-", " ", "-").Replace(strings.ToLower(strings.TrimSpace(raw))) {
	case Strategy, "soonestresetfirst", "srf":
		return true
	}
	return false
}

// SoonestResetSelector is a coreauth.Selector ordering candidates by weekly reset.
//
// It relies on the manager's contract for custom selectors: the candidates it receives
// are already filtered to the request's provider and model, enabled, out of cooldown,
// and narrowed to the highest available priority tier (both when called directly and
// as the session-affinity fallback). It adds, in order:
//
//  1. Codex websocket preference, as upstream's built-in selectors apply it.
//  2. A Claude or Codex OAuth credential with no quota headers observed yet, and no
//     weekly reading in taproot's quota store (Readings), is picked before any
//     ordering, so its weekly reset becomes known. Upstream keeps quota signals in
//     memory only, so after a restart every credential starts unobserved; without this
//     probe the first credential to answer would take every new session while the
//     others, unknown, sort last and never get observed. A probe that leaves the
//     credential unobserved is repeated after probeRetry.
//  3. Credentials whose weekly window is observed exhausted are skipped, unless every
//     candidate is.
//  4. The soonest known weekly reset wins, from the quota headers, else from
//     Readings. Credentials with no known reset come last.
//  5. Ties, and the all-unknown case, rotate round-robin.
type SoonestResetSelector struct {
	mu         sync.Mutex
	lastPicked map[string]string
	probed     map[string]time.Time
	readings   *Readings
	now        func() time.Time
}

var _ coreauth.Selector = (*SoonestResetSelector)(nil)

// NewSoonestResetSelector returns a selector using the wall clock and Stored.
func NewSoonestResetSelector() *SoonestResetSelector {
	return &SoonestResetSelector{readings: Stored}
}

// Pick implements coreauth.Selector.
func (s *SoonestResetSelector) Pick(ctx context.Context, provider, model string, _ cliproxyexecutor.Options, auths []*coreauth.Auth) (*coreauth.Auth, error) {
	if len(auths) == 0 {
		return nil, &coreauth.Error{Code: "auth_not_found", Message: "no auth candidates", HTTPStatus: http.StatusServiceUnavailable}
	}
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	candidates := preferCodexWebsocket(ctx, provider, auths)

	s.mu.Lock()
	defer s.mu.Unlock()
	if probe := s.nextProbe(candidates, now); probe != nil {
		return probe, nil
	}
	return s.rotate(provider+":"+model, s.soonestGroup(candidates, now)), nil
}

// nextProbe returns the first candidate by credential ID that needs a probe: a Claude
// or Codex OAuth credential with no quota headers and no stored weekly reading, not
// probed within probeRetry.
func (s *SoonestResetSelector) nextProbe(auths []*coreauth.Auth, now time.Time) *coreauth.Auth {
	var probe *coreauth.Auth
	for _, auth := range auths {
		if len(auth.Quota.Signals) > 0 || !reportsWeeklyWindow(auth) {
			continue
		}
		if _, stored := s.readings.weekly(auth.Index, now); stored {
			continue
		}
		if at, done := s.probed[auth.ID]; done && now.Sub(at) < probeRetry {
			continue
		}
		if probe == nil || auth.ID < probe.ID {
			probe = auth
		}
	}
	if probe == nil {
		return nil
	}
	if s.probed == nil || len(s.probed) >= maxRotationKeys {
		s.probed = make(map[string]time.Time)
	}
	s.probed[probe.ID] = now
	return probe
}

// reportsWeeklyWindow reports whether responses for auth carry a weekly window. API
// keys get neither Claude's unified limits nor Codex's plan windows.
func reportsWeeklyWindow(auth *coreauth.Auth) bool {
	switch strings.ToLower(strings.TrimSpace(auth.Provider)) {
	case "claude", "codex":
		return strings.TrimSpace(auth.Attributes["api_key"]) == ""
	}
	return false
}

func (s *SoonestResetSelector) weeklyWindow(auth *coreauth.Auth, now time.Time) (WeeklyWindow, bool) {
	if w, ok := WeeklyWindowOf(auth, now); ok {
		return w, true
	}
	return s.readings.weekly(auth.Index, now)
}

// soonestGroup returns the candidates whose weekly reset is within
// resetTieGranularity of the soonest one, or every remaining candidate when no reset
// is known.
func (s *SoonestResetSelector) soonestGroup(auths []*coreauth.Auth, now time.Time) []*coreauth.Auth {
	type ranked struct {
		auth  *coreauth.Auth
		reset time.Time
	}
	all := make([]ranked, 0, len(auths))
	open := make([]ranked, 0, len(auths))
	for _, auth := range auths {
		w, known := s.weeklyWindow(auth, now)
		r := ranked{auth: auth}
		if known {
			r.reset = w.ResetsAt
		}
		all = append(all, r)
		if !known || !w.Exhausted {
			open = append(open, r)
		}
	}
	if len(open) == 0 {
		open = all
	}
	var soonest time.Time
	for _, r := range open {
		if !r.reset.IsZero() && (soonest.IsZero() || r.reset.Before(soonest)) {
			soonest = r.reset
		}
	}
	group := make([]*coreauth.Auth, 0, len(open))
	for _, r := range open {
		if r.reset.IsZero() == soonest.IsZero() && r.reset.Sub(soonest) < resetTieGranularity {
			group = append(group, r.auth)
		}
	}
	return group
}

// rotate picks the successor of the previous pick for key, by credential ID. The
// caller holds s.mu.
func (s *SoonestResetSelector) rotate(key string, group []*coreauth.Auth) *coreauth.Auth {
	sorted := append([]*coreauth.Auth(nil), group...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	if s.lastPicked == nil || (len(s.lastPicked) >= maxRotationKeys && s.lastPicked[key] == "") {
		s.lastPicked = make(map[string]string)
	}
	last := s.lastPicked[key]
	picked := sorted[0]
	if last != "" {
		if i := sort.Search(len(sorted), func(i int) bool { return sorted[i].ID > last }); i < len(sorted) {
			picked = sorted[i]
		}
	}
	s.lastPicked[key] = picked.ID
	return picked
}

// preferCodexWebsocket mirrors upstream's built-in selectors: a downstream websocket
// request to Codex prefers credentials with websockets enabled when any exist.
func preferCodexWebsocket(ctx context.Context, provider string, auths []*coreauth.Auth) []*coreauth.Auth {
	if !strings.EqualFold(strings.TrimSpace(provider), "codex") || !cliproxyexecutor.DownstreamWebsocket(ctx) {
		return auths
	}
	enabled := make([]*coreauth.Auth, 0, len(auths))
	for _, auth := range auths {
		if websocketsEnabled(auth) {
			enabled = append(enabled, auth)
		}
	}
	if len(enabled) == 0 {
		return auths
	}
	return enabled
}

func websocketsEnabled(auth *coreauth.Auth) bool {
	if raw := strings.TrimSpace(auth.Attributes["websockets"]); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			return parsed
		}
	}
	switch v := auth.Metadata["websockets"].(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return err == nil && parsed
	}
	return false
}

// WeeklyWindow is a credential's weekly quota window as last observed.
type WeeklyWindow struct {
	// ResetsAt is the next reset at or after the evaluation time. A reset observed in
	// the past is rolled forward by whole periods, because weekly windows reset at a
	// fixed time per account.
	ResetsAt time.Time
	// Exhausted reports utilization >= 1 (or a rejected status) in the window that is
	// still running. A rolled-forward window is a new window, so it is never exhausted.
	Exhausted bool
}

// WeeklyWindowOf reads the weekly window from the raw quota headers upstream keeps on
// the credential (auth.Quota.Signals, replaced on every response that carries them).
//
//   - Claude: anthropic-ratelimit-unified-7d-{reset,utilization,status}, the all-models
//     weekly limit. Model-scoped weeklies (7d_<model>) are ignored.
//   - Codex: the x-codex-* window whose window-minutes is one week, normally secondary.
//     Secondary is assumed weekly when it does not report its length.
func WeeklyWindowOf(auth *coreauth.Auth, now time.Time) (WeeklyWindow, bool) {
	if auth == nil || len(auth.Quota.Signals) == 0 {
		return WeeklyWindow{}, false
	}
	signal := func(name string) string {
		return strings.TrimSpace(auth.Quota.Signals[http.CanonicalHeaderKey(name)])
	}
	switch strings.ToLower(strings.TrimSpace(auth.Provider)) {
	case "claude":
		return claudeWeekly(signal, now)
	case "codex":
		return codexWeekly(signal, auth.Quota.ObservedAt, now)
	}
	return WeeklyWindow{}, false
}

func claudeWeekly(signal func(string) string, now time.Time) (WeeklyWindow, bool) {
	const prefix = "anthropic-ratelimit-unified-7d-"
	reset, ok := parseUnixTime(signal(prefix + "reset"))
	if !ok {
		return WeeklyWindow{}, false
	}
	utilization, _ := parseFinite(signal(prefix + "utilization"))
	rejected := strings.EqualFold(signal(prefix+"status"), "rejected")
	return rollForward(reset, week, utilization >= 1 || rejected, now), true
}

func codexWeekly(signal func(string) string, observedAt, now time.Time) (WeeklyWindow, bool) {
	weekMinutes := week.Minutes()
	for _, namespace := range []string{"secondary", "primary"} {
		base := "x-codex-" + namespace + "-"
		minutes, hasLength := parseFinite(signal(base + "window-minutes"))
		if hasLength && minutes != weekMinutes {
			continue
		}
		if !hasLength && namespace != "secondary" {
			continue
		}
		reset, ok := parseUnixTime(signal(base + "reset-at"))
		if !ok {
			seconds, okAfter := parseFinite(signal(base + "reset-after-seconds"))
			if !okAfter || seconds < 0 || observedAt.IsZero() {
				continue
			}
			reset = observedAt.Add(time.Duration(seconds * float64(time.Second)))
		}
		used, _ := parseFinite(signal(base + "used-percent"))
		return rollForward(reset, week, used >= 100, now), true
	}
	return WeeklyWindow{}, false
}

func rollForward(reset time.Time, period time.Duration, exhausted bool, now time.Time) WeeklyWindow {
	if reset.After(now) {
		return WeeklyWindow{ResetsAt: reset, Exhausted: exhausted}
	}
	periods := now.Sub(reset)/period + 1
	return WeeklyWindow{ResetsAt: reset.Add(periods * period)}
}

func parseFinite(raw string) (float64, bool) {
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// parseUnixTime accepts unix seconds, which both providers send, and RFC 3339.
func parseUnixTime(raw string) (time.Time, bool) {
	if v, ok := parseFinite(raw); ok && v > 0 {
		sec := math.Floor(v)
		return time.Unix(int64(sec), int64((v-sec)*1e9)), true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}
