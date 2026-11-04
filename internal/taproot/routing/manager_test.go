package routing

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/265866/taproot/internal/registry"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/265866/taproot/sdk/cliproxy/executor"
)

// stubExecutor stands in for the provider network call. Selection never executes it;
// the manager only requires one registered for the provider.
type stubExecutor struct{ provider string }

func (e stubExecutor) Identifier() string { return e.provider }
func (stubExecutor) Execute(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}
func (stubExecutor) ExecuteStream(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}
func (stubExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}
func (stubExecutor) CountTokens(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}
func (stubExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

// newAffinityManager builds a real manager with the selector wrapped in upstream's
// session-affinity selector, as sdk/cliproxy/service_config.go does.
func newAffinityManager(t *testing.T, model string, auths ...*coreauth.Auth) *coreauth.Manager {
	t.Helper()
	manager := coreauth.NewManager(nil, nil, nil)
	affinity := coreauth.NewSessionAffinitySelectorWithConfig(coreauth.SessionAffinityConfig{
		Fallback: NewSoonestResetSelector(),
		TTL:      time.Hour,
	})
	t.Cleanup(affinity.Stop)
	manager.SetSelector(affinity)
	manager.RegisterExecutor(stubExecutor{provider: "claude"})
	for _, auth := range auths {
		register(t, manager, model, auth)
	}
	return manager
}

func register(t *testing.T, manager *coreauth.Manager, model string, auth *coreauth.Auth) {
	t.Helper()
	if _, err := manager.Register(coreauth.WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatalf("Register(%s): %v", auth.ID, err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
	id := auth.ID
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
}

// observe records the quota headers of a response on the credential, as the manager
// does after every upstream call.
func observe(t *testing.T, manager *coreauth.Manager, auth *coreauth.Auth, resetIn time.Duration) {
	t.Helper()
	current, ok := manager.GetByID(auth.ID)
	if !ok {
		t.Fatalf("GetByID(%s) not found", auth.ID)
	}
	current.Quota.Signals = liveClaudeAuth(auth.ID, resetIn).Quota.Signals
	current.Quota.ObservedAt = time.Now()
	if _, err := manager.Update(coreauth.WithSkipPersist(context.Background()), current); err != nil {
		t.Fatalf("Update(%s): %v", auth.ID, err)
	}
}

func sessionOpts(sessionID string) cliproxyexecutor.Options {
	headers := http.Header{}
	headers.Set("X-Claude-Code-Session-Id", sessionID)
	return cliproxyexecutor.Options{Headers: headers, Metadata: map[string]any{}}
}

func liveClaudeAuth(id string, resetIn time.Duration) *coreauth.Auth {
	now := time.Now()
	auth := claudeAuth(id, now.Add(resetIn), "0.3")
	auth.Status = coreauth.StatusActive
	auth.Quota.ObservedAt = now
	return auth
}

func TestAffinityKeepsBoundSessionAndRoutesNewSessionsBySoonestReset(t *testing.T) {
	const model = "srf-affinity-model"
	late := liveClaudeAuth("srf-affinity-late", 96*time.Hour)
	soon := liveClaudeAuth("srf-affinity-soon", 20*time.Hour)
	// The late account is the only one at first, so the first session binds to it.
	manager := newAffinityManager(t, model, late)
	ctx := context.Background()

	pick := func(sessionID string) string {
		t.Helper()
		auth, err := manager.SelectAuth(ctx, "claude", model, sessionOpts(sessionID))
		if err != nil {
			t.Fatalf("SelectAuth(%s): %v", sessionID, err)
		}
		return auth.ID
	}

	if got := pick("session-old"); got != late.ID {
		t.Fatalf("first session = %s, want %s (only account)", got, late.ID)
	}

	// An account whose week resets sooner is added.
	register(t, manager, model, soon)

	if got := pick("session-old"); got != late.ID {
		t.Fatalf("bound session moved to %s, want it to keep %s", got, late.ID)
	}
	for _, sessionID := range []string{"session-new-1", "session-new-2"} {
		if got := pick(sessionID); got != soon.ID {
			t.Fatalf("%s = %s, want soonest reset %s", sessionID, got, soon.ID)
		}
	}
	if got := pick("session-old"); got != late.ID {
		t.Fatalf("bound session moved to %s after new sessions, want %s", got, late.ID)
	}
}

// After a restart upstream has no quota signals for any account. Each account is probed
// once, and once their resets are known new sessions go to the soonest one, even though
// the late account answered first.
func TestAfterRestartEveryAccountIsProbedThenSoonestWins(t *testing.T) {
	const model = "srf-restart-model"
	late := liveClaudeAuth("srf-restart-a-late", 0)
	soon := liveClaudeAuth("srf-restart-b-soon", 0)
	late.Quota = coreauth.QuotaState{}
	soon.Quota = coreauth.QuotaState{}
	manager := newAffinityManager(t, model, late, soon)

	pick := func(sessionID string) string {
		t.Helper()
		auth, err := manager.SelectAuth(context.Background(), "claude", model, sessionOpts(sessionID))
		if err != nil {
			t.Fatalf("SelectAuth(%s): %v", sessionID, err)
		}
		return auth.ID
	}

	if got := pick("session-1"); got != late.ID {
		t.Fatalf("session-1 = %s, want probe of %s", got, late.ID)
	}
	observe(t, manager, late, 96*time.Hour)
	if got := pick("session-2"); got != soon.ID {
		t.Fatalf("session-2 = %s, want probe of unobserved %s", got, soon.ID)
	}
	observe(t, manager, soon, 20*time.Hour)
	for _, sessionID := range []string{"session-3", "session-4"} {
		if got := pick(sessionID); got != soon.ID {
			t.Fatalf("%s = %s, want soonest reset %s", sessionID, got, soon.ID)
		}
	}
	if got := pick("session-1"); got != late.ID {
		t.Fatalf("session-1 moved to %s, want it to keep %s", got, late.ID)
	}
}

func TestFailoverWhenSoonestResetCoolsDown(t *testing.T) {
	const model = "srf-failover-model"
	late := liveClaudeAuth("srf-failover-late", 96*time.Hour)
	soon := liveClaudeAuth("srf-failover-soon", 20*time.Hour)
	manager := newAffinityManager(t, model, late, soon)
	ctx := context.Background()

	pick := func(sessionID string) string {
		t.Helper()
		auth, err := manager.SelectAuth(ctx, "claude", model, sessionOpts(sessionID))
		if err != nil {
			t.Fatalf("SelectAuth(%s): %v", sessionID, err)
		}
		return auth.ID
	}

	if got := pick("session-a"); got != soon.ID {
		t.Fatalf("session-a = %s, want %s", got, soon.ID)
	}
	// The 5h window runs out: upstream records a 429 cooldown on the soon account.
	manager.MarkResult(ctx, coreauth.Result{
		AuthID:   soon.ID,
		Provider: "claude",
		Model:    model,
		Success:  false,
		Error:    &coreauth.Error{HTTPStatus: http.StatusTooManyRequests, Message: "rate limited"},
	})
	if got := pick("session-a"); got != late.ID {
		t.Fatalf("bound session after cooldown = %s, want failover to %s", got, late.ID)
	}
	if got := pick("session-b"); got != late.ID {
		t.Fatalf("new session during cooldown = %s, want %s", got, late.ID)
	}
}

func TestHigherPriorityTierWinsOverSoonerReset(t *testing.T) {
	const model = "srf-priority-model"
	preferred := liveClaudeAuth("srf-priority-high", 96*time.Hour)
	preferred.Attributes = map[string]string{"priority": "10"}
	backup := liveClaudeAuth("srf-priority-low", time.Hour)
	manager := newAffinityManager(t, model, preferred, backup)

	auth, err := manager.SelectAuth(context.Background(), "claude", model, sessionOpts("session-p"))
	if err != nil {
		t.Fatalf("SelectAuth: %v", err)
	}
	if auth.ID != preferred.ID {
		t.Fatalf("picked %s, want higher priority %s", auth.ID, preferred.ID)
	}
}
