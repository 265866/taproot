package taproot

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/265866/taproot/sdk/cliproxy/auth"
)

// A missing taproot.yaml stops startup with the path taproot looked at,
// instead of starting with defaults that refuse every caller.
func TestSetupNamesMissingTaprootYAML(t *testing.T) {
	_, err := setupServer(t, "")
	if err == nil || !strings.Contains(err.Error(), "taproot.yaml not found at ") || !strings.Contains(err.Error(), "TAPROOT_CONFIG") {
		t.Fatalf("Setup without taproot.yaml = %v, want an error naming the expected path", err)
	}
	path := strings.TrimPrefix(strings.SplitN(err.Error(), ": ", 2)[0], "taproot.yaml not found at ")
	if filepath.Base(path) != "taproot.yaml" || !filepath.IsAbs(path) {
		t.Fatalf("error names %q, want the absolute path of taproot.yaml beside config.yaml", path)
	}

	// TAPROOT_CONFIG moves the expected path, and the error follows it.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.yaml")
	t.Setenv("TAPROOT_CONFIG", elsewhere)
	if _, err := setupServer(t, ""); err == nil || !strings.Contains(err.Error(), "taproot.yaml not found at "+elsewhere+":") {
		t.Fatalf("Setup with TAPROOT_CONFIG=%s = %v, want an error naming it", elsewhere, err)
	}

	// A relative --config still names where taproot looked, whatever the
	// supervisor's working directory is.
	t.Setenv("TAPROOT_CONFIG", "")
	cwd := t.TempDir()
	t.Chdir(cwd)
	want := "taproot.yaml not found at " + filepath.Join(cwd, "taproot.yaml") + ":"
	if _, _, err := Setup(context.Background(), nil, "config.yaml"); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Setup with --config config.yaml = %v, want %q", err, want)
	}
}

// An enabled guard without tailscaled would refuse every tailnet caller, so
// startup fails and says how to mount the socket; a disabled guard needs none.
func TestSetupRequiresTailscaledSocket(t *testing.T) {
	// Short path: unix socket paths are limited to about 104 bytes on macOS.
	dir, err := os.MkdirTemp("", "ts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// tailscaled's own file name, so the advice is a folder mount.
	missing := filepath.Join(dir, "gone", "tailscaled.sock")
	// A socket file nobody listens on: it exists, but connecting is refused.
	staleDir := filepath.Join(dir, "stale")
	if err := os.Mkdir(staleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(staleDir, "tailscaled.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	// Another file name: a folder mount would not create it, so the advice
	// mounts the socket file itself.
	custom := filepath.Join(dir, "custom.sock")
	for _, tc := range []struct{ name, socket, want, mount string }{
		{"missing", missing, "tailscaled socket " + missing + " does not exist", "Volume=/var/run/tailscale:" + filepath.ToSlash(filepath.Dir(missing)) + ":ro"},
		{"nobody listening", stale, "tailscaled socket " + stale + " is unreachable", "Volume=/var/run/tailscale:" + filepath.ToSlash(staleDir) + ":ro"},
		{"another file name", custom, "tailscaled socket " + custom + " does not exist", "Volume=/var/run/tailscale/tailscaled.sock:" + filepath.ToSlash(custom) + ":ro"},
	} {
		tp := "access:\n  tailscale-socket: " + filepath.ToSlash(tc.socket) + "\n"
		_, err := setupServer(t, tp)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.mount) ||
			!strings.Contains(err.Error(), "access.enabled: false") || !strings.Contains(err.Error(), "taproot.yaml") {
			t.Errorf("%s: Setup = %v, want %q and %q", tc.name, err, tc.want, tc.mount)
		}
	}

	tp := "access:\n  enabled: false\n  tailscale-socket: " + filepath.ToSlash(missing) + "\nbackups:\n  daily-at: \"off\"\n"
	if _, err := setupServer(t, tp); err != nil {
		t.Fatalf("Setup with the guard disabled and no socket = %v, want success", err)
	}
}

// With no account at all, upstream's "unknown provider for model" becomes a
// message that says what to do. Once an account exists, the message is upstream's.
func TestEmptyPoolSaysNoAccounts(t *testing.T) {
	f := newServer(t)
	requests := []struct{ path, body, model string }{
		{"/v1/messages", `{"model":"claude-sonnet-4-5","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, "claude-sonnet-4-5"},
		{"/v1/messages", `{"model":"claude-sonnet-4-5","stream":true,"max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`, "claude-sonnet-4-5"},
		{"/v1/chat/completions", `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`, "gpt-5"},
		{"/v1/responses", `{"model":"gpt-5","stream":true,"input":"hi"}`, "gpt-5"},
		// The model name is the client's, so it stays escaped in the rewritten body.
		{"/backend-api/codex/responses", `{"model":"gpt-5\"x","stream":true,"input":"hi"}`, "gpt-5\"x"},
	}
	send := func(path, body string) (int, string) {
		rec := f.do(req{remote: "127.0.0.1", host: "localhost:8317", method: http.MethodPost, path: path, headers: map[string]string{"Content-Type": "application/json"}, body: body})
		var parsed struct{ Error struct{ Message string } }
		if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
			t.Fatalf("POST %s = %d %q, not JSON: %v", path, rec.Code, rec.Body, err)
		}
		return rec.Code, parsed.Error.Message
	}
	for _, r := range requests {
		code, msg := send(r.path, r.body)
		want := "no accounts configured: sign in to an account on the taproot dashboard's Accounts page, then retry model " + r.model
		if code != http.StatusBadRequest || msg != want {
			t.Errorf("POST %s with no accounts = %d %q, want 400 %q", r.path, code, msg, want)
		}
	}

	if _, err := f.auths.Register(context.Background(), &auth.Auth{ID: "claude-x.json", Provider: "claude"}); err != nil {
		t.Fatal(err)
	}
	for _, r := range requests {
		code, msg := send(r.path, r.body)
		if want := "unknown provider for model " + r.model; code != http.StatusBadRequest || msg != want {
			t.Errorf("POST %s with an account = %d %q, want upstream's 400 %q", r.path, code, msg, want)
		}
	}
}
