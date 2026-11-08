package helps

import (
	"context"
	gotls "crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestClaudeCodeTransportKeepsConcurrentConnections sends two bursts of
// concurrent streams. Every stream of the first burst holds its own HTTP/1.1
// connection, so the second burst must find them all idle instead of dialing
// and handshaking again.
func TestClaudeCodeTransportKeepsConcurrentConnections(t *testing.T) {
	const streams = 8
	certificate := newResumptionTestCertificateAt(t, time.Now())
	roots := x509.NewCertPool()
	roots.AddCert(certificate.Leaf)

	// The handler holds each request until the whole burst has arrived, so the
	// transport needs one connection per stream.
	var arrived sync.WaitGroup
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			arrived.Done()
			arrived.Wait()
			_, _ = io.WriteString(w, "{}")
		}),
		TLSConfig: &gotls.Config{Certificates: []gotls.Certificate{certificate}, NextProtos: []string{"http/1.1"}},
	}
	listener, errListen := net.Listen("tcp", "127.0.0.1:0")
	if errListen != nil {
		t.Fatalf("listen: %v", errListen)
	}
	go func() { _ = server.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = server.Close() })

	var dials atomic.Int32
	dialer := contextDialerFunc(func(ctx context.Context, network, _ string) (net.Conn, error) {
		dials.Add(1)
		var d net.Dialer
		return d.DialContext(ctx, network, listener.Addr().String())
	})
	transport := newClaudeCodeRoundTripperForDialerAndRoots(dialer, roots)
	t.Cleanup(transport.CloseIdleConnections)

	burst := func() {
		arrived.Add(streams)
		var done sync.WaitGroup
		for range streams {
			done.Go(func() {
				req, errReq := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
				if errReq != nil {
					t.Errorf("new request: %v", errReq)
					arrived.Done()
					return
				}
				resp, errRT := transport.RoundTrip(req)
				if errRT != nil {
					t.Errorf("round trip: %v", errRT)
					return
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			})
		}
		done.Wait()
	}
	burst()
	if got := dials.Load(); got != streams {
		t.Fatalf("first burst dialed %d connections, want %d", got, streams)
	}
	burst()
	if got := dials.Load(); got != streams {
		t.Fatalf("second burst dialed %d new connections, want 0", got-streams)
	}
}
