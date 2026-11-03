// Package taproot wires the fork's additions into the upstream server through
// server options, so upstream files stay untouched apart from one call in
// cmd/server/main.go.
package taproot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/access"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/alerts"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/backup"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/ingest"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/reqlog"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/settings"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/web"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"gopkg.in/yaml.v3"
)

// APIPrefix is where taproot's own endpoints live.
const APIPrefix = "/taproot/api/v1"

// Config is taproot.yaml. It sits beside config.yaml because upstream parses
// config.yaml strictly and rejects unknown sections.
type Config struct {
	// DataDir holds the database, request logs, and backups. Relative paths
	// resolve against the config file's directory.
	DataDir     string        `yaml:"data-dir"`
	Access      access.Config `yaml:"access"`
	Store       store.Config  `yaml:"store"`
	Ingest      ingest.Config `yaml:"ingest"`
	RequestLogs reqlog.Config `yaml:"request-logs"`
	Backups     backup.Config `yaml:"backups"`
	Alerts      alerts.Config `yaml:"alerts"`
}

// LoadConfig reads taproot.yaml. A missing file yields defaults.
func LoadConfig(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// ConfigPath returns taproot.yaml's location: TAPROOT_CONFIG, otherwise beside config.yaml.
func ConfigPath(upstreamConfigPath string) string {
	if p := os.Getenv("TAPROOT_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(upstreamConfigPath), "taproot.yaml")
}

// DataDir resolves where taproot keeps its database, request logs, and backups.
func DataDir(tp Config, upstreamConfigPath string) string {
	dataDir := tp.DataDir
	if dataDir == "" {
		dataDir = "taproot"
	}
	if !filepath.IsAbs(dataDir) {
		dataDir = filepath.Join(filepath.Dir(upstreamConfigPath), dataDir)
	}
	return dataDir
}

// Setup opens taproot's modules and returns the server options that mount them.
// It must run before the service is built, because the access guard prepares
// the management secret that the management handler reads at construction.
func Setup(ctx context.Context, cfg *config.Config, upstreamConfigPath string) ([]api.ServerOption, error) {
	tpPath := ConfigPath(upstreamConfigPath)
	tp, err := LoadConfig(tpPath)
	if err != nil {
		return nil, err
	}
	dataDir := DataDir(tp, upstreamConfigPath)

	guard, err := access.New(tp.Access)
	if err != nil {
		return nil, fmt.Errorf("taproot access: %w", err)
	}
	api.SetRESPConnectionFilter(access.RESPFilter)
	if err := backup.ApplyPendingRestore(dataDir); err != nil {
		return nil, fmt.Errorf("taproot pending restore: %w", err)
	}
	db, err := store.Open(ctx, tp.Store, dataDir)
	if err != nil {
		return nil, fmt.Errorf("taproot store: %w", err)
	}
	usage, err := ingest.New(tp.Ingest, db)
	if err != nil {
		return nil, fmt.Errorf("taproot ingest: %w", err)
	}
	usage.AttachDevices(guard)
	logs, err := reqlog.New(tp.RequestLogs, db, dataDir)
	if err != nil {
		return nil, fmt.Errorf("taproot request logs: %w", err)
	}
	authDir, err := util.ResolveAuthDir(cfg.AuthDir)
	if err != nil {
		return nil, fmt.Errorf("taproot auth dir: %w", err)
	}
	backups, err := backup.New(tp.Backups, db, backup.Paths{
		ConfigFile:  upstreamConfigPath,
		TaprootFile: tpPath,
		AuthDir:     authDir,
		DataDir:     dataDir,
	})
	if err != nil {
		return nil, fmt.Errorf("taproot backups: %w", err)
	}
	notify, err := alerts.New(tp.Alerts, db, usage)
	if err != nil {
		return nil, fmt.Errorf("taproot alerts: %w", err)
	}
	editable, err := settings.New(settings.Deps{
		Access:        guard,
		AccessEnabled: tp.Access.Enabled == nil || *tp.Access.Enabled,
		Logs:          logs,
		Backups:       backups,
		Alerts:        notify,
		PollInterval:  tp.Ingest.PollInterval,
		ConfigPath:    tpPath,
		DataDir:       dataDir,
	})
	if err != nil {
		return nil, fmt.Errorf("taproot settings: %w", err)
	}

	usage.Start(ctx)
	logs.Start(ctx)
	backups.Start(ctx)
	notify.Start(ctx)

	opts := []api.ServerOption{
		// The guard must run first so the dashboard is access-checked too.
		api.WithMiddleware(guard.Middleware(), web.Middleware()),
		api.WithRouterConfigurator(func(engine *gin.Engine, h *handlers.BaseAPIHandler, _ *config.Config) {
			if h != nil {
				usage.AttachAuthManager(h.AuthManager)
				notify.AttachAuthManager(h.AuthManager)
			}
			group := engine.Group(APIPrefix)
			guard.Routes(group)
			usage.Routes(group)
			logs.Routes(group)
			backups.Routes(group)
			editable.Routes(group)
			notify.Routes(group)
		}),
	}
	if factory := logs.LoggerFactory(); factory != nil {
		opts = append(opts, api.WithRequestLoggerFactory(factory))
	}
	return opts, nil
}
