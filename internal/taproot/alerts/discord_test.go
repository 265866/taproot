package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	log "github.com/sirupsen/logrus"
)

const fakeToken = "s3cr3t-token_ABCDEFxyz123"

// fakeDiscord records webhook posts and answers with the queued statuses.
// Like Discord, it refuses a post that is not a Components V2 message sent
// with ?wait=true&with_components=true.
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
	f.paths = append(f.paths, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Content-Type"))
	f.bodies = append(f.bodies, body)
	var respond func(http.ResponseWriter)
	if len(f.statuses) > 0 {
		respond, f.statuses = f.statuses[0], f.statuses[1:]
	}
	f.mu.Unlock()
	if problem := v2Problem(r.URL.Query(), body); problem != "" {
		status(http.StatusBadRequest, `{"message":"`+problem+`","code":50035}`)(w)
		return
	}
	if respond == nil {
		status(http.StatusOK, `{"id":"1","flags":32768}`)(w)
		return
	}
	respond(w)
}

// v2Problem reports why Discord would refuse the post, or "" if it would not.
func v2Problem(q url.Values, body []byte) string {
	if q.Get("wait") != "true" || q.Get("with_components") != "true" {
		return "missing wait=true or with_components=true"
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(body, &p); err != nil {
		return "body is not JSON"
	}
	if string(p["flags"]) != "32768" {
		return "flags must be 1<<15"
	}
	if _, ok := p["embeds"]; ok {
		return "a V2 message cannot have embeds"
	}
	if _, ok := p["content"]; ok {
		return "a V2 message cannot have content"
	}
	var components []json.RawMessage
	if json.Unmarshal(p["components"], &components) != nil || len(components) == 0 {
		return "no components"
	}
	return ""
}

// card is the text of a posted message.
type card struct {
	accent                              int
	title, description, details, footer string
	thumbnail                           string
}

func decodeCard(t *testing.T, body []byte) card {
	t.Helper()
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("payload: %v\n%s", err, body)
	}
	if len(p.Components) != 1 || len(p.Components[0].Components) < 3 {
		t.Fatalf("want one container with a section, a divider, and a footer: %s", body)
	}
	box := p.Components[0]
	section, parts := box.Components[0], box.Components
	if section.Type != componentSection || len(section.Components) == 0 || section.Accessory == nil || section.Accessory.Media == nil {
		t.Fatalf("first component is not a section with a thumbnail: %s", body)
	}
	c := card{
		accent:    box.AccentColor,
		title:     strings.TrimPrefix(section.Components[0].Content, "### "),
		footer:    parts[len(parts)-1].Content,
		thumbnail: section.Accessory.Media.URL,
	}
	if len(section.Components) > 1 {
		c.description = section.Components[1].Content
	}
	if len(parts) == 4 {
		c.details = parts[2].Content
	}
	return c
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
		Fields: []Field{{Name: "Account", Value: "acct"}, {Name: "Long", Value: strings.Repeat("x", 2000)}},
	}
	if err := s.Send(context.Background(), testWebhook, msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if fake.paths[0] != "POST /api/webhooks/123456789/"+fakeToken+"?wait=true&with_components=true application/json" {
		t.Fatalf("request = %q", fake.paths[0])
	}
	// The card layout chosen from mockups posted to a test channel.
	want := `{
		"username": "Taproot",
		"avatar_url": "https://raw.githubusercontent.com/265866/taproot/main/internal/taproot/alerts/assets/taproot.png",
		"flags": 32768,
		"allowed_mentions": {"parse": []},
		"components": [{"type": 17, "accent_color": 16096779, "components": [
			{"type": 9, "components": [
				{"type": 10, "content": "### Claude account hit its 5-hour limit"},
				{"type": 10, "content": "**acct** is at its limit"}
			], "accessory": {"type": 11, "media": {"url": "https://raw.githubusercontent.com/265866/taproot/main/internal/taproot/alerts/assets/taproot.png"}}},
			{"type": 14, "divider": true, "spacing": 1},
			{"type": 10, "content": "**Account** acct\n**Long** ` + strings.Repeat("x", 1023) + `…"},
			{"type": 10, "content": "-# Taproot · <t:1793707200:f>"}
		]}]
	}`
	assertJSONEqual(t, fake.bodies[0], want)
}

func assertJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("payload = %s\nwant %s", got, want)
	}
}

// TestPayloadWithoutDescriptionOrFields keeps the card valid when an
// optional part is empty: Discord refuses an empty text display.
func TestPayloadWithoutDescriptionOrFields(t *testing.T) {
	raw, err := json.Marshal(payloadFor(Message{Severity: SeverityRecovered, Title: "t", Description: " ", At: t0}))
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, raw, `{
		"username": "Taproot", "avatar_url": "`+logoURL+`", "flags": 32768, "allowed_mentions": {"parse": []},
		"components": [{"type": 17, "accent_color": 2278750, "components": [
			{"type": 9, "components": [{"type": 10, "content": "### t"}], "accessory": {"type": 11, "media": {"url": "`+logoURL+`"}}},
			{"type": 14, "divider": true, "spacing": 1},
			{"type": 10, "content": "-# Taproot · <t:1793707200:f>"}
		]}]
	}`)
}

// TestPayloadStaysWithinDiscordLimits sends oversized text: the message
// keeps under Discord's 4000 characters of text and 40 components, and the
// title and footer survive.
func TestPayloadStaysWithinDiscordLimits(t *testing.T) {
	m := Message{Title: strings.Repeat("T", 500), Description: strings.Repeat("d", 5000), At: t0}
	for i := range 30 {
		m.Fields = append(m.Fields, Field{Name: "Field " + itoa(int64(i)), Value: strings.Repeat("é", 2000)})
	}
	p := payloadFor(m)
	var count, chars int
	var walk func([]component)
	walk = func(cs []component) {
		for _, c := range cs {
			count++
			chars += utf8.RuneCountInString(c.Content)
			walk(c.Components)
			if c.Accessory != nil {
				walk([]component{*c.Accessory})
			}
		}
	}
	walk(p.Components)
	if count > 40 || chars > maxText {
		t.Fatalf("components = %d, text = %d characters", count, chars)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	c := decodeCard(t, raw)
	if c.title != strings.Repeat("T", maxTitle-1)+"…" || c.footer != "-# Taproot · <t:1793707200:f>" {
		t.Fatalf("title = %q, footer = %q", c.title, c.footer)
	}
	if utf8.RuneCountInString(c.description) != maxDescription || !strings.HasPrefix(c.details, "**Field 0** éé") || strings.Count(c.details, "\n") >= maxFields {
		t.Fatalf("description %d characters, details %q…", utf8.RuneCountInString(c.description), c.details[:40])
	}
}

func TestTargetAddsTheExecuteQuery(t *testing.T) {
	want := testWebhook + "?wait=true&with_components=true"
	if got, err := NewSender(nil, "").target(testWebhook); err != nil || got != want {
		t.Fatalf("target = %q, %v; want %q", got, err, want)
	}
	want = "http://127.0.0.1:9/api/webhooks/123456789/" + fakeToken + "?wait=true&with_components=true"
	if got, err := NewSender(nil, "http://127.0.0.1:9/").target(testWebhook); err != nil || got != want {
		t.Fatalf("origin target = %q, %v; want %q", got, err, want)
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
// alert in place, which is reported as not sent.
func TestEnqueueCoalescesByKeyAndKeepsRecoveries(t *testing.T) {
	fake := &fakeDiscord{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s, _ := newTestSender(srv)
	s.webhook = func() string { return testWebhook }
	var replaced []string
	s.onResult = func(m Message, err error) {
		if err != nil {
			replaced = append(replaced, m.Title+": "+err.Error())
		}
	}
	s.Enqueue(Message{Key: "tailscaled", Severity: SeverityCritical, Title: "outage", At: t0})
	for i := range 100 {
		s.Enqueue(Message{Key: "limit:" + itoa(int64(i)), Title: "limit", At: t0})
	}
	s.Enqueue(Message{Key: "tailscaled", Severity: SeverityRecovered, Title: "reaches tailscaled again", At: t0})
	if !slices.Equal(replaced, []string{"outage: not sent"}) {
		t.Fatalf("replaced = %q", replaced)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	waitFor(t, s.idle)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.bodies) != 101 {
		t.Fatalf("posts = %d, want 101", len(fake.bodies))
	}
	first := decodeCard(t, fake.bodies[0])
	if first.title != "reaches tailscaled again" || first.accent != severityColors[SeverityRecovered] {
		t.Fatalf("first post = %+v, want the recovery in the outage's place", first)
	}
	for i, p := range fake.paths {
		if !strings.HasSuffix(p, "?wait=true&with_components=true application/json") {
			t.Fatalf("post %d = %q", i, p)
		}
	}
}
