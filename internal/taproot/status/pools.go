package status

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/265866/taproot/internal/taproot/ingest"
)

const (
	// burnWindow is the trailing span the burn rate and the run-out
	// projection read.
	burnWindow = 3 * time.Hour
	// historySpan is how much quota history a pool reads: the burn window,
	// plus room for the observation before it, which the burn rate counts
	// from. An idle account is polled every 15 minutes by default.
	historySpan = burnWindow + 6*time.Hour
	// projectionSpan is how far ahead the pool looks for a reset or for
	// running out; it covers a weekly window.
	projectionSpan = 8 * 24 * time.Hour
)

// Pool is one provider's quota right now. Amounts are in accounts: 1 is one
// account's whole window.
type Pool struct {
	Provider string `json:"provider"`
	// UsableAccounts serve now; EnabledAccounts are every account not
	// disabled; TotalAccounts include the disabled ones.
	UsableAccounts  int `json:"usableAccounts"`
	EnabledAccounts int `json:"enabledAccounts"`
	TotalAccounts   int `json:"totalAccounts"`
	// LeftNow is the quota left on usable accounts.
	LeftNow float64 `json:"leftNow"`
	// PercentLeft is LeftNow as a share of the enabled accounts.
	PercentLeft     float64       `json:"percentLeft"`
	Accounts        []PoolAccount `json:"accounts"`
	LockedAccounts  LockedQuota   `json:"lockedAccounts"`
	WaitingAccounts WaitingQuota  `json:"waitingAccounts"`
	NextReset       *Reset        `json:"nextReset"`
	BurnPctPerHour  float64       `json:"burnPctPerHour"`
	RunsOutAt       *time.Time    `json:"runsOutAt"`
	LastsPastReset  bool          `json:"lastsPastReset"`
	// cooling holds the cooling slots, for the attention detail.
	cooling []PoolAccount
}

// PoolAccount is one slot of a pool bar.
type PoolAccount struct {
	AuthIndex   string     `json:"authIndex"`
	Label       string     `json:"label"`
	PercentLeft float64    `json:"percentLeft"`
	State       State      `json:"state"`
	Until       *time.Time `json:"until"`
	left        float64
	// name is the account's email, else its label, for text.
	name string
}

// LockedQuota is quota on accounts that serve only after the user acts:
// signed out or disabled.
type LockedQuota struct {
	Count int     `json:"count"`
	Left  float64 `json:"left"`
}

// WaitingQuota is quota on cooling accounts, which serve again on their own
// at Until (the earliest of them; null when unknown).
type WaitingQuota struct {
	Count int        `json:"count"`
	Left  float64    `json:"left"`
	Until *time.Time `json:"until"`
}

// Reset is the first reset that gives capacity back if usage stopped now.
type Reset struct {
	At   time.Time `json:"at"`
	Gain float64   `json:"gain"`
}

// buildPools projects each provider's pool. latest is the newest observation
// of each window, history the observations since now - historySpan.
func buildPools(accounts []Account, latest []ingest.QuotaObservation, history []ingest.QuotaSeries, now time.Time) []Pool {
	inputs := map[string]*accountInput{}
	input := func(idx string) *accountInput {
		in := inputs[idx]
		if in == nil {
			in = &accountInput{authIndex: idx}
			inputs[idx] = in
		}
		return in
	}
	for _, o := range latest {
		w := windowObs{
			window: o.Window, accountWide: o.AccountWide, utilization: o.Utilization,
			observedAt: msOf(o.ObservedAt), periodMs: float64(max(o.PeriodSeconds, 0)) * 1000,
		}
		if !o.ResetsAt.IsZero() {
			w.resetsAt, w.hasReset = msOf(o.ResetsAt), true
		}
		input(o.AuthIndex).windows = append(input(o.AuthIndex).windows, w)
	}
	for _, s := range history {
		ws := windowSeries{window: s.Window, accountWide: s.AccountWide}
		for _, p := range s.Points {
			ws.points = append(ws.points, seriesPoint{msOf(p.T), p.Utilization})
		}
		for _, r := range s.Resets {
			ws.resets = append(ws.resets, msOf(r))
		}
		input(s.AuthIndex).series = append(input(s.AuthIndex).series, ws)
	}

	byProvider := map[string][]Account{}
	for _, a := range accounts {
		byProvider[a.Provider] = append(byProvider[a.Provider], a)
	}
	nowMs := msOf(now)
	end := msOf(now.Add(projectionSpan))
	var out []Pool
	for provider, list := range byProvider {
		p := Pool{Provider: provider, TotalAccounts: len(list), Accounts: []PoolAccount{}}
		disambiguateLabels(list)
		var usable, serveLater, recorded []accountInput
		var lines [][]point
		sort.SliceStable(list, func(x, y int) bool {
			if (list[x].State == StateDisabled) != (list[y].State == StateDisabled) {
				return list[y].State == StateDisabled
			}
			return strings.ToLower(list[x].Label) < strings.ToLower(list[y].Label)
		})
		for _, a := range list {
			in := input(a.AuthIndex)
			left := projectAccount(*in, nowMs, nowMs, 0, 0).remainingNow
			slot := PoolAccount{AuthIndex: a.AuthIndex, Label: a.Label, PercentLeft: round1(left * 100), State: a.State, left: left, name: a.Name()}
			switch a.State {
			case StateServing:
				usable = append(usable, *in)
				serveLater = append(serveLater, *in)
				recorded = append(recorded, *in)
				p.LeftNow += left
			case StateCooling:
				serveLater = append(serveLater, *in)
				p.WaitingAccounts.Count++
				p.WaitingAccounts.Left += left
				if !a.Until.IsZero() {
					until := a.Until.UTC()
					slot.Until = &until
					if p.WaitingAccounts.Until == nil || until.Before(*p.WaitingAccounts.Until) {
						p.WaitingAccounts.Until = &until
					}
				}
				p.cooling = append(p.cooling, slot)
			default:
				p.LockedAccounts.Count++
				p.LockedAccounts.Left += left
			}
			if a.State != StateDisabled {
				p.EnabledAccounts++
			}
			p.Accounts = append(p.Accounts, slot)
		}
		p.UsableAccounts = len(usable)
		for _, in := range recorded {
			lines = append(lines, recordedAccount(in.series, nowMs-float64(burnWindow.Milliseconds()), nowMs))
		}
		if p.EnabledAccounts > 0 {
			p.PercentLeft = round1(100 * p.LeftNow / float64(p.EnabledAccounts))
			p.BurnPctPerHour = round1(100 * drainAt(sumPolylines(lines), nowMs, float64(burnWindow.Milliseconds())) / float64(p.EnabledAccounts))
		}
		if r := projectPool(serveLater, nowMs, end, 0, 0).nextReset; r != nil {
			p.NextReset = &Reset{At: timeOf(r.t), Gain: round2(r.gain)}
		}
		switch {
		case len(usable) == 0 || p.LeftNow <= epsilon:
			at := now.UTC()
			p.RunsOutAt = &at
		default:
			if empty := projectPool(usable, nowMs, end, float64(burnWindow.Milliseconds()), 1).emptyAt; !math.IsNaN(empty) {
				at := timeOf(empty)
				p.RunsOutAt = &at
			}
		}
		p.LastsPastReset = p.NextReset != nil && (p.RunsOutAt == nil || p.RunsOutAt.After(p.NextReset.At))
		p.LeftNow = round2(p.LeftNow)
		p.LockedAccounts.Left = round2(p.LockedAccounts.Left)
		p.WaitingAccounts.Left = round2(p.WaitingAccounts.Left)
		out = append(out, p)
	}
	sort.Slice(out, func(x, y int) bool { return compareProviders(out[x].Provider, out[y].Provider) })
	return out
}

// disambiguateLabels names accounts by their full email when two accounts of
// one pool share the part before the @, such as alice@example.com and
// alice@example.org.
func disambiguateLabels(list []Account) {
	seen := map[string]int{}
	for _, a := range list {
		seen[strings.ToLower(a.Label)]++
	}
	for k, a := range list {
		if a.Email != "" && seen[strings.ToLower(a.Label)] > 1 {
			list[k].Label = a.Email
		}
	}
}

func msOf(t time.Time) float64 { return float64(t.UnixMilli()) }

func timeOf(ms float64) time.Time { return time.UnixMilli(int64(math.Round(ms))).UTC() }

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// compareProviders puts Claude, then Codex, then the rest alphabetically,
// as the dashboard orders them.
func compareProviders(a, b string) bool {
	rank := func(p string) int {
		switch p {
		case "claude":
			return 0
		case "codex":
			return 1
		}
		return 2
	}
	if rank(a) != rank(b) {
		return rank(a) < rank(b)
	}
	return a < b
}

// providerName is how text names a provider.
func providerName(p string) string {
	switch p {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	case "":
		return "Unknown"
	}
	return strings.ToUpper(p[:1]) + p[1:]
}
