package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/access"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/alerts"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/backup"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/ingest"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/reqlog"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	"gopkg.in/yaml.v3"
)

func init() { gin.SetMode(gin.TestMode) }

const commentedYAML = `# taproot settings
data-dir: /srv/taproot # keep me
access:
  allow-logins: [alice@github] # me
  trusted-cidrs:
    - 10.0.0.0/8
request-logs:
  max-bytes: 20GB
backups:
  keep: 3
`

// fileConfig mirrors taproot.Config with the modules' own types, so the tests
// read the written file the way Setup does: strictly, refusing unknown keys.
type fileConfig struct {
	DataDir     string        `yaml:"data-dir"`
	Access      access.Config `yaml:"access"`
	Store       store.Config  `yaml:"store"`
	Ingest      ingest.Config `yaml:"ingest"`
	RequestLogs reqlog.Config `yaml:"request-logs"`
	Backups     backup.Config `yaml:"backups"`
	Alerts      alerts.Config `yaml:"alerts"`
}

func decodeStrict(raw []byte, cfg *fileConfig) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// fixtureLogin admits a caller when the fixture's taproot.yaml names no
// login, because an enabled guard refuses to start without one.
const fixtureLogin = "fixture@example.com"

type fixture struct {
	path    string
	guard   *access.Guard
	logs    *reqlog.Logs
	backups *backup.Backups
	s       *Settings
	engine  *gin.Engine
	// caller is the identity handlers see; the zero value is a loopback caller.
	caller access.Identity
}

type options struct {
	yaml          string
	accessEnabled *bool
	pollInterval  string
}

func newFixture(t *testing.T, opt options) *fixture {
	t.Helper()
	t.Setenv("MANAGEMENT_PASSWORD", "test-secret")
	dir := t.TempDir()
	f := &fixture{path: filepath.Join(dir, "taproot.yaml"), caller: access.Identity{Via: access.ViaLoopback}}
	if opt.yaml != "" {
		if err := os.WriteFile(f.path, []byte(opt.yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var cfg fileConfig
	if err := decodeStrict([]byte(opt.yaml), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Access.Enabled = opt.accessEnabled
	if cfg.Access.AllowLogins == nil {
		cfg.Access.AllowLogins = []string{fixtureLogin}
	}
	dataDir := filepath.Join(dir, "data")
	db, err := store.Open(context.Background(), store.Config{}, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if f.guard, err = access.New(cfg.Access); err != nil {
		t.Fatal(err)
	}
	if f.logs, err = reqlog.New(cfg.RequestLogs, db, dataDir); err != nil {
		t.Fatal(err)
	}
	if f.backups, err = backup.New(cfg.Backups, db, backup.Paths{TaprootFile: f.path, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	f.s, err = New(Deps{
		Access: f.guard, AccessEnabled: opt.accessEnabled == nil || *opt.accessEnabled,
		Logs: f.logs, Backups: f.backups, PollInterval: opt.pollInterval,
		ConfigPath: f.path, DataDir: dataDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.s.identityFrom = func(*gin.Context) (access.Identity, bool) { return f.caller, true }
	f.engine = gin.New()
	f.s.Routes(f.engine.Group("/taproot/api/v1"))
	return f
}

func (f *fixture) do(t *testing.T, method, body string) (int, view, string) {
	t.Helper()
	req := httptest.NewRequest(method, "/taproot/api/v1/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.engine.ServeHTTP(rec, req)
	var v view
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, v, rec.Body.String()
}

func (f *fixture) file(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (f *fixture) parsed(t *testing.T) fileConfig {
	t.Helper()
	var cfg fileConfig
	if err := decodeStrict([]byte(f.file(t)), &cfg); err != nil {
		t.Fatalf("written taproot.yaml does not parse: %v\n%s", err, f.file(t))
	}
	return cfg
}

func errorCode(t *testing.T, body string) string {
	t.Helper()
	var e struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &e); err != nil || e.Error.Code == "" {
		t.Fatalf("not an API error: %s", body)
	}
	return e.Error.Code
}

func TestGetReportsLiveValues(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	code, v, body := f.do(t, http.MethodGet, "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d %s", code, body)
	}
	if !v.Access.Enabled || !slices.Equal(v.Access.AllowLogins, []string{"alice@github"}) {
		t.Errorf("access = %+v", v.Access)
	}
	// max-days is absent from the file, so reqlog's default of 14 applies.
	if v.RequestLogs.MaxDays != 14 || v.RequestLogs.MaxBytes != 20<<30 || v.RequestLogs.ExcludePaths == nil {
		t.Errorf("requestLogs = %+v", v.RequestLogs)
	}
	if v.Backups.DailyAt != "03:13" || v.Backups.Keep != 3 {
		t.Errorf("backups = %+v", v.Backups)
	}
	if v.Ingest.PollInterval != "15m" || v.Ingest.ActivePollInterval != "15m" || v.Ingest.RestartRequired {
		t.Errorf("ingest = %+v", v.Ingest)
	}
	if v.Info.ConfigPath != f.path || v.Info.DataDir == "" || v.Info.Version == "" {
		t.Errorf("info = %+v", v.Info)
	}
}

func TestPutAppliesLiveAndPreservesFile(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	f.caller = access.Identity{Login: "alice@github", Via: access.ViaTailnet}
	code, v, body := f.do(t, http.MethodPut, `{
		"access": {"allowLogins": ["alice@github", " Friend@GitHub "]},
		"requestLogs": {"maxDays": 30, "maxBytes": 5368709120, "excludePaths": ["HEAD /api/*"], "keepUnrouted404": true},
		"backups": {"dailyAt": "04:30", "keep": 7},
		"ingest": {"pollInterval": "30m"}
	}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}

	if got := f.guard.AllowLogins(); !slices.Equal(got, []string{"alice@github", "friend@github"}) {
		t.Errorf("guard allow-logins = %v", got)
	}
	ret := f.logs.Retention()
	if ret.MaxDays != 30 || ret.MaxBytes != 5<<30 || !slices.Equal(ret.ExcludePaths, []string{"HEAD /api/*"}) || !ret.KeepUnrouted404 {
		t.Errorf("retention = %+v", ret)
	}
	if at, keep := f.backups.Schedule(); at != "04:30" || keep != 7 {
		t.Errorf("schedule = %s keep %d", at, keep)
	}
	if v.Ingest.PollInterval != "30m" || v.Ingest.ActivePollInterval != "15m" || !v.Ingest.RestartRequired {
		t.Errorf("ingest = %+v", v.Ingest)
	}

	raw := f.file(t)
	for _, keep := range []string{"# taproot settings", "# keep me", "# me", "trusted-cidrs:", "- 10.0.0.0/8", "data-dir: /srv/taproot"} {
		if !strings.Contains(raw, keep) {
			t.Errorf("taproot.yaml lost %q:\n%s", keep, raw)
		}
	}
	if !strings.Contains(raw, "allow-logins: [alice@github, friend@github]") {
		t.Errorf("allow-logins not written in flow style:\n%s", raw)
	}
	if !strings.Contains(raw, "max-bytes: 5GB") {
		t.Errorf("max-bytes not written as a size:\n%s", raw)
	}
	cfg := f.parsed(t)
	if !slices.Equal(cfg.Access.AllowLogins, []string{"alice@github", "friend@github"}) ||
		!slices.Equal(cfg.Access.TrustedCIDRs, []string{"10.0.0.0/8"}) {
		t.Errorf("file access = %+v", cfg.Access)
	}
	if cfg.RequestLogs.MaxDays != 30 || cfg.RequestLogs.MaxBytes != 5<<30 || !cfg.RequestLogs.KeepUnrouted404 ||
		!slices.Equal(cfg.RequestLogs.ExcludePaths, []string{"HEAD /api/*"}) {
		t.Errorf("file request-logs = %+v", cfg.RequestLogs)
	}
	if cfg.Backups.DailyAt != "04:30" || cfg.Backups.Keep != 7 {
		t.Errorf("file backups = %+v", cfg.Backups)
	}
	if cfg.Ingest.PollInterval != "30m" || cfg.DataDir != "/srv/taproot" {
		t.Errorf("file ingest = %+v data-dir %q", cfg.Ingest, cfg.DataDir)
	}

	// Setting the poll interval back to the running value clears the restart notice.
	if _, v, _ = f.do(t, http.MethodPut, `{"ingest": {"pollInterval": "15m"}}`); v.Ingest.RestartRequired {
		t.Errorf("restart still required after reverting: %+v", v.Ingest)
	}
}

func TestPutRefusesToLockOutCaller(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	f.caller = access.Identity{Login: "alice@github", Via: access.ViaTailnet}
	code, _, body := f.do(t, http.MethodPut, `{"access": {"allowLogins": ["friend@github"]}, "backups": {"keep": 5}}`)
	if code != http.StatusConflict || errorCode(t, body) != "would_lock_out" {
		t.Fatalf("PUT = %d %s, want 409 would_lock_out", code, body)
	}
	if got := f.guard.AllowLogins(); !slices.Equal(got, []string{"alice@github"}) {
		t.Errorf("allow-logins changed to %v", got)
	}
	if _, keep := f.backups.Schedule(); keep != 3 {
		t.Errorf("keep changed to %d", keep)
	}
	if f.file(t) != commentedYAML {
		t.Errorf("file changed:\n%s", f.file(t))
	}

	// Matching is case-insensitive, like the guard's.
	if code, _, body = f.do(t, http.MethodPut, `{"access": {"allowLogins": ["Alice@GitHub", "friend@github"]}}`); code != http.StatusOK {
		t.Fatalf("PUT keeping own login in other case = %d %s", code, body)
	}
}

func TestAdminNodes(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	_, v, _ := f.do(t, http.MethodGet, "")
	if v.Access.AdminNodes == nil || len(v.Access.AdminNodes) != 0 {
		t.Fatalf("GET adminNodes = %#v, want []", v.Access.AdminNodes)
	}

	// Loopback may set any list; entries are normalized and written to the file.
	code, v, body := f.do(t, http.MethodPut, `{"access": {"adminNodes": [" Laptop. ", "laptop", "phone.tailnet-0000.ts.net"]}}`)
	if code != http.StatusOK || !slices.Equal(v.Access.AdminNodes, []string{"laptop", "phone.tailnet-0000.ts.net"}) {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if got := f.parsed(t).Access.AdminNodes; !slices.Equal(got, []string{"laptop", "phone.tailnet-0000.ts.net"}) {
		t.Errorf("file admin-nodes = %v", got)
	}
	if !strings.Contains(f.file(t), "allow-logins: [alice@github] # me") {
		t.Errorf("unrelated access lines changed:\n%s", f.file(t))
	}

	// A tailnet caller cannot drop its own device.
	f.caller = access.Identity{Login: "alice@github", Node: "phone.tailnet-0000.ts.net", Via: access.ViaTailnet}
	before := f.file(t)
	code, _, body = f.do(t, http.MethodPut, `{"access": {"adminNodes": ["laptop"]}, "backups": {"keep": 5}}`)
	if code != http.StatusConflict || errorCode(t, body) != "would_lock_out" {
		t.Fatalf("PUT dropping own node = %d %s, want 409 would_lock_out", code, body)
	}
	if got := f.guard.AdminNodes(); !slices.Equal(got, []string{"laptop", "phone.tailnet-0000.ts.net"}) {
		t.Errorf("admin-nodes changed to %v", got)
	}
	if _, keep := f.backups.Schedule(); keep != 3 || f.file(t) != before {
		t.Errorf("a refused update changed state: keep %d\n%s", keep, f.file(t))
	}
	if code, _, body = f.do(t, http.MethodPut, `{"access": {"adminNodes": []}}`); code != http.StatusConflict {
		t.Fatalf("PUT clearing the list from a tailnet caller = %d %s", code, body)
	}
	if code, v, body = f.do(t, http.MethodPut, `{"access": {"adminNodes": ["PHONE.tailnet-0000.ts.net."]}}`); code != http.StatusOK || !slices.Equal(v.Access.AdminNodes, []string{"phone.tailnet-0000.ts.net"}) {
		t.Fatalf("PUT keeping own node in other case = %d %s", code, body)
	}

	// Loopback may clear it.
	f.caller = access.Identity{Via: access.ViaLoopback}
	if code, v, body = f.do(t, http.MethodPut, `{"access": {"adminNodes": []}}`); code != http.StatusOK || len(v.Access.AdminNodes) != 0 {
		t.Fatalf("PUT clearing from loopback = %d %s", code, body)
	}
	if got := f.parsed(t).Access.AdminNodes; len(got) != 0 {
		t.Errorf("file admin-nodes after clearing = %v", got)
	}
}

func TestLoopbackCallerMaySetAnyList(t *testing.T) {
	f := newFixture(t, options{})
	code, v, body := f.do(t, http.MethodPut, `{"access": {"allowLogins": ["friend@github"]}}`)
	if code != http.StatusOK || !slices.Equal(v.Access.AllowLogins, []string{"friend@github"}) {
		t.Fatalf("PUT = %d %s", code, body)
	}
	// taproot.yaml did not exist; it is created private.
	cfg := f.parsed(t)
	if !slices.Equal(cfg.Access.AllowLogins, []string{"friend@github"}) {
		t.Errorf("file allow-logins = %v", cfg.Access.AllowLogins)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", info.Mode().Perm())
		}
	}
	for _, bad := range []string{`{"access": {"allowLogins": []}}`, `{"access": {"allowLogins": ["a@github", "  "]}}`} {
		if code, _, body := f.do(t, http.MethodPut, bad); code != http.StatusBadRequest || errorCode(t, body) != "invalid_allow_logins" {
			t.Errorf("PUT %s = %d %s", bad, code, body)
		}
	}
}

func TestLaterFailureRollsBackEarlierChanges(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
	}{
		{"bad exclude path", `{"access": {"allowLogins": ["a@github"]}, "requestLogs": {"excludePaths": ["no-slash"]}}`, "invalid_retention"},
		{"bad admin node", `{"access": {"allowLogins": ["a@github"], "adminNodes": ["bad name"]}}`, "invalid_admin_nodes"},
		{"bad daily-at", `{"access": {"allowLogins": ["a@github"]}, "requestLogs": {"maxDays": 3}, "backups": {"dailyAt": "25:00"}}`, "invalid_backups"},
		{"bad keep", `{"requestLogs": {"maxDays": 3}, "backups": {"keep": 0}}`, "invalid_backups"},
		{"bad poll interval", `{"access": {"allowLogins": ["a@github"]}, "backups": {"dailyAt": "off"}, "ingest": {"pollInterval": "soon"}}`, "invalid_poll_interval"},
		{"negative poll interval", `{"backups": {"keep": 9}, "ingest": {"pollInterval": "-5m"}}`, "invalid_poll_interval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, options{yaml: commentedYAML})
			code, _, body := f.do(t, http.MethodPut, tc.body)
			if code != http.StatusBadRequest || errorCode(t, body) != tc.code {
				t.Fatalf("PUT = %d %s, want 400 %s", code, body, tc.code)
			}
			if got := f.guard.AllowLogins(); !slices.Equal(got, []string{"alice@github"}) {
				t.Errorf("allow-logins = %v", got)
			}
			if ret := f.logs.Retention(); ret.MaxDays != 14 || len(ret.ExcludePaths) != 0 {
				t.Errorf("retention = %+v", ret)
			}
			if at, keep := f.backups.Schedule(); at != "03:13" || keep != 3 {
				t.Errorf("schedule = %s keep %d", at, keep)
			}
			if f.file(t) != commentedYAML {
				t.Errorf("file changed:\n%s", f.file(t))
			}
		})
	}
}

func TestWriteFailureRollsBackLiveChanges(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	// A directory where the file should be makes the read fail.
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.path, 0o700); err != nil {
		t.Fatal(err)
	}
	code, _, body := f.do(t, http.MethodPut, `{"access": {"allowLogins": ["a@github"]}, "backups": {"keep": 2}, "requestLogs": {"maxDays": 2}}`)
	if code != http.StatusInternalServerError {
		t.Fatalf("PUT = %d %s, want 500", code, body)
	}
	if got := f.guard.AllowLogins(); !slices.Equal(got, []string{"alice@github"}) {
		t.Errorf("allow-logins = %v", got)
	}
	if ret := f.logs.Retention(); ret.MaxDays != 14 {
		t.Errorf("max-days = %d", ret.MaxDays)
	}
	if _, keep := f.backups.Schedule(); keep != 3 {
		t.Errorf("keep = %d", keep)
	}
}

func TestUnchangedValuesLeaveFileAlone(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	before, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	code, _, body := f.do(t, http.MethodPut, `{
		"access": {"allowLogins": ["alice@github"]},
		"requestLogs": {"maxDays": 14, "maxBytes": 21474836480, "excludePaths": [], "keepUnrouted404": false},
		"backups": {"dailyAt": "03:13", "keep": 3},
		"ingest": {"pollInterval": "15m"}
	}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	after, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if f.file(t) != commentedYAML || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("no-op PUT rewrote the file:\n%s", f.file(t))
	}
}

func TestDisabledGuardRefusesAllowLogins(t *testing.T) {
	off := false
	f := newFixture(t, options{accessEnabled: &off})
	_, v, _ := f.do(t, http.MethodGet, "")
	if v.Access.Enabled || v.Access.AllowLogins == nil {
		t.Errorf("access = %+v", v.Access)
	}
	for _, put := range []string{`{"access": {"allowLogins": ["a@github"]}}`, `{"access": {"adminNodes": ["laptop"]}}`} {
		code, _, body := f.do(t, http.MethodPut, put)
		if code != http.StatusConflict || errorCode(t, body) != "access_disabled" {
			t.Fatalf("PUT %s = %d %s", put, code, body)
		}
	}
	// Other sections still save.
	if code, _, body := f.do(t, http.MethodPut, `{"backups": {"dailyAt": "off"}}`); code != http.StatusOK {
		t.Fatalf("PUT backups = %d %s", code, body)
	}
	if got := f.parsed(t).Backups.DailyAt; got != "off" {
		t.Errorf("file daily-at = %q", got)
	}
}

func TestMalformedBodyAndFile(t *testing.T) {
	f := newFixture(t, options{})
	if code, _, body := f.do(t, http.MethodPut, `{"backups": {"keep": "many"}}`); code != http.StatusBadRequest || errorCode(t, body) != "invalid_body" {
		t.Errorf("PUT wrong type = %d %s", code, body)
	}
	if err := os.WriteFile(f.path, []byte("- a\n- b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, body := f.do(t, http.MethodPut, `{"backups": {"keep": 4}}`); code != http.StatusInternalServerError {
		t.Errorf("PUT with list file = %d %s", code, body)
	}
	if _, keep := f.backups.Schedule(); keep != 14 {
		t.Errorf("keep = %d after failed write", keep)
	}
}

func TestWriteYAMLHandlesNullSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "taproot.yaml")
	if err := os.WriteFile(path, []byte("# only a comment\naccess:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeYAML(path, []yamlEdit{{path: []string{"access", "allow-logins"}, value: []string{"a@github"}}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var cfg fileConfig
	if err := decodeStrict(raw, &cfg); err != nil || !slices.Equal(cfg.Access.AllowLogins, []string{"a@github"}) {
		t.Fatalf("got %+v %v:\n%s", cfg.Access, err, raw)
	}
	if !bytes.Contains(raw, []byte("# only a comment")) {
		t.Errorf("comment lost:\n%s", raw)
	}

	commentOnly := filepath.Join(t.TempDir(), "taproot.yaml")
	if err := os.WriteFile(commentOnly, []byte("# nothing yet\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeYAML(commentOnly, []yamlEdit{{path: []string{"backups", "keep"}, value: 2}}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(commentOnly)
	if err := decodeStrict(raw, &cfg); err != nil || cfg.Backups.Keep != 2 {
		t.Fatalf("comment-only file: %+v %v:\n%s", cfg.Backups, err, raw)
	}
}

func TestFormatting(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "0", 15 * time.Minute: "15m", 90 * time.Minute: "1h30m", 2 * time.Hour: "2h", 45 * time.Second: "45s",
		time.Hour + 30*time.Second: "1h0m30s",
	} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
	for n, want := range map[int64]string{
		20 << 30: "20GB", 512 << 20: "512MB", 1536 << 20: "1536MB", 1 << 40: "1TB", 1000: "1000", -1: "-1", 0: "0",
	} {
		if got := formatByteSize(n); got != want {
			t.Errorf("formatByteSize(%d) = %q, want %q", n, got, want)
		}
		if n > 0 {
			if back, err := reqlog.ParseByteSize(formatByteSize(n)); err != nil || int64(back) != n {
				t.Errorf("ParseByteSize(%q) = %d, %v; want %d", formatByteSize(n), back, err, n)
			}
		}
	}
	for raw, want := range map[string]time.Duration{"": 15 * time.Minute, "0": 0, " 5m ": 5 * time.Minute, "0s": 0} {
		if got, err := parsePollInterval(raw); err != nil || got != want {
			t.Errorf("parsePollInterval(%q) = %v, %v", raw, got, err)
		}
	}
	if _, err := New(Deps{Access: &access.Guard{}, Logs: &reqlog.Logs{}, Backups: &backup.Backups{}, ConfigPath: "x", PollInterval: "often"}); err == nil {
		t.Error("New accepted a bad startup poll interval")
	}
}
