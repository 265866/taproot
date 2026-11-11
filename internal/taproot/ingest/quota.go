package ingest

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// observation is one quota reading for one account window.
type observation struct {
	AuthIndex     string
	Provider      string
	Window        string
	Utilization   float64 // 0..1
	ResetsAt      time.Time
	Status        string
	PeriodSeconds int64
	ObservedAt    time.Time
	Source        string
}

const (
	anthropicUnifiedPrefix = "anthropic-ratelimit-unified-"
	codexHeaderPrefix      = "x-codex-"
)

// windowPeriodSeconds knows the period of Anthropic windows by name; Codex
// windows carry their own length.
func windowPeriodSeconds(window string) int64 {
	switch {
	case window == "five_hour" || strings.HasPrefix(window, "five_hour_"):
		return 5 * 3600
	case window == "seven_day" || strings.HasPrefix(window, "seven_day_"):
		return 7 * 24 * 3600
	}
	return 0
}

// anthropicClaimWindow maps the claim segment of
// anthropic-ratelimit-unified-<claim>-utilization to the window names used by
// the oauth/usage endpoint, so header and poll observations share a series.
func anthropicClaimWindow(claim string) string {
	switch claim {
	case "5h":
		return "five_hour"
	case "7d":
		return "seven_day"
	case "7d_oi":
		return "seven_day_fable"
	}
	switch {
	case strings.HasPrefix(claim, "7d_"):
		return "seven_day_" + claim[3:]
	case strings.HasPrefix(claim, "5h_"):
		return "five_hour_" + claim[3:]
	}
	return claim
}

func headerValue(h http.Header, lowerName string) string {
	for k, vs := range h {
		if strings.ToLower(k) == lowerName && len(vs) > 0 {
			return strings.TrimSpace(vs[len(vs)-1])
		}
	}
	return ""
}

func lowerHeaderNames(h http.Header) []string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, strings.ToLower(k))
	}
	sort.Strings(names)
	return names
}

// parseQuotaHeaders extracts window observations from one upstream response.
func parseQuotaHeaders(provider string, h http.Header, observedAt time.Time) []observation {
	if len(h) == 0 {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude":
		return parseAnthropicHeaders(h, observedAt)
	case "codex":
		return parseCodexHeaders(h, observedAt)
	}
	return nil
}

func parseAnthropicHeaders(h http.Header, observedAt time.Time) []observation {
	var out []observation
	for _, name := range lowerHeaderNames(h) {
		if !strings.HasPrefix(name, anthropicUnifiedPrefix) || !strings.HasSuffix(name, "-utilization") {
			continue
		}
		claim := strings.TrimSuffix(strings.TrimPrefix(name, anthropicUnifiedPrefix), "-utilization")
		if claim == "" {
			continue
		}
		u, ok := parseFiniteFloat(headerValue(h, name))
		if !ok || u < 0 {
			continue
		}
		window := anthropicClaimWindow(claim)
		o := observation{
			Provider:      "claude",
			Window:        window,
			Utilization:   u,
			Status:        strings.ToLower(headerValue(h, anthropicUnifiedPrefix+claim+"-status")),
			PeriodSeconds: windowPeriodSeconds(window),
			ObservedAt:    observedAt,
			Source:        "header",
		}
		if t, ok := parseUnixSeconds(headerValue(h, anthropicUnifiedPrefix+claim+"-reset")); ok {
			o.ResetsAt = t
		}
		out = append(out, o)
	}
	return out
}

// accountWideWindow reports whether a window limits every request on the
// account. Model- and feature-scoped windows (seven_day_opus, Codex
// additional limits, code review) only limit their own traffic.
func accountWideWindow(window string) bool {
	switch window {
	case "five_hour", "seven_day", "codex_primary", "codex_secondary":
		return true
	}
	return false
}

// codexWindowName names a Codex header family the way the wham/usage poller
// names the same limit, so one limit is one series. Additional limits arrive
// as x-codex-<short namespace>-* on HTTP (with a -limit-name header) and as
// x-codex-additional-<limit name>-* on the websocket path; both map to
// codex_<normalized limit name>_<slot>.
func codexWindowName(h http.Header, ns string) string {
	limit, slot := ns, ""
	for _, s := range []string{"primary", "secondary"} {
		if ns == s {
			return "codex_" + s
		}
		if cut, ok := strings.CutSuffix(ns, "-"+s); ok {
			limit, slot = cut, s
			break
		}
	}
	if slot == "" {
		return "codex_" + normalizeWindowName(ns)
	}
	name := normalizeWindowName(headerValue(h, codexHeaderPrefix+limit+"-limit-name"))
	if name == "" {
		name = normalizeWindowName(strings.TrimPrefix(limit, "additional-"))
	}
	return "codex_" + name + "_" + slot
}

// parseCodexHeaders reads every x-codex-<namespace>-used-percent family:
// x-codex-primary-*, x-codex-secondary-*, and namespaced additional limits
// such as x-codex-bengalfox-primary-*.
func parseCodexHeaders(h http.Header, observedAt time.Time) []observation {
	var out []observation
	for _, name := range lowerHeaderNames(h) {
		if !strings.HasPrefix(name, codexHeaderPrefix) || !strings.HasSuffix(name, "-used-percent") {
			continue
		}
		base := strings.TrimSuffix(name, "-used-percent")
		ns := strings.TrimPrefix(base, codexHeaderPrefix)
		if ns == "" {
			continue
		}
		used, ok := parseFiniteFloat(headerValue(h, name))
		if !ok || used < 0 {
			continue
		}
		o := observation{
			Provider:    "codex",
			Window:      codexWindowName(h, ns),
			Utilization: used / 100,
			ObservedAt:  observedAt,
			Source:      "header",
		}
		if mins, ok := parseFiniteFloat(headerValue(h, base+"-window-minutes")); ok && mins > 0 {
			o.PeriodSeconds = int64(mins * 60)
		}
		if t, ok := parseUnixSeconds(headerValue(h, base+"-reset-at")); ok {
			o.ResetsAt = t
		} else if secs, ok := parseFiniteFloat(headerValue(h, base+"-reset-after-seconds")); ok && secs >= 0 {
			o.ResetsAt = observedAt.Add(time.Duration(secs * float64(time.Second)))
		}
		o.Status = codexStatus(o.Utilization)
		out = append(out, o)
	}
	return out
}

func codexStatus(u float64) string {
	if u >= 1 {
		return "rejected"
	}
	return "allowed"
}

func parseFiniteFloat(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func parseUnixSeconds(raw string) (time.Time, bool) {
	v, ok := parseFiniteFloat(raw)
	if ok && v > 0 {
		sec := math.Floor(v)
		return time.Unix(int64(sec), int64((v-sec)*1e9)).UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// claudeLegacyWindow names the window a top-level oauth/usage key reports.
// Anthropic calls the Fable limit seven_day_overage_included (header claim
// 7d_oi); iguana_necktie carried it before the limits array existed.
func claudeLegacyWindow(key string) string {
	switch key {
	case "iguana_necktie", "seven_day_overage_included":
		return "seven_day_fable"
	}
	return key
}

// isFableModel matches the Fable family by display name ("Fable", "Fable 5",
// "Fable 5.1"), the way Claude Code's model allowlist does.
func isFableModel(displayName string) bool {
	name := strings.ToLower(strings.TrimSpace(displayName))
	return name == "fable" || strings.HasPrefix(name, "fable ")
}

type claudeUsageLimit struct {
	Kind     string   `json:"kind"`
	Percent  *float64 `json:"percent"`
	ResetsAt *string  `json:"resets_at"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
}

// claudeLimitWindow names the window of one limits[] item, or "" for a kind
// taproot does not record.
func claudeLimitWindow(l claudeUsageLimit) string {
	switch l.Kind {
	case "session":
		return "five_hour"
	case "weekly_all":
		return "seven_day"
	case "weekly_scoped":
		if l.Scope == nil || l.Scope.Model == nil {
			return ""
		}
		if isFableModel(l.Scope.Model.DisplayName) {
			return "seven_day_fable"
		}
		if slug := normalizeWindowName(l.Scope.Model.DisplayName); slug != "" {
			return "seven_day_" + slug
		}
	}
	return ""
}

func claudePollObservation(window string, percent float64, resetsAt *string, observedAt time.Time) observation {
	o := observation{
		Provider:      "claude",
		Window:        window,
		Utilization:   percent / 100,
		PeriodSeconds: windowPeriodSeconds(window),
		ObservedAt:    observedAt,
		Source:        "poll",
		Status:        "allowed",
	}
	if resetsAt != nil {
		if t, err := time.Parse(time.RFC3339, *resetsAt); err == nil {
			o.ResetsAt = t.UTC()
		}
	}
	if o.Utilization >= 1 {
		o.Status = "rejected"
	}
	return o
}

// statusNoLimit marks a window the account does not have. The row replaces an
// older reading as the window's latest, and latestObservations leaves such a
// window out.
const statusNoLimit = "no_limit"

// parseClaudeUsagePayload reads api.anthropic.com/api/oauth/usage. The
// limits array comes first; a top-level window key adds only windows the
// array did not report. Utilization there is a percentage (0..100).
//
// A limits array without a session item means the account has no 5-hour
// limit, which is recorded as a statusNoLimit row. Without the array, a null
// five_hour means no session is open, which is recorded as an empty window
// with no reset. Either way an older reading stops counting as current.
func parseClaudeUsagePayload(body []byte, observedAt time.Time) []observation {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	var out []observation
	seen := map[string]bool{}
	var limits []claudeUsageLimit
	hasLimits, hasSession := false, false
	if raw, ok := payload["limits"]; ok && json.Unmarshal(raw, &limits) == nil && len(limits) > 0 {
		hasLimits = true
		for _, l := range limits {
			hasSession = hasSession || l.Kind == "session"
			window := claudeLimitWindow(l)
			if window == "" || seen[window] || l.Percent == nil {
				continue
			}
			seen[window] = true
			out = append(out, claudePollObservation(window, *l.Percent, l.ResetsAt, observedAt))
		}
	}
	type legacyWindow struct {
		Utilization     *float64 `json:"utilization"`
		ResetsAt        *string  `json:"resets_at"`
		LimitDollars    *float64 `json:"limit_dollars"`
		UsedDollars     *float64 `json:"used_dollars"`
		RemainingDollar *float64 `json:"remaining_dollars"`
	}
	keys := make([]string, 0, len(payload))
	for k := range payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "extra_usage" || key == "limits" {
			continue
		}
		var w legacyWindow
		if err := json.Unmarshal(payload[key], &w); err != nil || w.Utilization == nil {
			continue
		}
		if w.LimitDollars != nil || w.UsedDollars != nil || w.RemainingDollar != nil {
			continue
		}
		window := claudeLegacyWindow(key)
		if seen[window] || (window == "five_hour" && hasLimits && !hasSession) {
			continue
		}
		seen[window] = true
		out = append(out, claudePollObservation(window, *w.Utilization, w.ResetsAt, observedAt))
	}
	if hasLimits && !hasSession {
		o := claudePollObservation("five_hour", 0, nil, observedAt)
		o.Status = statusNoLimit
		out = append(out, o)
	} else if raw, ok := payload["five_hour"]; ok && !seen["five_hour"] && string(raw) == "null" {
		out = append(out, claudePollObservation("five_hour", 0, nil, observedAt))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Window < out[b].Window })
	return out
}

type codexWindowPayload struct {
	UsedPercent        *float64 `json:"used_percent"`
	LimitWindowSeconds *float64 `json:"limit_window_seconds"`
	ResetAfterSeconds  *float64 `json:"reset_after_seconds"`
	ResetAt            *float64 `json:"reset_at"`
}

type codexRateLimitPayload struct {
	PrimaryWindow   *codexWindowPayload `json:"primary_window"`
	SecondaryWindow *codexWindowPayload `json:"secondary_window"`
}

// parseCodexUsagePayload reads chatgpt.com/backend-api/wham/usage.
func parseCodexUsagePayload(body []byte, observedAt time.Time) []observation {
	var payload struct {
		RateLimit            *codexRateLimitPayload `json:"rate_limit"`
		CodeReviewRateLimit  *codexRateLimitPayload `json:"code_review_rate_limit"`
		AdditionalRateLimits []struct {
			LimitName string                 `json:"limit_name"`
			RateLimit *codexRateLimitPayload `json:"rate_limit"`
		} `json:"additional_rate_limits"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	var out []observation
	add := func(prefix string, rl *codexRateLimitPayload) {
		if rl == nil {
			return
		}
		for _, w := range []struct {
			name string
			p    *codexWindowPayload
		}{{"primary", rl.PrimaryWindow}, {"secondary", rl.SecondaryWindow}} {
			if w.p == nil || w.p.UsedPercent == nil {
				continue
			}
			o := observation{
				Provider:    "codex",
				Window:      prefix + w.name,
				Utilization: *w.p.UsedPercent / 100,
				ObservedAt:  observedAt,
				Source:      "poll",
			}
			if w.p.LimitWindowSeconds != nil && *w.p.LimitWindowSeconds > 0 {
				o.PeriodSeconds = int64(*w.p.LimitWindowSeconds)
			}
			if w.p.ResetAt != nil && *w.p.ResetAt > 0 {
				o.ResetsAt = time.Unix(int64(*w.p.ResetAt), 0).UTC()
			} else if w.p.ResetAfterSeconds != nil && *w.p.ResetAfterSeconds >= 0 {
				o.ResetsAt = observedAt.Add(time.Duration(*w.p.ResetAfterSeconds * float64(time.Second)))
			}
			o.Status = codexStatus(o.Utilization)
			out = append(out, o)
		}
	}
	add("codex_", payload.RateLimit)
	add("codex_code_review_", payload.CodeReviewRateLimit)
	for _, extra := range payload.AdditionalRateLimits {
		if name := normalizeWindowName(extra.LimitName); name != "" {
			add("codex_"+name+"_", extra.RateLimit)
		}
	}
	return out
}

func normalizeWindowName(s string) string {
	var b strings.Builder
	lastUnderscore := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
