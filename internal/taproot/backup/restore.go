package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/265866/taproot/internal/taproot/store"
	log "github.com/sirupsen/logrus"
)

const (
	restoreTmpSuffix     = ".restore-tmp"
	restorePendingSuffix = ".restore-pending"
	pendingMarkerName    = "restore-pending.json"
)

// pendingRestore is written to the data dir when a restore stages a database
// snapshot. ApplyPendingRestore consumes it on the next start.
type pendingRestore struct {
	// DB is the database path, relative to the data dir when it lies inside
	// it, so a data dir mounted at another path still finds it. Markers from
	// before this hold an absolute path.
	DB       string `json:"db"`
	BackupID string `json:"backupId"`
}

// RestoreOptions are the caller's side of a restore.
type RestoreOptions struct {
	// CallerNodeID is the stable node ID of the tailnet device asking, or ""
	// for a caller that the access guard admits whatever the lists say.
	CallerNodeID string
	// AllowLockout restores a backup that takes the caller's Admin away.
	AllowLockout bool
}

// RestoreResult reports a completed restore.
type RestoreResult struct {
	PreRestoreBackupID string
}

// Restore verifies the backup and checks that the next start can load it,
// saves the current state as a pre-restore backup, adds the backup's refresh
// journal entries, replaces the config and auth files, and stages the
// database snapshot for ApplyPendingRestore on the next start. A backup that
// fails a check changes nothing. The restore marker is written last, so a
// failure while replacing files leaves the database alone; the files are then
// put back from the pre-restore backup. Journal entries the restore added
// stay, because a claim is never removed. After a restore, writes to
// taproot.yaml, backups, and restores are refused until the process restarts.
func (b *Backups) Restore(ctx context.Context, id string, opts RestoreOptions) (RestoreResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.isRestartPending() {
		return RestoreResult{}, ErrRestartPending
	}

	archive, err := b.archivePath(id)
	if err != nil {
		return RestoreResult{}, err
	}
	dbPath, err := filepath.Abs(b.db.Path())
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resolve database path: %w", err)
	}
	dbTmp := dbPath + restoreTmpSuffix
	dbStaged := dbPath + restorePendingSuffix
	defer func() {
		if errRemove := os.Remove(dbTmp); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
			log.WithError(errRemove).Warn("taproot backups: remove restore staging")
		}
	}()

	c, err := extractVerified(archive, dbTmp)
	if err != nil {
		return RestoreResult{}, err
	}
	// The code holds no migrations, so a snapshot from before a schema change
	// would stop the next start. Refuse it now, before anything changes.
	if err := store.CheckFile(ctx, dbTmp); err != nil {
		return RestoreResult{}, fmt.Errorf("%w: its database predates this release's schema: %v", errIncompatible, err)
	}
	if err := b.prepareConfigFiles(c, dbPath, opts); err != nil {
		return RestoreResult{}, err
	}

	// Settings must not edit taproot.yaml between the pre-restore backup and
	// the restore replacing it.
	b.cfgMu.Lock()
	defer b.cfgMu.Unlock()
	// Pruning waits until the restore is done and spares its source, which
	// may be the oldest archive.
	pre, err := b.createLocked(ctx, ReasonPreRestore)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("pre-restore backup: %w", err)
	}
	err = b.applyFiles(c)
	if err == nil {
		err = b.stageDatabase(dbTmp, dbStaged, dbPath, id)
	}
	if err != nil {
		if errRollback := b.rollbackFiles(pre.ID); errRollback != nil {
			return RestoreResult{}, fmt.Errorf("%w; putting files back from %s also failed: %v", err, pre.ID, errRollback)
		}
		return RestoreResult{}, fmt.Errorf("%w; files put back from %s", err, pre.ID)
	}
	b.restartPending = true
	b.prune(id)
	log.WithFields(log.Fields{"id": id, "preRestore": pre.ID}).Info("taproot backups: restored; database swaps in on next start")
	return RestoreResult{PreRestoreBackupID: pre.ID}, nil
}

// prepareConfigFiles makes the backup's config files fit this server: it
// keeps the current data-dir, store.path, and auth-dir, since the database
// is staged and the credentials are written where this server keeps them,
// and checks that the next start loads the result and leaves the caller
// Admin.
func (b *Backups) prepareConfigFiles(c contents, dbPath string, opts RestoreOptions) error {
	for name, current := range map[string]string{configEntry: b.paths.ConfigFile, taprootEntry: b.paths.TaprootFile} {
		data, ok := c.files[name]
		if !ok || current == "" {
			continue
		}
		kept, err := keepPathKeys(data, current, pathKeys[name])
		if err != nil {
			return fmt.Errorf("%w: %s: %v", errIncompatible, name, err)
		}
		c.files[name] = kept
	}
	data, ok := c.files[taprootEntry]
	if !ok || b.paths.TaprootFile == "" || b.paths.CheckTaproot == nil {
		return nil
	}
	next, err := b.paths.CheckTaproot(data)
	if err != nil {
		return fmt.Errorf("%w: this release cannot start with the backup's taproot.yaml: %v", errIncompatible, err)
	}
	if !samePath(next.DataDir, b.paths.DataDir) || !samePath(next.DBPath, dbPath) {
		return fmt.Errorf("%w: taproot.yaml now keeps data in %s (database %s), but taproot is running with %s (database %s); restart taproot, then restore", errIncompatible, next.DataDir, next.DBPath, b.paths.DataDir, dbPath)
	}
	if opts.CallerNodeID == "" || opts.AllowLockout {
		return nil
	}
	const howTo = "restore it from the server itself, or from a device the backup gives Admin"
	if !next.AccessEnabled {
		return fmt.Errorf("%w: the backup's taproot.yaml turns the access guard off, which leaves the dashboard to the server itself; %s", errWouldLockOut, howTo)
	}
	if !slices.Contains(next.Admins, opts.CallerNodeID) {
		return fmt.Errorf("%w: the backup's taproot.yaml does not give this device (%s) Admin; %s", errWouldLockOut, opts.CallerNodeID, howTo)
	}
	return nil
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

// stageDatabase moves the extracted snapshot to its pending name and then
// writes the marker that makes ApplyPendingRestore swap it in.
func (b *Backups) stageDatabase(dbTmp, dbStaged, dbPath, id string) error {
	if err := os.Rename(dbTmp, dbStaged); err != nil {
		return fmt.Errorf("stage database: %w", err)
	}
	markerDB := dbPath
	if dataDir, err := filepath.Abs(b.paths.DataDir); err == nil {
		if rel, errRel := filepath.Rel(dataDir, dbPath); errRel == nil && filepath.IsLocal(rel) {
			markerDB = rel
		}
	}
	marker, err := json.Marshal(pendingRestore{DB: markerDB, BackupID: id})
	if err == nil {
		err = writeFileAtomic(filepath.Join(b.paths.DataDir, pendingMarkerName), marker)
	}
	if err != nil {
		if errRemove := os.Remove(dbStaged); errRemove != nil {
			log.WithError(errRemove).Warn("taproot backups: remove staged database")
		}
		return fmt.Errorf("write restore marker: %w", err)
	}
	return nil
}

// applyFiles writes the config files and makes the auth dir match c.
func (b *Backups) applyFiles(c contents) error {
	for _, f := range []struct{ name, dest string }{{configEntry, b.paths.ConfigFile}, {taprootEntry, b.paths.TaprootFile}} {
		data, ok := c.files[f.name]
		if !ok || f.dest == "" {
			continue
		}
		if err := writeFileAtomic(f.dest, data); err != nil {
			return err
		}
	}
	return b.restoreAuth(c)
}

// rollbackFiles puts back the config and auth files saved in a pre-restore
// backup after a restore failed partway.
func (b *Backups) rollbackFiles(preID string) error {
	staging, err := os.MkdirTemp(b.dir, tempPrefix)
	if err != nil {
		return fmt.Errorf("create rollback staging: %w", err)
	}
	defer func() {
		if errRemove := os.RemoveAll(staging); errRemove != nil {
			log.WithError(errRemove).Warn("taproot backups: remove rollback staging")
		}
	}()
	c, err := extractVerified(filepath.Join(b.dir, preID+archiveExt), filepath.Join(staging, dbEntry))
	if err != nil {
		return err
	}
	return b.applyFiles(c)
}

// restoreAuth merges the backup's refresh journal into the auth dir, then
// makes the auth dir hold exactly the backup's auth files. Files it removes
// are in the pre-restore backup.
func (b *Backups) restoreAuth(c contents) error {
	if b.paths.AuthDir == "" {
		return nil
	}
	if err := os.MkdirAll(b.paths.AuthDir, 0o700); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}
	if err := b.restoreJournal(c); err != nil {
		return err
	}
	keep := map[string]bool{}
	for name, data := range c.files {
		rel, ok := strings.CutPrefix(name, authPrefix)
		if !ok || inJournal(name) {
			continue
		}
		keep[rel] = true
		dest := filepath.Join(b.paths.AuthDir, filepath.FromSlash(rel))
		// A symlinked credential keeps its link; the content goes to its target.
		if target, errResolve := filepath.EvalSymlinks(dest); errResolve == nil {
			dest = target
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return fmt.Errorf("create auth subdir: %w", err)
		}
		if err := writeFileAtomic(dest, data); err != nil {
			return err
		}
	}
	current, err := b.authFiles()
	if err != nil {
		return err
	}
	for rel, path := range current {
		if keep[rel] || !c.manifest.AuthTree && strings.Contains(rel, "/") {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove auth file not in backup: %w", err)
		}
	}
	return nil
}

// restoreJournal adds the backup's refresh journal claims and receipts that
// the auth dir lacks, and syncs them to disk, before any credential is
// written. Upstream must never resend a refresh token the journal records,
// so a restored credential must not appear before its claim. It never
// removes or replaces an entry: claims never expire, and the live journal
// holds every claim made since the backup, including claims on the tokens
// the backup restores.
func (b *Backups) restoreJournal(c contents) error {
	dest := func(name string) string {
		return filepath.Join(b.paths.AuthDir, filepath.FromSlash(strings.TrimPrefix(name, authPrefix)))
	}
	changed := map[string]bool{}
	sort.Strings(c.dirs)
	for _, name := range c.dirs {
		path := dest(name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("restore refresh journal: %w", err)
		}
		changed[filepath.Dir(path)] = true
	}
	for name, data := range c.files {
		if !inJournal(name) {
			continue
		}
		path := dest(name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("restore refresh journal: %w", err)
		}
		if err := writeFileExclusive(path, data); err != nil {
			return fmt.Errorf("restore refresh journal: %w", err)
		}
		changed[filepath.Dir(path)] = true
	}
	for dir := range changed {
		if err := syncDir(dir); err != nil {
			return fmt.Errorf("restore refresh journal: %w", err)
		}
	}
	return nil
}

// linkFile is os.Link; a test replaces it to act as a filesystem without
// hard links.
var linkFile = os.Link

// writeFileExclusive creates path with data unless it already exists. Readers
// see no file or the whole file: the data is written to a temp file and then
// hard-linked into place, which fails rather than replace an existing file.
// Some SMB, 9p, and FUSE mounts have no hard links; there the temp file is
// renamed into place after a fresh check, as upstream writes its receipts.
func writeFileExclusive(path string, data []byte) error {
	if _, errStat := os.Lstat(path); errStat == nil {
		return nil
	}
	dir, base := filepath.Split(path)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", base, err)
	}
	defer func() {
		_ = tmp.Close()
		if errRemove := os.Remove(tmp.Name()); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
			log.WithError(errRemove).Warn("taproot backups: remove journal temp file")
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temp for %s: %w", base, err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temp for %s: %w", base, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp for %s: %w", base, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", base, err)
	}
	errLink := linkFile(tmp.Name(), path)
	if errLink == nil || errors.Is(errLink, os.ErrExist) {
		return nil
	}
	if _, errStat := os.Lstat(path); errStat == nil {
		return nil
	}
	if errRename := os.Rename(tmp.Name(), path); errRename != nil {
		return fmt.Errorf("link %s: %w; rename: %w", base, errLink, errRename)
	}
	return nil
}

// syncDir makes new entries in dir durable. Windows cannot sync a directory.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	errSync := f.Sync()
	return errors.Join(errSync, f.Close())
}

// writeFileAtomic replaces path with data through a temp file in the same
// directory, so readers see the old or the new file and never a partial one.
func writeFileAtomic(path string, data []byte) (err error) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", base, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temp for %s: %w", base, err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temp for %s: %w", base, err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp for %s: %w", base, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", base, err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", base, err)
	}
	if err = syncDir(dir); err != nil {
		return fmt.Errorf("sync directory of %s: %w", base, err)
	}
	return nil
}

// ApplyPendingRestore swaps a database snapshot staged by Restore into place.
// Call it with the data dir before store.Open. It does nothing when no restore
// is pending, and it is safe to rerun after an interruption.
func ApplyPendingRestore(dataDir string) error {
	markerPath := filepath.Join(dataDir, pendingMarkerName)
	raw, err := os.ReadFile(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read restore marker: %w", err)
	}
	var p pendingRestore
	if err := json.Unmarshal(raw, &p); err != nil || p.DB == "" {
		return fmt.Errorf("parse restore marker %s: invalid", markerPath)
	}
	db := p.DB
	if !filepath.IsAbs(db) {
		db = filepath.Join(dataDir, db)
	}
	staged := db + restorePendingSuffix
	if _, err := os.Stat(staged); err == nil {
		// A WAL left by the old database would be replayed onto the snapshot.
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := os.Remove(db + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove old database %s: %w", suffix, err)
			}
		}
		if err := os.Rename(staged, db); err != nil {
			return fmt.Errorf("swap in restored database: %w", err)
		}
		if err := syncDir(filepath.Dir(db)); err != nil {
			return fmt.Errorf("swap in restored database: %w", err)
		}
		log.WithField("backup", p.BackupID).Info("taproot backups: restored database swapped in")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat staged database: %w", err)
	} else if _, errDB := os.Stat(db); errDB != nil {
		// The swap finished before an interruption only when the database is
		// there; otherwise the restore cannot finish.
		log.WithFields(log.Fields{"backup": p.BackupID, "db": db}).Warn("taproot backups: a restore was pending, but its staged database is missing; the database was not restored")
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove restore marker: %w", err)
	}
	return nil
}
