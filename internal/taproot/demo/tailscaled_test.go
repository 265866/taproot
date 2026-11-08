package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/taproot"
	"github.com/265866/taproot/internal/taproot/access"
)

// The demo's taproot.yaml names a socket that passes the server's startup
// check once the stand-in serves it, and the stand-in answers every LocalAPI
// call with an error, so the guard never learns about a tailnet.
func TestStandInTailscaledPassesStartupCheck(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "demo-test-only")
	// Short path: seed puts the socket in dir, and unix socket paths are
	// limited to about 104 bytes on macOS.
	dir, err := os.MkdirTemp("", "demo")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if _, err := seed(context.Background(), dir, 8399, testStart); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tp, err := taproot.LoadConfig(filepath.Join(dir, "taproot.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.FromSlash(tp.Access.TailscaleSocket)
	if socket != filepath.Join(dir, tailscaledSocket) {
		t.Fatalf("taproot.yaml names socket %q, want %q", socket, filepath.Join(dir, tailscaledSocket))
	}
	guard, err := access.New(tp.Access)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.CheckTailscaled(context.Background()); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("CheckTailscaled before the stand-in = %v, want the missing-socket error", err)
	}

	ln, err := listenTailscaled(socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- serveTailscaled(ctx, ln) }()
	if err := guard.CheckTailscaled(context.Background()); err != nil {
		t.Fatalf("CheckTailscaled with the stand-in = %v", err)
	}

	client := http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}}
	resp, err := client.Get("http://local-tailscaled.sock/localapi/v0/status?peers=false")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stand-in status = %d %s, want 503", resp.StatusCode, body)
	}

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serveTailscaled after cancel = %v, want nil", err)
	}
}
