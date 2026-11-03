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

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v8/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// server's tailnet as measured with tailscale status and whois on 2026-11-02.
const (
	tailnet  = "tailnet-0000.ts.net"
	selfIP   = "100.64.0.1"
	adminIP = "100.64.0.2"
	lanIP    = "192.168.0.88"
	host     = "server:8317"
	// Fake token values; the test checks they never leave the server cross-origin.
	refreshToken = "sk-ant-ort-TESTONLY"
)

// fakeLocalAPI serves tailscaled's whois and status endpoints on a real unix
// socket, with server as the server's own node.
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
	nodes := map[string]string{selfIP: "server", adminIP: "desktop"}
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
	mux.HandleFunc("/localapi/v0/status", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Self": map[string]any{
			"ID": "nserverCNTRL", "DNSName": "server." + tailnet + ".", "TailscaleIPs": []string{selfIP, "fd7a:115c:a1e0::1"},
		}})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return socket
}

type fixture struct {
	h           http.Handler
	taprootYAML string
}

// newServer builds the real server the way cmd/server does: taproot.Setup
// first, then api.NewServer with its options.
func newServer(t *testing.T) fixture {
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
	ctx, cancel := context.WithCancel(context.Background())
	// The store stays open for the process, so on Windows removal is best effort.
	t.Cleanup(func() { cancel(); _ = os.RemoveAll(dir) })

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
	tp := "access:\n  tailscale-socket: " + filepath.ToSlash(fakeLocalAPI(t)) + "\n  trusted-cidrs: [\"" + lanIP + "/32\"]\nbackups:\n  daily-at: \"off\"\n"
	if err := os.WriteFile(taprootYAML, []byte(tp), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Port = 8317
	cfg.AuthDir = authDir
	opts, err := Setup(ctx, cfg, configPath)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	srv := api.NewServer(cfg, auth.NewManager(nil, nil, nil), sdkaccess.NewManager(), configPath, opts...)
	return fixture{h: srv.Handler(), taprootYAML: taprootYAML}
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

func isGuardDenial(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "caller is not allowed")
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
// repro (finding 8): a page on another site, in the owner's browser on an
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
		{"allowlist edit", req{method: http.MethodPut, path: "/taproot/api/v1/settings", headers: textPlain, body: `{"access":{"allowLogins":["alice@github","bob@example.org"]}}`}},
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
		{method: http.MethodPut, path: "/taproot/api/v1/settings", body: `{"access":{"allowLogins":["alice@github"]}}`},
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
	adminPaths := []string{"/", "/taproot/api/v1/me", "/v8/management/config", "/v0/management/auth-files/download?name=claude-x.json", "/v0/resource/plugins/demo/index.html", "/anthropic/callback?state=x"}
	for _, tc := range []struct {
		name, remote, host string
		admin              bool
	}{
		// Every process and container on server connects from server's own node.
		{"server's own node", selfIP, selfIP + ":8317", false},
		// server-local callers using the 127.0.1.1 publish arrive from the LAN address.
		{"trusted cidr", lanIP, "127.0.1.1:8317", false},
		{"allowlisted other node", adminIP, host, true},
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
		})
	}
	for _, remote := range []string{adminIP, selfIP, "127.0.0.1", "203.0.113.9"} {
		if rec := f.do(req{remote: remote, path: "/management.html"}); rec.Code != http.StatusNotFound {
			t.Errorf("/management.html from %s = %d, want 404", remote, rec.Code)
		}
	}
}
