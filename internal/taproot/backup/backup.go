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
	"sync/atomic"
	"time"

	"github.com/265866/taproot/internal/taproot/access"
	"github.com/265866/taproot/internal/taproot/store"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// Config is the backups section of taproot.yaml.
type Config struct {
	// DailyAt is the local time of the scheduled backup as HH:MM, or "off".
	DailyAt string `yaml:"daily-at"`
	// Keep is how many backups to retain of each reason (scheduled, manual,
	// pre-restore), so manual and pre-restore backups never push out
	// scheduled ones. Zero or less means the default.
	Keep int `yaml:"keep"`
}

const (
	defaultDailyAt = "00:00"
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
	// CheckTaproot reads a taproot.yaml the way the next start will. A
	// restore whose taproot.yaml it refuses changes nothing. Without it, a
	// restore writes the backup's taproot.yaml unchecked.
	CheckTaproot func(raw []byte) (NextStart, error)
}

// NextStart is what the next start reads from a restored taproot.yaml.
type NextStart struct {
	// DataDir and DBPath are where it keeps its data and its database.
	DataDir, DBPath string
	// AccessEnabled is false when the file turns the access guard off, which
	// leaves the dashboard to loopback callers.
	AccessEnabled bool
	// Admins are the device IDs access.devices gives Admin.
	Admins []string
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
	// errIncompatible is a backup this release cannot start with: its
	// database lacks columns this release declares, or its taproot.yaml does
	// not load.
	errIncompatible = errors.New("incompatible backup")
	// errWouldLockOut is a restore that takes the dashboard from the
	// calling device.
	errWouldLockOut = errors.New("this restore would lock this device out of the dashboard")
	// ErrRestartPending refuses a change while a restore waits for the
	// process to restart.
	ErrRestartPending = errors.New("a restore is waiting for taproot to restart; restart taproot first")
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
	// newTimer is time.NewTimer, replaceable in tests.
	newTimer func(time.Duration) (<-chan time.Time, func() bool)

	// cfgMu serializes the writers of taproot.yaml: a restore and the
	// settings module. restartPending is set once a restore replaced the
	// files, and refuses later writes, backups, and restores, which would
	// mix the restored files with the running database and settings.
	cfgMu          sync.Mutex
	restartPending bool

	// manifests caches each archive's file count, keyed by name, since
	// reading it allocates the decoder's whole window.
	manifestsMu sync.Mutex
	manifests   map[string]manifestCount
	// identityFrom reads the caller the access guard admitted.
	identityFrom func(*gin.Context) (access.Identity, bool)
	// restart asks the process to exit gracefully so a supervisor restarts it.
	// It is nil where the process cannot signal itself.
	restart          func() error
	restartRequested atomic.Bool
}

// RestartRequested reports whether a restore asked the process to restart.
func (b *Backups) RestartRequested() bool { return b.restartRequested.Load() }

// New prepares the module.
func New(cfg Config, db *store.DB, paths Paths) (*Backups, error) {
	if db == nil {
		return nil, errors.New("backups need a store")
	}
	if paths.DataDir == "" {
		return nil, errors.New("backups need a data dir")
	}
	b := &Backups{
		db:    db,
		paths: paths,
		dir:   filepath.Join(paths.DataDir, "backups"),
		keep:  cfg.Keep,
		wake:  make(chan struct{}, 1),
		now:   time.Now,
		newTimer: func(d time.Duration) (<-chan time.Time, func() bool) {
			t := time.NewTimer(d)
			return t.C, t.Stop
		},
		restart:      selfRestart,
		identityFrom: access.IdentityFrom,
		manifests:    map[string]manifestCount{},
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

func (b *Backups) isRestartPending() bool {
	b.cfgMu.Lock()
	defer b.cfgMu.Unlock()
	return b.restartPending
}

// WriteConfig runs write, which changes taproot.yaml, so that no restore
// replaces the file while it runs. It returns ErrRestartPending without
// running write once a restore is waiting for a restart.
func (b *Backups) WriteConfig(write func() error) error {
	b.cfgMu.Lock()
	defer b.cfgMu.Unlock()
	if b.restartPending {
		return ErrRestartPending
	}
	return write()
}

// Start runs the schedule until ctx ends. The loop runs even while the
// schedule is off, so SetSchedule can turn it on.
func (b *Backups) Start(ctx context.Context) {
	if at, _ := b.Schedule(); at == "off" {
		log.Info("taproot backups: schedule off")
	}
	go b.runSchedule(ctx)
}

// maxScheduleWait bounds one wait of the schedule loop. Its timer runs on
// the monotonic clock, which stops while the host is suspended and ignores
// wall clock steps, so the loop rechecks the wall clock this often.
const maxScheduleWait = time.Minute

func (b *Backups) runSchedule(ctx context.Context) {
	// last is the run time of the last scheduled backup, so a wall clock
	// that steps back after it cannot run the same day's backup again.
	var last time.Time
	for {
		b.schedMu.Lock()
		scheduled, hour, minute := b.scheduled, b.hour, b.minute
		b.schedMu.Unlock()
		var fire <-chan time.Time
		stop := func() bool { return false }
		var target time.Time
		if scheduled {
			now := b.now()
			target = nextRun(now, hour, minute)
			if !target.After(last) {
				target = nextRun(last, hour, minute)
			}
			fire, stop = b.newTimer(min(target.Sub(now), maxScheduleWait))
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
		if b.now().Before(target) {
			continue
		}
		last = target
		entry, err := b.Create(ctx, ReasonScheduled)
		if errors.Is(err, ErrRestartPending) {
			log.Info("taproot backups: scheduled backup skipped: a restore is waiting for a restart")
			continue
		}
		if err != nil {
			log.WithError(err).Error("taproot backups: scheduled backup failed")
			continue
		}
		log.WithFields(log.Fields{"id": entry.ID, "bytes": entry.Bytes}).Info("taproot backups: scheduled backup written")
	}
}

// NextRun returns when the next scheduled backup starts, in the server's local
// time, and false while the schedule is off.
func (b *Backups) NextRun() (time.Time, bool) {
	b.schedMu.Lock()
	scheduled, hour, minute := b.scheduled, b.hour, b.minute
	b.schedMu.Unlock()
	if !scheduled {
		return time.Time{}, false
	}
	return nextRun(b.now(), hour, minute), true
}

// nextRun returns the first hour:minute in now's location strictly after now.
// A repeated wall time runs at its first occurrence. A skipped one runs as
// far past the gap as it fell inside it, so 02:30 on a day that jumps from
// 02:00 to 03:00 runs at 03:30.
func nextRun(now time.Time, hour, minute int) time.Time {
	y, m, d := now.Date()
	next := wallTime(y, m, d, hour, minute, now.Location())
	if !next.After(now) {
		next = wallTime(y, m, d+1, hour, minute, now.Location())
	}
	return next
}

func wallTime(y int, m time.Month, d, hour, minute int, loc *time.Location) time.Time {
	t := time.Date(y, m, d, hour, minute, 0, 0, loc)
	// time.Date resolves a skipped wall time to either side of the gap: before
	// it in zones west of UTC, after it in zones east. Move a time that landed
	// before the gap forward by the wall-clock shortfall. Never move it back:
	// a gap at midnight would put the run on the previous day.
	want := time.Date(y, m, d, hour, minute, 0, 0, time.UTC)
	got := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	if short := want.Sub(got); short > 0 {
		t = t.Add(short)
	}
	// A repeated wall time resolves to its second occurrence in zones east of
	// UTC. When the offset dropped shortly before t and the instant that much
	// earlier shows the same wall clock, that instant is the first occurrence.
	_, off := t.Zone()
	_, offBefore := t.Add(-12 * time.Hour).Zone()
	if back := time.Duration(offBefore-off) * time.Second; back > 0 {
		if first := t.Add(-back); first.Format(time.DateTime) == t.Format(time.DateTime) {
			t = first
		}
	}
	return t
}

// Create writes a new backup and prunes old ones beyond the retention count.
func (b *Backups) Create(ctx context.Context, reason Reason) (Entry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.isRestartPending() {
		return Entry{}, ErrRestartPending
	}
	entry, err := b.createLocked(ctx, reason)
	if err == nil {
		b.prune("")
	}
	return entry, err
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

	m := manifest{Version: manifestVersion, ID: id, CreatedAt: createdAt, Reason: reason, AuthTree: true}
	tmpArchive := filepath.Join(staging, id+archiveExt)
	if err := writeArchive(tmpArchive, &m, sources); err != nil {
		return Entry{}, err
	}
	final := filepath.Join(b.dir, id+archiveExt)
	if err := os.Rename(tmpArchive, final); err != nil {
		return Entry{}, fmt.Errorf("move backup into place: %w", err)
	}
	syncRenamed(b.dir)
	info, err := os.Stat(final)
	if err != nil {
		return Entry{}, fmt.Errorf("stat backup: %w", err)
	}
	return Entry{ID: id, CreatedAt: createdAt, Bytes: info.Size(), Reason: reason, Files: fileCount(m)}, nil
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

// collect reads config and auth files and the refresh journal into memory.
// They are small, and holding them keeps the archive consistent with its
// manifest even when a token refresh rewrites a file mid-backup. The journal
// is read after the credentials, so a claim on any refresh token the backup
// holds, made before that token was read, is in the backup too.
func (b *Backups) collect() ([]source, error) {
	var sources []source
	for _, f := range []struct{ name, path string }{
		{configEntry, b.paths.ConfigFile},
		{taprootEntry, b.paths.TaprootFile},
	} {
		if f.path == "" {
			continue
		}
		data, err := readSmallFile(f.path, f.name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sources = append(sources, source{name: f.name, data: data})
	}
	auth, err := b.authFiles()
	if err != nil {
		return nil, err
	}
	for rel, path := range auth {
		data, err := readSmallFile(path, "auth file "+rel)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sources = append(sources, source{name: authPrefix + rel, data: data})
	}
	journal, err := b.journalSources()
	if err != nil {
		return nil, err
	}
	sources = append(sources, journal...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].name < sources[j].name })
	return sources, nil
}

// readSmallFile reads a config, auth, or journal file. One larger than a
// restore reads fails the backup now, rather than making it unrestorable.
func readSmallFile(path, what string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("read %s: %w", what, err)
	}
	if len(data) > maxSmallFileBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d-byte limit a restore reads", what, len(data), maxSmallFileBytes)
	}
	return data, nil
}

// authRoot returns the absolute auth dir with symlinks resolved, or "" when
// none is configured.
func (b *Backups) authRoot() (string, error) {
	if b.paths.AuthDir == "" {
		return "", nil
	}
	root, err := filepath.Abs(b.paths.AuthDir)
	if err != nil {
		return "", fmt.Errorf("resolve auth dir: %w", err)
	}
	if resolved, errResolve := filepath.EvalSymlinks(root); errResolve == nil {
		root = resolved
	}
	return root, nil
}

// journalSources reads every directory and regular file of upstream's Codex
// refresh journal. Upstream says restoring Codex credentials without their
// journal is unsafe: a refresh token the journal records as sent must never
// be sent again.
func (b *Backups) journalSources() ([]source, error) {
	root, err := b.authRoot()
	if err != nil || root == "" {
		return nil, err
	}
	dir := filepath.Join(root, journalDir)
	if resolved, errResolve := filepath.EvalSymlinks(dir); errResolve == nil {
		dir = resolved
	}
	var sources []source
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return filepath.SkipDir
			}
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := journalPrefix
		if rel != "." {
			name += "/" + filepath.ToSlash(rel)
		}
		if d.IsDir() {
			sources = append(sources, source{name: name, dir: true})
			return nil
		}
		if !d.Type().IsRegular() || rel == "." {
			return nil
		}
		data, err := readSmallFile(path, "refresh journal "+rel)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		sources = append(sources, source{name: name, data: data})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk refresh journal: %w", err)
	}
	return sources, nil
}

// authFiles maps credential file names to their paths: every *.json file
// under the auth dir, as upstream's startup load reads them, including
// symlinked files and files in subdirectories. It leaves out the refresh
// journal, which is backed up on its own, and the data dir, which may sit
// inside the auth dir. A subdirectory that cannot be read is skipped with a
// warning, so it cannot stop every backup.
func (b *Backups) authFiles() (map[string]string, error) {
	files := map[string]string{}
	root, err := b.authRoot()
	if err != nil || root == "" {
		return files, err
	}
	skip := map[string]bool{filepath.Join(root, journalDir): true}
	if dataDir, errAbs := filepath.Abs(b.paths.DataDir); errAbs == nil {
		if resolved, errResolve := filepath.EvalSymlinks(dataDir); errResolve == nil {
			dataDir = resolved
		}
		// When the auth dir is the data dir, skipping it would skip the
		// whole walk.
		if dataDir != root {
			skip[dataDir] = true
		}
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if path == root {
				if errors.Is(err, os.ErrNotExist) {
					return filepath.SkipDir
				}
				return err
			}
			log.WithError(err).WithField("path", path).Warn("taproot backups: skip unreadable part of the auth dir")
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skip[path] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		if !d.Type().IsRegular() {
			if info, errStat := os.Stat(path); errStat != nil || !info.Mode().IsRegular() {
				return nil
			}
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

// prune keeps the newest keep backups of each reason, so a burst of manual
// backups or restores cannot evict the scheduled history. protect is never
// removed; a restore passes the archive it restored.
func (b *Backups) prune(protect string) {
	entries, err := b.List()
	if err != nil {
		log.WithError(err).Warn("taproot backups: list for retention")
		return
	}
	_, keep := b.Schedule()
	seen := map[Reason]int{}
	for _, e := range entries {
		seen[e.Reason]++
		if seen[e.Reason] <= keep || e.ID == protect {
			continue
		}
		if errRemove := os.Remove(filepath.Join(b.dir, e.ID+archiveExt)); errRemove != nil {
			log.WithError(errRemove).WithField("id", e.ID).Warn("taproot backups: prune")
			continue
		}
		log.WithField("id", e.ID).Info("taproot backups: pruned")
	}
}

// manifestCount is an archive's file count and the size and modification
// time it was read at. Archives are never rewritten in place.
type manifestCount struct {
	size    int64
	modTime time.Time
	files   int
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
	b.manifestsMu.Lock()
	counts := b.manifests
	b.manifestsMu.Unlock()
	seen := make(map[string]manifestCount, len(counts))
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
		key := manifestCount{size: info.Size(), modTime: info.ModTime()}
		if cached, ok := counts[name]; ok && cached.size == key.size && cached.modTime.Equal(key.modTime) {
			e.Files = cached.files
			key.files = cached.files
		} else if m, errManifest := readManifest(filepath.Join(b.dir, name)); errManifest == nil {
			e.Files = fileCount(m)
			key.files = e.Files
		} else {
			log.WithError(errManifest).WithField("id", id).Warn("taproot backups: unreadable manifest")
		}
		seen[name] = key
		found = append(found, keyed{Entry: e, seq: seq})
	}
	b.manifestsMu.Lock()
	b.manifests = seen
	b.manifestsMu.Unlock()
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
