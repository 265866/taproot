// Package settings serves the editable subset of taproot.yaml: the access
// device and address lists, request log retention, the backup schedule, the
// ingest poll interval, and the alerts webhook. A change is applied to the
// running modules first and written to taproot.yaml only after every live
// setter accepted it.
package settings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/265866/taproot/internal/buildinfo"
	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/alerts"
	"github.com/265866/taproot/internal/taproot/reqlog"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const defaultPollInterval = 15 * time.Minute

// AccessControl is the access guard's runtime device and address lists, and
// its view of the tailnet.
type AccessControl interface {
	Grants() access.Grants
	SetGrants(access.Grants) error
	// ServerNodeID is the server's own stable node ID, or "" while unknown.
	ServerNodeID() string
	ReadTailnet(context.Context) (access.Tailnet, error)
	Reconcile(access.Tailnet) (access.Grants, []access.DeviceGrant, bool)
	// Claimable, CheckClaimCode, and EndClaim are the one-time setup code.
	Claimable() bool
	CheckClaimCode(code string, now time.Time) error
	EndClaim()
	// ResolveRequests and PruneRequests are the access requests: a saved
	// grant closes the request of the device it was given to, and the sweep
	// prunes old ones.
	ResolveRequests(ctx context.Context, before, after access.Grants)
	PruneRequests(ctx context.Context, tn access.Tailnet)
}

// RetentionSetter is the request log module's runtime retention.
type RetentionSetter interface {
	Retention() reqlog.Retention
	SetRetention(reqlog.Retention) error
}

// ScheduleSetter is the backup module's runtime schedule.
type ScheduleSetter interface {
	Schedule() (dailyAt string, keep int)
	SetSchedule(dailyAt string, keep int) error
	// NextRun is the next scheduled backup, false while the schedule is off.
	NextRun() (time.Time, bool)
}

// AlertsSetter is the alerts module's runtime webhook and switch.
type AlertsSetter interface {
	Config() alerts.Config
	SetConfig(alerts.Config) error
	Active() bool
	WebhookInvalid() bool
	// AnnounceWebhook queues a "Webhook connected" message without blocking.
	AnnounceWebhook()
}

// Deps are the modules and paths the settings endpoints read and change.
type Deps struct {
	Access        AccessControl
	AccessEnabled bool
	Logs          RetentionSetter
	Backups       ScheduleSetter
	// Alerts is optional; without it the alerts section is left out.
	Alerts AlertsSetter
	// PollInterval is the ingest poll-interval the process started with.
	// Ingest reads it once, so a change applies after a restart.
	PollInterval string
	// ConfigPath is taproot.yaml.
	ConfigPath string
	DataDir    string
}

// Settings owns GET and PUT /settings.
type Settings struct {
	deps         Deps
	activePoll   time.Duration
	identityFrom func(*gin.Context) (access.Identity, bool)
	// timeZone is the IANA name of the server's zone, or "" when unknown.
	timeZone string
	now      func() time.Time

	mu         sync.Mutex
	configPoll time.Duration
}

// New validates the startup poll interval and prepares the endpoints.
func New(deps Deps) (*Settings, error) {
	if deps.Access == nil || deps.Logs == nil || deps.Backups == nil {
		return nil, errors.New("settings need the access, request log, and backup modules")
	}
	if deps.ConfigPath == "" {
		return nil, errors.New("settings need the taproot.yaml path")
	}
	poll, err := parsePollInterval(deps.PollInterval)
	if err != nil {
		return nil, err
	}
	return &Settings{
		deps: deps, activePoll: poll, configPoll: poll, identityFrom: access.IdentityFrom,
		timeZone: localZoneName(os.LookupEnv, os.Readlink), now: time.Now,
	}, nil
}

// parsePollInterval mirrors ingest's rules: blank is the default, "0" disables.
func parsePollInterval(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "":
		return defaultPollInterval, nil
	case "0":
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("poll interval %q: want a non-negative duration such as 15m, or 0 to disable", raw)
	}
	return d, nil
}

// formatDuration writes "15m" and "1h30m" rather than time.Duration's "15m0s".
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Routes registers the settings endpoints under the taproot API group.
func (s *Settings) Routes(r *gin.RouterGroup) {
	r.GET("/settings", s.handleGet)
	r.PUT("/settings", s.handlePut)
	r.POST("/claim", s.handleClaim)
}

type accessView struct {
	Enabled   bool                  `json:"enabled"`
	Devices   []access.DeviceGrant  `json:"devices"`
	Addresses []access.AddressGrant `json:"addresses"`
}

type requestLogsView struct {
	MaxDays         int      `json:"maxDays"`
	MaxBytes        int64    `json:"maxBytes"`
	ExcludePaths    []string `json:"excludePaths"`
	KeepUnrouted404 bool     `json:"keepUnrouted404"`
}

type backupsView struct {
	DailyAt string `json:"dailyAt"`
	Keep    int    `json:"keep"`
	// NextRunAt is RFC 3339 in the server's zone, absent while the schedule is off.
	NextRunAt string `json:"nextRunAt,omitempty"`
}

type ingestView struct {
	// PollInterval is the value in taproot.yaml; ActivePollInterval is what
	// the running poller uses until the next restart.
	PollInterval       string `json:"pollInterval"`
	ActivePollInterval string `json:"activePollInterval"`
	RestartRequired    bool   `json:"restartRequired"`
}

// alertsView never carries the webhook itself, only its last characters.
type alertsView struct {
	Enabled     bool   `json:"enabled"`
	WebhookSet  bool   `json:"webhookSet"`
	WebhookHint string `json:"webhookHint"`
	// WebhookInvalid means taproot.yaml holds a URL that is not a Discord
	// webhook, so alerts are off until it is replaced or cleared.
	WebhookInvalid bool `json:"webhookInvalid"`
	Active         bool `json:"active"`
}

type infoView struct {
	ConfigPath string `json:"configPath"`
	DataDir    string `json:"dataDir"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	BuildDate  string `json:"buildDate"`
	// TimeZone is the server zone's IANA name, or "" when it cannot be
	// named. UTCOffsetSeconds and ZoneAbbreviation are its current values.
	TimeZone         string `json:"timeZone"`
	UTCOffsetSeconds int    `json:"utcOffsetSeconds"`
	ZoneAbbreviation string `json:"zoneAbbreviation"`
}

type view struct {
	Access      accessView      `json:"access"`
	RequestLogs requestLogsView `json:"requestLogs"`
	Backups     backupsView     `json:"backups"`
	Ingest      ingestView      `json:"ingest"`
	Alerts      *alertsView     `json:"alerts,omitempty"`
	Info        infoView        `json:"info"`
}

// update is the PUT body. Absent sections and fields stay as they are.
type update struct {
	Access      *accessUpdate `json:"access"`
	RequestLogs *struct {
		MaxDays         *int      `json:"maxDays"`
		MaxBytes        *int64    `json:"maxBytes"`
		ExcludePaths    *[]string `json:"excludePaths"`
		KeepUnrouted404 *bool     `json:"keepUnrouted404"`
	} `json:"requestLogs"`
	Backups *struct {
		DailyAt *string `json:"dailyAt"`
		Keep    *int    `json:"keep"`
	} `json:"backups"`
	Ingest *struct {
		PollInterval *string `json:"pollInterval"`
	} `json:"ingest"`
	Alerts *struct {
		// DiscordWebhook replaces the webhook; "" clears it.
		DiscordWebhook *string `json:"discordWebhook"`
		Enabled        *bool   `json:"enabled"`
	} `json:"alerts"`
}

// accessUpdate replaces whole lists: devices with a permission, and addresses.
type accessUpdate struct {
	Devices   *[]access.DeviceGrant  `json:"devices"`
	Addresses *[]access.AddressGrant `json:"addresses"`
}

func (s *Settings) current() view {
	ret := s.deps.Logs.Retention()
	dailyAt, keep := s.deps.Backups.Schedule()
	backups := backupsView{DailyAt: dailyAt, Keep: keep}
	if next, ok := s.deps.Backups.NextRun(); ok {
		backups.NextRunAt = next.Format(time.RFC3339)
	}
	abbreviation, offset := s.now().Zone()
	grants := s.deps.Access.Grants()
	var alertsSection *alertsView
	if s.deps.Alerts != nil {
		cfg := s.deps.Alerts.Config()
		alertsSection = &alertsView{
			Enabled: cfg.Enabled == nil || *cfg.Enabled, WebhookSet: cfg.DiscordWebhook != "",
			WebhookHint: alerts.MaskWebhook(cfg.DiscordWebhook), WebhookInvalid: s.deps.Alerts.WebhookInvalid(), Active: s.deps.Alerts.Active(),
		}
	}
	return view{
		Access: accessView{Enabled: s.deps.AccessEnabled, Devices: grants.Devices, Addresses: grants.Addresses},
		RequestLogs: requestLogsView{
			MaxDays: ret.MaxDays, MaxBytes: int64(ret.MaxBytes), ExcludePaths: nonNilStrings(ret.ExcludePaths), KeepUnrouted404: ret.KeepUnrouted404,
		},
		Backups: backups,
		Ingest: ingestView{
			PollInterval:       formatDuration(s.configPoll),
			ActivePollInterval: formatDuration(s.activePoll),
			RestartRequired:    s.configPoll != s.activePoll,
		},
		Alerts: alertsSection,
		Info: infoView{
			ConfigPath: s.deps.ConfigPath, DataDir: s.deps.DataDir,
			Version: buildinfo.Version, Commit: buildinfo.Commit, BuildDate: buildinfo.BuildDate,
			TimeZone: s.timeZone, UTCOffsetSeconds: offset, ZoneAbbreviation: abbreviation,
		},
	}
}

func (s *Settings) handleGet(c *gin.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.JSON(http.StatusOK, s.current())
}

type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.message }

func badRequest(code, format string, args ...any) *apiError {
	return &apiError{status: http.StatusBadRequest, code: code, message: fmt.Sprintf(format, args...)}
}

func writeError(c *gin.Context, err error) {
	var ae *apiError
	if !errors.As(err, &ae) {
		log.WithError(err).Error("taproot settings: update failed")
		ae = &apiError{status: http.StatusInternalServerError, code: "internal", message: err.Error()}
	}
	c.AbortWithStatusJSON(ae.status, gin.H{"error": gin.H{"code": ae.code, "message": ae.message}})
}

func (s *Settings) handlePut(c *gin.Context) {
	var u update
	if err := c.ShouldBindJSON(&u); err != nil {
		writeError(c, badRequest("invalid_body", "settings body: %v", err))
		return
	}
	id, _ := s.identityFrom(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.apply(c.Request.Context(), u, id); err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, s.current())
}

// apply changes each module in turn, undoing earlier changes when a later
// one or the file write fails, so the running state and taproot.yaml agree.
func (s *Settings) apply(ctx context.Context, u update, caller access.Identity) error {
	var edits []yamlEdit
	var undo []func()
	// resolve closes the access requests the new grants answer, once saved.
	resolve := func() {}
	// claimed is true when the new device list gives some device Admin, which
	// uses up the setup code once the change is saved.
	claimed := false
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}

	if a := u.Access; a != nil && (a.Devices != nil || a.Addresses != nil) {
		before := s.deps.Access.Grants()
		next := before
		if a.Devices != nil {
			next.Devices = *a.Devices
		}
		if a.Addresses != nil {
			next.Addresses = *a.Addresses
		}
		if err := access.CheckGrants(next); err != nil {
			return badRequest("invalid_access", "%v", err)
		}
		if err := s.checkAccess(next, caller); err != nil {
			return err
		}
		if err := s.deps.Access.SetGrants(next); err != nil {
			return badRequest("invalid_access", "%v", err)
		}
		after := s.deps.Access.Grants()
		// taproot.yaml is written by then, so a client that hung up must
		// not leave the request open.
		resolve = func() { s.deps.Access.ResolveRequests(context.WithoutCancel(ctx), before, after) }
		undo = append(undo, func() { s.restore("access", s.deps.Access.SetGrants(before)) })
		edits = append(edits, accessEdits(before, s.deps.Access.Grants())...)
		claimed = slices.ContainsFunc(s.deps.Access.Grants().Devices, func(d access.DeviceGrant) bool { return d.Admin })
	}

	if r := u.RequestLogs; r != nil {
		before := s.deps.Logs.Retention()
		next := before
		next.ExcludePaths = slices.Clone(before.ExcludePaths)
		if r.MaxDays != nil {
			next.MaxDays = *r.MaxDays
		}
		if r.MaxBytes != nil {
			next.MaxBytes = reqlog.ByteSize(*r.MaxBytes)
		}
		if r.ExcludePaths != nil {
			next.ExcludePaths = trimAll(*r.ExcludePaths)
		}
		if r.KeepUnrouted404 != nil {
			next.KeepUnrouted404 = *r.KeepUnrouted404
		}
		if err := s.deps.Logs.SetRetention(next); err != nil {
			rollback()
			return badRequest("invalid_retention", "%v", err)
		}
		undo = append(undo, func() { s.restore("request log retention", s.deps.Logs.SetRetention(before)) })
		after := s.deps.Logs.Retention()
		if after.MaxDays != before.MaxDays {
			edits = append(edits, yamlEdit{path: []string{"request-logs", "max-days"}, value: after.MaxDays})
		}
		if after.MaxBytes != before.MaxBytes {
			edits = append(edits, yamlEdit{path: []string{"request-logs", "max-bytes"}, value: formatByteSize(int64(after.MaxBytes))})
		}
		if !slices.Equal(after.ExcludePaths, before.ExcludePaths) {
			edits = append(edits, yamlEdit{path: []string{"request-logs", "exclude-paths"}, value: nonNilStrings(after.ExcludePaths)})
		}
		if after.KeepUnrouted404 != before.KeepUnrouted404 {
			edits = append(edits, yamlEdit{path: []string{"request-logs", "keep-unrouted-404"}, value: after.KeepUnrouted404})
		}
	}

	if b := u.Backups; b != nil {
		beforeAt, beforeKeep := s.deps.Backups.Schedule()
		at, keep := beforeAt, beforeKeep
		if b.DailyAt != nil {
			at = strings.TrimSpace(*b.DailyAt)
		}
		if b.Keep != nil {
			keep = *b.Keep
		}
		if err := s.deps.Backups.SetSchedule(at, keep); err != nil {
			rollback()
			return badRequest("invalid_backups", "%v", err)
		}
		undo = append(undo, func() { s.restore("backup schedule", s.deps.Backups.SetSchedule(beforeAt, beforeKeep)) })
		afterAt, afterKeep := s.deps.Backups.Schedule()
		if afterAt != beforeAt {
			edits = append(edits, yamlEdit{path: []string{"backups", "daily-at"}, value: afterAt})
		}
		if afterKeep != beforeKeep {
			edits = append(edits, yamlEdit{path: []string{"backups", "keep"}, value: afterKeep})
		}
	}

	newWebhook := false
	if a := u.Alerts; a != nil {
		if s.deps.Alerts == nil {
			rollback()
			return badRequest("alerts_unavailable", "alerts are not running")
		}
		before := s.deps.Alerts.Config()
		next := before
		if a.DiscordWebhook != nil {
			next.DiscordWebhook = strings.TrimSpace(*a.DiscordWebhook)
		}
		if a.Enabled != nil {
			enabled := *a.Enabled
			next.Enabled = &enabled
		}
		if err := s.deps.Alerts.SetConfig(next); err != nil {
			rollback()
			return badRequest("invalid_alerts", "discord webhook: %v", err)
		}
		undo = append(undo, func() { s.restore("alerts", s.deps.Alerts.SetConfig(before)) })
		if next.DiscordWebhook != before.DiscordWebhook {
			edits = append(edits, yamlEdit{path: []string{"alerts", "discord-webhook"}, value: next.DiscordWebhook})
			newWebhook = next.DiscordWebhook != ""
		}
		if next.Enabled != nil && (before.Enabled == nil || *before.Enabled != *next.Enabled) {
			edits = append(edits, yamlEdit{path: []string{"alerts", "enabled"}, value: *next.Enabled})
		}
	}

	poll := s.configPoll
	if u.Ingest != nil && u.Ingest.PollInterval != nil {
		d, err := parsePollInterval(*u.Ingest.PollInterval)
		if err != nil {
			rollback()
			return badRequest("invalid_poll_interval", "%v", err)
		}
		if d != s.configPoll {
			poll = d
			edits = append(edits, yamlEdit{path: []string{"ingest", "poll-interval"}, value: formatDuration(d)})
		}
	}

	if len(edits) > 0 {
		if err := writeYAML(s.deps.ConfigPath, edits); err != nil {
			rollback()
			return fmt.Errorf("write %s: %w", s.deps.ConfigPath, err)
		}
	}
	s.configPoll = poll
	resolve()
	if claimed {
		s.deps.Access.EndClaim()
	}
	if newWebhook {
		s.deps.Alerts.AnnounceWebhook()
	}
	return nil
}

// checkAccess refuses a change the guard would ignore or that would lock the
// caller out: the guard is off, the server's own node is given Admin, or the
// calling device loses Admin. Loopback callers are admins whatever the lists
// say.
func (s *Settings) checkAccess(next access.Grants, caller access.Identity) error {
	if !s.deps.AccessEnabled {
		return &apiError{
			status: http.StatusConflict, code: "access_disabled",
			message: "the access guard is disabled in taproot.yaml, so the device and address lists have no effect",
		}
	}
	server := s.deps.Access.ServerNodeID()
	callerAdmin := false
	for _, d := range next.Devices {
		id := strings.TrimSpace(d.ID)
		if server != "" && id == server && d.Admin {
			return badRequest("server_admin", "the taproot server's own node %s never has Admin", id)
		}
		if id == caller.NodeID && d.Admin {
			callerAdmin = true
		}
	}
	if caller.Via == access.ViaTailnet && !callerAdmin {
		return &apiError{
			status: http.StatusConflict, code: "would_lock_out",
			message: fmt.Sprintf("this device (%s, ID %s) must keep Admin", caller.Node, caller.NodeID),
		}
	}
	return nil
}

// accessEdits are the taproot.yaml edits that turn before into after.
func accessEdits(before, after access.Grants) []yamlEdit {
	var edits []yamlEdit
	if !slices.Equal(before.Devices, after.Devices) {
		edits = append(edits, yamlEdit{path: []string{"access", "devices"}, value: after.Devices})
	}
	if !slices.Equal(before.Addresses, after.Addresses) {
		edits = append(edits, yamlEdit{path: []string{"access", "addresses"}, value: after.Addresses})
	}
	return edits
}

// sweepInterval is how often taproot reads the tailnet to forget removed
// devices and refresh device names in taproot.yaml.
const sweepInterval = 10 * time.Minute

// Start reads the tailnet now and every sweepInterval until ctx ends, and
// applies what access.Reconcile finds: devices gone for access.ForgetAfter
// leave access.devices, and names follow the tailnet.
func (s *Settings) Start(ctx context.Context) {
	if !s.deps.AccessEnabled {
		return
	}
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			if err := s.sweep(ctx); err != nil && ctx.Err() == nil {
				log.Warnf("taproot access: update access.devices from the tailnet: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// sweep reads the tailnet once and writes what Reconcile changes, live and
// to taproot.yaml together. A failed read changes nothing and is not an
// error worth logging every interval, since a machine without tailscaled
// fails every time.
func (s *Settings) sweep(ctx context.Context) error {
	tn, err := s.deps.Access.ReadTailnet(ctx)
	if err != nil {
		log.Debugf("taproot access: read the tailnet: %v", err)
		return nil
	}
	s.deps.Access.PruneRequests(ctx, tn)
	s.mu.Lock()
	defer s.mu.Unlock()
	next, forgotten, changed := s.deps.Access.Reconcile(tn)
	if !changed {
		return nil
	}
	before := s.deps.Access.Grants()
	if err := s.deps.Access.SetGrants(next); err != nil {
		return err
	}
	if edits := accessEdits(before, s.deps.Access.Grants()); len(edits) > 0 {
		if err := writeYAML(s.deps.ConfigPath, edits); err != nil {
			s.restore("access", s.deps.Access.SetGrants(before))
			return fmt.Errorf("write %s: %w", s.deps.ConfigPath, err)
		}
	}
	for _, d := range forgotten {
		log.Infof("taproot access: forgot device %s (%s), which has not been on the tailnet for %s", d.Name, d.ID, access.ForgetAfter)
	}
	return nil
}
func (s *Settings) restore(what string, err error) {
	if err != nil {
		log.WithError(err).Errorf("taproot settings: could not restore %s after a failed update", what)
	}
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// formatByteSize writes sizes the way people type them in taproot.yaml when
// they are whole units; reqlog reads both forms.
func formatByteSize(n int64) string {
	for _, unit := range []struct {
		suffix string
		size   int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}} {
		if n > 0 && n%unit.size == 0 {
			return fmt.Sprintf("%d%s", n/unit.size, unit.suffix)
		}
	}
	return fmt.Sprintf("%d", n)
}
