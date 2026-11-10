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
	// The zone tests need tzdata on hosts that do not ship it.
	_ "time/tzdata"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/alerts"
	"github.com/265866/taproot/internal/taproot/backup"
	"github.com/265866/taproot/internal/taproot/ingest"
	"github.com/265866/taproot/internal/taproot/reqlog"
	"github.com/265866/taproot/internal/taproot/store"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

func init() { gin.SetMode(gin.TestMode) }

const commentedYAML = `# taproot settings
data-dir: /srv/taproot # keep me
access:
  devices:
    - id: nlaptopCNTRL
      name: laptop
      proxy: true
      admin: true
  addresses:
    - cidr: 10.0.0.0/8 # podman
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

// laptop is the fixture's admin device; the server's own node is serverID.
var laptop = access.DeviceGrant{ID: "nlaptopCNTRL", Name: "laptop", Proxy: true, Admin: true}

const serverID = "nserverCNTRL"

// fakeAccess is the guard with a known server node and, when tailnet is set,
// a canned tailnet read and reconcile result, since the fixture has no
// tailscaled. realServerNode reads the node through the guard instead.
type fakeAccess struct {
	*access.Guard
	tailnet        *access.Tailnet
	reconcile      func(access.Tailnet) (access.Grants, []access.DeviceGrant, bool)
	realServerNode bool
}

func (f *fakeAccess) ServerNodeID(ctx context.Context) string {
	if f.realServerNode {
		return f.Guard.ServerNodeID(ctx)
	}
	return serverID
}

func (f *fakeAccess) ReadTailnet(ctx context.Context) (access.Tailnet, error) {
	if f.tailnet == nil {
		return f.Guard.ReadTailnet(ctx)
	}
	return *f.tailnet, nil
}

func (f *fakeAccess) Reconcile(tn access.Tailnet) (access.Grants, []access.DeviceGrant, bool) {
	if f.reconcile != nil {
		return f.reconcile(tn)
	}
	return f.Guard.Reconcile(tn)
}

type fixture struct {
	path   string
	guard  *access.Guard
	access *fakeAccess
	// requests is the guard's access request store.
	requests *access.Requests
	logs     *reqlog.Logs
	backups  *backup.Backups
	s        *Settings
	engine   *gin.Engine
	// caller is the identity handlers see; the zero value is a loopback caller.
	caller access.Identity
}

type options struct {
	yaml          string
	accessEnabled *bool
	pollInterval  string
	// tailscaleSocket is the guard's LocalAPI socket; empty is an absent one.
	tailscaleSocket string
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
	// Never the host's real tailscaled.
	cfg.Access.TailscaleSocket = filepath.Join(dir, "absent.sock")
	if opt.tailscaleSocket != "" {
		cfg.Access.TailscaleSocket = opt.tailscaleSocket
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
	f.requests = access.NewRequests(db, nil)
	f.guard.AttachRequests(f.requests)
	f.access = &fakeAccess{Guard: f.guard}
	f.s, err = New(Deps{
		Access: f.access, AccessEnabled: opt.accessEnabled == nil || *opt.accessEnabled,
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
	if !v.Access.Enabled || !slices.Equal(v.Access.Devices, []access.DeviceGrant{laptop}) ||
		!slices.Equal(v.Access.Addresses, []access.AddressGrant{{CIDR: "10.0.0.0/8"}}) {
		t.Errorf("access = %+v", v.Access)
	}
	// max-days is absent from the file, so reqlog's default of 14 applies.
	if v.RequestLogs.MaxDays != 14 || v.RequestLogs.MaxBytes != 20<<30 || v.RequestLogs.ExcludePaths == nil {
		t.Errorf("requestLogs = %+v", v.RequestLogs)
	}
	if v.Backups.DailyAt != "00:00" || v.Backups.Keep != 3 {
		t.Errorf("backups = %+v", v.Backups)
	}
	if v.Ingest.PollInterval != "15m" || v.Ingest.ActivePollInterval != "15m" || v.Ingest.RestartRequired {
		t.Errorf("ingest = %+v", v.Ingest)
	}
	if v.Info.ConfigPath != f.path || v.Info.DataDir == "" || v.Info.Version == "" {
		t.Errorf("info = %+v", v.Info)
	}
}

func TestGetReportsNextBackupAndServerZone(t *testing.T) {
	f := newFixture(t, options{})
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	f.s.timeZone = "America/New_York"
	f.s.now = func() time.Time { return time.Date(2027, 7, 1, 12, 0, 0, 0, ny) }
	before := time.Now()
	_, v, body := f.do(t, http.MethodGet, "")
	if v.Backups.DailyAt != "00:00" {
		t.Errorf("default dailyAt = %q, want 00:00", v.Backups.DailyAt)
	}
	// The backup module keeps its own clock, so nextRunAt is the next local
	// midnight after the real now, written with the server's offset.
	next, err := time.Parse(time.RFC3339, v.Backups.NextRunAt)
	if err != nil {
		t.Fatalf("nextRunAt %q: %v (%s)", v.Backups.NextRunAt, err, body)
	}
	if next.Format("15:04:05") != "00:00:00" || !next.After(before) || next.Sub(before) > 25*time.Hour {
		t.Errorf("nextRunAt = %s, want the next local midnight after %s", v.Backups.NextRunAt, before.Format(time.RFC3339))
	}
	_, gotOffset := next.Zone()
	_, wantOffset := next.In(time.Local).Zone()
	if gotOffset != wantOffset {
		t.Errorf("nextRunAt %s has offset %d, want the server's %d", v.Backups.NextRunAt, gotOffset, wantOffset)
	}
	if v.Info.TimeZone != "America/New_York" || v.Info.UTCOffsetSeconds != -4*3600 || v.Info.ZoneAbbreviation != "EDT" {
		t.Errorf("zone = %q %d %q, want America/New_York -14400 EDT", v.Info.TimeZone, v.Info.UTCOffsetSeconds, v.Info.ZoneAbbreviation)
	}

	if code, _, body := f.do(t, http.MethodPut, `{"backups": {"dailyAt": "off"}}`); code != http.StatusOK {
		t.Fatalf("PUT off = %d %s", code, body)
	}
	_, _, body = f.do(t, http.MethodGet, "")
	if strings.Contains(body, "nextRunAt") {
		t.Errorf("schedule off still reports nextRunAt: %s", body)
	}
}

func TestLocalZoneName(t *testing.T) {
	env := func(tz string, set bool) func(string) (string, bool) {
		return func(key string) (string, bool) {
			if key != "TZ" {
				t.Fatalf("looked up %s", key)
			}
			return tz, set
		}
	}
	link := func(target string, err error) func(string) (string, error) {
		return func(path string) (string, error) {
			if path != "/etc/localtime" {
				t.Fatalf("read link %s", path)
			}
			return target, err
		}
	}
	notLink := link("", errors.New("invalid argument"))
	for _, tc := range []struct {
		name     string
		tz       string
		tzSet    bool
		readlink func(string) (string, error)
		want     string
	}{
		{"TZ names a zone", "Europe/Berlin", true, notLink, "Europe/Berlin"},
		{"TZ with a leading colon", ":Asia/Tokyo", true, notLink, "Asia/Tokyo"},
		{"TZ names a zone file", "/usr/share/zoneinfo/America/New_York", true, notLink, "America/New_York"},
		{"empty TZ means UTC", "", true, notLink, "UTC"},
		{"TZ that does not load", "Mars/Olympus_Mons", true, notLink, ""},
		{"TZ outside a zoneinfo directory", "/tmp/zone", true, notLink, ""},
		{"TZ pointing at /etc/localtime", "/etc/localtime", true, link("/usr/share/zoneinfo/Etc/UTC", nil), "Etc/UTC"},
		{"symlinked /etc/localtime", "", false, link("/usr/share/zoneinfo/Etc/UTC", nil), "Etc/UTC"},
		{"relative symlink", "", false, link("../usr/share/zoneinfo/America/Los_Angeles", nil), "America/Los_Angeles"},
		{"posix subdirectory", "", false, link("/usr/share/zoneinfo/posix/Europe/Paris", nil), "Europe/Paris"},
		{"NixOS zoneinfo", "", false, link("/etc/zoneinfo/Australia/Sydney", nil), "Australia/Sydney"},
		{"copied /etc/localtime", "", false, notLink, ""},
		{"no /etc/localtime", "", false, link("", os.ErrNotExist), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := localZoneName(env(tc.tz, tc.tzSet), tc.readlink); got != tc.want {
				t.Errorf("localZoneName = %q, want %q", got, tc.want)
			}
		})
	}
}

// laptopCaller is the fixture's admin device calling over the tailnet.
var laptopCaller = access.Identity{Login: "alice@github", Node: "laptop.tailnet-0000.ts.net", NodeID: laptop.ID, Via: access.ViaTailnet, Proxy: true, Admin: true}

func TestPutAppliesLiveAndPreservesFile(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	f.caller = laptopCaller
	code, v, body := f.do(t, http.MethodPut, `{
		"access": {"devices": [
			{"id": "nlaptopCNTRL", "name": "laptop", "proxy": true, "admin": true},
			{"id": " nworkerCNTRL ", "name": " worker ", "proxy": true, "admin": false},
			{"id": "nbob-pcCNTRL", "name": "bob-pc", "proxy": false, "admin": false}
		]},
		"requestLogs": {"maxDays": 30, "maxBytes": 5368709120, "excludePaths": ["HEAD /api/*"], "keepUnrouted404": true},
		"backups": {"dailyAt": "04:30", "keep": 7},
		"ingest": {"pollInterval": "30m"}
	}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}

	// A device with neither permission is not kept.
	wantDevices := []access.DeviceGrant{laptop, {ID: "nworkerCNTRL", Name: "worker", Proxy: true}}
	if got := f.guard.Grants().Devices; !slices.Equal(got, wantDevices) {
		t.Errorf("guard devices = %+v", got)
	}
	if !slices.Equal(v.Access.Devices, wantDevices) {
		t.Errorf("response devices = %+v", v.Access.Devices)
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
	for _, keep := range []string{"# taproot settings", "# keep me", "cidr: 10.0.0.0/8 # podman", "data-dir: /srv/taproot", "- id: nworkerCNTRL\n      name: worker\n      proxy: true\n      admin: false"} {
		if !strings.Contains(raw, keep) {
			t.Errorf("taproot.yaml lacks %q:\n%s", keep, raw)
		}
	}
	if !strings.Contains(raw, "max-bytes: 5GB") {
		t.Errorf("max-bytes not written as a size:\n%s", raw)
	}
	cfg := f.parsed(t)
	if !slices.Equal(cfg.Access.Devices, wantDevices) || !slices.Equal(cfg.Access.Addresses, []access.AddressGrant{{CIDR: "10.0.0.0/8"}}) {
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

// A device cannot take its own Admin away, and nothing else in the same
// request is applied.
func TestPutRefusesToLockOutCaller(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	f.caller = laptopCaller
	for _, put := range []string{
		`{"access": {"devices": [{"id": "nlaptopCNTRL", "proxy": true, "admin": false}]}, "backups": {"keep": 5}}`,
		`{"access": {"devices": []}, "backups": {"keep": 5}}`,
		`{"access": {"devices": [{"id": "nworkerCNTRL", "admin": true}]}, "backups": {"keep": 5}}`,
	} {
		code, _, body := f.do(t, http.MethodPut, put)
		if code != http.StatusConflict || errorCode(t, body) != "would_lock_out" {
			t.Fatalf("PUT %s = %d %s, want 409 would_lock_out", put, code, body)
		}
	}
	if got := f.guard.Grants().Devices; !slices.Equal(got, []access.DeviceGrant{laptop}) {
		t.Errorf("devices changed to %+v", got)
	}
	if _, keep := f.backups.Schedule(); keep != 3 {
		t.Errorf("keep changed to %d", keep)
	}
	if f.file(t) != commentedYAML {
		t.Errorf("file changed:\n%s", f.file(t))
	}

	// Keeping Admin, the device may drop its own Proxy, and addresses alone
	// need no device list.
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nlaptopCNTRL", "name": "laptop", "admin": true}]}}`); code != http.StatusOK {
		t.Fatalf("PUT keeping own Admin = %d %s", code, body)
	}
	if code, v, body := f.do(t, http.MethodPut, `{"access": {"addresses": []}}`); code != http.StatusOK || len(v.Access.Addresses) != 0 || len(v.Access.Devices) != 1 {
		t.Fatalf("PUT addresses only = %d %s", code, body)
	}
}

// The server's own node always has Proxy and never Admin, so a list that
// gives it Admin is refused rather than silently ignored.
func TestServerNodeNeverGetsAdmin(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nlaptopCNTRL", "admin": true}, {"id": "nserverCNTRL", "proxy": true, "admin": true}]}}`)
	if code != http.StatusBadRequest || errorCode(t, body) != "server_admin" {
		t.Fatalf("PUT = %d %s, want 400 server_admin", code, body)
	}
	if f.file(t) != commentedYAML {
		t.Errorf("file changed:\n%s", f.file(t))
	}
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nserverCNTRL", "proxy": true}]}}`); code != http.StatusOK {
		t.Fatalf("PUT server with Proxy = %d %s", code, body)
	}
}

func TestAddresses(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	code, v, body := f.do(t, http.MethodPut, `{"access": {"addresses": [{"cidr": " 192.0.2.20 ", "note": " agents "}, {"cidr": "fd00::5"}]}}`)
	want := []access.AddressGrant{{CIDR: "192.0.2.20/32", Note: "agents"}, {CIDR: "fd00::5/128"}}
	if code != http.StatusOK || !slices.Equal(v.Access.Addresses, want) {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if got := f.parsed(t).Access.Addresses; !slices.Equal(got, want) {
		t.Errorf("file addresses = %+v", got)
	}
	if got := f.parsed(t).Access.Devices; !slices.Equal(got, []access.DeviceGrant{laptop}) {
		t.Errorf("an address change touched devices: %+v", got)
	}
	for _, bad := range []string{
		`{"access": {"addresses": [{"cidr": "10.0.0.0/33"}]}}`,
		`{"access": {"addresses": [{"cidr": "laptop"}]}}`,
		`{"access": {"addresses": [{"cidr": "10.0.0.1"}, {"cidr": "10.0.0.1/32"}]}}`,
	} {
		if code, _, body := f.do(t, http.MethodPut, bad); code != http.StatusBadRequest || errorCode(t, body) != "invalid_access" {
			t.Errorf("PUT %s = %d %s", bad, code, body)
		}
	}
	if got := f.guard.Grants().Addresses; !slices.Equal(got, want) {
		t.Errorf("a refused update changed addresses to %+v", got)
	}
}

func TestLoopbackCallerMaySetAnyList(t *testing.T) {
	f := newFixture(t, options{})
	code, v, body := f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nworkerCNTRL", "proxy": true}]}}`)
	if code != http.StatusOK || !slices.Equal(v.Access.Devices, []access.DeviceGrant{{ID: "nworkerCNTRL", Proxy: true}}) {
		t.Fatalf("PUT = %d %s", code, body)
	}
	// taproot.yaml did not exist; it is created private.
	cfg := f.parsed(t)
	if !slices.Equal(cfg.Access.Devices, []access.DeviceGrant{{ID: "nworkerCNTRL", Proxy: true}}) {
		t.Errorf("file devices = %+v", cfg.Access.Devices)
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
	if code, v, body := f.do(t, http.MethodPut, `{"access": {"devices": []}}`); code != http.StatusOK || len(v.Access.Devices) != 0 {
		t.Fatalf("PUT clearing from loopback = %d %s", code, body)
	}
	for _, bad := range []string{`{"access": {"devices": [{"id": "", "proxy": true}]}}`, `{"access": {"devices": [{"id": "n1", "proxy": true}, {"id": "n1", "admin": true}]}}`} {
		if code, _, body := f.do(t, http.MethodPut, bad); code != http.StatusBadRequest || errorCode(t, body) != "invalid_access" {
			t.Errorf("PUT %s = %d %s", bad, code, body)
		}
	}
}

// The sweep applies what Reconcile finds live and to taproot.yaml, and a
// failed write leaves the guard as it was.
func TestSweepWritesForgottenAndRenamedDevices(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	worker := access.DeviceGrant{ID: "nworkerCNTRL", Name: "worker", Proxy: true}
	if code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nlaptopCNTRL", "name": "laptop", "proxy": true, "admin": true}, {"id": "nworkerCNTRL", "name": "worker", "proxy": true}]}}`); code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	renamed := laptop
	renamed.Name = "alice-laptop"
	f.access.tailnet = &access.Tailnet{}
	f.access.reconcile = func(access.Tailnet) (access.Grants, []access.DeviceGrant, bool) {
		return access.Grants{Devices: []access.DeviceGrant{renamed}, Addresses: f.guard.Grants().Addresses}, []access.DeviceGrant{worker}, true
	}
	if err := f.s.sweep(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := f.guard.Grants().Devices; !slices.Equal(got, []access.DeviceGrant{renamed}) {
		t.Errorf("guard devices = %+v", got)
	}
	if got := f.parsed(t).Access.Devices; !slices.Equal(got, []access.DeviceGrant{renamed}) {
		t.Errorf("file devices = %+v", got)
	}
	if !strings.Contains(f.file(t), "cidr: 10.0.0.0/8 # podman") {
		t.Errorf("sweep rewrote addresses:\n%s", f.file(t))
	}

	// Nothing to change writes nothing.
	f.access.reconcile = func(access.Tailnet) (access.Grants, []access.DeviceGrant, bool) {
		return f.guard.Grants(), nil, false
	}
	before := f.file(t)
	if err := f.s.sweep(t.Context()); err != nil || f.file(t) != before {
		t.Fatalf("no-op sweep: %v\n%s", err, f.file(t))
	}

	// A failed write rolls the guard back.
	f.access.reconcile = func(access.Tailnet) (access.Grants, []access.DeviceGrant, bool) {
		return access.Grants{Devices: []access.DeviceGrant{}}, []access.DeviceGrant{renamed}, true
	}
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.s.sweep(t.Context()); err == nil {
		t.Fatal("sweep with an unwritable file returned no error")
	}
	if got := f.guard.Grants().Devices; !slices.Equal(got, []access.DeviceGrant{renamed}) {
		t.Errorf("guard devices after a failed write = %+v", got)
	}
}

// Without tailscaled, a sweep reads nothing and changes nothing.
func TestSweepWithoutTailscaledChangesNothing(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	if err := f.s.sweep(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if f.file(t) != commentedYAML || !slices.Equal(f.guard.Grants().Devices, []access.DeviceGrant{laptop}) {
		t.Fatalf("sweep without tailscaled changed state:\n%s", f.file(t))
	}
}
func TestLaterFailureRollsBackEarlierChanges(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
	}{
		{"bad exclude path", `{"access": {"devices": [{"id": "nworkerCNTRL", "proxy": true}]}, "requestLogs": {"excludePaths": ["no-slash"]}}`, "invalid_retention"},
		{"bad device id", `{"backups": {"keep": 9}, "access": {"devices": [{"id": "bad id", "proxy": true}]}}`, "invalid_access"},
		{"bad daily-at", `{"access": {"devices": [{"id": "nworkerCNTRL", "proxy": true}]}, "requestLogs": {"maxDays": 3}, "backups": {"dailyAt": "25:00"}}`, "invalid_backups"},
		{"bad keep", `{"requestLogs": {"maxDays": 3}, "backups": {"keep": 0}}`, "invalid_backups"},
		{"bad poll interval", `{"access": {"devices": [{"id": "nworkerCNTRL", "proxy": true}]}, "backups": {"dailyAt": "off"}, "ingest": {"pollInterval": "soon"}}`, "invalid_poll_interval"},
		{"negative poll interval", `{"backups": {"keep": 9}, "ingest": {"pollInterval": "-5m"}}`, "invalid_poll_interval"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, options{yaml: commentedYAML})
			code, _, body := f.do(t, http.MethodPut, tc.body)
			if code != http.StatusBadRequest || errorCode(t, body) != tc.code {
				t.Fatalf("PUT = %d %s, want 400 %s", code, body, tc.code)
			}
			if got := f.guard.Grants().Devices; !slices.Equal(got, []access.DeviceGrant{laptop}) {
				t.Errorf("devices = %+v", got)
			}
			if ret := f.logs.Retention(); ret.MaxDays != 14 || len(ret.ExcludePaths) != 0 {
				t.Errorf("retention = %+v", ret)
			}
			if at, keep := f.backups.Schedule(); at != "00:00" || keep != 3 {
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
	code, _, body := f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nworkerCNTRL", "proxy": true}]}, "backups": {"keep": 2}, "requestLogs": {"maxDays": 2}}`)
	if code != http.StatusInternalServerError {
		t.Fatalf("PUT = %d %s, want 500", code, body)
	}
	if got := f.guard.Grants().Devices; !slices.Equal(got, []access.DeviceGrant{laptop}) {
		t.Errorf("devices = %+v", got)
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
	// A rewrite replaces the file, so it would carry the current time instead.
	written := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(f.path, written, written); err != nil {
		t.Fatal(err)
	}
	code, _, body := f.do(t, http.MethodPut, `{
		"access": {"devices": [{"id": "nlaptopCNTRL", "name": "laptop", "proxy": true, "admin": true}], "addresses": [{"cidr": "10.0.0.0/8"}]},
		"requestLogs": {"maxDays": 14, "maxBytes": 21474836480, "excludePaths": [], "keepUnrouted404": false},
		"backups": {"dailyAt": "00:00", "keep": 3},
		"ingest": {"pollInterval": "15m"}
	}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	after, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if f.file(t) != commentedYAML || !after.ModTime().Equal(written) {
		t.Errorf("no-op PUT rewrote the file:\n%s", f.file(t))
	}
}

func TestDisabledGuardRefusesAccessLists(t *testing.T) {
	off := false
	f := newFixture(t, options{accessEnabled: &off})
	_, v, _ := f.do(t, http.MethodGet, "")
	if v.Access.Enabled || v.Access.Devices == nil || v.Access.Addresses == nil {
		t.Errorf("access = %+v", v.Access)
	}
	for _, put := range []string{`{"access": {"devices": [{"id": "nworkerCNTRL", "proxy": true}]}}`, `{"access": {"addresses": [{"cidr": "10.0.0.1"}]}}`} {
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
	if err := writeYAML(path, []yamlEdit{{path: []string{"access", "devices"}, value: []access.DeviceGrant{{ID: "n1", Proxy: true}}}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var cfg fileConfig
	if err := decodeStrict(raw, &cfg); err != nil || !slices.Equal(cfg.Access.Devices, []access.DeviceGrant{{ID: "n1", Proxy: true}}) {
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

// A page that read the lists before another save cannot undo that save: a
// whole-list save from the older version is refused, and Allow sets only its
// own device.
func TestStaleAccessCopyCannotUndoAChange(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	f.caller = laptopCaller
	worker := access.DeviceGrant{ID: "nworkerCNTRL", Name: "worker", Proxy: true, Admin: true}
	put := func(body string) (int, view, string) { return f.do(t, http.MethodPut, body) }
	code, v, body := put(`{"access": {"devices": [{"id": "nlaptopCNTRL", "name": "laptop", "proxy": true, "admin": true}, {"id": "nworkerCNTRL", "name": "worker", "proxy": true, "admin": true}]}}`)
	if code != http.StatusOK || v.Access.Version == "" {
		t.Fatalf("grant worker Admin = %d %s", code, body)
	}
	stale := v.Access.Version

	// Another page revokes the worker's Admin.
	code, v, body = put(`{"access": {"version": "` + stale + `", "devices": [{"id": "nlaptopCNTRL", "name": "laptop", "proxy": true, "admin": true}, {"id": "nworkerCNTRL", "name": "worker", "proxy": true}]}}`)
	if code != http.StatusOK || v.Access.Version == stale {
		t.Fatalf("revoke = %d %s", code, body)
	}
	worker.Admin = false
	revoked := []access.DeviceGrant{laptop, worker}

	// The stale page saves its whole list.
	code, _, body = put(`{"access": {"version": "` + stale + `", "devices": [{"id": "nlaptopCNTRL", "name": "laptop", "proxy": true, "admin": true}, {"id": "nworkerCNTRL", "name": "worker", "proxy": true, "admin": true}], "addresses": []}}`)
	if code != http.StatusConflict || errorCode(t, body) != "access_changed" {
		t.Fatalf("stale save = %d %s, want 409 access_changed", code, body)
	}
	if got := f.guard.Grants().Devices; !slices.Equal(got, revoked) {
		t.Fatalf("a stale save changed devices to %+v", got)
	}

	// The stale page allows a request: only that device changes.
	code, v, body = put(`{"access": {"device": {"id": "nbobCNTRL", "name": "bob-pc", "label": "Bob", "proxy": true}}}`)
	want := append(slices.Clone(revoked), access.DeviceGrant{ID: "nbobCNTRL", Name: "bob-pc", Label: "Bob", Proxy: true})
	if code != http.StatusOK || !slices.Equal(v.Access.Devices, want) {
		t.Fatalf("allow one device = %d %s", code, body)
	}
	if got := f.parsed(t).Access.Devices; !slices.Equal(got, want) {
		t.Errorf("file devices = %+v", got)
	}
	// It replaces an existing entry in place, and taking every permission away removes it.
	code, v, body = put(`{"access": {"device": {"id": "nworkerCNTRL", "name": "worker", "proxy": false}}}`)
	if code != http.StatusOK || !slices.Equal(v.Access.Devices, []access.DeviceGrant{laptop, want[2]}) {
		t.Fatalf("device without permissions = %d %s", code, body)
	}
	// The caller's own Admin is still protected.
	if code, _, body := put(`{"access": {"device": {"id": "nlaptopCNTRL", "proxy": true}}}`); code != http.StatusConflict || errorCode(t, body) != "would_lock_out" {
		t.Errorf("device update dropping own Admin = %d %s", code, body)
	}
	if code, _, body := put(`{"access": {"devices": [], "device": {"id": "nbobCNTRL", "proxy": true}}}`); code != http.StatusBadRequest || errorCode(t, body) != "invalid_access" {
		t.Errorf("devices and device together = %d %s", code, body)
	}
}

// taproot.yaml never ignores a key, so PUT refuses a field it does not know
// instead of answering 200 and changing nothing.
func TestPutRefusesUnknownFields(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	before := f.file(t)
	for _, body := range []string{
		`{"backups": {"daily-at": "03:00", "keep": 5}}`,
		`{"backup": {"keep": 5}}`,
		`{"access": {"devices": [{"id": "nlaptopCNTRL", "proxy": true, "admin": true, "owner": "x"}]}}`,
	} {
		code, _, resp := f.do(t, http.MethodPut, body)
		if code != http.StatusBadRequest || errorCode(t, resp) != "invalid_body" {
			t.Fatalf("PUT %s = %d %s", body, code, resp)
		}
	}
	if f.file(t) != before {
		t.Fatalf("taproot.yaml changed:\n%s", f.file(t))
	}
	if _, keep := f.backups.Schedule(); keep != 3 {
		t.Fatalf("keep = %d after refused updates", keep)
	}
}

// After a restore replaced taproot.yaml, settings must not write the running
// state over it before the restart that applies it.
func TestPutRefusedWhileARestoreWaitsForRestart(t *testing.T) {
	f := newFixture(t, options{yaml: commentedYAML})
	e, err := f.backups.Create(context.Background(), backup.ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	code, _, body := f.do(t, http.MethodPut, `{"backups": {"keep": 5}}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d %s", code, body)
	}
	if _, err := f.backups.Restore(context.Background(), e.ID, backup.RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	restored := f.file(t)
	if !strings.Contains(restored, "keep: 3") {
		t.Fatalf("restored taproot.yaml = %s", restored)
	}

	code, _, body = f.do(t, http.MethodPut, `{"access": {"devices": [{"id": "nlaptopCNTRL", "proxy": true, "admin": true}, {"id": "nphoneCNTRL", "proxy": true}]}}`)
	if code != http.StatusConflict || errorCode(t, body) != "restart_pending" {
		t.Fatalf("PUT after restore = %d %s", code, body)
	}
	if got := f.access.Grants().Devices; len(got) != 1 {
		t.Fatalf("live devices changed: %+v", got)
	}
	if f.file(t) != restored {
		t.Fatalf("taproot.yaml changed after the restore:\n%s", f.file(t))
	}

	// The sweep leaves the restored device list alone too.
	renamed := laptop
	renamed.Name = "alice-laptop"
	f.access.tailnet = &access.Tailnet{}
	f.access.reconcile = func(access.Tailnet) (access.Grants, []access.DeviceGrant, bool) {
		return access.Grants{Devices: []access.DeviceGrant{renamed}}, nil, true
	}
	if err := f.s.sweep(t.Context()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if f.file(t) != restored || !slices.Equal(f.guard.Grants().Devices, []access.DeviceGrant{laptop}) {
		t.Fatalf("the sweep changed state after the restore:\n%s", f.file(t))
	}
}
