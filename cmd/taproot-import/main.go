// Command taproot-import copies a CPA Usage Keeper database into taproot's
// store. It is idempotent: rerunning it over the same database adds nothing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/ingest"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	log "github.com/sirupsen/logrus"
)

func main() {
	if err := run(); err != nil {
		log.WithError(err).Error("taproot-import failed")
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config.yaml", "path to the server's config.yaml; taproot.yaml is read beside it or from TAPROOT_CONFIG")
	keeperPath := flag.String("usage-keeper", "", "path to a CPA Usage Keeper app.db (opened read-only)")
	flag.Parse()
	if *keeperPath == "" {
		return errors.New("-usage-keeper is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	tp, err := taproot.LoadConfig(taproot.ConfigPath(*configPath))
	if err != nil {
		return err
	}
	dataDir := tp.DataDir
	if dataDir == "" {
		dataDir = "taproot"
	}
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(filepath.Dir(*configPath), dataDir)
	}
	db, err := store.Open(ctx, tp.Store, dataDir)
	if err != nil {
		return fmt.Errorf("open taproot store: %w", err)
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			log.WithError(errClose).Warn("close taproot store")
		}
	}()

	start := time.Now()
	stats, err := ingest.ImportUsageKeeper(ctx, db, *keeperPath)
	if err != nil {
		return err
	}
	fmt.Printf("imported into %s in %s\n", db.Path(), time.Since(start).Round(time.Millisecond))
	fmt.Printf("  usage events:       %d read, %d new\n", stats.EventsRead, stats.EventsInserted)
	fmt.Printf("  quota observations: %d read, %d new\n", stats.ObservationsRead, stats.ObservationsInserted)
	fmt.Printf("  prices:             %d new\n", stats.PricesInserted)
	fmt.Printf("  accounts:           %d labelled\n", stats.AccountsUpdated)
	return nil
}
