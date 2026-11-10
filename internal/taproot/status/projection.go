package status

import (
	"math"
	"slices"
	"sort"
)

// This file ports the dashboard's quota model
// (web/src/features/taproot-quota/model/projection.ts) so the server's pools
// match the Accounts page. Times are epoch milliseconds as float64, because
// the projection moves to instants a window empties at, which fall between
// milliseconds; utilization and remaining are fractions of one account.

const (
	hourMs  = 3_600_000.0
	epsilon = 1e-9
	// maxSimSteps bounds one account's projection, as the web's guard does.
	maxSimSteps = 100_000
)

// windowObs is the latest observation of one window.
type windowObs struct {
	window      string
	accountWide bool
	utilization float64
	resetsAt    float64
	hasReset    bool
	observedAt  float64
	periodMs    float64
}

// windowSeries is one window's recorded observations, oldest first, and its
// known reset instants, ascending.
type windowSeries struct {
	window      string
	accountWide bool
	points      []seriesPoint
	resets      []float64
}

type seriesPoint struct{ t, u float64 }

// accountInput is what the projection reads of one account.
type accountInput struct {
	authIndex string
	windows   []windowObs
	series    []windowSeries
}

// point is a polyline vertex; two points with the same t are a jump.
type point struct{ t, v float64 }

// resetEvent is an increase in remaining at a reset.
type resetEvent struct{ t, gain float64 }

type accountProjection struct {
	authIndex    string
	remainingNow float64
	// emptyAt is the first instant the account has nothing left, or NaN.
	emptyAt float64
	points  []point
	resets  []resetEvent
}

type poolProjection struct {
	accounts     []accountProjection
	points       []point
	resets       []resetEvent
	capacity     int
	remainingNow float64
	// emptyAt is when the pool first has nothing left, or NaN.
	emptyAt   float64
	nextReset *resetEvent
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Min(1, math.Max(0, v))
}

// rollForward returns the window's utilization at t, rolled through the
// resets since it was observed, and its next reset.
func rollForward(w windowObs, t float64) (u, next float64, hasNext bool) {
	if !w.hasReset {
		return clamp01(w.utilization), 0, false
	}
	if t < w.resetsAt {
		return clamp01(w.utilization), w.resetsAt, true
	}
	if w.periodMs <= 0 {
		return 0, 0, false
	}
	periods := math.Floor((t-w.resetsAt)/w.periodMs) + 1
	return 0, w.resetsAt + periods*w.periodMs, true
}

// activeWindows drops a window whose last reset has passed and whose newest
// observation is more than one period older than the account's newest.
func activeWindows(windows []windowObs, now float64) []windowObs {
	if len(windows) == 0 {
		return nil
	}
	freshest := math.Inf(-1)
	for _, w := range windows {
		freshest = math.Max(freshest, w.observedAt)
	}
	var out []windowObs
	for _, w := range windows {
		lastReset, known := w.resetsAt, w.hasReset
		if !known && w.periodMs > 0 {
			lastReset, known = w.observedAt+w.periodMs, true
		}
		resetPassed := known && lastReset <= now
		lagging := freshest-w.observedAt > math.Max(w.periodMs, 0)
		if !resetPassed || !lagging {
			out = append(out, w)
		}
	}
	return out
}

// burnRate is the fraction per hour a window consumed over
// [now - windowMs, now]. Usage between observations is taken as linear, and
// a known reset between two observations restarts the increase from 0.
func burnRate(series *windowSeries, now, windowMs float64) float64 {
	if series == nil || windowMs <= 0 {
		return 0
	}
	start := now - windowMs
	var points []seriesPoint
	for _, p := range series.points {
		if p.t <= now {
			points = append(points, p)
		}
	}
	baseline := -1
	for k, p := range points {
		if p.t > start {
			break
		}
		baseline = k
	}
	first := baseline
	if first < 0 {
		first = slices.IndexFunc(points, func(p seriesPoint) bool { return p.t > start })
	}
	if first < 0 || first >= len(points)-1 {
		return 0
	}
	spanStart := points[first].t
	if baseline >= 0 {
		spanStart = start
	}
	spanHours := (now - spanStart) / hourMs
	if spanHours <= 0 {
		return 0
	}
	used := 0.0
	for k := first + 1; k < len(points); k++ {
		prev, cur := points[k-1], points[k]
		from, base := prev.t, prev.u
		for _, r := range series.resets {
			if r > prev.t && r <= cur.t {
				from, base = r, 0
			}
		}
		increase := math.Max(0, cur.u-base)
		if from < spanStart {
			used += increase * (cur.t - spanStart) / (cur.t - from)
		} else {
			used += increase
		}
	}
	return used / spanHours
}

type simWindow struct {
	u       float64
	next    float64
	hasNext bool
	period  float64
	// rate is the fraction per millisecond, multiplier applied.
	rate float64
}

func remainingOf(windows []simWindow) float64 {
	if len(windows) == 0 {
		return 1
	}
	top := math.Inf(-1)
	for _, w := range windows {
		top = math.Max(top, w.u)
	}
	return 1 - top
}

// timeToOvertake is the time until a window below the maximum catches up
// with it, where remaining changes slope.
func timeToOvertake(windows []simWindow) float64 {
	if len(windows) < 2 {
		return math.Inf(1)
	}
	top := math.Inf(-1)
	for _, w := range windows {
		top = math.Max(top, w.u)
	}
	lead := math.Inf(-1)
	for _, w := range windows {
		if top-w.u <= epsilon {
			lead = math.Max(lead, w.rate)
		}
	}
	soonest := math.Inf(1)
	for _, w := range windows {
		if top-w.u > epsilon && w.rate > lead {
			soonest = math.Min(soonest, (top-w.u)/(w.rate-lead))
		}
	}
	return soonest
}

// projectAccount projects one account's remaining capacity from now to end.
func projectAccount(a accountInput, now, end, burnWindowMs, multiplier float64) accountProjection {
	var sim []simWindow
	for _, w := range activeWindows(a.windows, now) {
		if !w.accountWide {
			continue
		}
		u, next, hasNext := rollForward(w, now)
		var series *windowSeries
		for k := range a.series {
			if a.series[k].window == w.window {
				series = &a.series[k]
				break
			}
		}
		sim = append(sim, simWindow{
			u: u, next: next, hasNext: hasNext, period: w.periodMs,
			rate: burnRate(series, now, burnWindowMs) * math.Max(0, multiplier) / hourMs,
		})
	}

	points := []point{{now, remainingOf(sim)}}
	var resets []resetEvent
	emptyAt := math.NaN()
	if remainingOf(sim) <= epsilon {
		emptyAt = now
	}
	t := now
	for guard := 0; t < end && guard < maxSimSteps; guard++ {
		blocked := false
		for _, w := range sim {
			if w.u >= 1-epsilon {
				blocked = true
			}
		}
		next := end
		for _, w := range sim {
			if w.hasNext {
				next = math.Min(next, w.next)
			}
			if !blocked && w.rate > 0 {
				next = math.Min(next, t+(1-w.u)/w.rate)
			}
		}
		if !blocked {
			next = math.Min(next, t+timeToOvertake(sim))
		}
		next = math.Max(next, t)
		if !blocked {
			for k := range sim {
				sim[k].u = math.Min(1, sim[k].u+sim[k].rate*(next-t))
				if 1-sim[k].u <= epsilon {
					sim[k].u = 1
				}
			}
		}
		t = next
		before := remainingOf(sim)
		reset := false
		for k := range sim {
			for sim[k].hasNext && sim[k].next <= t {
				sim[k].u = 0
				if sim[k].period > 0 {
					sim[k].next += sim[k].period
				} else {
					sim[k].hasNext = false
				}
				reset = true
			}
		}
		after := remainingOf(sim)
		points = append(points, point{t, before})
		if before <= epsilon && math.IsNaN(emptyAt) {
			emptyAt = t
		}
		if reset && after != before {
			points = append(points, point{t, after})
			resets = append(resets, resetEvent{t, after - before})
		}
	}
	return accountProjection{authIndex: a.authIndex, remainingNow: points[0].v, emptyAt: emptyAt, points: dedupe(points), resets: resets}
}

func dedupe(points []point) []point {
	var out []point
	for _, p := range points {
		if n := len(out); n > 0 && out[n-1] == p {
			continue
		}
		out = append(out, p)
	}
	return out
}

// sumPolylines adds polylines; a line adds nothing outside its own span.
func sumPolylines(lines [][]point) []point {
	var live [][]point
	seen := map[float64]bool{}
	var times []float64
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		live = append(live, line)
		for _, p := range line {
			if !seen[p.t] {
				seen[p.t] = true
				times = append(times, p.t)
			}
		}
	}
	sort.Float64s(times)
	cursors := make([]int, len(live))
	var out []point
	for _, t := range times {
		left, right := 0.0, 0.0
		for k, line := range live {
			if t < line[0].t || t > line[len(line)-1].t {
				continue
			}
			i := cursors[k]
			for line[i].t < t {
				i++
			}
			cursors[k] = i
			if line[i].t == t {
				j := i
				for j+1 < len(line) && line[j+1].t == t {
					j++
				}
				if i > 0 {
					left += line[i].v
				}
				right += line[j].v
			} else {
				a, b := line[i-1], line[i]
				v := a.v + (b.v-a.v)*(t-a.t)/(b.t-a.t)
				left += v
				right += v
			}
		}
		if len(out) > 0 {
			out = append(out, point{t, left})
		}
		if len(out) == 0 || math.Abs(right-left) > epsilon {
			out = append(out, point{t, right})
		}
	}
	return out
}

// projectPool projects every account and their sum. Pass only the accounts
// that count toward the pool.
func projectPool(accounts []accountInput, now, end, burnWindowMs, multiplier float64) poolProjection {
	projected := make([]accountProjection, len(accounts))
	lines := make([][]point, len(accounts))
	gains := map[float64]float64{}
	remaining := 0.0
	for k, a := range accounts {
		projected[k] = projectAccount(a, now, end, burnWindowMs, multiplier)
		lines[k] = projected[k].points
		remaining += projected[k].remainingNow
		for _, r := range projected[k].resets {
			gains[r.t] += r.gain
		}
	}
	var resets []resetEvent
	for t, gain := range gains {
		if gain > epsilon {
			resets = append(resets, resetEvent{t, gain})
		}
	}
	sort.Slice(resets, func(x, y int) bool { return resets[x].t < resets[y].t })
	points := sumPolylines(lines)
	out := poolProjection{accounts: projected, points: points, resets: resets, capacity: len(accounts), remainingNow: remaining, emptyAt: math.NaN()}
	if out.capacity > 0 {
		for _, p := range points {
			if p.v <= epsilon {
				out.emptyAt = p.t
				break
			}
		}
	}
	for k := range resets {
		if resets[k].t > now {
			out.nextReset = &resets[k]
			break
		}
	}
	return out
}

// recordedAccount is one account's recorded remaining as a step function
// over its account-wide observations, clipped to [from, to].
func recordedAccount(series []windowSeries, from, to float64) []point {
	type event struct {
		t      float64
		window string
		u      float64
		reset  bool
	}
	var events []event
	for _, s := range series {
		if !s.accountWide {
			continue
		}
		for _, p := range s.points {
			events = append(events, event{p.t, s.window, p.u, false})
		}
		for _, r := range s.resets {
			events = append(events, event{r, s.window, 0, true})
		}
	}
	// At equal times the reset applies first, so an observation at the reset
	// instant wins.
	sort.SliceStable(events, func(x, y int) bool {
		if events[x].t != events[y].t {
			return events[x].t < events[y].t
		}
		return events[x].reset && !events[y].reset
	})
	used := map[string]float64{}
	remaining := func() float64 {
		if len(used) == 0 {
			return 1
		}
		top := math.Inf(-1)
		for _, u := range used {
			top = math.Max(top, u)
		}
		return 1 - top
	}
	var steps []point
	for _, e := range events {
		if e.t > to {
			break
		}
		if _, ok := used[e.window]; e.reset && !ok {
			continue
		}
		used[e.window] = clamp01(e.u)
		v := remaining()
		if n := len(steps); n > 0 && steps[n-1].t == e.t {
			steps[n-1].v = v
		} else {
			steps = append(steps, point{e.t, v})
		}
	}
	if len(steps) == 0 {
		return nil
	}
	var out []point
	var carried *point
	for k := range steps {
		s := steps[k]
		if s.t < from {
			carried = &steps[k]
			continue
		}
		if len(out) == 0 && carried != nil {
			out = append(out, point{from, carried.v})
		}
		if n := len(out); n > 0 {
			if out[n-1].v == s.v {
				continue
			}
			out = append(out, point{s.t, out[n-1].v})
		}
		out = append(out, s)
	}
	if len(out) == 0 && carried != nil {
		out = append(out, point{from, carried.v})
	}
	if last := out[len(out)-1]; last.t < to {
		out = append(out, point{to, last.v})
	}
	return out
}

// decreaseBetween is the total decrease of a polyline over (a, b];
// increases (resets) are ignored.
func decreaseBetween(points []point, a, b float64) float64 {
	total := 0.0
	for k := 1; k < len(points); k++ {
		p, q := points[k-1], points[k]
		if q.t == p.t {
			if p.t > a && p.t <= b {
				total += math.Max(0, p.v-q.v)
			}
			continue
		}
		lo, hi := math.Max(a, p.t), math.Min(b, q.t)
		if hi <= lo {
			continue
		}
		total += math.Max(0, p.v-q.v) * (hi - lo) / (q.t - p.t)
	}
	return total
}

// drainAt is the drain rate, fraction per hour, of a polyline over the
// trailing window ending at t.
func drainAt(points []point, t, windowMs float64) float64 {
	if windowMs <= 0 {
		return 0
	}
	return decreaseBetween(points, t-windowMs, t) / (windowMs / hourMs)
}
