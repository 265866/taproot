package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
// backup, replaces the config and auth files, and stages the database
// snapshot for ApplyPendingRestore on the next start. A backup that fails
// verification changes nothing.
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
	staged := false
	defer func() {
		if staged {
			return
		}
		if errRemove := os.Remove(dbTmp); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
			log.WithError(errRemove).Warn("taproot backups: remove restore staging")
		}
	}()

	c, err := extractVerified(archive, dbTmp)
	if err != nil {
		return RestoreResult{}, err
	}
	pre, err := b.createLocked(ctx, ReasonPreRestore)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("pre-restore backup: %w", err)
	}

	if err := os.Rename(dbTmp, dbPath+restorePendingSuffix); err != nil {
		return RestoreResult{}, fmt.Errorf("stage database: %w", err)
	}
	staged = true
	marker, err := json.Marshal(pendingRestore{DB: dbPath, BackupID: id})
	if err != nil {
		return RestoreResult{}, fmt.Errorf("encode restore marker: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(b.paths.DataDir, pendingMarkerName), marker); err != nil {
		return RestoreResult{}, err
	}

	for name, dest := range map[string]string{configEntry: b.paths.ConfigFile, taprootEntry: b.paths.TaprootFile} {
		data, ok := c.files[name]
		if !ok || dest == "" {
			continue
		}
		if err := writeFileAtomic(dest, data); err != nil {
			return RestoreResult{}, err
		}
	}
	if err := b.restoreAuth(c.files); err != nil {
		return RestoreResult{}, err
	}
	log.WithFields(log.Fields{"id": id, "preRestore": pre.ID}).Info("taproot backups: restored; database swaps in on next start")
	return RestoreResult{PreRestoreBackupID: pre.ID}, nil
}

// restoreAuth makes the auth dir hold exactly the backup's auth files. Files
// it removes are in the pre-restore backup.
func (b *Backups) restoreAuth(files map[string][]byte) error {
	if b.paths.AuthDir == "" {
		return nil
	}
	if err := os.MkdirAll(b.paths.AuthDir, 0o700); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}
	keep := map[string]bool{}
	for name, data := range files {
		rel, ok := strings.CutPrefix(name, authPrefix)
		if !ok {
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
