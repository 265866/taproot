package alerts

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/ingest"
)

var t0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func obs(idx, provider, window string, util float64, resetsIn time.Duration, status string) ingest.QuotaObservation {
	o := ingest.QuotaObservation{
		AuthIndex: idx, Provider: provider, Window: window, Utilization: util, Status: status,
		ObservedAt: t0, AccountWide: window == "five_hour" || window == "seven_day" || window == "codex_primary" || window == "codex_secondary",
	}
	if resetsIn != 0 {
		o.ResetsAt = t0.Add(resetsIn)
	}
	return o
}

func claude(idx string) Account {
	return Account{AuthIndex: idx, Provider: "claude", Label: "acct-" + idx}
}

func keys(m map[string]Condition) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestConditions(t *testing.T) {
	codex := Account{AuthIndex: "x1", Provider: "codex", Label: "codex-one"}
	broken := claude("b")
	broken.LoginBroken, broken.LoginReason = true, "refresh token invalid"
	tests := []struct {
		name string
		snap Snapshot
		want []string
	}{
		{
			name: "below the limit is quiet",
			snap: Snapshot{Now: t0, Accounts: []Account{claude("a")}, Quota: []ingest.QuotaObservation{obs("a", "claude", "five_hour", 0.99, time.Hour, "allowed")}},
		},
		{
			name: "utilization at 1 is a limit; the only account also empties the pool",
			snap: Snapshot{Now: t0, Accounts: []Account{claude("a")}, Quota: []ingest.QuotaObservation{obs("a", "claude", "five_hour", 1, time.Hour, "allowed")}},
			want: []string{"limit:a:five_hour", "pool:claude"},
		},
		{
			name: "rejected status is a limit below 100%",
			snap: Snapshot{Now: t0, Accounts: []Account{claude("a"), claude("b")}, Quota: []ingest.QuotaObservation{obs("a", "claude", "seven_day", 0.97, time.Hour, "rejected")}},
			want: []string{"limit:a:seven_day"},
		},
		{
			name: "a window whose reset passed is usable before the next observation",
			snap: Snapshot{Now: t0, Accounts: []Account{claude("a")}, Quota: []ingest.QuotaObservation{obs("a", "claude", "five_hour", 1, -time.Minute, "rejected")}},
		},
		{
			name: "a model-scoped limit does not empty the pool",
			snap: Snapshot{Now: t0, Accounts: []Account{claude("a")}, Quota: []ingest.QuotaObservation{obs("a", "claude", "seven_day_opus", 1, time.Hour, "rejected")}},
			want: []string{"limit:a:seven_day_opus"},
		},
		{
			name: "observations of removed or disabled accounts are ignored",
			snap: Snapshot{Now: t0, Accounts: []Account{claude("a")}, Quota: []ingest.QuotaObservation{obs("gone", "claude", "five_hour", 1, time.Hour, "rejected")}},
		},
		{
			name: "pool is per provider",
			snap: Snapshot{Now: t0, Accounts: []Account{claude("a"), codex}, Quota: []ingest.QuotaObservation{
				obs("x1", "codex", "codex_secondary", 1, time.Hour, "rejected"),
			}},
			want: []string{"limit:x1:codex_secondary", "pool:codex"},
		},
		{
			name: "a broken login is reported and counts toward an empty pool",
			snap: Snapshot{Now: t0, Accounts: []Account{claude("a"), broken}, Quota: []ingest.QuotaObservation{obs("a", "claude", "five_hour", 1, time.Hour, "rejected")}},
			want: []string{"limit:a:five_hour", "login:b", "pool:claude"},
		},
		{
			name: "grant with resets left expiring within a day",
			snap: Snapshot{Now: t0, Accounts: []Account{claude("a")}, Grants: map[string][]Grant{"a": {
				{ID: "soon", ResetsLeft: 2, EndsAt: t0.Add(23 * time.Hour)},
				{ID: "later", ResetsLeft: 2, EndsAt: t0.Add(25 * time.Hour)},
				{ID: "spent", ResetsLeft: 0, EndsAt: t0.Add(time.Hour)},
				{ID: "expired", ResetsLeft: 1, EndsAt: t0.Add(-time.Minute)},
				{ID: "open", ResetsLeft: 1},
			}}},
			want: []string{"grant:a:soon"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := keys(Conditions(tc.snap)); !slices.Equal(got, tc.want) {
				t.Fatalf("conditions = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPoolEmptyNamesTheFirstAccountBack(t *testing.T) {
	snap := Snapshot{Now: t0, Accounts: []Account{claude("a"), claude("b")}, Quota: []ingest.QuotaObservation{
		obs("a", "claude", "five_hour", 1, 3*time.Hour, "rejected"),
		obs("b", "claude", "five_hour", 1, 2*time.Hour, "rejected"),
		// b is also at its weekly limit, which resets later, so b is back only then.
		obs("b", "claude", "seven_day", 1, 4*time.Hour, "rejected"),
	}}
	c := Conditions(snap)["pool:claude"]
	if c.Alert.Severity != SeverityCritical || !strings.Contains(c.Alert.Description, "**acct-a**") {
		t.Fatalf("pool alert = %+v", c.Alert)
	}
	want := t0.Add(3 * time.Hour).Unix()
	if !strings.Contains(c.Alert.Description, "<t:"+itoa(want)+":f>") {
		t.Fatalf("pool alert should name a's reset %d: %s", want, c.Alert.Description)
	}
}

func TestLimitMessage(t *testing.T) {
	a := claude("a")
	c := Conditions(Snapshot{Now: t0, Accounts: []Account{a, claude("b")}, Quota: []ingest.QuotaObservation{obs("a", "claude", "five_hour", 1, 90*time.Minute, "rejected")}})["limit:a:five_hour"]
	if c.Alert.Title != "Claude account hit its 5-hour limit" {
		t.Fatalf("title = %q", c.Alert.Title)
	}
	fields := map[string]string{}
	for _, f := range c.Alert.Fields {
		fields[f.Name] = f.Value
	}
	reset := "<t:" + itoa(t0.Add(90*time.Minute).Unix()) + ":f>"
	if fields["Account"] != "acct-a" || fields["Provider"] != "Claude" || fields["Window"] != "5-hour" || !strings.HasPrefix(fields["Resets"], reset) {
		t.Fatalf("fields = %v", fields)
	}
	if c.Recovery == nil || c.Recovery.Severity != SeverityRecovered {
		t.Fatalf("limit needs a recovery message: %+v", c.Recovery)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

type step struct {
	at     time.Duration
	active map[string]Condition
	want   []string // "alert:<key>" or "recovered:<key>"
}

func cond(key string, recover bool, delay time.Duration) map[string]Condition {
	c := Condition{Alert: Message{Key: key, Severity: SeverityWarning, Title: "alert"}, Delay: delay}
	if recover {
		c.Recovery = &Message{Key: key, Severity: SeverityRecovered, Title: "ok"}
	}
	return map[string]Condition{key: c}
}

func TestDetectorStep(t *testing.T) {
	const cd, st = 30 * time.Minute, 2 * time.Minute
	limit := func() map[string]Condition { return cond("limit:a:five_hour", true, 0) }
	none := map[string]Condition{}
	tests := []struct {
		name  string
		steps []step
	}{
		{"limit hit then recovered after settling", []step{
			{0, limit(), []string{"alert:limit:a:five_hour"}},
			{time.Minute, limit(), nil},
			{time.Hour, none, nil},
			{time.Hour + time.Minute, none, nil},
			{time.Hour + st, none, []string{"recovered:limit:a:five_hour"}},
			{time.Hour + 3*st, none, nil},
		}},
		{"a flap shorter than settle continues the episode", []step{
			{0, limit(), []string{"alert:limit:a:five_hour"}},
			{time.Minute, none, nil},
			{time.Minute + 30*time.Second, limit(), nil},
			{10 * time.Minute, none, nil},
			{10*time.Minute + st, none, []string{"recovered:limit:a:five_hour"}},
		}},
		{"a new episode inside the cooldown waits for it", []step{
			{0, limit(), []string{"alert:limit:a:five_hour"}},
			{time.Minute, none, nil},
			{time.Minute + st, none, []string{"recovered:limit:a:five_hour"}},
			{5 * time.Minute, limit(), nil},
			{20 * time.Minute, limit(), nil},
			{cd, limit(), []string{"alert:limit:a:five_hour"}},
		}},
		{"an episode that ends inside the cooldown after a recovery stays silent", []step{
			{0, limit(), []string{"alert:limit:a:five_hour"}},
			{time.Minute, none, nil},
			{time.Minute + st, none, []string{"recovered:limit:a:five_hour"}},
			{5 * time.Minute, limit(), nil},
			{6 * time.Minute, none, nil},
			{6*time.Minute + st, none, nil},
			{cd + time.Minute, none, nil},
		}},
		{"login broken waits out its delay, then recovers", []step{
			{0, cond("login:b", true, loginDelay), nil},
			{time.Minute, cond("login:b", true, loginDelay), nil},
			{loginDelay, cond("login:b", true, loginDelay), []string{"alert:login:b"}},
			{time.Hour, none, nil},
			{time.Hour + st, none, []string{"recovered:login:b"}},
		}},
		{"a brief login failure never alerts", []step{
			{0, cond("login:b", true, loginDelay), nil},
			{time.Minute, none, nil},
			{time.Hour, cond("login:b", true, loginDelay), nil},
			{time.Hour + loginDelay, cond("login:b", true, loginDelay), []string{"alert:login:b"}},
		}},
		{"grant alerts once and clears silently", []step{
			{0, cond("grant:a:g", false, 0), []string{"alert:grant:a:g"}},
			{3 * time.Hour, cond("grant:a:g", false, 0), nil},
			{24 * time.Hour, none, nil},
			{25 * time.Hour, none, nil},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDetector(cd, st)
			for _, s := range tc.steps {
				msgs, _ := d.Step(t0.Add(s.at), s.active)
				var got []string
				for _, m := range msgs {
					kind := "alert:"
					if m.Severity == SeverityRecovered {
						kind = "recovered:"
					}
					got = append(got, kind+m.Key)
					if !m.At.Equal(t0.Add(s.at)) {
						t.Fatalf("at %v: message time %v", s.at, m.At)
					}
				}
				if !slices.Equal(got, s.want) {
					t.Fatalf("at %v: got %v, want %v", s.at, got, s.want)
				}
			}
		})
	}
}

func TestDetectorForgetsQuietKeys(t *testing.T) {
	d := NewDetector(30*time.Minute, 2*time.Minute)
	d.Step(t0, cond("k", true, 0))
	d.Step(t0.Add(time.Minute), nil)
	d.Step(t0.Add(3*time.Minute), nil)
	if _, changed := d.Step(t0.Add(31*time.Minute), nil); !changed || len(d.tracks) != 0 {
		t.Fatalf("tracks = %v, want none after the cooldown", d.tracks)
	}
	if _, changed := d.Step(t0.Add(32*time.Minute), nil); changed {
		t.Fatal("an idle step should not report a change")
	}
}
