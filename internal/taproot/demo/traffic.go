package main

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/sdk/cliproxy/usage"
)

// demoSeed fixes the generated traffic. Every time is an offset from the
// start time the tool is given, so each run produces the same history,
// shifted to end at its own start.
const demoSeed = 20261103

const (
	historyDays = 30
	// burstSize requests finish at the start time; they also go to the request log.
	burstSize = 8
	// startHour and startWeekday are the demo user's local time at the start: a Wednesday
	// afternoon, so the history ends with sessions in progress.
	startHour    = 15
	startWeekday = time.Wednesday
)

type price struct{ in, out, read, write5m, write1h float64 }

type model struct {
	name      string
	provider  string
	price     price // USD per million tokens
	ttft      time.Duration
	tokPerSec float64
}

var (
	opus   = model{"claude-opus-5-5", "claude", price{4, 20, 0.2, 5, 8}, 2200 * time.Millisecond, 55}
	sonnet = model{"claude-sonnet-5-5", "claude", price{2, 10, 0.2, 2.5, 4}, 1300 * time.Millisecond, 85}
	haiku  = model{"claude-haiku-5-5", "claude", price{0.1, 0.5, 0.01, 0.125, 0.2}, 600 * time.Millisecond, 160}
	sol    = model{"gpt-6-sol", "codex", price{2, 10, 0.2, 0, 0}, 2600 * time.Millisecond, 70}
	luna   = model{"gpt-6-luna", "codex", price{0.1, 0.5, 0.01, 0, 0}, 900 * time.Millisecond, 150}
)

// device is a caller on the demo tailnet. oneHour is the share of its main
// Claude conversations that write the 1h prompt cache instead of the 5m one.
type device struct {
	access.Device
	weight  float64
	oneHour float64
}

const demoLogin = "alice@github"

var devices = []device{
	{access.Device{ID: "node:nLAPTOP01CNTRL", Name: "laptop", Login: demoLogin, Via: access.ViaTailnet}, 0.38, 0.92},
	{access.Device{ID: "node:nDESKTOP1CNTRL", Name: "desktop", Login: demoLogin, Via: access.ViaTailnet}, 0.30, 0.55},
	{access.Device{ID: "node:nWORKSTN1CNTRL", Name: "workstation", Login: demoLogin, Via: access.ViaTailnet}, 0.27, 0.04},
	{access.Device{ID: "node:nPHONE001CNTRL", Name: "phone", Login: demoLogin, Via: access.ViaTailnet}, 0.05, 1},
}

// serverDevice runs scheduled agents on the server itself.
var serverDevice = device{access.Device{ID: access.LocalDeviceID, Name: "server (local)", Login: demoLogin, Via: access.ViaTailnet}, 0, 0}

// demoAccount is one fake subscription. Budgets are API-equivalent dollars
// per window; they turn the generated traffic into quota utilization.
type demoAccount struct {
	provider, email, plan string
	weeklyBudget          float64
	shortBudget           float64
	// nextWeekly is when the weekly window next resets, from the start time.
	nextWeekly time.Duration
	// signedOut is how long before the start the account lost its login;
	// zero means it never did. From then on it serves nothing.
	signedOut time.Duration

	authIndex, fileName string

	short, weekly window
}

func demoAccounts() []*demoAccount {
	return []*demoAccount{
		{provider: "claude", email: "alice@example.com", plan: "max", weeklyBudget: 72, shortBudget: 14, nextWeekly: 50 * time.Hour},
		{provider: "claude", email: "alice@example.org", plan: "max", weeklyBudget: 72, shortBudget: 14, nextWeekly: 109 * time.Hour},
		{provider: "claude", email: "research@example.com", plan: "pro", weeklyBudget: 30, shortBudget: 5, nextWeekly: 20 * time.Hour},
		{provider: "codex", email: "alice@example.com", plan: "pro", weeklyBudget: 26, shortBudget: 6, nextWeekly: 74 * time.Hour},
		{provider: "codex", email: "team@example.org", plan: "plus", weeklyBudget: 9, shortBudget: 2.5, nextWeekly: 138 * time.Hour},
		{provider: "codex", email: "dana@example.com", plan: "plus", weeklyBudget: 9, shortBudget: 2.5, nextWeekly: 100 * time.Hour, signedOut: 3 * time.Hour},
	}
}

// window is one rate-limit window. A weekly window resets at fixed instants;
// a short window opens at the hour of the first request after the last one ended.
type window struct {
	period time.Duration
	fixed  bool
	// origin is the generator's start; short windows open on whole hours from it.
	origin time.Time
	end    time.Time
	used   float64
	budget float64
}

func (w *window) advance(t time.Time) {
	if w.end.IsZero() || t.Before(w.end) {
		return
	}
	w.used = 0
	if !w.fixed {
		w.end = time.Time{}
		return
	}
	for !t.Before(w.end) {
		w.end = w.end.Add(w.period)
	}
}

func (w *window) open(t time.Time) {
	if w.end.IsZero() {
		w.end = floorHour(t, w.origin).Add(w.period)
	}
}

// floorHour rounds t down to a whole number of hours from origin.
func floorHour(t, origin time.Time) time.Time {
	d := t.Sub(origin)
	r := d % time.Hour
	if r < 0 {
		r += time.Hour
	}
	return t.Add(-r)
}

func (w *window) util() float64 { return math.Min(w.used/w.budget, 1) }

func (a *demoAccount) advance(t time.Time) {
	a.short.advance(t)
	a.weekly.advance(t)
}

func (a *demoAccount) limited() bool { return a.short.util() >= 1 || a.weekly.util() >= 1 }

// request is one generated usage record, the device that sent it, and, for
// the final burst, what goes into its request log.
type request struct {
	usage.Record
	device device
	burst  bool
	// logged also writes a request log for a request outside the burst.
	logged bool
	prompt string
	reply  string
}

type session struct {
	// uid is the client's session UUID, or a subagent's agent ID.
	uid       string
	parent    *session
	provider  string
	model     model
	device    device
	account   *demoAccount
	next      time.Time
	last      time.Time
	turnsLeft int
	oneHour   bool
	ctx       int64 // cached prefix tokens
	pending   int64 // tokens added since the last request
	fresh     bool
	subagent  bool
	topic     int
	index     int // heap index
}

// id is the session as the proxy records it: claude:<uuid> or codex:<uuid>,
// and claude:<uuid>:agent:<agent ID> for a subagent.
func (s *session) id() string {
	if s.parent != nil {
		return s.parent.id() + ":agent:" + s.uid
	}
	return s.provider + ":" + s.uid
}

func (s *session) parentID() string {
	if s.parent == nil {
		return ""
	}
	return s.parent.id()
}

type sessionQueue []*session

func (q sessionQueue) Len() int           { return len(q) }
func (q sessionQueue) Less(i, j int) bool { return q[i].next.Before(q[j].next) }
func (q sessionQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index, q[j].index = i, j
}

func (q *sessionQueue) Push(x any) {
	s := x.(*session)
	s.index = len(*q)
	*q = append(*q, s)
}

func (q *sessionQueue) Pop() any {
	old := *q
	s := old[len(old)-1]
	*q = old[:len(old)-1]
	return s
}

type generator struct {
	rng      *rand.Rand
	start    time.Time
	accounts []*demoAccount
	queue    sessionQueue
	out      []request
}

// generate returns 30 days of traffic ending at start, sorted by time. It
// is a pure function of start and the accounts' auth indexes.
func generate(start time.Time, accounts []*demoAccount) []request {
	return generateWith(start, accounts, false)
}

// squeezeAt is how long before the start -degraded uses up most of the
// serving Claude accounts' weekly quota, as a heavy job would.
const squeezeAt = 15 * time.Minute

// generateWith is generate; with degraded, the two Claude max accounts are
// 92% through their weekly window from squeezeAt before the start on, so the
// Claude pool runs low, and degradedTraffic's overloaded quarter hour is added.
func generateWith(start time.Time, accounts []*demoAccount, degraded bool) []request {
	g := &generator{rng: rand.New(rand.NewPCG(demoSeed, demoSeed>>1)), start: start, accounts: accounts}
	from := start.Add(-historyDays * 24 * time.Hour)
	for _, a := range accounts {
		a.short = window{period: 5 * time.Hour, origin: start, budget: a.shortBudget}
		end := start.Add(a.nextWeekly)
		for end.Sub(from) > 7*24*time.Hour {
			end = end.Add(-7 * 24 * time.Hour)
		}
		a.weekly = window{period: 7 * 24 * time.Hour, fixed: true, end: end, budget: a.weeklyBudget}
	}
	g.scheduleSessions(from)
	cutoff := start.Add(-3 * time.Minute)
	squeezed := !degraded
	for g.queue.Len() > 0 && g.queue[0].next.Before(cutoff) {
		s := heap.Pop(&g.queue).(*session)
		if !squeezed && !s.next.Before(start.Add(-squeezeAt)) {
			squeezed = true
			for _, a := range accounts[:2] {
				a.advance(s.next)
				a.weekly.used = math.Max(a.weekly.used, 0.92*a.weekly.budget)
			}
		}
		g.turn(s, s.next, false)
		if s.turnsLeft > 0 {
			heap.Push(&g.queue, s)
		}
	}
	g.burst()
	g.recentFailures()
	if degraded {
		g.out = append(g.out, degradedTraffic(start, accounts)...)
	}
	sort.SliceStable(g.out, func(i, j int) bool { return g.out[i].RequestedAt.Before(g.out[j].RequestedAt) })
	return g.out
}

// activity is the relative chance of a new interactive session in a local hour.
var activity = [24]float64{0.25, 0.12, 0.04, 0.02, 0.02, 0.03, 0.1, 0.3, 0.7, 1, 1, 0.9, 0.6, 0.9, 1, 1, 0.9, 0.6, 0.4, 0.5, 0.7, 0.7, 0.55, 0.4}

func (g *generator) scheduleSessions(from time.Time) {
	for k := historyDays * 24; k > 0; k-- {
		h := g.start.Add(-time.Duration(k) * time.Hour)
		hour := startHour - k
		day := (hour - 23) / 24 // floor(hour / 24); hour is at most startHour
		rate := activity[(hour%24+24)%24]
		if wd := (int(startWeekday) + day%7 + 7) % 7; wd == int(time.Saturday) || wd == int(time.Sunday) {
			rate *= 0.4
		}
		// Usage grows a little over the month.
		rate *= 0.85 + 0.35*float64(h.Sub(from))/float64(g.start.Sub(from))
		for range g.poisson(rate) {
			g.newSession(h.Add(g.jitter(time.Hour)), g.pickDevice())
		}
		for range g.poisson(0.1) {
			g.newSession(h.Add(g.jitter(time.Hour)), serverDevice)
		}
	}
}

func (g *generator) newSession(at time.Time, dev device) *session {
	s := &session{uid: g.uuid(), device: dev, next: at, fresh: true, topic: g.rng.IntN(len(topics))}
	switch {
	case dev.ID == serverDevice.ID:
		s.provider, s.model, s.turnsLeft = "claude", sonnet, 3+g.rng.IntN(10)
	case g.rng.Float64() < 0.72:
		s.provider, s.model = "claude", opus
		if g.rng.Float64() < 0.28 {
			s.model = sonnet
		}
		s.turnsLeft = g.turns(22, 90)
		s.oneHour = g.rng.Float64() < dev.oneHour
	default:
		s.provider, s.model = "codex", sol
		if g.rng.Float64() < 0.15 {
			s.model = luna
		}
		s.turnsLeft = g.turns(14, 60)
	}
	heap.Push(&g.queue, s)
	return s
}

// turn runs one request of s at t and schedules its next one.
func (g *generator) turn(s *session, t time.Time, burst bool) *request {
	if s.account == nil || g.signedOut(s.account, t) {
		s.account = g.route(s.provider, t, nil)
	}
	s.account.advance(t)
	retryOf := ""
	if s.account.limited() {
		// The request that finds the limit fails; the proxy retries it on
		// another account, as the same client request, or the session stops
		// when the whole pool is limited.
		g.out = append(g.out, g.rejected(s, t))
		retryOf = g.out[len(g.out)-1].TraceID
		s.account = g.route(s.provider, t, s.account)
		s.account.advance(t)
		if s.account.limited() {
			s.turnsLeft = 0
			g.out[len(g.out)-1].burst = burst
			return &g.out[len(g.out)-1]
		}
		t = t.Add(time.Second + g.jitter(2*time.Second))
	}
	r := g.request(s, t)
	if retryOf != "" {
		r.TraceID = retryOf
	}
	r.burst = burst
	g.out = append(g.out, r)
	if !s.subagent && s.provider == "claude" && s.model != sonnet && g.rng.Float64() < 0.06 {
		g.spawnSubagent(s, t.Add(r.Latency))
	}
	s.turnsLeft--
	s.next = t.Add(r.Latency).Add(g.gap(s))
	return &g.out[len(g.out)-1]
}

// signedOut reports whether a has lost its login by t.
func (g *generator) signedOut(a *demoAccount, t time.Time) bool {
	return a.signedOut > 0 && !t.Before(g.start.Add(-a.signedOut))
}

// route is soonest-reset-first: the eligible account whose weekly window
// resets first, with some sessions spread over the others.
func (g *generator) route(provider string, t time.Time, exclude *demoAccount) *demoAccount {
	var eligible, all []*demoAccount
	for _, a := range g.accounts {
		if a.provider != provider || a == exclude || g.signedOut(a, t) {
			continue
		}
		a.advance(t)
		all = append(all, a)
		if !a.limited() {
			eligible = append(eligible, a)
		}
	}
	if len(eligible) == 0 {
		eligible = all
	}
	sort.SliceStable(eligible, func(i, j int) bool { return eligible[i].weekly.end.Before(eligible[j].weekly.end) })
	if len(eligible) > 1 && g.rng.Float64() < 0.2 {
		return eligible[1+g.rng.IntN(len(eligible)-1)]
	}
	return eligible[0]
}

func (g *generator) spawnSubagent(parent *session, at time.Time) {
	s := &session{
		uid: g.hex(16), parent: parent, provider: "claude", model: sonnet, device: parent.device,
		account: parent.account, next: at.Add(time.Second), fresh: true, subagent: true,
		turnsLeft: 3 + g.rng.IntN(16), topic: parent.topic,
	}
	if g.rng.Float64() < 0.4 {
		s.model = haiku
	}
	heap.Push(&g.queue, s)
}

func (g *generator) request(s *session, t time.Time) request {
	a := s.account
	r := request{device: s.device}
	r.Record = usage.Record{
		RequestID: g.uuid(), TraceID: g.hex(8), Provider: s.provider, Model: s.model.name,
		SessionID: s.id(), ParentSessionID: s.parentID(), AuthIndex: a.authIndex, AuthID: a.fileName,
		Stream: true, RequestedAt: t,
	}
	if g.rng.Float64() < 0.006 {
		// Overloaded or a server error: no tokens, no quota headers.
		r.Failed = true
		r.Fail.StatusCode = 529
		if s.provider == "codex" {
			r.Fail.StatusCode = 500
		}
		r.Latency = 800*time.Millisecond + g.jitter(4*time.Second)
		return r
	}
	var cost float64
	if s.provider == "claude" {
		cost = g.claudeTokens(s, &r)
	} else {
		cost = g.codexTokens(s, &r)
	}
	a.short.open(t)
	a.short.used += cost
	a.weekly.used += cost
	r.ResponseHeaders = quotaHeaders(a, "allowed")
	s.last = t.Add(r.Latency)
	return r
}

func (g *generator) rejected(s *session, t time.Time) request {
	a := s.account
	r := request{device: s.device}
	r.Record = usage.Record{
		RequestID: g.uuid(), TraceID: g.hex(8), Provider: s.provider, Model: s.model.name,
		SessionID: s.id(), ParentSessionID: s.parentID(), AuthIndex: a.authIndex, AuthID: a.fileName,
		Stream: true, RequestedAt: t, Failed: true, Latency: 250*time.Millisecond + g.jitter(300*time.Millisecond),
	}
	r.Fail.StatusCode = http.StatusTooManyRequests
	a.short.open(t)
	r.ResponseHeaders = quotaHeaders(a, "rejected")
	return r
}

func (g *generator) claudeTokens(s *session, r *request) float64 {
	ttl := 5 * time.Minute
	if s.oneHour {
		ttl = time.Hour
	}
	added := s.pending + g.lognormal(1800, 0.9, 150, 40_000)
	if s.fresh {
		added = 9_000 + g.rng.Int64N(13_000)
		if s.subagent {
			added = 5_000 + g.rng.Int64N(7_000)
		}
	}
	var read, write int64
	if s.fresh || r.RequestedAt.Sub(s.last) > ttl {
		write = s.ctx + added
	} else {
		read, write = s.ctx, added
	}
	s.fresh = false
	in := 1 + g.rng.Int64N(12)
	out := g.lognormal(520, 1, 12, 14_000)
	s.ctx = read + write
	s.pending = out
	if s.ctx > 165_000 {
		// Compaction: the next request starts from a summary.
		s.ctx, s.pending, s.fresh = 0, 0, true
	}
	d := &r.Detail
	d.InputTokens, d.OutputTokens, d.CacheReadTokens, d.CacheCreationTokens = in, out, read, write
	w5, w1 := write, int64(0)
	if s.oneHour && !s.subagent {
		w5, w1 = 0, write
	}
	d.CacheCreation5mTokens, d.CacheCreation1hTokens = &w5, &w1
	d.TotalTokens = in + out + read + write

	ttft := g.scale(s.model.ttft) + time.Duration(float64(read+write)/100_000*float64(800*time.Millisecond))
	r.TTFT = ttft
	r.Latency = ttft + time.Duration(float64(out)/s.model.tokPerSec*(0.8+0.45*g.rng.Float64())*float64(time.Second))
	p := s.model.price
	return (float64(in)*p.in + float64(out)*p.out + float64(read)*p.read + float64(w5)*p.write5m + float64(w1)*p.write1h) / 1e6
}

func (g *generator) codexTokens(s *session, r *request) float64 {
	added := s.pending + g.lognormal(2600, 0.9, 200, 50_000)
	if s.fresh {
		added = 11_000 + g.rng.Int64N(6_000)
	}
	cached := s.ctx
	if s.fresh || r.RequestedAt.Sub(s.last) > 30*time.Minute {
		cached = 0
	}
	s.fresh = false
	total := s.ctx + added
	out := g.lognormal(700, 0.9, 20, 16_000)
	reasoning := int64(float64(out) * (0.2 + 0.5*g.rng.Float64()))
	s.ctx = total
	s.pending = out
	if s.ctx > 220_000 {
		s.ctx, s.pending, s.fresh = 0, 0, true
	}
	d := &r.Detail
	d.InputTokens, d.CachedTokens, d.OutputTokens, d.ReasoningTokens = total, cached, out, reasoning
	d.TotalTokens = total + out

	ttft := g.scale(s.model.ttft) + time.Duration(float64(reasoning)/120*float64(time.Second))
	r.TTFT = ttft
	r.Latency = ttft + time.Duration(float64(out-reasoning)/s.model.tokPerSec*(0.8+0.45*g.rng.Float64())*float64(time.Second))
	p := s.model.price
	return (float64(total-cached)*p.in + float64(cached)*p.read + float64(out)*p.out) / 1e6
}

// burst ends the history with requests from the live sessions that finish at
// the start time, so the request log has recent records with real durations.
func (g *generator) burst() {
	providers := []string{"claude", "codex"}
	want := map[string]int{"claude": burstSize - 2, "codex": 2}
	var picked []*session
	for _, s := range g.queue {
		if !s.subagent && !s.fresh && want[s.provider] > 0 {
			want[s.provider]--
			picked = append(picked, s)
		}
	}
	for _, provider := range providers {
		for range want[provider] {
			s := g.newSession(g.start, devices[len(picked)%len(devices)])
			s.provider, s.model = provider, opus
			if provider == "codex" {
				s.model = sol
			}
			picked = append(picked, s)
		}
	}
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].uid < picked[j].uid })
	overloaded := false
	for i, s := range picked {
		r := g.turn(s, g.start, true)
		if i >= 1 && s.provider == "claude" && !overloaded {
			overloaded = true
			// One overloaded response, so the log shows a failure too.
			r.Failed, r.Fail.StatusCode = true, 529
			r.Detail, r.ResponseHeaders = usage.Detail{}, nil
			r.TTFT, r.Latency = 0, 1400*time.Millisecond
		}
		r.RequestedAt = g.start.Add(-r.Latency)
		if r.Failed {
			// Before the quarter hour the Dashboard's verdict reads, so the
			// demo serves normally unless -degraded.
			r.RequestedAt = g.start.Add(-16 * time.Minute)
		}
		r.prompt, r.reply = topics[s.topic].prompt, topics[s.topic].reply
	}
}

func quotaHeaders(a *demoAccount, status string) http.Header {
	h := http.Header{}
	short, weekly := a.short.util(), a.weekly.util()
	if status == "rejected" {
		if a.short.util() >= 1 {
			short = 1
		} else {
			weekly = 1
		}
	}
	shortEnd := a.short.end
	statusOf := func(u float64) string {
		switch {
		case u >= 1:
			return "rejected"
		case u >= 0.8:
			return "allowed_warning"
		}
		return "allowed"
	}
	unix := func(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }
	if a.provider == "claude" {
		for _, w := range []struct {
			claim string
			u     float64
			end   time.Time
		}{{"5h", short, shortEnd}, {"7d", weekly, a.weekly.end}} {
			h.Set("anthropic-ratelimit-unified-"+w.claim+"-utilization", strconv.FormatFloat(w.u, 'f', 4, 64))
			h.Set("anthropic-ratelimit-unified-"+w.claim+"-reset", unix(w.end))
			h.Set("anthropic-ratelimit-unified-"+w.claim+"-status", statusOf(w.u))
		}
		return h
	}
	for _, w := range []struct {
		slot    string
		u       float64
		minutes int
		end     time.Time
	}{{"primary", short, 300, shortEnd}, {"secondary", weekly, 10080, a.weekly.end}} {
		h.Set("x-codex-"+w.slot+"-used-percent", strconv.FormatFloat(w.u*100, 'f', 1, 64))
		h.Set("x-codex-"+w.slot+"-window-minutes", strconv.Itoa(w.minutes))
		h.Set("x-codex-"+w.slot+"-reset-at", unix(w.end))
	}
	return h
}

func (g *generator) pickDevice() device {
	x := g.rng.Float64()
	for _, d := range devices {
		if x < d.weight {
			return d
		}
		x -= d.weight
	}
	return devices[0]
}

func (g *generator) poisson(lambda float64) int {
	l, k, p := math.Exp(-lambda), 0, 1.0
	for {
		p *= g.rng.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

func (g *generator) jitter(d time.Duration) time.Duration {
	return time.Duration(g.rng.Int64N(int64(d)))
}

// scale varies a base duration between 0.7x and 1.5x.
func (g *generator) scale(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.7 + 0.8*g.rng.Float64()))
}

func (g *generator) lognormal(median, sigma float64, lo, hi int64) int64 {
	v := int64(median * math.Exp(sigma*g.rng.NormFloat64()))
	return min(max(v, lo), hi)
}

func (g *generator) turns(median float64, hi int) int {
	return int(g.lognormal(median, 0.7, 2, int64(hi)))
}

// gap is the pause before a session's next request: mostly a minute or so,
// sometimes long enough for a 5m prompt cache to expire.
func (g *generator) gap(s *session) time.Duration {
	if s.subagent {
		return 2*time.Second + g.jitter(15*time.Second)
	}
	if g.rng.Float64() < 0.08 {
		return 6*time.Minute + g.jitter(60*time.Minute)
	}
	median := 45.0
	if s.provider == "codex" {
		median = 80
	}
	return time.Duration(g.lognormal(median, 0.9, 4, 1200)) * time.Second
}

func (g *generator) uuid() string {
	var b [16]byte
	for i := range b {
		b[i] = byte(g.rng.UintN(256))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (g *generator) hex(n int) string {
	const digits = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[g.rng.IntN(16)]
	}
	return string(b)
}

// topics are the synthetic prompts and replies shown in request logs.
var topics = []struct{ prompt, reply string }{
	{"Add a regression test for the CSV importer's quoted-field handling, then run the importer tests.", "I'll read the importer and its existing tests first, then add a case for a quoted field that contains a comma."},
	{"Why does the cache warm-up job run twice after a deploy? Check the scheduler.", "The scheduler registers the job in both the startup hook and the config watcher. I'll confirm by reading both call sites."},
	{"Rename the Store interface to Repository across the package and fix the callers.", "There are 14 references in 6 files. I'll rename the interface, update the callers, and run the build."},
	{"Make the settings form keep its draft when the page reloads.", "I'll store the draft in session storage on change and restore it when the form mounts, clearing it after a save."},
	{"Profile the image resize endpoint; it got slower after the last release.", "I'll run the benchmark on both tags and compare the profiles before changing anything."},
	{"Write a migration that adds an index on orders(created_at) and check the query plan.", "I'll add the migration, apply it to the test database, and compare EXPLAIN output before and after."},
	{"Explain the retry logic in the HTTP client and add jitter to the backoff.", "The client retries 5xx and connection errors with a fixed doubling delay. I'll add full jitter and a test with a fixed random source."},
	{"Summarize what changed in the billing module since last week.", "Three commits touched billing: a currency rounding fix, a new invoice field, and a test cleanup. Details below."},
}
