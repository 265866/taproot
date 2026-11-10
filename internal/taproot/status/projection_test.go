package status

import (
	"math"
	"testing"
	"time"
)

// These cases are web/tests/taprootQuotaProjection.test.ts's, with the same
// inputs and expected values, so the server's pools and the Accounts page
// stay one model.

var (
	nowMs = msOf(time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	hr    = hourMs
	week  = 168 * hourMs
	five  = 5 * hourMs
)

var accountWideNames = map[string]bool{"five_hour": true, "seven_day": true, "codex_primary": true, "codex_secondary": true}

func obsAt(window string, u float64, resetsAt float64, hasReset bool, period, observedAt float64) windowObs {
	return windowObs{window: window, accountWide: accountWideNames[window], utilization: u, resetsAt: resetsAt, hasReset: hasReset, periodMs: period, observedAt: observedAt}
}

func obs(window string, u, resetsAt, period float64) windowObs {
	return obsAt(window, u, resetsAt, true, period, nowMs)
}

// ramp rises by perHour each hour over the last hours hours, ending at end now.
func ramp(window string, end, perHour float64, hours int) windowSeries {
	s := windowSeries{window: window, accountWide: accountWideNames[window]}
	for i := 0; i <= hours; i++ {
		back := float64(hours - i)
		s.points = append(s.points, seriesPoint{nowMs - back*hr, end - back*perHour})
	}
	return s
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func expectPoints(t *testing.T, got []point, want [][2]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("points = %v, want %v", got, want)
	}
	for i, p := range got {
		if !near(p.t, want[i][0]) || !near(p.v, want[i][1]) {
			t.Fatalf("point %d = %v, want %v (all: %v)", i, p, want[i], got)
		}
	}
}

func TestRollForward(t *testing.T) {
	if u, next, ok := rollForward(obs("w", 0.4, nowMs+hr, week), nowMs); u != 0.4 || !ok || next != nowMs+hr {
		t.Errorf("before the reset: %v %v %v", u, next, ok)
	}
	if u, next, ok := rollForward(obs("w", 0.9, nowMs-15*24*hr, week), nowMs); u != 0 || !ok || next != nowMs+6*24*hr {
		t.Errorf("after the reset: %v %v %v, want 0 and +6d", u, next, ok)
	}
	if u, _, ok := rollForward(obs("w", 0.9, nowMs-hr, 0), nowMs); u != 0 || ok {
		t.Errorf("no period: %v %v, want 0 and no next reset", u, ok)
	}
}

func TestActiveWindows(t *testing.T) {
	fiveHour := obsAt("five_hour", 0.64, nowMs-31*hr, true, five, nowMs-31.5*hr)
	weekly := obsAt("seven_day", 0.02, nowMs+7*24*hr, true, week, nowMs-0.1*hr)
	if got := activeWindows([]windowObs{fiveHour, weekly}, nowMs); len(got) != 1 || got[0].window != "seven_day" {
		t.Errorf("a window that stopped reporting: %v", got)
	}
	primary := obsAt("codex_primary", 0.47, nowMs-30*24*hr, true, week, nowMs-34*24*hr)
	if got := activeWindows([]windowObs{primary}, nowMs); len(got) != 1 {
		t.Errorf("an idle account's window was dropped")
	}
	lagging := obsAt("five_hour", 0.3, nowMs+hr, true, five, nowMs-8*hr)
	if got := activeWindows([]windowObs{lagging, obs("seven_day", 0.1, nowMs+24*hr, week)}, nowMs); len(got) != 2 {
		t.Errorf("a lagging window with its reset ahead was dropped")
	}
}

func TestBurnRate(t *testing.T) {
	s := ramp("seven_day", 0.2, 0.02, 3)
	if got := burnRate(&s, nowMs, 3*hr); !near(got, 0.02) {
		t.Errorf("constant burn = %v, want 0.02", got)
	}
	reset := windowSeries{window: "five_hour", points: []seriesPoint{{nowMs - 3*hr, 0.9}, {nowMs - 2*hr, 0.95}, {nowMs - hr, 0.05}, {nowMs, 0.1}}, resets: []float64{nowMs - 1.5*hr}}
	if got := burnRate(&reset, nowMs, 3*hr); !near(got, 0.05) {
		t.Errorf("with a known reset = %v, want 0.05", got)
	}
	reset.resets = nil
	if got := burnRate(&reset, nowMs, 3*hr); !near(got, 0.1/3) {
		t.Errorf("without the reset = %v, want 0.1/3", got)
	}
	if got := burnRate(nil, nowMs, 3*hr); got != 0 {
		t.Errorf("no history = %v", got)
	}
	short := windowSeries{window: "w", points: []seriesPoint{{nowMs - hr, 0.1}, {nowMs, 0.2}}}
	if got := burnRate(&short, nowMs, 6*hr); !near(got, 0.1) {
		t.Errorf("short history = %v, want 0.1", got)
	}
}

func TestProjectAccount(t *testing.T) {
	steady := accountInput{authIndex: "a", windows: []windowObs{obs("seven_day", 0.2, nowMs+100*hr, week)}, series: []windowSeries{ramp("seven_day", 0.2, 0.02, 3)}}
	p := projectAccount(steady, nowMs, nowMs+120*hr, 3*hr, 1)
	if !near(p.remainingNow, 0.8) || p.emptyAt != nowMs+40*hr {
		t.Errorf("steady: remaining %v, empty %v", p.remainingNow, p.emptyAt-nowMs)
	}
	expectPoints(t, p.points, [][2]float64{{nowMs, 0.8}, {nowMs + 40*hr, 0}, {nowMs + 100*hr, 0}, {nowMs + 100*hr, 1}, {nowMs + 120*hr, 0.6}})

	full := accountInput{authIndex: "c", windows: []windowObs{obs("seven_day", 1, nowMs+10*hr, week)}, series: []windowSeries{ramp("seven_day", 1, 0.02, 3)}}
	p = projectAccount(full, nowMs, nowMs+20*hr, 3*hr, 1)
	if p.emptyAt != nowMs {
		t.Errorf("full account empty at %v, want now", p.emptyAt-nowMs)
	}
	expectPoints(t, p.points, [][2]float64{{nowMs, 0}, {nowMs + 10*hr, 0}, {nowMs + 10*hr, 1}, {nowMs + 20*hr, 0.8}})

	both := accountInput{authIndex: "d",
		windows: []windowObs{obs("five_hour", 0.5, nowMs+2*hr, five), obs("seven_day", 0.3, nowMs+50*hr, week)},
		series:  []windowSeries{ramp("five_hour", 0.5, 0.1, 3), ramp("seven_day", 0.3, 0.01, 3)}}
	crossing := nowMs + 2*hr + (32.0/9)*hr
	expectPoints(t, projectAccount(both, nowMs, nowMs+8*hr, 3*hr, 1).points, [][2]float64{
		{nowMs, 0.5}, {nowMs + 2*hr, 0.3}, {nowMs + 2*hr, 0.68}, {crossing, 0.68 - 0.32/9}, {nowMs + 7*hr, 0.5}, {nowMs + 7*hr, 0.63}, {nowMs + 8*hr, 0.62},
	})

	blocked := accountInput{authIndex: "e",
		windows: []windowObs{obs("five_hour", 0.9, nowMs+4*hr, five), obs("seven_day", 0.1, nowMs+100*hr, week)},
		series:  []windowSeries{ramp("five_hour", 0.9, 0.1, 3), ramp("seven_day", 0.1, 0.01, 3)}}
	p = projectAccount(blocked, nowMs, nowMs+5*hr, 3*hr, 1)
	if p.emptyAt != nowMs+hr {
		t.Errorf("blocked: empty at %v, want +1h", p.emptyAt-nowMs)
	}
	expectPoints(t, p.points, [][2]float64{{nowMs, 0.1}, {nowMs + hr, 0}, {nowMs + 4*hr, 0}, {nowMs + 4*hr, 0.89}, {nowMs + 5*hr, 0.88}})

	codex := accountInput{authIndex: "c",
		windows: []windowObs{
			obs("codex_primary", 0.2, nowMs+3*hr, five), obs("codex_secondary", 0.1, nowMs+100*hr, week),
			obs("codex_gpt_5_3_codex_spark_primary", 1, nowMs+2*hr, five),
		},
		series: []windowSeries{ramp("codex_gpt_5_3_codex_spark_primary", 1, 0.3, 3)}}
	p = projectAccount(codex, nowMs, nowMs+10*hr, 3*hr, 1)
	if !near(p.remainingNow, 0.8) || !math.IsNaN(p.emptyAt) || len(p.resets) != 1 || p.resets[0].t != nowMs+3*hr || !near(p.resets[0].gain, 0.1) {
		t.Errorf("an exhausted model limit: remaining %v, empty %v, resets %v", p.remainingNow, p.emptyAt, p.resets)
	}
}

func TestProjectPool(t *testing.T) {
	withBoth := accountInput{authIndex: "a", windows: []windowObs{obs("five_hour", 0.5, nowMs+2*hr, five), obs("seven_day", 0.3, nowMs+50*hr, week)}}
	weeklyOnly := accountInput{authIndex: "b", windows: []windowObs{obsAt("five_hour", 0.64, nowMs-31*hr, true, five, nowMs-31.5*hr), obs("seven_day", 0.4, nowMs+30*hr, week)}}
	pool := projectPool([]accountInput{withBoth, weeklyOnly}, nowMs, nowMs+40*hr, 3*hr, 1)
	if pool.capacity != 2 || !near(pool.remainingNow, 1.1) || !math.IsNaN(pool.emptyAt) {
		t.Fatalf("pool: %+v", pool)
	}
	if len(pool.resets) != 2 || pool.resets[0].t != nowMs+2*hr || !near(pool.resets[0].gain, 0.2) || pool.resets[1].t != nowMs+30*hr || !near(pool.resets[1].gain, 0.4) {
		t.Errorf("resets = %v, want +2h 0.2 and +30h 0.4", pool.resets)
	}
	if pool.nextReset == nil || pool.nextReset.t != nowMs+2*hr {
		t.Errorf("next reset = %v, want +2h", pool.nextReset)
	}

	a := accountInput{authIndex: "a", windows: []windowObs{obs("seven_day", 0.8, nowMs+500*hr, week)}, series: []windowSeries{ramp("seven_day", 0.8, 0.02, 3)}}
	b := accountInput{authIndex: "b", windows: []windowObs{obs("seven_day", 0.6, nowMs+500*hr, week)}, series: []windowSeries{ramp("seven_day", 0.6, 0.02, 3)}}
	if pool := projectPool([]accountInput{a, b}, nowMs, nowMs+48*hr, 3*hr, 1); pool.emptyAt != nowMs+20*hr {
		t.Errorf("pool empties at %v, want +20h", pool.emptyAt-nowMs)
	}
	if pool := projectPool(nil, nowMs, nowMs+10*hr, 3*hr, 1); pool.capacity != 0 || !math.IsNaN(pool.emptyAt) || pool.nextReset != nil {
		t.Errorf("an empty pool: %+v", pool)
	}
	if pool := projectPool([]accountInput{{authIndex: "x"}}, nowMs, nowMs+hr, 3*hr, 1); pool.remainingNow != 1 {
		t.Errorf("an account with no windows has %v left, want 1 (full)", pool.remainingNow)
	}
}

func TestRecordedAccountAndDrain(t *testing.T) {
	s := windowSeries{window: "seven_day", accountWide: true, points: []seriesPoint{{nowMs - 10*hr, 0.5}, {nowMs - 6*hr, 0.7}, {nowMs - 2*hr, 0.1}}, resets: []float64{nowMs - 4*hr}}
	expectPoints(t, recordedAccount([]windowSeries{s}, nowMs-8*hr, nowMs), [][2]float64{
		{nowMs - 8*hr, 0.5}, {nowMs - 6*hr, 0.5}, {nowMs - 6*hr, 0.3}, {nowMs - 4*hr, 0.3}, {nowMs - 4*hr, 1}, {nowMs - 2*hr, 1}, {nowMs - 2*hr, 0.9}, {nowMs, 0.9},
	})
	if got := recordedAccount([]windowSeries{s}, nowMs-20*hr, nowMs-11*hr); len(got) != 0 {
		t.Errorf("no observation before the end: %v", got)
	}
	line := []point{{0, 1}, {10 * hr, 0}, {10 * hr, 1}, {12 * hr, 1}, {12 * hr, 0.5}}
	if got := decreaseBetween(line, 2*hr, 5*hr); !near(got, 0.3) {
		t.Errorf("linear decrease = %v, want 0.3", got)
	}
	if got := decreaseBetween(line, 11*hr, 13*hr); !near(got, 0.5) {
		t.Errorf("a jump down = %v, want 0.5", got)
	}
	if got := drainAt(line, 5*hr, 3*hr); !near(got, 0.1) {
		t.Errorf("drain = %v, want 0.1 an hour", got)
	}
	expectPoints(t, sumPolylines([][]point{{{0, 1}, {2, 1}, {2, 0}, {4, 0}}, {{0, 0.5}, {4, 0.5}}}), [][2]float64{{0, 1.5}, {2, 1.5}, {2, 0.5}, {4, 0.5}})
}
