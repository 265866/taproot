package access

import (
	"os"
	"strings"
	"sync"
	"time"
)

// Device is the client device behind an admitted request. Ingest stores it on
// each usage event so usage can be grouped by device.
type Device struct {
	// ID groups requests: "node:<StableID>" for a tailnet node, "local" for
	// loopback and the server's own node, and "cidr:<address>" for a
	// trusted-cidrs caller.
	ID string
	// Name is for display: the node's MagicDNS name (its first label inside
	// the server's tailnet), "<server> (local)", or the caller's address.
	Name  string
	Login string
	Via   Via
}

// LocalDeviceID is the device ID of loopback callers and the server's own node.
const LocalDeviceID = "local"

// requestGrace is how long a finished request's device stays known. Usage
// records are delivered on another goroutine after the handler returns, and
// ingest resolves them within about a second.
const requestGrace = 10 * time.Minute

// requestDevices maps upstream's per-request ID (the ID usage records carry
// as TraceID) to the device the guard admitted. Entries live while the request
// runs, which for a Codex websocket is the whole connection, and for
// requestGrace after.
type requestDevices struct {
	mu        sync.Mutex
	entries   map[string]*requestEntry
	lastPrune time.Time
}

type requestEntry struct {
	device Device
	// done is zero while the request runs.
	done time.Time
}

func (r *requestDevices) start(requestID string, d Device) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]*requestEntry{}
	}
	r.entries[requestID] = &requestEntry{device: d}
}

func (r *requestDevices) finish(requestID string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[requestID]; ok {
		e.done = now
	}
	if now.Sub(r.lastPrune) < time.Minute {
		return
	}
	r.lastPrune = now
	for id, e := range r.entries {
		if !e.done.IsZero() && now.Sub(e.done) > requestGrace {
			delete(r.entries, id)
		}
	}
}

func (r *requestDevices) get(requestID string) (Device, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[requestID]
	if !ok {
		return Device{}, false
	}
	return e.device, true
}

// RequestDevice returns the device the guard admitted for the request with
// upstream's request ID (usage.Record.TraceID). It knows requests that are
// running or finished within the last ten minutes.
func (g *Guard) RequestDevice(requestID string) (Device, bool) {
	if requestID == "" {
		return Device{}, false
	}
	return g.requests.get(requestID)
}

// deviceFor names the device of an admitted caller. It reads the server's own
// node from cache only, so attribution never waits on tailscaled.
func (g *Guard) deviceFor(id Identity) Device {
	self, _ := g.cachedSelf()
	switch {
	case id.Via == ViaLoopback || id.self:
		return Device{ID: LocalDeviceID, Name: serverName(self) + " (local)", Login: id.Login, Via: id.Via}
	case id.Via == ViaCIDR:
		return Device{ID: "cidr:" + id.Node, Name: id.Node, Via: id.Via}
	}
	d := Device{ID: "node:" + id.NodeID, Name: shortNodeName(id.Node, self), Login: id.Login, Via: id.Via}
	if id.NodeID == "" {
		d.ID = "node:" + strings.ToLower(id.Node)
	}
	return d
}

// serverName is the first label of the server's MagicDNS name, or the host
// name when tailscaled has not answered (as on a development machine).
func serverName(self *selfNode) string {
	if self != nil {
		if label, _, _ := strings.Cut(self.DNSName, "."); label != "" {
			return label
		}
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		label, _, _ := strings.Cut(host, ".")
		return strings.ToLower(label)
	}
	return "server"
}

// shortNodeName drops the server's tailnet suffix, so nodes in the same
// tailnet show as "desktop" and shared-in nodes keep their full name.
func shortNodeName(node string, self *selfNode) string {
	node = strings.TrimSuffix(node, ".")
	if self != nil {
		if _, suffix, ok := strings.Cut(self.DNSName, "."); ok && suffix != "" {
			if short, ok := strings.CutSuffix(node, "."+suffix); ok {
				return short
			}
		}
	}
	return node
}
