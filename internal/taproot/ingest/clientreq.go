package ingest

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/265866/taproot/internal/clienterror"
	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/store"
	"github.com/265866/taproot/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

// ClientRequest is one request a client sent to a proxy route, as the client
// saw it: the attempts upstream on other accounts are part of it.
type ClientRequest struct {
	Start     time.Time
	RequestID string
	Device    access.Device
	Path      string
	Model     string
	Provider  string
	AuthIndex string
	Stream    bool
	Status    int
	Failed    bool
	Latency   time.Duration
	// TTFT is the time to the first response byte of a stream, or zero.
	TTFT time.Duration
	// Attempts counts upstream attempts; nil when no usage record can be
	// linked to the request.
	Attempts     *int
	ErrorCode    string
	ErrorMessage string
}

const (
	// clientSettle is how long a finished request waits for its last usage
	// records, which upstream delivers on another goroutine after the
	// executor returns, before its attempts are counted.
	clientSettle = 2 * time.Second
	// maxClientPending bounds the queue; past it, requests are dropped and
	// counted rather than slowing the proxy.
	maxClientPending = 50_000
	// maxLinks bounds the requests tracked at once. A request past it is
	// still recorded, with unknown attempts.
	maxLinks        = 50_000
	errorMessageMax = 300
	slotMs          = int64(15 * time.Minute / time.Millisecond)
)

// RequestLink collects what usage records say about one client request while
// it runs. Its fields are guarded by Ingest.linkMu.
type RequestLink struct {
	traceID   string
	websocket bool
	device    access.Device
	path      string

	attempts    int
	lastAttempt time.Time
	model       string
	provider    string
	authIndex   string
	stream      bool
	turns       int
}

// pendingRequest is a finished client request waiting to be written. A
// websocket's entry carries no row and only ends its link.
type pendingRequest struct {
	req       ClientRequest
	link      *RequestLink
	errorBody []byte
	ready     time.Time
	noRow     bool
}

// TrackRequest starts linking usage records to the client request with
// upstream's request ID (usage.Record.TraceID). For a websocket, every usage
// record of the connection becomes a request of its own: one per turn. It
// returns nil when the ID is empty or too many requests are tracked; the
// request is then recorded without attempts.
func (i *Ingest) TrackRequest(traceID string, websocket bool, path string) *RequestLink {
	if traceID == "" {
		return nil
	}
	l := &RequestLink{traceID: traceID, websocket: websocket, path: path, device: i.requestDevice(traceID)}
	i.linkMu.Lock()
	defer i.linkMu.Unlock()
	if len(i.links) >= maxLinks {
		return nil
	}
	if i.links == nil {
		i.links = map[string]*RequestLink{}
	}
	i.links[traceID] = l
	return l
}

func (i *Ingest) requestDevice(traceID string) access.Device {
	if src := i.devices.Load(); src != nil {
		if d, ok := (*src).RequestDevice(traceID); ok {
			return d
		}
	}
	return access.Device{}
}

// FinishRequest queues a finished client request. errorBody holds the start
// of an error response, which the writer reads the message from. It never
// touches the database.
func (i *Ingest) FinishRequest(link *RequestLink, r ClientRequest, errorBody []byte) {
	p := pendingRequest{req: r, link: link, errorBody: errorBody, ready: i.now().Add(clientSettle)}
	if link != nil {
		p.req.Device = link.device
		if link.websocket {
			i.linkMu.Lock()
			turns := link.turns
			i.linkMu.Unlock()
			// A connection that served a turn is recorded per turn; one that
			// failed before any turn, such as a refused upgrade, is one request.
			p.noRow = turns > 0 || r.Status < http.StatusBadRequest
		}
	} else if r.RequestID != "" {
		p.req.Device = i.requestDevice(r.RequestID)
	}
	i.enqueueRequest(p)
}

// RecordClientRequest queues a request whose fields are final, as the demo
// writes history. Its row is written on the next flush.
func (i *Ingest) RecordClientRequest(r ClientRequest) {
	i.enqueueRequest(pendingRequest{req: r})
}

func (i *Ingest) enqueueRequest(p pendingRequest) {
	i.mu.Lock()
	if len(i.pendingRequests) >= maxClientPending {
		i.droppedRequests++
		dropped := i.droppedRequests
		i.mu.Unlock()
		if p.link != nil {
			i.unlink(p.link)
		}
		if dropped == 1 || dropped%1000 == 0 {
			log.WithField("dropped", dropped).Warn("taproot ingest: client request queue full, dropping requests")
		}
		return
	}
	i.pendingRequests = append(i.pendingRequests, p)
	i.mu.Unlock()
}

func (i *Ingest) unlink(l *RequestLink) {
	i.linkMu.Lock()
	if i.links[l.traceID] == l {
		delete(i.links, l.traceID)
	}
	i.linkMu.Unlock()
}

// observeAttempt counts a usage record against the client request it belongs
// to. Codex's image tool adds a second record for the same attempt with the
// attempt's start time, so a record that repeats the previous start is not a
// new attempt.
func (i *Ingest) observeAttempt(r usage.Record) {
	if r.TraceID == "" {
		return
	}
	i.linkMu.Lock()
	l := i.links[r.TraceID]
	if l == nil || (l.attempts > 0 && r.RequestedAt.Equal(l.lastAttempt)) {
		i.linkMu.Unlock()
		return
	}
	l.attempts++
	l.lastAttempt = r.RequestedAt
	if l.websocket {
		l.turns++
	}
	if r.Model != "" {
		l.model = r.Model
	}
	if p := strings.ToLower(strings.TrimSpace(r.Provider)); p != "" {
		l.provider = p
	}
	l.authIndex = cmp.Or(r.AuthIndex, l.authIndex)
	l.stream = l.stream || r.Stream
	websocket, device, path := l.websocket, l.device, l.path
	i.linkMu.Unlock()
	if websocket {
		i.enqueueRequest(pendingRequest{req: turnRequest(r, device, path), errorBody: []byte(r.Fail.Body)})
	}
}

// turnRequest is one websocket turn, from its usage record.
func turnRequest(r usage.Record, device access.Device, path string) ClientRequest {
	status := r.Fail.StatusCode
	if status == 0 {
		status = http.StatusOK
		if r.Failed {
			status = http.StatusBadGateway
		}
	}
	one := 1
	out := ClientRequest{
		Start: r.RequestedAt, RequestID: r.TraceID, Device: device, Path: path, Model: r.Model,
		Provider: strings.ToLower(strings.TrimSpace(r.Provider)), AuthIndex: r.AuthIndex, Stream: r.Stream,
		Status: status, Failed: r.Failed && status != StatusClientClosed, Latency: r.Latency, Attempts: &one,
	}
	if r.Stream {
		out.TTFT = r.TTFT
	}
	if r.Fail.Code == usage.FailureStreamTruncatedCode {
		out.ErrorCode = "stream_truncated"
	}
	return out
}

// AttachProviderGuess names the provider of a model no usage record named,
// such as a request no account was found for.
func (i *Ingest) AttachProviderGuess(guess func(model string) string) {
	if guess != nil {
		i.guessProvider.Store(&guess)
	}
}

// StatusClientClosed is the status of a request the client abandoned. It is
// not a failure of the pool.
const StatusClientClosed = clienterror.StatusClientClosedRequest

// takeRequests removes the queued requests ready by readyBy and finalizes them.
func (i *Ingest) takeRequests(readyBy time.Time) []ClientRequest {
	i.mu.Lock()
	var ready []pendingRequest
	keep := i.pendingRequests[:0]
	for _, p := range i.pendingRequests {
		if p.ready.After(readyBy) {
			keep = append(keep, p)
		} else {
			ready = append(ready, p)
		}
	}
	clear(i.pendingRequests[len(keep):])
	i.pendingRequests = keep
	i.mu.Unlock()

	out := make([]ClientRequest, 0, len(ready))
	for _, p := range ready {
		if p.link != nil {
			i.linkMu.Lock()
			l := p.link
			if i.links[l.traceID] == l {
				delete(i.links, l.traceID)
			}
			if !p.noRow {
				attempts := l.attempts
				p.req.Attempts = &attempts
				p.req.Model = cmp.Or(l.model, p.req.Model)
				p.req.Provider = cmp.Or(l.provider, p.req.Provider)
				p.req.AuthIndex = cmp.Or(l.authIndex, p.req.AuthIndex)
				p.req.Stream = p.req.Stream || l.stream
			}
			i.linkMu.Unlock()
		}
		if p.noRow {
			continue
		}
		out = append(out, i.finalize(p.req, p.errorBody))
	}
	return out
}

func (i *Ingest) finalize(r ClientRequest, errorBody []byte) ClientRequest {
	if r.Provider == "" && r.Model != "" {
		if g := i.guessProvider.Load(); g != nil {
			r.Provider = strings.ToLower((*g)(r.Model))
		}
	}
	if !r.Stream {
		r.TTFT = 0
	}
	switch {
	case r.ErrorMessage != "":
		r.ErrorMessage = cmp.Or(errorMessage([]byte(r.ErrorMessage)), r.ErrorMessage)
	case len(errorBody) > 0 && (r.Failed || r.Status >= http.StatusBadRequest):
		r.ErrorMessage = errorMessage(errorBody)
	}
	r.ErrorMessage = shorten(r.ErrorMessage)
	r.ErrorCode = errorCode(r)
	return r
}

// errorCode is a short machine code for what went wrong, empty for a success.
func errorCode(r ClientRequest) string {
	if r.ErrorCode != "" {
		return r.ErrorCode
	}
	if !r.Failed && r.Status < http.StatusBadRequest {
		return ""
	}
	switch r.Status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case StatusClientClosed:
		return "client_closed"
	case http.StatusBadGateway:
		return "bad_gateway"
	case http.StatusServiceUnavailable:
		return "unavailable"
	case http.StatusGatewayTimeout:
		return "timeout"
	case 529:
		return "overloaded"
	}
	switch {
	case r.Status >= 500:
		return "http_5xx"
	case r.Status >= 400:
		return "http_4xx"
	}
	return "stream_error"
}

// errorMessage reads an error response's message: error.message, a string
// error, or message in a JSON body, else the body's first line.
func errorMessage(body []byte) string {
	text := strings.TrimSpace(string(body))
	// An SSE error event carries its JSON on a data line.
	if strings.HasPrefix(text, "event:") || strings.HasPrefix(text, "data:") {
		for line := range strings.Lines(text) {
			if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
				text = strings.TrimSpace(data)
				break
			}
		}
	}
	var doc struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal([]byte(text), &doc) == nil {
		var inner struct {
			Message string `json:"message"`
		}
		var s string
		switch {
		case json.Unmarshal(doc.Error, &inner) == nil && inner.Message != "":
			return inner.Message
		case json.Unmarshal(doc.Error, &s) == nil && s != "":
			return s
		case doc.Message != "":
			return doc.Message
		}
		return ""
	}
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// shorten collapses whitespace and cuts s to errorMessageMax runes.
func shorten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= errorMessageMax {
		return s
	}
	r := []rune(s)
	return string(r[:errorMessageMax-1]) + "…"
}

// slotKey is one client_request_slots row.
type slotKey struct {
	Slot            int64
	DeviceID, Model string
}

type slotDelta struct {
	requests, failed, retries int64
	latency, ttft             histogram
}

func (d *slotDelta) add(r ClientRequest) {
	d.requests++
	if r.Failed {
		d.failed++
	}
	d.retries += retries(r.Attempts)
	d.latency.add(r.Latency.Milliseconds())
	if r.TTFT > 0 {
		d.ttft.add(r.TTFT.Milliseconds())
	}
}

func retries(attempts *int) int64 {
	if attempts == nil || *attempts < 2 {
		return 0
	}
	return int64(*attempts - 1)
}

type slotRollup map[slotKey]*slotDelta

func (s slotRollup) add(r ClientRequest) {
	k := slotKey{Slot: ms(r.Start) / slotMs, DeviceID: r.Device.ID, Model: r.Model}
	d := s[k]
	if d == nil {
		d = &slotDelta{latency: histogram{}, ttft: histogram{}}
		s[k] = d
	}
	d.add(r)
}

// apply adds the deltas to client_request_slots, merging histograms with the
// stored rows. empty skips the read of a stored row, as a rebuild may.
func (s slotRollup) apply(ctx context.Context, c *sql.Conn, empty bool) error {
	read, err := c.PrepareContext(ctx, `SELECT latency_hist, ttft_hist FROM client_request_slots WHERE slot = ? AND device_id = ? AND model = ?`)
	if err != nil {
		return fmt.Errorf("prepare slot read: %w", err)
	}
	defer closeStmt(read)
	write, err := c.PrepareContext(ctx, `INSERT INTO client_request_slots (slot, device_id, model, requests, failed, retries, latency_hist, ttft_hist)
		VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(slot, device_id, model) DO UPDATE SET
		requests = requests + excluded.requests, failed = failed + excluded.failed, retries = retries + excluded.retries,
		latency_hist = excluded.latency_hist, ttft_hist = excluded.ttft_hist`)
	if err != nil {
		return fmt.Errorf("prepare slot upsert: %w", err)
	}
	defer closeStmt(write)
	for _, k := range keysInOrder(s, func(a, b slotKey) int {
		return cmp.Or(cmp.Compare(a.Slot, b.Slot), strings.Compare(a.DeviceID, b.DeviceID), strings.Compare(a.Model, b.Model))
	}) {
		d := s[k]
		lat, ttft := histogram{}, histogram{}
		if !empty {
			var latBlob, ttftBlob []byte
			if err := read.QueryRowContext(ctx, k.Slot, k.DeviceID, k.Model).Scan(&latBlob, &ttftBlob); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("read client request slot: %w", err)
			}
			decodeHistogram(latBlob, lat)
			decodeHistogram(ttftBlob, ttft)
		}
		lat.merge(d.latency)
		ttft.merge(d.ttft)
		if _, err := write.ExecContext(ctx, k.Slot, k.DeviceID, k.Model, d.requests, d.failed, d.retries, lat.encode(), ttft.encode()); err != nil {
			return fmt.Errorf("upsert client request slot: %w", err)
		}
	}
	return nil
}

func closeStmt(s *sql.Stmt) {
	if err := s.Close(); err != nil {
		log.WithError(err).Warn("taproot ingest: close statement")
	}
}

// insertClientRequests writes the rows and their slot rollup.
func insertClientRequests(ctx context.Context, c *sql.Conn, reqs []ClientRequest) error {
	if len(reqs) == 0 {
		return nil
	}
	stmt, err := c.PrepareContext(ctx, `INSERT INTO client_requests (ts, request_id, device_id, device_name, path, model,
		provider, auth_index, stream, status, failed, latency_ms, ttft_ms, attempts, error_code, error_message)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("prepare client request insert: %w", err)
	}
	defer closeStmt(stmt)
	slots := slotRollup{}
	for _, r := range reqs {
		var ttft, attempts any
		if r.TTFT > 0 {
			ttft = r.TTFT.Milliseconds()
		}
		if r.Attempts != nil {
			attempts = *r.Attempts
		}
		if _, err := stmt.ExecContext(ctx, ms(r.Start), r.RequestID, r.Device.ID, r.Device.Name, r.Path, r.Model,
			r.Provider, r.AuthIndex, boolInt(r.Stream), r.Status, boolInt(r.Failed), r.Latency.Milliseconds(), ttft, attempts,
			r.ErrorCode, r.ErrorMessage); err != nil {
			return fmt.Errorf("insert client request: %w", err)
		}
		slots.add(r)
	}
	return slots.apply(ctx, c, false)
}

// rebuildClientSlots refills client_request_slots from client_requests when
// its request count differs, as rebuildDerived does for usage.
func rebuildClientSlots(ctx context.Context, db *sql.DB) error {
	return store.WithImmediateTx(ctx, db, func(c *sql.Conn) error {
		started := time.Now()
		var stale bool
		if err := c.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM client_requests) <>
			(SELECT COALESCE(SUM(requests), 0) FROM client_request_slots)`).Scan(&stale); err != nil {
			return fmt.Errorf("check client_request_slots: %w", err)
		}
		if !stale {
			return nil
		}
		rows, err := c.QueryContext(ctx, `SELECT ts, device_id, model, failed, attempts, latency_ms, COALESCE(ttft_ms, 0) FROM client_requests`)
		if err != nil {
			return fmt.Errorf("read client requests: %w", err)
		}
		slots := slotRollup{}
		n := 0
		for rows.Next() {
			var (
				r             ClientRequest
				ts, lat, ttft int64
				attempts      sql.NullInt64
			)
			if err := rows.Scan(&ts, &r.Device.ID, &r.Model, &r.Failed, &attempts, &lat, &ttft); err != nil {
				closeRows(rows)
				return fmt.Errorf("scan client request: %w", err)
			}
			r.Start, r.Latency, r.TTFT = time.UnixMilli(ts), time.Duration(lat)*time.Millisecond, time.Duration(ttft)*time.Millisecond
			if attempts.Valid {
				a := int(attempts.Int64)
				r.Attempts = &a
			}
			slots.add(r)
			n++
		}
		closeRows(rows)
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read client requests: %w", err)
		}
		if _, err := c.ExecContext(ctx, `DELETE FROM client_request_slots`); err != nil {
			return fmt.Errorf("clear client_request_slots: %w", err)
		}
		if err := slots.apply(ctx, c, true); err != nil {
			return fmt.Errorf("rebuild client_request_slots: %w", err)
		}
		log.Infof("taproot ingest: rebuilt client_request_slots from %d client requests in %s", n, time.Since(started).Round(time.Millisecond))
		return nil
	})
}
