package status

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/alerts"
	"github.com/265866/taproot/internal/taproot/backup"
)

// Severities, most pressing first.
const (
	SeverityBlocking = "blocking"
	SeverityAtRisk   = "at_risk"
	SeverityWaiting  = "waiting"
	SeverityOverdue  = "overdue"
)

var severityRank = map[string]int{SeverityBlocking: 0, SeverityAtRisk: 1, SeverityWaiting: 2, SeverityOverdue: 3}

const (
	// lowPercent is the share of a pool below which it needs attention
	// whatever its burn.
	lowPercent = 15
	// backupMaxAge is how old the newest backup may get while the daily
	// schedule is on: a day, plus room for a late or slow run.
	backupMaxAge = 26 * time.Hour
	// tailscaledDocs is the troubleshooting entry for an unreachable
	// tailscaled.
	tailscaledDocs = "https://github.com/265866/taproot/blob/main/docs/troubleshooting.md#every-tailnet-device-is-refused-after-tailscaled-restarts-or-updates"
	// maxReason bounds upstream's failure text in a detail line.
	maxReason = 200
)

// Item is one thing that needs the user.
type Item struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Severity string    `json:"severity"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail"`
	Since    time.Time `json:"since"`
	Subject  Subject   `json:"subject"`
	Action   Action    `json:"action"`
}

// Subject is what an item is about.
type Subject struct {
	// Type is account, provider, device, backups, alerts, or tailscaled.
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Action is the one fix an item offers.
type Action struct {
	// Type is sign_in (target: provider), enable (auth index),
	// review_access (node ID), backup_now, open_settings (section),
	// open_accounts (provider), or docs (URL).
	Type   string `json:"type"`
	Target string `json:"target"`
}

// Counts are the items per sidebar area.
type Counts struct {
	Accounts int `json:"accounts"`
	Access   int `json:"access"`
	Backups  int `json:"backups"`
	Total    int `json:"total"`
}

// Attention is the Needs-you list.
type Attention struct {
	Items  []Item    `json:"items"`
	Counts Counts    `json:"counts"`
	At     time.Time `json:"at"`
}

// backupState is what the backup module says about recent backups.
type backupState struct {
	// scheduled is false while the daily schedule is off.
	scheduled bool
	// last is the newest backup of any kind; zero when there is none.
	last   time.Time
	run    backup.Run
	hasRun bool
	// watchingSince is when this process started, which bounds how long a
	// server without any backup has gone without one.
	watchingSince time.Time
}

// tailscaledDown is a reported tailscaled outage.
type tailscaledDown struct {
	socket, err string
	since       time.Time
	stale       bool
}

type attentionInputs struct {
	now        time.Time
	accounts   []Account
	pools      []Pool
	recent     map[string]int64
	requests   []access.AccessRequest
	backup     backupState
	alerts     alerts.DeliveryHealth
	tailscaled *tailscaledDown
	// firstSeen returns when an item was first listed, for items whose
	// start the data does not say.
	firstSeen func(id string) time.Time
}

func attention(in attentionInputs) Attention {
	var items []Item
	add := func(it Item) {
		if it.Since.IsZero() {
			it.Since = in.firstSeen(it.ID)
		}
		it.Since = it.Since.UTC()
		items = append(items, it)
	}
	pools := map[string]Pool{}
	for _, p := range in.pools {
		pools[p.Provider] = p
	}
	for _, a := range in.accounts {
		if it, ok := accountItem(a, pools[a.Provider], in.recent[a.Provider], in.now); ok {
			add(it)
		}
	}
	for _, p := range in.pools {
		if it, ok := poolItem(p, in.now); ok {
			add(it)
		}
	}
	for _, r := range in.requests {
		add(requestItem(r))
	}
	if it, ok := backupItem(in.backup, in.now); ok {
		add(it)
	}
	if h := in.alerts; h.Active && h.Failures > 0 {
		add(Item{
			ID: "alerts", Kind: "alerts_failing", Severity: SeverityAtRisk,
			Title:   "Discord alerts are not being delivered",
			Detail:  deliveryDetail(h),
			Since:   h.FailingSince,
			Subject: Subject{Type: "alerts"},
			Action:  Action{Type: "open_settings", Target: "alerts"},
		})
	}
	if t := in.tailscaled; t != nil {
		detail := fmt.Sprintf("Calls to tailscaled on %s are failing (%s), so taproot refuses every tailnet device, admins included.", t.socket, t.err)
		if t.stale {
			detail += " If tailscaled restarted or updated, restart taproot."
		} else {
			detail += " Check that tailscaled runs and is logged in on the host."
		}
		add(Item{
			ID: "tailscaled", Kind: "tailscaled_down", Severity: SeverityBlocking,
			Title: "Taproot can't reach tailscaled", Detail: detail, Since: t.since,
			Subject: Subject{Type: "tailscaled"},
			Action:  Action{Type: "docs", Target: tailscaledDocs},
		})
	}

	sort.SliceStable(items, func(x, y int) bool {
		a, b := items[x], items[y]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		return a.ID < b.ID
	})
	out := Attention{Items: []Item{}, At: in.now}
	for _, it := range items {
		out.Items = append(out.Items, it)
		switch {
		case strings.HasPrefix(it.Kind, "account_"), it.Kind == "pool_low":
			out.Counts.Accounts++
		case it.Kind == "access_request", it.Kind == "tailscaled_down":
			out.Counts.Access++
		case it.Kind == "backup_overdue":
			out.Counts.Backups++
		}
	}
	out.Counts.Total = len(out.Items)
	return out
}

func accountItem(a Account, p Pool, recent int64, now time.Time) (Item, bool) {
	name := providerName(a.Provider)
	it := Item{Subject: Subject{Type: "account", ID: a.AuthIndex}, Since: a.Since}
	switch {
	case a.State == StateSignedOut:
		it.ID, it.Kind, it.Severity = "account:"+a.AuthIndex+":signed_out", "account_signed_out", SeverityBlocking
		it.Title = fmt.Sprintf("%s: %s is signed out", name, a.Name())
		it.Detail = sentence(a.Reason) + fmt.Sprintf("%s is serving from %d of %d %s", name, p.UsableAccounts, p.EnabledAccounts, plural(int64(p.EnabledAccounts), "account", "accounts"))
		if left := slotLeft(p, a.AuthIndex); left > 0 {
			it.Detail += fmt.Sprintf("; %.2f of its quota is locked until you sign in again.", left)
		} else {
			it.Detail += "."
		}
		it.Action = Action{Type: "sign_in", Target: a.Provider}
	case a.RefreshFailing:
		it.ID, it.Kind, it.Severity = "account:"+a.AuthIndex+":refresh_failing", "account_refresh_failing", SeverityAtRisk
		it.Title = fmt.Sprintf("%s: %s can't refresh its login", name, a.Name())
		it.Detail = sentence(a.Reason) + "It keeps serving until its access token expires"
		if !a.ServesUntil.IsZero() {
			it.Detail += " " + inAbout(a.ServesUntil.Sub(now))
		}
		it.Detail += "; sign in again before then."
		it.Action = Action{Type: "sign_in", Target: a.Provider}
	case a.State == StateDisabled && p.UsableAccounts == 0 && recent > 0:
		it.ID, it.Kind, it.Severity = "account:"+a.AuthIndex+":disabled", "account_disabled", SeverityBlocking
		it.Title = fmt.Sprintf("%s: %s is disabled", name, a.Name())
		it.Detail = fmt.Sprintf("No %s account can serve, and clients sent %d %s requests in the last hour. Enable it to serve them again.", name, recent, name)
		it.Action = Action{Type: "enable", Target: a.AuthIndex}
		it.Since = time.Time{}
	default:
		return Item{}, false
	}
	return it, true
}

// poolItem reports a pool that may run out before its next reset or has
// under lowPercent left. Cooling accounts are named in it rather than listed
// on their own: they matter only when the rest of the pool cannot carry the
// load until they are back. A pool with no usable account and none cooling
// gets no item; its signed-out or disabled accounts have their own.
func poolItem(p Pool, now time.Time) (Item, bool) {
	if p.EnabledAccounts == 0 {
		return Item{}, false
	}
	name := providerName(p.Provider)
	it := Item{
		ID: "pool:" + p.Provider, Kind: "pool_low",
		Subject: Subject{Type: "provider", ID: p.Provider},
		Action:  Action{Type: "open_accounts", Target: p.Provider},
	}
	if p.UsableAccounts == 0 {
		if p.WaitingAccounts.Count == 0 {
			return Item{}, false
		}
		it.Severity = SeverityBlocking
		it.Title = fmt.Sprintf("%s has no usable account", name)
		it.Detail = coolingLine(p, now)
		return it, true
	}
	runsOutFirst := p.RunsOutAt != nil && !p.LastsPastReset
	if !runsOutFirst && p.PercentLeft >= lowPercent {
		return Item{}, false
	}
	it.Severity = SeverityAtRisk
	switch {
	case runsOutFirst && p.NextReset != nil:
		it.Title = fmt.Sprintf("%s pool may run out before its next reset", name)
	case runsOutFirst:
		it.Title = fmt.Sprintf("%s pool may run out", name)
	default:
		it.Title = fmt.Sprintf("%s pool is under %d%%", name, lowPercent)
	}
	it.Detail = fmt.Sprintf("%.2f left on %d usable %s (%s of the pool), burning %s an hour",
		p.LeftNow, p.UsableAccounts, plural(int64(p.UsableAccounts), "account", "accounts"), percent(p.PercentLeft), percent(p.BurnPctPerHour))
	if p.RunsOutAt != nil {
		it.Detail += ": empty " + inAbout(p.RunsOutAt.Sub(now))
	}
	if p.NextReset != nil {
		it.Detail += ", next reset " + inAbout(p.NextReset.At.Sub(now))
	}
	it.Detail += "."
	if p.WaitingAccounts.Count > 0 {
		it.Detail += " " + coolingLine(p, now)
	}
	return it, true
}

// coolingLine names the cooling accounts and when they are back.
func coolingLine(p Pool, now time.Time) string {
	parts := make([]string, 0, len(p.cooling))
	for _, c := range p.cooling {
		s := c.name + " is cooling down"
		if c.Until != nil {
			s += ", back " + inAbout(c.Until.Sub(now))
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ") + "."
}

func requestItem(r access.AccessRequest) Item {
	name := cmp.Or(r.NameHint, r.Node, "A device")
	ask := "Proxy"
	if r.Asked == access.AskAdmin {
		ask = "Admin"
	}
	var parts []string
	for _, p := range []string{r.Login, r.Node, r.Client} {
		if p != "" && p != name && !slices.Contains(parts, p) {
			parts = append(parts, p)
		}
	}
	return Item{
		ID: "access:" + r.NodeID, Kind: "access_request", Severity: SeverityWaiting,
		Title:   fmt.Sprintf("%s asks for %s access", name, ask),
		Detail:  strings.Join(parts, " · "),
		Since:   r.FirstSeen,
		Subject: Subject{Type: "device", ID: r.NodeID},
		Action:  Action{Type: "review_access", Target: r.NodeID},
	}
}

func backupItem(b backupState, now time.Time) (Item, bool) {
	if !b.scheduled {
		return Item{}, false
	}
	newest := b.last
	if newest.IsZero() {
		newest = b.watchingSince
	}
	stale := now.Sub(newest) > backupMaxAge
	// A backup written after the failed run, by hand or by a later run,
	// settles it.
	failed := b.hasRun && b.run.Error != "" && (b.last.IsZero() || b.last.Before(b.run.At))
	if !stale && !failed {
		return Item{}, false
	}
	it := Item{
		ID: "backup", Kind: "backup_overdue", Severity: SeverityOverdue,
		Subject: Subject{Type: "backups"},
		Action:  Action{Type: "backup_now"},
	}
	switch {
	case stale && b.last.IsZero():
		it.Title = "No backup yet"
	case stale:
		it.Title = fmt.Sprintf("Last backup was %d h ago", int(now.Sub(b.last)/time.Hour))
	default:
		it.Title = "The last scheduled backup failed"
	}
	switch {
	case failed:
		it.Detail = fmt.Sprintf("The scheduled run %s ago failed: %s.", roughly(now.Sub(b.run.At)), strings.TrimSuffix(clip(b.run.Error), "."))
		it.Since = b.run.At
	default:
		it.Detail = "The daily backup has not written one; the server's log says why."
	}
	if stale {
		if since := newest.Add(backupMaxAge); it.Since.IsZero() || since.Before(it.Since) {
			it.Since = since
		}
	}
	return it, true
}

func slotLeft(p Pool, authIndex string) float64 {
	for _, s := range p.Accounts {
		if s.AuthIndex == authIndex {
			return round2(s.left)
		}
	}
	return 0
}

// sentence ends s with a period and a space, for text that continues; ""
// stays "".
func sentence(s string) string {
	s = strings.TrimSpace(clip(s))
	if s == "" {
		return ""
	}
	// A leading error code such as refresh_outcome_unknown keeps its case.
	if r := []rune(s); unicode.IsLower(r[0]) && !strings.Contains(strings.Fields(s)[0], "_") {
		r[0] = unicode.ToUpper(r[0])
		s = string(r)
	}
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s + " "
}

func clip(s string) string {
	if r := []rune(s); len(r) > maxReason {
		return string(r[:maxReason-1]) + "…"
	}
	return s
}

func percent(v float64) string {
	if v > 0 && v < 1 {
		return fmt.Sprintf("%.1f%%", v)
	}
	return fmt.Sprintf("%.0f%%", v)
}

// inAbout says when something happens relative to now, as "in about 2 h
// 40 min" or "now".
func inAbout(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	return "in about " + roughly(d)
}

// roughly rounds a duration the way a person would say it.
func roughly(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 10*time.Hour:
		h, m := int(d/time.Hour), int((d%time.Hour)/(10*time.Minute))*10
		if m == 0 {
			return fmt.Sprintf("%d h", h)
		}
		return fmt.Sprintf("%d h %d min", h, m)
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d/time.Hour))
	}
	return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
}

func deliveryDetail(h alerts.DeliveryHealth) string {
	what := "The last delivery"
	if h.Failures > 1 {
		what = fmt.Sprintf("The last %d deliveries", h.Failures)
	}
	return fmt.Sprintf("%s failed (%s). Check the webhook in Settings.", what, h.LastError)
}
