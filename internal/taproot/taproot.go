// Package taproot wires the fork's additions into the upstream server through
// server options, so upstream files stay untouched apart from one call in
// cmd/server/main.go.
package taproot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/265866/taproot/internal/api"
	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/alerts"
	"github.com/265866/taproot/internal/taproot/backup"
	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/265866/taproot/internal/taproot/reqlog"
	"github.com/265866/taproot/internal/taproot/settings"
	"github.com/265866/taproot/internal/taproot/store"
	"github.com/265866/taproot/internal/taproot/web"
	"github.com/265866/taproot/internal/util"
	"github.com/265866/taproot/sdk/api/handlers"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// APIPrefix is where taproot's own endpoints live.
const APIPrefix = "/taproot/api/v1"

// Config is taproot.yaml. It sits beside config.yaml, which upstream owns and
// loads leniently: a taproot key there would be ignored without a word.
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

// LoadConfig reads taproot.yaml. A missing or empty file yields defaults. Unknown
// keys are an error, so a misspelled access setting cannot fall back silently.
func LoadConfig(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	// A removed access key gets a message naming its replacement. The strict
	// decode below reports any other problem, so a parse error here is ignored.
	var keys struct {
		Access map[string]yaml.Node `yaml:"access"`
	}
	if yaml.Unmarshal(raw, &keys) == nil {
		for _, key := range slices.Sorted(maps.Keys(keys.Access)) {
			if err := access.RemovedKey(key); err != nil {
				return cfg, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
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

// CheckServerMode refuses the upstream modes that would run without Setup, and
// so without the access guard: Home mode (-home-jwt or HOME_JWT), in every
// mode, because main.go contacts the Home control plane before anything else,
// and the TUI's embedded server (--tui --standalone). main.go calls it as soon
// as the flags and HOME_JWT are read. The plain TUI client starts no server
// and is allowed.
func CheckServerMode(homeMode, tuiMode, standalone bool) error {
	if homeMode {
		return errors.New("taproot does not support Home mode (-home-jwt or HOME_JWT): it fetches config and plugins from a Home control plane and starts the server without taproot's access guard")
	}
	if tuiMode && standalone {
		return errors.New("taproot does not support --tui --standalone: its embedded server would start without taproot's access guard; run the server, then connect with --tui")
	}
	return nil
}

// Setup opens taproot's modules and returns the server options that mount them.
// It must run before the service is built, because the access guard prepares
// the management secret that the management handler reads at construction.
func Setup(ctx context.Context, cfg *config.Config, upstreamConfigPath string) ([]api.ServerOption, error) {
	opts, _, err := setup(ctx, cfg, upstreamConfigPath)
	return opts, err
}

// setup is Setup that also returns stop, which cancels the background work
// and waits for the request-log writer and compactor. The writer saves queued
// records to new files as it stops, so delete a data dir only after stop.
func setup(ctx context.Context, cfg *config.Config, upstreamConfigPath string) (_ []api.ServerOption, stop func(), _ error) {
	tpPath := ConfigPath(upstreamConfigPath)
	if _, err := os.Stat(tpPath); errors.Is(err, os.ErrNotExist) {
		if abs, errAbs := filepath.Abs(tpPath); errAbs == nil {
			tpPath = abs
		}
		return nil, nil, fmt.Errorf("taproot.yaml not found at %s: create it there (the taproot repository's examples/taproot/taproot.yaml is a starting point), or set TAPROOT_CONFIG to its path", tpPath)
	}
	tp, err := LoadConfig(tpPath)
	if err != nil {
		return nil, nil, err
	}
	dataDir := DataDir(tp, upstreamConfigPath)

	guard, err := access.New(tp.Access)
	if err != nil {
		return nil, nil, fmt.Errorf("taproot access (%s): %w", tpPath, err)
	}
	if err := guard.CheckTailscaled(ctx); err != nil {
		return nil, nil, fmt.Errorf("taproot access (%s): %w", tpPath, err)
	}
	api.SetRESPConnectionFilter(access.RESPFilter)
	if err := backup.ApplyPendingRestore(dataDir); err != nil {
		return nil, nil, fmt.Errorf("taproot pending restore: %w", err)
	}
	db, err := store.Open(ctx, tp.Store, dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("taproot store: %w", err)
	}
	usage, err := ingest.New(tp.Ingest, db)
	if err != nil {
		return nil, nil, fmt.Errorf("taproot ingest: %w", err)
	}
	usage.AttachDevices(guard)
	plugins := &pluginCallers{}
	usage.AttachPlugins(plugins)
	accounts := &noAccounts{}
	logs, err := reqlog.New(tp.RequestLogs, db, dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("taproot request logs: %w", err)
	}
	authDir, err := util.ResolveAuthDir(cfg.AuthDir)
	if err != nil {
		return nil, nil, fmt.Errorf("taproot auth dir: %w", err)
	}
	backups, err := backup.New(tp.Backups, db, backup.Paths{
		ConfigFile:  upstreamConfigPath,
		TaprootFile: tpPath,
		AuthDir:     authDir,
		DataDir:     dataDir,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("taproot backups: %w", err)
	}
	notify, err := alerts.New(tp.Alerts, db, usage)
	if err != nil {
		return nil, nil, fmt.Errorf("taproot alerts: %w", err)
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
		return nil, nil, fmt.Errorf("taproot settings: %w", err)
	}
	code, err := guard.StartClaim()
	if err != nil {
		return nil, nil, fmt.Errorf("taproot access: %w", err)
	}
	if code != "" {
		log.Warnf("taproot access: setup code %s. No device has Admin yet: open %s from the device that should become the first admin, and enter this code. It works once, and taproot makes a new one each time it starts until a device has Admin.",
			access.FormatClaimCode(code), dashboardURL(cfg, guard.ServerDNSName(ctx)))
	}

	ctx, cancel := context.WithCancel(ctx)
	stop = func() {
		cancel()
		logs.Close()
	}
	usage.Start(ctx)
	logs.Start(ctx)
	backups.Start(ctx)
	notify.Start(ctx)
	editable.Start(ctx)

	opts := []api.ServerOption{
		// The guard must run first so the dashboard is access-checked too.
		api.WithMiddleware(guard.Middleware(), web.Middleware(), accounts.middleware()),
		api.WithRouterConfigurator(func(engine *gin.Engine, h *handlers.BaseAPIHandler, _ *config.Config) {
			if h != nil {
				usage.AttachAuthManager(h.AuthManager)
				notify.AttachAuthManager(h.AuthManager)
				plugins.attach(h)
				accounts.attach(h.AuthManager)
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
	return opts, stop, nil
}

// dashboardURL is where a tailnet device opens the dashboard: the server's
// MagicDNS name and the port taproot listens on. A container that publishes
// another port is reached on that port instead.
func dashboardURL(cfg *config.Config, dnsName string) string {
	scheme, host := "http", dnsName
	if cfg.TLS.Enable {
		scheme = "https"
	}
	if host == "" {
		host = "<this server's tailnet name>"
	}
	port := cfg.Port
	if port == 0 {
		port = 8317
	}
	return fmt.Sprintf("%s://%s:%d/", scheme, host, port)
}
