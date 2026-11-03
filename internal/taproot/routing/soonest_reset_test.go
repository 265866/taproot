package routing

import (
	"context"
	"strconv"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

var testNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func unix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func claudeAuth(id string, reset time.Time, utilization string) *coreauth.Auth {
	signals := map[string]string{}
	if !reset.IsZero() {
		signals["Anthropic-Ratelimit-Unified-7d-Reset"] = unix(reset)
	}
	if utilization != "" {
		signals["Anthropic-Ratelimit-Unified-7d-Utilization"] = utilization
	}
	return &coreauth.Auth{ID: id, Provider: "claude", Quota: coreauth.QuotaState{Signals: signals, ObservedAt: testNow}}
}

func ids(auths []*coreauth.Auth) []string {
	out := make([]string, len(auths))
	for i, a := range auths {
		out[i] = a.ID
	}
	return out
}

func newTestSelector() *SoonestResetSelector {
	return &SoonestResetSelector{now: func() time.Time { return testNow }}
}

// pickSequence runs n picks over the same candidates and returns the chosen IDs.
func pickSequence(t *testing.T, s *SoonestResetSelector, provider string, n int, auths []*coreauth.Auth) []string {
	t.Helper()
	out := make([]string, 0, n)
	for range n {
		got, err := s.Pick(context.Background(), provider, "m", cliproxyexecutor.Options{}, auths)
		if err != nil {
			t.Fatalf("Pick: %v", err)
		}
		out = append(out, got.ID)
	}
	return out
}

func TestPickOrdering(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		name  string
		auths []*coreauth.Auth
		want  []string
	}{
		{
			name: "soonest known reset wins every pick",
			auths: []*coreauth.Auth{
				claudeAuth("a-late", testNow.Add(4*day), "0.1"),
				claudeAuth("b-soon", testNow.Add(1*day), "0.9"),
			},
			want: []string{"b-soon", "b-soon", "b-soon"},
		},
		{
			name: "known reset beats unknown",
			auths: []*coreauth.Auth{
				claudeAuth("a-unknown", time.Time{}, ""),
				claudeAuth("b-known", testNow.Add(6*day), "0.2"),
			},
			want: []string{"b-known", "b-known"},
		},
		{
			name: "past reset rolls forward by whole weeks",
			auths: []*coreauth.Auth{
				// Last seen resetting 13 days ago: next reset is in 1 day.
				claudeAuth("a-stale", testNow.Add(-13*day), "1"),
				claudeAuth("b-fresh", testNow.Add(3*day), "0.1"),
			},
			want: []string{"a-stale", "a-stale"},
		},
		{
			name: "exhausted weekly is skipped",
			auths: []*coreauth.Auth{
				claudeAuth("a-exhausted", testNow.Add(1*day), "1.0"),
				claudeAuth("b-open", testNow.Add(5*day), "0.4"),
			},
			want: []string{"b-open", "b-open"},
		},
		{
			name: "every candidate exhausted still picks the soonest",
			auths: []*coreauth.Auth{
				claudeAuth("a", testNow.Add(2*day), "1"),
				claudeAuth("b", testNow.Add(1*day), "1"),
			},
			want: []string{"b", "b"},
		},
		{
			name: "equal resets rotate",
			auths: []*coreauth.Auth{
				claudeAuth("c", testNow.Add(2*day), "0.1"),
				claudeAuth("a", testNow.Add(2*day+20*time.Second), "0.1"),
				claudeAuth("b", testNow.Add(5*day), "0.1"),
			},
			want: []string{"a", "c", "a", "c"},
		},
		{
			name: "all unknown rotate",
			auths: []*coreauth.Auth{
				claudeAuth("b", time.Time{}, ""),
				claudeAuth("a", time.Time{}, ""),
			},
			want: []string{"a", "b", "a"},
		},
		{
			name: "model-scoped weekly is ignored",
			auths: []*coreauth.Auth{
				{ID: "a-model-weekly", Provider: "claude", Quota: coreauth.QuotaState{Signals: map[string]string{
					"Anthropic-Ratelimit-Unified-7d_sonnet-Reset": unix(testNow.Add(time.Hour)),
				}}},
				claudeAuth("b-weekly", testNow.Add(5*day), "0.1"),
			},
			want: []string{"b-weekly"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pickSequence(t, newTestSelector(), "claude", len(tc.want), tc.auths)
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("picks = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestWeeklyWindowOfCodex(t *testing.T) {
	observed := testNow.Add(-time.Hour)
	codex := func(signals map[string]string) *coreauth.Auth {
		return &coreauth.Auth{ID: "c", Provider: "codex", Quota: coreauth.QuotaState{Signals: signals, ObservedAt: observed}}
	}
	tests := []struct {
		name      string
		signals   map[string]string
		wantKnown bool
		wantReset time.Time
		exhausted bool
	}{
		{
			name: "secondary weekly via reset-after-seconds, primary 5h ignored",
			signals: map[string]string{
				"X-Codex-Primary-Window-Minutes":        "300",
				"X-Codex-Primary-Reset-After-Seconds":   "60",
				"X-Codex-Primary-Used-Percent":          "100",
				"X-Codex-Secondary-Window-Minutes":      "10080",
				"X-Codex-Secondary-Reset-After-Seconds": "86400",
				"X-Codex-Secondary-Used-Percent":        "40",
			},
			wantKnown: true,
			wantReset: observed.Add(24 * time.Hour),
		},
		{
			name: "secondary reset-at wins and full usage is exhausted",
			signals: map[string]string{
				"X-Codex-Secondary-Reset-At":     unix(testNow.Add(48 * time.Hour)),
				"X-Codex-Secondary-Used-Percent": "100",
			},
			wantKnown: true,
			wantReset: testNow.Add(48 * time.Hour),
			exhausted: true,
		},
		{
			name: "weekly reported as primary when there is no secondary",
			signals: map[string]string{
				"X-Codex-Primary-Window-Minutes": "10080",
				"X-Codex-Primary-Reset-At":       unix(testNow.Add(72 * time.Hour)),
			},
			wantKnown: true,
			wantReset: testNow.Add(72 * time.Hour),
		},
		{
			name: "past secondary reset rolls forward and is no longer exhausted",
			signals: map[string]string{
				"X-Codex-Secondary-Reset-At":     unix(testNow.Add(-2 * time.Hour)),
				"X-Codex-Secondary-Used-Percent": "100",
			},
			wantKnown: true,
			wantReset: testNow.Add(-2*time.Hour + week),
		},
		{
			name:    "only a 5h window is unknown",
			signals: map[string]string{"X-Codex-Primary-Window-Minutes": "300", "X-Codex-Primary-Reset-At": unix(testNow.Add(time.Hour))},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, known := WeeklyWindowOf(codex(tc.signals), testNow)
			if known != tc.wantKnown {
				t.Fatalf("known = %v, want %v", known, tc.wantKnown)
			}
			if !known {
				return
			}
			if !w.ResetsAt.Equal(tc.wantReset) || w.Exhausted != tc.exhausted {
				t.Fatalf("window = %+v, want reset %v exhausted %v", w, tc.wantReset, tc.exhausted)
			}
		})
	}
}

func TestPickCodexOrdersBySecondaryWindow(t *testing.T) {
	codex := func(id string, secondaryAfter, primaryAfter time.Duration) *coreauth.Auth {
		return &coreauth.Auth{ID: id, Provider: "codex", Quota: coreauth.QuotaState{ObservedAt: testNow, Signals: map[string]string{
			"X-Codex-Primary-Window-Minutes":        "300",
			"X-Codex-Primary-Reset-After-Seconds":   strconv.Itoa(int(primaryAfter.Seconds())),
			"X-Codex-Secondary-Window-Minutes":      "10080",
			"X-Codex-Secondary-Reset-After-Seconds": strconv.Itoa(int(secondaryAfter.Seconds())),
		}}}
	}
	// a's 5h window resets first, but b's week resets first: b wins.
	auths := []*coreauth.Auth{codex("a", 96*time.Hour, time.Minute), codex("b", 20*time.Hour, 4*time.Hour)}
	if got := pickSequence(t, newTestSelector(), "codex", 2, auths); got[0] != "b" || got[1] != "b" {
		t.Fatalf("picks = %v, want [b b]", got)
	}
}

func TestPickPrefersCodexWebsocketCredentials(t *testing.T) {
	soonNoWS := &coreauth.Auth{ID: "a", Provider: "codex", Quota: coreauth.QuotaState{Signals: map[string]string{
		"X-Codex-Secondary-Reset-At": unix(testNow.Add(time.Hour)),
	}}}
	lateWS := &coreauth.Auth{ID: "b", Provider: "codex", Attributes: map[string]string{"websockets": "true"}, Quota: coreauth.QuotaState{Signals: map[string]string{
		"X-Codex-Secondary-Reset-At": unix(testNow.Add(90 * time.Hour)),
	}}}
	auths := []*coreauth.Auth{soonNoWS, lateWS}
	s := newTestSelector()

	ws := cliproxyexecutor.WithDownstreamWebsocket(context.Background())
	got, err := s.Pick(ws, "codex", "m", cliproxyexecutor.Options{}, auths)
	if err != nil || got.ID != "b" {
		t.Fatalf("websocket pick = %v, %v; want b", got, err)
	}
	got, err = s.Pick(context.Background(), "codex", "m", cliproxyexecutor.Options{}, auths)
	if err != nil || got.ID != "a" {
		t.Fatalf("http pick = %v, %v; want a", got, err)
	}
}

func TestPickWithoutCandidates(t *testing.T) {
	if _, err := newTestSelector().Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, nil); err == nil {
		t.Fatal("Pick with no candidates returned no error")
	}
}

func TestIsStrategy(t *testing.T) {
	for _, raw := range []string{"soonest-reset-first", " Soonest_Reset_First ", "srf", "soonestresetfirst"} {
		if !IsStrategy(raw) {
			t.Errorf("IsStrategy(%q) = false", raw)
		}
	}
	for _, raw := range []string{"", "round-robin", "fill-first", "soonest"} {
		if IsStrategy(raw) {
			t.Errorf("IsStrategy(%q) = true", raw)
		}
	}
}
