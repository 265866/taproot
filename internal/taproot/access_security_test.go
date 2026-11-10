package taproot

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var adminHeaders = map[string]string{"Origin": "http://" + host, "Content-Type": "application/json", "X-Taproot-Request": "1"}

// An access.addresses entry is matched before whois, so one that covers
// tailnet addresses would hide those devices' identity: the admin would lose
// the dashboard and every device would get Proxy. Saving one is refused.
func TestTailnetAddressesAreRefused(t *testing.T) {
	f := newServer(t)
	for _, cidr := range []string{"100.64.0.0/10", adminIP, "100.0.0.0/8", "0.0.0.0/0", "fd7a:115c:a1e0::/64", "::/0"} {
		body := `{"access":{"addresses":[{"cidr":"` + cidr + `"}]}}`
		rec := f.do(req{remote: adminIP, method: http.MethodPut, path: "/taproot/api/v1/settings", headers: adminHeaders, body: body})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "covers Tailscale addresses") {
			t.Errorf("PUT addresses %s = %d %s, want 400", cidr, rec.Code, rec.Body)
		}
	}
	if rec := f.do(req{remote: adminIP, path: "/taproot/api/v1/me", headers: adminHeaders}); rec.Code != http.StatusOK {
		t.Errorf("admin /me after the refused saves = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(req{remote: newIP, method: http.MethodPost, path: "/v1/messages", body: `{}`}); guardCode(rec) != "no_proxy" {
		t.Errorf("ungranted device after the refused saves = %d %s, want no_proxy", rec.Code, rec.Body)
	}
}

// Turning off a device's Proxy on the Access page ends its open Responses
// websocket, which the guard admitted once when it connected.
func TestProxyRevocationEndsOpenWebsocket(t *testing.T) {
	f := newServer(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := net.SplitHostPort(r.RemoteAddr)
		r.RemoteAddr = net.JoinHostPort(workerIP, port)
		r.Host = host
		f.h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/responses", nil)
	if err != nil {
		t.Fatalf("dial as worker: %v (%v)", err, resp)
	}
	defer func() { _ = conn.Close() }()

	body := `{"access":{"devices":[{"id":"ndesktopCNTRL","name":"desktop","proxy":true,"admin":true}]}}`
	if rec := f.do(req{remote: adminIP, method: http.MethodPut, path: "/taproot/api/v1/settings", headers: adminHeaders, body: body}); rec.Code != http.StatusOK {
		t.Fatalf("revoke worker = %d %s", rec.Code, rec.Body)
	}
	// The deadline only bounds a broken test: without the fix the read waits
	// for a turn that never comes.
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err == nil {
		t.Fatalf("read %q from a revoked websocket", msg)
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		t.Fatalf("the revoked websocket stayed open: %v", err)
	}
}
