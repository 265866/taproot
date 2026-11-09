// Command demo writes a taproot data directory filled with synthetic
// accounts, usage, quota history, request logs, and backups, for screenshots
// and local dashboard work. It writes through taproot's own modules (ingest,
// reqlog, backup) and never contacts a provider. See CONTRIBUTING.md, "Demo
// data and screenshots".
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/backup"
	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/265866/taproot/internal/taproot/store"
	log "github.com/sirupsen/logrus"
)

func main() {
	dir := flag.String("dir", "", "empty or missing directory to write the demo config and data into (required)")
	port := flag.Int("port", 8399, "loopback port the demo server listens on")
	tailscaled := flag.String("tailscaled", "", "instead of seeding, serve a stand-in tailscaled on this unix socket until stopped")
	flag.Parse()
	log.SetLevel(log.WarnLevel)
	if *tailscaled != "" {
		if err := runTailscaled(*tailscaled); err != nil {
			fmt.Fprintln(os.Stderr, "taproot demo:", err)
			os.Exit(1)
		}
		return
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./internal/taproot/demo -dir <empty directory> [-port 8399]")
		fmt.Fprintln(os.Stderr, "       go run ./internal/taproot/demo -tailscaled <socket>")
		os.Exit(2)
	}
	summary, err := seed(context.Background(), *dir, *port, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "taproot demo:", err)
		os.Exit(1)
	}
	fmt.Println(summary)
}

// seed writes the demo into dir with history ending at start.
func seed(ctx context.Context, dir string, port int, start time.Time) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := requireEmptyDir(dir); err != nil {
		return "", err
	}
	authDir := filepath.Join(dir, "auths")
	dataDir := filepath.Join(dir, "taproot")
	accounts := demoAccounts()
	if err := writeConfig(dir, authDir, port); err != nil {
		return "", err
	}
	if err := writeAuthFiles(authDir, accounts, start); err != nil {
		return "", err
	}
	requests := generate(start, accounts)

	db, err := store.Open(ctx, store.Config{}, dataDir)
	if err != nil {
		return "", fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Error("taproot demo: close store")
		}
	}()
	// Request logs first: their durations run to the moment they are written.
	logged, err := writeRequestLogs(ctx, db, dataDir, requests)
	if err != nil {
		return "", err
	}
	if err := writeUsage(ctx, db, requests); err != nil {
		return "", err
	}
	backups, err := backup.New(backup.Config{DailyAt: "off"}, db, backup.Paths{
		ConfigFile:  filepath.Join(dir, "config.yaml"),
		TaprootFile: filepath.Join(dir, "taproot.yaml"),
		AuthDir:     authDir,
		DataDir:     dataDir,
	})
	if err != nil {
		return "", fmt.Errorf("backups: %w", err)
	}
	for _, reason := range []backup.Reason{backup.ReasonScheduled, backup.ReasonManual} {
		if _, err := backups.Create(ctx, reason); err != nil {
			return "", fmt.Errorf("create %s backup: %w", reason, err)
		}
	}
	return fmt.Sprintf("Wrote %d usage events, %d request logs, and 2 backups for %d accounts to %s.\nStart the server with internal/taproot/demo/run.sh, or see CONTRIBUTING.md.",
		len(requests), logged, len(accounts), dir), nil
}

func requireEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty; the demo writes into an empty directory so a rerun never mixes data", dir)
	}
	return nil
}

// deviceTable is the demo's stand-in for the access guard: it names the
// device behind each generated request, keyed by trace ID as the guard is.
type deviceTable map[string]access.Device

func (d deviceTable) RequestDevice(traceID string) (access.Device, bool) {
	dev, ok := d[traceID]
	return dev, ok
}

func (d deviceTable) DeviceName(_, stored string) string { return stored }

// writeUsage hands every record to ingest's usage sink, as the proxy does,
// and waits until its writer has flushed them all. The writer is left
// running: cancelling it while a COMMIT is in flight makes it report a failed
// flush and retry the batch, and with its queue empty it never touches the
// database again.
func writeUsage(ctx context.Context, db *store.DB, requests []request) error {
	sink, err := ingest.New(ingest.Config{PollInterval: "0"}, db)
	if err != nil {
		return fmt.Errorf("ingest: %w", err)
	}
	table := deviceTable{}
	for _, r := range requests {
		table[r.TraceID] = r.device.Device
	}
	sink.AttachDevices(table)
	sink.Start(ctx)
	for _, r := range requests {
		sink.HandleUsage(ctx, r.Record)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events`).Scan(&n); err != nil {
			return fmt.Errorf("count usage events: %w", err)
		}
		if n == len(requests) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ingest stored %d of %d usage events", n, len(requests))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
