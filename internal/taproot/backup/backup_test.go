package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
)

func init() {
	store.Register(store.Migration{Module: "backup_test", Version: 1, SQL: `CREATE TABLE probe (v TEXT NOT NULL)`})
	gin.SetMode(gin.TestMode)
}

const (
	origConfig  = "port: 8317\nauth-dir: auths\n"
	origTaproot = "backups:\n  keep: 3\n"
)

var origAuth = map[string]string{
	"claude-a.json": `{"type":"claude","token":"a"}`,
	"claude-b.json": `{"type":"claude","token":"b"}`,
}

// otherAuthDirFiles are files upstream does not load as credentials. Backups
// leave them out and restores leave them alone.
var otherAuthDirFiles = map[string]string{
	"nested/codex.json": `{"type":"codex","token":"c"}`,
	"logs/main.log":     "app log line\n",
	"notes.txt":         "not a credential\n",
}

type fixture struct {
	root    string
	paths   Paths
	db      *store.DB
	b       *Backups
	srv     *httptest.Server
	restart chan struct{}
	clock   time.Time
}

func newFixture(t *testing.T, cfg Config) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{
		root: root,
		paths: Paths{
			ConfigFile:  filepath.Join(root, "config.yaml"),
			TaprootFile: filepath.Join(root, "taproot.yaml"),
			AuthDir:     filepath.Join(root, "auths"),
			DataDir:     filepath.Join(root, "taproot"),
		},
		restart: make(chan struct{}, 4),
		clock:   time.Date(2026, 11, 2, 3, 13, 0, 0, time.UTC),
	}
	writeFile(t, f.paths.ConfigFile, origConfig)
	writeFile(t, f.paths.TaprootFile, origTaproot)
	for _, files := range []map[string]string{origAuth, otherAuthDirFiles} {
		for rel, body := range files {
			writeFile(t, filepath.Join(f.paths.AuthDir, filepath.FromSlash(rel)), body)
		}
	}
	f.openDB(t)
	if _, err := f.db.Exec(`INSERT INTO probe (v) VALUES ('before')`); err != nil {
		t.Fatal(err)
	}
	b, err := New(cfg, f.db, f.paths)
	if err != nil {
		t.Fatal(err)
	}
	b.now = func() time.Time { return f.clock }
	b.restart = func() error { f.restart <- struct{}{}; return nil }
	f.b = b
	engine := gin.New()
	b.Routes(engine.Group("/api"))
	f.srv = httptest.NewServer(engine)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) openDB(t *testing.T) {
	t.Helper()
	db, err := store.Open(context.Background(), store.Config{}, f.paths.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	f.db = db
	t.Cleanup(func() { _ = db.Close() })
}

func (f *fixture) probe(t *testing.T) string {
	t.Helper()
	var v string
	if err := f.db.QueryRow(`SELECT v FROM probe`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (f *fixture) do(t *testing.T, method, path string, out any) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, body, err)
		}
	}
	return resp.StatusCode, body
}

func (f *fixture) list(t *testing.T) []Entry {
	t.Helper()
	var resp struct{ Backups []Entry }
	if code, body := f.do(t, http.MethodGet, "/api/backups", &resp); code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	return resp.Backups
}

func (f *fixture) create(t *testing.T) Entry {
	t.Helper()
	var e Entry
	if code, body := f.do(t, http.MethodPost, "/api/backups", &e); code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	return e
}

type apiError struct {
	Error struct{ Code, Message string }
}

// untar decompresses an archive and returns its members in order.
func untar(t *testing.T, raw []byte) ([]string, map[string][]byte) {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var names []string
	members := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
		members[hdr.Name] = body
	}
	return names, members
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestCreateListDownloadRestoreRoundTrip(t *testing.T) {
	f := newFixture(t, Config{})

	e := f.create(t)
	if e.ID != "20261102T031300Z-manual" || e.Reason != ReasonManual || e.Files != 5 || e.Bytes <= 0 {
		t.Fatalf("unexpected entry %+v", e)
	}
	if got := f.list(t); len(got) != 1 || got[0] != e {
		t.Fatalf("list = %+v, want [%+v]", got, e)
	}

	code, archive := f.do(t, http.MethodGet, "/api/backups/"+e.ID+"/download", nil)
	if code != http.StatusOK || int64(len(archive)) != e.Bytes {
		t.Fatalf("download: %d, %d bytes", code, len(archive))
	}
	names, members := untar(t, archive)
	if names[0] != manifestEntry {
		t.Fatalf("first member %q, want manifest", names[0])
	}
	var m manifest
	if err := json.Unmarshal(members[manifestEntry], &m); err != nil {
		t.Fatal(err)
	}
	wantSums := map[string]string{"config.yaml": sha(origConfig), "taproot.yaml": sha(origTaproot)}
	for rel, body := range origAuth {
		wantSums["auth/"+rel] = sha(body)
	}
	for _, mf := range m.Files {
		if want, ok := wantSums[mf.Path]; ok && mf.SHA256 != want {
			t.Errorf("%s sum %s, want %s", mf.Path, mf.SHA256, want)
		}
		got := sha256.Sum256(members[mf.Path])
		if hex.EncodeToString(got[:]) != mf.SHA256 {
			t.Errorf("%s does not match its manifest sum", mf.Path)
		}
		delete(wantSums, mf.Path)
	}
	if len(wantSums) != 0 || len(members[dbEntry]) == 0 {
		t.Fatalf("archive missing %v or the database", wantSums)
	}
	for rel := range otherAuthDirFiles {
		if _, ok := members["auth/"+rel]; ok {
			t.Errorf("archive holds non-credential auth dir file %s", rel)
		}
	}

	// Drift from the backed-up state in every covered place.
	writeFile(t, f.paths.ConfigFile, "port: 9999\n")
	if err := os.Remove(filepath.Join(f.paths.AuthDir, "claude-b.json")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.paths.AuthDir, "added.json"), `{"token":"new"}`)
	if _, err := f.db.Exec(`UPDATE probe SET v = 'after'`); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Hour)

	var rr restoreResponse
	if code, body := f.do(t, http.MethodPost, "/api/backups/"+e.ID+"/restore", &rr); code != http.StatusOK {
		t.Fatalf("restore: %d %s", code, body)
	}
	if !rr.Restored || rr.PreRestoreBackupID != "20261102T041300Z-pre-restore" || rr.Restart != "scheduled" {
		t.Fatalf("restore response %+v", rr)
	}
	select {
	case <-f.restart:
	case <-time.After(5 * time.Second):
		t.Fatal("restore did not request a restart")
	}

	if got := readFile(t, f.paths.ConfigFile); got != origConfig {
		t.Errorf("config.yaml = %q, want original", got)
	}
	for rel, body := range origAuth {
		if got := readFile(t, filepath.Join(f.paths.AuthDir, filepath.FromSlash(rel))); got != body {
			t.Errorf("auth %s = %q, want %q", rel, got, body)
		}
	}
	if _, err := os.Stat(filepath.Join(f.paths.AuthDir, "added.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("auth file added after the backup survived restore: %v", err)
	}
	for rel, body := range otherAuthDirFiles {
		if got := readFile(t, filepath.Join(f.paths.AuthDir, filepath.FromSlash(rel))); got != body {
			t.Errorf("restore changed non-credential file %s: %q", rel, got)
		}
	}

	// The pre-restore backup holds the drifted state.
	_, pre := f.do(t, http.MethodGet, "/api/backups/"+rr.PreRestoreBackupID+"/download", nil)
	_, preMembers := untar(t, pre)
	if string(preMembers["config.yaml"]) != "port: 9999\n" || preMembers["auth/added.json"] == nil {
		t.Errorf("pre-restore backup does not hold the state it replaced")
	}

	// The running database is untouched until the next start swaps it.
	if got := f.probe(t); got != "after" {
		t.Fatalf("live database changed before restart: %q", got)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPendingRestore(f.paths.DataDir); err != nil {
		t.Fatal(err)
	}
	f.openDB(t)
	if got := f.probe(t); got != "before" {
		t.Fatalf("database after swap = %q, want before", got)
	}
	if _, err := os.Stat(filepath.Join(f.paths.DataDir, pendingMarkerName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("restore marker left behind: %v", err)
	}
	if err := ApplyPendingRestore(f.paths.DataDir); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if got := f.probe(t); got != "before" {
		t.Fatalf("database after second apply = %q", got)
	}
}

func TestApplyPendingRestoreResumesAfterInterruptedSwap(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "taproot.db")
	writeFile(t, dbPath, "restored")
	marker, _ := json.Marshal(pendingRestore{DB: dbPath, BackupID: "x"})
	writeFile(t, filepath.Join(dataDir, pendingMarkerName), string(marker))

	// The rename finished but the marker was not removed.
	if err := ApplyPendingRestore(dataDir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dbPath); got != "restored" {
		t.Fatalf("db = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, pendingMarkerName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker not removed: %v", err)
	}

	// A staged file replaces the database and drops the old WAL and SHM.
	writeFile(t, dbPath+restorePendingSuffix, "newer")
	writeFile(t, dbPath+"-wal", "stale wal")
	writeFile(t, dbPath+"-shm", "stale shm")
	writeFile(t, filepath.Join(dataDir, pendingMarkerName), string(marker))
	if err := ApplyPendingRestore(dataDir); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dbPath); got != "newer" {
		t.Fatalf("db = %q", got)
	}
	for _, suffix := range []string{"-wal", "-shm", restorePendingSuffix} {
		if _, err := os.Stat(dbPath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left behind: %v", suffix, err)
		}
	}
}

func TestRetentionKeepsNewest(t *testing.T) {
	f := newFixture(t, Config{Keep: 3})
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, f.create(t).ID)
		f.clock = f.clock.Add(24 * time.Hour)
	}
	got := f.list(t)
	if len(got) != 3 {
		t.Fatalf("kept %d backups, want 3", len(got))
	}
	for i, e := range got {
		if want := ids[4-i]; e.ID != want {
			t.Errorf("list[%d] = %s, want %s", i, e.ID, want)
		}
	}
	for _, id := range ids[:2] {
		if _, err := os.Stat(filepath.Join(f.b.dir, id+archiveExt)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s not pruned: %v", id, err)
		}
	}
}

func TestSameSecondBackupsGetDistinctIDs(t *testing.T) {
	f := newFixture(t, Config{})
	a, b, c := f.create(t), f.create(t), f.create(t)
	if a.ID == b.ID || b.ID != "20261102T031300Z.2-manual" || c.ID != "20261102T031300Z.3-manual" {
		t.Fatalf("ids %s %s %s", a.ID, b.ID, c.ID)
	}
	got := f.list(t)
	if got[0].ID != c.ID || got[2].ID != a.ID {
		t.Fatalf("same-second ordering wrong: %+v", got)
	}
}

// rewriteArchive replaces a backup with one built from edited members.
func rewriteArchive(t *testing.T, path string, edit func(m *manifest, members map[string][]byte) []string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, members := untar(t, raw)
	var m manifest
	if err := json.Unmarshal(members[manifestEntry], &m); err != nil {
		t.Fatal(err)
	}
	order := edit(&m, members)
	mj, _ := json.Marshal(m)
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, name := range append([]string{manifestEntry}, order...) {
		body := members[name]
		if name == manifestEntry {
			body = mj
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Size: int64(len(body)), Mode: 0o600}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func manifestOrder(m *manifest) []string {
	var names []string
	for _, mf := range m.Files {
		names = append(names, mf.Path)
	}
	return names
}

func TestCorruptArchiveRejectedWithoutChanges(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"truncated": func(t *testing.T, path string) {
			raw := readFile(t, path)
			writeFile(t, path, raw[:len(raw)/2])
		},
		"not zstd": func(t *testing.T, path string) {
			writeFile(t, path, "this is not an archive")
		},
		"tampered auth file": func(t *testing.T, path string) {
			rewriteArchive(t, path, func(m *manifest, members map[string][]byte) []string {
				members["auth/claude-a.json"] = []byte(`{"token":"evil"}`)
				for i := range m.Files {
					if m.Files[i].Path == "auth/claude-a.json" {
						m.Files[i].Size = int64(len(members["auth/claude-a.json"]))
					}
				}
				return manifestOrder(m)
			})
		},
		"member missing": func(t *testing.T, path string) {
			rewriteArchive(t, path, func(m *manifest, _ map[string][]byte) []string {
				order := manifestOrder(m)
				return order[:len(order)-1]
			})
		},
		"path traversal": func(t *testing.T, path string) {
			rewriteArchive(t, path, func(m *manifest, members map[string][]byte) []string {
				members["auth/../../escape.json"] = []byte("x")
				m.Files = append(m.Files, manifestFile{Path: "auth/../../escape.json", Size: 1, SHA256: sha("x")})
				return manifestOrder(m)
			})
		},
		"unlisted member": func(t *testing.T, path string) {
			rewriteArchive(t, path, func(m *manifest, members map[string][]byte) []string {
				members["auth/extra.json"] = []byte("x")
				return append(manifestOrder(m), "auth/extra.json")
			})
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, Config{})
			e := f.create(t)
			corrupt(t, filepath.Join(f.b.dir, e.ID+archiveExt))
			writeFile(t, f.paths.ConfigFile, "port: 9999\n")
			f.clock = f.clock.Add(time.Hour)

			var apiErr apiError
			code, body := f.do(t, http.MethodPost, "/api/backups/"+e.ID+"/restore", &apiErr)
			if code != http.StatusUnprocessableEntity || apiErr.Error.Code != "corrupt_backup" {
				t.Fatalf("restore: %d %s", code, body)
			}
			if got := readFile(t, f.paths.ConfigFile); got != "port: 9999\n" {
				t.Errorf("config changed by a rejected restore: %q", got)
			}
			if got := f.list(t); len(got) != 1 {
				t.Errorf("rejected restore created backups: %+v", got)
			}
			for _, p := range []string{
				filepath.Join(f.paths.DataDir, pendingMarkerName),
				f.db.Path() + restorePendingSuffix,
				f.db.Path() + restoreTmpSuffix,
				filepath.Join(f.root, "escape.json"),
			} {
				if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s exists after rejected restore: %v", p, err)
				}
			}
			select {
			case <-f.restart:
				t.Error("rejected restore requested a restart")
			default:
			}
		})
	}
}

func TestIDsAreValidated(t *testing.T) {
	f := newFixture(t, Config{})
	writeFile(t, filepath.Join(f.root, "secret.tar.zst"), "x")
	for _, tc := range []struct {
		path string
		code int
		err  string
	}{
		{"/api/backups/%2E%2E/download", http.StatusBadRequest, "invalid_id"},
		{"/api/backups/20261102T031300Z-manual/download", http.StatusNotFound, "not_found"},
		{"/api/backups/20261102T031300Z-bogus/download", http.StatusBadRequest, "invalid_id"},
	} {
		var apiErr apiError
		code, body := f.do(t, http.MethodGet, tc.path, &apiErr)
		if code != tc.code || apiErr.Error.Code != tc.err {
			t.Errorf("%s: %d %s, want %d %s", tc.path, code, body, tc.code, tc.err)
		}
	}
	for _, p := range []string{"/api/backups/..%2Fsecret/download", "/api/backups/..%2F..%2Fsecret/download"} {
		if code, body := f.do(t, http.MethodGet, p, nil); code == http.StatusOK || bytes.Equal(body, []byte("x")) {
			t.Errorf("%s served a file outside the backups dir: %d", p, code)
		}
	}
	var apiErr apiError
	if code, _ := f.do(t, http.MethodPost, "/api/backups/%2E%2E/restore", &apiErr); code != http.StatusBadRequest {
		t.Errorf("restore with traversal id: %d", code)
	}
}

func TestBackupSkipsDataDirInsideAuthDir(t *testing.T) {
	f := newFixture(t, Config{})
	f.b.paths.AuthDir = f.root // the data dir and its backups sit under it
	first := f.create(t)
	f.clock = f.clock.Add(time.Minute)
	second := f.create(t)
	_, raw := f.do(t, http.MethodGet, "/api/backups/"+second.ID+"/download", nil)
	names, _ := untar(t, raw)
	for _, n := range names {
		if strings.HasPrefix(n, "auth/taproot/") {
			t.Fatalf("backup of %s contains data dir file %q", first.ID, n)
		}
	}
}

func TestArchiveAndDirModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not keep unix permission bits")
	}
	f := newFixture(t, Config{})
	e := f.create(t)
	info, err := os.Stat(filepath.Join(f.b.dir, e.ID+archiveExt))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("archive mode %v", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(f.b.dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("backups dir mode %v", dirInfo.Mode().Perm())
	}
	if _, err := f.b.Restore(context.Background(), e.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{f.paths.ConfigFile, filepath.Join(f.paths.AuthDir, "claude-a.json")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v after restore", p, info.Mode().Perm())
		}
	}
}

func TestRestoreWithoutSelfRestartAsksForManualRestart(t *testing.T) {
	f := newFixture(t, Config{})
	f.b.restart = nil
	e := f.create(t)
	f.clock = f.clock.Add(time.Minute)
	var rr restoreResponse
	if code, body := f.do(t, http.MethodPost, "/api/backups/"+e.ID+"/restore", &rr); code != http.StatusOK {
		t.Fatalf("restore: %d %s", code, body)
	}
	if rr.Restart != "manual" || rr.Message == "" {
		t.Fatalf("response %+v", rr)
	}
}

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("test", -7*3600)
	for _, tc := range []struct{ now, want time.Time }{
		{time.Date(2026, 11, 2, 1, 0, 0, 0, loc), time.Date(2026, 11, 2, 3, 13, 0, 0, loc)},
		{time.Date(2026, 11, 2, 3, 13, 0, 0, loc), time.Date(2026, 11, 3, 3, 13, 0, 0, loc)},
		{time.Date(2026, 11, 2, 23, 59, 0, 0, loc), time.Date(2026, 11, 3, 3, 13, 0, 0, loc)},
		{time.Date(2026, 12, 31, 4, 0, 0, 0, loc), time.Date(2027, 1, 1, 3, 13, 0, 0, loc)},
	} {
		if got := nextRun(tc.now, 3, 13); !got.Equal(tc.want) {
			t.Errorf("nextRun(%v) = %v, want %v", tc.now, got, tc.want)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(context.Background(), store.Config{}, root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	paths := Paths{DataDir: root}

	b, err := New(Config{}, db, paths)
	if err != nil {
		t.Fatal(err)
	}
	if !b.scheduled || b.hour != 3 || b.minute != 13 || b.keep != 14 {
		t.Errorf("defaults: scheduled=%v %d:%d keep=%d", b.scheduled, b.hour, b.minute, b.keep)
	}
	b, err = New(Config{DailyAt: "off", Keep: 2}, db, paths)
	if err != nil {
		t.Fatal(err)
	}
	if b.scheduled || b.keep != 2 {
		t.Errorf("off: scheduled=%v keep=%d", b.scheduled, b.keep)
	}
	for _, bad := range []string{"25:00", "3pm", "03:13:00"} {
		if _, err := New(Config{DailyAt: bad}, db, paths); err == nil {
			t.Errorf("daily-at %q accepted", bad)
		}
	}
}

func TestSetScheduleAppliesAtRuntime(t *testing.T) {
	f := newFixture(t, Config{DailyAt: "off", Keep: 3})
	if at, keep := f.b.Schedule(); at != "off" || keep != 3 {
		t.Fatalf("Schedule() = %q, %d; want off, 3", at, keep)
	}
	for _, bad := range []struct {
		at   string
		keep int
	}{{"25:00", 3}, {"3pm", 3}, {"03:13", 0}, {"03:13", -1}} {
		if err := f.b.SetSchedule(bad.at, bad.keep); err == nil {
			t.Errorf("SetSchedule(%q, %d) accepted", bad.at, bad.keep)
		}
	}
	if at, keep := f.b.Schedule(); at != "off" || keep != 3 {
		t.Fatalf("rejected changes leaked: %q, %d", at, keep)
	}

	// The loop starts while the schedule is off; turning it on must wake it.
	// The fake clock sits 50ms before 03:13, so the timer fires almost at once.
	clock := time.Date(2026, 11, 2, 3, 12, 59, 950_000_000, time.Local)
	f.b.now = func() time.Time { return clock }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.b.Start(ctx)
	if err := f.b.SetSchedule("03:13", 2); err != nil {
		t.Fatal(err)
	}
	if at, keep := f.b.Schedule(); at != "03:13" || keep != 2 {
		t.Fatalf("Schedule() = %q, %d; want 03:13, 2", at, keep)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := f.b.List()
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) > 0 {
			if entries[0].Reason != ReasonScheduled {
				t.Fatalf("reason = %s, want scheduled", entries[0].Reason)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no scheduled backup after enabling the schedule")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	// A lower keep prunes at the next backup, per reason: two manual backups
	// leave the newer one and the scheduled one.
	if err := f.b.SetSchedule("off", 1); err != nil {
		t.Fatal(err)
	}
	f.create(t)
	newest := f.create(t)
	if got := f.list(t); len(got) != 2 || got[0].ID != newest.ID || got[1].Reason != ReasonScheduled {
		t.Fatalf("after keep=1: %+v", got)
	}
}
