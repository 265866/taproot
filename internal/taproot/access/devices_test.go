package access

import (
	"testing"
	"time"
)

// TestRequestDevicesOutliveTheRequestBriefly checks a running request stays
// known however long it runs, and a finished one for requestGrace only.
func TestRequestDevicesOutliveTheRequestBriefly(t *testing.T) {
	var r requestDevices
	start := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	laptop := Device{ID: "node:n1", Name: "laptop"}
	r.start("ws", laptop)
	r.start("http", laptop)
	r.finish("http", start)

	// Pruning runs at most once a minute, on a finish.
	r.finish("other", start.Add(requestGrace+2*time.Minute))
	if _, ok := r.get("http"); ok {
		t.Error("finished request still known after the grace period")
	}
	if d, ok := r.get("ws"); !ok || d != laptop {
		t.Errorf("running websocket = %+v %t, want laptop", d, ok)
	}

	r.finish("ws", start.Add(requestGrace+3*time.Minute))
	if _, ok := r.get("ws"); !ok {
		t.Error("websocket forgotten as soon as it finished")
	}
	r.finish("other", start.Add(2*requestGrace+5*time.Minute))
	if _, ok := r.get("ws"); ok {
		t.Error("websocket still known after the grace period")
	}
}

func TestDeviceNames(t *testing.T) {
	g := &Guard{now: time.Now}
	g.selfNode = &selfNode{StableID: "nserver", DNSName: "server.tailnet-0000.ts.net"}
	g.selfExpires = time.Now().Add(time.Hour)
	for _, tc := range []struct {
		id   Identity
		want Device
	}{
		{Identity{Via: ViaLoopback, Node: "127.0.0.1"}, Device{ID: "local", Name: "server (local)", Via: ViaLoopback}},
		{Identity{Via: ViaTailnet, Login: "a@github", Node: "server.tailnet-0000.ts.net", NodeID: "nserver", self: true},
			Device{ID: "local", Name: "server (local)", Login: "a@github", Via: ViaTailnet}},
		{Identity{Via: ViaCIDR, Node: "192.168.0.88"}, Device{ID: "cidr:192.168.0.88", Name: "192.168.0.88", Via: ViaCIDR}},
		{Identity{Via: ViaTailnet, Login: "a@github", Node: "desktop.tailnet-0000.ts.net", NodeID: "nDesk"},
			Device{ID: "node:nDesk", Name: "desktop", Login: "a@github", Via: ViaTailnet}},
		{Identity{Via: ViaTailnet, Login: "b@github", Node: "shared.other.ts.net", NodeID: "nShared"},
			Device{ID: "node:nShared", Name: "shared.other.ts.net", Login: "b@github", Via: ViaTailnet}},
	} {
		if got := g.deviceFor(tc.id); got != tc.want {
			t.Errorf("deviceFor(%+v) = %+v, want %+v", tc.id, got, tc.want)
		}
	}
}
