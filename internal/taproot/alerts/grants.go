package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/265866/taproot/internal/taproot/egress"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// These mirror the dashboard's read in web/src/services/api/claudeResetGrants.ts.
// The read is a GET of the usage endpoint; nothing here redeems a grant.
const (
	grantStatusURL  = "https://api.anthropic.com/api/oauth/usage?cedar_ember=1&skip_spend=1"
	claudeUserAgent = "claude-cli/2.1.280 (external, cli)"
	// grantTimeout bounds the background read-only probe, like ingest's poller.
	grantTimeout = 30 * time.Second
	maxGrantBody = 1 << 20
)

var grantIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

// parseGrantStatus reads the cedar_ember block. An ineligible account has no
// usable grants. Malformed grants are skipped rather than failing the read.
func parseGrantStatus(body []byte) ([]Grant, error) {
	var payload struct {
		CedarEmber *struct {
			Eligible *bool `json:"eligible"`
			Grants   []struct {
				ID         string  `json:"id"`
				Label      string  `json:"label"`
				ResetsLeft *int    `json:"resets_left"`
				EndsAt     *string `json:"ends_at"`
			} `json:"grants"`
		} `json:"cedar_ember"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("malformed usage response")
	}
	block := payload.CedarEmber
	if block == nil || block.Eligible == nil {
		return nil, errors.New("usage response has no cedar_ember block")
	}
	if !*block.Eligible {
		return nil, nil
	}
	var out []Grant
	for _, g := range block.Grants {
		if !grantIDPattern.MatchString(g.ID) || g.ResetsLeft == nil || *g.ResetsLeft < 0 {
			continue
		}
		grant := Grant{ID: g.ID, Label: safeLabel(g.Label), ResetsLeft: *g.ResetsLeft}
		if g.EndsAt != nil {
			t, err := time.Parse(time.RFC3339, *g.EndsAt)
			if err != nil {
				continue
			}
			grant.EndsAt = t.UTC()
		}
		out = append(out, grant)
	}
	return out, nil
}

var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, `*`, `\*`, `_`, `\_`, "`", "\\`", `~`, `\~`, `|`, `\|`, `>`, `\>`, `<`, `\<`, `#`, `\#`, `[`, `\[`, `]`, `\]`,
)

// safeLabel makes upstream or user text inert inside an embed: no control
// characters, no markdown, bounded length.
func safeLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)
	return markdownEscaper.Replace(truncate(strings.Join(strings.Fields(s), " "), 120))
}

type grantReader struct {
	url   string
	proxy *egress.Proxy
}

// read fetches one Claude account's grants with its current access token.
// Refreshing stays with the auth manager.
func (g grantReader) read(ctx context.Context, a *coreauth.Auth) ([]Grant, error) {
	token, _ := a.Metadata["access_token"].(string)
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("no access token")
	}
	ctx, cancel := context.WithTimeout(ctx, grantTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", claudeUserAgent)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	client, err := g.proxy.Client(a)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request grants: %s", withoutURL(err))
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Debug("taproot alerts: close grant body")
		}
	}()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxGrantBody))
	if err != nil {
		return nil, fmt.Errorf("read grants: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage endpoint status %d", resp.StatusCode)
	}
	return parseGrantStatus(body)
}
