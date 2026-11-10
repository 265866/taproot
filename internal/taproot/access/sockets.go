package access

import (
	"bufio"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// openSockets holds the hijacked connections (websockets) of callers whose
// grant can be taken away. The guard admits a connection once, and nothing
// else ends one: upstream's per-turn re-authentication asks an access manager
// that taproot leaves empty, and established connections get no timeouts. So
// when a grant change removes the permission a connection was admitted with,
// SetGrants closes it.
type openSockets struct {
	mu    sync.Mutex
	conns map[*openSocket]struct{}
}

type openSocket struct {
	conn net.Conn
	id   Identity
	// addr is the caller's address, for an access.addresses caller.
	addr netip.Addr
	// admin is true on the admin surface, where the connection needs Admin
	// rather than Proxy.
	admin bool
}

// allowed reports whether set still grants what the connection needs.
func (s *openSocket) allowed(set *grantSet) bool {
	if s.id.Via == ViaCIDR {
		for _, prefix := range set.prefixes {
			if prefix.Contains(s.addr) {
				return true
			}
		}
		return false
	}
	grant := set.byID[s.id.NodeID]
	if s.admin {
		return grant.Admin
	}
	return grant.Proxy
}

// add tracks a connection and closes it at once when set, the grants current
// when it was hijacked, no longer allows it: a grant change between admission
// and the upgrade would otherwise miss it.
func (o *openSockets) add(s *openSocket, set func() *grantSet) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !s.allowed(set()) {
		closeSocket(s)
		return
	}
	if o.conns == nil {
		o.conns = map[*openSocket]struct{}{}
	}
	o.conns[s] = struct{}{}
}

func (o *openSockets) remove(s *openSocket) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.conns, s)
}

// revoke closes every connection set no longer allows.
func (o *openSockets) revoke(set *grantSet) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for s := range o.conns {
		if !s.allowed(set) {
			closeSocket(s)
			delete(o.conns, s)
		}
	}
}

func closeSocket(s *openSocket) {
	need := "Proxy"
	if s.admin {
		need = "Admin"
	}
	if s.id.Via == ViaCIDR {
		log.Infof("taproot access: closed a connection from %s, which access.addresses no longer lists", s.addr)
	} else {
		log.Infof("taproot access: closed a connection from device %s (%s), which lost %s", quoteHeader(s.id.Node), s.id.NodeID, need)
	}
	if err := s.conn.Close(); err != nil {
		log.Debugf("taproot access: close a revoked connection: %v", err)
	}
}

// trackSocket tracks a hijack of this request's connection until the
// connection is closed, for a caller whose grant can be taken away. A handler
// such as upstream's /v1/ws relay returns right after the upgrade and serves
// the connection from another goroutine, so the request's end does not end
// the connection.
func (g *Guard) trackSocket(c *gin.Context, id Identity, surf surface) {
	if !isUpgrade(c.Request) || (!id.device && id.Via != ViaCIDR) {
		return
	}
	s := &openSocket{id: id, admin: surf == surfaceAdmin}
	if id.Via == ViaCIDR {
		addr, err := netip.ParseAddr(id.Node)
		if err != nil {
			return
		}
		s.addr = addr
	}
	c.Writer = &hijackWriter{ResponseWriter: c.Writer, hijacked: func(conn net.Conn) net.Conn {
		s.conn = conn
		g.sockets.add(s, g.grants.Load)
		return &trackedConn{Conn: conn, untrack: func() { g.sockets.remove(s) }}
	}}
}

// trackedConn is the connection the handler gets. Revocation closes the
// underlying connection directly, because it holds the lock that untrack
// takes.
type trackedConn struct {
	net.Conn
	once    sync.Once
	untrack func()
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.untrack)
	return err
}

func isUpgrade(r *http.Request) bool {
	return r.Header.Get("Upgrade") != "" && strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// hijackWriter hands the connection a handler takes over to hijacked, and the
// handler gets the connection hijacked returns.
type hijackWriter struct {
	gin.ResponseWriter
	hijacked func(net.Conn) net.Conn
}

func (w *hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.Hijack()
	if err == nil {
		conn = w.hijacked(conn)
	}
	return conn, rw, err
}
