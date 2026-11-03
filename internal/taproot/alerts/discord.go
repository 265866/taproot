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
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	queueSize = 64
	// maxAttempts bounds retries of one message, including 429 waits.
	maxAttempts = 4
	// maxRetryAfter caps how long one 429 may hold the queue.
	maxRetryAfter = 2 * time.Minute
	// sendTimeout bounds one webhook POST. Alerts are outbound notices, not
	// proxied traffic, so AGENTS.md's no-timeout rule does not apply.
	sendTimeout = 15 * time.Second
	maxBodyRead = 64 << 10
)

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
}

func (e *DeliveryError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("discord answered %d", e.Status)
	}
	return "discord unreachable: " + e.Reason
}

// Sender posts messages to the webhook from a bounded in-memory queue.
type Sender struct {
	client  *http.Client
	queue   chan Message
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
		queue:   make(chan Message, queueSize),
		webhook: webhook,
		origin:  strings.TrimRight(origin, "/"),
		backoff: 2 * time.Second,
		sleep:   sleepCtx,
	}
}

// Enqueue queues m without blocking. A full queue drops the message.
func (s *Sender) Enqueue(m Message) {
	select {
	case s.queue <- m:
	default:
		log.WithField("key", m.Key).Warn("taproot alerts: queue full, dropping alert")
	}
}

// Run delivers queued messages until ctx ends.
func (s *Sender) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-s.queue:
			hook := s.webhook()
			if hook == "" {
				continue
			}
			if err := s.Send(ctx, hook, m); err != nil && ctx.Err() == nil {
				log.WithError(err).WithField("key", m.Key).Warn("taproot alerts: discord delivery failed")
			}
		}
	}
}

// Send posts one message, retrying network errors, 5xx, and 429 a few times.
func (s *Sender) Send(ctx context.Context, webhook string, m Message) error {
	body, err := json.Marshal(payloadFor(m))
	if err != nil {
		return fmt.Errorf("encode alert: %w", err)
	}
	target, err := s.target(webhook)
	if err != nil {
		return err
	}
	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		wait, retry, errPost := s.post(ctx, target, body)
		if errPost == nil {
			return nil
		}
		last = errPost
		if !retry || attempt == maxAttempts {
			break
		}
		if wait == 0 {
			wait = s.backoff << (attempt - 1)
		}
		log.WithError(errPost).WithFields(log.Fields{"key": m.Key, "attempt": attempt, "retry_in": wait.String()}).
			Debug("taproot alerts: retrying discord delivery")
		if err := s.sleep(ctx, wait); err != nil {
			return err
		}
	}
	return last
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
		return retryAfter(resp.Header, raw), true, &DeliveryError{Status: resp.StatusCode}
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
	d := time.Duration(secs * float64(time.Second))
	if d <= 0 {
		d = time.Second
	}
	return min(d, maxRetryAfter)
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
