package settings

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// fakeLocalAPI answers tailscaled's /localapi/v0/status?peers=false with the
// server's own node on a real unix socket, as the guard reads it.
func fakeLocalAPI(t *testing.T) string {
	t.Helper()
	// Short path: unix socket paths are limited to about 108 bytes.
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
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/localapi/v0/status" || r.FormValue("peers") == "true" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"BackendState": "Running",
			"Self":         map[string]any{"ID": serverID, "DNSName": "server.tailnet-0000.ts.net.", "TailscaleIPs": []string{"100.64.0.1"}},
		})
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return socket
}

// newRealNodeFixture reads the server's node from the guard itself, with an
// empty cache, as after a restart where only loopback has called so far.
func newRealNodeFixture(t *testing.T, yaml, socket string) *fixture {
	t.Helper()
	f := newFixture(t, options{yaml: yaml, tailscaleSocket: socket})
	f.access.realServerNode = true
	return f
}

// The Admin check reads the server's node from tailscaled when no request
// has looked it up yet.
func TestServerAdminRefusedBeforeTheNodeIsCached(t *testing.T) {
	f := newRealNodeFixture(t, commentedYAML, fakeLocalAPI(t))
	code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, {"id": "`+serverID+`", "proxy": true, "admin": true}]}}`)
	if code != http.StatusBadRequest || errorCode(t, body) != "server_admin" {
		t.Fatalf("Admin for the server's own node with an empty cache = %d %s, want 400 server_admin", code, body)
	}
	if f.file(t) != commentedYAML {
		t.Errorf("file changed:\n%s", f.file(t))
	}
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, {"id": "nphoneCNTRL", "admin": true}]}}`); code != http.StatusOK {
		t.Fatalf("Admin for another device = %d %s", code, body)
	}
}

// While tailscaled cannot be read and the node was never read, a save that
// gives a device new Admin is refused, and every other save goes through.
func TestNewAdminRefusedWhileTheServerNodeIsUnknown(t *testing.T) {
	f := newRealNodeFixture(t, commentedYAML, "")
	code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, {"id": "nphoneCNTRL", "proxy": true, "admin": true}]}}`)
	if code != http.StatusServiceUnavailable || errorCode(t, body) != "server_node_unknown" {
		t.Fatalf("new Admin with the node unknown = %d %s, want 503 server_node_unknown", code, body)
	}
	if f.file(t) != commentedYAML {
		t.Errorf("file changed:\n%s", f.file(t))
	}
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [`+laptopEntry+`, {"id": "nphoneCNTRL", "proxy": true}]}}`); code != http.StatusOK {
		t.Fatalf("Proxy only with the node unknown = %d %s", code, body)
	}
	// Admin the laptop already had is not new.
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nlaptopCNTRL", "name": "laptop", "label": "Desk", "proxy": true, "admin": true}]}}`); code != http.StatusOK {
		t.Fatalf("relabel an existing admin with the node unknown = %d %s", code, body)
	}
}

// A claim reads the server's node from tailscaled when no request has looked
// it up yet, and answers 503 only when tailscaled cannot be read.
func TestClaimReadsTheServerNode(t *testing.T) {
	f := newRealNodeFixture(t, unclaimedYAML, fakeLocalAPI(t))
	code := startClaim(t, f.guard)
	f.caller = newDevice
	if status, body, _ := f.postClaim(t, code); status != http.StatusOK {
		t.Fatalf("claim with an empty cache = %d %s, want 200", status, body)
	}

	f = newRealNodeFixture(t, unclaimedYAML, "")
	code = startClaim(t, f.guard)
	f.caller = newDevice
	if status, body, _ := f.postClaim(t, code); status != http.StatusServiceUnavailable || errorCode(t, body) != "server_node_unknown" {
		t.Fatalf("claim with tailscaled unreachable = %d %s, want 503 server_node_unknown", status, body)
	}
}
