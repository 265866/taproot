// Package settings serves the editable subset of taproot.yaml: the access
// allowlist, request log retention, the backup schedule, and the ingest poll
// interval, and the alerts webhook. A change is applied to the running modules first and written to
// taproot.yaml only after every live setter accepted it.
package settings

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/buildinfo"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/access"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/alerts"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/reqlog"
	log "github.com/sirupsen/logrus"
)

const defaultPollInterval = 15 * time.Minute

// AccessLists are the access guard's runtime login and admin node lists.
type AccessLists interface {
	AllowLogins() []string
	SetAllowLogins([]string) error
	AdminNodes() []string
	SetAdminNodes([]string) error
	// AdminNodesInclude reports whether a candidate admin-nodes list admits node.
	AdminNodesInclude(nodes []string, caller access.Identity) bool
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
}

// AlertsSetter is the alerts module's runtime webhook and switch.
type AlertsSetter interface {
	Config() alerts.Config
	SetConfig(alerts.Config) error
}

// Deps are the modules and paths the settings endpoints read and change.
type Deps struct {
	Access        AccessLists
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
	return &Settings{deps: deps, activePoll: poll, configPoll: poll, identityFrom: access.IdentityFrom}, nil
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
}

type accessView struct {
	Enabled     bool     `json:"enabled"`
	AllowLogins []string `json:"allowLogins"`
	AdminNodes  []string `json:"adminNodes"`
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
	Active      bool   `json:"active"`
}

type infoView struct {
	ConfigPath string `json:"configPath"`
	DataDir    string `json:"dataDir"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	BuildDate  string `json:"buildDate"`
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
	Access *struct {
		AllowLogins *[]string `json:"allowLogins"`
		AdminNodes  *[]string `json:"adminNodes"`
	} `json:"access"`
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

func (s *Settings) current() view {
	ret := s.deps.Logs.Retention()
	dailyAt, keep := s.deps.Backups.Schedule()
	var alertsSection *alertsView
	if s.deps.Alerts != nil {
		cfg := s.deps.Alerts.Config()
		alertsSection = &alertsView{
			Enabled: cfg.Enabled == nil || *cfg.Enabled, WebhookSet: cfg.DiscordWebhook != "",
			WebhookHint: alerts.MaskWebhook(cfg.DiscordWebhook), Active: cfg.Active(),
		}
	}
	return view{
		Access: accessView{
			Enabled:     s.deps.AccessEnabled,
			AllowLogins: nonNilStrings(s.deps.Access.AllowLogins()),
			AdminNodes:  nonNilStrings(s.deps.Access.AdminNodes()),
		},
		RequestLogs: requestLogsView{
			MaxDays: ret.MaxDays, MaxBytes: int64(ret.MaxBytes), ExcludePaths: nonNilStrings(ret.ExcludePaths), KeepUnrouted404: ret.KeepUnrouted404,
		},
		Backups: backupsView{DailyAt: dailyAt, Keep: keep},
		Ingest: ingestView{
			PollInterval:       formatDuration(s.configPoll),
			ActivePollInterval: formatDuration(s.activePoll),
			RestartRequired:    s.configPoll != s.activePoll,
		},
		Alerts: alertsSection,
		Info: infoView{
			ConfigPath: s.deps.ConfigPath, DataDir: s.deps.DataDir,
			Version: buildinfo.Version, Commit: buildinfo.Commit, BuildDate: buildinfo.BuildDate,
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
	if err := s.apply(u, id); err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, s.current())
}

// apply changes each module in turn, undoing earlier changes when a later
// one or the file write fails, so the running state and taproot.yaml agree.
func (s *Settings) apply(u update, caller access.Identity) error {
	var edits []yamlEdit
	var undo []func()
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}

	if u.Access != nil && u.Access.AllowLogins != nil {
		logins, err := s.checkLogins(*u.Access.AllowLogins, caller)
		if err != nil {
			return err
		}
		before := s.deps.Access.AllowLogins()
		if !slices.Equal(before, logins) {
			if err := s.deps.Access.SetAllowLogins(logins); err != nil {
				return badRequest("invalid_allow_logins", "allow-logins: %v", err)
			}
			undo = append(undo, func() { s.restore("allow-logins", s.deps.Access.SetAllowLogins(before)) })
			edits = append(edits, yamlEdit{path: []string{"access", "allow-logins"}, value: s.deps.Access.AllowLogins()})
		}
	}

	if u.Access != nil && u.Access.AdminNodes != nil {
		if err := s.checkAdminNodes(*u.Access.AdminNodes, caller); err != nil {
			rollback()
			return err
		}
		before := s.deps.Access.AdminNodes()
		if err := s.deps.Access.SetAdminNodes(*u.Access.AdminNodes); err != nil {
			rollback()
			return badRequest("invalid_admin_nodes", "admin-nodes: %v", err)
		}
		if after := s.deps.Access.AdminNodes(); !slices.Equal(before, after) {
			undo = append(undo, func() { s.restore("admin-nodes", s.deps.Access.SetAdminNodes(before)) })
			edits = append(edits, yamlEdit{path: []string{"access", "admin-nodes"}, value: nonNilStrings(after)})
		}
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
	return nil
}

// checkLogins normalizes the list and refuses one that would lock the caller out.
// Loopback and trusted-CIDR callers have no login and are admitted regardless.
func (s *Settings) checkLogins(raw []string, caller access.Identity) ([]string, error) {
	if !s.deps.AccessEnabled {
		return nil, &apiError{
			status: http.StatusConflict, code: "access_disabled",
			message: "the access guard is disabled in taproot.yaml, so allow-logins has no effect",
		}
	}
	logins := make([]string, 0, len(raw))
	for _, login := range raw {
		login = strings.ToLower(strings.TrimSpace(login))
		if login == "" {
			return nil, badRequest("invalid_allow_logins", "allow-logins must not contain blank entries")
		}
		if !slices.Contains(logins, login) {
			logins = append(logins, login)
		}
	}
	if len(logins) == 0 {
		return nil, badRequest("invalid_allow_logins", "allow-logins needs at least one login")
	}
	if caller.Login != "" && !slices.Contains(logins, strings.ToLower(caller.Login)) {
		return nil, &apiError{
			status: http.StatusConflict, code: "would_lock_out",
			message: fmt.Sprintf("allow-logins must keep your own login %s", caller.Login),
		}
	}
	return logins, nil
}

// checkAdminNodes refuses a list that would drop the calling device. Loopback
// callers are admins whatever the list says.
func (s *Settings) checkAdminNodes(nodes []string, caller access.Identity) error {
	if !s.deps.AccessEnabled {
		return &apiError{
			status: http.StatusConflict, code: "access_disabled",
			message: "the access guard is disabled in taproot.yaml, so admin-nodes has no effect",
		}
	}
	if caller.Via == access.ViaTailnet && !s.deps.Access.AdminNodesInclude(nodes, caller) {
		return &apiError{
			status: http.StatusConflict, code: "would_lock_out",
			message: fmt.Sprintf("admin-nodes must keep this device %s", caller.Node),
		}
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
