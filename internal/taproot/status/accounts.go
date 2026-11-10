package status

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/265866/taproot/internal/taproot/ingest"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
)

// State is whether an account can serve right now.
type State string

const (
	StateServing   State = "serving"
	StateCooling   State = "cooling"
	StateSignedOut State = "signed_out"
	StateDisabled  State = "disabled"
)

// Account is one credential as the status module sees it.
type Account struct {
	AuthIndex string
	Provider  string
	// Email is the account's email, or "" when the credential has none.
	Email string
	// Label is the part of the email before the @, else the credential's
	// label or file name.
	Label string
	State State
	// Until is when a cooling account serves again; zero when unknown.
	Until time.Time
	// RefreshFailing marks a serving account whose login can no longer be
	// refreshed: upstream keeps using its access token until ServesUntil
	// (zero when unknown), and then it is signed out.
	RefreshFailing bool
	ServesUntil    time.Time
	// Reason says in plain English why a signed-out or refresh-failing
	// account can't renew its login (loginReason): a sentence for a failure
	// upstream names by code, else upstream's sanitized message.
	Reason string
	// Since is when the sign-in problem was first seen.
	Since time.Time
}

// Name is how titles name the account: its email, else its label.
func (a Account) Name() string {
	if a.Email != "" {
		return a.Email
	}
	return a.Label
}

// accountsFrom classifies the credentials the auth manager lists. latest is
// the newest quota observation of each window: an account-wide window at its
// limit makes the account cooling until that window resets.
func accountsFrom(auths []*coreauth.Auth, latest []ingest.QuotaObservation, now time.Time) []Account {
	limited := limitedUntil(latest, now)
	out := make([]Account, 0, len(auths))
	for _, a := range auths {
		if a == nil {
			continue
		}
		idx := a.EnsureIndex()
		if idx == "" {
			continue
		}
		email := authEmail(a)
		acct := Account{
			AuthIndex: idx,
			Provider:  strings.ToLower(strings.TrimSpace(a.Provider)),
			Email:     email,
			Label:     labelFor(a, email),
			State:     StateServing,
		}
		// The sign-in rule is the alerts module's: upstream keeps serving a
		// sign-in-required account while its access token is valid
		// (canServeWithAccessToken in conductor_lifecycle.go), unless that
		// token was itself rejected.
		rejected := coreauth.HasUnauthorizedAuthFailure(a)
		broken := a.RequiresSignIn || rejected
		stillServes := broken && !rejected && a.HasValidAccessToken(now)
		if broken {
			acct.Reason = loginReason(a, now)
			acct.Since = a.RenewalEventAt
			if acct.Since.IsZero() {
				acct.Since = a.UpdatedAt
			}
		}
		switch {
		case a.Disabled || a.Status == coreauth.StatusDisabled:
			acct.State = StateDisabled
		case broken && !stillServes:
			acct.State = StateSignedOut
		default:
			acct.RefreshFailing = stillServes
			if stillServes {
				if exp, ok := a.AccessTokenExpirationTime(); ok {
					acct.ServesUntil = exp
				}
			}
			if until, cooling := coolingUntil(a, limited, idx, now); cooling {
				acct.State, acct.Until = StateCooling, until
			}
		}
		out = append(out, acct)
	}
	return out
}

type limit struct {
	until time.Time
	// unknown is set when one limited window has no known reset.
	unknown bool
}

// limitedUntil returns, per account at an account-wide limit, when its last
// limited window resets. The rule is the alerts module's limit rule.
func limitedUntil(latest []ingest.QuotaObservation, now time.Time) map[string]limit {
	out := map[string]limit{}
	for _, o := range latest {
		if !o.AccountWide || !atLimit(o, now) {
			continue
		}
		l := out[o.AuthIndex]
		reset := o.ResetsAt
		if reset.IsZero() && o.PeriodSeconds > 0 {
			reset = o.ObservedAt.Add(time.Duration(o.PeriodSeconds) * time.Second)
		}
		if reset.IsZero() {
			l.unknown = true
		} else if reset.After(l.until) {
			l.until = reset
		}
		out[o.AuthIndex] = l
	}
	return out
}

// atLimit reports whether a window blocks traffic now: it is rejected or
// full, and its reset (or one period after the reading, when no reset is
// known) is still ahead.
func atLimit(o ingest.QuotaObservation, now time.Time) bool {
	if o.Status != "rejected" && o.Utilization < 1 {
		return false
	}
	if !o.ResetsAt.IsZero() {
		return o.ResetsAt.After(now)
	}
	return o.PeriodSeconds <= 0 || o.ObservedAt.Add(time.Duration(o.PeriodSeconds)*time.Second).After(now)
}

// coolingUntil reports whether the account is resting: upstream holds the
// whole credential back after an error or a quota response, or an
// account-wide quota window is at its limit. A cooldown on some models only
// leaves the account serving the others, so it does not count. until is the
// later of the two ends, zero when one is unknown.
func coolingUntil(a *coreauth.Auth, limited map[string]limit, idx string, now time.Time) (time.Time, bool) {
	var until time.Time
	cooling, unknown := false, false
	for _, v := range coreauth.CooldownSnapshotForAuth(a, now) {
		if v.Scope != "credential" {
			continue
		}
		cooling = true
		if v.RetryAt.After(until) {
			until = v.RetryAt
		}
	}
	if l, ok := limited[idx]; ok {
		cooling = true
		unknown = l.unknown
		if l.until.After(until) {
			until = l.until
		}
	}
	if unknown {
		until = time.Time{}
	}
	return until, cooling
}

func authEmail(a *coreauth.Auth) string {
	if v, ok := a.Metadata["email"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(a.Attributes["email"])
}

// labelFor names an account in a pool bar: the part of its email before the
// @, else its label, else its credential file's name.
func labelFor(a *coreauth.Auth, email string) string {
	if local, _, ok := strings.Cut(email, "@"); ok && local != "" {
		return local
	}
	if email != "" {
		return email
	}
	if l := strings.TrimSpace(a.Label); l != "" {
		return l
	}
	if a.FileName != "" {
		return strings.TrimSuffix(filepath.Base(a.FileName), filepath.Ext(a.FileName))
	}
	return a.EnsureIndex()
}

// loginReason says why an account's login failed. A failure upstream names
// by a known code reads as plain English; any other text is upstream's
// sanitized message, which already has tokens, codes, and URLs redacted.
func loginReason(a *coreauth.Auth, now time.Time) string {
	msg := ""
	switch {
	case a.RefreshError != nil && strings.TrimSpace(a.RefreshError.Message) != "":
		msg = a.RefreshError.Message
	case strings.TrimSpace(a.StatusMessage) != "":
		msg = a.StatusMessage
	case a.LastError != nil:
		msg = a.LastError.Message
	}
	if msg = strings.TrimSpace(coreauth.SanitizeRenewalMessage(msg)); msg == "" {
		return renewalStates[a.RenewalStateAt(now)]
	}
	lower := strings.ToLower(msg)
	for _, r := range renewalCodes {
		if strings.Contains(lower, r.code) {
			return r.text
		}
	}
	return msg
}

// renewalCodes are the refresh failures upstream names by code
// (internal/auth/codex/refresh_journal.go, isTerminalRefreshGrantMessage in
// sdk/cliproxy/auth), in the order they are checked.
var renewalCodes = []struct{ code, text string }{
	{"refresh_outcome_unknown", "A login refresh was interrupted, so taproot can't tell whether the provider still accepts this login"},
	{"refresh_token_reused", "The provider ended this login because its refresh token was used elsewhere, such as by another copy of the credential"},
	{"refresh_token_expired", "The login expired"},
	{"refresh_token_invalidated", "The provider ended this login, for example after a sign-out or password change"},
	{"token_revoked", "The login was revoked"},
	{"invalid_grant", "The provider no longer accepts this login"},
	{"invalid_client", "The provider refused taproot's sign-in client"},
}

// renewalStates says upstream's renewal state in words, for an account with
// no failure text. States that are no reason to sign in say nothing.
var renewalStates = map[string]string{
	coreauth.RenewalStateSignInRequired:        "The provider asks for a new sign-in",
	coreauth.RenewalStateExpiredSignInRequired: "The login expired",
	coreauth.RenewalStateRetryScheduled:        "Taproot will try the login refresh again",
}
