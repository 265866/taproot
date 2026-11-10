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
	"unicode/utf8"

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
	// proxied traffic, so CONTRIBUTING.md's no-timeout rule does not apply.
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

// Discord Components V2 component types.
const (
	componentSection   = 9
	componentText      = 10
	componentThumbnail = 11
	componentSeparator = 14
	componentContainer = 17
)

const (
	// flagComponentsV2 marks a message whose content is components only.
	flagComponentsV2 = 1 << 15
	// executeQuery makes Discord accept components from a channel webhook
	// and answer with the created message.
	executeQuery = "wait=true&with_components=true"
	// maxText is Discord's limit on the text of all components of a message.
	// The fixed layout stays far below its 40-component limit.
	maxText        = 4000
	maxTitle       = 256
	maxDescription = 2000
	maxFieldName   = 256
	maxFieldValue  = 1024
	maxFields      = 25
)

// component is one Components V2 component; each type uses its own subset
// of the fields.
type component struct {
	Type        int         `json:"type"`
	AccentColor int         `json:"accent_color,omitempty"`
	Content     string      `json:"content,omitempty"`
	Components  []component `json:"components,omitempty"`
	Accessory   *component  `json:"accessory,omitempty"`
	Media       *media      `json:"media,omitempty"`
	Divider     bool        `json:"divider,omitempty"`
	Spacing     int         `json:"spacing,omitempty"`
}

type media struct {
	URL string `json:"url"`
}

type webhookPayload struct {
	Username        string          `json:"username"`
	AvatarURL       string          `json:"avatar_url"`
	Flags           int             `json:"flags"`
	Components      []component     `json:"components"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

// allowedMentions with no parse list keeps labels from pinging anyone.
type allowedMentions struct {
	Parse []string `json:"parse"`
}

func textDisplay(content string) component {
	return component{Type: componentText, Content: content}
}

// payloadFor lays m out as one card: the title and description beside the
// logo, a divider, the fields as "**Name** value" lines, and a footer with
// the time in Discord's <t:…> markup.
func payloadFor(m Message) webhookPayload {
	title := "### " + truncate(m.Title, maxTitle)
	description := truncate(m.Description, maxDescription)
	footer := fmt.Sprintf("-# Taproot · <t:%d:f>", m.At.Unix())

	header := []component{textDisplay(title)}
	if strings.TrimSpace(description) != "" {
		header = append(header, textDisplay(description))
	}
	card := []component{
		{Type: componentSection, Components: header, Accessory: &component{Type: componentThumbnail, Media: &media{URL: logoURL}}},
		{Type: componentSeparator, Divider: true, Spacing: 1},
	}
	fields := m.Fields[:min(len(m.Fields), maxFields)]
	lines := make([]string, 0, len(fields))
	for _, f := range fields {
		lines = append(lines, "**"+truncate(f.Name, maxFieldName)+"** "+truncate(f.Value, maxFieldValue))
	}
	if len(lines) > 0 {
		budget := maxText - utf8.RuneCountInString(title) - utf8.RuneCountInString(description) - utf8.RuneCountInString(footer)
		card = append(card, textDisplay(truncate(strings.Join(lines, "\n"), budget)))
	}
	card = append(card, textDisplay(footer))

	return webhookPayload{
		Username:        "Taproot",
		AvatarURL:       logoURL,
		Flags:           flagComponentsV2,
		Components:      []component{{Type: componentContainer, AccentColor: severityColors[m.Severity], Components: card}},
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

// errNotSent reports a message the queue skipped because alerts were off, or
// replaced with a newer one for the same key.
var errNotSent = errors.New("not sent")

// errQueueFull reports a message dropped because the queue was full. It
// counts as a failed delivery, so the detector waits before queuing it again.
var errQueueFull = errors.New("alert queue full")

// Sender posts messages to the webhook from an in-memory queue that holds at
// most one message per key. A newer message for a pending key replaces it in
// place. The detector queues a recovery only after its alert was delivered.
type Sender struct {
	client *http.Client

	mu      sync.Mutex
	pending map[string]Message
	order   []string
	ready   chan struct{}
	// busy is set while Run delivers a message it took from the queue.
	busy bool

	webhook func() string
	// origin, when set, replaces the webhook's scheme and host. It exists for
	// end-to-end tests against a local fake Discord.
	origin  string
	backoff time.Duration
	sleep   func(context.Context, time.Duration) error
	// onResult, when set, receives each message's outcome: nil once Discord
	// accepted it, otherwise why it was not delivered. It must not block.
	onResult func(Message, error)
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
	prev, pending := s.pending[m.Key]
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
		s.report(m, errQueueFull)
		return
	}
	if pending {
		s.report(prev, errNotSent)
	}
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

func (s *Sender) report(m Message, err error) {
	if s.onResult != nil {
		s.onResult(m, err)
	}
}

func (s *Sender) next() (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = len(s.order) > 0
	if len(s.order) == 0 {
		return Message{}, false
	}
	key := s.order[0]
	s.order = s.order[1:]
	m := s.pending[key]
	delete(s.pending, key)
	return m, true
}

// idle reports whether the queue is empty and Run is not delivering a message.
func (s *Sender) idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.busy && len(s.order) == 0
}

// Run delivers queued messages until ctx ends.
func (s *Sender) Run(ctx context.Context) {
	for {
		for m, ok := s.next(); ok && ctx.Err() == nil; m, ok = s.next() {
			hook := s.webhook()
			if hook == "" {
				s.report(m, errNotSent)
				continue
			}
			err := s.Send(ctx, hook, m)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				log.WithError(err).WithField("key", m.Key).Warn("taproot alerts: discord delivery failed")
			}
			s.report(m, err)
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

// target is the execute URL: the webhook, or its path on the test origin,
// with executeQuery. ValidateWebhook refuses webhooks that carry a query.
func (s *Sender) target(webhook string) (string, error) {
	u, err := url.Parse(webhook)
	if err != nil {
		return "", &DeliveryError{Reason: "invalid webhook"}
	}
	if s.origin != "" {
		return s.origin + u.EscapedPath() + "?" + executeQuery, nil
	}
	u.RawQuery = executeQuery
	return u.String(), nil
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
