// Package alerts posts Discord messages when an account hits a quota limit, a
// provider's pool runs empty, a login breaks, or a Claude reset grant is about
// to expire with resets left. It reads quota observations from ingest and
// credential state from the auth manager in-process, and never blocks the proxy.
package alerts

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Config is the alerts section of taproot.yaml.
type Config struct {
	// DiscordWebhook is a Discord webhook URL. It is a secret: it is never
	// logged and the API only shows its last characters.
	DiscordWebhook string `yaml:"discord-webhook"`
	// Enabled turns alerts off without forgetting the webhook. Unset means on.
	Enabled *bool `yaml:"enabled"`
}

// Active reports whether alerts are sent.
func (c Config) Active() bool {
	return c.DiscordWebhook != "" && (c.Enabled == nil || *c.Enabled)
}

var (
	webhookHosts = map[string]bool{
		"discord.com": true, "discordapp.com": true,
		"ptb.discord.com": true, "canary.discord.com": true,
	}
	webhookPath = regexp.MustCompile(`^/api/(v\d+/)?webhooks/\d+/[A-Za-z0-9_-]+$`)
)

// ErrInvalidWebhook is returned for anything but a Discord webhook URL.
var ErrInvalidWebhook = errors.New("want a Discord webhook URL such as https://discord.com/api/webhooks/<id>/<token>")

// ValidateWebhook accepts https://discord.com/api/webhooks/<id>/<token> and
// its discordapp.com, ptb, and canary forms. The error never echoes the URL.
func ValidateWebhook(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" || u.RawQuery != "" {
		return ErrInvalidWebhook
	}
	if !webhookHosts[strings.ToLower(u.Hostname())] || !webhookPath.MatchString(u.Path) {
		return ErrInvalidWebhook
	}
	return nil
}

// MaskWebhook shows only the last six characters of a webhook URL.
func MaskWebhook(raw string) string {
	if raw == "" {
		return ""
	}
	if len(raw) <= 6 {
		return "…"
	}
	return "…" + raw[len(raw)-6:]
}
