package taproot

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/265866/taproot/internal/clienterror"
	"github.com/265866/taproot/internal/httpwire"
	"github.com/265866/taproot/internal/interfaces"
	"github.com/265866/taproot/internal/logging"
	"github.com/265866/taproot/internal/taproot/ingest"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
)

// clientRequestPrefixes are the routes upstream gives a request ID
// (aiAPIPrefixes in internal/logging), which links a request to its usage
// records and its request log.
var clientRequestPrefixes = []string{"/v1/", "/v1beta/", "/openai/v1/", "/backend-api/codex/"}

// websocketTurnRoutes serve Responses websockets, where each turn is a request.
var websocketTurnRoutes = map[string]bool{"/v1/responses": true, "/backend-api/codex/responses": true}

const (
	// bodyPrefixLimit is how much of a request body is kept to find its model
	// when no usage record names it.
	bodyPrefixLimit = 16 << 10
	errorBodyLimit  = 2048
)

// clientRequestKind reports whether the request asks the pool for a model
// response: a POST to a matched route under a request-ID prefix, other than
// token counts and realtime voice sessions, or a Responses websocket.
func clientRequestKind(c *gin.Context) (record, websocket bool) {
	route := c.FullPath()
	if route == "" {
		return false, false
	}
	path := c.Request.URL.Path
	prefixed := false
	for _, p := range clientRequestPrefixes {
		if strings.HasPrefix(path, p) {
			prefixed = true
			break
		}
	}
	if !prefixed || strings.HasPrefix(route, "/v1/realtime") || strings.HasPrefix(route, "/v1/live") ||
		strings.HasSuffix(route, "/count_tokens") || strings.HasSuffix(path, ":countTokens") {
		return false, false
	}
	switch c.Request.Method {
	case http.MethodPost:
		return true, false
	case http.MethodGet:
		ws := websocketTurnRoutes[route] && c.IsWebsocket()
		return ws, ws
	}
	return false, false
}

// clientRequests records every client request to the pool after its response,
// through ingest's queue. It runs after the access guard, so a refused
// request is never one, and outside the no-accounts middleware, so it sees
// the rewritten error. On the request path it only takes timestamps and
// keeps the first bytes of the body and of an error response; the database
// write happens on ingest's writer.
func clientRequests(sink *ingest.Ingest, accounts *noAccounts) gin.HandlerFunc {
	return func(c *gin.Context) {
		record, websocket := clientRequestKind(c)
		if !record {
			c.Next()
			return
		}
		start := time.Now()
		clientCtx := c.Request.Context()
		requestID := logging.GetGinRequestID(c)
		link := sink.TrackRequest(requestID, websocket, c.FullPath())
		var (
			w    *recordingWriter
			body *bodyPrefix
		)
		if !websocket {
			w = &recordingWriter{ResponseWriter: c.Writer}
			c.Writer = w
			if c.Request.Body != nil {
				body = &bodyPrefix{ReadCloser: c.Request.Body}
				c.Request.Body = body
			}
		}
		returned := false
		defer func() {
			end := time.Now()
			r := ingest.ClientRequest{
				Start: start, RequestID: requestID, Path: c.FullPath(), Status: c.Writer.Status(), Latency: end.Sub(start),
			}
			var errBody []byte
			if w != nil {
				c.Writer = w.ResponseWriter
				if w.wrote && isStream(w.Header()) {
					r.Stream = true
					if !w.first.IsZero() {
						r.TTFT = w.first.Sub(start)
					}
				}
				errBody = w.errBody
			}
			r.Model = requestModel(c, body)
			body.release()
			switch {
			case !returned:
				// A panic: upstream's recovery answers 500 once it unwinds.
				r.Status, r.Failed, r.ErrorCode = http.StatusInternalServerError, true, "internal_error"
			case websocket:
				// After the upgrade, each turn is a request and the errors in
				// the context are the turns'.
				r.Failed = r.Status >= http.StatusBadRequest
				if !r.Failed {
					refused, unexplained := refusedTurns(c, accounts, r)
					sink.FinishWebsocket(link, r, refused, unexplained)
					return
				}
			default:
				classify(c, clientCtx, &r)
			}
			sink.FinishRequest(link, r, errBody)
		}()
		c.Next()
		returned = true
	}
}

// classify sets whether the request failed. A stream that already sent 200
// and then ended in an error takes that error's status, as the request log
// does. A request the client abandoned is 499 and not a failure; when only
// the client's context says so, ingest decides from the request's last
// attempt (see ingest.ClientRequest.ClientGone).
func classify(c *gin.Context, clientCtx context.Context, r *ingest.ClientRequest) {
	if v, ok := c.Get(noAccountKey); ok {
		refused, _ := v.(noAccount)
		r.ErrorCode, r.Failed = refused.code, true
		r.Model = cmp.Or(refused.model, r.Model)
		return
	}
	errs := apiResponseErrors(c)
	var actionable *interfaces.ErrorMessage
	cancelled := false
	for k := len(errs) - 1; k >= 0; k-- {
		e := errs[k]
		if e == nil {
			continue
		}
		if clienterror.IsClientCancellation(e.StatusCode, e.Error) {
			cancelled = true
			continue
		}
		if actionable == nil {
			actionable = e
		}
	}
	switch {
	case r.Status == ingest.StatusClientClosed:
	case r.Status >= http.StatusBadRequest:
		r.Failed = true
	case actionable != nil:
		r.Failed = true
		if s := errorStatus(actionable); s >= http.StatusBadRequest {
			r.Status = s
		} else {
			r.ErrorCode = "stream_error"
		}
		if actionable.Error != nil {
			r.ErrorMessage = actionable.Error.Error()
		}
	case cancelled:
		r.Status = ingest.StatusClientClosed
	case clientCtx.Err() != nil:
		r.ClientGone = true
	}
}

// apiResponseErrors are the errors upstream's handlers reported for the
// request, oldest first.
func apiResponseErrors(c *gin.Context) []*interfaces.ErrorMessage {
	v, _ := c.Get("API_RESPONSE_ERROR")
	errs, _ := v.([]*interfaces.ErrorMessage)
	return errs
}

// refusalCodes are the codes of upstream's auth manager errors for a request
// it found no account to try for. Without Home mode, which taproot refuses,
// the manager reports an attempted account's error instead whenever it tried
// one, so these mean no account was tried in the final round. After a retry
// round whose failed attempt the executor did not mark as upstream (Codex
// HTTP, openai-compat), the turn can also have a failed usage record.
var refusalCodes = map[string]bool{
	"auth_not_found": true, "auth_unavailable": true, "auth_disabled": true, "model_cooldown": true,
	"provider_not_found": true, "executor_not_found": true, "session_auth_unavailable": true,
}

// refusedTurns reads the turns of a Responses websocket that published no
// usage record from the errors upstream reported into the connection's gin
// context, which is the only place it reports them. refused are turns no
// account was tried for: no account serves the model, or every account that
// does is cooling down or unavailable. unexplained are the other errors
// except client cancellations, such as a turn the per-turn API key check
// refused or a malformed turn; they are turns only if the connection
// published no usage record, since otherwise an attempt may explain them.
// The errors carry no time, so each turn is dated when the connection ends.
func refusedTurns(c *gin.Context, accounts *noAccounts, conn ingest.ClientRequest) (refused, unexplained []ingest.ClientRequest) {
	for _, e := range apiResponseErrors(c) {
		if e == nil || e.Error == nil || clienterror.IsClientCancellation(e.StatusCode, e.Error) {
			continue
		}
		turn := ingest.ClientRequest{Start: conn.Start.Add(conn.Latency), RequestID: conn.RequestID, Path: conn.Path,
			Status: cmp.Or(errorStatus(e), http.StatusInternalServerError), ErrorMessage: e.Error.Error()}
		var authErr *coreauth.Error
		var cooling interface{ IsModelCooldown() bool }
		if model, ok := unknownProviderModel([]byte(turn.ErrorMessage)); ok {
			turn.Model = model
			code, text, _ := accounts.message(model)
			turn.ErrorCode, turn.ErrorMessage = code, cmp.Or(text, turn.ErrorMessage)
			refused = append(refused, turn)
		} else if errors.As(e.Error, &authErr) && refusalCodes[authErr.Code] || errors.As(e.Error, &cooling) && cooling.IsModelCooldown() {
			refused = append(refused, turn)
		} else {
			unexplained = append(unexplained, turn)
		}
	}
	return refused, unexplained
}

func errorStatus(e *interfaces.ErrorMessage) int {
	if e.StatusCode >= http.StatusBadRequest && e.StatusCode != clienterror.StatusClientClosedRequest {
		return e.StatusCode
	}
	if s := clienterror.HTTPStatusFromError(e.Error); s != clienterror.StatusClientClosedRequest {
		return s
	}
	return 0
}

func isStream(h http.Header) bool {
	ct := h.Get("Content-Type")
	return strings.HasPrefix(ct, "text/event-stream") || strings.HasPrefix(ct, "application/x-ndjson")
}

// requestModel is the model the request names: in the path for Gemini
// routes, else the body's first "model" string. Usage records name the model
// of every request that reached an account and take precedence; this covers
// requests refused before that. While request logging is on, upstream's
// logging middleware can swap the body for its own copy after this
// middleware wrapped it, and the body then gives nothing.
func requestModel(c *gin.Context, body *bodyPrefix) string {
	if action := c.Param("action"); action != "" {
		model, _, _ := strings.Cut(strings.TrimPrefix(action, "/"), ":")
		return model
	}
	if body == nil {
		return ""
	}
	return bodyModel(body.buf)
}

// bodyModel finds "model": "<name>" in a JSON prefix without parsing it.
func bodyModel(b []byte) string {
	const key = `"model"`
	for {
		k := bytes.Index(b, []byte(key))
		if k < 0 {
			return ""
		}
		b = b[k+len(key):]
		rest := bytes.TrimLeft(b, " \t\r\n")
		if len(rest) == 0 || rest[0] != ':' {
			continue
		}
		rest = bytes.TrimLeft(rest[1:], " \t\r\n")
		if len(rest) == 0 || rest[0] != '"' {
			continue
		}
		end := bytes.IndexAny(rest[1:], `"\`)
		if end < 0 || end > 200 || rest[1+end] != '"' {
			return ""
		}
		return string(rest[1 : 1+end])
	}
}

// bodyPrefix keeps the first bodyPrefixLimit bytes the handler reads, in a
// pooled buffer that release returns.
type bodyPrefix struct {
	io.ReadCloser
	buf []byte
}

var prefixBuffers = sync.Pool{New: func() any { b := make([]byte, 0, bodyPrefixLimit); return &b }}

func (b *bodyPrefix) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && b.buf == nil {
		b.buf = (*prefixBuffers.Get().(*[]byte))[:0]
	}
	if room := bodyPrefixLimit - len(b.buf); room > 0 && n > 0 {
		b.buf = append(b.buf, p[:min(n, room)]...)
	}
	return n, err
}

func (b *bodyPrefix) release() {
	if b != nil && b.buf != nil {
		buf := b.buf[:0]
		b.buf = nil
		prefixBuffers.Put(&buf)
	}
}

// recordingWriter notes whether the response has a body and when its first
// byte other than an SSE comment was written, and keeps the start of an
// error response. Handlers can send SSE keep-alive comments before the
// first token (streaming.send-keepalive-before-ft), and those are not one.
type recordingWriter struct {
	gin.ResponseWriter
	wrote   bool
	first   time.Time
	errBody []byte
}

func seen[T string | []byte](w *recordingWriter, p T) bool {
	if len(p) == 0 {
		return false
	}
	w.wrote = true
	if w.first.IsZero() && !sseCommentsOnly(p) {
		w.first = time.Now()
	}
	// A compressed body cannot be read for its message.
	return w.Status() >= http.StatusBadRequest && len(w.errBody) < errorBodyLimit &&
		w.Header().Get("Content-Encoding") == ""
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	if seen(w, p) {
		w.errBody = append(w.errBody, p[:min(len(p), errorBodyLimit-len(w.errBody))]...)
	}
	return w.ResponseWriter.Write(p)
}

func (w *recordingWriter) WriteString(s string) (int, error) {
	if seen(w, s) {
		w.errBody = append(w.errBody, s[:min(len(s), errorBodyLimit-len(w.errBody))]...)
	}
	return w.ResponseWriter.WriteString(s)
}

// sseCommentsOnly says whether every line of p is empty or an SSE comment.
func sseCommentsOnly[T string | []byte](p T) bool {
	lineStart := true
	for k := range len(p) {
		switch b := p[k]; {
		case b == '\n' || b == '\r':
			lineStart = true
		case lineStart && b != ':':
			return false
		default:
			lineStart = false
		}
	}
	return true
}

// FlushError and Unwrap keep upstream's error-aware flush and
// http.ResponseController reaching the transport, as its own wrappers do.
func (w *recordingWriter) FlushError() error { return httpwire.FlushResponse(w.ResponseWriter) }

func (w *recordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
