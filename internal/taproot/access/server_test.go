package access

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/api"
	proxyconfig "github.com/265866/taproot/internal/config"
	sdkaccess "github.com/265866/taproot/sdk/access"
	"github.com/265866/taproot/sdk/api/handlers"
	"github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
)

// newRealServer builds upstream's server the way taproot.Setup wires it: the
// guard is constructed first, then passed in with WithMiddleware, and its
// routes are mounted by a router configurator.
func newRealServer(t *testing.T, g *Guard) *api.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	authDir := filepath.Join(dir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 0\nauth-dir: "+filepath.ToSlash(authDir)+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg := &proxyconfig.Config{}
	cfg.Port = 0
	cfg.AuthDir = authDir
	cfg.Debug = true
	cfg.LoggingToFile = false
	cfg.UsageStatisticsEnabled = false
	if cfg.RemoteManagement.SecretKey != "" || cfg.RemoteManagement.AllowRemote {
		t.Fatalf("test config must start without a management key")
	}
	opts := []api.ServerOption{}
	if g != nil {
		opts = append(opts,
			api.WithMiddleware(g.Middleware()),
			api.WithRouterConfigurator(func(engine *gin.Engine, _ *handlers.BaseAPIHandler, _ *proxyconfig.Config) {
				g.Routes(engine.Group("/taproot/api/v1"))
			}),
		)
	}
	return api.NewServer(cfg, auth.NewManager(nil, nil, nil), sdkaccess.NewManager(), configPath, opts...)
}

func serve(h http.Handler, method, remote, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	req.Host = "localhost:8317"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestRealServerGuardsEveryRoute proves the guard runs ahead of every kind of
// route upstream registers: proxy routes, the websocket upgrade, the control
// panel, both management trees, plugin management and resource routes served
// from NoRoute, taproot's own API, and unknown paths.
func TestRealServerGuardsEveryRoute(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	h := newRealServer(t, g).Handler()

	wsUpgrade := map[string]string{
		"Connection":            "Upgrade",
		"Upgrade":               "websocket",
		"Sec-WebSocket-Version": "13",
		"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
	}
	routes := []struct {
		method, path string
		headers      map[string]string
	}{
		{http.MethodGet, "/", nil},
		{http.MethodGet, "/v1/models", nil},
		{http.MethodPost, "/v1/chat/completions", nil},
		{http.MethodPost, "/v1/messages", nil},
		{http.MethodGet, "/v1/responses", wsUpgrade},
		{http.MethodGet, "/backend-api/codex/responses", wsUpgrade},
		{http.MethodGet, "/v8/management/config", nil},
		{http.MethodGet, "/v8/management/credentials", nil},
		{http.MethodGet, "/v0/management/config", nil},
		{http.MethodGet, "/v0/management/auth-files", nil},
		{http.MethodGet, "/v0/management/plugin-owned/route", nil},
		{http.MethodGet, "/v0/resource/plugins/demo/index.html", nil},
		{http.MethodGet, "/anthropic/callback", nil},
		{http.MethodGet, "/taproot/api/v1/me", nil},
		{http.MethodGet, "/no/such/route", nil},
		{http.MethodOptions, "/v1/models", map[string]string{"Origin": "http://evil.example", "Access-Control-Request-Method": "POST"}},
	}
	for _, remote := range []string{"203.0.113.9:5000", strayIP + ":40000"} {
		for _, r := range routes {
			rec := serve(h, r.method, remote, r.path, r.headers)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"forbidden"`) {
				t.Errorf("%s %s from %s = %d %q, want the guard's 403", r.method, r.path, remote, rec.Code, rec.Body)
			}
		}
	}
	// A device with no permission loads only the static page; everything
	// else is the guard's 403, which names the device.
	for _, r := range routes {
		rec := serve(h, r.method, outsiderIP+":40000", r.path, r.headers)
		if r.path == "/" && r.method == http.MethodGet {
			if rec.Code != http.StatusOK {
				t.Errorf("GET / from an unlisted device = %d, want the static page", rec.Code)
			}
			continue
		}
		body := rec.Body.String()
		guardCode := strings.Contains(body, `"no_proxy"`) || strings.Contains(body, `"not_admin"`) || strings.Contains(body, `"forbidden"`)
		if rec.Code != http.StatusForbidden || !guardCode {
			t.Errorf("%s %s from an unlisted device = %d %q, want the guard's 403", r.method, r.path, rec.Code, body)
		}
	}

	for _, remote := range []string{"203.0.113.9:5000", outsiderIP + ":40000", adminIP + ":40000", "127.0.0.1:1"} {
		if rec := serve(h, http.MethodGet, remote, "/management.html", nil); rec.Code != http.StatusNotFound {
			t.Errorf("/management.html from %s = %d, want 404", remote, rec.Code)
		}
	}

	for _, remote := range []string{"203.0.113.9:5000", outsiderIP + ":40000"} {
		if rec := serve(h, http.MethodGet, remote, "/healthz", nil); rec.Code != http.StatusOK {
			t.Errorf("/healthz from %s = %d, want 200", remote, rec.Code)
		}
		if rec := serve(h, http.MethodHead, remote, "/healthz", nil); rec.Code != http.StatusOK {
			t.Errorf("HEAD /healthz from %s = %d, want 200", remote, rec.Code)
		}
	}

	// Admitted callers get past the guard to upstream's own handling.
	if rec := serve(h, http.MethodGet, adminIP+":40000", "/taproot/api/v1/me", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"login":"alice@github"`) {
		t.Fatalf("/me for allowlisted login = %d %s", rec.Code, rec.Body)
	}
	if rec := serve(h, http.MethodGet, adminIP+":40000", "/no/such/route", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown route for allowlisted login = %d, want upstream's 404", rec.Code)
	}
}

// TestRealServerKeylessManagement proves an admitted caller reaches the
// management API without a key while nobody else does, with no secret-key in
// config.yaml.
func TestRealServerKeylessManagement(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	srv := newRealServer(t, g)
	h := srv.Handler()

	for _, tc := range []struct {
		name, remote string
		headers      map[string]string
		want         int
	}{
		{"allowlisted login, no key", adminIP + ":40000", nil, http.StatusOK},
		{"allowlisted login, wrong key", adminIP + ":40000", map[string]string{"Authorization": "Bearer wrong", "X-Management-Key": "wrong"}, http.StatusOK},
		{"loopback, no key", "127.0.0.1:5000", nil, http.StatusOK},
		{"unknown login", outsiderIP + ":40000", nil, http.StatusForbidden},
		{"unknown login with spoofed identity", outsiderIP + ":40000", map[string]string{"Tailscale-User-Login": "alice@github", "X-Forwarded-For": "127.0.0.1"}, http.StatusForbidden},
		{"non-tailnet", "203.0.113.9:5000", nil, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, path := range []string{"/v8/management/config", "/v0/management/config"} {
				rec := serve(h, http.MethodGet, tc.remote, path, tc.headers)
				if rec.Code != tc.want {
					t.Fatalf("%s = %d, want %d; body %s", path, rec.Code, tc.want, rec.Body)
				}
				if strings.Contains(rec.Body.String(), g.secret) || strings.Contains(strings.Join(headerValues(rec.Header()), "\n"), g.secret) {
					t.Fatalf("%s response leaked the management secret", path)
				}
			}
		})
	}

	// Over a real TCP connection the source is loopback and no key is sent.
	live := httptest.NewServer(h)
	defer live.Close()
	resp, err := http.Get(live.URL + "/v8/management/config")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "auth-dir") {
		t.Fatalf("live GET /v8/management/config = %d %.200s", resp.StatusCode, body)
	}
}

// TestRealServerWithoutGuardHasNoManagement shows the baseline the guard
// changes: with no key anywhere, upstream does not register management routes.
func TestRealServerWithoutGuardHasNoManagement(t *testing.T) {
	t.Setenv(managementEnv, "")
	if err := os.Unsetenv(managementEnv); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	h := newRealServer(t, nil).Handler()
	if rec := serve(h, http.MethodGet, "127.0.0.1:5000", "/v8/management/config", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("without guard /v8/management/config = %d, want 404", rec.Code)
	}
}

func headerValues(h http.Header) []string {
	var out []string
	for _, vs := range h {
		out = append(out, vs...)
	}
	return out
}
