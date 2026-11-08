package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

const fakeToken = "s3cr3t-token_ABCDEFxyz123"

// fakeDiscord records webhook posts and answers with the queued statuses.
type fakeDiscord struct {
	mu       sync.Mutex
	paths    []string
	bodies   [][]byte
	statuses []func(http.ResponseWriter)
}

func (f *fakeDiscord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/api/webhooks/") {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type"))
	f.bodies = append(f.bodies, body)
	var respond func(http.ResponseWriter)
	if len(f.statuses) > 0 {
		respond, f.statuses = f.statuses[0], f.statuses[1:]
	}
	f.mu.Unlock()
	if respond == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	respond(w)
}

func (f *fakeDiscord) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func status(code int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
}

// newTestSender points a sender at srv the way OriginEnv does in production.
func newTestSender(srv *httptest.Server) (*Sender, *[]time.Duration) {
	s := NewSender(func() string { return "" }, srv.URL)
	var waits []time.Duration
	s.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return s, &waits
}

const testWebhook = "https://discord.com/api/webhooks/123456789/" + fakeToken

// lockedBuffer lets the test read log output while the sender goroutine writes it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureLogs(t *testing.T) *lockedBuffer {
	t.Helper()
	buf := &lockedBuffer{}
	prevOut, prevLevel := log.StandardLogger().Out, log.GetLevel()
	log.SetOutput(buf)
	log.SetLevel(log.DebugLevel)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetLevel(prevLevel)
	})
	return buf
}

func TestSendPayloadShape(t *testing.T) {
	fake := &fakeDiscord{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, _ := newTestSender(srv)
	msg := Message{
		Key: "limit:a:five_hour", Severity: SeverityWarning, At: t0,
		Title: "Claude account hit its 5-hour limit", Description: "**acct** is at its limit",
		Fields: []Field{{Name: "Account", Value: "acct", Inline: true}, {Name: "Long", Value: strings.Repeat("x", 2000)}},
	}
	if err := s.Send(context.Background(), testWebhook, msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if fake.paths[0] != "POST /api/webhooks/123456789/"+fakeToken+" application/json" {
		t.Fatalf("request = %q", fake.paths[0])
	}
	var got struct {
		Username string `json:"username"`
		Embeds   []struct {
			Title       string  `json:"title"`
			Description string  `json:"description"`
			Color       int     `json:"color"`
			Timestamp   string  `json:"timestamp"`
			Fields      []Field `json:"fields"`
		} `json:"embeds"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal(fake.bodies[0], &got); err != nil {
		t.Fatalf("payload: %v\n%s", err, fake.bodies[0])
	}
	if got.Username != "taproot" || len(got.Embeds) != 1 || got.AllowedMentions.Parse == nil || len(got.AllowedMentions.Parse) != 0 {
		t.Fatalf("payload = %s", fake.bodies[0])
	}
	e := got.Embeds[0]
	if e.Title != msg.Title || e.Description != msg.Description || e.Color != 0xF59E0B || e.Timestamp != "2026-11-03T12:00:00Z" {
		t.Fatalf("embed = %+v", e)
	}
	if len(e.Fields) != 2 || e.Fields[0] != msg.Fields[0] || len([]rune(e.Fields[1].Value)) != 1024 {
		t.Fatalf("fields = %+v", e.Fields)
	}
}

func TestSendHonorsRetryAfter(t *testing.T) {
	fake := &fakeDiscord{statuses: []func(http.ResponseWriter){
		status(http.StatusTooManyRequests, `{"message":"You are being rate limited.","retry_after":1.5,"global":false}`),
		func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)
		},
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, waits := newTestSender(srv)
	if err := s.Send(context.Background(), testWebhook, Message{Key: "k", Title: "t", At: t0}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if fake.count() != 3 {
		t.Fatalf("posts = %d, want 3", fake.count())
	}
	if len(*waits) != 2 || (*waits)[0] != 1500*time.Millisecond || (*waits)[1] != 3*time.Second {
		t.Fatalf("waits = %v, want [1.5s 3s]", *waits)
	}
}

func TestSendRetriesServerErrorsThenGivesUp(t *testing.T) {
	fake := &fakeDiscord{}
	for range maxAttempts {
		fake.statuses = append(fake.statuses, status(http.StatusBadGateway, ""))
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, waits := newTestSender(srv)
	err := s.Send(context.Background(), testWebhook, Message{Key: "k", Title: "t", At: t0})
	if err == nil || err.Error() != "discord answered 502" {
		t.Fatalf("err = %v", err)
	}
	if fake.count() != maxAttempts || len(*waits) != maxAttempts-1 || (*waits)[1] != 2*(*waits)[0] {
		t.Fatalf("posts = %d, waits = %v", fake.count(), *waits)
	}
}

func TestSendDoesNotRetryClientErrors(t *testing.T) {
	fake := &fakeDiscord{statuses: []func(http.ResponseWriter){status(http.StatusNotFound, `{"message":"Unknown Webhook","code":10015}`)}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, _ := newTestSender(srv)
	if err := s.Send(context.Background(), testWebhook, Message{Key: "k", Title: "t", At: t0}); err == nil || fake.count() != 1 {
		t.Fatalf("err = %v, posts = %d", err, fake.count())
	}
}

// TestQueuedFailuresNeverLogTheWebhook runs the queue against a dead server
// and a 404, and checks that neither the token nor the URL reaches the log.
func TestQueuedFailuresNeverLogTheWebhook(t *testing.T) {
	logs := captureLogs(t)
	fake := &fakeDiscord{statuses: []func(http.ResponseWriter){status(http.StatusNotFound, `{"message":"Unknown Webhook"}`)}}
	srv := httptest.NewServer(fake)
	s, _ := newTestSender(srv)
	s.webhook = func() string { return testWebhook }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	s.Enqueue(Message{Key: "first", Title: "t", At: t0})
	waitFor(t, func() bool { return strings.Contains(logs.String(), "key=first") })

	srv.Close()
	s.Enqueue(Message{Key: "second", Title: "t", At: t0})
	waitFor(t, func() bool {
		return strings.Contains(logs.String(), "key=second") && strings.Contains(logs.String(), "discord delivery failed")
	})

	out := logs.String()
	if strings.Contains(out, fakeToken) || strings.Contains(out, "/api/webhooks") || strings.Contains(out, srv.URL) {
		t.Fatalf("log leaks the webhook:\n%s", out)
	}
	if !strings.Contains(out, "discord answered 404") || !strings.Contains(out, "discord unreachable") {
		t.Fatalf("log should say why delivery failed:\n%s", out)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestValidateWebhook(t *testing.T) {
	for _, ok := range []string{
		"https://discord.com/api/webhooks/123/abc_DEF-9",
		"https://discordapp.com/api/webhooks/123/abc",
		"https://canary.discord.com/api/webhooks/123/abc",
		"https://discord.com/api/v10/webhooks/123/abc",
	} {
		if err := ValidateWebhook(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "http://discord.com/api/webhooks/123/abc", "https://discord.com.evil.test/api/webhooks/123/abc",
		"https://evil.test/api/webhooks/123/abc", "https://discord.com/api/webhooks/abc/abc",
		"https://discord.com/api/webhooks/123", "https://user@discord.com/api/webhooks/123/abc",
		"https://discord.com:8443/api/webhooks/123/abc", "https://discord.com/api/webhooks/123/abc?x=1",
		"https://discord.com/api/webhooks/123/abc/../../x",
	} {
		err := ValidateWebhook(bad)
		if err == nil {
			t.Errorf("%q accepted", bad)
		} else if bad != "" && strings.Contains(err.Error(), bad) {
			t.Errorf("error echoes the URL: %v", err)
		}
	}
}

func TestMaskWebhook(t *testing.T) {
	if got := MaskWebhook(testWebhook); got != "…xyz123" {
		t.Fatalf("mask = %q", got)
	}
	if MaskWebhook("") != "" {
		t.Fatal("empty webhook should mask to empty")
	}
}

// TestSendCapsTotalWaitUnder429 keeps one message from holding the queue
// for minutes when Discord keeps asking for long waits.
func TestSendCapsTotalWaitUnder429(t *testing.T) {
	fake := &fakeDiscord{}
	for range maxAttempts {
		fake.statuses = append(fake.statuses, status(http.StatusTooManyRequests, `{"retry_after":45}`))
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, waits := newTestSender(srv)
	if err := s.Send(context.Background(), testWebhook, Message{Key: "k", Title: "t", At: t0}); err == nil {
		t.Fatal("send should give up")
	}
	var total time.Duration
	for _, w := range *waits {
		total += w
	}
	if total > maxSendWait || len(*waits) != 1 {
		t.Fatalf("waits = %v (total %v), want one 45s wait within %v", *waits, total, maxSendWait)
	}
}

// TestEnqueueCoalescesByKeyAndKeepsRecoveries fills the queue while the
// sender is busy: nothing is dropped, and a recovery replaces its own pending
// alert in place.
func TestEnqueueCoalescesByKeyAndKeepsRecoveries(t *testing.T) {
	fake := &fakeDiscord{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, _ := newTestSender(srv)
	s.webhook = func() string { return testWebhook }
	s.Enqueue(Message{Key: "login:k", Title: "login broken", At: t0})
	for i := range 100 {
		s.Enqueue(Message{Key: "limit:" + itoa(int64(i)), Title: "limit", At: t0})
	}
	s.Enqueue(Message{Key: "login:k", Severity: SeverityRecovered, Title: "login works again", At: t0})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	waitFor(t, s.idle)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.bodies) != 101 {
		t.Fatalf("posts = %d, want 101", len(fake.bodies))
	}
	var first webhookPayload
	if err := json.Unmarshal(fake.bodies[0], &first); err != nil {
		t.Fatal(err)
	}
	if first.Embeds[0].Title != "login works again" {
		t.Fatalf("first post = %q, want the recovery in the alert's place", first.Embeds[0].Title)
	}
}
