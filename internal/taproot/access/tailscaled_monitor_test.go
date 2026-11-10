package access

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// probeErr returns the error a real LocalAPI status read gives for socket.
func probeErr(t *testing.T, socket string) error {
	t.Helper()
	_, err := newLocalClient(socket).status(context.Background())
	if err == nil {
		t.Fatalf("status on %s succeeded", socket)
	}
	return err
}

// A socket that is missing or that nobody listens on is what a container
// sees after tailscaled restarts under a stale mount.
func TestStaleSocketErrors(t *testing.T) {
	dir, err := os.MkdirTemp("", "ts")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	missing := probeErr(t, filepath.Join(dir, "missing.sock"))
	if !staleSocket(missing) {
		t.Fatalf("missing socket: staleSocket(%v) = false", missing)
	}
	if brief := briefError(missing); strings.Contains(brief, localAPIHost) || !strings.Contains(brief, "missing.sock") {
		t.Fatalf("briefError = %q, want the dial error without the URL", brief)
	}

	// A socket file whose listener is gone, as a file bind mount keeps it.
	dead := filepath.Join(dir, "dead.sock")
	ln, err := net.Listen("unix", dead)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if refused := probeErr(t, dead); !staleSocket(refused) {
		t.Fatalf("dead socket: staleSocket(%v) = false", refused)
	}

	if staleSocket(errors.New("tailscale status: status 503: no backend")) {
		t.Fatal("an error from a running tailscaled is not a stale socket")
	}
}

func TestMonitorReportsOneOutageAndOneRecovery(t *testing.T) {
	var got []TailscaledNotice
	m := &tailscaledMonitor{socket: "/var/run/tailscale/tailscaled.sock", notify: func(n TailscaledNotice) { got = append(got, n) }}
	t0 := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	down := probeErr(t, filepath.Join(t.TempDir(), "gone.sock"))

	m.observe(t0, nil)
	if len(got) != 0 {
		t.Fatalf("a success with no outage notified: %+v", got)
	}
	// Failures shorter than the threshold notify nothing.
	start := t0.Add(30 * time.Second)
	for at := start; at.Sub(start) < tailscaledDownAfter; at = at.Add(tailscaledProbeInterval) {
		m.observe(at, down)
	}
	if len(got) != 0 {
		t.Fatalf("notified before %s: %+v", tailscaledDownAfter, got)
	}
	// The probe at the threshold reports once, though it reads a few
	// milliseconds early, as a real probe does; later ones do not.
	for i := range 10 {
		m.observe(start.Add(tailscaledDownAfter-3*time.Millisecond+time.Duration(i)*tailscaledProbeInterval), down)
	}
	if len(got) != 1 {
		t.Fatalf("notices = %d, want 1: %+v", len(got), got)
	}
	n := got[0]
	if !n.Down || !n.StaleSocket || !n.Since.Equal(start) || !n.At.Equal(start.Add(tailscaledDownAfter-3*time.Millisecond)) ||
		n.Socket != m.socket || !strings.Contains(n.Err, "gone.sock") {
		t.Fatalf("outage notice = %+v", n)
	}

	recovered := start.Add(10 * time.Minute)
	m.observe(recovered, nil)
	m.observe(recovered.Add(tailscaledProbeInterval), nil)
	if len(got) != 2 {
		t.Fatalf("notices = %d, want 2: %+v", len(got), got)
	}
	if r := got[1]; r.Down || !r.Since.Equal(start) || !r.At.Equal(recovered) {
		t.Fatalf("recovery notice = %+v", r)
	}

	// A short blip after the recovery stays quiet, and a second outage
	// reports again with its own start.
	m.observe(recovered.Add(time.Minute), down)
	m.observe(recovered.Add(2*time.Minute), nil)
	if len(got) != 2 {
		t.Fatalf("a blip notified: %+v", got[2:])
	}
	again := recovered.Add(time.Hour)
	m.observe(again, errors.New("tailscale status: status 503: no backend"))
	m.observe(again.Add(tailscaledDownAfter), errors.New("tailscale status: status 503: no backend"))
	if len(got) != 3 || !got[2].Down || got[2].StaleSocket || !got[2].Since.Equal(again) {
		t.Fatalf("second outage = %+v", got[2:])
	}
}

// An outage that starts with taproot, before any read succeeded, reports too.
func TestMonitorReportsAnOutageFromStart(t *testing.T) {
	var got []TailscaledNotice
	m := &tailscaledMonitor{socket: "s", notify: func(n TailscaledNotice) { got = append(got, n) }}
	t0 := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	err := errors.New("tailscale status: tailscaled is \"Stopped\", not Running")
	m.observe(t0, err)
	m.observe(t0.Add(tailscaledDownAfter), err)
	if len(got) != 1 || !got[0].Down || !got[0].Since.Equal(t0) {
		t.Fatalf("notices = %+v", got)
	}
}

// The monitor reads tailscaled through the guard's own LocalAPI client and
// stops with its context; with the guard off it does nothing.
func TestMonitorTailscaledProbesAndStops(t *testing.T) {
	f := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: f.socket})
	ctx, cancel := context.WithCancel(context.Background())
	g.MonitorTailscaled(ctx, func(TailscaledNotice) {})
	waitUntil(t, func() bool { return f.statusCount() >= 1 })
	cancel()

	off := false
	f2 := newFakeTailscaled(t, defaultPeers())
	g2 := newTestGuard(t, Config{Enabled: &off, TailscaleSocket: f2.socket})
	g2.MonitorTailscaled(context.Background(), func(TailscaledNotice) {})
	if f2.statusCount() != 0 {
		t.Fatal("a disabled guard probed tailscaled")
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
