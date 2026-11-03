package api

import (
	"bufio"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/redisqueue"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/access"
)

// remoteAddrListener reports a chosen remote address for every accepted
// connection, the way the multiplexer would see a tailnet peer.
type remoteAddrListener struct {
	net.Listener
	remote net.Addr
}

func (l remoteAddrListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.remote == nil {
		return conn, nil
	}
	return remoteAddrConn{Conn: conn, remote: l.remote}, nil
}

type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c remoteAddrConn) RemoteAddr() net.Addr { return c.remote }

// Taproot installs access.RESPFilter with SetRESPConnectionFilter. Security recheck F2:
// a tailnet peer authenticated to the RESP usage queue with the management
// secret; with the filter it is closed before any reply.
func TestRESPConnectionFilterThroughMultiplexer(t *testing.T) {
	const managementPassword = "test-management-password"
	t.Setenv("MANAGEMENT_PASSWORD", managementPassword)
	redisqueue.SetEnabled(false)
	t.Cleanup(func() { redisqueue.SetEnabled(false) })
	SetRESPConnectionFilter(access.RESPFilter)
	t.Cleanup(func() { SetRESPConnectionFilter(nil) })

	server := newTestServer(t)
	for _, tc := range []struct {
		name   string
		remote net.Addr
		admit  bool
	}{
		{"tailnet peer", &net.TCPAddr{IP: net.ParseIP("100.64.0.9"), Port: 40000}, false},
		{"server's own tailnet address", &net.TCPAddr{IP: net.ParseIP("100.64.0.1"), Port: 40000}, false},
		{"LAN address", &net.TCPAddr{IP: net.ParseIP("192.168.0.88"), Port: 40000}, false},
		{"loopback", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- server.acceptMuxConnections(remoteAddrListener{Listener: ln, remote: tc.remote}, nil) }()
			t.Cleanup(func() {
				_ = ln.Close()
				select {
				case err := <-done:
					if err != nil && !errors.Is(err, net.ErrClosed) {
						t.Errorf("accept loop: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Error("accept loop did not stop")
				}
			})

			conn, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			if err := writeTestRESPCommand(conn, "AUTH", managementPassword); err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			if tc.admit {
				if msg, err := readTestRESPSimpleString(reader); err != nil || msg != "OK" {
					t.Fatalf("loopback AUTH = %q, %v; want OK", msg, err)
				}
				return
			}
			if b, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
				t.Fatalf("filtered connection read %q, %v; want EOF before any reply", b, err)
			}
		})
	}
}
