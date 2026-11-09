package taproot

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/alerts"
)

// discordPosts collects the bodies posted to a fake Discord webhook.
type discordPosts struct {
	mu     sync.Mutex
	bodies []string
}

func (d *discordPosts) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	d.mu.Lock()
	d.bodies = append(d.bodies, string(body))
	d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"id":"1"}`)
}

// waitPosts waits until n bodies arrived and returns them.
func (d *discordPosts) waitPosts(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		d.mu.Lock()
		got := append([]string(nil), d.bodies...)
		d.mu.Unlock()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("Discord got %d posts, want %d", len(got), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Through the real server, a refused device opens one request, which posts
// one Discord message however often the device is refused. The request
// endpoint opens a second device's request, and its message arrives next:
// the queue is in order, so a repeat of the first would have come before it.
func TestRefusalsPostOneAlertPerRequest(t *testing.T) {
	posts := &discordPosts{}
	discord := httptest.NewServer(posts)
	t.Cleanup(discord.Close)
	t.Setenv(alerts.OriginEnv, discord.URL)
	tp := accessYAML(fakeLocalAPI(t)) + "backups:\n  daily-at: \"off\"\nalerts:\n  discord-webhook: https://discord.com/api/webhooks/1/token\n"
	f, err := setupServer(t, tp)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	refuse := func() {
		t.Helper()
		rec := f.do(req{remote: newIP, method: http.MethodPost, path: "/v1/messages", body: `{}`, headers: map[string]string{"User-Agent": "claude-cli/2.1.200 (external, cli)"}})
		if guardCode(rec) != "no_proxy" {
			t.Fatalf("refusal = %d %s", rec.Code, rec.Body)
		}
	}
	for range 3 {
		refuse()
	}
	first := posts.waitPosts(t, 1)
	if !strings.Contains(first[0], "new-laptop") || !strings.Contains(first[0], "Claude Code") {
		t.Fatalf("first post = %s", first[0])
	}
	for range 3 {
		refuse()
	}
	rec := f.do(req{remote: workerIP, method: http.MethodPost, path: "/taproot/api/v1/access/request", body: `{"name":"Build box"}`,
		headers: map[string]string{"Origin": "http://" + host, "X-Taproot-Request": "1", "Content-Type": "application/json"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("request = %d %s", rec.Code, rec.Body)
	}
	got := posts.waitPosts(t, 2)
	if len(got) != 2 || !strings.Contains(got[1], "Build box") {
		t.Fatalf("posts = %q, want new-laptop's once, then worker's", got)
	}
}
