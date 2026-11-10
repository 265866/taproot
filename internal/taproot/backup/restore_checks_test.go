package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
)

// checkNextStart makes the fixture check restored taproot.yaml files the way
// the server does: refuse reports why the next start would refuse raw, and
// admins are the devices it would give Admin.
func (f *fixture) checkNextStart(refuse func(raw []byte) error, admins ...string) {
	f.b.paths.CheckTaproot = func(raw []byte) (NextStart, error) {
		if refuse != nil {
			if err := refuse(raw); err != nil {
				return NextStart{}, err
			}
		}
		return NextStart{DataDir: f.paths.DataDir, DBPath: f.db.Path(), AccessEnabled: true, Admins: admins}, nil
	}
}

func (f *fixture) restore(t *testing.T, id, body string) (int, apiError, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/backups/"+id+"/restore", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var apiErr apiError
	if resp.StatusCode != http.StatusOK {
		_ = json.Unmarshal(raw, &apiErr)
	}
	return resp.StatusCode, apiErr, raw
}

// assertUnchanged checks that a refused restore changed nothing.
func (f *fixture) assertUnchanged(t *testing.T, config, taproot string, backups int) {
	t.Helper()
	if got := readFile(t, f.paths.ConfigFile); got != config {
		t.Errorf("config.yaml = %q, want %q", got, config)
	}
	if got := readFile(t, f.paths.TaprootFile); got != taproot {
		t.Errorf("taproot.yaml = %q, want %q", got, taproot)
	}
	if got := f.list(t); len(got) != backups {
		t.Errorf("refused restore left %d backups, want %d: %+v", len(got), backups, got)
	}
	for _, p := range []string{
		filepath.Join(f.paths.DataDir, pendingMarkerName),
		f.db.Path() + restorePendingSuffix,
		f.db.Path() + restoreTmpSuffix,
	} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after a refused restore: %v", filepath.Base(p), err)
		}
	}
	select {
	case <-f.restart:
		t.Error("refused restore requested a restart")
	default:
	}
}

func TestRestoreRefusesTaprootYAMLTheNextStartRefuses(t *testing.T) {
	f := newFixture(t, Config{})
	writeFile(t, f.paths.TaprootFile, "backups:\n  keep-old-name: 3\n")
	e := f.create(t)
	writeFile(t, f.paths.TaprootFile, origTaproot)
	writeFile(t, f.paths.ConfigFile, "port: 9999\n")
	f.checkNextStart(func(raw []byte) error {
		if bytes.Contains(raw, []byte("keep-old-name")) {
			return errors.New("line 2: field keep-old-name not found in type backup.Config")
		}
		return nil
	})
	f.clock = f.clock.Add(time.Hour)

	code, apiErr, body := f.restore(t, e.ID, "")
	if code != http.StatusConflict || apiErr.Error.Code != "incompatible_backup" || !strings.Contains(apiErr.Error.Message, "keep-old-name") {
		t.Fatalf("restore: %d %s", code, body)
	}
	f.assertUnchanged(t, "port: 9999\n", origTaproot, 1)
}

// A backup taken before the data dir, the database, or the auth dir moved
// keeps pointing the next start at where this server keeps them now.
func TestRestoreKeepsCurrentPaths(t *testing.T) {
	f := newFixture(t, Config{})
	writeFile(t, f.paths.TaprootFile, "# backed up\ndata-dir: old-data # old\nstore:\n  path: old.db\nbackups:\n  keep: 3\n")
	writeFile(t, f.paths.ConfigFile, "port: 8317\nauth-dir: old-auths\n")
	e := f.create(t)
	writeFile(t, f.paths.TaprootFile, "data-dir: taproot\nbackups:\n  keep: 5\n")
	writeFile(t, f.paths.ConfigFile, "port: 8317\nauth-dir: auths\n")
	var checked string
	f.checkNextStart(func(raw []byte) error { checked = string(raw); return nil })
	f.clock = f.clock.Add(time.Hour)

	if code, _, body := f.restore(t, e.ID, ""); code != http.StatusOK {
		t.Fatalf("restore: %d %s", code, body)
	}
	tp := readFile(t, f.paths.TaprootFile)
	if tp != checked {
		t.Errorf("the restore wrote a taproot.yaml other than the one it checked:\n%s\nchecked:\n%s", tp, checked)
	}
	for _, want := range []string{"# backed up", "data-dir: taproot # old", "keep: 3"} {
		if !strings.Contains(tp, want) {
			t.Errorf("taproot.yaml lacks %q:\n%s", want, tp)
		}
	}
	if strings.Contains(tp, "old.db") || strings.Contains(tp, "old-data") {
		t.Errorf("taproot.yaml keeps the backup's paths:\n%s", tp)
	}
	if got := readFile(t, f.paths.ConfigFile); got != "port: 8317\nauth-dir: auths\n" {
		t.Errorf("config.yaml = %q", got)
	}
	if got := readFile(t, filepath.Join(f.paths.DataDir, pendingMarkerName)); !strings.Contains(got, `"db":"taproot.db"`) {
		t.Errorf("marker %s, want the database relative to the data dir", got)
	}
}

func TestKeepPathKeysLeavesMatchingFilesAlone(t *testing.T) {
	current := filepath.Join(t.TempDir(), "taproot.yaml")
	restored := []byte("data-dir: same   # odd spacing kept\nbackups: {keep: 3}\n")
	writeFile(t, current, "data-dir: same\n")
	got, err := keepPathKeys(restored, current, pathKeys[taprootEntry])
	if err != nil || !bytes.Equal(got, restored) {
		t.Fatalf("keepPathKeys = %q, %v; want the file unchanged", got, err)
	}
	// A key the current file leaves out is removed, so its default applies.
	writeFile(t, current, "backups:\n  keep: 1\n")
	got, err = keepPathKeys([]byte("data-dir: old\nstore:\n  path: x.db\n"), current, pathKeys[taprootEntry])
	if err != nil || strings.Contains(string(got), "data-dir") || strings.Contains(string(got), "x.db") {
		t.Fatalf("keepPathKeys = %q, %v; want the paths removed", got, err)
	}
	// A missing current file names no paths either.
	got, err = keepPathKeys([]byte("data-dir: old\n"), filepath.Join(t.TempDir(), "absent.yaml"), pathKeys[taprootEntry])
	if err != nil || strings.Contains(string(got), "data-dir") {
		t.Fatalf("keepPathKeys = %q, %v", got, err)
	}
}

// A restore marker written inside the data dir still finds its database
// after the data dir is mounted at another path.
func TestPendingRestoreFollowsAMovedDataDir(t *testing.T) {
	f := newFixture(t, Config{})
	e := f.create(t)
	if _, err := f.db.Exec(`UPDATE probe SET v = 'after'`); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Hour)
	if _, err := f.b.Restore(context.Background(), e.ID, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(f.root, "moved")
	if err := os.Rename(f.paths.DataDir, moved); err != nil {
		t.Fatal(err)
	}
	f.paths.DataDir = moved
	if err := ApplyPendingRestore(moved); err != nil {
		t.Fatal(err)
	}
	f.openDB(t)
	if got := f.probe(t); got != "before" {
		t.Fatalf("database after the swap = %q, want before", got)
	}
}

// A restore whose taproot.yaml keeps data elsewhere than the running server,
// as after an edit that has not been restarted yet, would stage the database
// where the next start does not look.
func TestRestoreRefusesADataDirTheServerDoesNotUse(t *testing.T) {
	f := newFixture(t, Config{})
	e := f.create(t)
	f.b.paths.CheckTaproot = func([]byte) (NextStart, error) {
		return NextStart{DataDir: filepath.Join(f.root, "elsewhere"), DBPath: filepath.Join(f.root, "elsewhere", "taproot.db"), AccessEnabled: true}, nil
	}
	f.clock = f.clock.Add(time.Hour)
	code, apiErr, body := f.restore(t, e.ID, "")
	if code != http.StatusConflict || apiErr.Error.Code != "incompatible_backup" || !strings.Contains(apiErr.Error.Message, "restart taproot") {
		t.Fatalf("restore: %d %s", code, body)
	}
	f.assertUnchanged(t, origConfig, origTaproot, 1)
}

func TestRestoreRefusesToTakeTheCallersAdmin(t *testing.T) {
	const me, other = "nphoneCNTRL", "nlaptopCNTRL"
	cases := []struct {
		name          string
		caller        access.Identity
		admins        []string
		guardDisabled bool
		body          string
		want          int
	}{
		{name: "caller keeps Admin", caller: access.Identity{Via: access.ViaTailnet, NodeID: me}, admins: []string{other, me}, want: http.StatusOK},
		{name: "caller loses Admin", caller: access.Identity{Via: access.ViaTailnet, NodeID: me}, admins: []string{other}, want: http.StatusConflict},
		{name: "guard turned off", caller: access.Identity{Via: access.ViaTailnet, NodeID: me}, admins: []string{me}, guardDisabled: true, want: http.StatusConflict},
		{name: "caller allows it", caller: access.Identity{Via: access.ViaTailnet, NodeID: me}, admins: []string{other}, body: `{"allowLockout": true}`, want: http.StatusOK},
		{name: "loopback caller", caller: access.Identity{Via: access.ViaLoopback}, want: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, Config{})
			e := f.create(t)
			writeFile(t, f.paths.ConfigFile, "port: 9999\n")
			f.caller = tc.caller
			f.b.paths.CheckTaproot = func([]byte) (NextStart, error) {
				return NextStart{DataDir: f.paths.DataDir, DBPath: f.db.Path(), AccessEnabled: !tc.guardDisabled, Admins: tc.admins}, nil
			}
			f.clock = f.clock.Add(time.Hour)
			code, apiErr, body := f.restore(t, e.ID, tc.body)
			if code != tc.want {
				t.Fatalf("restore: %d %s", code, body)
			}
			if code == http.StatusConflict {
				if apiErr.Error.Code != "would_lock_out" {
					t.Fatalf("restore: %d %s", code, body)
				}
				f.assertUnchanged(t, "port: 9999\n", origTaproot, 1)
			}
		})
	}
}

func TestRestoreRefusesUnknownBodyFields(t *testing.T) {
	f := newFixture(t, Config{})
	e := f.create(t)
	if code, apiErr, body := f.restore(t, e.ID, `{"force": true}`); code != http.StatusBadRequest || apiErr.Error.Code != "invalid_body" {
		t.Fatalf("restore: %d %s", code, body)
	}
}

// Until the process restarts, the restored files wait for their database:
// another restore, a backup, or a taproot.yaml write would mix them with the
// running state.
func TestRestoreRefusesChangesUntilRestart(t *testing.T) {
	f := newFixture(t, Config{})
	f.b.restart = nil
	first := f.create(t)
	f.clock = f.clock.Add(time.Minute)
	second := f.create(t)
	f.clock = f.clock.Add(time.Minute)
	if code, _, body := f.restore(t, first.ID, ""); code != http.StatusOK {
		t.Fatalf("restore: %d %s", code, body)
	}
	restored := readFile(t, f.paths.TaprootFile)
	marker := readFile(t, filepath.Join(f.paths.DataDir, pendingMarkerName))
	f.clock = f.clock.Add(time.Minute)

	if code, apiErr, body := f.restore(t, second.ID, ""); code != http.StatusConflict || apiErr.Error.Code != "restart_pending" {
		t.Fatalf("second restore: %d %s", code, body)
	}
	var apiErr apiError
	if code, body := f.do(t, http.MethodPost, "/api/backups", &apiErr); code != http.StatusConflict || apiErr.Error.Code != "restart_pending" {
		t.Fatalf("backup after restore: %d %s", code, body)
	}
	wrote := false
	if err := f.b.WriteConfig(func() error { wrote = true; return nil }); !errors.Is(err, ErrRestartPending) || wrote {
		t.Fatalf("WriteConfig after restore = %v, ran = %v", err, wrote)
	}
	if got := readFile(t, f.paths.TaprootFile); got != restored {
		t.Errorf("taproot.yaml changed after the restore: %q", got)
	}
	if got := readFile(t, filepath.Join(f.paths.DataDir, pendingMarkerName)); got != marker {
		t.Errorf("restore marker changed: %s, want %s", got, marker)
	}
	if got := len(f.list(t)); got != 3 {
		t.Errorf("%d backups, want the two and one pre-restore", got)
	}
}

// Upstream loads every *.json file under the auth dir, through symlinks
// too, so a backup holds them all and a restore puts each one back.
func TestBackupHoldsNestedAndSymlinkedCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}
	f := newFixture(t, Config{})
	secret := filepath.Join(f.root, "secrets", "linked.json")
	writeFile(t, secret, `{"type":"claude","token":"linked"}`)
	if err := os.Symlink(secret, filepath.Join(f.paths.AuthDir, "linked.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.root, "absent.json"), filepath.Join(f.paths.AuthDir, "dangling.json")); err != nil {
		t.Fatal(err)
	}
	e := f.create(t)
	_, raw := f.do(t, http.MethodGet, "/api/backups/"+e.ID+"/download", nil)
	_, members := untar(t, raw)
	for _, name := range []string{"auth/linked.json", "auth/nested/codex.json"} {
		if members[name] == nil {
			t.Errorf("backup lacks %s", name)
		}
	}
	if string(members["auth/linked.json"]) != `{"type":"claude","token":"linked"}` {
		t.Errorf("auth/linked.json = %q, want the link target's content", members["auth/linked.json"])
	}
	if _, ok := members["auth/dangling.json"]; ok {
		t.Error("backup holds a dangling link")
	}

	writeFile(t, secret, `{"type":"claude","token":"rotated"}`)
	writeFile(t, filepath.Join(f.paths.AuthDir, "nested", "codex.json"), `{"type":"codex","token":"rotated"}`)
	writeFile(t, filepath.Join(f.paths.AuthDir, "nested", "added.json"), `{"type":"codex","token":"added"}`)
	f.clock = f.clock.Add(time.Hour)
	if _, err := f.b.Restore(context.Background(), e.ID, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(f.paths.AuthDir, "linked.json")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlinked credential is no longer a link: %v", err)
	}
	if got := readFile(t, secret); got != `{"type":"claude","token":"linked"}` {
		t.Errorf("link target = %q, want the backed-up content", got)
	}
	if got := readFile(t, filepath.Join(f.paths.AuthDir, "nested", "codex.json")); got != origAuth["nested/codex.json"] {
		t.Errorf("nested credential = %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.paths.AuthDir, "nested", "added.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("nested credential added after the backup survived: %v", err)
	}
}

// A backup from before subdirectories were backed up says nothing about
// them, so restoring it leaves them alone.
func TestRestoreOfOlderBackupLeavesNestedCredentials(t *testing.T) {
	f := newFixture(t, Config{})
	e := f.create(t)
	rewriteArchive(t, filepath.Join(f.b.dir, e.ID+archiveExt), func(m *manifest, members map[string][]byte) []string {
		m.AuthTree = false
		files := m.Files[:0]
		for _, mf := range m.Files {
			if mf.Path != "auth/nested/codex.json" {
				files = append(files, mf)
			}
		}
		m.Files = files
		delete(members, "auth/nested/codex.json")
		return manifestOrder(m)
	})
	f.clock = f.clock.Add(time.Hour)
	if _, err := f.b.Restore(context.Background(), e.ID, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(f.paths.AuthDir, "nested", "codex.json")); got != origAuth["nested/codex.json"] {
		t.Errorf("nested credential = %q after restoring an older backup", got)
	}
}

func TestBackupRefusesAFileARestoreCannotRead(t *testing.T) {
	f := newFixture(t, Config{})
	writeFile(t, filepath.Join(f.paths.AuthDir, "big.json"), strings.Repeat("x", maxSmallFileBytes+1))
	var apiErr apiError
	code, body := f.do(t, http.MethodPost, "/api/backups", &apiErr)
	if code != http.StatusInternalServerError || !strings.Contains(apiErr.Error.Message, "big.json") {
		t.Fatalf("create: %d %s", code, body)
	}
	if got := f.list(t); len(got) != 0 {
		t.Fatalf("a backup no restore can read was written: %+v", got)
	}
}

// Listing reads each archive's manifest once: reading one allocates the
// decoder's whole window, about 8 MB.
func TestListReadsEachManifestOnce(t *testing.T) {
	f := newFixture(t, Config{})
	// Enough data that the archive's zstd window is several megabytes.
	if _, err := f.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n WHERE x < 20000)
		INSERT INTO probe (v) SELECT hex(randomblob(64)) FROM n`); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		f.create(t)
		f.clock = f.clock.Add(time.Minute)
	}
	if _, err := f.b.List(); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	entries, err := f.b.List()
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Files != 6 {
		t.Fatalf("entries = %+v", entries)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("listing 3 archives allocated %d bytes", allocated)
	}
}
