package egress

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	coreauth "github.com/265866/taproot/sdk/cliproxy/auth"
)

// recordingProxy is a plain HTTP proxy that answers every request itself and
// records the absolute URLs it was asked for.
type recordingProxy struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newRecordingProxy(t *testing.T) *recordingProxy {
	t.Helper()
	p := &recordingProxy{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.URL.String())
		p.mu.Unlock()
		_, _ = io.WriteString(w, "via proxy")
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *recordingProxy) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func TestClientRoute(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	t.Cleanup(target.Close)

	tests := []struct {
		name       string
		credential string
		global     string
		want       string // "credential", "global", "direct", or "refused"
	}{
		{"no proxy goes direct", "", "", "direct"},
		{"the global proxy applies when the credential has none", "", "global", "global"},
		{"the credential proxy wins over the global one", "credential", "global", "credential"},
		{"a credential set to direct skips the global proxy", "direct", "global", "direct"},
		{"an invalid credential proxy is refused, not sent direct", "socks5://user:pa ss@[::1", "", "refused"},
		{"an invalid credential proxy does not fall back to the global one", "ftp://example.com:21", "global", "refused"},
		{"an invalid global proxy is refused", "", "not a url", "refused"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			credProxy, globalProxy := newRecordingProxy(t), newRecordingProxy(t)
			resolve := func(v string) string {
				switch v {
				case "credential":
					return credProxy.URL
				case "global":
					return globalProxy.URL
				}
				return v
			}
			p := &Proxy{}
			p.SetGlobal(func() string { return resolve(tc.global) })
			auth := &coreauth.Auth{ID: "a", Index: "idx-a", ProxyURL: resolve(tc.credential)}

			client, err := p.Client(auth)
			if tc.want == "refused" {
				if err == nil {
					t.Fatal("Client accepted an invalid proxy-url")
				}
				if strings.Contains(err.Error(), "pa ss") {
					t.Fatalf("error %q carries the proxy password", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Get(target.URL + "/usage")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			got := map[string]int{"credential": len(credProxy.requests()), "global": len(globalProxy.requests())}
			switch tc.want {
			case "direct":
				if string(body) != "direct" || got["credential"]+got["global"] != 0 {
					t.Fatalf("body %q, proxied %v; want a direct request", body, got)
				}
			default:
				if string(body) != "via proxy" || got[tc.want] != 1 || got["credential"]+got["global"] != 1 {
					t.Fatalf("body %q, proxied %v; want one request through the %s proxy", body, got, tc.want)
				}
			}
		})
	}
}

func TestClientFollowsGlobalChanges(t *testing.T) {
	proxy := newRecordingProxy(t)
	global := ""
	p := &Proxy{}
	p.SetGlobal(func() string { return global })
	global = proxy.URL
	client, err := p.Client(&coreauth.Auth{ID: "a"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get("http://example.com/usage")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := proxy.requests(); len(got) != 1 || got[0] != "http://example.com/usage" {
		t.Fatalf("proxy saw %v", got)
	}
}

func TestNilProxyGoesDirect(t *testing.T) {
	var p *Proxy
	client, err := p.Client(&coreauth.Auth{ID: "a"})
	if err != nil || client == nil || client.Transport != nil {
		t.Fatalf("client %+v, err %v; want the default transport", client, err)
	}
}
