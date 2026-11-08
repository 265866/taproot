package reqlog

import (
	"context"
	"database/sql"
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

// LogMeta is one row of GET /request-logs.
type LogMeta struct {
	RequestID       string `json:"requestId"`
	TraceID         string `json:"traceId,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	Timestamp       string `json:"timestamp"`
	DurationMs      int64  `json:"durationMs"`
	Method          string `json:"method"`
	URL             string `json:"url"`
	Path            string `json:"path"`
	Status          int    `json:"status"`
	Model           string `json:"model,omitempty"`
	AuthIndex       string `json:"authIndex,omitempty"`
	Streaming       bool   `json:"streaming"`
	ErrorOnly       bool   `json:"errorOnly"`
	Dropped         string `json:"dropped,omitempty"`
	RequestBytes    int64  `json:"requestBytes"`
	ResponseBytes   int64  `json:"responseBytes"`
	RecordBytes     int64  `json:"recordBytes"`
}

// Detail is GET /request-logs/:requestId.
type Detail struct {
	Meta     LogMeta        `json:"meta"`
	Request  DetailRequest  `json:"request"`
	Response DetailResponse `json:"response"`
	Upstream DetailUpstream `json:"upstream"`
}

type DetailRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

type DetailResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

// DetailUpstream holds the executor-formatted sections verbatim; each can hold
// several attempts.
type DetailUpstream struct {
	Request              string         `json:"request,omitempty"`
	Response             string         `json:"response,omitempty"`
	Errors               []apiErrorView `json:"errors,omitempty"`
	WebsocketTimeline    string         `json:"websocketTimeline,omitempty"`
	APIWebsocketTimeline string         `json:"apiWebsocketTimeline,omitempty"`
}

// Stats is GET /request-logs/stats.
type Stats struct {
	BytesOnDisk    int64   `json:"bytesOnDisk"`
	RawBytes       int64   `json:"rawBytes"`
	Oldest         *string `json:"oldest"`
	Newest         *string `json:"newest"`
	Count          int64   `json:"count"`
	Segments       int64   `json:"segments"`
	OpenSegments   int64   `json:"openSegments"`
	QueueBytes     int64   `json:"queueBytes"`
	DroppedBodies  int64   `json:"droppedBodies"`
	DroppedRecords int64   `json:"droppedRecords"`
	Truncated      int64   `json:"truncatedResponses"`
	WriteErrors    int64   `json:"writeErrors"`
}

// ListFilter selects rows for GET /request-logs.
type ListFilter struct {
	From, To  time.Time
	Query     string
	Status    string
	Model     string
	AuthIndex string
	SessionID string
	Cursor    string
	Limit     int
}

type recordLocation struct {
	segment int64
	day     string
	state   string
	dictID  int64
	offset  int64
	length  int
}

var errNotFound = errors.New("request log not found")

// filterError is a bad query parameter rather than a storage failure.
type filterError string

func (e filterError) Error() string { return string(e) }

const listColumns = `l.id, l.request_id, l.trace_id, l.session_id, l.parent_session_id, l.ts, l.duration_ms, l.method, l.url, l.path,
	l.status, l.model, l.auth_index, l.streaming, l.error_only, l.dropped, l.req_bytes, l.resp_bytes, l.text_bytes`

func scanMeta(row interface{ Scan(...any) error }) (LogMeta, int64, int64, error) {
	var m LogMeta
	var id, ts int64
	err := row.Scan(&id, &m.RequestID, &m.TraceID, &m.SessionID, &m.ParentSessionID, &ts, &m.DurationMs, &m.Method, &m.URL, &m.Path,
		&m.Status, &m.Model, &m.AuthIndex, &m.Streaming, &m.ErrorOnly, &m.Dropped, &m.RequestBytes, &m.ResponseBytes, &m.RecordBytes)
	m.Timestamp = formatTime(ts)
	return m, id, ts, err
}

func formatTime(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }

// List returns rows newest first. The cursor is opaque to callers.
func (l *Logs) List(ctx context.Context, f ListFilter) ([]LogMeta, string, error) {
	var where []string
	var args []any
	if !f.From.IsZero() {
		where, args = append(where, "l.ts >= ?"), append(args, f.From.UnixMilli())
	}
	if !f.To.IsZero() {
		where, args = append(where, "l.ts < ?"), append(args, f.To.UnixMilli())
	}
	if f.Model != "" {
		where, args = append(where, "l.model = ?"), append(args, f.Model)
	}
	if f.AuthIndex != "" {
		where, args = append(where, "l.auth_index = ?"), append(args, f.AuthIndex)
	}
	if f.SessionID != "" {
		where, args = append(where, "l.session_id = ?"), append(args, f.SessionID)
	}
	if f.Status != "" {
		clause, statusArgs, err := statusClause(f.Status)
		if err != nil {
			return nil, "", err
		}
		where, args = append(where, clause), append(args, statusArgs...)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
		where = append(where, `(l.request_id LIKE ? ESCAPE '\' OR l.trace_id LIKE ? ESCAPE '\' OR l.url LIKE ? ESCAPE '\' OR l.model LIKE ? ESCAPE '\' OR l.session_id LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like, like, like)
	}
	if f.Cursor != "" {
		ts, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		where, args = append(where, "(l.ts < ? OR (l.ts = ? AND l.id < ?))"), append(args, ts, ts, id)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 1000)
	query := "SELECT " + listColumns + " FROM reqlog_records l"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY l.ts DESC, l.id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := l.engine.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list request logs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	logs := make([]LogMeta, 0, limit)
	var lastID, lastTs int64
	next := ""
	for rows.Next() {
		m, id, ts, err := scanMeta(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan request log: %w", err)
		}
		if len(logs) == limit {
			next = encodeCursor(lastTs, lastID)
			break
		}
		logs = append(logs, m)
		lastID, lastTs = id, ts
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list request logs: %w", err)
	}
	return logs, next, nil
}

func statusClause(raw string) (string, []any, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "error" {
		return "l.status >= 400", nil, nil
	}
	if len(raw) == 3 && strings.HasSuffix(raw, "xx") && raw[0] >= '1' && raw[0] <= '5' {
		base := int(raw[0]-'0') * 100
		return "l.status >= ? AND l.status < ?", []any{base, base + 100}, nil
	}
	code, err := strconv.Atoi(raw)
	if err != nil {
		return "", nil, filterError("status must be a code, Nxx, or error")
	}
	return "l.status = ?", []any{code}, nil
}

func encodeCursor(ts, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(ts, 10) + ":" + strconv.FormatInt(id, 10)))
}

func decodeCursor(cursor string) (int64, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err == nil {
		tsPart, idPart, ok := strings.Cut(string(raw), ":")
		ts, errTs := strconv.ParseInt(tsPart, 10, 64)
		id, errID := strconv.ParseInt(idPart, 10, 64)
		if ok && errTs == nil && errID == nil {
			return ts, id, nil
		}
	}
	return 0, 0, filterError("invalid cursor")
}

func (l *Logs) locate(ctx context.Context, requestID string) (LogMeta, recordLocation, error) {
	row := l.engine.db.QueryRowContext(ctx, "SELECT "+listColumns+`, s.id, s.day, s.state, s.dict_id, l.offset, l.length
		FROM reqlog_records l JOIN reqlog_segments s ON s.id = l.segment_id
		WHERE l.request_id = ? ORDER BY l.ts DESC, l.id DESC LIMIT 1`, requestID)
	var m LogMeta
	var loc recordLocation
	var id, ts int64
	err := row.Scan(&id, &m.RequestID, &m.TraceID, &m.SessionID, &m.ParentSessionID, &ts, &m.DurationMs, &m.Method, &m.URL, &m.Path,
		&m.Status, &m.Model, &m.AuthIndex, &m.Streaming, &m.ErrorOnly, &m.Dropped, &m.RequestBytes, &m.ResponseBytes, &m.RecordBytes,
		&loc.segment, &loc.day, &loc.state, &loc.dictID, &loc.offset, &loc.length)
	if errors.Is(err, sql.ErrNoRows) {
		return m, loc, errNotFound
	}
	if err != nil {
		return m, loc, fmt.Errorf("find request log: %w", err)
	}
	m.Timestamp = formatTime(ts)
	return m, loc, nil
}

// Raw returns a record's text in upstream's log file layout.
func (l *Logs) Raw(ctx context.Context, requestID string) (LogMeta, []byte, error) {
	m, _, text, err := l.read(ctx, requestID)
	return m, text, err
}

func (l *Logs) read(ctx context.Context, requestID string) (LogMeta, *recordMeta, []byte, error) {
	m, loc, err := l.locate(ctx, requestID)
	if err != nil {
		return m, nil, nil, err
	}
	frame, err := l.engine.readFrame(ctx, loc)
	if err != nil {
		return m, nil, nil, err
	}
	frameMeta, residual, refs, err := decodeFrame(frame)
	if err != nil {
		return m, nil, nil, fmt.Errorf("segment %d offset %d: %w", loc.segment, loc.offset, err)
	}
	if frameMeta.RequestID != m.RequestID {
		return m, nil, nil, fmt.Errorf("segment %d offset %d holds %q, index says %q", loc.segment, loc.offset, frameMeta.RequestID, m.RequestID)
	}
	blobs, err := l.engine.blobs.getAll(ctx, refs)
	if err != nil {
		return m, nil, nil, err
	}
	text, err := spliceBlobs(residual, refs, blobs)
	if err != nil {
		return m, nil, nil, err
	}
	return m, frameMeta, text, nil
}

// Get returns a record split into its parts.
func (l *Logs) Get(ctx context.Context, requestID string) (*Detail, error) {
	m, fm, text, err := l.read(ctx, requestID)
	if err != nil {
		return nil, err
	}
	section := func(name string) string {
		s, ok := fm.Sections[name]
		if !ok || s[0] < 0 || s[1] > len(text) || s[0] > s[1] {
			return ""
		}
		return string(text[s[0]:s[1]])
	}
	return &Detail{
		Meta:     m,
		Request:  DetailRequest{Method: fm.Method, URL: fm.URL, Headers: nonNil(fm.RequestHeaders), Body: section(secRequestBody)},
		Response: DetailResponse{Status: fm.Status, Headers: nonNil(fm.ResponseHeaders), Body: section(secResponseBody)},
		Upstream: DetailUpstream{
			Request:              section(secAPIRequest),
			Response:             section(secAPIResponse),
			Errors:               fm.APIErrors,
			WebsocketTimeline:    section(secWebsocketTimeline),
			APIWebsocketTimeline: section(secAPIWSTimeline),
		},
	}, nil
}

func nonNil(h map[string][]string) map[string][]string {
	if h == nil {
		return map[string][]string{}
	}
	return h
}

// Stats summarizes stored logs and the writer's drop counters.
func (l *Logs) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	var oldest, newest sql.NullInt64
	if err := l.engine.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(ts), MAX(ts), COALESCE(SUM(text_bytes), 0) FROM reqlog_records`).Scan(&s.Count, &oldest, &newest, &s.RawBytes); err != nil {
		return s, fmt.Errorf("request log stats: %w", err)
	}
	var packBytes int64
	if err := l.engine.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(state != ?), 0),
		COALESCE(SUM(CASE WHEN state = ? THEN disk_bytes ELSE raw_bytes END), 0), (SELECT COALESCE(SUM(bytes), 0) FROM reqlog_packs) FROM reqlog_segments`,
		segmentSealed, segmentSealed).Scan(&s.Segments, &s.OpenSegments, &s.BytesOnDisk, &packBytes); err != nil {
		return s, fmt.Errorf("request log stats: %w", err)
	}
	s.BytesOnDisk += packBytes
	if oldest.Valid {
		t := formatTime(oldest.Int64)
		s.Oldest = &t
	}
	if newest.Valid {
		t := formatTime(newest.Int64)
		s.Newest = &t
	}
	st := &l.engine.stats
	s.QueueBytes = l.engine.budget.used.Load()
	s.DroppedBodies, s.DroppedRecords, s.Truncated, s.WriteErrors = st.droppedBodies.Load(), st.droppedRecords.Load(), st.truncated.Load(), st.writeErrors.Load()
	return s, nil
}

// Routes registers request log endpoints under the taproot API group.
func (l *Logs) Routes(r *gin.RouterGroup) {
	r.GET("/request-logs", l.handleList)
	r.GET("/request-logs/stats", l.handleStats)
	r.GET("/request-logs/:requestId", l.handleGet)
	r.GET("/request-logs/:requestId/raw", l.handleRaw)
}

func apiError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

func parseTimeParam(c *gin.Context, name string) (time.Time, bool) {
	raw := c.Query(name)
	if raw == "" {
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid_"+name, name+" must be RFC 3339")
		return time.Time{}, false
	}
	return t, true
}

func (l *Logs) handleList(c *gin.Context) {
	from, ok := parseTimeParam(c, "from")
	if !ok {
		return
	}
	to, ok := parseTimeParam(c, "to")
	if !ok {
		return
	}
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			apiError(c, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return
		}
		limit = n
	}
	logs, next, err := l.List(c.Request.Context(), ListFilter{
		From: from, To: to, Query: c.Query("q"), Status: c.Query("status"), Model: c.Query("model"),
		AuthIndex: c.Query("authIndex"), SessionID: c.Query("sessionId"), Cursor: c.Query("cursor"), Limit: limit,
	})
	var badFilter filterError
	if errors.As(err, &badFilter) {
		apiError(c, http.StatusBadRequest, "invalid_filter", badFilter.Error())
		return
	}
	if err != nil {
		log.WithError(err).Warn("request logs: list")
		apiError(c, http.StatusInternalServerError, "internal", "failed to list request logs")
		return
	}
	var nextCursor *string
	if next != "" {
		nextCursor = &next
	}
	c.JSON(http.StatusOK, gin.H{"logs": logs, "nextCursor": nextCursor})
}

func (l *Logs) handleGet(c *gin.Context) {
	detail, err := l.Get(c.Request.Context(), c.Param("requestId"))
	if err != nil {
		l.readError(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (l *Logs) handleRaw(c *gin.Context) {
	m, text, err := l.Raw(c.Request.Context(), c.Param("requestId"))
	if err != nil {
		l.readError(c, err)
		return
	}
	name := "request-" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, m.RequestID) + ".log"
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "text/plain; charset=utf-8", text)
}

func (l *Logs) readError(c *gin.Context, err error) {
	if errors.Is(err, errNotFound) {
		apiError(c, http.StatusNotFound, "not_found", "request log not found")
		return
	}
	log.WithError(err).Warn("request logs: read")
	apiError(c, http.StatusInternalServerError, "internal", "failed to read request log")
}

func (l *Logs) handleStats(c *gin.Context) {
	stats, err := l.Stats(c.Request.Context())
	if err != nil {
		log.WithError(err).Warn("request logs: stats")
		apiError(c, http.StatusInternalServerError, "internal", "failed to read request log stats")
		return
	}
	c.JSON(http.StatusOK, stats)
}
