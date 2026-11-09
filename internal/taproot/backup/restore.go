package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	DB       string `json:"db"`
	BackupID string `json:"backupId"`
}

// RestoreResult reports a completed restore.
type RestoreResult struct {
	PreRestoreBackupID string
}

// Restore verifies the backup, saves the current state as a pre-restore
// backup, adds the backup's refresh journal entries, replaces the config and
// auth files, and stages the database snapshot for ApplyPendingRestore on
// the next start. A backup that fails verification changes nothing. The
// restore marker is written last, so a failure while replacing files leaves
// the database alone; the files are then put back from the pre-restore
// backup. Journal entries the restore added stay, because a claim is never
// removed.
func (b *Backups) Restore(ctx context.Context, id string) (RestoreResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

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
		return RestoreResult{}, fmt.Errorf("%w: %v", errIncompatible, err)
	}
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
	b.prune(id)
	log.WithFields(log.Fields{"id": id, "preRestore": pre.ID}).Info("taproot backups: restored; database swaps in on next start")
	return RestoreResult{PreRestoreBackupID: pre.ID}, nil
}

// stageDatabase moves the extracted snapshot to its pending name and then
// writes the marker that makes ApplyPendingRestore swap it in.
func (b *Backups) stageDatabase(dbTmp, dbStaged, dbPath, id string) error {
	if err := os.Rename(dbTmp, dbStaged); err != nil {
		return fmt.Errorf("stage database: %w", err)
	}
	marker, err := json.Marshal(pendingRestore{DB: dbPath, BackupID: id})
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
		if keep[rel] {
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

// writeFileExclusive creates path with data unless it already exists. Readers
// see no file or the whole file: the data is written to a temp file and then
// hard-linked into place, which fails rather than replace an existing file.
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
	if err = os.Link(tmp.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("link %s: %w", base, err)
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
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temp for %s: %w", base, err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temp for %s: %w", base, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp for %s: %w", base, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", base, err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", base, err)
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
	staged := p.DB + restorePendingSuffix
	if _, err := os.Stat(staged); err == nil {
		// A WAL left by the old database would be replayed onto the snapshot.
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := os.Remove(p.DB + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove old database %s: %w", suffix, err)
			}
		}
		if err := os.Rename(staged, p.DB); err != nil {
			return fmt.Errorf("swap in restored database: %w", err)
		}
		log.WithField("backup", p.BackupID).Info("taproot backups: restored database swapped in")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat staged database: %w", err)
	}
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove restore marker: %w", err)
	}
	return nil
}
