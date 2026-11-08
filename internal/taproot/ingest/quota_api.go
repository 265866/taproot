package ingest

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

type quotaWindowJSON struct {
	Window        string  `json:"window"`
	Utilization   float64 `json:"utilization"`
	ResetsAt      *string `json:"resetsAt"`
	Status        string  `json:"status"`
	ObservedAt    string  `json:"observedAt"`
	PeriodSeconds int64   `json:"periodSeconds"`
	// AccountWide is false for model- and feature-scoped windows, which do not
	// limit the account's other traffic.
	AccountWide bool `json:"accountWide"`
}

type quotaAccountJSON struct {
	AuthIndex string            `json:"authIndex"`
	Provider  string            `json:"provider"`
	Windows   []quotaWindowJSON `json:"windows"`
}

// handleQuotaCurrent returns the latest observation of every window, plus
// enabled Claude and Codex accounts that have none yet. Accounts whose
// credential was removed are left out.
func (i *Ingest) handleQuotaCurrent(c *gin.Context) {
	ctx := c.Request.Context()
	byAccount, err := i.currentWindows(ctx, "")
	if err != nil {
		internalError(c, err)
		return
	}
	acct, err := i.db.QueryContext(ctx, `SELECT auth_index, provider FROM accounts WHERE disabled = 0 AND removed_at IS NULL AND provider IN ('claude', 'codex')`)
	if err != nil {
		internalError(c, err)
		return
	}
	for acct.Next() {
		var authIndex, provider string
		if err := acct.Scan(&authIndex, &provider); err != nil {
			closeRows(acct)
			internalError(c, err)
			return
		}
		if byAccount[authIndex] == nil {
			byAccount[authIndex] = &quotaAccountJSON{AuthIndex: authIndex, Provider: provider, Windows: []quotaWindowJSON{}}
		}
	}
	closeRows(acct)
	if err := acct.Err(); err != nil {
		internalError(c, err)
		return
	}
	out := make([]quotaAccountJSON, 0, len(byAccount))
	for _, a := range byAccount {
		out = append(out, *a)
	}
	sort.Slice(out, func(x, y int) bool {
		if out[x].Provider != out[y].Provider {
			return out[x].Provider < out[y].Provider
		}
		return out[x].AuthIndex < out[y].AuthIndex
	})
	c.JSON(http.StatusOK, gin.H{"accounts": out})
}

// currentWindows reads the latest observation of every window, for one
// account or, with an empty authIndex, for every account not removed.
func (i *Ingest) currentWindows(ctx context.Context, authIndex string) (map[string]*quotaAccountJSON, error) {
	// A loose index scan: each account, then each of its windows, then the
	// window's latest row are one seek each on quota_observations_key, so the
	// cost follows the number of windows rather than the length of history.
	auths := `SELECT MIN(auth_index) FROM quota_observations
		UNION ALL
		SELECT (SELECT MIN(auth_index) FROM quota_observations WHERE auth_index > a) FROM auths WHERE a IS NOT NULL`
	var args []any
	if authIndex != "" {
		auths = `SELECT ?`
		args = append(args, authIndex)
	}
	rows, err := i.db.QueryContext(ctx, `WITH RECURSIVE
		auths(a) AS (`+auths+`),
		windows(a, w) AS (
			SELECT a, (SELECT MIN(window_name) FROM quota_observations WHERE auth_index = a) FROM auths WHERE a IS NOT NULL
			UNION ALL
			SELECT a, (SELECT MIN(window_name) FROM quota_observations WHERE auth_index = a AND window_name > w) FROM windows WHERE w IS NOT NULL)
		SELECT q.auth_index, q.provider, q.window_name, q.utilization, q.resets_at, q.status, q.period_seconds, q.observed_at
		FROM windows JOIN quota_observations q ON q.id = (SELECT id FROM quota_observations
			WHERE auth_index = a AND window_name = w ORDER BY observed_at DESC LIMIT 1)
		WHERE w IS NOT NULL AND a NOT IN (SELECT auth_index FROM accounts WHERE removed_at IS NOT NULL)`, args...)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	byAccount := map[string]*quotaAccountJSON{}
	for rows.Next() {
		var (
			w                   quotaWindowJSON
			authIndex, provider string
			resetsAt            *int64
			observed            int64
		)
		if err := rows.Scan(&authIndex, &provider, &w.Window, &w.Utilization, &resetsAt, &w.Status, &w.PeriodSeconds, &observed); err != nil {
			return nil, err
		}
		w.ResetsAt, w.ObservedAt = formatNullableMs(resetsAt), formatMs(observed)
		w.AccountWide = accountWideWindow(w.Window)
		a := byAccount[authIndex]
		if a == nil {
			a = &quotaAccountJSON{AuthIndex: authIndex, Provider: provider, Windows: []quotaWindowJSON{}}
			byAccount[authIndex] = a
		}
		a.Windows = append(a.Windows, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, a := range byAccount {
		sort.Slice(a.Windows, func(x, y int) bool { return a.Windows[x].Window < a.Windows[y].Window })
	}
	return byAccount, nil
}

type quotaPointJSON struct {
	T           string  `json:"t"`
	Utilization float64 `json:"utilization"`
}

type quotaSeriesJSON struct {
	AuthIndex     string           `json:"authIndex"`
	Provider      string           `json:"provider"`
	Window        string           `json:"window"`
	PeriodSeconds int64            `json:"periodSeconds"`
	Points        []quotaPointJSON `json:"points"`
	Resets        []string         `json:"resets"`
	AccountWide   bool             `json:"accountWide"`
}

// handleQuotaSeries returns every stored observation in range per account
// window, and the distinct reset times that fall inside the range.
func (i *Ingest) handleQuotaSeries(c *gin.Context) {
	from, to, ok := i.parseRange(c, defaultRange)
	if !ok {
		return
	}
	where := "observed_at >= ? AND observed_at < ? AND auth_index NOT IN (SELECT auth_index FROM accounts WHERE removed_at IS NOT NULL)"
	args := []any{ms(from), ms(to)}
	if p := strings.ToLower(strings.TrimSpace(c.Query("provider"))); p != "" {
		where += " AND provider = ?"
		args = append(args, p)
	}
	rows, err := i.db.QueryContext(c.Request.Context(), `SELECT auth_index, provider, window_name, period_seconds, observed_at, utilization, resets_at
		FROM quota_observations WHERE `+where+` ORDER BY auth_index, window_name, observed_at`, args...)
	if err != nil {
		internalError(c, err)
		return
	}
	defer closeRows(rows)
	out := []quotaSeriesJSON{}
	var cur *quotaSeriesJSON
	resetSeen := map[int64]bool{}
	fromMs, toMs := ms(from), ms(to)
	for rows.Next() {
		var (
			authIndex, provider, window string
			period, observed            int64
			util                        float64
			resetsAt                    *int64
		)
		if err := rows.Scan(&authIndex, &provider, &window, &period, &observed, &util, &resetsAt); err != nil {
			internalError(c, err)
			return
		}
		if cur == nil || cur.AuthIndex != authIndex || cur.Window != window {
			out = append(out, quotaSeriesJSON{AuthIndex: authIndex, Provider: provider, Window: window, Points: []quotaPointJSON{}, Resets: []string{}, AccountWide: accountWideWindow(window)})
			cur = &out[len(out)-1]
			resetSeen = map[int64]bool{}
		}
		if period > cur.PeriodSeconds {
			cur.PeriodSeconds = period
		}
		cur.Points = append(cur.Points, quotaPointJSON{T: formatMs(observed), Utilization: util})
		if resetsAt != nil && *resetsAt >= fromMs && *resetsAt < toMs && !resetSeen[*resetsAt/60000] {
			resetSeen[*resetsAt/60000] = true
			cur.Resets = append(cur.Resets, formatMs(*resetsAt))
		}
	}
	if err := rows.Err(); err != nil {
		internalError(c, err)
		return
	}
	for k := range out {
		sort.Strings(out[k].Resets)
	}
	c.JSON(http.StatusOK, gin.H{"series": out})
}
