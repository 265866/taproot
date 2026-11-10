package taproot

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/265866/taproot/internal/config"
	"github.com/265866/taproot/internal/taproot/backup"
	"github.com/265866/taproot/internal/taproot/store"
)

// restoreFixture is a server's files on disk, with its backups module open
// the way setup opens it, so a test can restore and then start again.
type restoreFixture struct {
	dir, configPath, tpPath, authDir string
	db                               *store.DB
	backups                          *backup.Backups
}

func newRestoreFixture(t *testing.T, tp string) *restoreFixture {
	t.Helper()
	t.Setenv("MANAGEMENT_PASSWORD", "test-secret")
	dir := t.TempDir()
	f := &restoreFixture{
		dir: dir, configPath: filepath.Join(dir, "config.yaml"), tpPath: filepath.Join(dir, "taproot.yaml"),
		authDir: filepath.Join(dir, "auth"),
	}
	writeTestFile(t, f.configPath, "port: 8317\nauth-dir: "+filepath.ToSlash(f.authDir)+"\n")
	writeTestFile(t, filepath.Join(f.authDir, "claude-x.json"), `{"type":"claude"}`)
	writeTestFile(t, f.tpPath, tp)
	f.open(t)
	return f
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// open opens the store and backups as setup would with the current files.
func (f *restoreFixture) open(t *testing.T) {
	t.Helper()
	tp, err := LoadConfig(f.tpPath)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := DataDir(tp, f.configPath)
	if f.db, err = store.Open(context.Background(), tp.Store, dataDir); err != nil {
		t.Fatal(err)
	}
	db := f.db
	t.Cleanup(func() { _ = db.Close() })
	f.backups, err = backup.New(tp.Backups, f.db, backup.Paths{
		ConfigFile: f.configPath, TaprootFile: f.tpPath, AuthDir: f.authDir, DataDir: dataDir,
		CheckTaproot: restoreCheck(f.tpPath, f.configPath),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (f *restoreFixture) start(t *testing.T) error {
	t.Helper()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{AuthDir: f.authDir}
	cfg.Port = 8317
	_, stop, err := setup(context.Background(), cfg, f.configPath)
	if stop != nil {
		stop()
	}
	return err
}

// A backup whose taproot.yaml this release refuses, such as one from before
// a key was renamed or before overlapping addresses became an error, is
// refused before anything changes, so the server still starts.
func TestRestoreRefusesTaprootYAMLThatStopsStartup(t *testing.T) {
	const current = "access:\n  enabled: false\n"
	for name, archived := range map[string]string{
		"renamed key":                  "access:\n  enabled: false\nbackups:\n  keep-old-name: 3\n",
		"removed access key":           "access:\n  enabled: false\n  allow-logins: [alice@github]\n",
		"address covering Tailscale":   "access:\n  enabled: false\n  addresses:\n    - cidr: 0.0.0.0/0\n",
		"device listed twice":          "access:\n  enabled: false\n  devices:\n    - {id: nlaptopCNTRL, admin: true}\n    - {id: nlaptopCNTRL, proxy: true}\n",
		"not yaml":                     "access: [\n",
		"top level is not a mapping":   "- access\n",
		"wrong type for a known key":   "backups:\n  keep: many\n",
		"unknown top-level section":    "access:\n  enabled: false\nbackup:\n  keep: 3\n",
		"removed key beside good ones": "access:\n  enabled: false\n  trusted-cidrs: [192.0.2.20/32]\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newRestoreFixture(t, current)
			writeTestFile(t, f.tpPath, archived)
			e, err := f.backups.Create(context.Background(), backup.ReasonManual)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, f.tpPath, current)
			_, err = f.backups.Restore(context.Background(), e.ID, backup.RestoreOptions{})
			if err == nil || !strings.Contains(err.Error(), "incompatible backup") {
				t.Fatalf("restore = %v, want it refused as incompatible", err)
			}
			if raw, _ := os.ReadFile(f.tpPath); string(raw) != current {
				t.Fatalf("taproot.yaml = %q after a refused restore", raw)
			}
			if err := f.start(t); err != nil {
				t.Fatalf("next start: %v", err)
			}
		})
	}
}

// A backup taken before the data dir moved restores its database into the
// data dir the server uses now.
func TestRestoreAcrossDataDirChange(t *testing.T) {
	f := newRestoreFixture(t, "access:\n  enabled: false\ndata-dir: old\n")
	if _, err := f.db.Exec(`PRAGMA user_version = 42`); err != nil {
		t.Fatal(err)
	}
	e, err := f.backups.Create(context.Background(), backup.ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.dir, "old"), filepath.Join(f.dir, "new")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, f.tpPath, "access:\n  enabled: false\ndata-dir: new\n")
	f.open(t)
	if _, err := f.db.Exec(`PRAGMA user_version = 7`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.backups.Restore(context.Background(), e.ID, backup.RestoreOptions{}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := f.start(t); err != nil {
		t.Fatalf("next start: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "old")); !os.IsNotExist(err) {
		t.Errorf("the next start used the backup's data dir: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(f.dir, "new", "taproot.db"))+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 42 {
		t.Fatalf("database user_version = %d, want the backup's 42", version)
	}
}
