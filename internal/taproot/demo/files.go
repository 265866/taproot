package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/265866/taproot/internal/auth/claude"
	"github.com/265866/taproot/internal/auth/codex"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
)

// deadProxy is a loopback address nothing listens on. Every outbound request
// the server makes goes through it and fails at once.
const deadProxy = "http://127.0.0.1:9"

// configYAML keeps the demo on loopback and offline: outbound requests go to
// the dead proxy, and the management panel is never downloaded.
const configYAML = `# Demo server: loopback only, and every outbound request fails at a dead proxy.
config-version: 8
server:
    host: "127.0.0.1"
    port: %d
access: {}
routing:
    strategy: "soonest-reset-first"
    session-affinity: true
    session-affinity-ttl: "1h"
requests:
    proxy-url: %q
observability:
    logs:
        request-log: true
        logging-to-file: false
management:
    allow-remote: true
    disable-control-panel: true
    disable-auto-update-panel: true
oauth:
    auth-dir: %q
`

// taprootYAML turns off the idle-account poller, which reads from providers.
// Alerts are on with a fake webhook, so the Dashboard shows failing
// deliveries: run.sh points their origin, like every other outbound
// request, at the dead loopback proxy. The tailscaled socket is the demo's
// fake tailnet (serveTailnet), never a real one; the server listens on loopback
// only, so every caller is loopback and the tailnet only fills the Access
// page.
const taprootYAML = `data-dir: taproot
%singest:
  poll-interval: "0"
backups:
  daily-at: "00:00"
  keep: 14
request-logs:
  max-days: 14
  max-bytes: 20GiB
alerts:
  discord-webhook: https://discord.com/api/webhooks/000000000000000000/demo-webhook-token-not-real
`

// tailnetSocket is where the demo's fake tailscaled listens for a demo in dir.
func tailnetSocket(dir string) string {
	return filepath.Join(dir, "tailscaled.sock")
}

// writeConfig writes config.yaml and taproot.yaml into dir.
func writeConfig(dir, authDir string, port int) error {
	cfg := fmt.Sprintf(configYAML, port, deadProxy, filepath.ToSlash(authDir))
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600); err != nil {
		return fmt.Errorf("write config.yaml: %w", err)
	}
	tp := fmt.Sprintf(taprootYAML, accessYAML(filepath.ToSlash(tailnetSocket(dir))))
	if err := os.WriteFile(filepath.Join(dir, "taproot.yaml"), []byte(tp), 0o600); err != nil {
		return fmt.Errorf("write taproot.yaml: %w", err)
	}
	return nil
}

// writeAuthFiles writes one credential file per account, with tokens that are
// plainly fake, and fills in each account's file name and auth index. The
// tokens expire well after the demo runs, so the server never tries to refresh them.
func writeAuthFiles(authDir string, accounts []*demoAccount, start time.Time) error {
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}
	for i, a := range accounts {
		n := i + 1
		accountID := fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
		meta := map[string]any{
			"type":          a.provider,
			"email":         a.email,
			"access_token":  fmt.Sprintf("demo-%s-access-token-not-real-%d", a.provider, n),
			"refresh_token": fmt.Sprintf("demo-%s-refresh-token-not-real-%d", a.provider, n),
			"expired":       start.Add(10 * 24 * time.Hour).UTC().Format(time.RFC3339),
			"last_refresh":  start.UTC().Format(time.RFC3339),
			"plan_type":     a.plan,
		}
		switch a.provider {
		case "claude":
			meta["account_uuid"] = accountID
			meta["organization_uuid"] = accountID
			a.fileName = claude.CredentialFileName(a.email, accountID, "")
		case "codex":
			meta["account_id"] = accountID
			meta["id_token"] = fakeIDToken(a, accountID, start)
			a.fileName = codex.CredentialFileName(a.email, a.plan, fmt.Sprintf("%08x", n), true)
		}
		if a.signedOut > 0 {
			meta["expired"] = start.Add(-a.signedOut).UTC().Format(time.RFC3339)
			if err := claimRefresh(authDir, meta["refresh_token"].(string)); err != nil {
				return err
			}
		}
		raw, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return fmt.Errorf("encode %s: %w", a.email, err)
		}
		path := filepath.Join(authDir, a.fileName)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", a.fileName, err)
		}
		a.authIndex = authIndex(path, a.provider, meta)
	}
	return nil
}

// claimRefresh signs a Codex account out without contacting OpenAI: it
// records a claim on the refresh token in upstream's refresh journal, with
// no receipt, which is how upstream marks a refresh whose outcome is
// unknown. Its expired access token makes the server refresh at start, and
// upstream never sends a claimed token again, so the account needs sign-in.
func claimRefresh(authDir, refreshToken string) error {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(codex.ClientID+"\x00"+refreshToken)))
	if err := os.MkdirAll(filepath.Join(authDir, ".codex-refresh", key), 0o700); err != nil {
		return fmt.Errorf("claim refresh token: %w", err)
	}
	return nil
}

// authIndex is the index the server derives for a credential file: a hash of
// its type and absolute path.
func authIndex(path, provider string, meta map[string]any) string {
	auth := &coreauth.Auth{
		Provider:   provider,
		Attributes: map[string]string{coreauth.AttributePath: path},
		Metadata:   meta,
	}
	return auth.EnsureIndex()
}

// fakeIDToken is an unsigned JWT carrying only the claims taproot reads: the
// email, plan, and ChatGPT account. Its signature is the word "demo".
func fakeIDToken(a *demoAccount, accountID string, start time.Time) string {
	enc := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	claims := map[string]any{
		"email": a.email,
		"exp":   start.Add(10 * 24 * time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_plan_type":  a.plan,
			"chatgpt_account_id": accountID,
		},
	}
	return strings.Join([]string{enc(map[string]string{"alg": "none", "typ": "JWT"}), enc(claims), "demo"}, ".")
}
