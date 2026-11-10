package access

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/265866/taproot/internal/wsrelay"
)

// echoSocket upgrades and echoes every message until the connection ends.
func echoSocket(c *gin.Context) {
	conn, err := (&websocket.Upgrader{}).Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := conn.WriteMessage(mt, msg); err != nil {
			return
		}
	}
}

// socketServer serves an echo websocket on a proxy route and on /v1/ws, an
// admin route, behind the guard, as a caller from addr.
func socketServer(t *testing.T, g *Guard, addr string) string {
	t.Helper()
	return serveAs(t, g, addr, func(e *gin.Engine) {
		e.GET("/v1/responses", echoSocket)
		e.GET("/v1/ws", echoSocket)
	})
}

// serveAs serves the routes register adds behind the guard, as a caller from
// addr, and returns the server's ws:// URL.
func serveAs(t *testing.T, g *Guard, addr string, register func(*gin.Engine)) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(g.Middleware())
	register(e)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := net.SplitHostPort(r.RemoteAddr)
		r.RemoteAddr = net.JoinHostPort(addr, port)
		r.Host = "server:8317"
		e.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dialSocket(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v (%v)", url, err, resp)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// turn sends one message and reports whether the server echoed it. The
// deadline only bounds a broken test; a revoked socket fails at once.
func turn(t *testing.T, conn *websocket.Conn) bool {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("turn")); err != nil {
		return false
	}
	_, msg, err := conn.ReadMessage()
	return err == nil && string(msg) == "turn"
}

// A websocket is admitted once, so taking away the permission it was admitted
// with closes it; a change that leaves the permission keeps it open.
func TestRevokedGrantClosesOpenWebsockets(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	for _, tc := range []struct {
		name    string
		addr    string
		path    string
		cfg     Config
		keep    Grants
		revoked Grants
	}{
		{
			name:    "Proxy",
			addr:    otherNodeIP,
			path:    "/v1/responses",
			keep:    Grants{Devices: append(append([]DeviceGrant{}, defaultDevices...), DeviceGrant{ID: bobID, Proxy: true})},
			revoked: Grants{Devices: []DeviceGrant{defaultDevices[0]}},
		},
		{
			name:    "Admin",
			addr:    adminIP,
			path:    "/v1/ws",
			keep:    Grants{Devices: []DeviceGrant{{ID: laptopID, Admin: true}}},
			revoked: Grants{Devices: []DeviceGrant{{ID: laptopID, Proxy: true}}},
		},
		{
			name:    "address",
			addr:    "192.0.2.20",
			path:    "/v1/responses",
			cfg:     Config{Addresses: []AddressGrant{{CIDR: "192.0.2.20/32"}}},
			keep:    Grants{Devices: defaultDevices, Addresses: []AddressGrant{{CIDR: "192.0.2.0/24"}}},
			revoked: Grants{Devices: defaultDevices, Addresses: []AddressGrant{{CIDR: "192.0.2.21/32"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.TailscaleSocket = ts.socket
			g := newTestGuard(t, tc.cfg)
			url := socketServer(t, g, tc.addr) + tc.path
			conn := dialSocket(t, url)
			if !turn(t, conn) {
				t.Fatal("the first turn was not served")
			}
			if err := g.SetGrants(tc.keep); err != nil {
				t.Fatal(err)
			}
			if !turn(t, conn) {
				t.Fatal("a change that keeps the permission closed the websocket")
			}
			if err := g.SetGrants(tc.revoked); err != nil {
				t.Fatal(err)
			}
			if turn(t, conn) {
				t.Fatal("the websocket was still served after its permission was revoked")
			}
			if _, resp, err := websocket.DefaultDialer.Dial(url, nil); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Fatalf("a new websocket after revocation: %v %v", resp, err)
			}
		})
	}
}

// Upstream's /v1/ws relay returns from its handler right after the upgrade
// and serves the session from another goroutine, so the socket must stay
// tracked after the request ends.
func TestRevokedAdminClosesRelaySocket(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	if err := g.SetGrants(Grants{Devices: []DeviceGrant{{ID: laptopID, Admin: true}}}); err != nil {
		t.Fatal(err)
	}
	relay := wsrelay.NewManager(wsrelay.Options{})
	t.Cleanup(func() { _ = relay.Stop(context.Background()) })
	url := serveAs(t, g, adminIP, func(e *gin.Engine) {
		e.GET(relay.Path(), gin.WrapH(relay.Handler()))
	}) + relay.Path()
	conn := dialSocket(t, url)
	ping := func() bool {
		if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteJSON(wsrelay.Message{ID: "1", Type: wsrelay.MessageTypePing}); err != nil {
			return false
		}
		var msg wsrelay.Message
		return conn.ReadJSON(&msg) == nil && msg.Type == wsrelay.MessageTypePong
	}
	if !ping() {
		t.Fatal("the relay did not answer a ping")
	}
	if err := g.SetGrants(Grants{Devices: []DeviceGrant{{ID: laptopID, Proxy: true}}}); err != nil {
		t.Fatal(err)
	}
	if ping() {
		t.Fatal("the relay socket was still served after Admin was revoked")
	}
}

// A grant change between the guard's admission and the handler's upgrade
// closes the socket as soon as it is hijacked.
func TestGrantChangeBeforeUpgradeClosesSocket(t *testing.T) {
	ts := newFakeTailscaled(t, defaultPeers())
	g := newTestGuard(t, Config{TailscaleSocket: ts.socket})
	url := serveAs(t, g, otherNodeIP, func(e *gin.Engine) {
		e.GET("/v1/responses", func(c *gin.Context) {
			if err := g.SetGrants(Grants{Devices: []DeviceGrant{defaultDevices[0]}}); err != nil {
				t.Error(err)
			}
			echoSocket(c)
		})
	}) + "/v1/responses"
	if err := g.SetGrants(Grants{Devices: append(append([]DeviceGrant{}, defaultDevices...), DeviceGrant{ID: bobID, Proxy: true})}); err != nil {
		t.Fatal(err)
	}
	// The close usually lands before the 101 is written, so the dial fails.
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close() })
	if turn(t, conn) {
		t.Fatal("a socket upgraded after its grant was revoked was served")
	}
}
