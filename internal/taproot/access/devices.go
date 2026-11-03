package access

import (
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Device is the client device behind a request. Ingest stores it on each
// usage event so usage can be grouped by device.
type Device struct {
	// ID groups requests: "node:<StableID>" for a tailnet node, "local" for
	// loopback and the server's own node, "cidr:<address>" for a
	// trusted-cidrs caller, and "plugin:<plugin ID>" (or "plugins" when the
	// plugin is unknown) for a plugin's in-process request.
	ID string
	// Name is for display: the node's MagicDNS name (its first label inside
	// the server's tailnet), "<server> (local)", the caller's address
	// ("<server> (via <address>)" when it is one of the server's own), or the
	// plugin's name.
	Name  string
	Login string
	Via   Via
}

const (
	// LocalDeviceID is the device ID of loopback callers and the server's own node.
	LocalDeviceID = "local"
	// PluginsDeviceID groups in-process plugin requests whose plugin is unknown.
	PluginsDeviceID = "plugins"

	cidrDevicePrefix   = "cidr:"
	pluginDevicePrefix = "plugin:"
)

// PluginDevice is the device of a model request a plugin made in-process,
// through the plugin host rather than HTTP. name is the plugin's display
// name; an empty pluginID means the plugin is unknown.
func PluginDevice(pluginID, name string) Device {
	if pluginID == "" {
		return Device{ID: PluginsDeviceID, Name: "In-process plugins", Via: ViaPlugin}
	}
	if name == "" {
		name = pluginID
	}
	return Device{ID: pluginDevicePrefix + pluginID, Name: name, Via: ViaPlugin}
}

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
		return Device{ID: cidrDevicePrefix + id.Node, Name: g.cidrDeviceName(id.Node, self), Via: id.Via}
	}
	d := Device{ID: "node:" + id.NodeID, Name: shortNodeName(id.Node, self), Login: id.Login, Via: id.Via}
	if id.NodeID == "" {
		d.ID = "node:" + strings.ToLower(id.Node)
	}
	return d
}

// DeviceName returns the name to show for a stored device, given its ID and
// the name stored with it. Trusted-cidrs devices are named here at read time,
// so history recorded before the server knew an address as its own shows the
// current name too.
func (g *Guard) DeviceName(id, stored string) string {
	if addr, ok := strings.CutPrefix(id, cidrDevicePrefix); ok {
		self, _ := g.cachedSelf()
		return g.cidrDeviceName(addr, self)
	}
	return stored
}

// cidrDeviceName names a trusted-cidrs caller by its address, or, when the
// address is one of the server's own, after the server with the address it
// came from. Only a process on the server host connects from an own address:
// on server, the 127.0.1.1 publish arrives through pasta as the LAN address.
// The address stays in the name because it is what separates this route from
// "<server> (local)" and what trusted-cidrs lists.
func (g *Guard) cidrDeviceName(addr string, self *selfNode) string {
	a, err := netip.ParseAddr(addr)
	if err != nil || !g.own.contains(a, g.now()) {
		return addr
	}
	return serverName(self) + " (via " + addr + ")"
}

// ownAddrTTL is how long the server's interface addresses are reused before
// they are listed again. They change only when the host's network does.
const ownAddrTTL = time.Minute

// ownAddrs caches the server's own interface addresses. Inside a rootless
// Podman container with pasta networking, these include the host's LAN
// address, which pasta copies into the container.
type ownAddrs struct {
	// list returns interface addresses; nil means net.InterfaceAddrs.
	list    func() ([]net.Addr, error)
	mu      sync.Mutex
	addrs   map[netip.Addr]bool
	expires time.Time
}

func (o *ownAddrs) contains(addr netip.Addr, now time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !now.Before(o.expires) {
		o.refresh(now)
	}
	return o.addrs[addr.Unmap().WithZone("")]
}

// refresh lists the interface addresses again. After a failure it keeps the
// last list it read.
func (o *ownAddrs) refresh(now time.Time) {
	o.expires = now.Add(ownAddrTTL)
	list := o.list
	if list == nil {
		list = net.InterfaceAddrs
	}
	raw, err := list()
	if err != nil {
		log.WithError(err).Warn("taproot access: list the server's interface addresses")
		return
	}
	addrs := make(map[netip.Addr]bool, len(raw))
	for _, a := range raw {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if addr, ok := netip.AddrFromSlice(ip); ok {
			addrs[addr.Unmap()] = true
		}
	}
	o.addrs = addrs
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
