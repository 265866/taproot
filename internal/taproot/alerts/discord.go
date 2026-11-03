package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// maxPending bounds the queue by distinct keys. Keys come from accounts
	// and windows, so only a runaway caller reaches it.
	maxPending = 256
	// maxAttempts bounds retries of one message, including 429 waits.
	maxAttempts = 4
	// maxSendWait caps the total wait between attempts of one message, so a
	// long 429 sequence cannot hold the queue for minutes.
	maxSendWait = time.Minute
	// sendTimeout bounds one webhook POST. Alerts are outbound notices, not
	// proxied traffic, so AGENTS.md's no-timeout rule does not apply.
	sendTimeout = 15 * time.Second
	maxBodyRead = 64 << 10
)

// testSendTimeout bounds the dashboard's test send, which holds the request.
var testSendTimeout = 10 * time.Second

var severityColors = map[Severity]int{
	SeverityInfo:      0x3B82F6,
	SeverityWarning:   0xF59E0B,
	SeverityCritical:  0xEF4444,
	SeverityRecovered: 0x22C55E,
}

type embedFooter struct {
	Text string `json:"text"`
}

type embed struct {
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Color       int         `json:"color"`
	Timestamp   string      `json:"timestamp"`
	Fields      []Field     `json:"fields,omitempty"`
	Footer      embedFooter `json:"footer"`
}

type webhookPayload struct {
	Username        string          `json:"username"`
	Embeds          []embed         `json:"embeds"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

// allowedMentions with no parse list keeps labels from pinging anyone.
type allowedMentions struct {
	Parse []string `json:"parse"`
}

func payloadFor(m Message) webhookPayload {
	fields := make([]Field, 0, len(m.Fields))
	for _, f := range m.Fields {
		fields = append(fields, Field{Name: truncate(f.Name, 256), Value: truncate(f.Value, 1024), Inline: f.Inline})
	}
	if len(fields) > 25 {
		fields = fields[:25]
	}
	return webhookPayload{
		Username: "taproot",
		Embeds: []embed{{
			Title:       truncate(m.Title, 256),
			Description: truncate(m.Description, 4096),
			Color:       severityColors[m.Severity],
			Timestamp:   m.At.UTC().Format(time.RFC3339),
			Fields:      fields,
			Footer:      embedFooter{Text: "taproot"},
		}},
		AllowedMentions: allowedMentions{Parse: []string{}},
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// DeliveryError describes a failed delivery without the webhook URL.
type DeliveryError struct {
	Status int
	Reason string
	// RetryAfter is Discord's requested wait on a 429.
	RetryAfter time.Duration
}

func (e *DeliveryError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("discord answered %d", e.Status)
	}
	return "discord unreachable: " + e.Reason
}

// Sender posts messages to the webhook from an in-memory queue that holds at
// most one message per key. A newer message for a pending key replaces it in
// place, so a recovery is never dropped behind its own alert.
type Sender struct {
	client *http.Client

	mu      sync.Mutex
	pending map[string]Message
	order   []string
	ready   chan struct{}

	webhook func() string
	// origin, when set, replaces the webhook's scheme and host. It exists for
	// end-to-end tests against a local fake Discord.
	origin  string
	backoff time.Duration
	sleep   func(context.Context, time.Duration) error
}

// NewSender returns a sender that reads the current webhook from webhook.
func NewSender(webhook func() string, origin string) *Sender {
	return &Sender{
		client:  &http.Client{Timeout: sendTimeout},
		pending: map[string]Message{},
		ready:   make(chan struct{}, 1),
		webhook: webhook,
		origin:  strings.TrimRight(origin, "/"),
		backoff: 2 * time.Second,
		sleep:   sleepCtx,
	}
}

// Enqueue queues m without blocking. A pending message with the same key is
// replaced and keeps its place in line.
func (s *Sender) Enqueue(m Message) {
	s.mu.Lock()
	_, pending := s.pending[m.Key]
	full := !pending && len(s.order) >= maxPending
	if !full {
		if !pending {
			s.order = append(s.order, m.Key)
		}
		s.pending[m.Key] = m
	}
	s.mu.Unlock()
	if full {
		log.WithField("key", m.Key).Warn("taproot alerts: too many pending alerts, dropping alert")
		return
	}
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *Sender) next() (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.order) == 0 {
		return Message{}, false
	}
	key := s.order[0]
	s.order = s.order[1:]
	m := s.pending[key]
	delete(s.pending, key)
	return m, true
}

// Run delivers queued messages until ctx ends.
func (s *Sender) Run(ctx context.Context) {
	for {
		for m, ok := s.next(); ok && ctx.Err() == nil; m, ok = s.next() {
			hook := s.webhook()
			if hook == "" {
				continue
			}
			if err := s.Send(ctx, hook, m); err != nil && ctx.Err() == nil {
				log.WithError(err).WithField("key", m.Key).Warn("taproot alerts: discord delivery failed")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-s.ready:
		}
	}
}

// SendOnce posts m in a single attempt, for the dashboard's test button.
func (s *Sender) SendOnce(ctx context.Context, webhook string, m Message) error {
	body, target, err := s.prepare(webhook, m)
	if err != nil {
		return err
	}
	_, _, err = s.post(ctx, target, body)
	return err
}

// Send posts one message, retrying network errors, 5xx, and 429 a few times
// with at most maxSendWait of waiting in total.
func (s *Sender) Send(ctx context.Context, webhook string, m Message) error {
	body, target, err := s.prepare(webhook, m)
	if err != nil {
		return err
	}
	var waited time.Duration
	for attempt := 1; ; attempt++ {
		wait, retry, errPost := s.post(ctx, target, body)
		if errPost == nil {
			return nil
		}
		if wait == 0 {
			wait = s.backoff << (attempt - 1)
		}
		if !retry || attempt == maxAttempts || waited+wait > maxSendWait {
			return errPost
		}
		waited += wait
		log.WithError(errPost).WithFields(log.Fields{"key": m.Key, "attempt": attempt, "retry_in": wait.String()}).
			Debug("taproot alerts: retrying discord delivery")
		if err := s.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (s *Sender) prepare(webhook string, m Message) ([]byte, string, error) {
	body, err := json.Marshal(payloadFor(m))
	if err != nil {
		return nil, "", fmt.Errorf("encode alert: %w", err)
	}
	target, err := s.target(webhook)
	if err != nil {
		return nil, "", err
	}
	return body, target, nil
}

func (s *Sender) target(webhook string) (string, error) {
	if s.origin == "" {
		return webhook, nil
	}
	u, err := url.Parse(webhook)
	if err != nil {
		return "", &DeliveryError{Reason: "invalid webhook"}
	}
	return s.origin + u.EscapedPath(), nil
}

// post sends once. It returns how long Discord asked to wait, and whether
// the failure is worth retrying.
func (s *Sender) post(ctx context.Context, target string, body []byte) (time.Duration, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, false, &DeliveryError{Reason: "invalid webhook"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "taproot-alerts")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, true, &DeliveryError{Reason: withoutURL(err)}
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Debug("taproot alerts: close response body")
		}
	}()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return 0, false, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		wait := retryAfter(resp.Header, raw)
		return wait, true, &DeliveryError{Status: resp.StatusCode, RetryAfter: wait}
	case resp.StatusCode >= 500:
		return 0, true, &DeliveryError{Status: resp.StatusCode}
	}
	return 0, false, &DeliveryError{Status: resp.StatusCode}
}

// retryAfter reads Discord's JSON retry_after (seconds, fractional), falling
// back to the Retry-After header.
func retryAfter(h http.Header, body []byte) time.Duration {
	var payload struct {
		RetryAfter float64 `json:"retry_after"`
	}
	secs := 0.0
	if json.Unmarshal(body, &payload) == nil && payload.RetryAfter > 0 {
		secs = payload.RetryAfter
	} else if v, err := strconv.ParseFloat(strings.TrimSpace(h.Get("Retry-After")), 64); err == nil && v > 0 {
		secs = v
	}
	// The cap keeps a hostile value from overflowing the conversion.
	d := time.Duration(min(secs, 24*3600) * float64(time.Second))
	if d <= 0 {
		d = time.Second
	}
	return d
}

// withoutURL drops the request URL that net/http puts in its errors, since
// the webhook URL is the credential.
func withoutURL(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
