package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/265866/taproot/internal/auth/codex"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/265866/taproot/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

// These match the dashboard's quota adapters (web/src/utils/quota/constants.ts).
const (
	claudeUsageURL  = "https://api.anthropic.com/api/oauth/usage"
	codexUsageURL   = "https://chatgpt.com/backend-api/wham/usage"
	claudeUserAgent = "claude-cli/2.1.280 (external, cli)"
	codexUserAgent  = "codex-tui/0.149.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.149.1)"
	// pollTimeout bounds a background read-only probe; it never applies to
	// proxied traffic.
	pollTimeout = 30 * time.Second
	maxPollBody = 1 << 20
)

// refreshAccounts copies labels, emails, plans, and disabled flags from the
// auth manager. Accounts the manager no longer knows keep their last values,
// so historical rows stay labelled, and are marked removed so quota views
// stop counting them. An empty list is treated as not loaded yet.
func (i *Ingest) refreshAccounts(ctx context.Context) error {
	auths := i.authList()
	if len(auths) == 0 {
		return nil
	}
	now := i.now().UnixMilli()
	return withImmediateTx(ctx, i.db.DB, func(c *sql.Conn) error {
		present := map[string]bool{}
		for _, a := range auths {
			if a == nil {
				continue
			}
			idx := a.EnsureIndex()
			if idx == "" {
				continue
			}
			present[idx] = true
			email := authEmail(a)
			label := firstNonEmpty(a.Label, email, strings.TrimSuffix(filepath.Base(a.FileName), filepath.Ext(a.FileName)))
			if _, err := c.ExecContext(ctx, `INSERT INTO accounts (auth_index, provider, label, email, plan, disabled) VALUES (?,?,?,?,?,?)
				ON CONFLICT(auth_index) DO UPDATE SET provider=excluded.provider, label=excluded.label,
					email=excluded.email, plan=CASE WHEN excluded.plan='' THEN accounts.plan ELSE excluded.plan END,
					disabled=excluded.disabled, removed_at=NULL`,
				idx, strings.ToLower(a.Provider), label, email, authPlan(a), boolInt(a.Disabled)); err != nil {
				return fmt.Errorf("upsert account %s: %w", idx, err)
			}
		}
		if len(present) == 0 {
			return nil
		}
		rows, err := c.QueryContext(ctx, `SELECT auth_index FROM accounts WHERE removed_at IS NULL`)
		if err != nil {
			return fmt.Errorf("list accounts: %w", err)
		}
		var gone []string
		for rows.Next() {
			var idx string
			if err := rows.Scan(&idx); err != nil {
				closeRows(rows)
				return fmt.Errorf("scan account: %w", err)
			}
			if !present[idx] {
				gone = append(gone, idx)
			}
		}
		closeRows(rows)
		if err := rows.Err(); err != nil {
			return fmt.Errorf("list accounts: %w", err)
		}
		for _, idx := range gone {
			if _, err := c.ExecContext(ctx, `UPDATE accounts SET removed_at = ? WHERE auth_index = ?`, now, idx); err != nil {
				return fmt.Errorf("mark account %s removed: %w", idx, err)
			}
		}
		return nil
	})
}

func authEmail(a *coreauth.Auth) string {
	if v, ok := a.Metadata["email"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return firstNonEmpty(a.Attributes["email"], a.Attributes["account_email"])
}

func authPlan(a *coreauth.Auth) string {
	if v := firstNonEmpty(a.Attributes["plan_type"]); v != "" {
		return v
	}
	if v, ok := a.Metadata["plan_type"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if idToken, ok := a.Metadata["id_token"].(string); ok && strings.EqualFold(a.Provider, "codex") {
		if claims, err := codex.ParseJWTToken(idToken); err == nil && claims != nil {
			return strings.TrimSpace(claims.CodexAuthInfo.ChatgptPlanType)
		}
	}
	return ""
}

func accessToken(a *coreauth.Auth) string {
	if v, ok := a.Metadata["access_token"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// pollIdle reads the usage endpoint for every enabled Claude or Codex account
// that has no quota observation, and no poll attempt, within the interval.
// It only reads the current access token; refreshing stays with the auth
// manager.
func (i *Ingest) pollIdle(ctx context.Context) {
	now := i.now()
	for _, a := range i.authList() {
		if ctx.Err() != nil {
			return
		}
		if a == nil || a.Disabled {
			continue
		}
		provider := strings.ToLower(strings.TrimSpace(a.Provider))
		if provider != "claude" && provider != "codex" {
			continue
		}
		idx := a.EnsureIndex()
		if idx == "" || now.Sub(i.lastPoll[idx]) < i.pollInterval {
			continue
		}
		last, err := i.lastObservation(ctx, idx)
		if err != nil {
			log.WithError(err).Warn("taproot ingest: read last quota observation")
			continue
		}
		if !last.IsZero() && now.Sub(last) < i.pollInterval {
			continue
		}
		i.lastPoll[idx] = now
		obs, err := i.pollAccount(ctx, a, provider, now)
		if err != nil {
			log.WithError(err).WithField("auth_index", idx).Debug("taproot ingest: quota poll failed")
			continue
		}
		for k := range obs {
			obs[k].AuthIndex = idx
		}
		i.enqueueObservations(obs)
	}
}

func (i *Ingest) lastObservation(ctx context.Context, authIndex string) (time.Time, error) {
	var last sql.NullInt64
	err := i.db.QueryRowContext(ctx, `SELECT MAX(observed_at) FROM quota_observations WHERE auth_index=?`, authIndex).Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	if !last.Valid {
		return time.Time{}, nil
	}
	return time.UnixMilli(last.Int64).UTC(), nil
}

func (i *Ingest) pollAccount(ctx context.Context, a *coreauth.Auth, provider string, now time.Time) ([]observation, error) {
	token := accessToken(a)
	if token == "" {
		return nil, errors.New("no access token")
	}
	url := i.claudeUsageURL
	if provider == "codex" {
		url = i.codexUsageURL
	}
	reqCtx, cancel := context.WithTimeout(ctx, pollTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if provider == "claude" {
		req.Header.Set("User-Agent", claudeUserAgent)
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		req.Header.Set("User-Agent", codexUserAgent)
		if id, ok := a.Metadata["account_id"].(string); ok && strings.TrimSpace(id) != "" {
			req.Header.Set("Chatgpt-Account-Id", strings.TrimSpace(id))
		}
	}
	client := &http.Client{Transport: pollTransport(a)}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request usage: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Debug("taproot ingest: close poll body")
		}
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPollBody))
	if err != nil {
		return nil, fmt.Errorf("read usage: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage endpoint status %d", resp.StatusCode)
	}
	observedAt := now.UTC()
	if provider == "claude" {
		return parseClaudeUsagePayload(body, observedAt), nil
	}
	return parseCodexUsagePayload(body, observedAt), nil
}

func pollTransport(a *coreauth.Auth) http.RoundTripper {
	if proxy := strings.TrimSpace(a.ProxyURL); proxy != "" {
		if t, _, err := proxyutil.BuildHTTPTransport(proxy); err == nil && t != nil {
			return t
		}
		log.WithField("auth_index", a.Index).Warn("taproot ingest: invalid auth proxy URL, polling directly")
	}
	return http.DefaultTransport
}
