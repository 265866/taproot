package taproot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/api"
	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/registry"
	"github.com/265866/taproot/internal/runtime/executor"
	sdkaccess "github.com/265866/taproot/sdk/access"
	"github.com/265866/taproot/sdk/cliproxy/auth"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	claudeModel = "claude-opus-5-5"
	codexModel  = "gpt-6-sol"
)

// fakeUpstream answers Claude messages (JSON or SSE) and Codex responses (SSE)
// with fixed usage, so every proxied request produces a usage record.
func fakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	usage := `{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":200,"ephemeral_1h_input_tokens":100}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/messages") && strings.Contains(string(body), `"stream":true`):
			w.Header().Set("Content-Type", "text/event-stream")
			for _, ev := range []string{
				`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"` + claudeModel + `","content":[],"usage":` + usage + `}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
				`{"type":"message_stop"}`,
			} {
				var typ struct{ Type string }
				_ = json.Unmarshal([]byte(ev), &typ)
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ.Type, ev)
			}
		case strings.HasSuffix(r.URL.Path, "/v1/messages"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"`+claudeModel+`","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":`+usage+`}`)
		case strings.HasSuffix(r.URL.Path, "/responses"):
			w.Header().Set("Content-Type", "text/event-stream")
			resp := `{"id":"resp_1","object":"response","model":"` + codexModel + `","status":"completed","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":5},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":23}}`
			_, _ = fmt.Fprintf(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
			_, _ = fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":%s}\n\n", resp)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newProxyServer builds the real server with taproot.Setup's options, real
// Claude and Codex executors, and API-key credentials that point at upstream.
func newProxyServer(t *testing.T, upstream string, trusted ...string) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("MANAGEMENT_PASSWORD", "")
	if err := os.Unsetenv("MANAGEMENT_PASSWORD"); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "taproot-devices")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = os.RemoveAll(dir) })
	authDir := filepath.Join(dir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 8317\nauth-dir: "+filepath.ToSlash(authDir)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cidrs := `"` + lanIP + `/32"`
	for _, addr := range trusted {
		cidrs += `, "` + addr + `/32"`
	}
	tp := "access:\n  allow-logins: [\"" + login + "\"]\n  admin-nodes: [desktop]\n  tailscale-socket: " + filepath.ToSlash(fakeLocalAPI(t)) +
		"\n  trusted-cidrs: [" + cidrs + "]\nbackups:\n  daily-at: \"off\"\n"
	if err := os.WriteFile(filepath.Join(dir, "taproot.yaml"), []byte(tp), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Port = 8317
	cfg.AuthDir = authDir
	opts, err := Setup(ctx, cfg, configPath)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	manager := auth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor.NewClaudeExecutor(cfg))
	manager.RegisterExecutor(executor.NewCodexExecutor(cfg))
	for _, a := range []*auth.Auth{
		{
			ID: "devices-claude", Provider: "claude", Status: auth.StatusActive,
			Attributes: map[string]string{auth.AttributeAPIKey: "sk-ant-api03-TESTONLY", "base_url": upstream},
		},
		{
			ID: "devices-codex", Provider: "codex", Status: auth.StatusActive,
			Attributes: map[string]string{auth.AttributeAPIKey: "sk-TESTONLY", "base_url": upstream},
		},
	} {
		if _, err := manager.Register(ctx, a); err != nil {
			t.Fatalf("register %s: %v", a.ID, err)
		}
	}
	registry.GetGlobalRegistry().RegisterClient("devices-claude", "claude", []*registry.ModelInfo{{ID: claudeModel}})
	registry.GetGlobalRegistry().RegisterClient("devices-codex", "codex", []*registry.ModelInfo{{ID: codexModel}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient("devices-claude")
		registry.GetGlobalRegistry().UnregisterClient("devices-codex")
	})
	return api.NewServer(cfg, manager, sdkaccess.NewManager(), configPath, opts...).Handler()
}

type storedEvent struct {
	Provider    string `json:"provider"`
	SessionID   string `json:"sessionId"`
	Stream      bool   `json:"stream"`
	DeviceID    string `json:"deviceId"`
	DeviceName  string `json:"deviceName"`
	DeviceLogin string `json:"deviceLogin"`
}

// waitForEvents polls the usage API from loopback until want events are stored.
func waitForEvents(t *testing.T, h http.Handler, want int) []storedEvent {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/taproot/api/v1/usage/events?limit=100", nil)
		req.RemoteAddr = "127.0.0.1:5000"
		req.Host = host
		h.ServeHTTP(rec, req)
		var body struct{ Events []storedEvent }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode events %d %.200s: %v", rec.Code, rec.Body, err)
		}
		if len(body.Events) >= want || time.Now().After(deadline) {
			return body.Events
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestUsageEventsCarryTheCallersDevice drives HTTP, streaming, and Codex
// websocket requests through the real server and checks each stored usage
// event names the device the access guard admitted.
func TestUsageEventsCarryTheCallersDevice(t *testing.T) {
	upstream := fakeUpstream(t)
	// One of this machine's own addresses stands in for the server's LAN address,
	// which a local client using the 127.0.1.1 publish arrives from.
	own := machineAddrs(t)
	ownIP := ""
	cgnat := netip.MustParsePrefix("100.64.0.0/10") // tailnet addresses take the tailnet path
	for addr := range own {
		if addr.Is4() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !cgnat.Contains(addr) {
			ownIP = addr.String()
			break
		}
	}
	if ownIP == "" {
		t.Skip("this machine has no non-loopback IPv4 address to call from")
	}
	h := newProxyServer(t, upstream.URL, ownIP)
	lanName := lanIP
	if own[netip.MustParseAddr(lanIP)] {
		lanName = "server (via " + lanIP + ")"
	}

	claude := func(remote, session string, stream bool) {
		t.Helper()
		body := fmt.Sprintf(`{"model":%q,"max_tokens":16,"stream":%t,"messages":[{"role":"user","content":"hi"}]}`, claudeModel, stream)
		rec := (fixture{h: h}).do(req{
			method: http.MethodPost, remote: remote, path: "/v1/messages", body: body,
			headers: map[string]string{"Content-Type": "application/json", "X-Claude-Code-Session-Id": session},
		})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
			t.Fatalf("claude from %s (stream %t) = %d %.300s", remote, stream, rec.Code, rec.Body)
		}
	}
	claude(adminIP, "sess-http", false)
	claude(workerIP, "sess-stream", true)
	claude(selfIP, "sess-self", false)
	claude("127.0.0.1", "sess-loopback", false)
	claude(lanIP, "sess-lan", false)
	claude(ownIP, "sess-own", false)

	// The websocket needs a real connection; the wrapper stands in for the
	// tailnet source address the listener cannot produce in a test.
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = adminIP + ":40001"
		h.ServeHTTP(w, r)
	}))
	defer live.Close()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(live.URL, "http")+"/v1/responses", nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial websocket: %v (status %d)", err, status)
	}
	defer func() { _ = conn.Close() }()
	turn := fmt.Sprintf(`{"type":"response.create","model":%q,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`, codexModel)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(turn)); err != nil {
		t.Fatalf("write turn: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read websocket: %v", err)
		}
		if strings.Contains(string(msg), `"response.completed"`) {
			break
		}
		if strings.Contains(string(msg), `"error"`) {
			t.Fatalf("websocket turn failed: %s", msg)
		}
	}

	events := waitForEvents(t, h, 7)
	got := map[string]storedEvent{}
	for _, e := range events {
		key := e.SessionID
		if e.Provider == "codex" {
			key = "codex-ws"
		}
		got[key] = e
	}
	want := map[string]storedEvent{
		"claude:sess-http":     {DeviceID: "node:ndesktopCNTRL", DeviceName: "desktop", DeviceLogin: "alice@github"},
		"claude:sess-stream":   {DeviceID: "node:nworkerCNTRL", DeviceName: "worker", DeviceLogin: "alice@github", Stream: true},
		"claude:sess-self":     {DeviceID: "local", DeviceName: "server (local)", DeviceLogin: "alice@github"},
		"claude:sess-loopback": {DeviceID: "local", DeviceName: "server (local)"},
		"claude:sess-lan":      {DeviceID: "cidr:" + lanIP, DeviceName: lanName},
		"claude:sess-own":      {DeviceID: "cidr:" + ownIP, DeviceName: "server (via " + ownIP + ")"},
		"codex-ws":             {DeviceID: "node:ndesktopCNTRL", DeviceName: "desktop", DeviceLogin: "alice@github"},
	}
	for key, w := range want {
		e, ok := got[key]
		if !ok {
			t.Errorf("no usage event for %s; stored %+v", key, events)
			continue
		}
		if e.DeviceID != w.DeviceID || e.DeviceName != w.DeviceName || e.DeviceLogin != w.DeviceLogin || (key != "codex-ws" && e.Stream != w.Stream) {
			t.Errorf("%s stored device %q %q %q stream %t, want %q %q %q stream %t", key,
				e.DeviceID, e.DeviceName, e.DeviceLogin, e.Stream, w.DeviceID, w.DeviceName, w.DeviceLogin, w.Stream)
		}
	}

	// The devices API shows the same name, and the server's own address is
	// still kept off the admin surface.
	rec := (fixture{h: h}).do(req{method: http.MethodGet, remote: "127.0.0.1", path: "/taproot/api/v1/usage/devices?from=2000-01-01T00:00:00Z&to=2100-01-01T00:00:00Z"})
	var body struct {
		Devices []struct{ DeviceID, Name, Via string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode devices %d %.200s: %v", rec.Code, rec.Body, err)
	}
	var named bool
	for _, d := range body.Devices {
		named = named || (d.DeviceID == "cidr:"+ownIP && d.Name == "server (via "+ownIP+")" && d.Via == "cidr")
	}
	if !named {
		t.Errorf("devices API = %+v, want cidr:%s named server (via %s)", body.Devices, ownIP, ownIP)
	}
	if rec := (fixture{h: h}).do(req{method: http.MethodGet, remote: ownIP, path: "/taproot/api/v1/me"}); rec.Code != http.StatusForbidden {
		t.Errorf("/me from the server's own trusted address = %d, want 403", rec.Code)
	}
}

// machineAddrs lists this machine's interface addresses, which the guard
// reads to recognize the server's own addresses.
func machineAddrs(t *testing.T) map[netip.Addr]bool {
	t.Helper()
	raw, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatalf("list interface addresses: %v", err)
	}
	out := map[netip.Addr]bool{}
	for _, a := range raw {
		if ipnet, ok := a.(*net.IPNet); ok {
			if addr, ok := netip.AddrFromSlice(ipnet.IP); ok {
				out[addr.Unmap()] = true
			}
		}
	}
	return out
}
