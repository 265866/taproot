package main

import (
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/265866/taproot/sdk/cliproxy/usage"
)

// recentFailures adds, in the last two hours, the failures a dashboard should
// be able to show besides the rate limits and overloads of the traffic: an
// upstream reset, a prompt that is too long, a request the client abandoned,
// a retry that succeeded on another account, and one that failed on two. It
// draws from its own random source, so the rest of the history is unchanged.
func (g *generator) recentFailures() {
	x := &generator{rng: rand.New(rand.NewPCG(demoSeed, 2))}
	claude, codex := g.accounts[0], g.accounts[3]
	attempt := func(at time.Duration, dev device, m model, a *demoAccount, status int, body string, latency time.Duration) request {
		r := request{device: dev}
		r.Record = usage.Record{
			RequestID: x.uuid(), TraceID: x.hex(8), Provider: m.provider, Model: m.name, SessionID: m.provider + ":" + x.uuid(),
			AuthIndex: a.authIndex, AuthID: a.fileName, Stream: true, RequestedAt: g.start.Add(at), Latency: latency,
		}
		if status != http.StatusOK {
			r.Failed, r.Fail = true, usage.Failure{StatusCode: status, Body: body}
		}
		return r
	}
	laptop, desktop, workstation := devices[0], devices[1], devices[2]
	g.out = append(g.out,
		attempt(-95*time.Minute, workstation, sol, codex, http.StatusBadGateway, `{"error":{"message":"upstream connection reset by peer"}}`, 31*time.Second),
		attempt(-70*time.Minute, laptop, opus, claude, http.StatusBadRequest,
			`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 212034 tokens > 200000 maximum"}}`, 900*time.Millisecond),
		attempt(-40*time.Minute, desktop, sonnet, claude, ingest.StatusClientClosed, "", 6*time.Second),
	)
	// Overloaded on one account, then answered by another.
	first := attempt(-25*time.Minute, laptop, opus, g.accounts[2], 529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, 1200*time.Millisecond)
	retry := attempt(-25*time.Minute+1500*time.Millisecond, laptop, opus, claude, http.StatusOK, "", 9*time.Second)
	retry.TraceID, retry.SessionID = first.TraceID, first.SessionID
	retry.TTFT = 1800 * time.Millisecond
	retry.Detail = usage.Detail{InputTokens: 6, OutputTokens: 420, CacheReadTokens: 30_000, TotalTokens: 30_426}
	// Rate-limited on both Codex accounts.
	limited := attempt(-5*time.Minute, desktop, luna, g.accounts[4], http.StatusTooManyRequests,
		`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`, 400*time.Millisecond)
	again := attempt(-5*time.Minute+600*time.Millisecond, desktop, luna, codex, http.StatusTooManyRequests,
		`{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`, 350*time.Millisecond)
	again.TraceID, again.SessionID = limited.TraceID, limited.SessionID
	g.out = append(g.out, first, retry, limited, again)
}

// statusMessages stand in for the error body of generated failures that have none.
var statusMessages = map[int]string{
	http.StatusTooManyRequests:     "This request would exceed your account's rate limit. Please try again later.",
	http.StatusInternalServerError: "upstream server error",
	529:                            "Overloaded",
}

// clientRequests groups the usage records by their client request (trace
// ID): the attempts of one request are its retries on other accounts. Burst
// requests take the ID their request log is stored under, so the dashboard
// opens it. It adds one request no account could serve.
func clientRequests(requests []request, start time.Time) []ingest.ClientRequest {
	var order []string
	groups := map[string][]request{}
	for _, r := range requests {
		if groups[r.TraceID] == nil {
			order = append(order, r.TraceID)
		}
		groups[r.TraceID] = append(groups[r.TraceID], r)
	}
	out := make([]ingest.ClientRequest, 0, len(order)+1)
	for _, trace := range order {
		g := groups[trace]
		first, last := g[0], g[len(g)-1]
		end := first.RequestedAt
		id := trace
		for _, r := range g {
			if e := r.RequestedAt.Add(r.Latency); e.After(end) {
				end = e
			}
			if r.burst {
				id = r.RequestID
			}
		}
		attempts := len(g)
		c := ingest.ClientRequest{
			Start: first.RequestedAt, RequestID: id, Device: first.device.Device, Path: "/v1/messages", Model: last.Model,
			Provider: last.Provider, AuthIndex: last.AuthIndex, Stream: last.Stream, Status: http.StatusOK,
			Latency: end.Sub(first.RequestedAt), Attempts: &attempts,
		}
		if last.Provider == "codex" {
			c.Path = "/backend-api/codex/responses"
		}
		if last.TTFT > 0 {
			c.TTFT = last.RequestedAt.Sub(first.RequestedAt) + last.TTFT
		}
		if last.Failed {
			c.Status = last.Fail.StatusCode
			c.Failed = c.Status != ingest.StatusClientClosed
			c.ErrorMessage = last.Fail.Body
			if c.ErrorMessage == "" {
				c.ErrorMessage = statusMessages[c.Status]
			}
		}
		out = append(out, c)
	}
	zero := 0
	return append(out, ingest.ClientRequest{
		Start: start.Add(-12 * time.Minute), RequestID: "0a1b2c3d", Device: devices[3].Device, Path: "/v1/chat/completions",
		Model: "gemini-3-pro", Status: http.StatusBadRequest, Failed: true, Latency: 4 * time.Millisecond, Attempts: &zero,
		ErrorCode:    "unknown_provider",
		ErrorMessage: "no enabled account serves model gemini-3-pro: check the model name, or add an account that serves it on the taproot dashboard's Accounts page",
	})
}
