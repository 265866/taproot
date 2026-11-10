package access

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// tailscaledProbeInterval spaces the monitor's LocalAPI status reads.
	tailscaledProbeInterval = 30 * time.Second
	// tailscaledDownAfter is how long LocalAPI calls must fail before the
	// monitor reports an outage. A tailscaled restart takes seconds.
	tailscaledDownAfter = 2 * time.Minute
	// probeJitter lets the probe that runs tailscaledDownAfter after the
	// first failure report, though it measures a few milliseconds less.
	probeJitter = time.Second
	// troubleshootingEntry names the docs entry the error points to.
	troubleshootingEntry = `"Every tailnet device is refused after tailscaled restarts or updates" in docs/troubleshooting.md`
)

// TailscaledNotice reports that tailscaled's LocalAPI went down or answers again.
type TailscaledNotice struct {
	Socket string
	// Down is true when calls have failed for tailscaledDownAfter, false when
	// they succeed again after a reported outage.
	Down bool
	// Since is when the calls started failing.
	Since time.Time
	At    time.Time
	// Err is the last failure.
	Err string
	// StaleSocket is true when the socket is missing or nobody listens on it,
	// which is what a container sees after tailscaled restarts under a stale
	// mount.
	StaleSocket bool
}

// tailscaledMonitor turns probe results into one notice per outage and one
// per recovery. observe is called from one goroutine at a time.
type tailscaledMonitor struct {
	socket string
	notify func(TailscaledNotice)

	failingSince time.Time
	reported     bool
}

// MonitorTailscaled reads tailscaled's status every 30 s until ctx ends.
// When the reads have failed for 2 minutes it logs an error and calls notify
// once; when they succeed again it logs and calls notify once more. It does
// nothing while the guard is off. Each read has the LocalAPI client's 3 s
// timeout.
func (g *Guard) MonitorTailscaled(ctx context.Context, notify func(TailscaledNotice)) {
	if !g.enabled {
		return
	}
	m := &tailscaledMonitor{socket: g.socket, notify: notify}
	go func() {
		ticker := time.NewTicker(tailscaledProbeInterval)
		defer ticker.Stop()
		for {
			_, err := g.status(ctx)
			if ctx.Err() != nil {
				return
			}
			m.observe(g.now(), err)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (m *tailscaledMonitor) observe(now time.Time, err error) {
	if err == nil {
		if m.failingSince.IsZero() {
			return
		}
		since := m.failingSince
		m.failingSince = time.Time{}
		if !m.reported {
			log.Infof("taproot access: tailscaled LocalAPI at %s answers again", m.socket)
			return
		}
		m.reported = false
		log.Infof("taproot access: tailscaled LocalAPI at %s answers again after %s; tailnet devices are identified again", m.socket, now.Sub(since).Round(time.Second))
		m.send(TailscaledNotice{Socket: m.socket, Since: since, At: now})
		return
	}
	if m.failingSince.IsZero() {
		m.failingSince = now
		log.Warnf("taproot access: tailscaled LocalAPI at %s failed: %s", m.socket, briefError(err))
	}
	if m.reported || now.Sub(m.failingSince) < tailscaledDownAfter-probeJitter {
		return
	}
	m.reported = true
	stale := staleSocket(err)
	hint := "check that tailscaled runs and is logged in on the host (tailscale status)"
	if stale {
		hint = "if tailscaled runs on the host, taproot likely holds a stale socket from before tailscaled restarted or updated (a container that mounts the socket file, or the folder without RuntimeDirectoryPreserve=yes on tailscaled): restart taproot"
	}
	log.Errorf("taproot access: tailscaled LocalAPI at %s has failed for %s (%v), so every tailnet device is refused, admins included; %s. See %s",
		m.socket, now.Sub(m.failingSince).Round(time.Second), briefError(err), hint, troubleshootingEntry)
	m.send(TailscaledNotice{Socket: m.socket, Down: true, Since: m.failingSince, At: now, Err: briefError(err), StaleSocket: stale})
}

func (m *tailscaledMonitor) send(n TailscaledNotice) {
	if m.notify != nil {
		m.notify(n)
	}
}

// staleSocket reports whether err is a missing socket or one nobody listens on.
func staleSocket(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}

// briefError drops the LocalAPI URL that net/http puts in front of a dial
// error, so the alert's short error field keeps the cause.
func briefError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err.Error()
	}
	return err.Error()
}
