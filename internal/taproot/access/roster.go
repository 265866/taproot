package access

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// ForgetAfter is how long a device with a permission may be missing from
// tailscaled's list, across successful reads, before taproot forgets it.
// Tailscale never reuses a stable ID, so a removed device cannot come back
// under it, and keeping its entry would only clutter the list.
const ForgetAfter = 24 * time.Hour

// missingDevices records when each tracked device (a granted device, or the
// device of a stored access request) was first missing from a successful
// read that listed peers. It is in memory only, so a restart starts every
// device's 24 hours over.
type missingDevices struct {
	mu    sync.Mutex
	since map[string]time.Time
}

// ReadTailnet reads the server's node and its peers from tailscaled. A read
// that lists at least one peer is recorded: a granted device it lacks starts
// or continues its ForgetAfter, and one it lists stops it. A failed or empty
// read records nothing, so a tailscaled restart cannot make taproot forget a
// device.
func (g *Guard) ReadTailnet(ctx context.Context) (Tailnet, error) {
	if g.tailnet == nil {
		return Tailnet{}, errors.New("the access guard is disabled (access.enabled: false), so taproot does not read the tailnet")
	}
	tn, err := g.tailnet(ctx)
	if err != nil {
		return Tailnet{}, err
	}
	if len(tn.Peers) > 0 {
		g.observe(tn, g.now())
	}
	return tn, nil
}

func (g *Guard) observe(tn Tailnet, now time.Time) {
	set := g.grants.Load()
	ids := make([]string, 0, len(set.Devices))
	for _, d := range set.Devices {
		ids = append(ids, d.ID)
	}
	g.missing.track(ids, tn.ids(), now)
}

// track records when each of ids was first missing from present, drops
// every other ID, and returns a copy of the result.
func (m *missingDevices) track(ids []string, present map[string]bool, now time.Time) map[string]time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := map[string]time.Time{}
	for _, id := range ids {
		if present[id] {
			continue
		}
		if since, ok := m.since[id]; ok {
			next[id] = since
		} else {
			next[id] = now
		}
	}
	m.since = next
	return maps.Clone(next)
}

func (g *Guard) missingSince(id string) (time.Time, bool) {
	g.missing.mu.Lock()
	defer g.missing.mu.Unlock()
	since, ok := g.missing.since[id]
	return since, ok
}

// Reconcile returns the current grants with each device's name refreshed
// from tn, and without the devices that successful reads have missed for
// ForgetAfter, which it also returns as forgotten. Pass the Tailnet that
// ReadTailnet returned last. changed is false when the grants would not
// change.
func (g *Guard) Reconcile(tn Tailnet) (next Grants, forgotten []DeviceGrant, changed bool) {
	now := g.now()
	listed := tn.byID()
	cur := g.Grants()
	g.missing.mu.Lock()
	since := maps.Clone(g.missing.since)
	g.missing.mu.Unlock()
	next = Grants{Devices: make([]DeviceGrant, 0, len(cur.Devices)), Addresses: cur.Addresses}
	for _, d := range cur.Devices {
		if dev, ok := listed[d.ID]; ok {
			if dev.Name != "" && dev.Name != d.Name {
				d.Name = dev.Name
				changed = true
			}
			next.Devices = append(next.Devices, d)
			continue
		}
		if first, ok := since[d.ID]; ok && len(tn.Peers) > 0 && now.Sub(first) >= ForgetAfter {
			forgotten = append(forgotten, d)
			changed = true
			continue
		}
		next.Devices = append(next.Devices, d)
	}
	return next, forgotten, changed
}

func (tn Tailnet) all() []TailnetDevice {
	return append([]TailnetDevice{tn.Self}, tn.Peers...)
}

func (tn Tailnet) ids() map[string]bool {
	ids := make(map[string]bool, len(tn.Peers)+1)
	for _, d := range tn.all() {
		ids[d.ID] = true
	}
	return ids
}

func (tn Tailnet) byID() map[string]TailnetDevice {
	byID := make(map[string]TailnetDevice, len(tn.Peers)+1)
	for _, d := range tn.all() {
		byID[d.ID] = d
	}
	return byID
}

// deviceView is one row of GET /access/devices.
type deviceView struct {
	ID string `json:"id"`
	// Name is the Tailscale name, "" when Tailscale hides it.
	Name string `json:"name"`
	// Label is the owner's name for the device from access.devices.
	Label   string `json:"label"`
	DNSName string `json:"dnsName"`
	// Login is the owner's login; "" for a tagged device and for a device
	// that is no longer on the tailnet.
	Login     string     `json:"login"`
	OwnerName string     `json:"ownerName"`
	Tags      []string   `json:"tags"`
	OS        string     `json:"os"`
	Created   *time.Time `json:"created"`
	// Outside marks a device outside the server's own tailnet.
	Outside  bool       `json:"outside"`
	Online   bool       `json:"online"`
	LastSeen *time.Time `json:"lastSeen,omitempty"`
	// Addresses are the device's Tailscale IPs, IPv4 first; empty for a
	// device the last read did not list.
	Addresses []string `json:"addresses"`
	// Server marks the taproot server's own node, which always has Proxy and
	// never Admin.
	Server bool `json:"server"`
	// OnTailnet is false for a granted device the last read did not list.
	OnTailnet bool `json:"onTailnet"`
	// MissingSince is when successful reads stopped listing the device.
	MissingSince *time.Time `json:"missingSince"`
	Proxy        bool       `json:"proxy"`
	Admin        bool       `json:"admin"`
}

// requestView is one open access request on GET /access/devices.
type requestView struct {
	NodeID    string `json:"nodeId"`
	Login     string `json:"login"`
	OwnerName string `json:"ownerName"`
	// Name is the name the device gave itself, else its Tailscale name.
	Name      string    `json:"name"`
	Client    string    `json:"client"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Count     int       `json:"count"`
	Outside   bool      `json:"outside"`
	// Asked is what the device was refused: "admin" for the dashboard,
	// else "proxy".
	Asked Ask `json:"asked"`
}

type devicesResponse struct {
	Enabled bool `json:"enabled"`
	// Version identifies the device and address lists this response holds.
	// PUT /settings refuses a save from an older version.
	Version   string         `json:"version"`
	Devices   []deviceView   `json:"devices"`
	Requests  []requestView  `json:"requests"`
	Addresses []AddressGrant `json:"addresses"`
	// TailnetError says why tailscaled could not be read. Devices then lists
	// only the devices that have a permission.
	TailnetError     string `json:"tailnetError,omitempty"`
	ForgetAfterHours int    `json:"forgetAfterHours"`
}

// handleDevices joins tailscaled's device list with access.devices. It reads
// only; Reconcile's changes are written by the settings module.
func (g *Guard) handleDevices(c *gin.Context) {
	grants := g.Grants()
	resp := devicesResponse{
		Enabled: g.enabled, Version: grants.Version(), Devices: []deviceView{}, Requests: []requestView{}, Addresses: grants.Addresses,
		ForgetAfterHours: int(ForgetAfter / time.Hour),
	}
	byID := make(map[string]DeviceGrant, len(grants.Devices))
	for _, d := range grants.Devices {
		byID[d.ID] = d
	}
	listed := map[string]TailnetDevice{}
	tn, err := g.ReadTailnet(c.Request.Context())
	if err != nil {
		if g.enabled {
			log.Warnf("taproot access: read the tailnet for the Access page: %v", err)
		}
		resp.TailnetError = err.Error()
	} else {
		for i, d := range tn.all() {
			v := deviceView{
				ID: d.ID, Name: d.Name, Label: byID[d.ID].Label, DNSName: d.DNSName, Login: d.Login, OwnerName: d.OwnerName,
				Tags: d.Tags, OS: d.OS, Outside: d.Outside, Online: d.Online, OnTailnet: true, Addresses: addrStrings(d.Addresses),
			}
			if v.Tags == nil {
				v.Tags = []string{}
			}
			if !d.Created.IsZero() {
				created := d.Created.UTC()
				v.Created = &created
			}
			if !d.LastSeen.IsZero() {
				seen := d.LastSeen.UTC()
				v.LastSeen = &seen
			}
			if i == 0 {
				v.Server, v.Proxy = true, true
			} else {
				v.Proxy, v.Admin = byID[d.ID].Proxy, byID[d.ID].Admin
			}
			listed[d.ID] = d
			resp.Devices = append(resp.Devices, v)
		}
	}
	for _, d := range grants.Devices {
		if _, ok := listed[d.ID]; ok {
			continue
		}
		v := deviceView{ID: d.ID, Name: d.Name, Label: d.Label, Tags: []string{}, Addresses: []string{}, Proxy: d.Proxy, Admin: d.Admin}
		if since, ok := g.missingSince(d.ID); ok {
			since = since.UTC()
			v.MissingSince = &since
		}
		resp.Devices = append(resp.Devices, v)
	}
	resp.Requests = g.requestViews(c.Request.Context(), byID, listed)
	c.JSON(http.StatusOK, resp)
}

// requestViews lists the open requests, without those of devices that
// already have both permissions. listed is the last tailnet read, which
// names and places devices more freshly than the stored request.
func (g *Guard) requestViews(ctx context.Context, grants map[string]DeviceGrant, listed map[string]TailnetDevice) []requestView {
	views := []requestView{}
	asks := g.asks.Load()
	if asks == nil {
		return views
	}
	open, err := asks.Open(ctx)
	if err != nil {
		log.WithError(err).Warn("taproot access: list access requests")
		return views
	}
	for _, a := range open {
		if grant := grants[a.NodeID]; grant.Proxy && grant.Admin {
			continue
		}
		v := requestView{
			NodeID: a.NodeID, Login: a.Login, OwnerName: a.OwnerName, Name: cmp.Or(a.NameHint, a.Node), Client: a.Client,
			FirstSeen: a.FirstSeen, LastSeen: a.LastSeen, Count: a.Count, Outside: a.Outside, Asked: cmp.Or(a.Asked, AskProxy),
		}
		if d, ok := listed[a.NodeID]; ok {
			v.Outside = d.Outside
			v.Name = cmp.Or(a.NameHint, d.Name)
			v.OwnerName = cmp.Or(d.OwnerName, v.OwnerName)
		}
		views = append(views, v)
	}
	return views
}

// addrStrings formats addrs for the API, as an empty list rather than null.
func addrStrings(addrs []netip.Addr) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}
