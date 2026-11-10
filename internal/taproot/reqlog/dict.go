package reqlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
	"github.com/klauspost/compress/zstd"
	log "github.com/sirupsen/logrus"
)

// Every segment starts cold, and the first record of a session carries the
// same system prompt, tool schemas, and instructions as every other session's.
// Segments compress against a shared raw-content dictionary built from the
// first frames of recent segments, which cut the compressed stream by a sixth
// on production data. Dictionaries are rebuilt weekly so they follow client
// updates. Each is an immutable file, dicts/<id>.dict, kept while a segment
// uses it; the id is also in every zstd frame header, so a restored database
// that is older than the segments can still decode them.
const (
	dictMaxBytes      = 1 << 20
	dictRetrainEvery  = 7 * 24 * time.Hour
	dictMinSegments   = 32
	dictCandidateSegs = 256
)

type dictStore struct {
	db  *store.DB
	dir string

	mu    sync.Mutex
	cache map[int64][]byte
}

func newDictStore(db *store.DB, dir string) *dictStore {
	return &dictStore{db: db, dir: dir, cache: make(map[int64][]byte)}
}

func (d *dictStore) path(id int64) string {
	return filepath.Join(d.dir, strconv.FormatInt(id, 10)+".dict")
}

// get returns a dictionary's content; id 0 is no dictionary.
func (d *dictStore) get(id int64) ([]byte, error) {
	if id == 0 {
		return nil, nil
	}
	d.mu.Lock()
	content, ok := d.cache[id]
	d.mu.Unlock()
	if ok {
		return content, nil
	}
	content, err := os.ReadFile(d.path(id))
	if err != nil {
		return nil, fmt.Errorf("load dictionary %d: %w", id, err)
	}
	d.mu.Lock()
	d.cache[id] = content
	d.mu.Unlock()
	return content, nil
}

// ids lists stored dictionaries, oldest first.
func (d *dictStore) ids() ([]int64, error) {
	files, err := os.ReadDir(d.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list dictionaries: %w", err)
	}
	var ids []int64
	for _, f := range files {
		stem, ext, _ := strings.Cut(f.Name(), ".")
		if id, err := strconv.ParseInt(stem, 10, 64); err == nil && id > 0 && ext == "dict" {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

// current returns the newest dictionary for sealing, or none when it is older
// than maxAge (0 means no limit).
func (d *dictStore) current(now time.Time, maxAge time.Duration) (int64, []byte, error) {
	ids, err := d.ids()
	if err != nil || len(ids) == 0 {
		return 0, nil, err
	}
	id := ids[len(ids)-1]
	if d.expired(id, now, maxAge) {
		return 0, nil, nil
	}
	content, err := d.get(id)
	return id, content, err
}

// age is how long ago a dictionary was written.
func (d *dictStore) age(id int64, now time.Time) (time.Duration, bool) {
	info, err := os.Stat(d.path(id))
	if err != nil {
		return 0, false
	}
	return now.Sub(info.ModTime()), true
}

// expired reports whether a dictionary is at least maxAge old; 0 means no
// limit.
func (d *dictStore) expired(id int64, now time.Time, maxAge time.Duration) bool {
	if maxAge <= 0 {
		return false
	}
	age, ok := d.age(id, now)
	return !ok || age >= maxAge
}

// newestAge is how long ago the newest dictionary was written.
func (d *dictStore) newestAge(now time.Time) (time.Duration, bool) {
	ids, err := d.ids()
	if err != nil || len(ids) == 0 {
		return 0, false
	}
	return d.age(ids[len(ids)-1], now)
}

// bytes is the size of every stored dictionary.
func (d *dictStore) bytes() (int64, error) {
	ids, err := d.ids()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, id := range ids {
		info, err := os.Stat(d.path(id))
		if err != nil {
			return 0, fmt.Errorf("stat dictionary %d: %w", id, err)
		}
		total += info.Size()
	}
	return total, nil
}

// add writes a new dictionary under the next id. Only the compactor calls
// it, so two calls never pick the same id.
func (d *dictStore) add(content []byte) (int64, error) {
	ids, err := d.ids()
	if err != nil {
		return 0, err
	}
	id := int64(1)
	if len(ids) > 0 {
		id = ids[len(ids)-1] + 1
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return 0, fmt.Errorf("create dictionary dir: %w", err)
	}
	tmp := d.path(id) + ".tmp"
	if err := writeSynced(tmp, content); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("write dictionary: %w", err)
	}
	if err := os.Rename(tmp, d.path(id)); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("publish dictionary: %w", err)
	}
	return id, nil
}

func writeSynced(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, errWrite := f.Write(content)
	errSync := f.Sync()
	errClose := f.Close()
	return errors.Join(errWrite, errSync, errClose)
}

// prune removes dictionaries no segment uses. The newest stays for the next
// seal while it is younger than maxAge (0 means no limit) and a sealed
// segment exists; a dictionary holds record text, so it must not outlive the
// records it came from. prune runs on the compactor goroutine, which also
// seals, so no seal can pick a dictionary prune is removing.
func (d *dictStore) prune(ctx context.Context, now time.Time, maxAge time.Duration) error {
	ids, err := d.ids()
	if err != nil || len(ids) == 0 {
		return err
	}
	used, err := queryIDs(ctx, d.db, `SELECT DISTINCT dict_id FROM reqlog_segments`)
	if err != nil {
		return err
	}
	var sealed bool
	if err := d.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM reqlog_segments WHERE state = ?)`, segmentSealed).Scan(&sealed); err != nil {
		return fmt.Errorf("find sealed segments: %w", err)
	}
	newest := ids[len(ids)-1]
	keepNewest := sealed && !d.expired(newest, now, maxAge)
	for _, id := range ids {
		if slices.Contains(used, id) || id == newest && keepNewest {
			continue
		}
		if err := os.Remove(d.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove dictionary %d: %w", id, err)
		}
		d.mu.Lock()
		delete(d.cache, id)
		d.mu.Unlock()
	}
	return nil
}

// dictBuilder concatenates candidates, newest first in, into at most size
// bytes, skipping one that adds under a quarter of its standalone compressed
// size when compressed after what is already kept. The newest content ends up
// last, nearest the data in zstd's history.
type dictBuilder struct {
	enc   *zstd.Encoder
	size  int
	kept  []byte
	keptZ int
}

func newDictBuilder(size int) (*dictBuilder, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithWindowSize(8<<20), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	return &dictBuilder{enc: enc, size: size}, nil
}

func (d *dictBuilder) full() bool { return len(d.kept) >= d.size }

func (d *dictBuilder) add(c []byte) {
	if d.full() {
		return
	}
	alone := len(d.enc.EncodeAll(c, nil))
	joined := append(append(make([]byte, 0, len(d.kept)+len(c)), d.kept...), c...)
	if withZ := len(d.enc.EncodeAll(joined, nil)); len(d.kept) > 0 && (withZ-d.keptZ)*4 < alone {
		return
	}
	if room := d.size - len(d.kept); len(c) > room {
		c = c[len(c)-room:]
	}
	d.kept = append(append(make([]byte, 0, len(c)+len(d.kept)), c...), d.kept...)
	d.keptZ = len(d.enc.EncodeAll(d.kept, nil))
}

func (d *dictBuilder) content() []byte {
	_ = d.enc.Close()
	return d.kept
}

// trainDict builds a dictionary from the first frames of recent sealed
// segments and stores it.
func (e *engine) trainDict(ctx context.Context) (int64, error) {
	rows, err := e.db.QueryContext(ctx, `SELECT s.id, s.day, s.state, s.dict_id, MIN(r.offset), r.length
		FROM reqlog_segments s JOIN reqlog_records r ON r.segment_id = s.id
		WHERE s.state = ? GROUP BY s.id ORDER BY s.sealed_at DESC, s.id DESC LIMIT ?`, segmentSealed, dictCandidateSegs)
	if err != nil {
		return 0, fmt.Errorf("find dictionary candidates: %w", err)
	}
	var locs []recordLocation
	for rows.Next() {
		var loc recordLocation
		if err := rows.Scan(&loc.segment, &loc.day, &loc.state, &loc.dictID, &loc.offset, &loc.length); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan dictionary candidate: %w", err)
		}
		locs = append(locs, loc)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("find dictionary candidates: %w", err)
	}
	if len(locs) < dictMinSegments {
		return 0, nil
	}
	b, err := newDictBuilder(dictMaxBytes)
	if err != nil {
		return 0, err
	}
	for _, loc := range locs {
		if b.full() {
			break
		}
		fr, err := e.readFrame(ctx, loc)
		if err != nil {
			log.WithError(err).WithField("segment", loc.segment).Debug("request logs: read dictionary candidate")
			continue
		}
		b.add(fr)
	}
	content := b.content()
	if len(content) < 4096 {
		return 0, nil
	}
	return e.dicts.add(content)
}

// maybeTrainDict retrains when the newest dictionary is a week old, or older
// than max-days, or when none exists and enough segments have sealed.
func (e *engine) maybeTrainDict(ctx context.Context) {
	now := e.now()
	every := dictRetrainEvery
	if maxAge := e.dictMaxAge(); maxAge > 0 {
		every = min(every, maxAge)
	}
	if age, ok := e.dicts.newestAge(now); ok && age < every || now.Sub(e.dictTried) < time.Hour {
		return
	}
	e.dictTried = now
	if _, err := e.trainDict(ctx); err != nil {
		log.WithError(err).Warn("request logs: train dictionary")
	}
}
