// Package backup snapshots config, credentials, and the taproot database on a
// schedule and restores them on request.
package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/taproot/store"
	log "github.com/sirupsen/logrus"
)

// Config is the backups section of taproot.yaml.
type Config struct {
	// DailyAt is the local time of the scheduled backup as HH:MM, or "off".
	DailyAt string `yaml:"daily-at"`
	// Keep is how many backups to retain. Zero or less means the default.
	Keep int `yaml:"keep"`
}

const (
	defaultDailyAt = "03:13"
	defaultKeep    = 14
	archiveExt     = ".tar.zst"
	tempPrefix     = ".tmp-"
)

// Paths names the files a backup covers.
type Paths struct {
	ConfigFile  string
	TaprootFile string
	AuthDir     string
	DataDir     string
}

// Reason records why a backup was taken.
type Reason string

const (
	ReasonScheduled  Reason = "scheduled"
	ReasonManual     Reason = "manual"
	ReasonPreRestore Reason = "pre-restore"
)

// Entry describes one backup archive.
type Entry struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Bytes     int64     `json:"bytes"`
	Reason    Reason    `json:"reason"`
	Files     int       `json:"files"`
}

var (
	errInvalidID = errors.New("invalid backup id")
	errNotFound  = errors.New("backup not found")
	errCorrupt   = errors.New("corrupt backup")
)

// idPattern is the only id shape accepted from callers, which keeps ids from
// naming anything outside the backups directory.
var idPattern = regexp.MustCompile(`^(\d{8}T\d{6}Z)(?:\.(\d{1,4}))?-(scheduled|manual|pre-restore)$`)

const idTimeLayout = "20060102T150405Z"

// Backups owns snapshots.
type Backups struct {
	db    *store.DB
	paths Paths
	dir   string

	// schedMu guards the schedule and retention count, which SetSchedule
	// changes at runtime; wake tells the schedule loop to re-read them.
	schedMu      sync.Mutex
	keep         int
	scheduled    bool
	hour, minute int
	wake         chan struct{}

	mu  sync.Mutex
	now func() time.Time
	// restart asks the process to exit gracefully so a supervisor restarts it.
	// It is nil where the process cannot signal itself.
	restart func() error
}

// New prepares the module.
func New(cfg Config, db *store.DB, paths Paths) (*Backups, error) {
	if db == nil {
		return nil, errors.New("backups need a store")
	}
	if paths.DataDir == "" {
		return nil, errors.New("backups need a data dir")
	}
	b := &Backups{
		db:      db,
		paths:   paths,
		dir:     filepath.Join(paths.DataDir, "backups"),
		keep:    cfg.Keep,
		wake:    make(chan struct{}, 1),
		now:     time.Now,
		restart: selfRestart,
	}
	if b.keep <= 0 {
		b.keep = defaultKeep
	}
	var err error
	if b.scheduled, b.hour, b.minute, err = parseDailyAt(cfg.DailyAt); err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(b.dir); err != nil {
		return nil, err
	}
	b.removeLeftovers()
	return b, nil
}

// parseDailyAt reads daily-at: blank means the default time, "off" disables the schedule.
func parseDailyAt(raw string) (scheduled bool, hour, minute int, err error) {
	at := strings.TrimSpace(raw)
	if at == "" {
		at = defaultDailyAt
	}
	if strings.EqualFold(at, "off") {
		return false, 0, 0, nil
	}
	hour, minute, err = parseClock(at)
	return err == nil, hour, minute, err
}

// Schedule returns the active daily time as HH:MM or "off", and the retention count.
func (b *Backups) Schedule() (dailyAt string, keep int) {
	b.schedMu.Lock()
	defer b.schedMu.Unlock()
	if !b.scheduled {
		return "off", b.keep
	}
	return fmt.Sprintf("%02d:%02d", b.hour, b.minute), b.keep
}

// SetSchedule changes the daily time and retention count without a restart.
// A lower count prunes at the next backup. Persisting them to taproot.yaml is
// the caller's job.
func (b *Backups) SetSchedule(dailyAt string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("backups keep must be at least 1, got %d", keep)
	}
	scheduled, hour, minute, err := parseDailyAt(dailyAt)
	if err != nil {
		return err
	}
	b.schedMu.Lock()
	b.scheduled, b.hour, b.minute, b.keep = scheduled, hour, minute, keep
	b.schedMu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
	return nil
}

func parseClock(s string) (int, int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("backups daily-at %q: want HH:MM or off", s)
	}
	return t.Hour(), t.Minute(), nil
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}

// removeLeftovers deletes staging files that an interrupted create or restore left behind.
func (b *Backups) removeLeftovers() {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		log.WithError(err).Warn("taproot backups: list backups dir")
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			if errRemove := os.RemoveAll(filepath.Join(b.dir, e.Name())); errRemove != nil {
				log.WithError(errRemove).Warn("taproot backups: remove leftover staging")
			}
		}
	}
	if errRemove := os.Remove(b.db.Path() + restoreTmpSuffix); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
		log.WithError(errRemove).Warn("taproot backups: remove leftover restore staging")
	}
}

// Start runs the schedule until ctx ends. The loop runs even while the
// schedule is off, so SetSchedule can turn it on.
func (b *Backups) Start(ctx context.Context) {
	if at, _ := b.Schedule(); at == "off" {
		log.Info("taproot backups: schedule off")
	}
	go b.runSchedule(ctx)
}

func (b *Backups) runSchedule(ctx context.Context) {
	for {
		b.schedMu.Lock()
		scheduled, hour, minute := b.scheduled, b.hour, b.minute
		b.schedMu.Unlock()
		var timer *time.Timer
		var fire <-chan time.Time
		if scheduled {
			now := b.now()
			timer = time.NewTimer(nextRun(now, hour, minute).Sub(now))
			fire = timer.C
		}
		stop := func() {
			if timer != nil {
				timer.Stop()
			}
		}
		select {
		case <-ctx.Done():
			stop()
			return
		case <-b.wake:
			stop()
			continue
		case <-fire:
		}
		entry, err := b.Create(ctx, ReasonScheduled)
		if err != nil {
			log.WithError(err).Error("taproot backups: scheduled backup failed")
			continue
		}
		log.WithFields(log.Fields{"id": entry.ID, "bytes": entry.Bytes}).Info("taproot backups: scheduled backup written")
	}
}

// nextRun returns the first hour:minute in now's location strictly after now.
func nextRun(now time.Time, hour, minute int) time.Time {
	y, m, d := now.Date()
	next := time.Date(y, m, d, hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = time.Date(y, m, d+1, hour, minute, 0, 0, now.Location())
	}
	return next
}

// Create writes a new backup and prunes old ones beyond the retention count.
func (b *Backups) Create(ctx context.Context, reason Reason) (Entry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.createLocked(ctx, reason)
}

func (b *Backups) createLocked(ctx context.Context, reason Reason) (Entry, error) {
	if err := ensurePrivateDir(b.dir); err != nil {
		return Entry{}, err
	}
	createdAt := b.now().UTC().Truncate(time.Second)
	id, err := b.freeID(createdAt, reason)
	if err != nil {
		return Entry{}, err
	}
	staging, err := os.MkdirTemp(b.dir, tempPrefix)
	if err != nil {
		return Entry{}, fmt.Errorf("create backup staging: %w", err)
	}
	defer func() {
		if errRemove := os.RemoveAll(staging); errRemove != nil {
			log.WithError(errRemove).Warn("taproot backups: remove staging")
		}
	}()

	sources, err := b.collect()
	if err != nil {
		return Entry{}, err
	}
	snapshot := filepath.Join(staging, dbEntry)
	if err := b.db.Snapshot(ctx, snapshot); err != nil {
		return Entry{}, err
	}
	sources = append(sources, source{name: dbEntry, path: snapshot})

	m := manifest{Version: manifestVersion, ID: id, CreatedAt: createdAt, Reason: reason}
	tmpArchive := filepath.Join(staging, id+archiveExt)
	if err := writeArchive(tmpArchive, &m, sources); err != nil {
		return Entry{}, err
	}
	final := filepath.Join(b.dir, id+archiveExt)
	if err := os.Rename(tmpArchive, final); err != nil {
		return Entry{}, fmt.Errorf("move backup into place: %w", err)
	}
	info, err := os.Stat(final)
	if err != nil {
		return Entry{}, fmt.Errorf("stat backup: %w", err)
	}
	b.prune()
	return Entry{ID: id, CreatedAt: createdAt, Bytes: info.Size(), Reason: reason, Files: len(m.Files)}, nil
}

func (b *Backups) freeID(createdAt time.Time, reason Reason) (string, error) {
	base := createdAt.Format(idTimeLayout)
	for seq := 1; seq < 10000; seq++ {
		id := base + "-" + string(reason)
		if seq > 1 {
			id = base + "." + strconv.Itoa(seq) + "-" + string(reason)
		}
		_, err := os.Stat(filepath.Join(b.dir, id+archiveExt))
		if errors.Is(err, os.ErrNotExist) {
			return id, nil
		}
		if err != nil {
			return "", fmt.Errorf("check backup id: %w", err)
		}
	}
	return "", fmt.Errorf("no free backup id at %s", base)
}

// collect reads config and auth files into memory. They are small, and
// holding them keeps the archive consistent with its manifest even when a
// token refresh rewrites a file mid-backup.
func (b *Backups) collect() ([]source, error) {
	var sources []source
	for _, f := range []struct{ name, path string }{
		{configEntry, b.paths.ConfigFile},
		{taprootEntry, b.paths.TaprootFile},
	} {
		if f.path == "" {
			continue
		}
		data, err := os.ReadFile(f.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.name, err)
		}
		sources = append(sources, source{name: f.name, data: data})
	}
	auth, err := b.authFiles()
	if err != nil {
		return nil, err
	}
	for rel, path := range auth {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read auth file %s: %w", rel, err)
		}
		sources = append(sources, source{name: authPrefix + rel, data: data})
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].name < sources[j].name })
	return sources, nil
}

// authFiles maps credential file names to their paths. Upstream loads only
// top-level *.json files from the auth dir as credentials, so subdirectories
// (upstream may write app logs to <auth-dir>/logs, and the data dir may sit
// inside the auth dir) and other files are neither backed up nor touched by a
// restore.
func (b *Backups) authFiles() (map[string]string, error) {
	files := map[string]string{}
	if b.paths.AuthDir == "" {
		return files, nil
	}
	root, err := filepath.Abs(b.paths.AuthDir)
	if err != nil {
		return nil, fmt.Errorf("resolve auth dir: %w", err)
	}
	if resolved, errResolve := filepath.EvalSymlinks(root); errResolve == nil {
		root = resolved
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if path == root && errors.Is(err, os.ErrNotExist) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = path
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk auth dir: %w", err)
	}
	return files, nil
}

func (b *Backups) prune() {
	entries, err := b.List()
	if err != nil {
		log.WithError(err).Warn("taproot backups: list for retention")
		return
	}
	_, keep := b.Schedule()
	for i := keep; i < len(entries); i++ {
		path := filepath.Join(b.dir, entries[i].ID+archiveExt)
		if errRemove := os.Remove(path); errRemove != nil {
			log.WithError(errRemove).WithField("id", entries[i].ID).Warn("taproot backups: prune")
			continue
		}
		log.WithField("id", entries[i].ID).Info("taproot backups: pruned")
	}
}

// List returns the backups newest first.
func (b *Backups) List() ([]Entry, error) {
	dirEntries, err := os.ReadDir(b.dir)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	type keyed struct {
		Entry
		seq int
	}
	var found []keyed
	for _, de := range dirEntries {
		name := de.Name()
		if !de.Type().IsRegular() || !strings.HasSuffix(name, archiveExt) {
			continue
		}
		id := strings.TrimSuffix(name, archiveExt)
		createdAt, seq, reason, ok := parseID(id)
		if !ok {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		e := Entry{ID: id, CreatedAt: createdAt, Bytes: info.Size(), Reason: reason}
		if m, errManifest := readManifest(filepath.Join(b.dir, name)); errManifest == nil {
			e.Files = len(m.Files)
		} else {
			log.WithError(errManifest).WithField("id", id).Warn("taproot backups: unreadable manifest")
		}
		found = append(found, keyed{Entry: e, seq: seq})
	}
	sort.Slice(found, func(i, j int) bool {
		if !found[i].CreatedAt.Equal(found[j].CreatedAt) {
			return found[i].CreatedAt.After(found[j].CreatedAt)
		}
		return found[i].seq > found[j].seq
	})
	out := make([]Entry, len(found))
	for i, k := range found {
		out[i] = k.Entry
	}
	return out, nil
}

func parseID(id string) (time.Time, int, Reason, bool) {
	m := idPattern.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}, 0, "", false
	}
	createdAt, err := time.Parse(idTimeLayout, m[1])
	if err != nil {
		return time.Time{}, 0, "", false
	}
	seq := 1
	if m[2] != "" {
		seq, _ = strconv.Atoi(m[2])
	}
	return createdAt, seq, Reason(m[3]), true
}

// archivePath resolves a caller-supplied id to an existing archive.
func (b *Backups) archivePath(id string) (string, error) {
	if _, _, _, ok := parseID(id); !ok {
		return "", errInvalidID
	}
	path := filepath.Join(b.dir, id+archiveExt)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", errNotFound
	}
	if err != nil {
		return "", fmt.Errorf("stat backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errNotFound
	}
	return path, nil
}
