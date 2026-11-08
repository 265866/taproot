package settings

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/alerts"
	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/265866/taproot/internal/taproot/store"
)

type noQuota struct{}

func (noQuota) OnQuota(func([]ingest.QuotaObservation)) {}
func (noQuota) LatestQuota(context.Context) ([]ingest.QuotaObservation, error) {
	return nil, nil
}

const (
	hookToken = "tokenTAIL-abc_987654"
	hookURL   = "https://discord.com/api/webhooks/1234567890/" + hookToken
)

func withAlerts(t *testing.T, f *fixture) *alerts.Service {
	t.Helper()
	return withAlertsConfig(t, f, alerts.Config{})
}

func withAlertsConfig(t *testing.T, f *fixture, cfg alerts.Config) *alerts.Service {
	t.Helper()
	db, err := store.Open(context.Background(), store.Config{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := alerts.New(cfg, db, noQuota{})
	if err != nil {
		t.Fatal(err)
	}
	f.s.deps.Alerts = svc
	return svc
}

func TestAlertsWebhookSetMaskedPersistedAndCleared(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	svc := withAlerts(t, f)

	code, v, body := f.do(t, http.MethodGet, "")
	if code != http.StatusOK || v.Alerts == nil || v.Alerts.WebhookSet || !v.Alerts.Enabled || v.Alerts.Active {
		t.Fatalf("GET = %d %s", code, body)
	}

	code, v, body = f.do(t, http.MethodPut, `{"alerts": {"discordWebhook": " `+hookURL+` "}}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if strings.Contains(body, hookToken) || v.Alerts.WebhookHint != "…987654" || !v.Alerts.WebhookSet || !v.Alerts.Active {
		t.Fatalf("PUT response = %s", body)
	}
	if svc.Config().DiscordWebhook != hookURL {
		t.Fatal("the live webhook did not change")
	}
	file := f.file(t)
	if !strings.Contains(file, "discord-webhook: "+hookURL) || !strings.Contains(file, "# taproot settings") {
		t.Fatalf("taproot.yaml = %s", file)
	}

	code, v, body = f.do(t, http.MethodPut, `{"alerts": {"enabled": false}}`)
	if code != http.StatusOK || v.Alerts.Enabled || v.Alerts.Active || !v.Alerts.WebhookSet {
		t.Fatalf("disable = %d %s", code, body)
	}
	if !strings.Contains(f.file(t), "enabled: false") {
		t.Fatalf("taproot.yaml = %s", f.file(t))
	}

	code, v, body = f.do(t, http.MethodPut, `{"alerts": {"discordWebhook": ""}}`)
	if code != http.StatusOK || v.Alerts.WebhookSet || v.Alerts.WebhookHint != "" || svc.Config().DiscordWebhook != "" {
		t.Fatalf("clear = %d %s", code, body)
	}
	if strings.Contains(f.file(t), hookToken) {
		t.Fatalf("cleared webhook is still in taproot.yaml: %s", f.file(t))
	}
}

func TestAlertsRejectsOtherURLsWithoutEchoingThem(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	withAlerts(t, f)
	before := f.file(t)
	code, _, body := f.do(t, http.MethodPut, `{"alerts": {"discordWebhook": "https://evil.test/api/webhooks/1/`+hookToken+`"}}`)
	if code != http.StatusBadRequest || errorCode(t, body) != "invalid_alerts" || strings.Contains(body, hookToken) {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if f.file(t) != before {
		t.Fatal("taproot.yaml changed after a refused update")
	}
}

func TestAlertsChangeRollsBackWithTheRestOfTheUpdate(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	svc := withAlerts(t, f)
	code, _, body := f.do(t, http.MethodPut, `{"alerts": {"discordWebhook": "`+hookURL+`"}, "ingest": {"pollInterval": "soon"}}`)
	if code != http.StatusBadRequest {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if svc.Config().DiscordWebhook != "" {
		t.Fatal("the webhook stayed set after the update failed")
	}
}

// TestAlertsInvalidWebhookFromFileIsShownAndReplaceable covers a typo in a
// hand-edited taproot.yaml: settings say why alerts are off, and clearing
// or replacing the webhook fixes it.
func TestAlertsInvalidWebhookFromFileIsShownAndReplaceable(t *testing.T) {
	bad := "https://discord.com/api/webhoks/1/" + hookToken
	f := newFixture(t, options{yaml: commentedYAML + "alerts:\n  discord-webhook: " + bad + "\n"})
	svc := withAlertsConfig(t, f, alerts.Config{DiscordWebhook: bad})

	code, v, body := f.do(t, http.MethodGet, "")
	if code != http.StatusOK || v.Alerts == nil || !v.Alerts.WebhookInvalid || v.Alerts.Active || strings.Contains(body, hookToken) {
		t.Fatalf("GET = %d %s", code, body)
	}

	code, v, body = f.do(t, http.MethodPut, `{"alerts": {"discordWebhook": ""}}`)
	if code != http.StatusOK || v.Alerts.WebhookInvalid || v.Alerts.WebhookSet {
		t.Fatalf("clear = %d %s", code, body)
	}
	if strings.Contains(f.file(t), hookToken) {
		t.Fatalf("the invalid webhook is still in taproot.yaml: %s", f.file(t))
	}

	code, v, body = f.do(t, http.MethodPut, `{"alerts": {"discordWebhook": "`+hookURL+`"}}`)
	if code != http.StatusOK || v.Alerts.WebhookInvalid || !v.Alerts.Active || svc.Config().DiscordWebhook != hookURL {
		t.Fatalf("replace = %d %s", code, body)
	}
}

// announceCounter counts the "Webhook connected" requests settings make.
type announceCounter struct {
	*alerts.Service
	calls int
}

func (a *announceCounter) AnnounceWebhook() {
	a.calls++
	a.Service.AnnounceWebhook()
}

// TestAlertsAnnounceOnlyANewSavedWebhook checks that settings ask for the
// "Webhook connected" message once per new webhook, and only after
// taproot.yaml holds it.
func TestAlertsAnnounceOnlyANewSavedWebhook(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	counter := &announceCounter{Service: withAlerts(t, f)}
	f.s.deps.Alerts = counter
	other := "https://discord.com/api/webhooks/42/otherTAIL-xyz"

	for _, step := range []struct {
		body  string
		code  int
		calls int
	}{
		{`{"alerts": {"discordWebhook": "` + hookURL + `"}, "ingest": {"pollInterval": "soon"}}`, http.StatusBadRequest, 0},
		{`{"alerts": {"discordWebhook": "` + hookURL + `"}}`, http.StatusOK, 1},
		{`{"alerts": {"discordWebhook": " ` + hookURL + ` "}}`, http.StatusOK, 1},
		{`{"alerts": {"enabled": false}}`, http.StatusOK, 1},
		{`{"alerts": {"enabled": true}}`, http.StatusOK, 1},
		{`{"alerts": {"discordWebhook": "` + other + `"}}`, http.StatusOK, 2},
		{`{"alerts": {"discordWebhook": ""}}`, http.StatusOK, 2},
	} {
		code, _, body := f.do(t, http.MethodPut, step.body)
		if code != step.code || counter.calls != step.calls {
			t.Fatalf("PUT %s = %d %s, announcements = %d; want %d and %d", step.body, code, body, counter.calls, step.code, step.calls)
		}
	}
}

// TestAlertsConnectedMessageDoesNotHoldTheRequest saves a webhook while the
// fake Discord holds every post: the PUT returns, and the message arrives.
func TestAlertsConnectedMessageDoesNotHoldTheRequest(t *testing.T) {
	posts := make(chan string, 1)
	release := make(chan struct{})
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posts <- r.URL.RequestURI() + " " + string(body)
		<-release
	}))
	t.Cleanup(discord.Close)
	t.Cleanup(func() { close(release) })
	t.Setenv(alerts.OriginEnv, discord.URL)
	f := newFixture(t, options{yaml: commentedYAML})
	svc := withAlerts(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)

	if code, _, body := f.do(t, http.MethodPut, `{"alerts": {"discordWebhook": "`+hookURL+`"}}`); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	select {
	case got := <-posts:
		if !strings.HasPrefix(got, "/api/webhooks/1234567890/"+hookToken+"?wait=true&with_components=true ") || !strings.Contains(got, `"content":"### Webhook connected"`) {
			t.Fatalf("post = %s", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no connected message reached the fake Discord")
	}
}
