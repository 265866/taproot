package reqlog

import (
	"bytes"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/logging"
	"github.com/265866/taproot/sdk/cliproxy/session"
	"github.com/MoeForge/moego/ojson"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

// requestLogger replaces upstream's FileRequestLogger. It keeps upstream's
// switch semantics: with request-log on every request is stored; with it off
// only requests the middleware forces as errors are stored (marked errorOnly),
// and streaming requests are not captured at all.
type requestLogger struct {
	engine   *engine
	enabled  atomic.Bool
	excluder atomic.Pointer[excluder]
	now      func() time.Time

	// streams holds writers that may still hold budget. Upstream's Finalize
	// skips Close when request-log was switched off mid-stream, so SetEnabled
	// releases what those writers hold.
	streamsMu sync.Mutex
	streams   map[*streamWriter]struct{}
}

var (
	_ logging.RequestLogger      = (*requestLogger)(nil)
	_ logging.StreamingLogWriter = (*streamWriter)(nil)
)

func (l *requestLogger) IsEnabled() bool { return l.enabled.Load() }

// SetEnabled follows request-log on config reload. Turning it off abandons
// in-flight streaming records, because their Close may never come.
func (l *requestLogger) SetEnabled(enabled bool) {
	l.enabled.Store(enabled)
	if enabled {
		return
	}
	l.streamsMu.Lock()
	live := l.streams
	l.streams = nil
	l.streamsMu.Unlock()
	for w := range live {
		w.abandon()
	}
}

func (l *requestLogger) trackStream(w *streamWriter) {
	l.streamsMu.Lock()
	defer l.streamsMu.Unlock()
	if l.streams == nil {
		l.streams = make(map[*streamWriter]struct{})
	}
	l.streams[w] = struct{}{}
}

func (l *requestLogger) untrackStream(w *streamWriter) {
	l.streamsMu.Lock()
	delete(l.streams, w)
	l.streamsMu.Unlock()
}

// SetErrorLogsMaxFiles is accepted for config reload compatibility. Taproot's
// max-days and max-bytes govern error-only records along with the rest.
func (l *requestLogger) SetErrorLogsMaxFiles(int) {}

// NewFileBodySource lets the middleware and executors spool large sections
// to disk instead of memory, as they do with upstream's logger.
func (l *requestLogger) NewFileBodySource(prefix string) (*logging.FileBodySource, error) {
	return logging.NewFileBodySourceInDir(l.engine.tmpDir(), prefix)
}

func (l *requestLogger) LogRequest(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	return l.LogRequestWithOptionsAndAllSources(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, nil, apiRequest, nil, apiResponse, nil, apiWebsocketTimeline, nil, apiResponseErrors, false, requestID, requestTimestamp, apiResponseTimestamp)
}

func (l *requestLogger) LogRequestWithOptions(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, force bool, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	return l.LogRequestWithOptionsAndAllSources(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, nil, apiRequest, nil, apiResponse, nil, apiWebsocketTimeline, nil, apiResponseErrors, force, requestID, requestTimestamp, apiResponseTimestamp)
}

func (l *requestLogger) LogRequestWithOptionsAndSources(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline []byte, websocketTimelineSource *logging.FileBodySource, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiWebsocketTimelineSource *logging.FileBodySource, apiResponseErrors []*interfaces.ErrorMessage, force bool, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	return l.LogRequestWithOptionsAndAllSources(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, websocketTimelineSource, apiRequest, nil, apiResponse, nil, apiWebsocketTimeline, apiWebsocketTimelineSource, apiResponseErrors, force, requestID, requestTimestamp, apiResponseTimestamp)
}

// LogRequestWithOptionsAndAllSources is the entry point the middleware
// prefers. It owns the sources it is given and removes them before returning.
func (l *requestLogger) LogRequestWithOptionsAndAllSources(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline []byte, websocketTimelineSource *logging.FileBodySource, apiRequest []byte, apiRequestSource *logging.FileBodySource, apiResponse []byte, apiResponseSource *logging.FileBodySource, apiWebsocketTimeline []byte, apiWebsocketTimelineSource *logging.FileBodySource, apiResponseErrors []*interfaces.ErrorMessage, force bool, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	defer cleanupSources(websocketTimelineSource, apiRequestSource, apiResponseSource, apiWebsocketTimelineSource)
	enabled := l.IsEnabled()
	if !enabled && !force {
		return nil
	}
	reachedUpstream := hasPayload(apiRequest) || apiRequestSource.HasPayload() || hasPayload(apiResponse) || apiResponseSource.HasPayload() ||
		hasPayload(apiWebsocketTimeline) || apiWebsocketTimelineSource.HasPayload() || len(apiResponseErrors) > 0
	if l.excluder.Load().excluded(method, url, statusCode, reachedUpstream) {
		return nil
	}
	if requestTimestamp.IsZero() {
		requestTimestamp = l.now()
	}
	c := &capture{
		url:             url,
		method:          method,
		requestHeaders:  redactHeaders(requestHeaders),
		requestBody:     body,
		status:          statusCode,
		statusWritten:   true,
		responseHeaders: redactHeaders(responseHeaders),
		wsTimeline:      mergeSource(websocketTimeline, websocketTimelineSource),
		apiWSTimeline:   mergeSource(apiWebsocketTimeline, apiWebsocketTimelineSource),
		apiRequest:      mergeSource(apiRequest, apiRequestSource),
		apiResponse:     mergeSource(apiResponse, apiResponseSource),
		apiErrors:       apiResponseErrors,
		requestID:       requestID,
		requestTime:     requestTimestamp,
		apiResponseTime: apiResponseTimestamp,
		finishedAt:      l.now(),
		errorOnly:       !enabled,
		// Upstream picks the streaming writer from Content-Type at WriteHeader
		// time and can finish an event stream through this buffered path.
		streaming: isEventStream(responseHeaders),
	}
	c.responseBody, c.decompressErr = decodeResponse(responseHeaders, response)
	l.engine.submit(c)
	return nil
}

func (l *requestLogger) LogStreamingRequest(url, method string, headers map[string][]string, body []byte, requestID string) (logging.StreamingLogWriter, error) {
	if !l.IsEnabled() {
		return &logging.NoOpStreamingLogWriter{}, nil
	}
	w := &streamWriter{
		logger: l,
		c: capture{
			url:            url,
			method:         method,
			requestHeaders: redactHeaders(headers),
			requestBody:    body,
			requestID:      requestID,
			requestTime:    l.now(),
			streaming:      true,
		},
	}
	l.trackStream(w)
	// A switch-off between the check above and tracking would miss w.
	if !l.IsEnabled() {
		w.abandon()
	}
	return w, nil
}

func cleanupSources(sources ...*logging.FileBodySource) {
	for _, source := range sources {
		if source == nil {
			continue
		}
		if err := source.Cleanup(); err != nil {
			log.WithError(err).Warn("request logs: clean up spooled section")
		}
	}
}

// mergeSource appends a section's spooled continuation to its in-memory part.
// Executors use one or the other, so both being present is rare.
func mergeSource(payload []byte, source *logging.FileBodySource) []byte {
	if !source.HasPayload() {
		return payload
	}
	spooled, err := source.Bytes()
	if err != nil {
		log.WithError(err).Warn("request logs: read spooled section")
		return payload
	}
	if len(payload) == 0 {
		return spooled
	}
	var out bytes.Buffer
	out.Write(payload)
	if missing := 3 - trailingNewlines(payload); missing > 0 {
		out.WriteString(strings.Repeat("\n", missing))
	}
	out.Write(spooled)
	return out.Bytes()
}

// streamWriter collects a streaming response. Chunks count against the shared
// budget as they arrive; once it is exhausted the rest of the body is skipped
// and the record notes the truncation.
type streamWriter struct {
	logger    *requestLogger
	mu        sync.Mutex
	c         capture
	chunks    bytes.Buffer
	reserved  int64
	truncated int64
	apiReqSrc *logging.FileBodySource
	apiResSrc *logging.FileBodySource
	closed    bool
}

func (w *streamWriter) WriteChunkAsync(chunk []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || len(chunk) == 0 {
		return
	}
	if w.truncated > 0 || !w.logger.engine.budget.reserve(int64(len(chunk))) {
		w.truncated += int64(len(chunk))
		return
	}
	w.reserved += int64(len(chunk))
	w.chunks.Write(chunk)
}

func (w *streamWriter) WriteStatus(status int, headers map[string][]string) error {
	if status == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.status = status
	w.c.statusWritten = true
	if headers != nil {
		w.c.responseHeaders = redactHeaders(headers)
	}
	return nil
}

func (w *streamWriter) WriteAPIRequest(apiRequest []byte) error {
	if len(apiRequest) == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.apiRequest = bytes.Clone(apiRequest)
	return nil
}

func (w *streamWriter) WriteAPIRequestSource(source *logging.FileBodySource) error {
	if !source.HasPayload() {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.apiReqSrc = source
	return nil
}

func (w *streamWriter) WriteAPIResponse(apiResponse []byte) error {
	if len(apiResponse) == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.apiResponse = bytes.Clone(apiResponse)
	return nil
}

func (w *streamWriter) WriteAPIResponseSource(source *logging.FileBodySource) error {
	if !source.HasPayload() {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.apiResSrc = source
	return nil
}

func (w *streamWriter) WriteAPIWebsocketTimeline(timeline []byte) error {
	if len(timeline) == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.apiWSTimeline = bytes.Clone(timeline)
	return nil
}

func (w *streamWriter) SetFirstChunkTimestamp(ts time.Time) {
	if ts.IsZero() {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.c.apiResponseTime = ts
}

// Close hands the record to the queue. The middleware removes the spooled
// sources right after Close returns, so they are read here.
func (w *streamWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	l := w.logger
	l.untrackStream(w)
	l.engine.budget.release(w.reserved)
	w.reserved = 0

	reachedUpstream := hasPayload(w.c.apiRequest) || w.apiReqSrc.HasPayload() || hasPayload(w.c.apiResponse) || w.apiResSrc.HasPayload() || hasPayload(w.c.apiWSTimeline)
	if l.excluder.Load().excluded(w.c.method, w.c.url, w.c.status, reachedUpstream) {
		return nil
	}
	c := w.c
	c.apiRequest = mergeSource(c.apiRequest, w.apiReqSrc)
	c.apiResponse = mergeSource(c.apiResponse, w.apiResSrc)
	c.responseBody = w.chunks.Bytes()
	if w.truncated > 0 {
		l.engine.stats.truncated.Add(1)
		c.responseBody = append(c.responseBody, []byte("\n[taproot: response truncated, "+strconv.FormatInt(w.truncated, 10)+" bytes dropped, log queue full]")...)
		c.dropped = "response truncated"
	}
	w.chunks = bytes.Buffer{}
	c.finishedAt = l.now()
	l.engine.submit(&c)
	return nil
}

// abandon drops the record and returns its budget without storing anything.
func (w *streamWriter) abandon() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	w.logger.untrackStream(w)
	w.logger.engine.budget.release(w.reserved)
	w.reserved = 0
	w.chunks = bytes.Buffer{}
}

var geminiModelPattern = regexp.MustCompile(`/models/([^/:?]+)`)

// identify fills session, model, and account fields. It parses the request
// body, so it runs on the writer goroutine unless the body is about to be
// dropped.
func (c *capture) identify() {
	if c.identified {
		return
	}
	c.identified = true
	if info, ok := session.ExtractSessionInfo(http.Header(c.requestHeaders), c.requestBody, nil); ok {
		c.sessionID = info.SessionID
		c.parentSessionID = info.ParentSessionID
	}
	if len(c.requestBody) > 0 {
		c.model = ojson.GetBytes(c.requestBody, "model").String()
	}
	if c.model == "" {
		if m := geminiModelPattern.FindStringSubmatch(c.url); m != nil {
			c.model = m[1]
		}
	}
	c.traceID = headerValue(c.responseHeaders, logging.CPATraceIDHeader)
	c.authIndex = authIndexFromTrace(c.traceID, c.requestID)
}

// authIndexFromTrace reads the account out of X-CPA-TRACE-ID, which upstream
// formats as <yyyymmddhhmmss>-<auth index>-<request id>.
func authIndexFromTrace(traceID, requestID string) string {
	if len(traceID) < 16 || traceID[14] != '-' {
		return ""
	}
	rest := traceID[15:]
	if requestID != "" && strings.HasSuffix(rest, "-"+requestID) {
		return strings.TrimSuffix(rest, "-"+requestID)
	}
	if i := strings.IndexByte(rest, '-'); i > 0 {
		return rest[:i]
	}
	return ""
}

// build renders the record text and its index row.
func (c *capture) build() (*recordMeta, []byte, error) {
	c.identify()
	if c.requestID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, nil, err
		}
		c.requestID = id.String()
	}
	text, sections := renderText(c)
	path := c.url
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	meta := &recordMeta{
		RequestID:       c.requestID,
		TraceID:         c.traceID,
		SessionID:       c.sessionID,
		ParentSessionID: c.parentSessionID,
		TimeMs:          c.requestTime.UnixMilli(),
		Method:          c.method,
		URL:             c.url,
		Path:            path,
		Status:          c.status,
		Model:           c.model,
		AuthIndex:       c.authIndex,
		Streaming:       c.streaming,
		ErrorOnly:       c.errorOnly,
		Dropped:         c.dropped,
		RequestBytes:    len(c.requestBody),
		ResponseBytes:   len(c.responseBody),
		RequestHeaders:  c.requestHeaders,
		ResponseHeaders: c.responseHeaders,
		Sections:        sections,
	}
	if !c.finishedAt.IsZero() {
		meta.DurationMs = max(c.finishedAt.Sub(c.requestTime).Milliseconds(), 0)
	}
	for _, apiErr := range c.apiErrors {
		if apiErr == nil {
			continue
		}
		view := apiErrorView{Status: apiErr.StatusCode}
		if apiErr.Error != nil {
			view.Message = apiErr.Error.Error()
		}
		meta.APIErrors = append(meta.APIErrors, view)
	}
	return meta, text, nil
}

func isEventStream(headers map[string][]string) bool {
	for key, values := range headers {
		if !strings.EqualFold(key, "Content-Type") {
			continue
		}
		for _, v := range values {
			if strings.Contains(strings.ToLower(v), "text/event-stream") {
				return true
			}
		}
	}
	return false
}
