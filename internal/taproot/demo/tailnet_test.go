package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/taproot"
	"github.com/265866/taproot/internal/taproot/access"
	"github.com/gin-gonic/gin"
)

// The demo's taproot.yaml loads strictly, and the real guard reading the
// fake tailnet admits each device as its grant says.
func TestDemoTailnet(t *testing.T) {
	// Short path: unix socket paths are limited to about 108 bytes.
	dir, err := os.MkdirTemp("", "demo")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := writeConfig(dir, filepath.Join(dir, "auths"), 8399); err != nil {
		t.Fatal(err)
	}
	cfg, err := taproot.LoadConfig(filepath.Join(dir, "taproot.yaml"))
	if err != nil {
		t.Fatalf("demo taproot.yaml: %v", err)
	}
	t.Setenv("MANAGEMENT_PASSWORD", "demo")
	g, err := access.New(cfg.Access)
	if err != nil {
		t.Fatal(err)
	}
	// The server refuses to start until the fake tailnet serves the socket.
	if err := g.CheckTailscaled(context.Background()); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("CheckTailscaled before serveTailnet = %v, want the missing-socket error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := listenTailnet(ctx, tailnetSocket(dir))
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- serveTailnet(ctx, ln, testStart) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serveTailnet: %v", err)
		}
	})

	if err := g.CheckTailscaled(ctx); err != nil {
		t.Fatalf("CheckTailscaled with serveTailnet = %v", err)
	}
	tn, err := g.ReadTailnet(ctx)
	if err != nil {
		t.Fatalf("ReadTailnet: %v", err)
	}
	if tn.Self.ID != serverNode.id || len(tn.Peers) != len(tailnetDevices) {
		t.Fatalf("tailnet = self %s, %d peers", tn.Self.ID, len(tn.Peers))
	}

	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(g.Middleware())
	e.GET("/v1/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	e.GET("/v8/management/config", func(c *gin.Context) { c.Status(http.StatusOK) })
	code := func(ip, path string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr, r.Host = ip+":40000", "server:8399"
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		return w.Code
	}
	status := map[bool]int{true: http.StatusOK, false: http.StatusForbidden}
	for _, d := range tailnetDevices {
		if got := code(d.ip, "/v1/models"); got != status[d.proxy] {
			t.Errorf("%s proxy = %d, want %d", d.name, got, status[d.proxy])
		}
		if got := code(d.ip, "/v8/management/config"); got != status[d.admin] {
			t.Errorf("%s admin = %d, want %d", d.name, got, status[d.admin])
		}
	}
	if code(serverNode.ip, "/v1/models") != http.StatusOK || code(serverNode.ip, "/v8/management/config") != http.StatusForbidden {
		t.Error("the server's own node is not proxy-only")
	}
	if code("192.0.2.20", "/v1/models") != http.StatusOK {
		t.Error("the demo address does not reach the proxy")
	}
}
