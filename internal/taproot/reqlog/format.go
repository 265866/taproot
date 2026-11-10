package reqlog

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/265866/taproot/internal/buildinfo"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Section names index spans of a record's text, so the detail view can slice
// bodies out without reparsing the upstream text format.
const (
	secRequestBody       = "requestBody"
	secWebsocketTimeline = "websocketTimeline"
	secAPIWSTimeline     = "apiWebsocketTimeline"
	secAPIRequest        = "apiRequest"
	secAPIErrors         = "apiErrors"
	secAPIResponse       = "apiResponse"
	secResponseBody      = "responseBody"
)

const redacted = "[redacted]"

// capture is everything the middleware hands the logger for one request.
type capture struct {
	url, method     string
	requestHeaders  map[string][]string
	requestBody     []byte
	status          int
	statusWritten   bool
	responseHeaders map[string][]string
	responseBody    []byte
	decompressErr   error
	wsTimeline      []byte
	apiWSTimeline   []byte
	apiRequest      []byte
	apiResponse     []byte
	apiErrors       []*interfaces.ErrorMessage
	requestID       string
	requestTime     time.Time
	apiResponseTime time.Time
	finishedAt      time.Time
	streaming       bool
	errorOnly       bool
	dropped         string

	identified      bool
	sessionID       string
	parentSessionID string
	model           string
	traceID         string
	authIndex       string
}

func (c *capture) bodyBytes() int {
	return len(c.requestBody) + len(c.responseBody) + len(c.wsTimeline) + len(c.apiWSTimeline) + len(c.apiRequest) + len(c.apiResponse)
}

// dropBodies replaces every body with a marker so the record keeps its
// metadata when the queue has no room for the payload.
func (c *capture) dropBodies(reason string) {
	marker := func(b []byte) []byte {
		if len(b) == 0 {
			return nil
		}
		return []byte(fmt.Sprintf("[taproot: %d bytes dropped, %s]", len(b), reason))
	}
	c.requestBody = marker(c.requestBody)
	c.responseBody = marker(c.responseBody)
	c.wsTimeline = marker(c.wsTimeline)
	c.apiWSTimeline = marker(c.apiWSTimeline)
	c.apiRequest = marker(c.apiRequest)
	c.apiResponse = marker(c.apiResponse)
	if c.dropped != "" {
		reason = c.dropped + "; " + reason
	}
	c.dropped = reason
}

type span [2]int

// textBuilder writes upstream FileRequestLogger's text layout and remembers
// where each section landed.
type textBuilder struct {
	buf      bytes.Buffer
	sections map[string]span
}

func (t *textBuilder) str(s string) { t.buf.WriteString(s) }
func (t *textBuilder) raw(b []byte) { t.buf.Write(b) }
func (t *textBuilder) pos() int     { return t.buf.Len() }
func (t *textBuilder) mark(name string, start int) {
	if t.pos() > start {
		t.sections[name] = span{start, t.pos()}
	}
}

// spacing pads a section to end with three newlines, as upstream does.
func (t *textBuilder) spacing(trailing int) {
	if missing := 3 - trailing; missing > 0 {
		t.str(strings.Repeat("\n", missing))
	}
}

func trailingNewlines(b []byte) int {
	n := 0
	for i := len(b) - 1; i >= 0 && b[i] == '\n'; i-- {
		n++
	}
	return n
}

func hasPayload(b []byte) bool { return len(bytes.TrimSpace(b)) > 0 }

// renderText produces the record text in upstream's layout. Downstream and
// response headers are redacted and sorted; API sections arrive preformatted
// by the executors and get their header blocks redacted.
func renderText(c *capture) ([]byte, map[string]span) {
	t := &textBuilder{sections: make(map[string]span)}
	t.buf.Grow(c.bodyBytes() + 16<<10)
	wsTranscript := hasPayload(c.wsTimeline)
	downstream := "http"
	if wsTranscript || headerHas(c.requestHeaders, "Upgrade", "websocket") {
		downstream = "websocket"
	}
	if c.streaming {
		downstream = "http"
	}
	upstream := upstreamTransport(c)

	t.str("=== REQUEST INFO ===\n")
	t.str("Version: " + buildinfo.Version + "\n")
	t.str("URL: " + c.url + "\n")
	t.str("Method: " + c.method + "\n")
	t.str("Downstream Transport: " + downstream + "\n")
	if upstream != "" {
		t.str("Upstream Transport: " + upstream + "\n")
	}
	t.str("Timestamp: " + c.requestTime.Format(time.RFC3339Nano) + "\n")
	t.spacing(1)

	t.str("=== HEADERS ===\n")
	writeHeaderLines(t, c.requestHeaders)
	t.spacing(1)

	if !wsTranscript {
		t.str("=== REQUEST BODY ===\n")
		start := t.pos()
		t.raw(c.requestBody)
		t.mark(secRequestBody, start)
		if len(c.requestBody) > 0 {
			t.spacing(trailingNewlines(c.requestBody))
		} else {
			t.spacing(1)
		}
	}

	apiSection(t, secWebsocketTimeline, "=== WEBSOCKET TIMELINE ===\n", "=== WEBSOCKET TIMELINE", c.wsTimeline, time.Time{}, false)
	// Upstream's handshake and request events carry Headers: blocks with
	// cookies and partly masked tokens.
	apiSection(t, secAPIWSTimeline, "=== API WEBSOCKET TIMELINE ===\n", "=== API WEBSOCKET TIMELINE", c.apiWSTimeline, time.Time{}, true)
	apiSection(t, secAPIRequest, "=== API REQUEST ===\n", "=== API REQUEST", c.apiRequest, time.Time{}, true)

	start := t.pos()
	for _, apiErr := range c.apiErrors {
		if apiErr == nil {
			continue
		}
		t.str("=== API ERROR RESPONSE ===\n")
		t.str(fmt.Sprintf("HTTP Status: %d\n", apiErr.StatusCode))
		trailing := 1
		if apiErr.Error != nil {
			text := apiErr.Error.Error()
			t.str(text)
			if text != "" {
				trailing = trailingNewlines([]byte(text))
			}
		}
		t.spacing(trailing)
	}
	t.mark(secAPIErrors, start)

	apiSection(t, secAPIResponse, "=== API RESPONSE ===\n", "=== API RESPONSE", c.apiResponse, c.apiResponseTime, true)

	if wsTranscript && !c.streaming {
		return t.buf.Bytes(), t.sections
	}
	t.str("=== RESPONSE ===\n")
	if c.statusWritten {
		t.str(fmt.Sprintf("Status: %d\n", c.status))
	}
	writeHeaderLines(t, c.responseHeaders)
	if !bytes.HasPrefix(c.responseBody, []byte("\n")) && !bytes.HasPrefix(c.responseBody, []byte("\r\n")) {
		t.str("\n")
	}
	start = t.pos()
	t.raw(c.responseBody)
	t.mark(secResponseBody, start)
	if c.decompressErr != nil {
		t.str(fmt.Sprintf("\n[DECOMPRESSION ERROR: %v]", c.decompressErr))
	}
	if !c.streaming {
		t.str("\n")
	}
	return t.buf.Bytes(), t.sections
}

// apiSection writes a section that may arrive preformatted with its own
// heading. Executor-formatted sections carry headers and get them redacted on
// the way in, so a large body is copied once.
func apiSection(t *textBuilder, name, header, prefix string, payload []byte, ts time.Time, redact bool) {
	if len(payload) == 0 {
		return
	}
	start := t.pos()
	if !bytes.HasPrefix(payload, []byte(prefix)) {
		t.str(header)
		if !ts.IsZero() {
			t.str("Timestamp: " + ts.Format(time.RFC3339Nano) + "\n")
		}
	}
	if redact {
		writeRedacted(&t.buf, payload)
	} else {
		t.raw(payload)
	}
	t.mark(name, start)
	t.spacing(trailingNewlines(payload))
}

func upstreamTransport(c *capture) string {
	hasHTTP := hasPayload(c.apiRequest) || hasPayload(c.apiResponse)
	hasWS := hasPayload(c.apiWSTimeline)
	switch {
	case hasHTTP && hasWS:
		return "websocket+http"
	case hasWS:
		return "websocket"
	case hasHTTP:
		return "http"
	}
	return ""
}

func writeHeaderLines(t *textBuilder, headers map[string][]string) {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, value := range headers[key] {
			t.str(key + ": " + value + "\n")
		}
	}
}

func headerHas(headers map[string][]string, name, value string) bool {
	for key, values := range headers {
		if !strings.EqualFold(key, name) {
			continue
		}
		for _, v := range values {
			if strings.EqualFold(strings.TrimSpace(v), value) {
				return true
			}
		}
	}
	return false
}

func headerValue(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

// sensitiveHeader covers credentials in either direction. Upstream masks only
// some of these and keeps a prefix and suffix of the secret; taproot stores none
// of it.
func sensitiveHeader(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "cookie", "set-cookie", "proxy-authorization":
		return true
	}
	for _, needle := range []string{"authorization", "api-key", "apikey", "token", "secret", "password"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func redactHeaders(headers map[string][]string) map[string][]string {
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		copied := make([]string, len(values))
		for i, value := range values {
			if sensitiveHeader(key) {
				copied[i] = redacted
			} else {
				copied[i] = value
			}
		}
		out[key] = copied
	}
	return out
}

// writeRedacted copies an executor-formatted API section, masking
// credentials. Each attempt opens with an "=== API ..." line, and each
// websocket timeline event with "Event: api.websocket...", then a preamble
// that can carry "Upstream URL:" and "Auth: ... value=<key>" lines, which
// upstream masks only partly; headers follow a "Headers:" line until a blank
// line, and bodies follow a "Body:" line, so bodies are never touched.
func writeRedacted(out *bytes.Buffer, section []byte) {
	inPreamble, inHeaders := false, false
	rest := section
	for len(rest) > 0 {
		line := rest
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line = rest[:i+1]
		}
		rest = rest[len(line):]
		trimmed := bytes.TrimRight(line, "\r\n")
		eol := line[len(trimmed):]
		switch {
		case inHeaders && len(trimmed) == 0:
			inHeaders = false
		case inHeaders:
			if name, _, ok := bytes.Cut(trimmed, []byte(": ")); ok && sensitiveHeader(string(name)) {
				out.Write(name)
				out.WriteString(": " + redacted)
				out.Write(eol)
				continue
			}
		case string(trimmed) == "Headers:":
			inPreamble, inHeaders = false, true
		case string(trimmed) == "Body:":
			inPreamble = false
		case bytes.HasPrefix(trimmed, []byte("=== API ")):
			inPreamble = true
		case bytes.HasPrefix(trimmed, []byte("Event: ")):
			// Only a websocket request event carries Upstream URL and Auth
			// lines; a response event's frame text follows directly.
			inPreamble = bytes.HasPrefix(trimmed, []byte("Event: api.websocket.request"))
		case inPreamble && bytes.HasPrefix(trimmed, []byte("Upstream URL: ")):
			out.WriteString("Upstream URL: " + redactQuery(string(trimmed[len("Upstream URL: "):])))
			out.Write(eol)
			continue
		case inPreamble && bytes.HasPrefix(trimmed, []byte("Auth: ")):
			out.Write(authValue.ReplaceAll(trimmed, []byte("${1}"+redacted)))
			out.Write(eol)
			continue
		}
		out.Write(line)
	}
}

// authValue matches the credential in upstream's "Auth:" line, which ends
// the line ("type=api_key value=AIza...wxyz").
var authValue = regexp.MustCompile(`(\bvalue=).*`)

// redactQuery replaces the value of every credential-like query parameter in
// raw, a URL or path with an optional query. Upstream keeps 4 characters from
// each end of these; taproot stores none.
func redactQuery(raw string) string {
	base, query, ok := strings.Cut(raw, "?")
	if !ok {
		return raw
	}
	params := strings.Split(query, "&")
	for i, param := range params {
		key, _, hasValue := strings.Cut(param, "=")
		name, err := url.QueryUnescape(key)
		if err != nil {
			name = key
		}
		if hasValue && sensitiveParam(name) {
			params[i] = key + "=" + redacted
		}
	}
	return base + "?" + strings.Join(params, "&")
}

// sensitiveParam covers the query parameters upstream masks ("key", "value",
// which the management API uses for client keys, and api_key forms) and the
// names sensitiveHeader treats as credentials.
func sensitiveParam(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), "[]")
	return name == "key" || name == "value" || strings.Contains(name, "api_key") || sensitiveHeader(name)
}

// decodeResponse undoes Content-Encoding on non-streaming bodies the way
// upstream's logger does, so stored responses are readable and compressible.
// The decoded bytes hold their share of b, returned as held, so a
// decompression bomb stops at the budget; the caller releases held. On any
// error the body stays as it came.
func decodeResponse(headers map[string][]string, body []byte, b *budget) (decoded []byte, held int64, err error) {
	if len(body) == 0 {
		return body, 0, nil
	}
	var reader io.Reader
	switch strings.ToLower(headerValue(headers, "Content-Encoding")) {
	case "gzip":
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return body, 0, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	case "deflate":
		fl := flate.NewReader(bytes.NewReader(body))
		defer func() { _ = fl.Close() }()
		reader = fl
	case "br":
		reader = brotli.NewReader(bytes.NewReader(body))
	case "zstd":
		dec, err := zstd.NewReader(bytes.NewReader(body), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return body, 0, fmt.Errorf("failed to create zstd reader: %w", err)
		}
		defer dec.Close()
		reader = dec
	default:
		return body, 0, nil
	}
	out := &budgetWriter{budget: b}
	if _, err := io.Copy(out, reader); err != nil {
		b.release(out.held)
		return body, 0, fmt.Errorf("failed to decompress response: %w", err)
	}
	return out.buf.Bytes(), out.held, nil
}

var errBudgetFull = errors.New("log queue full")

// budgetWriter buffers what it is given, reserving each write in budget.
type budgetWriter struct {
	budget *budget
	buf    bytes.Buffer
	held   int64
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	if !w.budget.reserve(int64(len(p))) {
		return 0, errBudgetFull
	}
	w.held += int64(len(p))
	return w.buf.Write(p)
}
