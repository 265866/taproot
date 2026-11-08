package ingest

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	defaultRange    = 24 * time.Hour
	defaultLimit    = 100
	maxLimit        = 1000
	maxBucketCount  = 20_000
	noneGroupKey    = "(none)"
	timeFormat      = time.RFC3339Nano
	codeBadRequest  = "bad_request"
	codeInternal    = "internal"
	sessionsMaxRows = 50_000
)

type apiErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(c *gin.Context, status int, code, message string) {
	var body apiErrorBody
	body.Error.Code = code
	body.Error.Message = message
	c.AbortWithStatusJSON(status, body)
}

func badRequest(c *gin.Context, format string, args ...any) {
	writeError(c, http.StatusBadRequest, codeBadRequest, fmt.Sprintf(format, args...))
}

func internalError(c *gin.Context, err error) {
	log.WithError(err).WithField("path", c.FullPath()).Error("taproot ingest: request failed")
	writeError(c, http.StatusInternalServerError, codeInternal, "internal error")
}

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func formatMs(v int64) string { return formatTime(time.UnixMilli(v)) }

func formatNullableMs(v *int64) *string {
	if v == nil {
		return nil
	}
	s := formatMs(*v)
	return &s
}

// parseRange reads from/to (RFC 3339). Missing to means now; missing from
// means to minus span.
func (i *Ingest) parseRange(c *gin.Context, span time.Duration) (time.Time, time.Time, bool) {
	to := i.now().UTC()
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			badRequest(c, "to: want an RFC 3339 time")
			return time.Time{}, time.Time{}, false
		}
		to = t.UTC()
	}
	from := to.Add(-span)
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			badRequest(c, "from: want an RFC 3339 time")
			return time.Time{}, time.Time{}, false
		}
		from = t.UTC()
	}
	if !from.Before(to) {
		badRequest(c, "from must be before to")
		return time.Time{}, time.Time{}, false
	}
	return from, to, true
}

func parseLimit(c *gin.Context) (int, bool) {
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return defaultLimit, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxLimit {
		badRequest(c, "limit: want an integer from 1 to %d", maxLimit)
		return 0, false
	}
	return n, true
}

// cursor is an opaque "<ms>:<tiebreak>" pair for descending pagination.
func encodeCursor(ts int64, tiebreak string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(ts, 10) + ":" + tiebreak))
}

func decodeCursor(raw string) (int64, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, "", errors.New("malformed cursor")
	}
	tsPart, rest, ok := strings.Cut(string(b), ":")
	if !ok {
		return 0, "", errors.New("malformed cursor")
	}
	ts, err := strconv.ParseInt(tsPart, 10, 64)
	if err != nil {
		return 0, "", errors.New("malformed cursor")
	}
	return ts, rest, nil
}

func (i *Ingest) priceBook(c *gin.Context) (*priceBook, bool) {
	prices, err := loadPrices(c.Request.Context(), i.db)
	if err != nil {
		internalError(c, err)
		return nil, false
	}
	return newPriceBook(prices), true
}

// --- accounts ---

type accountJSON struct {
	AuthIndex string  `json:"authIndex"`
	Provider  string  `json:"provider"`
	Label     string  `json:"label"`
	Email     string  `json:"email"`
	Plan      string  `json:"plan"`
	Disabled  bool    `json:"disabled"`
	FirstSeen *string `json:"firstSeen"`
	LastSeen  *string `json:"lastSeen"`
	// Removed accounts no longer exist in the auth manager. They stay listed
	// so historical usage keeps its labels, and quota views leave them out.
	Removed   bool    `json:"removed"`
	RemovedAt *string `json:"removedAt"`
}

func (i *Ingest) handleAccounts(c *gin.Context) {
	rows, err := i.db.QueryContext(c.Request.Context(), `SELECT auth_index, provider, label, email, plan, disabled, first_seen, last_seen, removed_at
		FROM accounts ORDER BY provider, label, auth_index`)
	if err != nil {
		internalError(c, err)
		return
	}
	defer closeRows(rows)
	out := []accountJSON{}
	for rows.Next() {
		var (
			a                    accountJSON
			disabled             int
			first, last, removed *int64
		)
		if err := rows.Scan(&a.AuthIndex, &a.Provider, &a.Label, &a.Email, &a.Plan, &disabled, &first, &last, &removed); err != nil {
			internalError(c, err)
			return
		}
		a.Disabled = disabled != 0
		a.FirstSeen, a.LastSeen = formatNullableMs(first), formatNullableMs(last)
		a.Removed, a.RemovedAt = removed != nil, formatNullableMs(removed)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"accounts": out})
}

// --- prices ---

func (i *Ingest) handleGetPrices(c *gin.Context) {
	prices, err := loadPrices(c.Request.Context(), i.db)
	if err != nil {
		internalError(c, err)
		return
	}
	if prices == nil {
		prices = []Price{}
	}
	c.JSON(http.StatusOK, gin.H{"prices": prices})
}

func (i *Ingest) handlePutPrices(c *gin.Context) {
	var body struct {
		Prices *[]Price `json:"prices"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		badRequest(c, "body: want {\"prices\": [...]}")
		return
	}
	if body.Prices == nil {
		badRequest(c, "body: prices is required")
		return
	}
	if err := validatePrices(*body.Prices); err != nil {
		badRequest(c, "%s", err.Error())
		return
	}
	if err := replacePrices(c.Request.Context(), i.db.DB, *body.Prices, i.now()); err != nil {
		internalError(c, err)
		return
	}
	i.handleGetPrices(c)
}

func (i *Ingest) handleResetPrices(c *gin.Context) {
	if err := resetPrices(c.Request.Context(), i.db.DB, i.now()); err != nil {
		internalError(c, err)
		return
	}
	i.handleGetPrices(c)
}
