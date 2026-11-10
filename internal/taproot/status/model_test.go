package status

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/alerts"
	"github.com/265866/taproot/internal/taproot/backup"
	"github.com/265866/taproot/internal/taproot/ingest"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
)

var t0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func auth(provider, email string, opts ...func(*coreauth.Auth)) *coreauth.Auth {
	a := &coreauth.Auth{
		ID: provider + "-" + email, Provider: provider,
		Metadata:   map[string]any{"email": email, "access_token": "access-" + email, "expired": t0.Add(24 * time.Hour).Format(time.RFC3339)},
		Attributes: map[string]string{coreauth.AttributePath: "/auths/" + provider + "-" + email + ".json"},
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

func signedOut(a *coreauth.Auth) {
	a.RequiresSignIn = true
	a.Metadata["expired"] = t0.Add(-time.Hour).Format(time.RFC3339)
	a.RefreshError = &coreauth.Error{Message: "refresh token rejected"}
	a.RenewalEventAt = t0.Add(-44 * time.Minute)
}

func refreshFailing(a *coreauth.Auth) {
	a.RequiresSignIn = true
	a.RefreshError = &coreauth.Error{Message: "refresh token rejected"}
	a.RenewalEventAt = t0.Add(-10 * time.Minute)
}

func disabled(a *coreauth.Auth) { a.Disabled, a.Status = true, coreauth.StatusDisabled }

// coolingFor holds the whole credential back, as upstream does after a
// quota response.
func coolingFor(d time.Duration) func(*coreauth.Auth) {
	return func(a *coreauth.Auth) {
		a.Unavailable = true
		a.NextRetryAfter = t0.Add(d)
		a.Quota = coreauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: t0.Add(d)}
	}
}

// weekly is an account's weekly window at utilization u, observed now,
// resetting in resetIn.
func weekly(a *coreauth.Auth, u float64, resetIn time.Duration) ingest.QuotaObservation {
	window := "seven_day"
	if a.Provider == "codex" {
		window = "codex_secondary"
	}
	return ingest.QuotaObservation{
		AuthIndex: a.EnsureIndex(), Provider: a.Provider, Window: window, Utilization: u,
		ResetsAt: t0.Add(resetIn), ObservedAt: t0, PeriodSeconds: 7 * 24 * 3600, AccountWide: true,
	}
}

// burning is a weekly window that rose by perHour each hour for the last
// three hours, ending at u now.
func burning(a *coreauth.Auth, u, perHour float64) ingest.QuotaSeries {
	s := ingest.QuotaSeries{AuthIndex: a.EnsureIndex(), Provider: a.Provider, Window: "seven_day", AccountWide: true, PeriodSeconds: 7 * 24 * 3600}
	for back := 3; back >= 0; back-- {
		s.Points = append(s.Points, ingest.QuotaPoint{T: t0.Add(-time.Duration(back) * time.Hour), Utilization: u - float64(back)*perHour})
	}
	return s
}

type scene struct {
	auths    []*coreauth.Auth
	latest   []ingest.QuotaObservation
	history  []ingest.QuotaSeries
	window   RequestWindow
	recent   map[string]int64
	requests []access.AccessRequest
	backup   backupState
	alerts   alerts.DeliveryHealth
	ts       *tailscaledDown
}

func (s scene) eval() (Status, Attention, []Pool) {
	accounts := accountsFrom(s.auths, s.latest, t0)
	pools := buildPools(accounts, s.latest, s.history, t0)
	if s.recent == nil {
		s.recent = map[string]int64{}
	}
	st := verdict(pools, accounts, s.window, s.recent, t0)
	att := attention(attentionInputs{
		now: t0, accounts: accounts, pools: pools, recent: s.recent, requests: s.requests,
		backup: s.backup, alerts: s.alerts, tailscaled: s.ts,
		firstSeen: func(string) time.Time { return t0 },
	})
	return st, att, pools
}

func findItem(att Attention, kind string) (Item, bool) {
	for _, it := range att.Items {
		if it.Kind == kind {
			return it, true
		}
	}
	return Item{}, false
}

func TestVerdictFailureRateThresholds(t *testing.T) {
	cases := []struct {
		name             string
		requests, failed int64
		state, headline  string
	}{
		{"no traffic", 0, 0, verdictNormal, "Serving normally"},
		{"19 requests are too few to judge", 19, 19, verdictNormal, "Serving normally"},
		{"1 of 19 failed", 19, 1, verdictNormal, "Serving normally"},
		{"1 of 20 failed is 5%", 20, 1, verdictDegrade, "Degraded: 5% of requests failing in the last 15 min"},
		{"4.9% failed", 1000, 49, verdictNormal, "Serving normally"},
		{"5.0% failed", 1000, 50, verdictDegrade, "Degraded: 5% of requests failing in the last 15 min"},
		{"6% failed", 100, 6, verdictDegrade, "Degraded: 6% of requests failing in the last 15 min"},
		{"49.9% failed", 1000, 499, verdictDegrade, "Degraded: 49% of requests failing in the last 15 min"},
		{"50% failed", 20, 10, verdictDown, "Down: 50% of requests failing in the last 15 min"},
		{"all failed", 40, 40, verdictDown, "Down: 100% of requests failing in the last 15 min"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, _, _ := scene{
				auths:  []*coreauth.Auth{auth("claude", "alice@example.com")},
				window: RequestWindow{Requests: c.requests, Failed: c.failed},
			}.eval()
			if st.State != c.state || st.Headline != c.headline {
				t.Fatalf("state %q headline %q, want %q %q", st.State, st.Headline, c.state, c.headline)
			}
		})
	}
}

func TestVerdictUsableAccounts(t *testing.T) {
	claude := []*coreauth.Auth{auth("claude", "alice@example.com"), auth("claude", "bob@example.org")}
	cases := []struct {
		name            string
		auths           []*coreauth.Auth
		recent          map[string]int64
		state, headline string
	}{
		{"two usable", claude, map[string]int64{"claude": 30}, verdictNormal, "Serving normally"},
		{"down to one with traffic",
			[]*coreauth.Auth{claude[0], auth("claude", "bob@example.org", signedOut)}, map[string]int64{"claude": 30},
			verdictDegrade, "Degraded: Claude is down to one usable account"},
		{"down to one without traffic in the last hour",
			[]*coreauth.Auth{claude[0], auth("claude", "bob@example.org", signedOut)}, nil, verdictNormal, "Serving normally"},
		{"a provider with one account is not down to one",
			[]*coreauth.Auth{claude[0]}, map[string]int64{"claude": 30}, verdictNormal, "Serving normally"},
		{"one enabled and one disabled is not down to one",
			[]*coreauth.Auth{claude[0], auth("claude", "bob@example.org", disabled)}, map[string]int64{"claude": 30}, verdictNormal, "Serving normally"},
		{"none usable with traffic",
			[]*coreauth.Auth{auth("claude", "alice@example.com", signedOut), auth("claude", "bob@example.org", coolingFor(time.Hour)), auth("codex", "alice@example.com")},
			map[string]int64{"claude": 3, "codex": 1}, verdictDown, "Down: no account can serve Claude"},
		{"none usable without traffic",
			[]*coreauth.Auth{auth("claude", "alice@example.com", signedOut), auth("codex", "alice@example.com")},
			map[string]int64{"codex": 5}, verdictNormal, "Serving normally"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, _, _ := scene{auths: c.auths, recent: c.recent}.eval()
			if st.State != c.state || st.Headline != c.headline {
				t.Fatalf("state %q headline %q, want %q %q (reasons %+v)", st.State, st.Headline, c.state, c.headline, st.Reasons)
			}
		})
	}
}

func TestVerdictDetail(t *testing.T) {
	auths := []*coreauth.Auth{
		auth("claude", "alice@example.com"), auth("claude", "bob@example.org"), auth("claude", "carol@example.com"),
		auth("codex", "alice@example.com"), auth("codex", "dana@example.com", signedOut),
	}
	st, _, _ := scene{auths: auths, window: RequestWindow{Requests: 140}}.eval()
	if want := "No failures in the last 15 min · 4 of 5 accounts can serve (Claude 3, Codex 1)"; st.Detail != want {
		t.Errorf("detail = %q, want %q", st.Detail, want)
	}
	st, _, _ = scene{auths: auths, window: RequestWindow{Requests: 140, Failed: 2}}.eval()
	if want := "2 of 140 requests failed in the last 15 min · 4 of 5 accounts can serve (Claude 3, Codex 1)"; st.Detail != want {
		t.Errorf("detail = %q, want %q", st.Detail, want)
	}
	st, _, _ = scene{}.eval()
	if want := "No requests in the last 15 min · No accounts yet"; st.Detail != want || st.State != verdictNormal {
		t.Errorf("no accounts: %q %q", st.State, st.Detail)
	}
	if len(st.Providers) != 0 || st.Reasons == nil {
		t.Errorf("no accounts: providers %v, reasons %v", st.Providers, st.Reasons)
	}
}

func TestPoolsCountUsableAccountsOnly(t *testing.T) {
	alice := auth("claude", "alice@example.com")
	bob := auth("claude", "bob@example.org")
	reese := auth("claude", "reese@example.com", coolingFor(90*time.Minute))
	dana := auth("claude", "dana@example.com", signedOut)
	off := auth("claude", "off@example.com", disabled)
	_, _, pools := scene{
		auths: []*coreauth.Auth{alice, bob, reese, dana, off},
		latest: []ingest.QuotaObservation{
			weekly(alice, 0.78, 20*time.Hour), weekly(bob, 0.7, 50*time.Hour), weekly(reese, 0.59, 30*time.Hour),
			weekly(dana, 0.16, 40*time.Hour), weekly(off, 0.5, 60*time.Hour),
		},
		history: []ingest.QuotaSeries{burning(alice, 0.78, 0.05), burning(bob, 0.7, 0.02)},
	}.eval()
	if len(pools) != 1 {
		t.Fatalf("pools = %+v", pools)
	}
	p := pools[0]
	if p.UsableAccounts != 2 || p.EnabledAccounts != 4 || p.TotalAccounts != 5 || p.LeftNow != 0.52 {
		t.Errorf("counts: usable %d enabled %d total %d left %v; want 2, 4, 5, 0.52", p.UsableAccounts, p.EnabledAccounts, p.TotalAccounts, p.LeftNow)
	}
	if p.PercentLeft != 13 {
		t.Errorf("percent left = %v, want 13 (0.52 of 4 enabled accounts)", p.PercentLeft)
	}
	if p.LockedAccounts != (LockedQuota{Count: 2, Left: 1.34}) {
		t.Errorf("locked = %+v, want 2 accounts with 0.84 + 0.5", p.LockedAccounts)
	}
	if p.WaitingAccounts.Count != 1 || p.WaitingAccounts.Left != 0.41 || p.WaitingAccounts.Until == nil || !p.WaitingAccounts.Until.Equal(t0.Add(90*time.Minute)) {
		t.Errorf("waiting = %+v, want reese's 0.41 until +90 min", p.WaitingAccounts)
	}
	// Burn: 0.05 + 0.02 an hour over 4 enabled accounts.
	if p.BurnPctPerHour != 1.8 {
		t.Errorf("burn = %v%%/h, want 1.8 (0.07 accounts an hour of 4)", p.BurnPctPerHour)
	}
	// Usage stopped, the first reset that gives capacity back is alice's at
	// +20 h; the cooling account's reset counts too, the locked ones' do not.
	if p.NextReset == nil || !p.NextReset.At.Equal(t0.Add(20*time.Hour)) || p.NextReset.Gain != 0.78 {
		t.Errorf("next reset = %+v, want +20 h giving back 0.78", p.NextReset)
	}
	// alice empties at 0.22 / 0.05 = 4.4 h, bob at 0.3 / 0.02 = 15 h.
	if p.RunsOutAt == nil || !p.RunsOutAt.Equal(t0.Add(15*time.Hour)) || p.LastsPastReset {
		t.Errorf("runs out at %v (lasts past reset %t), want +15 h, before the reset", p.RunsOutAt, p.LastsPastReset)
	}
	want := []struct {
		label string
		pct   float64
		state State
	}{{"alice", 22, StateServing}, {"bob", 30, StateServing}, {"dana", 84, StateSignedOut}, {"reese", 41, StateCooling}, {"off", 50, StateDisabled}}
	for k, w := range want {
		if a := p.Accounts[k]; a.Label != w.label || a.PercentLeft != w.pct || a.State != w.state {
			t.Errorf("slot %d = %+v, want %+v", k, a, w)
		}
	}
}

func TestPoolLabelsShowTheEmailWhenNamesCollide(t *testing.T) {
	work := auth("claude", "alice@example.com")
	home := auth("claude", "Alice@example.org")
	bob := auth("claude", "bob@example.com")
	other := auth("codex", "alice@example.net")
	_, _, pools := scene{auths: []*coreauth.Auth{work, home, bob, other}}.eval()
	got := map[string][]string{}
	for _, p := range pools {
		for _, a := range p.Accounts {
			got[p.Provider] = append(got[p.Provider], a.Label)
		}
	}
	want := map[string][]string{"claude": {"alice@example.com", "Alice@example.org", "bob"}, "codex": {"alice"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
}

func TestAtLimitAccountIsCoolingUntilItsReset(t *testing.T) {
	a := auth("codex", "alice@example.com")
	b := auth("codex", "team@example.org")
	full := weekly(a, 1, 5*time.Hour)
	full.Status = "rejected"
	_, _, pools := scene{auths: []*coreauth.Auth{a, b}, latest: []ingest.QuotaObservation{full, weekly(b, 0.2, 9*time.Hour)}}.eval()
	slot := pools[0].Accounts[0]
	if slot.State != StateCooling || slot.Until == nil || !slot.Until.Equal(t0.Add(5*time.Hour)) {
		t.Fatalf("slot = %+v, want cooling until the window resets in 5 h", slot)
	}
	// Its reset is the first to give capacity back.
	if r := pools[0].NextReset; r == nil || !r.At.Equal(t0.Add(5*time.Hour)) {
		t.Errorf("next reset = %+v, want +5 h", r)
	}
	if pools[0].RunsOutAt != nil {
		t.Errorf("a pool without burn runs out at %v, want never", pools[0].RunsOutAt)
	}
}

func TestAttentionAccounts(t *testing.T) {
	dana := auth("codex", "dana@example.com", signedOut)
	carol := auth("claude", "carol@example.com", refreshFailing)
	_, att, _ := scene{
		auths:  []*coreauth.Auth{auth("codex", "alice@example.com"), dana, auth("claude", "alice@example.com"), carol},
		latest: []ingest.QuotaObservation{weekly(dana, 0.16, 40*time.Hour)},
	}.eval()
	it, ok := findItem(att, "account_signed_out")
	if !ok {
		t.Fatalf("no signed-out item in %+v", att.Items)
	}
	if it.Severity != SeverityBlocking || it.Title != "Codex: dana@example.com is signed out" || it.Action != (Action{Type: "sign_in", Target: "codex"}) ||
		it.Subject != (Subject{Type: "account", ID: dana.EnsureIndex()}) || !it.Since.Equal(t0.Add(-44*time.Minute)) {
		t.Errorf("signed-out item = %+v", it)
	}
	if want := "Refresh token rejected. Codex is serving from 1 of 2 accounts; 0.84 of its quota is locked until you sign in again."; it.Detail != want {
		t.Errorf("detail = %q, want %q", it.Detail, want)
	}
	it, ok = findItem(att, "account_refresh_failing")
	if !ok || it.Severity != SeverityAtRisk || it.Title != "Claude: carol@example.com can't refresh its login" || !strings.Contains(it.Detail, "keeps serving until its access token expires in about 24 h") {
		t.Errorf("refresh-failing item = %+v (found %t)", it, ok)
	}
	if att.Counts.Accounts != 2 || att.Counts.Total != 2 {
		t.Errorf("counts = %+v", att.Counts)
	}
}

// A disabled account needs the user only when its provider has nothing else
// that serves and clients still ask for it.
func TestAttentionDisabledAccount(t *testing.T) {
	off := auth("claude", "alice@example.com", disabled)
	cases := []struct {
		name   string
		auths  []*coreauth.Auth
		recent map[string]int64
		want   bool
	}{
		{"no usable account and traffic", []*coreauth.Auth{off, auth("claude", "bob@example.org", signedOut)}, map[string]int64{"claude": 4}, true},
		{"no usable account, no traffic", []*coreauth.Auth{off}, nil, false},
		{"another account serves", []*coreauth.Auth{off, auth("claude", "bob@example.org")}, map[string]int64{"claude": 4}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, att, _ := scene{auths: c.auths, recent: c.recent}.eval()
			it, ok := findItem(att, "account_disabled")
			if ok != c.want {
				t.Fatalf("disabled item found %t, want %t: %+v", ok, c.want, att.Items)
			}
			if ok && (it.Action != Action{Type: "enable", Target: off.EnsureIndex()} || it.Severity != SeverityBlocking) {
				t.Errorf("item = %+v", it)
			}
		})
	}
}

// Cooling accounts are not items of their own: they show up in the pool
// item when the rest of the pool cannot carry the load.
func TestAttentionCoolingOnlyWhenThePoolIsAtRisk(t *testing.T) {
	alice := auth("claude", "alice@example.com")
	bob := auth("claude", "bob@example.org")
	reese := auth("claude", "reese@example.com", coolingFor(5*time.Hour))
	t.Run("at risk", func(t *testing.T) {
		// 0.22 + 0.30 left of 3 accounts is 17%; alice burns 0.05 an hour
		// and bob 0.02, so the pool is empty at +15 h, before the reset at +20 h.
		_, att, _ := scene{
			auths:   []*coreauth.Auth{alice, bob, reese},
			latest:  []ingest.QuotaObservation{weekly(alice, 0.78, 20*time.Hour), weekly(bob, 0.7, 50*time.Hour), weekly(reese, 0.59, 30*time.Hour)},
			history: []ingest.QuotaSeries{burning(alice, 0.78, 0.05), burning(bob, 0.7, 0.02)},
		}.eval()
		it, ok := findItem(att, "pool_low")
		if !ok || len(att.Items) != 1 {
			t.Fatalf("items = %+v, want only the pool item", att.Items)
		}
		if it.Severity != SeverityAtRisk || it.Title != "Claude pool may run out before its next reset" || it.Action != (Action{Type: "open_accounts", Target: "claude"}) {
			t.Errorf("pool item = %+v", it)
		}
		want := "0.52 left on 2 usable accounts (17% of the pool), burning 2% an hour: empty in about 15 h, next reset in about 20 h. reese@example.com is cooling down, back in about 5 h."
		if it.Detail != want {
			t.Errorf("detail = %q\nwant      %q", it.Detail, want)
		}
	})
	t.Run("not at risk", func(t *testing.T) {
		_, att, _ := scene{
			auths:   []*coreauth.Auth{alice, bob, reese},
			latest:  []ingest.QuotaObservation{weekly(alice, 0.2, 20*time.Hour), weekly(bob, 0.1, 50*time.Hour), weekly(reese, 0.59, 30*time.Hour)},
			history: []ingest.QuotaSeries{burning(alice, 0.2, 0.01)},
		}.eval()
		if len(att.Items) != 0 {
			t.Fatalf("items = %+v, want none: the pool lasts", att.Items)
		}
	})
	t.Run("nothing else serves", func(t *testing.T) {
		_, att, _ := scene{auths: []*coreauth.Auth{reese}, latest: []ingest.QuotaObservation{weekly(reese, 0.59, 30*time.Hour)}}.eval()
		it, ok := findItem(att, "pool_low")
		if !ok || it.Severity != SeverityBlocking || it.Title != "Claude has no usable account" || it.Detail != "reese@example.com is cooling down, back in about 5 h." {
			t.Errorf("pool item = %+v (found %t)", it, ok)
		}
	})
}

func TestAttentionPoolUnderFifteenPercent(t *testing.T) {
	a := auth("codex", "alice@example.com")
	b := auth("codex", "team@example.org")
	for _, c := range []struct {
		left float64
		want bool
	}{{0.15, false}, {0.14, true}} {
		// Two accounts, each with c.left left, no burn: the percentage alone decides.
		_, att, _ := scene{auths: []*coreauth.Auth{a, b}, latest: []ingest.QuotaObservation{weekly(a, 1-c.left, 9*time.Hour), weekly(b, 1-c.left, 9*time.Hour)}}.eval()
		it, ok := findItem(att, "pool_low")
		if ok != c.want {
			t.Errorf("%.0f%% left: item found %t, want %t", c.left*100, ok, c.want)
		}
		if ok && it.Title != "Codex pool is under 15%" {
			t.Errorf("title = %q", it.Title)
		}
	}
}

func TestAttentionAccessRequest(t *testing.T) {
	_, att, _ := scene{requests: []access.AccessRequest{{
		NodeID: "nDANA", Login: "dana@github", Node: "danas-ipad", NameHint: "Dana's iPad", Client: "Claude Code",
		FirstSeen: t0.Add(-6 * time.Minute), LastSeen: t0, Asked: access.AskProxy,
	}}}.eval()
	it, ok := findItem(att, "access_request")
	want := Item{
		ID: "access:nDANA", Kind: "access_request", Severity: SeverityWaiting,
		Title: "Dana's iPad asks for Proxy access", Detail: "dana@github · danas-ipad · Claude Code",
		Since: t0.Add(-6 * time.Minute), Subject: Subject{Type: "device", ID: "nDANA"}, Action: Action{Type: "review_access", Target: "nDANA"},
	}
	if !ok || it != want {
		t.Fatalf("item = %+v\nwant   %+v", it, want)
	}
	if att.Counts.Access != 1 {
		t.Errorf("counts = %+v", att.Counts)
	}
}

func TestAttentionBackups(t *testing.T) {
	failedRun := backup.Run{At: t0.Add(-10 * time.Hour), Error: "write backup: no space left on device"}
	cases := []struct {
		name  string
		state backupState
		title string
	}{
		{"25 h old", backupState{scheduled: true, last: t0.Add(-25 * time.Hour)}, ""},
		{"27 h old", backupState{scheduled: true, last: t0.Add(-27 * time.Hour)}, "Last backup was 27 h ago"},
		{"27 h old with the schedule off", backupState{last: t0.Add(-27 * time.Hour)}, ""},
		{"none yet, started an hour ago", backupState{scheduled: true, watchingSince: t0.Add(-time.Hour)}, ""},
		{"none yet, started 27 h ago", backupState{scheduled: true, watchingSince: t0.Add(-27 * time.Hour)}, "No backup yet"},
		{"the last scheduled run failed", backupState{scheduled: true, last: t0.Add(-20 * time.Hour), run: failedRun, hasRun: true}, "The last scheduled backup failed"},
		{"a backup after the failed run settles it", backupState{scheduled: true, last: t0.Add(-time.Hour), run: failedRun, hasRun: true}, ""},
		{"the last run succeeded", backupState{scheduled: true, last: t0.Add(-10 * time.Hour), run: backup.Run{At: t0.Add(-10 * time.Hour)}, hasRun: true}, ""},
		{"stale and failed", backupState{scheduled: true, last: t0.Add(-31 * time.Hour), run: failedRun, hasRun: true}, "Last backup was 31 h ago"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, att, _ := scene{backup: c.state}.eval()
			it, ok := findItem(att, "backup_overdue")
			if ok != (c.title != "") || it.Title != c.title {
				t.Fatalf("item = %+v (found %t), want title %q", it, ok, c.title)
			}
			if !ok {
				return
			}
			if it.Severity != SeverityOverdue || it.Action.Type != "backup_now" || att.Counts.Backups != 1 {
				t.Errorf("item = %+v, counts %+v", it, att.Counts)
			}
			if c.state.hasRun && c.state.run.Error != "" && it.Detail != "The scheduled run 10 h ago failed: write backup: no space left on device." {
				t.Errorf("detail = %q", it.Detail)
			}
		})
	}
}

func TestAttentionServerHealth(t *testing.T) {
	_, att, _ := scene{
		alerts: alerts.DeliveryHealth{Active: true, Failures: 3, FailingSince: t0.Add(-time.Hour), LastError: "discord answered 401"},
		ts:     &tailscaledDown{socket: "/var/run/tailscale/tailscaled.sock", err: "connect: no such file or directory", since: t0.Add(-5 * time.Minute), stale: true},
	}.eval()
	it, ok := findItem(att, "alerts_failing")
	if !ok || it.Severity != SeverityAtRisk || it.Action != (Action{Type: "open_settings", Target: "alerts"}) || it.Detail != "The last 3 deliveries failed (discord answered 401). Check the webhook in Settings." {
		t.Errorf("alerts item = %+v (found %t)", it, ok)
	}
	it, ok = findItem(att, "tailscaled_down")
	if !ok || it.Severity != SeverityBlocking || it.Action.Type != "docs" || !strings.Contains(it.Action.Target, "#every-tailnet-device-is-refused") || !strings.Contains(it.Detail, "restart taproot") {
		t.Errorf("tailscaled item = %+v (found %t)", it, ok)
	}
	if att.Items[0].Kind != "tailscaled_down" {
		t.Errorf("first item = %s, want the blocking tailscaled item", att.Items[0].Kind)
	}
	if got := deliveryDetail(alerts.DeliveryHealth{Failures: 1, LastError: "discord answered 500"}); got != "The last delivery failed (discord answered 500). Check the webhook in Settings." {
		t.Errorf("one failure: %q", got)
	}
	_, att, _ = scene{alerts: alerts.DeliveryHealth{Failures: 3, LastError: "x"}}.eval()
	if len(att.Items) != 0 {
		t.Errorf("failures while alerts are off: %+v, want no item", att.Items)
	}
}

// Items sort by severity, then oldest first.
func TestAttentionOrder(t *testing.T) {
	_, att, _ := scene{
		auths: []*coreauth.Auth{auth("codex", "alice@example.com"), auth("codex", "dana@example.com", signedOut)},
		requests: []access.AccessRequest{
			{NodeID: "nNEW", Node: "new", FirstSeen: t0.Add(-time.Minute)},
			{NodeID: "nOLD", Node: "old", FirstSeen: t0.Add(-time.Hour)},
		},
		backup: backupState{scheduled: true, last: t0.Add(-30 * time.Hour)},
		alerts: alerts.DeliveryHealth{Active: true, Failures: 1, FailingSince: t0.Add(-2 * time.Hour), LastError: "discord answered 500"},
	}.eval()
	var got []string
	for _, it := range att.Items {
		got = append(got, it.ID)
	}
	want := "account:" + auth("codex", "dana@example.com").EnsureIndex() + ":signed_out alerts access:nOLD access:nNEW backup"
	if strings.Join(got, " ") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
	if att.Counts != (Counts{Accounts: 1, Access: 2, Backups: 1, Total: 5}) {
		t.Errorf("counts = %+v", att.Counts)
	}
}

// A cooldown on one model leaves the account serving the others, so it is
// not cooling; a credential-wide one is.
func TestModelCooldownIsNotCooling(t *testing.T) {
	model := auth("claude", "alice@example.com", func(a *coreauth.Auth) {
		a.ModelStates = map[string]*coreauth.ModelState{"claude-opus-5-5": {
			Unavailable: true, NextRetryAfter: t0.Add(time.Hour),
			Quota: coreauth.QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: t0.Add(time.Hour)},
		}}
	})
	whole := auth("claude", "bob@example.org", coolingFor(time.Hour))
	accounts := accountsFrom([]*coreauth.Auth{model, whole}, nil, t0)
	if accounts[0].State != StateServing {
		t.Errorf("a model cooldown: %s, want serving", accounts[0].State)
	}
	if accounts[1].State != StateCooling || !accounts[1].Until.Equal(t0.Add(time.Hour)) {
		t.Errorf("a credential cooldown: %s until %s, want cooling for an hour", accounts[1].State, accounts[1].Until)
	}
}
