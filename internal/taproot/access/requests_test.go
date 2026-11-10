package access

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
)

// shareeIP is a device of dana@example.com, a person the server is shared
// with; tailscaled hides its name.
const (
	shareeIP = "100.64.0.31"
	shareeID = "ndana-1CNTRL"
)

// phone is alice@github's phone, which the request harness grants Admin and
// not Proxy.
const (
	phoneIP = "100.64.0.5"
	phoneID = "nphoneCNTRL"
)

// requestHarness is a guard on the fake tailnet with a sharee device, an
// access.addresses entry, a request store, and a clock the test moves.
type requestHarness struct {
	g       *Guard
	ts      *fakeTailscaled
	asks    *Requests
	now     time.Time
	mu      sync.Mutex
	notices []RequestNotice
}

func newRequestHarness(t *testing.T) *requestHarness {
	t.Helper()
	peers := defaultPeers()
	peers[shareeIP] = fakePeer{login: "dana@example.com", displayName: "dana", node: "dana-1", sharee: true}
	peers[phoneIP] = fakePeer{login: testLogin, displayName: "Alice", node: "phone", os: "iOS"}
	ts := newFakeTailscaled(t, peers)
	devices := append(slices.Clone(defaultDevices), DeviceGrant{ID: phoneID, Name: "phone", Admin: true})
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: devices, Addresses: []AddressGrant{{CIDR: "192.0.2.20"}}})
	h := &requestHarness{g: g, ts: ts, now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	g.now = func() time.Time {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.now
	}
	db, err := store.Open(context.Background(), store.Config{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	h.asks = NewRequests(db, func(n RequestNotice) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.notices = append(h.notices, n)
	})
	g.AttachRequests(h.asks)
	return h
}

func (h *requestHarness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *requestHarness) noticeCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.notices)
}

func (h *requestHarness) open(t *testing.T) map[string]AccessRequest {
	t.Helper()
	list, err := h.asks.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]AccessRequest{}
	for _, a := range list {
		out[a.NodeID] = a
	}
	return out
}

func claudeCode() map[string]string {
	return map[string]string{"User-Agent": "claude-cli/2.1.200 (external, cli)"}
}

// Refusals of tailnet devices with an identity become requests, written at
// most once a minute per device with the refusals in between counted, and
// announced once. Nobody else's refusals or requests are recorded.
func TestRefusalsRecordRequests(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)

	for range 3 {
		assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", claudeCode()), "no_proxy")
	}
	got := h.open(t)
	bob, ok := got[bobID]
	if !ok || bob.Count != 1 || bob.Login != "bob@example.org" || bob.OwnerName != "Bob" || bob.Node != "bob-pc" || bob.Client != "Claude Code" || bob.Outside {
		t.Fatalf("after 3 refusals in a minute: %+v", got)
	}
	h.advance(time.Minute)
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", claudeCode()), "no_proxy")
	if bob = h.open(t)[bobID]; bob.Count != 4 || !bob.LastSeen.Equal(h.now) || bob.FirstSeen.Equal(h.now) {
		t.Fatalf("after a minute: %+v, want the skipped refusals counted", bob)
	}
	if n := h.noticeCount(); n != 1 {
		t.Fatalf("notices = %d, want 1 for one request", n)
	}

	// worker has Proxy; its admin refusal asks for more.
	assertDenied(t, do(e, otherNodeIP+":1234", "/v8/management/config", nil), "not_admin")
	// Callers that never become requests: loopback, an address, the server's
	// own node on the admin surface, an admitted device, and an unknown one.
	do(e, "127.0.0.1:1234", "/v8/management/config", nil)
	assertForbidden(t, do(e, "192.0.2.20:1234", "/v8/management/config", nil))
	assertPlainDenied(t, do(e, selfIP+":1234", "/v8/management/config", nil), "server_node")
	do(e, adminIP+":1234", "/v1/models", nil)
	assertForbidden(t, do(e, strayIP+":1234", "/v1/models", nil))

	got = h.open(t)
	if len(got) != 2 || got[workerID].Client != "" {
		t.Fatalf("requests = %+v, want bob-pc and worker only", got)
	}
}

// A dismissed request reopens on the next refusal as a new request.
func TestDismissedRequestReopens(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", nil), "no_proxy")
	dismiss := doMethod(e, http.MethodPost, adminIP+":1234", "/taproot/api/v1/access/requests/"+bobID+"/dismiss", nil)
	if dismiss.Code != http.StatusOK {
		t.Fatalf("dismiss = %d %s", dismiss.Code, dismiss.Body)
	}
	if again := doMethod(e, http.MethodPost, adminIP+":1234", "/taproot/api/v1/access/requests/"+bobID+"/dismiss", nil); again.Code != http.StatusNotFound {
		t.Fatalf("second dismiss = %d, want 404", again.Code)
	}
	if notAdmin := doMethod(e, http.MethodPost, otherNodeIP+":1234", "/taproot/api/v1/access/requests/"+bobID+"/dismiss", nil); notAdmin.Code != http.StatusForbidden {
		t.Fatalf("dismiss without Admin = %d, want 403", notAdmin.Code)
	}
	if len(h.open(t)) != 1 {
		// worker's refused dismiss became its own request.
		t.Fatalf("open after dismiss = %+v, want worker's only", h.open(t))
	}
	h.advance(2 * time.Minute)
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", nil), "no_proxy")
	bob, ok := h.open(t)[bobID]
	if !ok || bob.Count != 1 || !bob.FirstSeen.Equal(h.now) {
		t.Fatalf("reopened request = %+v, %t", bob, ok)
	}
	if n := h.noticeCount(); n != 3 {
		t.Fatalf("notices = %d, want bob, worker, and bob reopened", n)
	}
}

func postRequest(e http.Handler, remote, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, RequestPath, strings.NewReader(body))
	req.RemoteAddr = remote
	req.Host = "localhost:8317"
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// A tailnet device without Admin can ask for access from its browser and
// name itself. Nobody else can, the browser checks apply, and each device
// gets 10 requests an hour.
func TestSelfRequest(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	browser := map[string]string{"Origin": "http://localhost:8317", RequestHeader: "1", "User-Agent": "Mozilla/5.0 (Windows NT 10.0)"}

	rec := postRequest(e, shareeIP+":1234", `{"name": "  Dana's PC  "}`, browser)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"pending"`) {
		t.Fatalf("sharee request = %d %s", rec.Code, rec.Body)
	}
	dana := h.open(t)[shareeID]
	if dana.NameHint != "Dana's PC" || dana.Login != "dana@example.com" || dana.Node != "" || !dana.Outside || dana.Client != "Browser" {
		t.Fatalf("stored request = %+v", dana)
	}
	if n := h.noticeCount(); n != 1 || h.notices[0].Name != "Dana's PC" {
		t.Fatalf("notices = %+v", h.notices)
	}
	// Renaming an open request announces nothing new.
	if rec := postRequest(e, shareeIP+":1234", `{"name": "Dana desktop"}`, browser); rec.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body)
	}
	if h.open(t)[shareeID].NameHint != "Dana desktop" || h.noticeCount() != 1 {
		t.Fatalf("after rename: %+v, %d notices", h.open(t)[shareeID], h.noticeCount())
	}

	for name, tc := range map[string]struct {
		remote, body string
		headers      map[string]string
		status       int
		code         string
	}{
		"no request header": {shareeIP + ":1", `{}`, map[string]string{"Origin": "http://localhost:8317"}, http.StatusForbidden, "request_header_required"},
		"another site":      {shareeIP + ":1", `{}`, map[string]string{"Origin": "http://evil.example.com", RequestHeader: "1"}, http.StatusForbidden, "forbidden"},
		"loopback":          {"127.0.0.1:1", `{}`, browser, http.StatusForbidden, "request_tailnet_only"},
		"an address":        {"192.0.2.20:1", `{}`, browser, http.StatusForbidden, "forbidden"},
		"the server":        {selfIP + ":1", `{}`, browser, http.StatusForbidden, "server_node"},
		"no identity":       {strayIP + ":1", `{}`, browser, http.StatusForbidden, "forbidden"},
		"an admin":          {adminIP + ":1", `{}`, browser, http.StatusConflict, "has_access"},
		"a long name":       {outsiderIP + ":1", `{"name": "` + strings.Repeat("é", MaxLabelRunes+1) + `"}`, browser, http.StatusBadRequest, "invalid_name"},
		"a control char":    {outsiderIP + ":1", `{"name": "bob\u0007"}`, browser, http.StatusBadRequest, "invalid_name"},
	} {
		rec := postRequest(e, tc.remote, tc.body, tc.headers)
		var body deniedBody
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != tc.status || body.Error.Code != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, rec.Code, rec.Body, tc.status, tc.code)
		}
	}
	if _, ok := h.open(t)[bobID]; ok {
		t.Fatal("a refused name still recorded bob-pc's request")
	}

	for i := range 8 {
		if rec := postRequest(e, shareeIP+":1234", `{}`, browser); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d", i+3, rec.Code)
		}
	}
	limited := postRequest(e, shareeIP+":1234", `{}`, browser)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") != "3600" {
		t.Fatalf("11th request = %d %s (Retry-After %q)", limited.Code, limited.Body, limited.Header().Get("Retry-After"))
	}
	if h.open(t)[shareeID].NameHint != "Dana desktop" {
		t.Fatal("an empty name cleared the stored one")
	}
	h.advance(time.Hour)
	if rec := postRequest(e, shareeIP+":1234", `{}`, browser); rec.Code != http.StatusOK {
		t.Fatalf("after an hour = %d", rec.Code)
	}
}

// Claude Code prints the no_proxy message after "Please run /login", so it
// says not to and where to go instead. A sharee's device has no name.
func TestNoProxyMessage(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	body := assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", map[string]string{"Host": "server:8317"}), "no_proxy")
	want := "This device needs approval on taproot. Don't run /login. Open http://server:8317 on this device to name it, or ask the server's admin. (bob-pc, bob@example.org, ID nbob-pcCNTRL)"
	if body.Error.Message != want || body.Error.Node != "bob-pc" || body.Error.NodeID != bobID || body.Error.Login != "bob@example.org" {
		t.Fatalf("no_proxy = %+v, want message %q", body.Error, want)
	}
	// Upstream's buffered connection gives plain http an empty TLS state.
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr, req.Host, req.TLS = outsiderIP+":1234", "server:8317", &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if body := assertDenied(t, rec, "no_proxy"); !strings.Contains(body.Error.Message, "Open http://server:8317 ") {
		t.Fatalf("plain http with an empty TLS state = %q", body.Error.Message)
	}
	req.TLS = &tls.ConnectionState{HandshakeComplete: true}
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if body := assertDenied(t, rec, "no_proxy"); !strings.Contains(body.Error.Message, "Open https://server:8317 ") {
		t.Fatalf("https = %q", body.Error.Message)
	}
	body = assertDenied(t, do(e, shareeIP+":1234", "/v1/models", nil), "no_proxy")
	if !strings.HasSuffix(body.Error.Message, "(unnamed device, dana@example.com, ID "+shareeID+")") || body.Error.Node != "" {
		t.Fatalf("sharee no_proxy = %+v", body.Error)
	}
}

// The not_admin body says whether the device already has an open request.
func TestNotAdminSaysWhetherRequested(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	requested := func() any {
		rec := do(e, outsiderIP+":1234", "/v8/management/config", nil)
		var body struct{ Error map[string]any }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error["code"] != "not_admin" {
			t.Fatalf("not_admin = %d %s", rec.Code, rec.Body)
		}
		return body.Error["requested"]
	}
	if got := requested(); got != false {
		t.Fatalf("first visit requested = %v, want false", got)
	}
	if got := requested(); got != true {
		t.Fatalf("second visit requested = %v, want true", got)
	}
}

// The Access page lists open requests, named by the device when it named
// itself, and the sharee's device with its owner and outside the tailnet.
func TestDevicesListRequestsAndSharees(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", nil), "no_proxy")
	h.advance(time.Second)
	if rec := postRequest(e, shareeIP+":1234", `{"name": "Dana's PC"}`, map[string]string{"Origin": "http://localhost:8317", RequestHeader: "1"}); rec.Code != http.StatusOK {
		t.Fatalf("request = %d %s", rec.Code, rec.Body)
	}
	resp := getDevices(t, h.g)
	if len(resp.Requests) != 2 || resp.Requests[0].NodeID != shareeID || resp.Requests[1].NodeID != bobID {
		t.Fatalf("requests = %+v, want dana's then bob's", resp.Requests)
	}
	if r := resp.Requests[0]; r.Name != "Dana's PC" || r.Login != "dana@example.com" || r.OwnerName != "dana" || !r.Outside || r.Count != 1 || r.Asked != AskAdmin {
		t.Fatalf("dana's request = %+v", r)
	}
	if r := resp.Requests[1]; r.Name != "bob-pc" || r.Outside || r.Asked != AskProxy {
		t.Fatalf("bob's request = %+v", r)
	}
	// Tailscale hides the sharee's name but not its address, which its owner
	// sees in their own admin console.
	dana, ok := findDevice(resp, shareeID)
	if !ok || dana.Name != "" || !dana.Outside || dana.OwnerName != "dana" || dana.Login != "dana@example.com" || dana.OS != "" || dana.Created != nil || !slices.Equal(dana.Addresses, []string{shareeIP}) {
		t.Fatalf("sharee device = %+v", dana)
	}

	// A device that already has both permissions has nothing to ask for.
	if err := h.g.SetGrants(Grants{Devices: append(h.g.Grants().Devices, DeviceGrant{ID: bobID, Proxy: true, Admin: true})}); err != nil {
		t.Fatal(err)
	}
	if resp := getDevices(t, h.g); len(resp.Requests) != 1 {
		t.Fatalf("requests = %+v, want bob's hidden", resp.Requests)
	}
}

// ResolveRequests closes the requests of devices a saved change granted
// something new, and leaves the rest.
func TestResolveRequests(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", nil), "no_proxy")
	assertDenied(t, do(e, otherNodeIP+":1234", "/v8/management/config", nil), "not_admin")
	before := h.g.Grants()
	after := Grants{Devices: append(before.Devices, DeviceGrant{ID: bobID, Proxy: true})}
	h.g.ResolveRequests(context.Background(), before, after)
	got := h.open(t)
	if _, ok := got[bobID]; ok || len(got) != 1 {
		t.Fatalf("requests = %+v, want worker's only", got)
	}
	// The allowed request is kept, so bob's next refusal stays quiet.
	if err := h.g.SetGrants(after); err != nil {
		t.Fatal(err)
	}
	h.advance(2 * time.Minute)
	assertDenied(t, do(e, outsiderIP+":1234", "/v8/management/config", nil), "not_admin")
	if _, ok := h.open(t)[bobID]; ok || h.noticeCount() != 2 {
		t.Fatalf("after bob's dashboard refusal: open %+v, %d notices", h.open(t), h.noticeCount())
	}
}

// A refusal records what the device asked for, keeping the widest: Admin
// implies Proxy.
func TestRequestRecordsWhatWasAsked(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", nil), "no_proxy")
	if got := h.open(t)[bobID].Asked; got != AskProxy {
		t.Fatalf("after a model request asked = %q, want proxy", got)
	}
	// Within the write spacing, the dashboard refusal is held and widens the
	// next write.
	assertDenied(t, do(e, outsiderIP+":1234", "/v8/management/config", nil), "not_admin")
	h.advance(time.Minute)
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", nil), "no_proxy")
	if got := h.open(t)[bobID].Asked; got != AskAdmin {
		t.Fatalf("after a dashboard refusal asked = %q, want admin", got)
	}
	h.advance(time.Minute)
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", nil), "no_proxy")
	if got := h.open(t)[bobID].Asked; got != AskAdmin {
		t.Fatalf("a later model request narrowed asked to %q", got)
	}
}

// Allow is a decision: once the owner allows a request, refusals of what the
// device still lacks update it silently. Only the device's own request from
// the gate reopens it and alerts again, and a denied request reopens on a
// refusal as before.
func TestAllowedRequestStaysClosed(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	allow := func(id string) *httptest.ResponseRecorder {
		return doMethod(e, http.MethodPost, adminIP+":1234", "/taproot/api/v1/access/requests/"+id+"/allow", nil)
	}
	// worker has Proxy, so opening the dashboard asks for Admin.
	assertDenied(t, do(e, otherNodeIP+":1234", "/v8/management/config", nil), "not_admin")
	worker, ok := h.open(t)[workerID]
	if !ok || worker.Asked != AskAdmin || h.noticeCount() != 1 {
		t.Fatalf("worker's request = %+v, %t, %d notices", worker, ok, h.noticeCount())
	}
	// Allow with the defaults keeps Proxy only: the save grants nothing new,
	// so the allow call closes the request.
	h.g.ResolveRequests(context.Background(), h.g.Grants(), h.g.Grants())
	if _, ok := h.open(t)[workerID]; !ok {
		t.Fatal("a save that granted nothing closed the request")
	}
	if rec := allow(workerID); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"allowed":true`) {
		t.Fatalf("allow = %d %s", rec.Code, rec.Body)
	}
	if rec := allow(workerID); rec.Code != http.StatusOK {
		t.Fatalf("allow again = %d %s, want the decision kept", rec.Code, rec.Body)
	}
	if rec := allow(bobID); rec.Code != http.StatusNotFound {
		t.Fatalf("allow without a request = %d, want 404", rec.Code)
	}
	if rec := doMethod(e, http.MethodPost, otherNodeIP+":1234", "/taproot/api/v1/access/requests/"+workerID+"/allow", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("allow without Admin = %d, want 403", rec.Code)
	}

	for range 3 {
		h.advance(2 * time.Minute)
		body := do(e, otherNodeIP+":1234", "/v8/management/config", nil)
		var denial struct{ Error map[string]any }
		if err := json.Unmarshal(body.Body.Bytes(), &denial); err != nil || denial.Error["requested"] != false || denial.Error["proxy"] != true {
			t.Fatalf("not_admin after allow = %s", body.Body)
		}
	}
	if _, ok := h.open(t)[workerID]; ok || h.noticeCount() != 1 {
		t.Fatalf("after refusals of an allowed request: open %+v, %d notices", h.open(t), h.noticeCount())
	}

	// The gate's button asks again: the request reopens with the new name
	// and alerts once.
	browser := map[string]string{"Origin": "http://localhost:8317", RequestHeader: "1"}
	if rec := postRequest(e, otherNodeIP+":1234", `{"name": "Build box"}`, browser); rec.Code != http.StatusOK {
		t.Fatalf("self-request = %d %s", rec.Code, rec.Body)
	}
	worker, ok = h.open(t)[workerID]
	if !ok || worker.NameHint != "Build box" || worker.Count != 1 || !worker.FirstSeen.Equal(h.now) || h.noticeCount() != 2 {
		t.Fatalf("after the gate's request: %+v, %t, %d notices", worker, ok, h.noticeCount())
	}
	if postRequest(e, otherNodeIP+":1234", `{}`, browser).Code != http.StatusOK || h.noticeCount() != 2 {
		t.Fatalf("a second request of an open one alerted again: %d notices", h.noticeCount())
	}

	// Deny, then a refusal reopens it as before.
	if rec := doMethod(e, http.MethodPost, adminIP+":1234", "/taproot/api/v1/access/requests/"+workerID+"/dismiss", nil); rec.Code != http.StatusOK {
		t.Fatalf("dismiss = %d %s", rec.Code, rec.Body)
	}
	h.advance(2 * time.Minute)
	assertDenied(t, do(e, otherNodeIP+":1234", "/v8/management/config", nil), "not_admin")
	if _, ok := h.open(t)[workerID]; !ok || h.noticeCount() != 3 {
		t.Fatalf("after a refusal of a denied request: open %+v, %d notices", h.open(t), h.noticeCount())
	}
}

// The sweep prunes the requests of devices gone from the tailnet for
// ForgetAfter, and decided requests whose device has been quiet for 30 days.
func TestPruneRequests(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	ctx := context.Background()
	const goneID = "ngoneCNTRL"
	if err := h.asks.Record(ctx, RequestInput{NodeID: goneID, Login: "eve@example.com"}, 1, h.now); err != nil {
		t.Fatal(err)
	}
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", nil), "no_proxy")
	assertDenied(t, do(e, otherNodeIP+":1234", "/v8/management/config", nil), "not_admin")
	if _, err := h.asks.Dismiss(ctx, bobID, h.now); err != nil {
		t.Fatal(err)
	}
	if _, err := h.asks.Allow(ctx, workerID, h.now); err != nil {
		t.Fatal(err)
	}
	prune := func() {
		t.Helper()
		tn, err := h.g.ReadTailnet(ctx)
		if err != nil {
			t.Fatal(err)
		}
		h.g.PruneRequests(ctx, tn)
	}
	rows := func() []string {
		t.Helper()
		ids, err := queryNodeIDs(ctx, h.asks.db)
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}

	prune()
	h.advance(ForgetAfter - time.Second)
	prune()
	if got := rows(); len(got) != 3 {
		t.Fatalf("rows a second before ForgetAfter = %v", got)
	}
	// A read without peers counts for nothing.
	h.g.PruneRequests(ctx, Tailnet{})
	h.advance(time.Second)
	prune()
	if got := rows(); len(got) != 2 || slices.Contains(got, goneID) {
		t.Fatalf("rows after ForgetAfter = %v, want the gone device's pruned", got)
	}

	// worker keeps being refused, so its allowed request stays; bob's denied
	// one ages out.
	h.advance(keepDecidedRequests - ForgetAfter - time.Minute)
	assertDenied(t, do(e, otherNodeIP+":1234", "/v8/management/config", nil), "not_admin")
	h.advance(2 * time.Minute)
	prune()
	if got := rows(); len(got) != 1 || got[0] != workerID {
		t.Fatalf("rows after 30 days = %v, want worker's only", got)
	}
	if _, ok := h.open(t)[workerID]; ok {
		t.Fatal("pruning reopened worker's request")
	}
}

// A device outside the tailnet cannot claim, so its denial never offers the
// setup code form. It does say the server is unclaimed, so the gate does not
// offer a request that no admin could allow.
func TestOutsideDeviceIsNotClaimable(t *testing.T) {
	peers := defaultPeers()
	peers[shareeIP] = fakePeer{login: "dana@example.com", displayName: "dana", node: "dana-1", sharee: true}
	ts := newFakeTailscaled(t, peers)
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{}})
	startClaim(t, g)
	e, _ := testEngine(g)
	denial := func(remote string) map[string]any {
		t.Helper()
		var body struct{ Error map[string]any }
		rec := do(e, remote, "/v8/management/config", nil)
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error["code"] != "not_admin" {
			t.Fatalf("admin GET from %s = %d %s", remote, rec.Code, rec.Body)
		}
		return body.Error
	}
	if got := denial(outsiderIP + ":1"); got["claimable"] != true || got["unclaimed"] != true {
		t.Fatalf("own-tailnet device denial = %v, want claimable and unclaimed", got)
	}
	got := denial(shareeIP + ":1")
	if got["claimable"] != nil || got["unclaimed"] != true {
		t.Fatalf("sharee device denial = %v, want unclaimed without claimable", got)
	}

	if err := g.SetGrants(Grants{Devices: []DeviceGrant{{ID: "nOTHER0001CNTRL", Admin: true}}}); err != nil {
		t.Fatal(err)
	}
	if got := denial(shareeIP + ":1"); got["unclaimed"] != nil {
		t.Fatalf("sharee device denial once claimed = %v, want no unclaimed", got)
	}
}

func TestClientName(t *testing.T) {
	for ua, want := range map[string]string{
		"claude-cli/2.1.200 (external, cli)":                "Claude Code",
		"codex_cli_rs/0.80.0 (Ubuntu 24.4.0; x86_64) xterm": "Codex",
		"curl/8.5.0": "curl",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)": "Browser",
		"python-httpx/0.27.0": "",
		"":                    "",
	} {
		if got := clientName(ua); got != want {
			t.Errorf("clientName(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestCleanLabel(t *testing.T) {
	for raw, want := range map[string]string{"  Work laptop ": "Work laptop", "": "", strings.Repeat("é", MaxLabelRunes): strings.Repeat("é", MaxLabelRunes)} {
		if got, err := CleanLabel(raw); err != nil || got != want {
			t.Errorf("CleanLabel(%q) = %q, %v", raw, got, err)
		}
	}
	for _, bad := range []string{
		strings.Repeat("a", MaxLabelRunes+1), "tab\there", "line\nbreak", "\x7f", string([]byte{0xff}),
		// Format characters: a right-to-left override, a zero-width space and
		// joiner, and a byte order mark.
		"lap\u202etop", "lap\u200btop", "a\u200db", "\ufeffphone",
	} {
		if _, err := CleanLabel(bad); err == nil {
			t.Errorf("CleanLabel(%q) accepted", bad)
		}
	}
	if _, err := New(Config{Enabled: new(bool), Devices: []DeviceGrant{{ID: "n1", Proxy: true, Label: "a\nb"}}}); err == nil {
		t.Fatal("a label with a newline loaded")
	}
}

// The status read keeps what tailscaled 1.104.2 reported for a sharee's
// device (measured): a placeholder host name, no MagicDNS name or OS, a 1973
// join date, and a real presence.
func TestDecodeTailnet(t *testing.T) {
	body := `{
		"BackendState": "Running",
		"Self": {"ID": "nserverCNTRL", "HostName": "server", "DNSName": "server.tailnet-0000.ts.net.", "OS": "linux", "UserID": 1, "Online": true},
		"Peer": {
			"nodekey:a": {"ID": "nlaptopCNTRL", "HostName": "Alice's MacBook", "DNSName": "laptop.tailnet-0000.ts.net.", "OS": "macOS", "UserID": 1,
				"Created": "2026-01-02T03:04:05Z", "Online": false, "LastSeen": "2026-11-07T10:00:00Z"},
			"nodekey:b": {"ID": "ndanaCNTRL", "HostName": "device-of-shared-to-user", "DNSName": "", "OS": "", "UserID": 2, "Tags": null,
				"TailscaleIPs": ["fd7a:115c:a1e0::31", "100.64.0.31"],
				"Created": "1973-11-11T00:00:00.1Z", "Online": true, "LastSeen": "0001-01-01T00:00:00Z", "ShareeNode": true},
			"nodekey:c": {"ID": "nworkCNTRL", "HostName": "work", "DNSName": "work-laptop.tailnet-1111.ts.net.", "OS": "windows", "UserID": 3, "Online": true},
			"nodekey:d": {"ID": "nbareCNTRL", "HostName": "bare-host", "DNSName": "", "OS": "linux", "UserID": 1, "TailscaleIPs": ["fd7a:115c:a1e0::4", "bogus"]}
		},
		"User": {
			"1": {"LoginName": "alice@github", "DisplayName": "Alice"},
			"2": {"LoginName": "dana@example.com", "DisplayName": "dana"},
			"3": {"LoginName": "alice@example.org", "DisplayName": "Alice (work)"}
		}
	}`
	tn, err := decodeTailnet([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	byID := tn.byID()
	laptop := byID["nlaptopCNTRL"]
	if laptop.Name != "laptop" || laptop.Outside || laptop.Online || !laptop.LastSeen.Equal(time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)) || laptop.OwnerName != "Alice" {
		t.Errorf("laptop = %+v", laptop)
	}
	dana := byID["ndanaCNTRL"]
	if dana.Name != "" || !dana.Outside || !dana.Online || !dana.LastSeen.IsZero() || !dana.Created.IsZero() || dana.Login != "dana@example.com" || dana.OwnerName != "dana" {
		t.Errorf("sharee = %+v", dana)
	}
	// Tailscale hides a sharee's name but not its addresses, and IPv4 comes first.
	if got := addrStrings(dana.Addresses); !slices.Equal(got, []string{"100.64.0.31", "fd7a:115c:a1e0::31"}) {
		t.Errorf("sharee addresses = %v", got)
	}
	work := byID["nworkCNTRL"]
	if work.Name != "work-laptop.tailnet-1111.ts.net" || !work.Outside || work.Login != "alice@example.org" {
		t.Errorf("other tailnet's device = %+v, want its full name and outside", work)
	}
	if bare := byID["nbareCNTRL"]; bare.Name != "bare-host" || bare.Outside || !slices.Equal(addrStrings(bare.Addresses), []string{"fd7a:115c:a1e0::4"}) {
		t.Errorf("device without MagicDNS = %+v, want only its parsable IPv6", bare)
	}
	if laptop.Addresses != nil {
		t.Errorf("laptop without TailscaleIPs has addresses %v", laptop.Addresses)
	}
	if tn.Self.Name != "server" || !tn.Self.Online {
		t.Errorf("self = %+v", tn.Self)
	}
}

// A label is the owner's: the name refresh never touches it.
func TestReconcileKeepsLabels(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Devices: []DeviceGrant{
		{ID: laptopID, Name: "old-name", Label: "My laptop", Proxy: true, Admin: true},
	}})
	tn, err := g.ReadTailnet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next, _, changed := g.Reconcile(tn)
	if !changed || len(next.Devices) != 1 || next.Devices[0].Name != "laptop" || next.Devices[0].Label != "My laptop" {
		t.Fatalf("Reconcile = %+v, %t", next, changed)
	}
	if v, _ := findDevice(getDevices(t, g), laptopID); v.Label != "My laptop" || v.Name != "laptop" {
		t.Fatalf("listed = %+v", v)
	}
}

// Record, Allow, Dismiss, and Prune read and then write a row. Concurrent
// writers on the shared WAL database must neither fail nor lose an update:
// every refusal is counted and each request opens once.
func TestRequestsConcurrentWriters(t *testing.T) {
	db, err := store.Open(context.Background(), store.Config{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var mu sync.Mutex
	opened := map[string]int{}
	asks := NewRequests(db, func(n RequestNotice) {
		mu.Lock()
		defer mu.Unlock()
		opened[n.NodeID]++
	})
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	const nodes, perNode = 10, 20
	nodeID := func(i int) string { return "n" + strconv.Itoa(i%nodes) + "CNTRL" }

	run := func(n int, fn func(i int) error) []error {
		var wg sync.WaitGroup
		errs := make([]error, n)
		start := make(chan struct{})
		for i := range n {
			wg.Go(func() {
				<-start
				errs[i] = fn(i)
			})
		}
		close(start)
		wg.Wait()
		return slices.DeleteFunc(errs, func(e error) bool { return e == nil })
	}

	errs := run(nodes*perNode, func(i int) error {
		return asks.Record(ctx, RequestInput{NodeID: nodeID(i), Login: "alice@github", Asked: AskProxy}, 1, now)
	})
	if len(errs) > 0 {
		t.Fatalf("%d of %d concurrent Record calls failed, first: %v", len(errs), nodes*perNode, errs[0])
	}
	list, err := asks.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != nodes {
		t.Fatalf("open requests = %d, want %d", len(list), nodes)
	}
	for _, a := range list {
		if a.Count != perNode || opened[a.NodeID] != 1 {
			t.Fatalf("%s: count %d, opened %d times, want %d and 1", a.NodeID, a.Count, opened[a.NodeID], perNode)
		}
	}

	// Refusals interleaved with the owner's decisions and a prune.
	errs = run(nodes*perNode, func(i int) error {
		id := nodeID(i)
		switch i % 10 {
		case 3:
			_, err := asks.Dismiss(ctx, id, now)
			return err
		case 7:
			_, err := asks.Allow(ctx, id, now)
			return err
		case 9:
			return asks.Prune(ctx, Tailnet{}, now)
		}
		return asks.Record(ctx, RequestInput{NodeID: id, Login: "alice@github", Asked: AskAdmin, Self: i%5 == 0}, 1, now)
	})
	if len(errs) > 0 {
		t.Fatalf("%d concurrent writes failed, first: %v", len(errs), errs[0])
	}
	var both int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_requests WHERE allowed_at IS NOT NULL AND dismissed_at IS NOT NULL`).Scan(&both); err != nil || both != 0 {
		t.Fatalf("rows both allowed and denied = %d, %v", both, err)
	}
}

// A device with Admin and no Proxy reads the model list, which the dashboard
// shows, and nothing else on the proxy surface. Its refusals are never
// recorded or announced: it can turn on Proxy itself. Every other caller's
// refusals and admissions are as before.
func TestAdminWithoutProxy(t *testing.T) {
	h := newRequestHarness(t)
	e, _ := testEngine(h.g)
	phone := phoneIP + ":1234"

	if rec := do(e, phone, "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("phone GET /v1/models = %d %s, want 200", rec.Code, rec.Body)
	}
	if rec := do(e, phone, "/v1/models?client_version=0.159.3", nil); rec.Code != http.StatusOK {
		t.Fatalf("phone /v1/models with a query = %d, want 200", rec.Code)
	}
	if rec := do(e, phone, "/v8/management/config", nil); rec.Code != http.StatusOK {
		t.Fatalf("phone admin surface = %d, want 200", rec.Code)
	}
	// Only GET of the exact path; every other proxy request needs
	// Proxy, and each refusal is spaced past the write interval so it would
	// be written if it were recorded.
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/v1/messages"},
		{http.MethodPost, "/v1/models"},
		{http.MethodHead, "/v1/models"},
		{http.MethodGet, "/v1/models/claude-sonnet-5-5"},
		{http.MethodGet, "/v1beta/models"},
	} {
		h.advance(2 * time.Minute)
		body := assertDenied(t, doMethod(e, r.method, phone, r.path, claudeCode()), "no_proxy")
		if !strings.Contains(body.Error.Message, "turn on Proxy for this device on the Access page") || body.Error.NodeID != phoneID {
			t.Fatalf("%s %s denial = %+v, want the admin's advice", r.method, r.path, body.Error)
		}
	}
	// Its own request from the gate is refused too; it has the dashboard.
	gate := map[string]string{"Origin": "http://localhost:8317", RequestHeader: "1"}
	if rec := postRequest(e, phone, `{}`, gate); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "has_access") {
		t.Fatalf("phone self-request = %d %s, want 409 has_access", rec.Code, rec.Body)
	}
	if got := h.open(t); len(got) != 0 || h.noticeCount() != 0 {
		t.Fatalf("after the phone's refusals: requests %+v, %d notices, want none", got, h.noticeCount())
	}

	// While the server's own node is unknown, the guard withholds Admin, so
	// the model list needs Proxy again, but the device's grant still has
	// Admin, so its refusal is still not recorded.
	h.g.selfMu.Lock()
	h.g.selfNode, h.g.selfExpires, h.g.selfRetryAt = nil, time.Time{}, time.Time{}
	h.g.selfMu.Unlock()
	h.ts.failStatus(1)
	h.advance(2 * time.Minute)
	assertDenied(t, do(e, phone, "/v1/models", map[string]string{"Host": "127.0.0.1:8317"}), "no_proxy")
	if got := h.open(t); len(got) != 0 || h.noticeCount() != 0 {
		t.Fatalf("with the server unknown: requests %+v, %d notices, want none", got, h.noticeCount())
	}
	h.advance(2 * time.Minute)
	if rec := do(e, phone, "/v1/models", nil); rec.Code != http.StatusOK {
		t.Fatalf("phone /v1/models after the server is known again = %d %s, want 200", rec.Code, rec.Body)
	}

	// A device with neither permission still becomes a request, from the
	// model list too, and a Proxy-only device's dashboard refusal still asks
	// for Admin.
	assertDenied(t, do(e, outsiderIP+":1234", "/v1/models", nil), "no_proxy")
	assertDenied(t, do(e, otherNodeIP+":1234", "/v8/management/config", nil), "not_admin")
	got := h.open(t)
	if len(got) != 2 || got[bobID].Asked != AskProxy || got[workerID].Asked != AskAdmin {
		t.Fatalf("requests = %+v, want bob-pc asking for proxy and worker for admin", got)
	}
	if n := h.noticeCount(); n != 2 {
		t.Fatalf("notices = %d, want 2", n)
	}

	// Loopback reaches everything, an access.addresses entry and the server's
	// own node reach the proxy only, and none of them is recorded.
	for _, remote := range []string{"127.0.0.1:1234", "192.0.2.20:1234", selfIP + ":1234"} {
		for _, r := range []struct{ method, path string }{{http.MethodGet, "/v1/models"}, {http.MethodPost, "/v1/messages"}} {
			if rec := doMethod(e, r.method, remote, r.path, nil); rec.Code != http.StatusOK {
				t.Fatalf("%s %s %s = %d %s, want 200", remote, r.method, r.path, rec.Code, rec.Body)
			}
		}
	}
	assertForbidden(t, do(e, "192.0.2.20:1234", "/v8/management/config", nil))
	assertPlainDenied(t, do(e, selfIP+":1234", "/v8/management/config", nil), "server_node")
	if got := h.open(t); len(got) != 2 {
		t.Fatalf("requests = %+v, want bob-pc and worker only", got)
	}
}
