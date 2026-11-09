package access

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// requestHarness is a guard on the fake tailnet with a sharee device, an
// access.addresses entry, a request store, and a clock the test moves.
type requestHarness struct {
	g       *Guard
	asks    *Requests
	now     time.Time
	mu      sync.Mutex
	notices []RequestNotice
}

func newRequestHarness(t *testing.T) *requestHarness {
	t.Helper()
	peers := defaultPeers()
	peers[shareeIP] = fakePeer{login: "dana@example.com", displayName: "dana", node: "dana-1", sharee: true}
	ts := newFakeTailscaled(t, peers)
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket, Addresses: []AddressGrant{{CIDR: "192.0.2.20"}}})
	h := &requestHarness{g: g, now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
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
	assertForbidden(t, do(e, selfIP+":1234", "/v8/management/config", nil))
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
		"the server":        {selfIP + ":1", `{}`, browser, http.StatusForbidden, "forbidden"},
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
	if r := resp.Requests[0]; r.Name != "Dana's PC" || r.Login != "dana@example.com" || r.OwnerName != "dana" || !r.Outside || r.Count != 1 {
		t.Fatalf("dana's request = %+v", r)
	}
	if r := resp.Requests[1]; r.Name != "bob-pc" || r.Outside {
		t.Fatalf("bob's request = %+v", r)
	}
	dana, ok := findDevice(resp, shareeID)
	if !ok || dana.Name != "" || !dana.Outside || dana.OwnerName != "dana" || dana.Login != "dana@example.com" || dana.OS != "" || dana.Created != nil {
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
	for _, bad := range []string{strings.Repeat("a", MaxLabelRunes+1), "tab\there", "line\nbreak", "\x7f", string([]byte{0xff})} {
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
				"Created": "1973-11-11T00:00:00.1Z", "Online": true, "LastSeen": "0001-01-01T00:00:00Z", "ShareeNode": true},
			"nodekey:c": {"ID": "nworkCNTRL", "HostName": "work", "DNSName": "work-laptop.tailnet-1111.ts.net.", "OS": "windows", "UserID": 3, "Online": true},
			"nodekey:d": {"ID": "nbareCNTRL", "HostName": "bare-host", "DNSName": "", "OS": "linux", "UserID": 1}
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
	work := byID["nworkCNTRL"]
	if work.Name != "work-laptop.tailnet-1111.ts.net" || !work.Outside || work.Login != "alice@example.org" {
		t.Errorf("other tailnet's device = %+v, want its full name and outside", work)
	}
	if bare := byID["nbareCNTRL"]; bare.Name != "bare-host" || bare.Outside {
		t.Errorf("device without MagicDNS = %+v", bare)
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
