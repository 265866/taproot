package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

// runTailscaled serves the stand-in tailscaled on socket until SIGINT or SIGTERM.
func runTailscaled(socket string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, err := listenTailscaled(socket)
	if err != nil {
		return err
	}
	return serveTailscaled(ctx, ln)
}

// listenTailscaled opens the stand-in tailscaled's unix socket.
func listenTailscaled(socket string) (net.Listener, error) {
	if err := os.Remove(socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remove old socket: %w", err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socket, err)
	}
	return ln, nil
}

// serveTailscaled stands in for tailscaled on ln until ctx ends, so the demo
// server passes taproot's startup check for the socket. Every LocalAPI call
// gets 503, as from a tailscaled that is not running, so the guard admits
// loopback only and the demo never reads a real tailnet.
func serveTailscaled(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "taproot demo: no tailnet", http.StatusServiceUnavailable)
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		if errClose := srv.Close(); errClose != nil {
			log.WithError(errClose).Warn("taproot demo: close stand-in tailscaled")
		}
	}()
	if errServe := srv.Serve(ln); !errors.Is(errServe, http.ErrServerClosed) {
		return fmt.Errorf("serve stand-in tailscaled: %w", errServe)
	}
	return nil
}
