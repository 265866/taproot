package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
)

// tailnetSuffix is the demo tailnet's MagicDNS suffix.
const tailnetSuffix = "tailnet-0000.ts.net"

// tailnetDevice is a device on the demo tailnet and its grant in taproot.yaml.
type tailnetDevice struct {
	id, name, login, os string
	tags                []string
	ip                  string
	// joined is how long before the demo starts the device joined.
	joined       time.Duration
	proxy, admin bool
	// label is the owner's own name for the device in taproot.yaml.
	label string
	// suffix is the MagicDNS suffix of the other tailnet a device was shared
	// in from; "" means the demo tailnet.
	suffix string
	// sharee marks a device of a person the server is shared with:
	// tailscaled hides its name, MagicDNS name, and OS.
	sharee bool
	// idle is how long before the demo starts the device was last seen;
	// zero means it is online.
	idle time.Duration
}

// hiddenHostName is the HostName tailscaled reports for a sharee's device.
const hiddenHostName = "device-of-shared-to-user"

// shareeCreated is the join date tailscaled reports for a sharee's device.
const shareeCreated = "1973-11-11T00:00:00.1Z"

// danaLogin is the person the demo server is shared with.
const danaLogin = "dana@example.com"

// displayNames are the users' display names where they differ from the login.
var displayNames = map[string]string{danaLogin: "dana", "alice@example.org": "Alice"}

const day = 24 * time.Hour

// serverNode is the taproot server's own node. Usage history names it
// "server (local)", so the node is called server.
var serverNode = tailnetDevice{id: "nSERVER01CNTRL", name: "server", login: demoLogin, os: "linux", ip: "100.64.0.1", joined: 400 * day}

// tailnetDevices are the demo tailnet's other devices. The first four send
// the generated traffic (see devices in traffic.go). work-laptop and
// carol-ipad joined recently with no permissions, so the Access page marks
// them New. dana's three devices are a sharee's, as tailscaled lists every
// device of a person the server is shared with; alice-mbp was shared in from
// another tailnet and has a label. old-tablet has been idle for 45 days.
var tailnetDevices = []tailnetDevice{
	{id: "nLAPTOP01CNTRL", name: "laptop", login: demoLogin, os: "macOS", ip: "100.64.0.2", joined: 210 * day, proxy: true, admin: true},
	{id: "nDESKTOP1CNTRL", name: "desktop", login: demoLogin, os: "linux", ip: "100.64.0.3", joined: 380 * day, proxy: true, admin: true},
	{id: "nWORKSTN1CNTRL", name: "workstation", login: demoLogin, os: "windows", ip: "100.64.0.4", joined: 95 * day, proxy: true},
	{id: "nPHONE001CNTRL", name: "phone", login: demoLogin, os: "iOS", ip: "100.64.0.5", joined: 160 * day, admin: true, idle: 3 * time.Hour},
	{id: "nWORKLAP1CNTRL", name: "work-laptop", login: demoLogin, os: "macOS", ip: "100.64.0.6", joined: 10 * time.Minute},
	{id: "nOLDTAB01CNTRL", name: "old-tablet", login: demoLogin, os: "android", ip: "100.64.0.7", joined: 500 * day, idle: 45 * day},
	{id: "nBOBLAP01CNTRL", name: "bob-laptop", login: "bob@github", os: "windows", ip: "100.64.0.11", joined: 120 * day, proxy: true},
	{id: "nBOBPHN01CNTRL", name: "bob-phone", login: "bob@github", os: "android", ip: "100.64.0.12", joined: 300 * day, idle: 6 * day},
	{id: "nCAROLIP1CNTRL", name: "carol-ipad", login: "carol@github", os: "iOS", ip: "100.64.0.13", joined: 3 * day},
	{id: "nCIRUN001CNTRL", name: "ci-runner", os: "linux", tags: []string{"tag:ci"}, ip: "100.64.0.20", joined: 60 * day, proxy: true},
	danaPC,
	{id: "nDANA0002CNTRL", login: danaLogin, ip: "100.64.0.32", sharee: true, idle: 2 * day},
	{id: "nDANA0003CNTRL", login: danaLogin, ip: "100.64.0.33", sharee: true, idle: 40 * day},
	{id: "nALIMBP01CNTRL", name: "alice-mbp", login: "alice@example.org", os: "macOS", ip: "100.64.0.40", joined: 30 * day, proxy: true, admin: true, label: "Work laptop", suffix: "tailnet-1111.ts.net"},
}

// danaPC is the sharee device that asked for access (see seedRequest).
var danaPC = tailnetDevice{id: "nDANA0001CNTRL", login: danaLogin, ip: "100.64.0.31", sharee: true}

// dnsName is the device's MagicDNS name with its trailing dot, "" for a
// sharee's device.
func (d tailnetDevice) dnsName() string {
	if d.sharee {
		return ""
	}
	return d.name + "." + cmp.Or(d.suffix, tailnetSuffix) + "."
}

// goneDevice has Proxy in taproot.yaml but has left the tailnet, so the
// Access page lists it as no longer on the tailnet until taproot forgets it.
var goneDevice = access.DeviceGrant{ID: "nOLDLAP01CNTRL", Name: "old-laptop", Proxy: true}

// demoAddress reaches the proxy without Tailscale.
var demoAddress = access.AddressGrant{CIDR: "192.0.2.20/32", Note: "Agents on the server host"}

// accessYAML is taproot.yaml's access section for the demo tailnet.
func accessYAML(socket string) string {
	var b strings.Builder
	b.WriteString("access:\n  devices:\n")
	grant := func(id, name, label string, proxy, admin bool) {
		fmt.Fprintf(&b, "    - id: %s\n      name: %s\n", id, name)
		if label != "" {
			fmt.Fprintf(&b, "      label: %s\n", label)
		}
		fmt.Fprintf(&b, "      proxy: %t\n      admin: %t\n", proxy, admin)
	}
	for _, d := range tailnetDevices {
		if d.proxy || d.admin {
			name := d.name
			if d.suffix != "" {
				name = strings.TrimSuffix(d.dnsName(), ".")
			}
			grant(d.id, name, d.label, d.proxy, d.admin)
		}
	}
	grant(goneDevice.ID, goneDevice.Name, "", goneDevice.Proxy, goneDevice.Admin)
	fmt.Fprintf(&b, "  addresses:\n    - cidr: %s\n      note: %s\n", demoAddress.CIDR, demoAddress.Note)
	fmt.Fprintf(&b, "  tailscale-socket: %q\n", socket)
	return b.String()
}

// listenTailnet opens the fake tailscaled's unix socket at path, replacing a
// stale one.
func listenTailnet(ctx context.Context, path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	return ln, nil
}

// serveTailnet answers tailscaled's LocalAPI whois and status on ln, with
// the demo tailnet, until ctx ends. Devices' join times are offsets from
// start.
func serveTailnet(ctx context.Context, ln net.Listener, start time.Time) error {
	srv := &http.Server{Handler: tailnetHandler(start), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// tailnetHandler mirrors the parts of tailscaled's LocalAPI that taproot
// reads: whois, and status with and without peers.
func tailnetHandler(start time.Time) http.Handler {
	all := append([]tailnetDevice{serverNode}, tailnetDevices...)
	userIDs := map[string]int{}
	users := map[string]any{}
	for _, d := range all {
		login := d.login
		if len(d.tags) > 0 {
			login = "tagged-devices"
		}
		if _, ok := userIDs[login]; !ok {
			id := 100 + len(userIDs)
			userIDs[login] = id
			users[strconv.Itoa(id)] = map[string]any{"ID": id, "LoginName": login, "DisplayName": cmp.Or(displayNames[login], login)}
		}
	}
	status := func(d tailnetDevice) map[string]any {
		login := d.login
		if len(d.tags) > 0 {
			login = "tagged-devices"
		}
		s := map[string]any{
			"ID": d.id, "HostName": d.name, "DNSName": d.dnsName(), "OS": d.os,
			"UserID": userIDs[login], "Tags": d.tags, "TailscaleIPs": []string{d.ip},
			"Created": start.Add(-d.joined).UTC().Format(time.RFC3339), "Online": d.idle == 0,
			"LastSeen": time.Time{}.Format(time.RFC3339), "ShareeNode": d.sharee,
		}
		if d.idle > 0 {
			s["LastSeen"] = start.Add(-d.idle).UTC().Format(time.RFC3339)
		}
		if d.sharee {
			s["HostName"], s["Created"] = hiddenHostName, shareeCreated
		}
		return s
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/localapi/v0/status", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{"BackendState": "Running", "MagicDNSSuffix": tailnetSuffix, "Self": status(serverNode)}
		if r.FormValue("peers") == "true" {
			peers := map[string]any{}
			for _, d := range tailnetDevices {
				peers["nodekey:"+d.id] = status(d)
			}
			resp["Peer"], resp["User"] = peers, users
		}
		writeJSON(w, resp)
	})
	mux.HandleFunc("/localapi/v0/whois", func(w http.ResponseWriter, r *http.Request) {
		addr := r.FormValue("addr")
		if host, _, err := net.SplitHostPort(addr); err == nil {
			addr = host
		}
		for _, d := range all {
			if d.ip != addr {
				continue
			}
			login := d.login
			if len(d.tags) > 0 {
				login = "tagged-devices"
			}
			// A sharee's device has no Name, and its computed and reported
			// host names are the placeholder (measured on tailscaled 1.104.2).
			computed := cmp.Or(d.name, hiddenHostName)
			writeJSON(w, map[string]any{
				"Node": map[string]any{
					"StableID": d.id, "Name": d.dnsName(), "ComputedName": computed, "Tags": d.tags,
					"Hostinfo": map[string]any{"Hostname": computed},
				},
				"UserProfile": map[string]any{"LoginName": login, "DisplayName": cmp.Or(displayNames[login], login)},
			})
			return
		}
		http.Error(w, "no match for IP:port", http.StatusNotFound)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
