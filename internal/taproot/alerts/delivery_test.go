package alerts

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/gin-gonic/gin"
)

const (
	limitTitle    = "Claude account hit its weekly limit"
	recoveryTitle = "Claude account usable again"
	poolTitle     = "Claude pool is empty"
)

func weeklyLimit(idx string, resets time.Time) []ingest.QuotaObservation {
	return []ingest.QuotaObservation{{AuthIndex: idx, Provider: "claude", Window: "seven_day", Utilization: 1, Status: "rejected", ResetsAt: resets, ObservedAt: t0, AccountWide: true}}
}

// posts returns every post's title, failed attempts included, with the first
// characters of the webhook token it went to.
func (h *harness) posts(t *testing.T) []string {
	t.Helper()
	waitFor(t, h.svc.sender.idle)
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	var out []string
	for i, b := range h.fake.bodies {
		path := strings.SplitN(h.fake.paths[i], "?", 2)[0]
		token := path[strings.LastIndex(path, "/")+1:]
		out = append(out, decodeCard(t, b).title+" @"+token[:4])
	}
	return out
}

func unavailable(n int) []func(http.ResponseWriter) {
	out := make([]func(http.ResponseWriter), n)
	for i := range out {
		out[i] = status(http.StatusServiceUnavailable, `{}`)
	}
	return out
}

// A delivery that fails every attempt leaves the alert unsent, so it is sent
// again while the condition holds and its recovery follows it.
func TestFailedAlertIsSentAgain(t *testing.T) {
	fake := &fakeDiscord{statuses: unavailable(maxAttempts)}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, openDB(t, t.TempDir()), fake, a, b)
	h.feed.hook(weeklyLimit(a.EnsureIndex(), t0.Add(48*time.Hour)))
	h.step(t, 0)
	h.posts(t)
	h.step(t, evalInterval) // records the failure; the retry waits
	if got := h.posts(t); len(got) != maxAttempts {
		t.Fatalf("posts = %q, want only the %d failed attempts before the retry delay", got, maxAttempts)
	}
	h.step(t, retryFirst)
	h.step(t, time.Hour)
	h.step(t, 48*time.Hour)
	h.step(t, settle)
	want := []string{limitTitle + " @s3cr", recoveryTitle + " @s3cr"}
	if got := h.posts(t); !slices.Equal(got[maxAttempts:], want) {
		t.Fatalf("posts after the failed attempts = %q, want %q", got[maxAttempts:], want)
	}
}

// An alert that was never delivered gets no recovery when its condition
// clears.
func TestFailedAlertGetsNoRecovery(t *testing.T) {
	fake := &fakeDiscord{statuses: unavailable(maxAttempts)}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, openDB(t, t.TempDir()), fake, a, b)
	h.feed.hook(weeklyLimit(a.EnsureIndex(), t0.Add(30*time.Second)))
	h.step(t, 0)
	h.posts(t)
	h.step(t, time.Minute)
	h.step(t, settle)
	h.step(t, cooldown)
	if got := h.posts(t); len(got) != maxAttempts {
		t.Fatalf("posts = %q, want only the failed alert attempts", got)
	}
}

// Messages dropped because alerts were switched off are posted once alerts
// are back on, while their conditions hold.
func TestAlertsDroppedWhileOffArePostedWhenBackOn(t *testing.T) {
	release := make(chan struct{})
	fake := &fakeDiscord{statuses: []func(http.ResponseWriter){
		func(w http.ResponseWriter) { <-release; status(http.StatusOK, `{"id":"1"}`)(w) },
	}}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, openDB(t, t.TempDir()), fake, a, b)
	h.feed.hook(weeklyLimit(a.EnsureIndex(), t0.Add(48*time.Hour)))
	h.step(t, 0)
	waitFor(t, func() bool { return fake.count() == 1 })
	// b's limit and the empty pool queue behind a's alert, then alerts go off.
	h.feed.hook(weeklyLimit(b.EnsureIndex(), t0.Add(72*time.Hour)))
	h.step(t, time.Minute)
	off, on := false, true
	if err := h.svc.SetConfig(Config{DiscordWebhook: testWebhook, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitFor(t, h.svc.sender.idle)
	if err := h.svc.SetConfig(Config{DiscordWebhook: testWebhook, Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	h.step(t, time.Minute)
	got := h.posts(t)
	want := []string{limitTitle + " @s3cr", limitTitle + " @s3cr", poolTitle + " @s3cr"}
	if !slices.Equal(got, want) {
		t.Fatalf("posts = %q, want %q", got, want)
	}
}

// A new webhook gets the alerts that are still open, so its recoveries are
// not orphans.
func TestNewWebhookGetsTheOpenAlerts(t *testing.T) {
	fake := &fakeDiscord{}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, openDB(t, t.TempDir()), fake, a, b)
	h.feed.hook(weeklyLimit(a.EnsureIndex(), t0.Add(48*time.Hour)))
	h.step(t, 0)
	h.titles(t, 1)
	h.step(t, time.Minute)
	if err := h.svc.SetConfig(Config{DiscordWebhook: "https://discord.com/api/webhooks/123456789/NEWCHANNELtoken"}); err != nil {
		t.Fatal(err)
	}
	h.svc.AnnounceWebhook()
	h.step(t, 0)
	h.posts(t)
	h.step(t, time.Hour)
	h.step(t, 48*time.Hour)
	h.step(t, settle)
	want := []string{limitTitle + " @s3cr", "Webhook connected @NEWC", limitTitle + " @NEWC", recoveryTitle + " @NEWC"}
	if got := h.posts(t); !slices.Equal(got, want) {
		t.Fatalf("posts = %q, want %q", got, want)
	}
}

// While Discord holds the queue, an alert whose condition clears waits for
// its delivery outcome before its recovery is queued, so the channel never
// gets the recovery alone.
func TestRecoveryWaitsForItsAlert(t *testing.T) {
	release := make(chan struct{})
	fake := &fakeDiscord{statuses: []func(http.ResponseWriter){
		func(w http.ResponseWriter) { <-release; status(http.StatusOK, `{"id":"1"}`)(w) },
	}}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, openDB(t, t.TempDir()), fake, a, b)
	h.svc.AccessRequested(AccessRequest{NodeID: "n1", Login: "alice@github", At: t0})
	waitFor(t, func() bool { return fake.count() == 1 })
	h.feed.hook(weeklyLimit(a.EnsureIndex(), t0.Add(time.Minute)))
	h.step(t, 0)
	h.step(t, 2*time.Minute)
	h.step(t, settle)
	close(release)
	h.posts(t)
	h.step(t, evalInterval)
	want := []string{"Access requested @s3cr", limitTitle + " @s3cr", recoveryTitle + " @s3cr"}
	if got := h.posts(t); !slices.Equal(got, want) {
		t.Fatalf("posts = %q, want %q", got, want)
	}
}

// An alert still in the queue at shutdown was never delivered, so it is sent
// after the restart.
func TestQueuedAlertIsSentAfterRestart(t *testing.T) {
	db := openDB(t, t.TempDir())
	release := make(chan struct{})
	defer close(release)
	stuck := &fakeDiscord{statuses: []func(http.ResponseWriter){
		func(w http.ResponseWriter) { <-release },
	}}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, db, stuck, a, b)
	h.feed.hook(weeklyLimit(a.EnsureIndex(), t0.Add(48*time.Hour)))
	h.step(t, 0)

	fake := &fakeDiscord{}
	h2 := newHarnessAt(t, db, fake, h.now.Add(time.Minute), a, b)
	h2.svc.observe(weeklyLimit(a.EnsureIndex(), t0.Add(48*time.Hour)))
	h2.step(t, 0)
	if got := h2.titles(t, 1); got[0] != limitTitle {
		t.Fatalf("titles = %q", got)
	}
}

// A recovery still in the queue at shutdown was never delivered, so it is
// sent after the restart.
func TestQueuedRecoveryIsSentAfterRestart(t *testing.T) {
	db := openDB(t, t.TempDir())
	release := make(chan struct{})
	defer close(release)
	stuck := &fakeDiscord{statuses: []func(http.ResponseWriter){
		status(http.StatusOK, `{"id":"1"}`),
		func(w http.ResponseWriter) { <-release },
	}}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, db, stuck, a, b)
	h.feed.hook(weeklyLimit(a.EnsureIndex(), t0.Add(time.Hour)))
	h.step(t, 0)
	h.posts(t)
	h.step(t, 2*time.Hour)
	h.step(t, settle)
	waitFor(t, func() bool { return stuck.count() == 2 })

	fake := &fakeDiscord{}
	h2 := newHarnessAt(t, db, fake, h.now.Add(time.Minute), a, b)
	h2.step(t, 0)
	if got := h2.titles(t, 1); got[0] != recoveryTitle {
		t.Fatalf("titles = %q", got)
	}
}

// An alert dropped because the queue is full counts as a failed delivery,
// so it waits out the retry delay instead of being queued again at once.
func TestAlertDroppedFromAFullQueueWaitsForItsRetry(t *testing.T) {
	release := make(chan struct{})
	fake := &fakeDiscord{statuses: []func(http.ResponseWriter){
		func(w http.ResponseWriter) { <-release; status(http.StatusOK, `{"id":"1"}`)(w) },
	}}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, openDB(t, t.TempDir()), fake, a, b)
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free)
	var drops atomic.Int32
	report := h.svc.sender.onResult
	h.svc.sender.onResult = func(m Message, err error) {
		if m.tracked && err != nil {
			drops.Add(1)
		}
		report(m, err)
	}
	for i := range maxPending + 1 {
		h.svc.sender.Enqueue(Message{Key: fmt.Sprintf("filler:%d", i), Title: "filler", At: t0})
		if i == 0 {
			waitFor(t, func() bool { return fake.count() == 1 })
		}
	}
	h.feed.hook(weeklyLimit(a.EnsureIndex(), t0.Add(48*time.Hour)))
	for range 5 {
		h.step(t, time.Second)
	}
	if got := drops.Load(); got != 1 {
		t.Fatalf("the alert was dropped %d times before its retry delay, want 1", got)
	}
	free()
	h.posts(t)
	h.step(t, retryFirst)
	if got := h.posts(t); got[len(got)-1] != limitTitle+" @s3cr" {
		t.Fatalf("last post = %q, want the alert after its retry delay", got[len(got)-1])
	}
}

// DeliveryHealth counts failed deliveries until one succeeds, for the
// Dashboard's "alerts are not reaching Discord" item.
func TestDeliveryHealthTracksFailuresUntilASuccess(t *testing.T) {
	fake := &fakeDiscord{statuses: unavailable(maxAttempts)}
	a, b := claudeAuth("a", "main"), claudeAuth("b", "spare")
	h := newHarness(t, openDB(t, t.TempDir()), fake, a, b)
	if got := h.svc.DeliveryHealth(); got.Failures != 0 || !got.Active {
		t.Fatalf("before any delivery: %+v, want active with no failures", got)
	}
	h.feed.hook(weeklyLimit(a.EnsureIndex(), t0.Add(48*time.Hour)))
	h.step(t, 0)
	h.posts(t)
	got := h.svc.DeliveryHealth()
	if got.Failures != 1 || got.FailingSince.IsZero() || !strings.Contains(got.LastError, "503") || strings.Contains(got.LastError, "s3cr") {
		t.Fatalf("after a failed delivery: %+v, want one failure with Discord's status and no webhook token", got)
	}
	h.step(t, evalInterval)
	h.step(t, retryFirst)
	h.posts(t)
	if got := h.svc.DeliveryHealth(); got.Failures != 0 || got.LastSuccess.IsZero() || got.LastError != "" {
		t.Fatalf("after a delivered retry: %+v, want the failures cleared", got)
	}
}

// An alert whose delivery failed and whose condition then cleared queues
// nothing more, so the Dashboard's item must clear another way: a successful
// test send, or a new start of alerts.
func TestDeliveryHealthClearsOnATestSendOrARestart(t *testing.T) {
	fail := func(h *harness) {
		h.svc.recordDelivery(errors.New("discord answered 503"))
		if got := h.svc.DeliveryHealth(); got.Failures != 1 {
			t.Fatalf("after a failed delivery: %+v", got)
		}
	}
	fake := &fakeDiscord{}
	h := newHarness(t, openDB(t, t.TempDir()), fake)
	r := gin.New()
	h.svc.Routes(r.Group("/taproot/api/v1"))

	fail(h)
	fake.mu.Lock()
	fake.statuses = append(fake.statuses, status(http.StatusNotFound, `{"message":"Unknown Webhook"}`))
	fake.mu.Unlock()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/taproot/api/v1/alerts/test", nil))
	if got := h.svc.DeliveryHealth(); w.Code != http.StatusBadGateway || got.Failures != 1 {
		t.Fatalf("a failed test send = %d, health %+v; want it not counted", w.Code, got)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/taproot/api/v1/alerts/test", nil))
	if got := h.svc.DeliveryHealth(); w.Code != http.StatusOK || got.Failures != 0 || got.LastSuccess.IsZero() {
		t.Fatalf("a successful test send = %d, health %+v; want the failures cleared", w.Code, got)
	}

	fail(h)
	off := false
	if err := h.svc.SetConfig(Config{DiscordWebhook: testWebhook, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SetConfig(Config{DiscordWebhook: testWebhook}); err != nil {
		t.Fatal(err)
	}
	if got := h.svc.DeliveryHealth(); got.Failures != 0 || !got.Active {
		t.Fatalf("after turning alerts off and on: %+v, want a fresh count", got)
	}
	fail(h)
	if err := h.svc.SetConfig(Config{DiscordWebhook: testWebhook}); err != nil {
		t.Fatal(err)
	}
	if got := h.svc.DeliveryHealth(); got.Failures != 1 {
		t.Fatalf("an unchanged config: %+v, want the failures kept", got)
	}
}
