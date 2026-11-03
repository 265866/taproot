package settings

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/alerts"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/ingest"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
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
	db, err := store.Open(context.Background(), store.Config{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := alerts.New(alerts.Config{}, db, noQuota{})
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
