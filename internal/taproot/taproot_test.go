package taproot

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/api"
	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/taproot/access"
	sdkaccess "github.com/265866/taproot/sdk/access"
	"github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
)

// An example tailnet, in the shape tailscale status and whois report it.
const (
	tailnet = "tailnet-0000.ts.net"
	selfIP  = "100.64.0.1"
	adminIP = "100.64.0.2"
	// worker has Proxy but not Admin.
	workerIP = "100.101.1.2"
	// newIP is a device nobody gave a permission.
	newIP = "100.64.0.21"
	lanIP = "192.168.0.88"
	login = "alice@github"
	host  = "server:8317"
	// Fake token values; the test checks they never leave the server cross-origin.
	refreshToken = "sk-ant-ort-TESTONLY"
)

// fakeLocalAPI serves tailscaled's whois and status endpoints on a real unix
// socket, with the node "server" as the server's own node.
func fakeLocalAPI(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "ts.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]string{selfIP: "server", adminIP: "desktop", workerIP: "worker", newIP: "new-laptop"}
	mux := http.NewServeMux()
	mux.HandleFunc("/localapi/v0/whois", func(w http.ResponseWriter, r *http.Request) {
		node, ok := nodes[r.FormValue("addr")]
		if !ok {
			http.Error(w, "no match for IP:port", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Node":        map[string]any{"StableID": "n" + node + "CNTRL", "Name": node + "." + tailnet + ".", "ComputedName": node},
			"UserProfile": map[string]any{"LoginName": "alice@github", "DisplayName": "Alice"},
		})
	})
	mux.HandleFunc("/localapi/v0/status", func(w http.ResponseWriter, r *http.Request) {
		status := map[string]any{"BackendState": "Running", "Self": map[string]any{
			"ID": "nserverCNTRL", "DNSName": "server." + tailnet + ".", "TailscaleIPs": []string{selfIP, "fd7a:115c:a1e0::1"}, "OS": "linux", "UserID": 1,
		}}
		if r.FormValue("peers") == "true" {
			peers := map[string]any{}
			for addr, node := range nodes {
				if node != "server" {
					peers["nodekey:"+node] = map[string]any{
						"ID": "n" + node + "CNTRL", "DNSName": node + "." + tailnet + ".", "HostName": node, "OS": "linux", "UserID": 1,
						"TailscaleIPs": []string{addr}, "Created": "2026-01-02T03:04:05Z",
					}
				}
			}
			status["Peer"] = peers
			status["User"] = map[string]any{"1": map[string]any{"LoginName": login}}
		}
		_ = json.NewEncoder(w).Encode(status)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return socket
}

type fixture struct {
	h           http.Handler
	taprootYAML string
	auths       *auth.Manager
}

// accessYAML is the access section of the test tailnet: desktop has both
// permissions, worker has Proxy, and addresses reach the proxy.
func accessYAML(socket string, addresses ...string) string {
	tp := "access:\n  devices:\n" +
		"    - {id: ndesktopCNTRL, name: desktop, proxy: true, admin: true}\n" +
		"    - {id: nworkerCNTRL, name: worker, proxy: true}\n" +
		"  tailscale-socket: " + filepath.ToSlash(socket) + "\n  addresses:\n"
	for _, addr := range addresses {
		tp += "    - cidr: " + addr + "/32\n"
	}
	return tp
}

// newServer builds the real server the way cmd/server does: taproot.Setup
// first, then api.NewServer with its options.
func newServer(t *testing.T) fixture {
	t.Helper()
	tp := accessYAML(fakeLocalAPI(t), lanIP) + "backups:\n  daily-at: \"off\"\n"
	f, err := setupServer(t, tp)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return f
}

// setupServer writes config.yaml, an auth file, and taproot.yaml (unless tp is
// empty) to a fresh directory, then runs Setup and builds the server.
func setupServer(t *testing.T, tp string) (fixture, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("MANAGEMENT_PASSWORD", "")
	if err := os.Unsetenv("MANAGEMENT_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "taproot-security")
	if err != nil {
		t.Fatal(err)
	}
	// The store stays open for the process, so on Windows removal is best effort.
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	authDir := filepath.Join(dir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	token := `{"type":"claude","access_token":"sk-ant-oat-TESTONLY","refresh_token":"` + refreshToken + `"}`
	if err := os.WriteFile(filepath.Join(authDir, "claude-x.json"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 8317\nauth-dir: "+filepath.ToSlash(authDir)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	taprootYAML := filepath.Join(dir, "taproot.yaml")
	if tp != "" {
		if err := os.WriteFile(taprootYAML, []byte(tp), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{}
	cfg.Port = 8317
	cfg.AuthDir = authDir
	opts, running, err := setup(context.Background(), cfg, configPath)
	if err != nil {
		return fixture{}, err
	}
	t.Cleanup(running.stop)
	auths := auth.NewManager(nil, nil, nil)
	srv := api.NewServer(cfg, auths, sdkaccess.NewManager(), configPath, opts...)
	return fixture{h: srv.Handler(), taprootYAML: taprootYAML, auths: auths}, nil
}

type req struct {
	method, remote, host, path, body string
	headers                          map[string]string
}

func (f fixture) do(r req) *httptest.ResponseRecorder {
	if r.method == "" {
		r.method = http.MethodGet
	}
	if r.host == "" {
		r.host = host
	}
	hr := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	hr.RemoteAddr = r.remote + ":40000"
	hr.Host = r.host
	for k, v := range r.headers {
		hr.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, hr)
	return rec
}

// guardCode returns the code of the guard's 403, or "" for any other response.
func guardCode(rec *httptest.ResponseRecorder) string {
	if rec.Code != http.StatusForbidden {
		return ""
	}
	var body struct{ Error struct{ Code string } }
	if json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		return ""
	}
	switch body.Error.Code {
	case "forbidden", "guard_disabled", "no_proxy", "not_admin", "request_header_required", "server_node", "server_node_unknown", "unknown_host":
		return body.Error.Code
	}
	return ""
}

func isGuardDenial(rec *httptest.ResponseRecorder) bool {
	return guardCode(rec) != ""
}

// assertDenied checks the guard answered 403 before upstream's CORS
// middleware, so a browser cannot read the response.
func assertDenied(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if !isGuardDenial(rec) {
		t.Errorf("%s = %d %.200s, want the guard's 403", what, rec.Code, rec.Body)
	}
	if acao := rec.Header().Get("Access-Control-Allow-Origin"); acao != "" {
		t.Errorf("%s carried Access-Control-Allow-Origin %q", what, acao)
	}
	if strings.Contains(rec.Body.String(), refreshToken) {
		t.Errorf("%s leaked the refresh token", what)
	}
}

var crossSite = map[string]string{"Origin": "http://evil.example", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors"}

// TestCrossOriginBrowserRequestsAreRefused replays the security review's
// repro (finding 8): a page on another site, in the admin's browser on an
// allowlisted node, reads tokens, edits the allowlist, and creates and
// restores backups through upstream's wildcard CORS.
func TestCrossOriginBrowserRequestsAreRefused(t *testing.T) {
	f := newServer(t)
	before, err := os.ReadFile(f.taprootYAML)
	if err != nil {
		t.Fatal(err)
	}
	textPlain := map[string]string{"Content-Type": "text/plain"}
	for k, v := range crossSite {
		textPlain[k] = v
	}
	for _, tc := range []struct {
		what string
		r    req
	}{
		{"token download", req{path: "/v0/management/auth-files/download?name=claude-x.json", headers: crossSite}},
		{"settings preflight", req{method: http.MethodOptions, path: "/taproot/api/v1/settings", headers: map[string]string{
			"Origin": "http://evil.example", "Access-Control-Request-Method": "PUT", "Access-Control-Request-Headers": "content-type",
		}}},
		{"access edit", req{method: http.MethodPut, path: "/taproot/api/v1/settings", headers: textPlain, body: `{"access":{"devices":[{"id":"nworkerCNTRL","proxy":true,"admin":true}]}}`}},
		{"device list", req{path: "/taproot/api/v1/access/devices", headers: crossSite}},
		{"backup create", req{method: http.MethodPost, path: "/taproot/api/v1/backups", headers: textPlain}},
		{"backup restore", req{method: http.MethodPost, path: "/taproot/api/v1/backups/20261102T000000Z/restore", headers: textPlain}},
		{"v8 config", req{path: "/v8/management/config", headers: crossSite}},
		{"dashboard fetch", req{path: "/", headers: crossSite}},
		{"dashboard in a cross-site iframe", req{path: "/", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}}},
		{"proxy spend", req{method: http.MethodPost, path: "/v1/messages", headers: textPlain, body: `{}`}},
		{"loopback with foreign origin", req{remote: "127.0.0.1", host: "localhost:8317", path: "/v8/management/config", headers: map[string]string{"Origin": "http://evil.example"}}},
		{"codex websocket", req{path: "/v1/responses", headers: map[string]string{
			"Origin": "http://evil.example", "Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
		}}},
		{"management websocket", req{path: "/v1/ws", headers: map[string]string{
			"Origin": "http://evil.example", "Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
		}}},
		{"dns rebinding dashboard", req{host: "rebind.evil.example:8317", path: "/"}},
		{"dns rebinding token download", req{host: "rebind.evil.example:8317", path: "/v0/management/auth-files/download?name=claude-x.json"}},
		{"dns rebinding proxy", req{host: "rebind.evil.example:8317", method: http.MethodPost, path: "/v1/messages", body: `{}`}},
	} {
		if tc.r.remote == "" {
			tc.r.remote = adminIP
		}
		assertDenied(t, f.do(tc.r), tc.what)
	}
	after, err := os.ReadFile(f.taprootYAML)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("taproot.yaml changed:\n%s", after)
	}
}

// TestSameOriginDashboardWorks covers what the dashboard itself sends.
func TestSameOriginDashboardWorks(t *testing.T) {
	f := newServer(t)
	sameOrigin := map[string]string{"Origin": "http://" + host, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors"}

	rec := f.do(req{remote: adminIP, path: "/", headers: map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("GET / = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("Content-Security-Policy") != "frame-ancestors 'none'" {
		t.Fatalf("dashboard framing headers = %q / %q", rec.Header().Get("X-Frame-Options"), rec.Header().Get("Content-Security-Policy"))
	}
	// Following a link from another site to the dashboard is a top-level navigation.
	if rec := f.do(req{remote: adminIP, host: "server." + tailnet + ":8317", path: "/", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}}); rec.Code != http.StatusOK {
		t.Fatalf("link to the dashboard = %d", rec.Code)
	}

	for _, r := range []req{
		{path: "/taproot/api/v1/me"},
		{path: "/taproot/api/v1/settings"},
		{path: "/v8/management/config"},
		{path: "/v0/management/auth-files/download?name=claude-x.json"},
		{path: "/taproot/api/v1/access/devices"},
		{method: http.MethodPut, path: "/taproot/api/v1/settings", body: `{"access":{"devices":[{"id":"ndesktopCNTRL","name":"desktop","proxy":true,"admin":true},{"id":"nworkerCNTRL","name":"worker","proxy":true}]}}`},
	} {
		r.remote = adminIP
		r.headers = sameOrigin
		if rec := f.do(r); rec.Code != http.StatusOK {
			t.Errorf("same-origin %s %s = %d %.200s", r.method, r.path, rec.Code, rec.Body)
		}
	}
	rec = f.do(req{remote: adminIP, path: "/taproot/api/v1/me", headers: sameOrigin})
	var me struct {
		Login, Node, Via string
		Admin            bool
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.Node != "desktop."+tailnet || me.Via != "tailnet" || !me.Admin {
		t.Fatalf("/me = %s", rec.Body)
	}
	// Admin responses other than the dashboard may be framed by the dashboard only.
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("/me X-Frame-Options = %q, want SAMEORIGIN", got)
	}
}

// TestSurfaces checks who reaches the proxy and the admin surface, as the
// security model in package access describes (finding 9).
func TestSurfaces(t *testing.T) {
	f := newServer(t)
	adminPaths := []string{"/taproot/api/v1/me", "/v8/management/config", "/v0/management/auth-files/download?name=claude-x.json", "/v0/resource/plugins/demo/index.html", "/anthropic/callback?state=x"}
	for _, tc := range []struct {
		name, remote, host string
		admin              bool
	}{
		// Every process and container on the server host connects from its own node.
		{"server's own node", selfIP, selfIP + ":8317", false},
		// Host-local callers using a 127.0.1.1 publish arrive from the LAN address.
		{"address in access.addresses", lanIP, "127.0.1.1:8317", false},
		{"device with Admin", adminIP, host, true},
		// Security recheck F1: another device of the same login is not an admin.
		{"device with Proxy only", workerIP, host, false},
		{"loopback", "127.0.0.1", "localhost:8317", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Claude Code 2.1.316 and Codex 0.159.3 send no Origin or Sec-Fetch-* headers.
			rec := f.do(req{remote: tc.remote, host: tc.host, method: http.MethodPost, path: "/v1/messages", body: `{}`, headers: map[string]string{"Anthropic-Version": "2023-06-01", "User-Agent": "claude-cli/2.1.316"}})
			if isGuardDenial(rec) {
				t.Fatalf("proxy POST /v1/messages denied: %s", rec.Body)
			}
			for _, path := range adminPaths {
				rec := f.do(req{remote: tc.remote, host: tc.host, path: path})
				if tc.admin && isGuardDenial(rec) {
					t.Errorf("admin %s denied", path)
				}
				if !tc.admin && !isGuardDenial(rec) {
					t.Errorf("admin %s = %d, want the guard's 403", path, rec.Code)
				}
			}
			// The dashboard page is static, and every identified caller may load it.
			if rec := f.do(req{remote: tc.remote, host: tc.host, path: "/"}); rec.Code != http.StatusOK {
				t.Errorf("GET / = %d, want 200", rec.Code)
			}
		})
	}
	if rec := f.do(req{remote: "203.0.113.9", path: "/"}); !isGuardDenial(rec) {
		t.Errorf("GET / from a stranger = %d, want the guard's 403", rec.Code)
	}
	for _, remote := range []string{adminIP, selfIP, "127.0.0.1", "203.0.113.9"} {
		if rec := f.do(req{remote: remote, path: "/management.html"}); rec.Code != http.StatusNotFound {
			t.Errorf("/management.html from %s = %d, want 404", remote, rec.Code)
		}
	}
}

// Security recheck F1: a device without Admin gets no tokens, and its 403
// names the device and the switch to turn on.
func TestDeviceWithoutAdminIsTold(t *testing.T) {
	f := newServer(t)
	sameOrigin := map[string]string{"Origin": "http://" + host}
	rec := f.do(req{remote: workerIP, path: "/v0/management/auth-files/download?name=claude-x.json", headers: sameOrigin})
	if guardCode(rec) != "not_admin" || strings.Contains(rec.Body.String(), refreshToken) {
		t.Fatalf("token download from worker = %d %s", rec.Code, rec.Body)
	}
	var body struct {
		Error struct{ Node, NodeID, Login, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Node != "worker" || body.Error.NodeID != "nworkerCNTRL" ||
		body.Error.Login != login || !strings.Contains(body.Error.Message, "Admin") || !strings.Contains(body.Error.Message, "Access page") {
		t.Fatalf("not_admin body = %s", rec.Body)
	}
	if rec := f.do(req{remote: workerIP, method: http.MethodPost, path: "/v1/messages", body: `{}`}); isGuardDenial(rec) {
		t.Fatalf("worker proxy request denied: %s", rec.Body)
	}
}

// The device list is editable through settings, refuses to take the
// caller's own Admin, and is written to taproot.yaml.
func TestDevicesEditableThroughSettings(t *testing.T) {
	f := newServer(t)
	sameOrigin := map[string]string{"Origin": "http://" + host, "Content-Type": "application/json"}
	put := func(remote, body string) *httptest.ResponseRecorder {
		r := req{remote: remote, method: http.MethodPut, path: "/taproot/api/v1/settings", headers: sameOrigin, body: body}
		if remote == "127.0.0.1" {
			r.host, r.headers = "localhost:8317", map[string]string{"Content-Type": "application/json"}
		}
		return f.do(r)
	}
	rec := put(adminIP, `{"access":{"devices":[{"id":"ndesktopCNTRL","proxy":true},{"id":"nworkerCNTRL","proxy":true,"admin":true}]}}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "would_lock_out") {
		t.Fatalf("dropping own Admin = %d %s, want 409 would_lock_out", rec.Code, rec.Body)
	}
	rec = put(adminIP, `{"access":{"devices":[{"id":"ndesktopCNTRL","name":"desktop","proxy":true,"admin":true},{"id":"nworkerCNTRL","name":"worker","proxy":true,"admin":true}]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("giving worker Admin = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(req{remote: workerIP, path: "/v8/management/config", headers: sameOrigin}); rec.Code != http.StatusOK {
		t.Fatalf("worker with Admin = %d %s", rec.Code, rec.Body)
	}
	written, err := os.ReadFile(f.taprootYAML)
	if err != nil {
		t.Fatal(err)
	}
	tp, err := LoadConfig(f.taprootYAML)
	if err != nil || len(tp.Access.Devices) != 2 || !tp.Access.Devices[1].Admin || tp.Access.Devices[1].ID != "nworkerCNTRL" {
		t.Fatalf("taproot.yaml devices = %+v (%v):\n%s", tp.Access.Devices, err, written)
	}
	// Loopback may clear the list; it stays an admin itself.
	if rec := put("127.0.0.1", `{"access":{"devices":[]}}`); rec.Code != http.StatusOK {
		t.Fatalf("loopback clearing devices = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(req{remote: adminIP, path: "/v8/management/config", headers: sameOrigin}); guardCode(rec) != "not_admin" {
		t.Fatalf("admin after clearing = %d %s", rec.Code, rec.Body)
	}
}

// A new device is refused on both surfaces until a switch is on, and each
// switch opens only its own surface.
func TestNewDeviceRefusedUntilSwitchOn(t *testing.T) {
	f := newServer(t)
	sameOrigin := map[string]string{"Origin": "http://" + host}
	proxy := func() *httptest.ResponseRecorder {
		return f.do(req{remote: newIP, method: http.MethodPost, path: "/v1/messages", body: `{}`, headers: map[string]string{"Anthropic-Version": "2023-06-01"}})
	}
	admin := func() *httptest.ResponseRecorder {
		return f.do(req{remote: newIP, path: "/v0/management/auth-files/download?name=claude-x.json", headers: sameOrigin})
	}
	if rec := proxy(); guardCode(rec) != "no_proxy" || !strings.Contains(rec.Body.String(), "new-laptop") || !strings.Contains(rec.Body.String(), "Don't run /login") {
		t.Fatalf("new device proxy = %d %s, want no_proxy naming it", rec.Code, rec.Body)
	}
	if rec := admin(); guardCode(rec) != "not_admin" || strings.Contains(rec.Body.String(), refreshToken) {
		t.Fatalf("new device admin = %d %s, want not_admin", rec.Code, rec.Body)
	}
	if rec := f.do(req{remote: newIP, path: "/"}); rec.Code != http.StatusOK {
		t.Fatalf("new device dashboard page = %d, want the static page", rec.Code)
	}

	devices := `{"id":"ndesktopCNTRL","name":"desktop","proxy":true,"admin":true},{"id":"nworkerCNTRL","name":"worker","proxy":true}`
	settings := func(extra string) {
		t.Helper()
		rec := f.do(req{remote: adminIP, method: http.MethodPut, path: "/taproot/api/v1/settings", headers: map[string]string{"Origin": "http://" + host, "Content-Type": "application/json"},
			body: `{"access":{"devices":[` + devices + extra + `]}}`})
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT /settings = %d %s", rec.Code, rec.Body)
		}
	}
	settings(`,{"id":"nnew-laptopCNTRL","name":"new-laptop","proxy":true}`)
	if rec := proxy(); isGuardDenial(rec) {
		t.Fatalf("new device with Proxy denied: %s", rec.Body)
	}
	if rec := admin(); guardCode(rec) != "not_admin" {
		t.Fatalf("new device with Proxy only, admin = %d %s", rec.Code, rec.Body)
	}
	settings(`,{"id":"nnew-laptopCNTRL","name":"new-laptop","admin":true}`)
	if rec := admin(); isGuardDenial(rec) {
		t.Fatalf("new device with Admin denied: %s", rec.Body)
	}
	if rec := proxy(); guardCode(rec) != "no_proxy" {
		t.Fatalf("new device with Admin only, proxy = %d %s", rec.Code, rec.Body)
	}
}

// The Access page's list joins tailscaled's peers with access.devices.
func TestAccessDevicesEndpoint(t *testing.T) {
	f := newServer(t)
	rec := f.do(req{remote: adminIP, path: "/taproot/api/v1/access/devices", headers: map[string]string{"Origin": "http://" + host}})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /access/devices = %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Enabled bool
		Devices []struct {
			ID, Name, Login string
			Server          bool
			Proxy, Admin    bool
		}
		Addresses []struct{ CIDR string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || !got.Enabled || len(got.Devices) != 4 || len(got.Addresses) != 1 || got.Addresses[0].CIDR != lanIP+"/32" {
		t.Fatalf("devices = %s", rec.Body)
	}
	want := map[string][2]bool{"nserverCNTRL": {true, false}, "ndesktopCNTRL": {true, true}, "nworkerCNTRL": {true, false}, "nnew-laptopCNTRL": {false, false}}
	for _, d := range got.Devices {
		if p, ok := want[d.ID]; !ok || d.Proxy != p[0] || d.Admin != p[1] || d.Login != login || d.Server != (d.ID == "nserverCNTRL") {
			t.Errorf("device %+v", d)
		}
	}
	if rec := f.do(req{remote: workerIP, path: "/taproot/api/v1/access/devices"}); guardCode(rec) != "not_admin" {
		t.Fatalf("device list from a device without Admin = %d %s", rec.Code, rec.Body)
	}
}

// Security recheck F3: over plain http a cross-site no-cors GET carries no
// Origin and no Sec-Fetch-*, so GETs that change state need RequestHeader.
func TestStateChangingGETsNeedRequestHeader(t *testing.T) {
	f := newServer(t)
	for _, path := range []string{
		"/v0/management/usage-queue",
		"/v8/management/observability/usage/queue",
		"/v0/management/anthropic-auth-url",
		"/v8/management/oauth/auth-url?provider=anthropic",
	} {
		if rec := f.do(req{remote: adminIP, path: path}); guardCode(rec) != "request_header_required" {
			t.Errorf("header-less GET %s = %d %.200s, want request_header_required", path, rec.Code, rec.Body)
		}
	}
	rec := f.do(req{remote: adminIP, path: "/v0/management/usage-queue", headers: map[string]string{"X-Taproot-Request": "1"}})
	if rec.Code != http.StatusOK {
		t.Errorf("usage-queue with the header = %d %.200s", rec.Code, rec.Body)
	}
	// Read-only GETs, such as a plugin page's state fetch, need no header.
	for _, path := range []string{"/v8/management/config", "/v0/management/claude-keep-rolling/state", "/taproot/api/v1/settings"} {
		if rec := f.do(req{remote: adminIP, path: path}); isGuardDenial(rec) {
			t.Errorf("read-only GET %s denied: %s", path, rec.Body)
		}
	}
}

// Security recheck F2: Setup limits upstream's RESP listener to loopback.
func TestSetupLimitsRESPToLoopback(t *testing.T) {
	api.SetRESPConnectionFilter(nil)
	newServer(t)
	t.Cleanup(func() { api.SetRESPConnectionFilter(nil) })
	if !api.RESPConnectionAllowed(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}) {
		t.Error("loopback RESP refused")
	}
	for _, ip := range []string{adminIP, selfIP, lanIP} {
		if api.RESPConnectionAllowed(&net.TCPAddr{IP: net.ParseIP(ip), Port: 1}) {
			t.Errorf("RESP from %s admitted", ip)
		}
	}
}

// Launch audit B2: with access.enabled false, a remote caller read settings
// and created and downloaded a backup holding the OAuth tokens. The switch now
// opens the proxy only; the taproot API and management stay loopback-only.
func TestDisabledGuardKeepsTaprootAPILoopbackOnly(t *testing.T) {
	f, err := setupServer(t, "access:\n  enabled: false\nbackups:\n  daily-at: \"off\"\n")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	local := func(r req) *httptest.ResponseRecorder {
		r.remote, r.host = "127.0.0.1", "localhost:8317"
		return f.do(r)
	}
	rec := local(req{method: http.MethodPost, path: "/taproot/api/v1/backups"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("loopback backup create = %d %s", rec.Code, rec.Body)
	}
	var backup struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &backup); err != nil || backup.ID == "" {
		t.Fatalf("backup create body = %s", rec.Body)
	}
	if rec := local(req{path: "/taproot/api/v1/settings"}); rec.Code != http.StatusOK {
		t.Fatalf("loopback settings = %d %s", rec.Code, rec.Body)
	}
	if rec := local(req{path: "/taproot/api/v1/backups/" + backup.ID + "/download"}); rec.Code != http.StatusOK {
		t.Fatalf("loopback backup download = %d", rec.Code)
	}
	// Review finding 2: a browser on the same host also connects from
	// loopback, so another site's page, or a DNS-rebound name, gets nothing.
	textPlain := map[string]string{"Content-Type": "text/plain", "Origin": "http://evil.example", "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors"}
	for _, r := range []req{
		{method: http.MethodPost, path: "/taproot/api/v1/backups", headers: textPlain},
		{method: http.MethodPost, path: "/taproot/api/v1/backups/" + backup.ID + "/restore", headers: textPlain},
		{path: "/taproot/api/v1/backups/" + backup.ID + "/download", headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors"}},
		{host: "rebind.evil.example:8317", path: "/taproot/api/v1/backups/" + backup.ID + "/download"},
		{host: "rebind.evil.example:8317", method: http.MethodPost, path: "/taproot/api/v1/backups"},
	} {
		r.remote = "127.0.0.1"
		if r.host == "" {
			r.host = "localhost:8317"
		}
		if rec := f.do(r); !isGuardDenial(rec) || strings.Contains(rec.Body.String(), refreshToken) {
			t.Errorf("loopback browser %s %s (Host %s) = %d %.200s, want the guard's 403", r.method, r.path, r.host, rec.Code, rec.Body)
		}
	}

	for _, remote := range []string{"203.0.113.9", adminIP, selfIP, lanIP} {
		for _, r := range []req{
			{path: "/taproot/api/v1/settings"},
			{method: http.MethodPost, path: "/taproot/api/v1/backups"},
			{path: "/taproot/api/v1/backups/" + backup.ID + "/download"},
			{path: "/taproot/api/v1/me"},
			{path: "/v8/management/config"},
			{path: "/v0/management/auth-files/download?name=claude-x.json"},
			{path: "/"},
		} {
			r.remote = remote
			if rec := f.do(r); guardCode(rec) != "guard_disabled" || strings.Contains(rec.Body.String(), refreshToken) {
				t.Errorf("%s %s from %s = %d %.200s, want the guard's 403", r.method, r.path, remote, rec.Code, rec.Body)
			}
		}
		if rec := f.do(req{remote: remote, method: http.MethodPost, path: "/v1/messages", body: `{}`}); isGuardDenial(rec) {
			t.Errorf("proxy from %s denied: %s", remote, rec.Body)
		}
	}
}

// Home mode and the TUI's embedded server skip Setup, so taproot refuses them;
// the TUI client starts no server and stays allowed.
func TestCheckServerMode(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		home, tui, standalone bool
		wantErr               string
	}{
		{name: "server"},
		{name: "--standalone alone is a TUI flag", standalone: true},
		{name: "TUI client", tui: true},
		{name: "TUI client with a Home JWT", home: true, tui: true, wantErr: "Home mode"},
		{name: "Home server", home: true, wantErr: "Home mode"},
		{name: "TUI embedded server", tui: true, standalone: true, wantErr: "--tui --standalone"},
		{name: "TUI embedded server with Home", home: true, tui: true, standalone: true, wantErr: "Home mode"},
	} {
		err := CheckServerMode(tc.home, tc.tui, tc.standalone)
		if tc.wantErr == "" && err != nil {
			t.Errorf("%s: %v, want nil", tc.name, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "access guard")) {
			t.Errorf("%s: %v, want an error naming %s and the access guard", tc.name, err, tc.wantErr)
		}
	}
}

// A misspelled key fails at startup with its name, instead of being ignored.
func TestSetupRefusesUnknownKeys(t *testing.T) {
	for key, tp := range map[string]string{
		"allow-login": "access:\n  allow-login: [" + login + "]\n",
		"backup":      "access:\n  devices: []\nbackup:\n  keep: 3\n",
		"login":       "access:\n  devices:\n    - {id: n1, proxy: true, login: alice@github}\n",
	} {
		_, err := setupServer(t, tp)
		if err == nil || !strings.Contains(err.Error(), "field "+key+" not found") || !strings.Contains(err.Error(), "taproot.yaml") {
			t.Errorf("Setup with %q = %v, want an unknown-key error naming %s", tp, err, key)
		}
	}
}

// The access keys from before per-device permissions stop startup with a
// message that names what replaces them; there is no migration.
func TestSetupRefusesRemovedAccessKeys(t *testing.T) {
	for key, want := range map[string]string{
		"allow-logins":  "access.devices",
		"admin-nodes":   "admin: true",
		"trusted-cidrs": "access.addresses",
	} {
		_, err := setupServer(t, "access:\n  "+key+": []\n  devices: []\n")
		if err == nil || !strings.Contains(err.Error(), "access."+key+" was removed") || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "taproot.yaml") {
			t.Errorf("Setup with %s = %v, want an error naming %s and taproot.yaml", key, err, want)
		}
	}
}

// A fresh install with no devices listed starts once tailscaled's socket is
// reachable; only loopback is an admin.
func TestSetupStartsWithoutDevices(t *testing.T) {
	socket := filepath.ToSlash(fakeLocalAPI(t))
	for name, tp := range map[string]string{
		"no devices key": "access:\n  tailscale-socket: " + socket + "\nbackups:\n  daily-at: \"off\"\n",
		"empty list":     "access:\n  devices: []\n  tailscale-socket: " + socket + "\nbackups:\n  daily-at: \"off\"\n",
	} {
		f, err := setupServer(t, tp)
		if err != nil {
			t.Fatalf("%s: Setup = %v", name, err)
		}
		if rec := f.do(req{remote: "127.0.0.1", host: "localhost:8317", path: "/taproot/api/v1/me"}); rec.Code != http.StatusOK {
			t.Errorf("%s: loopback /me = %d", name, rec.Code)
		}
	}
}

// Everything the settings API writes loads again under strict decoding, so a
// dashboard edit never leaves a taproot.yaml that the next start refuses.
func TestSettingsOutputLoadsStrictly(t *testing.T) {
	f := newServer(t)
	body := `{
		"access": {"devices": [{"id": "ndesktopCNTRL", "name": "desktop", "label": "Desk: main", "proxy": true, "admin": true}, {"id": "nExample2CNTRL", "proxy": true}], "addresses": [{"cidr": "192.0.2.10", "note": "agents: on the host"}]},
		"requestLogs": {"maxDays": -1, "maxBytes": -1, "excludePaths": ["HEAD /api/*"], "keepUnrouted404": true},
		"backups": {"dailyAt": "04:30", "keep": 3},
		"ingest": {"pollInterval": "5m"},
		"alerts": {"discordWebhook": "https://discord.com/api/webhooks/1/token", "enabled": false}
	}`
	rec := f.do(req{remote: "127.0.0.1", host: "localhost:8317", method: http.MethodPut, path: "/taproot/api/v1/settings", headers: map[string]string{"Content-Type": "application/json"}, body: body})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /settings = %d %s", rec.Code, rec.Body)
	}
	written, err := os.ReadFile(f.taprootYAML)
	if err != nil {
		t.Fatal(err)
	}
	tp, err := LoadConfig(f.taprootYAML)
	if err != nil {
		t.Fatalf("LoadConfig of the settings output: %v\n%s", err, written)
	}
	if len(tp.Access.Devices) != 2 || tp.Access.Devices[0] != (access.DeviceGrant{ID: "ndesktopCNTRL", Name: "desktop", Label: "Desk: main", Proxy: true, Admin: true}) ||
		tp.Access.Devices[1] != (access.DeviceGrant{ID: "nExample2CNTRL", Proxy: true}) ||
		len(tp.Access.Addresses) != 1 || tp.Access.Addresses[0] != (access.AddressGrant{CIDR: "192.0.2.10/32", Note: "agents: on the host"}) ||
		tp.RequestLogs.MaxDays != -1 || tp.RequestLogs.MaxBytes != -1 || !tp.RequestLogs.KeepUnrouted404 || len(tp.RequestLogs.ExcludePaths) != 1 ||
		tp.Backups.DailyAt != "04:30" || tp.Backups.Keep != 3 || tp.Ingest.PollInterval != "5m" ||
		tp.Alerts.DiscordWebhook == "" || tp.Alerts.Enabled == nil || *tp.Alerts.Enabled {
		t.Fatalf("reloaded config = %+v\n%s", tp, written)
	}
}

// A deployed taproot.yaml of the documented shape, plus the request-logs
// section the settings API writes for unlimited retention, still loads.
func TestDeployedConfigShapesLoad(t *testing.T) {
	dir := t.TempDir()
	deployed := filepath.Join(dir, "taproot.yaml")
	raw := `access:
  devices:
    - id: nExample1CNTRL
      name: laptop
      proxy: true
      admin: true
    - id: nExample2CNTRL
      name: phone
      proxy: false
      admin: true
  addresses:
    - cidr: 192.0.2.10/32
      note: agents on the server host
  tailscale-socket: /var/run/tailscale/tailscaled.sock
backups:
  daily-at: "03:13"
  keep: 14
request-logs:
  max-days: -1
  max-bytes: "-1"
`
	if err := os.WriteFile(deployed, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	tp, err := LoadConfig(deployed)
	if err != nil || len(tp.Access.Devices) != 2 || len(tp.Access.Addresses) != 1 || tp.RequestLogs.MaxDays != -1 || tp.RequestLogs.MaxBytes != -1 {
		t.Fatalf("LoadConfig = %+v, %v", tp, err)
	}
	example, err := LoadConfig(filepath.Join("..", "..", "examples", "taproot", "taproot.yaml"))
	if err != nil || len(example.Access.Devices) == 0 {
		t.Fatalf("examples/taproot/taproot.yaml = %+v, %v", example, err)
	}
	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, []byte("# nothing set\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(empty); err != nil {
		t.Fatalf("comment-only taproot.yaml: %v", err)
	}
}

// Only the first YAML document would be read, so a file with a second one is
// refused rather than loaded with that document ignored.
func TestLoadConfigRefusesASecondDocument(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, content string
		ok            bool
	}{
		{"leading marker", "---\ndata-dir: a\n", true},
		{"trailing marker", "data-dir: a\n---\n", true},
		{"end marker", "data-dir: a\n...\n", true},
		{"second document", "data-dir: a\n---\naccess:\n  enabled: false\n", false},
		{"unknown key in the second document", "data-dir: a\n---\nbogus-key: 1\n", false},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".yaml")
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadConfig(path)
		if tc.ok && err != nil {
			t.Errorf("%s: LoadConfig = %v, want success", tc.name, err)
		}
		if !tc.ok && (err == nil || !strings.Contains(err.Error(), "more than one YAML document")) {
			t.Errorf("%s: LoadConfig = %v, want the more-than-one-document error", tc.name, err)
		}
	}
}

// A request under the API prefix that matches no route, or matches one only
// under another method, gets the API's JSON error rather than an empty 404.
func TestUnknownAPIPathsAnswerJSON(t *testing.T) {
	f := newServer(t)
	headers := map[string]string{"X-Taproot-Request": "1"}
	for _, r := range []req{
		{path: "/taproot/api/v1/nope"},
		{path: "/taproot/api/v1"},
		{method: http.MethodDelete, path: "/taproot/api/v1/backups"},
		{method: http.MethodPut, path: "/taproot/api/v1/me"},
		{method: http.MethodHead, path: "/taproot/api/v1/settings"},
	} {
		r.remote, r.headers = adminIP, headers
		rec := f.do(r)
		var body struct {
			Error struct{ Code, Message string }
		}
		if rec.Code != http.StatusNotFound || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error.Code != "not_found" || !strings.Contains(body.Error.Message, r.path) {
			t.Errorf("%s %s = %d %q, want 404 with error code not_found", r.method, r.path, rec.Code, rec.Body)
		}
	}
	// Known routes and paths outside the prefix are untouched, and the guard still answers first.
	if rec := f.do(req{remote: adminIP, path: "/taproot/api/v1/me"}); rec.Code != http.StatusOK {
		t.Errorf("GET /taproot/api/v1/me = %d, want 200", rec.Code)
	}
	if rec := f.do(req{remote: adminIP, path: "/taproot/api/v1x"}); rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
		t.Errorf("GET /taproot/api/v1x = %d %q, want upstream's empty 404", rec.Code, rec.Body)
	}
	if rec := f.do(req{remote: "203.0.113.9", path: "/taproot/api/v1/nope"}); !isGuardDenial(rec) {
		t.Errorf("GET /taproot/api/v1/nope from a stranger = %d, want the guard's 403", rec.Code)
	}
}
