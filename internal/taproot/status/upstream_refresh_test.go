package status

import (
	"context"
	"errors"
	"testing"
	"time"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
)

// refusingClaude stands in for the Claude executor's token endpoint, which
// cannot be pointed elsewhere. Its errors read as internal/auth/claude's
// RefreshTokensWithRetry words them.
type refusingClaude struct {
	coreauth.ProviderExecutor
	reply string
}

func (refusingClaude) Identifier() string { return "claude" }

func (c refusingClaude) Refresh(context.Context, *coreauth.Auth) (*coreauth.Auth, error) {
	return nil, errors.New("token refresh failed after 3 attempts: token refresh failed with status " + c.reply)
}

const (
	expiredGrant = `400: {"error": "invalid_grant", "error_description": "Refresh token expired"}`
	forbidden    = `403: {"type": "error", "error": {"type": "permission_error", "message": "OAuth access is disabled for this organization"}}`
	rateLimited  = `429: {"type": "error", "error": {"type": "rate_limit_error", "message": "Rate limited"}}`
)

// Upstream's own refresh handling, fed Anthropic's refusal, leaves the state
// the status module must read. Upstream stamps its changes with the wall
// clock, so the token expiry and the evaluation use it too.
func TestUpstreamClaudeRefreshRefusal(t *testing.T) {
	cases := []struct {
		name      string
		reply     string
		expiresIn time.Duration
		refusals  int
		state     State
		failing   bool
		reason    string
	}{
		{"token valid, refused once", expiredGrant, 4 * time.Hour, 1, StateServing, false, ""},
		{"token valid, refused twice", expiredGrant, 4 * time.Hour, 2, StateServing, true, "The login expired"},
		{"token expired, refused twice", expiredGrant, -time.Hour, 2, StateSignedOut, false, "The login expired"},
		{"token expired, forbidden once", forbidden, -time.Hour, 1, StateSignedOut, false, ""},
		// Upstream retries a rate limit in minutes; a new sign-in would not help.
		{"token expired, rate limited", rateLimited, -time.Hour, 2, StateServing, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			m := coreauth.NewManager(nil, nil, nil)
			m.RegisterExecutor(refusingClaude{reply: c.reply})
			registered, err := m.Register(ctx, &coreauth.Auth{
				ID: "claude-bob.json", FileName: "claude-bob.json", Provider: "claude",
				Metadata: map[string]any{
					"email": "bob@example.org", "access_token": "access-bob", "refresh_token": "refresh-bob",
					"expired": time.Now().Add(c.expiresIn).Format(time.RFC3339),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			for range c.refusals {
				if _, err := m.ForceRefreshAuth(ctx, registered.ID); err == nil {
					t.Fatal("refresh succeeded, want Anthropic's refusal")
				}
			}
			auths := m.List()
			if len(auths) != 1 || auths[0].RequiresSignIn {
				t.Fatalf("upstream now marks the credential RequiresSignIn, so LoginBroken's extra cases may be unneeded: %+v", auths)
			}
			accounts := accountsFrom(auths, nil, time.Now())
			if len(accounts) != 1 {
				t.Fatalf("accounts = %+v", accounts)
			}
			a := accounts[0]
			if a.State != c.state || a.RefreshFailing != c.failing || (c.reason != "" && a.Reason != c.reason) {
				t.Fatalf("account = %+v, want %s, refresh failing %t, reason %q", a, c.state, c.failing, c.reason)
			}
		})
	}
}
