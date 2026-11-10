package alerts

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Grant data from a read that later reads failed to refresh is not alerted
// on once it is older than two read intervals; a fresh read alerts again.
func TestStaleGrantDataIsNotAlertedOn(t *testing.T) {
	var mu sync.Mutex
	failing := false
	grants := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fail := failing
		mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"cedar_ember":{"eligible":true,"grants":[{"id":"g","resets_left":1,"ends_at":"` + t0.Add(31*time.Hour).Format(time.RFC3339) + `"}]}}`))
	}))
	defer grants.Close()
	setFailing := func(v bool) {
		mu.Lock()
		failing = v
		mu.Unlock()
	}
	h := newHarness(t, openDB(t, t.TempDir()), &fakeDiscord{}, claudeAuth("a", "main"))
	h.svc.grants.url = grants.URL
	h.step(t, 0)
	h.step(t, 0)
	setFailing(true)
	h.step(t, grantInterval)
	h.step(t, grantInterval)
	h.step(t, time.Hour) // 24 h left, from a read 7 h old
	h.titles(t, 0)

	setFailing(false)
	h.step(t, 2*time.Hour)
	h.step(t, 0)
	if got := h.titles(t, 1); got[0] != "Claude reset grant expires soon" {
		t.Fatalf("titles = %q", got)
	}
}
