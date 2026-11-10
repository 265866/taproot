package status

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/alerts"
	"github.com/265866/taproot/internal/taproot/backup"
	"github.com/265866/taproot/internal/taproot/ingest"
	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type fakeUsage struct {
	counts func(from, to time.Time) ingest.RequestCounts
	err    error
}

func (f *fakeUsage) LatestQuota(context.Context) ([]ingest.QuotaObservation, error) {
	return nil, f.err
}

func (f *fakeUsage) QuotaHistory(context.Context, time.Time, time.Time) ([]ingest.QuotaSeries, error) {
	return nil, nil
}

func (f *fakeUsage) ClientRequestCounts(_ context.Context, from, to time.Time) (ingest.RequestCounts, error) {
	if f.counts == nil {
		return ingest.RequestCounts{}, nil
	}
	return f.counts(from, to), nil
}

type fakeRequests struct {
	open []access.AccessRequest
	err  error
}

func (f *fakeRequests) OpenRequests(context.Context) ([]access.AccessRequest, error) {
	return f.open, f.err
}

type fakeBackups struct{ listErr error }

func (f *fakeBackups) List() ([]backup.Entry, error) { return nil, f.listErr }
func (*fakeBackups) LastScheduledRun(context.Context) (backup.Run, bool, error) {
	return backup.Run{}, false, nil
}
func (*fakeBackups) NextRun() (time.Time, bool) { return time.Time{}, true }

type fakeAlerts struct{}

func (fakeAlerts) DeliveryHealth() alerts.DeliveryHealth { return alerts.DeliveryHealth{} }

type fakeAuths []*coreauth.Auth

func (f fakeAuths) List() []*coreauth.Auth { return f }

type serviceFixture struct {
	svc      *Service
	clock    time.Time
	failed   int64
	requests *fakeRequests
	backups  *fakeBackups
	usage    *fakeUsage
	engine   *gin.Engine
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	f := &serviceFixture{clock: t0, requests: &fakeRequests{}, backups: &fakeBackups{}}
	f.usage = &fakeUsage{counts: func(from, to time.Time) ingest.RequestCounts {
		return ingest.RequestCounts{Requests: 100, Failed: f.failed, ByProvider: map[string]int64{"claude": 100}}
	}}
	svc, err := New(Deps{Usage: f.usage, Requests: f.requests, Backups: f.backups, Alerts: fakeAlerts{}})
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return f.clock }
	svc.auths = fakeAuths{auth("claude", "alice@example.com"), auth("claude", "bob@example.org")}
	f.svc = svc
	f.engine = gin.New()
	svc.Routes(f.engine.Group("/api"))
	return f
}

func (f *serviceFixture) get(t *testing.T, path string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	f.engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: %v: %s", path, err, rec.Body)
		}
	}
	return rec.Code
}

// The verdict's since is when this server first saw the current state, and
// stays put while the state holds.
func TestStatusSinceFollowsStateChanges(t *testing.T) {
	f := newServiceFixture(t)
	var st Status
	step := func(d time.Duration, failed int64, wantState string, wantSince time.Time) {
		t.Helper()
		f.clock = f.clock.Add(d)
		f.failed = failed
		if code := f.get(t, "/api/status", &st); code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		if st.State != wantState || !st.Since.Equal(wantSince) {
			t.Fatalf("at +%s: state %q since %s, want %q since %s", f.clock.Sub(t0), st.State, st.Since, wantState, wantSince)
		}
	}
	step(0, 0, verdictNormal, t0)
	step(time.Minute, 0, verdictNormal, t0)
	step(time.Minute, 6, verdictDegrade, t0.Add(2*time.Minute))
	step(time.Minute, 7, verdictDegrade, t0.Add(2*time.Minute))
	step(time.Minute, 60, verdictDown, t0.Add(4*time.Minute))
	step(time.Minute, 0, verdictNormal, t0.Add(5*time.Minute))
	if st.Window.Requests != 100 || st.Window.FailureRate == nil || *st.Window.FailureRate != 0 || !st.Window.To.Equal(f.clock) || !st.Window.From.Equal(f.clock.Add(-15*time.Minute)) {
		t.Errorf("window = %+v", st.Window)
	}
}

// An item without a start in its data keeps the time it was first listed,
// and gets a new one when it comes back after clearing.
func TestAttentionFirstSeen(t *testing.T) {
	f := newServiceFixture(t)
	// An access request carries its own start; a pool item does not.
	f.svc.auths = fakeAuths{auth("claude", "alice@example.com", coolingFor(time.Hour))}
	var att Attention
	f.get(t, "/api/attention", &att)
	if len(att.Items) != 1 || !att.Items[0].Since.Equal(t0) {
		t.Fatalf("items = %+v, want the pool item since t0", att.Items)
	}
	f.clock = t0.Add(time.Minute)
	f.get(t, "/api/attention", &att)
	if !att.Items[0].Since.Equal(t0) {
		t.Fatalf("since moved to %s", att.Items[0].Since)
	}
	f.svc.auths = fakeAuths{auth("claude", "alice@example.com")}
	f.clock = t0.Add(2 * time.Minute)
	f.get(t, "/api/attention", &att)
	if len(att.Items) != 0 {
		t.Fatalf("items = %+v after recovery", att.Items)
	}
	f.svc.auths = fakeAuths{auth("claude", "alice@example.com", coolingFor(time.Hour))}
	f.clock = t0.Add(3 * time.Minute)
	f.get(t, "/api/attention", &att)
	if len(att.Items) != 1 || !att.Items[0].Since.Equal(t0.Add(3*time.Minute)) {
		t.Fatalf("items = %+v, want the item back since +3 min", att.Items)
	}
}

func TestTailscaledNoticeOpensAndClosesItem(t *testing.T) {
	f := newServiceFixture(t)
	f.svc.Tailscaled(access.TailscaledNotice{Socket: "/run/tailscale/tailscaled.sock", Down: true, Since: t0.Add(-3 * time.Minute), Err: "connection refused", StaleSocket: true})
	var att Attention
	f.get(t, "/api/attention", &att)
	if it, ok := findItem(att, "tailscaled_down"); !ok || !it.Since.Equal(t0.Add(-3*time.Minute)) || att.Counts.Access != 1 {
		t.Fatalf("items = %+v", att.Items)
	}
	f.svc.Tailscaled(access.TailscaledNotice{Socket: "/run/tailscale/tailscaled.sock", Since: t0.Add(-3 * time.Minute)})
	f.get(t, "/api/attention", &att)
	if len(att.Items) != 0 {
		t.Fatalf("items after recovery = %+v", att.Items)
	}
}

func TestPoolsEndpointAndErrors(t *testing.T) {
	f := newServiceFixture(t)
	var body struct {
		Pools []Pool    `json:"pools"`
		At    time.Time `json:"at"`
	}
	if code := f.get(t, "/api/pools", &body); code != http.StatusOK || len(body.Pools) != 1 || body.Pools[0].UsableAccounts != 2 || !body.At.Equal(t0) {
		t.Fatalf("pools: %d %+v", code, body)
	}
	f.usage.err = errors.New("database is locked")
	for _, path := range []string{"/api/status", "/api/attention", "/api/pools"} {
		if code := f.get(t, path, nil); code != http.StatusInternalServerError {
			t.Errorf("%s with a failing source = %d, want 500", path, code)
		}
	}
}

// Access requests and backups are optional sources: when one cannot be read,
// the verdict and pools still answer, and Needs you says what it cannot list.
func TestUnreadableSourceKeepsTheVerdict(t *testing.T) {
	f := newServiceFixture(t)
	logs := &strings.Builder{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f.requests.err = errors.New("database is locked")
	f.backups.listErr = errors.New("open /data/backups: permission denied")
	var st Status
	if code := f.get(t, "/api/status", &st); code != http.StatusOK || st.State != verdictNormal {
		t.Fatalf("status = %d %+v, want 200 and normal", code, st)
	}
	var pools struct{ Pools []Pool }
	if code := f.get(t, "/api/pools", &pools); code != http.StatusOK || len(pools.Pools) != 1 {
		t.Fatalf("pools = %d %+v", code, pools)
	}
	var att Attention
	if code := f.get(t, "/api/attention", &att); code != http.StatusOK {
		t.Fatalf("attention = %d", code)
	}
	ids := map[string]Item{}
	for _, it := range att.Items {
		ids[it.ID] = it
	}
	acc, bk := ids["unreadable:access"], ids["unreadable:backups"]
	if acc.Title != "Can't read the access requests" || acc.Action.Type != "open_logs" || !strings.Contains(acc.Detail, "database is locked") {
		t.Errorf("access item = %+v", acc)
	}
	if bk.Title != "Can't check the backups" || !strings.Contains(bk.Detail, "permission denied") || len(att.Items) != 2 {
		t.Errorf("items = %+v, want the two unreadable sources and no overdue backup", att.Items)
	}
	if att.Counts.Access != 1 || att.Counts.Backups != 1 {
		t.Errorf("counts = %+v", att.Counts)
	}
	if n := strings.Count(logs.String(), "read access requests"); n != 1 {
		t.Errorf("logged the access failure %d times over three reads, want once:\n%s", n, logs)
	}

	f.requests.err, f.backups.listErr = nil, nil
	if f.get(t, "/api/attention", &att); len(att.Items) != 0 {
		t.Errorf("after the sources read again: %+v", att.Items)
	}
	if !strings.Contains(logs.String(), "access requests read again") {
		t.Errorf("recovery not logged:\n%s", logs)
	}
}
