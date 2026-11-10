package alerts

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/ingest"
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
