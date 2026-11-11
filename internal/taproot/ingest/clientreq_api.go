package ingest

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	defaultTop         = 5
	maxTop             = 50
	defaultRecentLimit = 5
	maxRecentLimit     = 100
)

// logIndex says which request IDs have a stored request log.
type logIndex interface {
	Logged(ctx context.Context, requestIDs []string) (map[string]bool, error)
}

// AttachRequestLogs lets /requests/recent link only requests whose log exists.
func (i *Ingest) AttachRequestLogs(src logIndex) {
	if src != nil {
		i.logs.Store(&src)
	}
}

type requestTotals struct {
	Requests        int64    `json:"requests"`
	Failed          int64    `json:"failed"`
	FailureRate     *float64 `json:"failureRate"`
	Retries         int64    `json:"retries"`
	MedianLatencyMs *float64 `json:"medianLatencyMs"`
	MedianTTFTMs    *float64 `json:"medianTtftMs"`
	// CostUSD is the window's API-equivalent usage cost; UnpricedModels used
	// tokens but match no price, so it leaves them out.
	CostUSD        float64  `json:"costUSD"`
	UnpricedModels []string `json:"unpricedModels,omitempty"`
}

type requestBucket struct {
	T           string   `json:"t"`
	Requests    int64    `json:"requests"`
	Failed      int64    `json:"failed"`
	FailureRate *float64 `json:"failureRate"`
}

type topDevice struct {
	DeviceID string  `json:"deviceId"`
	Name     string  `json:"name"`
	Requests int64   `json:"requests"`
	Failed   int64   `json:"failed"`
	CostUSD  float64 `json:"costUSD"`
}

type topModel struct {
	Model    string  `json:"model"`
	Requests int64   `json:"requests"`
	Failed   int64   `json:"failed"`
	CostUSD  float64 `json:"costUSD"`
}

type requestCounts struct{ requests, failed int64 }

func (c *requestCounts) add(requests, failed int64) {
	c.requests += requests
	c.failed += failed
}

func failureRate(requests, failed int64) *float64 {
	if requests == 0 {
		return nil
	}
	r := float64(failed) / float64(requests)
	return &r
}

// requestsBuilder aggregates client requests into the buckets /usage/summary
// would use for the same range, bucket, and zone.
type requestsBuilder struct {
	days       *summaryBuilder
	buckets    map[int64]*requestCounts
	total      requestCounts
	retries    int64
	lat        histogram
	ttft       histogram
	devices    map[string]*requestCounts
	models     map[string]*requestCounts
	lastSlot   int64
	lastBucket int64
}

func newRequestsBuilder(days *summaryBuilder) *requestsBuilder {
	return &requestsBuilder{
		days: days, buckets: map[int64]*requestCounts{}, lat: histogram{}, ttft: histogram{},
		devices: map[string]*requestCounts{}, models: map[string]*requestCounts{}, lastSlot: -1,
	}
}

func (b *requestsBuilder) add(bucket int64, device, model string, requests, failed, retries int64) {
	c := b.buckets[bucket]
	if c == nil {
		c = &requestCounts{}
		b.buckets[bucket] = c
	}
	c.add(requests, failed)
	b.total.add(requests, failed)
	b.retries += retries
	countIn(b.devices, device).add(requests, failed)
	countIn(b.models, model).add(requests, failed)
}

func countIn(m map[string]*requestCounts, key string) *requestCounts {
	c := m[key]
	if c == nil {
		c = &requestCounts{}
		m[key] = c
	}
	return c
}

func (b *requestsBuilder) scanRaw(ctx context.Context, q querier, fromMs, toMs int64) error {
	if fromMs >= toMs {
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT ts, device_id, model, status, failed, attempts, latency_ms, COALESCE(ttft_ms, 0)
		FROM client_requests WHERE ts >= ? AND ts < ?`, fromMs, toMs)
	if err != nil {
		return fmt.Errorf("query client requests: %w", err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var (
			ts, failed, lat, ttft int64
			status                int
			attempts              *int
			device, model         string
		)
		if err := rows.Scan(&ts, &device, &model, &status, &failed, &attempts, &lat, &ttft); err != nil {
			return fmt.Errorf("scan client request: %w", err)
		}
		b.add(b.days.bucketStart(ts), device, model, 1, failed, retries(attempts))
		if servedTiming(attempts, status) {
			b.lat.add(lat)
			if ttft > 0 {
				b.ttft.add(ttft)
			}
		}
	}
	return rows.Err()
}

func (b *requestsBuilder) scanSlots(ctx context.Context, q querier, fromSlot, toSlot int64, skip map[int64]bool) error {
	rows, err := q.QueryContext(ctx, `SELECT slot, device_id, model, requests, failed, retries, latency_hist, ttft_hist
		FROM client_request_slots WHERE slot >= ? AND slot < ?`, fromSlot, toSlot)
	if err != nil {
		return fmt.Errorf("query client request slots: %w", err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var (
			slot, requests, failed, retries int64
			device, model                   string
			latBlob, ttftBlob               []byte
		)
		if err := rows.Scan(&slot, &device, &model, &requests, &failed, &retries, &latBlob, &ttftBlob); err != nil {
			return fmt.Errorf("scan client request slot: %w", err)
		}
		if skip[slot] {
			continue
		}
		// Rows come in slot order, and a day's bucket start costs a zone lookup.
		if slot != b.lastSlot {
			b.lastSlot, b.lastBucket = slot, b.days.bucketStart(slot*slotMs)
		}
		b.add(b.lastBucket, device, model, requests, failed, retries)
		decodeHistogram(latBlob, b.lat)
		decodeHistogram(ttftBlob, b.ttft)
	}
	return rows.Err()
}

// collect reads whole slots from client_request_slots, and from raw rows the
// partial slots at either edge and any slot a bucket boundary splits, so any
// from/to and zone gives exact counts. 5-minute buckets read raw rows only.
func (b *requestsBuilder) collect(ctx context.Context, q querier, fromMs, toMs int64, useSlots bool) error {
	fullStart := (fromMs + slotMs - 1) / slotMs * slotMs
	fullEnd := toMs / slotMs * slotMs
	if !useSlots || fullStart >= fullEnd {
		return b.scanRaw(ctx, q, fromMs, toMs)
	}
	split := map[int64]bool{}
	for t := b.days.bucketStart(fullStart); t < fullEnd; t = b.days.nextBucket(t) {
		if t > fullStart && t%slotMs != 0 {
			split[t/slotMs] = true
		}
	}
	if err := b.scanSlots(ctx, q, fullStart/slotMs, fullEnd/slotMs, split); err != nil {
		return err
	}
	for s := range split {
		if err := b.scanRaw(ctx, q, s*slotMs, (s+1)*slotMs); err != nil {
			return err
		}
	}
	if err := b.scanRaw(ctx, q, fromMs, fullStart); err != nil {
		return err
	}
	return b.scanRaw(ctx, q, fullEnd, toMs)
}

func parseBounded(c *gin.Context, name string, def, hi int) (int, bool) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > hi {
		badRequest(c, "%s: want an integer from 1 to %d", name, hi)
		return 0, false
	}
	return n, true
}

func (i *Ingest) handleRequestsSummary(c *gin.Context) {
	from, to, ok := i.parseRange(c, defaultRange)
	if !ok {
		return
	}
	bucketName := c.DefaultQuery("bucket", "1h")
	bucket, ok := bucketSizes[bucketName]
	if !ok {
		badRequest(c, "bucket: want 5m, 1h, or 1d")
		return
	}
	top, ok := parseBounded(c, "top", defaultTop, maxTop)
	if !ok {
		return
	}
	days := &summaryBuilder{bucketMs: bucket.Milliseconds()}
	if raw := strings.TrimSpace(c.Query("tz")); raw != "" {
		loc, err := time.LoadLocation(raw)
		if err != nil || strings.EqualFold(raw, "local") {
			badRequest(c, "tz: want an IANA time zone such as America/Los_Angeles")
			return
		}
		if bucketName == "1d" {
			days.loc = loc
		}
	}
	fromMs, toMs := ms(from), ms(to)
	firstBucket := days.bucketStart(fromMs)
	if (toMs-firstBucket)/min(days.bucketMs, 22*hourMs) >= maxBucketCount {
		badRequest(c, "range holds more than %d %s buckets", maxBucketCount, bucketName)
		return
	}
	pb, ok := i.priceBook(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	// One read transaction gives every query one snapshot, so the buckets
	// and the top lists add up to the same totals.
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		internalError(c, err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	b := newRequestsBuilder(days)
	if err := b.collect(ctx, tx, fromMs, toMs, bucketName != "5m"); err != nil {
		internalError(c, err)
		return
	}
	usageRows, err := collectDeviceRows(ctx, tx, fromMs, toMs)
	if err != nil {
		internalError(c, err)
		return
	}
	deviceCost, modelCost := map[string]float64{}, map[string]float64{}
	totals := requestTotals{
		Requests: b.total.requests, Failed: b.total.failed, FailureRate: failureRate(b.total.requests, b.total.failed),
		Retries: b.retries, MedianLatencyMs: b.lat.percentile(0.5), MedianTTFTMs: b.ttft.percentile(0.5),
	}
	for k, r := range usageRows {
		usd, priced := pb.cost(k.Model, r.tokens)
		if !priced {
			if r.tokens != (tokenSums{}) && !slices.Contains(totals.UnpricedModels, k.Model) {
				totals.UnpricedModels = append(totals.UnpricedModels, k.Model)
			}
			continue
		}
		deviceCost[k.DeviceID] += usd
		modelCost[k.Model] += usd
		totals.CostUSD += usd
	}
	slices.Sort(totals.UnpricedModels)

	buckets := make([]requestBucket, 0, (toMs-firstBucket)/days.bucketMs+2)
	for t := firstBucket; t < toMs; t = days.nextBucket(t) {
		var n requestCounts
		if got := b.buckets[t]; got != nil {
			n = *got
		}
		buckets = append(buckets, requestBucket{T: formatMs(t), Requests: n.requests, Failed: n.failed, FailureRate: failureRate(n.requests, n.failed)})
	}
	devices := make([]topDevice, 0, top)
	for _, id := range topKeys(b.devices, top) {
		n := b.devices[id]
		name, err := i.storedDeviceName(ctx, tx, id)
		if err != nil {
			internalError(c, err)
			return
		}
		devices = append(devices, topDevice{DeviceID: id, Name: i.deviceName(id, name), Requests: n.requests, Failed: n.failed, CostUSD: deviceCost[id]})
	}
	models := make([]topModel, 0, top)
	for _, m := range topKeys(b.models, top) {
		n := b.models[m]
		models = append(models, topModel{Model: m, Requests: n.requests, Failed: n.failed, CostUSD: modelCost[m]})
	}
	first, err := firstClientRequest(ctx, tx)
	if err != nil {
		internalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"totals": totals, "buckets": buckets, "topDevices": devices, "topModels": models, "firstRequestAt": first})
}

// firstClientRequest is the start of the oldest recorded client request, or
// nil when there is none. A server upgraded from 0.4 records client requests
// only from the upgrade on, so a window that starts earlier undercounts.
func firstClientRequest(ctx context.Context, q querier) (*string, error) {
	var first *int64
	if err := q.QueryRowContext(ctx, `SELECT MIN(ts) FROM client_requests`).Scan(&first); err != nil {
		return nil, fmt.Errorf("read first client request: %w", err)
	}
	return formatNullableMs(first), nil
}

// topKeys returns up to n keys with the most requests, then the most
// failures, then by key.
func topKeys(m map[string]*requestCounts, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Or(cmp.Compare(m[b].requests, m[a].requests), cmp.Compare(m[b].failed, m[a].failed), strings.Compare(a, b))
	})
	return keys[:min(n, len(keys))]
}

// storedDeviceName is the name stored with the device's latest request.
func (i *Ingest) storedDeviceName(ctx context.Context, q querier, id string) (string, error) {
	var name string
	err := q.QueryRowContext(ctx, `SELECT device_name FROM client_requests WHERE device_id = ? ORDER BY ts DESC LIMIT 1`, id).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("read device name: %w", err)
	}
	return name, nil
}

type recentRequest struct {
	T          string `json:"ts"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Path       string `json:"path"`
	Model      string `json:"model"`
	Provider   string `json:"provider"`
	Status     int    `json:"status"`
	Failed     bool   `json:"failed"`
	ErrorCode  string `json:"errorCode"`
	// ErrorMessage is the start of the error the client got.
	ErrorMessage string `json:"errorMessage"`
	Attempts     *int64 `json:"attempts"`
	LatencyMs    int64  `json:"latencyMs"`
	TTFTMs       *int64 `json:"ttftMs"`
	// RequestID opens the request in Request Logs; null when no log is stored.
	RequestID *string `json:"requestId"`
}

func (i *Ingest) handleRequestsRecent(c *gin.Context) {
	limit, ok := parseBounded(c, "limit", defaultRecentLimit, maxRecentLimit)
	if !ok {
		return
	}
	where := ""
	switch strings.TrimSpace(c.Query("failed")) {
	case "":
	case "true":
		where = "WHERE failed = 1"
	case "false":
		where = "WHERE failed = 0"
	default:
		badRequest(c, "failed: want true or false")
		return
	}
	ctx := c.Request.Context()
	rows, err := i.db.QueryContext(ctx, `SELECT ts, request_id, device_id, device_name, path, model, provider, status, failed,
		error_code, error_message, attempts, latency_ms, ttft_ms FROM client_requests `+where+` ORDER BY ts DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		internalError(c, err)
		return
	}
	defer closeRows(rows)
	out := []recentRequest{}
	var ids []string
	for rows.Next() {
		var (
			r         recentRequest
			ts        int64
			requestID string
		)
		if err := rows.Scan(&ts, &requestID, &r.DeviceID, &r.DeviceName, &r.Path, &r.Model, &r.Provider, &r.Status, &r.Failed,
			&r.ErrorCode, &r.ErrorMessage, &r.Attempts, &r.LatencyMs, &r.TTFTMs); err != nil {
			internalError(c, err)
			return
		}
		r.T = formatMs(ts)
		r.DeviceName = i.deviceName(r.DeviceID, r.DeviceName)
		if requestID != "" {
			r.RequestID = &requestID
			ids = append(ids, requestID)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		internalError(c, err)
		return
	}
	if src := i.logs.Load(); src != nil && len(ids) > 0 {
		logged, err := (*src).Logged(ctx, ids)
		if err != nil {
			// The links may lead to a missing log, which Request Logs reports.
			log.WithError(err).Warn("taproot ingest: look up request logs")
		} else {
			for k := range out {
				if out[k].RequestID != nil && !logged[*out[k].RequestID] {
					out[k].RequestID = nil
				}
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"requests": out})
}

// RequestCounts is the client requests that started in a range.
type RequestCounts struct {
	Requests int64
	// Failed leaves out requests the client abandoned (499 client_closed).
	Failed int64
	// ByProvider counts requests per provider; requests whose provider is
	// unknown are under "".
	ByProvider map[string]int64
}

// ClientRequestCounts counts the client requests that started in [from, to).
// Requests still waiting for their late usage records are not written yet,
// so the last two seconds or so may be missing.
func (i *Ingest) ClientRequestCounts(ctx context.Context, from, to time.Time) (RequestCounts, error) {
	out := RequestCounts{ByProvider: map[string]int64{}}
	rows, err := i.db.QueryContext(ctx, `SELECT provider, COUNT(*), SUM(failed) FROM client_requests WHERE ts >= ? AND ts < ? GROUP BY provider`, ms(from), ms(to))
	if err != nil {
		return out, fmt.Errorf("count client requests: %w", err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var (
			provider         string
			requests, failed int64
		)
		if err := rows.Scan(&provider, &requests, &failed); err != nil {
			return out, fmt.Errorf("count client requests: %w", err)
		}
		out.Requests += requests
		out.Failed += failed
		out.ByProvider[provider] += requests
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("count client requests: %w", err)
	}
	return out, nil
}
