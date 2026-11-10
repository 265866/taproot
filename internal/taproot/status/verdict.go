package status

import (
	"fmt"
	"strings"
	"time"
)

const (
	// verdictWindow is how far back the verdict reads client requests.
	verdictWindow = 15 * time.Minute
	// trafficWindow decides which providers count: one with a client
	// request in it is one clients use.
	trafficWindow = time.Hour
	// minSample is the fewest requests a failure rate is judged on, so two
	// failures of three requests do not report an outage.
	minSample      = 20
	degradedRate   = 0.05
	downRate       = 0.5
	verdictNormal  = "normal"
	verdictDegrade = "degraded"
	verdictDown    = "down"
)

// Status is the "right now" verdict.
type Status struct {
	State    string    `json:"state"`
	Headline string    `json:"headline"`
	Detail   string    `json:"detail"`
	Reasons  []Reason  `json:"reasons"`
	Since    time.Time `json:"since"`
	// Providers lists every provider with an account.
	Providers []ProviderAccounts `json:"providers"`
	Window    RequestWindow      `json:"window"`
	At        time.Time          `json:"at"`
}

// Reason is one condition behind a degraded or down verdict.
type Reason struct {
	// Code is failure_rate, no_usable_account, or one_usable_account.
	Code string `json:"code"`
	// State is the verdict this reason alone gives.
	State    string `json:"state"`
	Provider string `json:"provider,omitempty"`
	Message  string `json:"message"`
}

// ProviderAccounts counts one provider's accounts by state.
type ProviderAccounts struct {
	Provider  string `json:"provider"`
	Accounts  int    `json:"accounts"`
	Usable    int    `json:"usable"`
	Cooling   int    `json:"cooling"`
	SignedOut int    `json:"signedOut"`
	Disabled  int    `json:"disabled"`
	// RecentRequests counts its client requests in the last hour.
	RecentRequests int64 `json:"recentRequests"`
}

// RequestWindow is the client requests of the last 15 minutes. Failed
// leaves out requests the client abandoned.
type RequestWindow struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	Requests    int64     `json:"requests"`
	Failed      int64     `json:"failed"`
	FailureRate *float64  `json:"failureRate"`
}

// verdict judges the snapshot. recent counts the last hour's client requests
// per provider.
func verdict(pools []Pool, accounts []Account, window RequestWindow, recent map[string]int64, now time.Time) Status {
	st := Status{State: verdictNormal, Reasons: []Reason{}, Providers: []ProviderAccounts{}, Window: window, At: now}
	byProvider := map[string]*ProviderAccounts{}
	for _, p := range pools {
		pa := &ProviderAccounts{Provider: p.Provider, RecentRequests: recent[p.Provider]}
		byProvider[p.Provider] = pa
	}
	for _, a := range accounts {
		pa := byProvider[a.Provider]
		if pa == nil {
			continue
		}
		pa.Accounts++
		switch a.State {
		case StateServing:
			pa.Usable++
		case StateCooling:
			pa.Cooling++
		case StateSignedOut:
			pa.SignedOut++
		case StateDisabled:
			pa.Disabled++
		}
	}
	for _, p := range pools {
		st.Providers = append(st.Providers, *byProvider[p.Provider])
	}

	// Provider reasons come first: they name what to fix.
	var empty []string
	for _, pa := range st.Providers {
		if pa.RecentRequests == 0 {
			continue
		}
		name := providerName(pa.Provider)
		switch pa.Usable {
		case 0:
			empty = append(empty, name)
			st.Reasons = append(st.Reasons, Reason{Code: "no_usable_account", State: verdictDown, Provider: pa.Provider, Message: "No account can serve " + name})
		case 1:
			// A provider with one enabled account always has one; that is
			// its normal, not a loss.
			if pa.Accounts-pa.Disabled > 1 {
				st.Reasons = append(st.Reasons, Reason{Code: "one_usable_account", State: verdictDegrade, Provider: pa.Provider, Message: name + " is down to one usable account"})
			}
		}
	}
	if window.Requests >= minSample {
		rate := float64(window.Failed) / float64(window.Requests)
		msg := fmt.Sprintf("%d%% of requests failing in the last 15 min", window.Failed*100/window.Requests)
		switch {
		case rate >= downRate:
			st.Reasons = append(st.Reasons, Reason{Code: "failure_rate", State: verdictDown, Message: msg})
		case rate >= degradedRate:
			st.Reasons = append(st.Reasons, Reason{Code: "failure_rate", State: verdictDegrade, Message: msg})
		}
	}
	for _, r := range st.Reasons {
		if r.State == verdictDown {
			st.State = verdictDown
		} else if st.State == verdictNormal {
			st.State = verdictDegrade
		}
	}

	switch st.State {
	case verdictNormal:
		st.Headline = "Serving normally"
	case verdictDown:
		if len(empty) > 0 {
			st.Headline = "Down: no account can serve " + strings.Join(empty, " or ")
		} else {
			st.Headline = "Down: " + firstMessage(st.Reasons, verdictDown)
		}
	default:
		st.Headline = "Degraded: " + firstMessage(st.Reasons, verdictDegrade)
	}
	st.Detail = requestsLine(window) + " · " + accountsLine(st.Providers)
	return st
}

func firstMessage(reasons []Reason, state string) string {
	for _, r := range reasons {
		if r.State == state {
			return r.Message
		}
	}
	return ""
}

func requestsLine(w RequestWindow) string {
	switch {
	case w.Requests == 0:
		return "No requests in the last 15 min"
	case w.Failed == 0:
		return "No failures in the last 15 min"
	}
	return fmt.Sprintf("%d of %d %s failed in the last 15 min", w.Failed, w.Requests, plural(w.Requests, "request", "requests"))
}

func accountsLine(providers []ProviderAccounts) string {
	total, usable := 0, 0
	parts := make([]string, 0, len(providers))
	for _, p := range providers {
		total += p.Accounts
		usable += p.Usable
		parts = append(parts, fmt.Sprintf("%s %d", providerName(p.Provider), p.Usable))
	}
	if total == 0 {
		return "No accounts yet"
	}
	return fmt.Sprintf("%d of %d %s can serve (%s)", usable, total, plural(int64(total), "account", "accounts"), strings.Join(parts, ", "))
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
