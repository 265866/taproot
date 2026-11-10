// Package taproot wires the fork's additions into the upstream server through
// server options. The few upstream edits it needs, most of them calls in
// cmd/server/main.go, are listed under "Upstream boundary" in DESIGN.md.
package taproot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/265866/taproot/internal/api"
	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/alerts"
	"github.com/265866/taproot/internal/taproot/backup"
	"github.com/265866/taproot/internal/taproot/egress"
	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/265866/taproot/internal/taproot/reqlog"
	"github.com/265866/taproot/internal/taproot/routing"
	"github.com/265866/taproot/internal/taproot/settings"
	"github.com/265866/taproot/internal/taproot/status"
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
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	return parseConfig(raw, path)
}

// parseConfig decodes taproot.yaml's content strictly; path names it in errors.
func parseConfig(raw []byte, path string) (Config, error) {
	var cfg Config
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
	// Only the first document is read, so a second one would be ignored without a word.
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return cfg, nil
		}
		if err != nil {
			return cfg, fmt.Errorf("parse %s: %w", path, err)
		}
		if !emptyDocument(&doc) {
			return cfg, fmt.Errorf("parse %s: it holds more than one YAML document (a line with only ---); merge them into one", path)
		}
	}
}

func emptyDocument(doc *yaml.Node) bool {
	return len(doc.Content) == 0 || (len(doc.Content) == 1 && doc.Content[0].Tag == "!!null")
}

// restoreCheck reads a backup's taproot.yaml the way setup will on the next
// start, and refuses one that setup would refuse before the server serves
// the dashboard again.
func restoreCheck(tpPath, upstreamConfigPath string) func([]byte) (backup.NextStart, error) {
	return func(raw []byte) (backup.NextStart, error) {
		tp, err := parseConfig(raw, tpPath)
		if err != nil {
			return backup.NextStart{}, err
		}
		if err := access.CheckGrants(access.Grants{Devices: tp.Access.Devices, Addresses: tp.Access.Addresses}); err != nil {
			return backup.NextStart{}, err
		}
		next := backup.NextStart{AccessEnabled: tp.Access.Enabled == nil || *tp.Access.Enabled}
		next.DataDir = DataDir(tp, upstreamConfigPath)
		next.DBPath = tp.Store.File(next.DataDir)
		for _, d := range tp.Access.Devices {
			if d.Admin {
				next.Admins = append(next.Admins, strings.TrimSpace(d.ID))
			}
		}
		return next, nil
	}
}

// ConfigPath returns taproot.yaml's location: TAPROOT_CONFIG, otherwise beside config.yaml.
func ConfigPath(upstreamConfigPath string) string {
	return taprootConfigPath(upstreamConfigPath, os.Getenv)
}

func taprootConfigPath(upstreamConfigPath string, getenv func(string) string) string {
	if p := getenv("TAPROOT_CONFIG"); p != "" {
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

// RestartExitCode is the exit status after a restore asks taproot to restart
// (EX_TEMPFAIL). It is not 0 so that every restart policy, systemd's
// Restart=on-failure and Docker's on-failure included, starts taproot again.
const RestartExitCode = 75

// Setup opens taproot's modules and returns the server options that mount them,
// and shutdown, which main calls once the server has stopped. shutdown writes the
// request-log records and usage events still queued, then exits with
// RestartExitCode when a restore asked for a restart.
// Setup must run before the service is built, because the access guard prepares
// the management secret that the management handler reads at construction.
func Setup(ctx context.Context, cfg *config.Config, upstreamConfigPath string) (_ []api.ServerOption, shutdown func(), _ error) {
	opts, m, err := setup(ctx, cfg, upstreamConfigPath)
	if err != nil {
		return nil, nil, err
	}
	return opts, func() {
		m.stop()
		if m.backups.RestartRequested() {
			log.Infof("taproot: exiting with status %d so the supervisor restarts taproot to finish the restore", RestartExitCode)
			os.Exit(RestartExitCode)
		}
	}, nil
}

// modules is what shutdown needs of a running setup.
type modules struct {
	cancel  context.CancelFunc
	logs    *reqlog.Logs
	usage   *ingest.Ingest
	backups *backup.Backups
}

// stop cancels the background work and waits for the request-log writer and
// compactor and for ingest's writer, which save the records still queued as
// they stop. Delete a data dir only after stop.
func (m *modules) stop() {
	m.cancel()
	m.logs.Close()
	m.usage.Wait()
}

// setup is Setup without the restart exit, for tests.
func setup(ctx context.Context, cfg *config.Config, upstreamConfigPath string) ([]api.ServerOption, *modules, error) {
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
	// The router configurator follows config reloads through the handler.
	providerProxy := &egress.Proxy{}
	providerProxy.SetGlobal(func() string { return cfg.ProxyURL })
	usage.UseProxy(providerProxy)
	usage.OnQuota(func(obs []ingest.QuotaObservation) { routing.Stored.Observe(routingWindows(obs)) })
	if latest, errLatest := usage.LatestQuota(ctx); errLatest != nil {
		log.WithError(errLatest).Warn("taproot routing: load latest quota")
	} else {
		routing.Stored.Observe(routingWindows(latest))
	}
	usage.AttachDevices(guard)
	usage.AttachProviderGuess(modelProvider)
	plugins := &pluginCallers{}
	usage.AttachPlugins(plugins)
	accounts := &noAccounts{}
	logs, err := reqlog.New(tp.RequestLogs, db, dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("taproot request logs: %w", err)
	}
	usage.AttachRequestLogs(logs)
	authDir, err := util.ResolveAuthDir(cfg.AuthDir)
	if err != nil {
		return nil, nil, fmt.Errorf("taproot auth dir: %w", err)
	}
	backups, err := backup.New(tp.Backups, db, backup.Paths{
		ConfigFile:   upstreamConfigPath,
		TaprootFile:  tpPath,
		AuthDir:      authDir,
		DataDir:      dataDir,
		CheckTaproot: restoreCheck(tpPath, upstreamConfigPath),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("taproot backups: %w", err)
	}
	notify, err := alerts.New(tp.Alerts, db, usage)
	if err != nil {
		return nil, nil, fmt.Errorf("taproot alerts: %w", err)
	}
	notify.UseProxy(providerProxy)
	guard.AttachRequests(access.NewRequests(db, func(n access.RequestNotice) {
		notify.AccessRequested(alerts.AccessRequest{NodeID: n.NodeID, Name: n.Name, Login: n.Login, Client: n.Client, At: n.At})
	}))
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
	announce := func(code string) {
		log.Warnf("taproot access: setup code %s. No device has Admin: open %s from the device that should become the first admin, and enter this code. It works once. While no device has Admin, taproot makes a new one each time it starts, and after the last device with Admin is removed.",
			access.FormatClaimCode(code), dashboardURL(cfg, guard.ServerDNSName(ctx)))
	}
	state, err := status.New(status.Deps{Usage: usage, Requests: guard, Backups: backups, Alerts: notify})
	if err != nil {
		return nil, nil, fmt.Errorf("taproot status: %w", err)
	}
	if err := guard.StartClaim(ctx, announce); err != nil {
		return nil, nil, fmt.Errorf("taproot access: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	running := &modules{cancel: cancel, logs: logs, usage: usage, backups: backups}
	usage.Start(ctx)
	logs.Start(ctx)
	backups.Start(ctx)
	notify.Start(ctx)
	editable.Start(ctx)
	state.Start(ctx)
	guard.MonitorTailscaled(ctx, func(n access.TailscaledNotice) {
		notify.Tailscaled(alerts.TailscaledOutage{Socket: n.Socket, Down: n.Down, Since: n.Since, At: n.At, Err: n.Err, StaleSocket: n.StaleSocket})
		state.Tailscaled(n)
	})

	opts := []api.ServerOption{
		// The guard must run first so the dashboard is access-checked too, and
		// refused requests never count as client requests.
		api.WithMiddleware(guard.Middleware(), web.Middleware(), apiNotFound(), clientRequests(usage, accounts), accounts.middleware()),
		api.WithRouterConfigurator(func(engine *gin.Engine, h *handlers.BaseAPIHandler, _ *config.Config) {
			if h != nil {
				providerProxy.SetGlobal(func() string {
					if c := h.CurrentConfig(); c != nil {
						return c.ProxyURL
					}
					return ""
				})
				usage.AttachAuthManager(h.AuthManager)
				notify.AttachAuthManager(h.AuthManager)
				state.AttachAuthManager(h.AuthManager)
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
			state.Routes(group)
		}),
	}
	if factory := logs.LoggerFactory(); factory != nil {
		opts = append(opts, api.WithRequestLoggerFactory(factory))
	}
	return opts, running, nil
}

// apiNotFound answers a request under APIPrefix that matches no route, or
// matches one only under another method, with taproot's JSON error. gin runs
// global middleware before its no-route handler, which upstream sets to an
// empty 404, and leaves FullPath empty for such a request.
func apiNotFound() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if c.FullPath() != "" || (p != APIPrefix && !strings.HasPrefix(p, APIPrefix+"/")) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": gin.H{"code": "not_found", "message": "no endpoint " + c.Request.Method + " " + p}})
	}
}

func routingWindows(obs []ingest.QuotaObservation) []routing.Window {
	out := make([]routing.Window, len(obs))
	for k, o := range obs {
		out[k] = routing.Window{
			AuthIndex: o.AuthIndex, Provider: o.Provider, Name: o.Window, Utilization: o.Utilization, Status: o.Status,
			ResetsAt: o.ResetsAt, PeriodSeconds: o.PeriodSeconds, ObservedAt: o.ObservedAt,
		}
	}
	return out
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
