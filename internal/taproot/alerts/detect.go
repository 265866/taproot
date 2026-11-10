package alerts

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/265866/taproot/internal/taproot/ingest"
)

// Severity picks the embed color.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityCritical
	SeverityRecovered
)

// Field is one "**Name** value" line of a message.
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Message is one alert or recovery notice.
type Message struct {
	Key         string    `json:"key"`
	Severity    Severity  `json:"severity"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Fields      []Field   `json:"fields,omitempty"`
	At          time.Time `json:"at"`
	// tracked marks a message from Detector.Step, whose delivery outcome goes
	// back to the detector.
	tracked bool
}

// Account is a credential as alerts see it: present in the auth manager and
// not disabled by the user.
type Account struct {
	AuthIndex   string
	Provider    string
	Label       string
	LoginBroken bool
	LoginReason string
	// StillServes marks a broken login whose current access token upstream
	// still uses, until ServesUntil when that is known.
	StillServes bool
	ServesUntil time.Time
}

// Grant is one Claude reset grant (cedar_ember) read from the usage endpoint.
type Grant struct {
	ID         string
	Label      string
	ResetsLeft int
	EndsAt     time.Time
}

// Snapshot is everything the detector looks at in one evaluation.
type Snapshot struct {
	Now      time.Time
	Accounts []Account
	// Quota holds the latest observation per account window.
	Quota []ingest.QuotaObservation
	// Grants maps a Claude auth index to its last read grants.
	Grants map[string][]Grant
}

// Condition is a problem that holds right now. Recovery, when set, is posted
// once the condition has cleared and stayed clear.
type Condition struct {
	Alert    Message
	Recovery *Message
	// Delay is how long the condition must hold before it is posted, for
	// states upstream can set briefly while it retries.
	Delay time.Duration
	// Until, when set, keeps a posted alert's episode open until then even
	// while the condition is not seen, for a condition that cannot recover
	// before it ends, such as a grant's expiry.
	Until time.Time
}

const grantWarnWithin = 24 * time.Hour

// loginDelay lets upstream finish a refresh after a single 401 before the
// login counts as broken.
const loginDelay = 2 * time.Minute

// Conditions lists every condition the snapshot shows, keyed by a stable
// dedupe key.
func Conditions(s Snapshot) map[string]Condition {
	out := map[string]Condition{}
	accounts := map[string]Account{}
	for _, a := range s.Accounts {
		accounts[a.AuthIndex] = a
	}

	// limitedUntil is, per account, when it is usable again: the latest reset
	// of its account-wide windows at their limit. The zero time means one of
	// those windows has no known reset.
	limitedUntil := map[string]time.Time{}
	limited := map[string]bool{}
	for _, o := range s.Quota {
		a, ok := accounts[o.AuthIndex]
		if !ok || !atLimit(o, s.Now) {
			continue
		}
		key := "limit:" + o.AuthIndex + ":" + o.Window
		out[key] = limitCondition(key, a, o)
		if o.AccountWide {
			// The account is back when its last limited window resets; one
			// window with no known reset makes the whole account unknown.
			until := limitedUntil[o.AuthIndex]
			switch {
			case !limited[o.AuthIndex]:
				until = o.ResetsAt
			case until.IsZero():
			case o.ResetsAt.IsZero() || o.ResetsAt.After(until):
				until = o.ResetsAt
			}
			limitedUntil[o.AuthIndex], limited[o.AuthIndex] = until, true
		}
	}

	byProvider := map[string][]Account{}
	for _, a := range s.Accounts {
		if a.LoginBroken {
			key := "login:" + a.AuthIndex
			out[key] = loginCondition(key, a)
		}
		byProvider[a.Provider] = append(byProvider[a.Provider], a)
	}
	for provider, list := range byProvider {
		if c, ok := poolCondition(provider, list, limited, limitedUntil); ok {
			out[c.Alert.Key] = c
		}
	}

	for idx, grants := range s.Grants {
		a, ok := accounts[idx]
		if !ok {
			continue
		}
		for _, g := range grants {
			if g.ResetsLeft <= 0 || g.EndsAt.IsZero() || !g.EndsAt.After(s.Now) || g.EndsAt.Sub(s.Now) > grantWarnWithin {
				continue
			}
			key := "grant:" + idx + ":" + g.ID
			out[key] = grantCondition(key, a, g)
		}
	}
	return out
}

// atLimit reports whether a window blocks traffic now. A window whose reset
// has passed is usable again even before a new observation arrives. With no
// reset known, a window of known length has reset one period after the
// reading at the latest.
func atLimit(o ingest.QuotaObservation, now time.Time) bool {
	if o.Status != "rejected" && o.Utilization < 1 {
		return false
	}
	if !o.ResetsAt.IsZero() {
		return o.ResetsAt.After(now)
	}
	return o.PeriodSeconds <= 0 || o.ObservedAt.Add(time.Duration(o.PeriodSeconds)*time.Second).After(now)
}

func limitCondition(key string, a Account, o ingest.QuotaObservation) Condition {
	window := windowLabel(o)
	scope := ""
	if !o.AccountWide {
		scope = " Other models on this account still work."
	}
	desc := fmt.Sprintf("**%s** is at its %s limit.%s", a.Label, window, scope)
	if !o.ResetsAt.IsZero() {
		desc = fmt.Sprintf("**%s** is at its %s limit until %s.%s", a.Label, window, discordTime(o.ResetsAt), scope)
	}
	return Condition{
		Alert: Message{
			Key: key, Severity: SeverityWarning,
			Title:       fmt.Sprintf("%s account hit its %s limit", providerName(a.Provider), window),
			Description: desc,
			Fields: []Field{
				{Name: "Account", Value: a.Label},
				{Name: "Provider", Value: providerName(a.Provider)},
				{Name: "Window", Value: window},
				{Name: "Used", Value: fmt.Sprintf("%.0f%%", o.Utilization*100)},
				{Name: "Resets", Value: resetText(o.ResetsAt)},
			},
		},
		Recovery: &Message{
			Key: key, Severity: SeverityRecovered,
			Title:       fmt.Sprintf("%s account usable again", providerName(a.Provider)),
			Description: fmt.Sprintf("**%s** is below its %s limit again.", a.Label, window),
			Fields: []Field{
				{Name: "Account", Value: a.Label},
				{Name: "Window", Value: window},
			},
		},
	}
}

func loginCondition(key string, a Account) Condition {
	fields := []Field{
		{Name: "Account", Value: a.Label},
		{Name: "Provider", Value: providerName(a.Provider)},
	}
	desc := fmt.Sprintf("**%s** can no longer refresh its credential and serves no requests until you sign in again from OAuth Login.", a.Label)
	if a.StillServes {
		until := "its current access token expires"
		if !a.ServesUntil.IsZero() {
			until += " at " + discordTime(a.ServesUntil)
		}
		desc = fmt.Sprintf("**%s** can no longer refresh its credential. It keeps working until %s; sign in again from OAuth Login before then.", a.Label, until)
		fields = append(fields, Field{Name: "Works until", Value: resetText(a.ServesUntil)})
	}
	if a.LoginReason != "" {
		fields = append(fields, Field{Name: "Reason", Value: a.LoginReason})
	}
	return Condition{
		Alert: Message{
			Key: key, Severity: SeverityWarning,
			Title:       fmt.Sprintf("%s login broken", providerName(a.Provider)),
			Description: desc,
			Fields:      fields,
		},
		Recovery: &Message{
			Key: key, Severity: SeverityRecovered,
			Title:       fmt.Sprintf("%s login works again", providerName(a.Provider)),
			Description: fmt.Sprintf("**%s** has a working credential again.", a.Label),
		},
		Delay: loginDelay,
	}
}

// poolCondition reports a provider whose every account is at an account-wide
// limit or needs sign-in and no longer serves. When a broken login is part of
// the reason, the pool waits out the login delay too.
func poolCondition(provider string, list []Account, limited map[string]bool, limitedUntil map[string]time.Time) (Condition, bool) {
	if len(list) == 0 {
		return Condition{}, false
	}
	var back time.Time
	var backLabel string
	broken := 0
	for _, a := range list {
		switch {
		case a.LoginBroken && !a.StillServes:
			broken++
		case limited[a.AuthIndex]:
			if t := limitedUntil[a.AuthIndex]; !t.IsZero() && (back.IsZero() || t.Before(back)) {
				back, backLabel = t, a.Label
			}
		default:
			return Condition{}, false
		}
	}
	name := providerName(provider)
	key := "pool:" + provider
	desc := fmt.Sprintf("Every %s account is out of quota or needs sign-in, so %s requests fail.", name, name)
	var delay time.Duration
	if broken > 0 {
		delay = loginDelay
	}
	backText := "no known reset"
	if !back.IsZero() {
		desc += fmt.Sprintf(" The first one back is **%s** at %s.", backLabel, discordTime(back))
		backText = discordTime(back)
	} else if broken == len(list) {
		desc += " None comes back on its own; sign in again from OAuth Login."
	}
	return Condition{
		Alert: Message{
			Key: key, Severity: SeverityCritical,
			Title:       fmt.Sprintf("%s pool is empty", name),
			Description: desc,
			Fields: []Field{
				{Name: "Provider", Value: name},
				{Name: "Accounts", Value: fmt.Sprintf("%d", len(list))},
				{Name: "Needs sign-in", Value: fmt.Sprintf("%d", broken)},
				{Name: "First back", Value: backText},
			},
		},
		Recovery: &Message{
			Key: key, Severity: SeverityRecovered,
			Title:       fmt.Sprintf("%s pool has capacity again", name),
			Description: fmt.Sprintf("At least one %s account is usable again.", name),
		},
		Delay: delay,
	}, true
}

func grantCondition(key string, a Account, g Grant) Condition {
	label := g.Label
	if label == "" {
		label = g.ID
	}
	return Condition{
		Alert: Message{
			Key: key, Severity: SeverityInfo,
			Title: "Claude reset grant expires soon",
			Description: fmt.Sprintf("**%s** has %d unused reset%s on grant *%s*, which expires %s. Redeem it from the dashboard before then.",
				a.Label, g.ResetsLeft, plural(g.ResetsLeft), label, discordTime(g.EndsAt)),
			Fields: []Field{
				{Name: "Account", Value: a.Label},
				{Name: "Resets left", Value: fmt.Sprintf("%d", g.ResetsLeft)},
				{Name: "Expires", Value: discordTime(g.EndsAt)},
			},
		},
		Until: g.EndsAt,
	}
}

// track is the detector's memory of one key.
type track struct {
	Active bool `json:"active"`
	// Notified is set from the alert until its recovery is posted. While
	// Queued, the alert waits for its delivery outcome.
	Notified  bool      `json:"notified"`
	Queued    bool      `json:"queued,omitempty"`
	ChangedAt time.Time `json:"changedAt"`
	LastAlert time.Time `json:"lastAlert"`
	Recovery  *Message  `json:"recovery,omitempty"`
	// Until is the posted condition's Until.
	Until time.Time `json:"until,omitzero"`
	// RetryAt holds back a message whose delivery failed; Failures counts
	// the failures in a row.
	RetryAt  time.Time `json:"retryAt,omitzero"`
	Failures int       `json:"failures,omitempty"`
}

// undeliver forgets the key's alert as if it was never sent.
func (t *track) undeliver() {
	t.Notified, t.Queued, t.LastAlert, t.Recovery, t.Until = false, false, time.Time{}, nil, time.Time{}
}

const (
	retryFirst = time.Minute
	retryMax   = 30 * time.Minute
)

// failed holds the key back for a delay that doubles with each failure in a row.
func (t *track) failed(now time.Time) {
	delay := retryMax
	if t.Failures < 5 {
		delay = retryFirst << t.Failures
	}
	t.RetryAt, t.Failures = now.Add(delay), t.Failures+1
}

// Detector turns conditions into messages. A key alerts once per episode; a
// recovery waits until the condition has stayed clear for settle, so a flap
// inside that time continues the same episode; and a new alert for the same
// key waits for cooldown after the previous one. An alert counts as posted
// only once Delivered reports it delivered: a failed one is sent again while
// its condition holds, and its recovery is never posted.
type Detector struct {
	cooldown time.Duration
	settle   time.Duration
	tracks   map[string]*track
}

// NewDetector returns a detector with no history.
func NewDetector(cooldown, settle time.Duration) *Detector {
	return &Detector{cooldown: cooldown, settle: settle, tracks: map[string]*track{}}
}

// Step compares the current conditions with the detector's memory and
// returns the messages to post, ordered by key. changed reports whether the
// memory changed and should be saved.
func (d *Detector) Step(now time.Time, active map[string]Condition) (out []Message, changed bool) {
	for key, c := range active {
		t := d.tracks[key]
		if t == nil {
			t = &track{}
			d.tracks[key] = t
		}
		if !t.Active {
			t.Active, t.ChangedAt, changed = true, now, true
		}
		if t.Notified || now.Sub(t.ChangedAt) < c.Delay || now.Before(t.RetryAt) {
			continue
		}
		if !t.LastAlert.IsZero() && now.Sub(t.LastAlert) < d.cooldown {
			continue
		}
		msg := c.Alert
		msg.At, msg.tracked = now, true
		out = append(out, msg)
		t.Notified, t.Queued, t.LastAlert, t.Recovery, t.Until, changed = true, true, now, c.Recovery, c.Until, true
	}
	for key, t := range d.tracks {
		if _, ok := active[key]; ok {
			continue
		}
		if t.Active {
			t.Active, t.ChangedAt, changed = false, now, true
		}
		if t.Notified && (t.Queued || now.Before(t.Until) || now.Before(t.RetryAt)) {
			continue
		}
		if t.Notified && now.Sub(t.ChangedAt) >= d.settle {
			if t.Recovery != nil {
				msg := *t.Recovery
				msg.At, msg.tracked = now, true
				out = append(out, msg)
			}
			t.Notified, t.Recovery, changed = false, nil, true
		}
		if !t.Notified && (t.LastAlert.IsZero() || now.Sub(t.LastAlert) >= d.cooldown) {
			delete(d.tracks, key)
			changed = true
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, changed
}

// Delivered records the delivery outcome of m, a message from Step. A failed
// alert is forgotten, so it is sent again while its condition holds, and no
// recovery follows it. A failed recovery is sent again unless a new alert for
// the key is already out. A failed delivery waits a growing delay first; a
// message the queue never sent (errNotSent) does not.
func (d *Detector) Delivered(now time.Time, m Message, err error) (changed bool) {
	t := d.tracks[m.Key]
	if m.Severity != SeverityRecovered {
		if t == nil || !t.Queued || !t.LastAlert.Equal(m.At) {
			return false
		}
		t.Queued = false
		if err == nil {
			t.Failures = 0
			return true
		}
		t.undeliver()
		if !errors.Is(err, errNotSent) {
			t.failed(now)
		}
		return true
	}
	if err == nil {
		return false
	}
	if t == nil {
		// Step dropped the settled track; ChangedAt stays zero, so the retry
		// needs no new settle.
		t = &track{}
		d.tracks[m.Key] = t
	} else if t.Notified {
		return false
	}
	recovery := m
	t.Notified, t.Recovery = true, &recovery
	if !errors.Is(err, errNotSent) {
		t.failed(now)
	}
	return true
}

// Rearm forgets which alerts reached the channel, for a new webhook: open
// conditions alert again there, and recoveries of alerts it never saw are
// dropped. Alerts still waiting in the queue go to the new webhook as they are.
func (d *Detector) Rearm() (changed bool) {
	for _, t := range d.tracks {
		if t.Notified && !t.Queued {
			t.undeliver()
			t.RetryAt, t.Failures = time.Time{}, 0
			changed = true
		}
	}
	return changed
}

// Forget drops, without a recovery, the tracks of accounts and providers no
// longer in accounts: an account the user removed or disabled did not
// recover. Pool keys name a provider; every other key names an auth index.
func (d *Detector) Forget(accounts []Account) (changed bool) {
	indexes, providers := map[string]bool{}, map[string]bool{}
	for _, a := range accounts {
		indexes[a.AuthIndex], providers[a.Provider] = true, true
	}
	for key := range d.tracks {
		kind, subject, _ := strings.Cut(key, ":")
		if kind == "pool" {
			if providers[subject] {
				continue
			}
		} else if idx, _, _ := strings.Cut(subject, ":"); indexes[idx] {
			continue
		}
		delete(d.tracks, key)
		changed = true
	}
	return changed
}

func providerName(p string) string {
	switch strings.ToLower(p) {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	case "":
		return "Unknown"
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

var windowLabels = map[string]string{
	"five_hour":                  "5-hour",
	"seven_day":                  "weekly",
	"seven_day_opus":             "weekly Opus",
	"seven_day_sonnet":           "weekly Sonnet",
	"seven_day_fable":            "weekly Fable",
	"seven_day_overage_included": "weekly overage",
	"codex_primary":              "5-hour",
	"codex_secondary":            "weekly",
}

// windowLabel names o's window. Codex names windows by header slot, and
// either slot can hold the 5-hour, weekly, or monthly window, so a Codex
// window of known length is named by its length.
func windowLabel(o ingest.QuotaObservation) string {
	if rest, ok := strings.CutPrefix(o.Window, "codex_"); ok {
		if period := periodLabel(o.PeriodSeconds); period != "" {
			limit := rest
			for _, slot := range []string{"primary", "secondary"} {
				if cut, found := strings.CutSuffix(limit, slot); found && (cut == "" || strings.HasSuffix(cut, "_")) {
					limit = strings.TrimSuffix(cut, "_")
					break
				}
			}
			return strings.TrimSpace(period + " " + strings.ReplaceAll(limit, "_", " "))
		}
	}
	if l, ok := windowLabels[o.Window]; ok {
		return l
	}
	return strings.ReplaceAll(strings.TrimPrefix(o.Window, "codex_"), "_", " ")
}

func periodLabel(seconds int64) string {
	const day = 24 * 3600
	switch {
	case seconds == 5*3600:
		return "5-hour"
	case seconds == 7*day:
		return "weekly"
	case seconds >= 28*day && seconds <= 31*day:
		return "monthly"
	}
	return ""
}

// discordTime renders in each reader's own time zone, with a relative hint.
func discordTime(t time.Time) string {
	return fmt.Sprintf("<t:%d:f> (<t:%d:R>)", t.Unix(), t.Unix())
}

func resetText(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return discordTime(t)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
