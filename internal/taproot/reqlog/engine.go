package reqlog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/265866/taproot/internal/taproot/store"
	"github.com/klauspost/compress/zstd"
	log "github.com/sirupsen/logrus"
)

// Storage layout under <data-dir>/request-logs:
//
//	<YYYY-MM-DD>/<segment id>.raw   open segment, append-only frames
//	<YYYY-MM-DD>/<segment id>.zst   sealed segment, one zstd frame
//	tmp/                            spooled bodies of in-flight requests
//
// One segment holds one session's records for one UTC day. Records stay raw
// while the session is active and are compressed in one pass when the segment
// seals, so a single encoder runs at a time no matter how many sessions are
// open. A sealed file is written to a temp name, verified, and renamed; it is
// never modified afterwards.
//
// An unclean stop loses records still in the in-memory queue and a frame cut
// off mid-write. Complete frames in open segments carry their own metadata and
// are re-indexed when the next start seals those segments.
const (
	segmentOpen    = "open"
	segmentSealing = "sealing"
	segmentSealed  = "sealed"

	// zstdWindow covers the previous request of the same session even for
	// 32 MB bodies, which is where Claude Code's redundancy lives. Sealing
	// holds about twice this in memory and a lookup about once.
	zstdWindow = 64 << 20

	queueEntries   = 8192
	writeBatchSize = 256
	maxReaders     = 2
)

type segState struct {
	id        int64
	sessionID string
	day       string
	file      *os.File
	size      int64
	createdAt time.Time
	lastWrite time.Time
	firstTs   int64
	lastTs    int64
}

// entry is one queued record, or a barrier when done is set. A barrier may
// carry work that must run on the writer goroutine.
type entry struct {
	capture  *capture
	reserved int64
	done     chan struct{}
	run      func()
}

type indexRow struct {
	meta    *recordMeta
	segment int64
	offset  int64
	length  int
}

type counters struct {
	droppedBodies  atomic.Int64
	droppedRecords atomic.Int64
	truncated      atomic.Int64
	writeErrors    atomic.Int64
}

type engine struct {
	db  *store.DB
	dir string
	cfg Config
	now func() time.Time

	retention atomic.Pointer[Config]

	queue   chan *entry
	budget  budget
	stats   counters
	readSem chan struct{}

	// open and pending belong to the writer goroutine.
	open    map[string]*segState
	pending []indexRow

	sealMu    sync.Mutex
	sealQueue []int64
	sealWake  chan struct{}

	// retries belongs to the compactor: segments whose compaction failed,
	// with the attempt count and when to try again.
	retries map[int64]compactRetry

	sweepNeeded atomic.Bool
	startOnce   sync.Once
	stopped     chan struct{}
	wg          sync.WaitGroup
}

func newEngine(ctx context.Context, db *store.DB, dir string, cfg Config, now func() time.Time) (*engine, error) {
	e := &engine{
		db:       db,
		dir:      dir,
		cfg:      cfg,
		now:      now,
		queue:    make(chan *entry, queueEntries),
		budget:   budget{limit: int64(cfg.QueueBytes)},
		readSem:  make(chan struct{}, maxReaders),
		open:     make(map[string]*segState),
		sealWake: make(chan struct{}, 1),
		retries:  make(map[int64]compactRetry),
		stopped:  make(chan struct{}),
	}
	retention := cfg
	e.retention.Store(&retention)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create request log dir: %w", err)
	}
	if err := os.RemoveAll(e.tmpDir()); err != nil {
		return nil, fmt.Errorf("clear request log temp dir: %w", err)
	}
	if err := os.MkdirAll(e.tmpDir(), 0o700); err != nil {
		return nil, fmt.Errorf("create request log temp dir: %w", err)
	}
	if err := e.recover(ctx); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *engine) tmpDir() string { return filepath.Join(e.dir, "tmp") }

func (e *engine) segmentPath(day string, id int64, ext string) string {
	return filepath.Join(e.dir, day, strconv.FormatInt(id, 10)+ext)
}

func utcDay(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.DateOnly) }

// budget bounds bytes held for logging without ever waiting.
type budget struct {
	limit int64
	used  atomic.Int64
}

func (b *budget) reserve(n int64) bool {
	for {
		cur := b.used.Load()
		if cur+n > b.limit {
			return false
		}
		if b.used.CompareAndSwap(cur, cur+n) {
			return true
		}
	}
}

func (b *budget) release(n int64) { b.used.Add(-n) }

// recordOverhead approximates what a record costs beyond its bodies.
const recordOverhead = 8 << 10

// submit queues a capture without blocking. When the budget is short it keeps
// the metadata and drops bodies; when even that fails the record is counted
// and discarded.
func (e *engine) submit(c *capture) {
	select {
	case <-e.stopped:
		e.stats.droppedRecords.Add(1)
		return
	default:
	}
	size := int64(c.bodyBytes()) + recordOverhead
	if !e.budget.reserve(size) {
		c.identify()
		c.dropBodies("log queue full")
		e.stats.droppedBodies.Add(1)
		size = recordOverhead
		if !e.budget.reserve(size) {
			e.stats.droppedRecords.Add(1)
			return
		}
	}
	select {
	case e.queue <- &entry{capture: c, reserved: size}:
	default:
		e.budget.release(size)
		e.stats.droppedRecords.Add(1)
	}
}

// flush waits until everything queued before it is written and indexed.
func (e *engine) flush(ctx context.Context) error { return e.do(ctx, nil) }

// do runs fn on the writer goroutine after everything queued before it.
func (e *engine) do(ctx context.Context, fn func()) error {
	done := make(chan struct{})
	select {
	case e.queue <- &entry{done: done, run: fn}:
	case <-e.stopped:
		return errors.New("request log writer stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *engine) start(ctx context.Context) {
	e.startOnce.Do(func() {
		e.wg.Add(2)
		go e.runWriter(ctx)
		go e.runCompactor(ctx)
	})
}

// startWriter runs only the writer; tests drive sealing and retention
// directly so they stay deterministic.
func (e *engine) startWriter(ctx context.Context) {
	e.startOnce.Do(func() {
		e.wg.Add(1)
		go e.runWriter(ctx)
	})
}

func (e *engine) wait() { e.wg.Wait() }

func (e *engine) runWriter(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			close(e.stopped)
			e.drain()
			e.closeOpenFiles()
			return
		case first := <-e.queue:
			batch := []*entry{first}
			for len(batch) < writeBatchSize {
				select {
				case next := <-e.queue:
					batch = append(batch, next)
					continue
				default:
				}
				break
			}
			e.writeBatch(batch)
		case <-ticker.C:
			e.sealDue(e.now())
		}
	}
}

func (e *engine) drain() {
	for {
		select {
		case next := <-e.queue:
			e.writeBatch([]*entry{next})
		default:
			return
		}
	}
}

func (e *engine) closeOpenFiles() {
	for key, seg := range e.open {
		if err := seg.file.Close(); err != nil {
			log.WithError(err).Warn("request logs: close open segment")
		}
		delete(e.open, key)
	}
}

func (e *engine) writeBatch(batch []*entry) {
	for _, ent := range batch {
		if ent.done == nil {
			e.writeOne(ent)
			e.budget.release(ent.reserved)
			continue
		}
		e.commitPending()
		if ent.run != nil {
			ent.run()
		}
		close(ent.done)
	}
	e.commitPending()
}

func (e *engine) writeOne(ent *entry) {
	meta, f, err := ent.capture.build()
	if err != nil {
		e.stats.writeErrors.Add(1)
		log.WithError(err).Warn("request logs: build record")
		return
	}
	day := utcDay(meta.TimeMs)
	seg, err := e.segmentFor(meta.SessionID, day)
	if err == nil && seg.size > 0 && seg.size+int64(f.size()) > int64(e.cfg.SegmentMaxBytes) {
		e.commitPending()
		e.seal(seg)
		seg, err = e.segmentFor(meta.SessionID, day)
	}
	if err != nil {
		e.stats.writeErrors.Add(1)
		log.WithError(err).Warn("request logs: open segment")
		return
	}
	offset := seg.size
	if err := f.writeTo(seg.file); err != nil {
		e.stats.writeErrors.Add(1)
		log.WithError(err).Warn("request logs: write record")
		// The tail may now hold a torn frame; sealing truncates to the last
		// complete one and new records go to a fresh segment.
		e.seal(seg)
		return
	}
	seg.size += int64(f.size())
	seg.lastWrite = e.now()
	if seg.firstTs == 0 || meta.TimeMs < seg.firstTs {
		seg.firstTs = meta.TimeMs
	}
	if meta.TimeMs > seg.lastTs {
		seg.lastTs = meta.TimeMs
	}
	e.pending = append(e.pending, indexRow{meta: meta, segment: seg.id, offset: offset, length: f.size()})
}

// maxPendingRows bounds index rows kept for retry while the database fails.
const maxPendingRows = 16384

// commitPending indexes written records. On failure, rows for segments still
// open stay pending and are retried with the next batch. Every frame is
// re-indexed from the raw file when its segment seals, so rows for segments
// no longer open are dropped: retrying them is redundant, and a row whose
// segment retention deleted would fail its foreign key on every attempt.
func (e *engine) commitPending() {
	if len(e.pending) == 0 {
		return
	}
	if err := insertRows(context.Background(), e.db, e.pending); err != nil {
		log.WithError(err).Warn("request logs: index records")
		openIDs := make(map[int64]bool, len(e.open))
		for _, seg := range e.open {
			openIDs[seg.id] = true
		}
		kept := e.pending[:0]
		for _, r := range e.pending {
			if openIDs[r.segment] {
				kept = append(kept, r)
			}
		}
		if over := len(kept) - maxPendingRows; over > 0 {
			kept = append(kept[:0], kept[over:]...)
		}
		clear(e.pending[len(kept):])
		e.pending = kept
		return
	}
	clear(e.pending)
	e.pending = e.pending[:0]
}

func (e *engine) segmentFor(sessionID, day string) (*segState, error) {
	key := sessionID + "\x00" + day
	if seg := e.open[key]; seg != nil {
		return seg, nil
	}
	now := e.now()
	res, err := e.db.Exec(`INSERT INTO request_log_segments (session_id, day, state, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, day, segmentOpen, now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("insert segment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("segment id: %w", err)
	}
	path := e.segmentPath(day, id, ".raw")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create day dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create segment: %w", err)
	}
	seg := &segState{id: id, sessionID: sessionID, day: day, file: file, createdAt: now, lastWrite: now}
	e.open[key] = seg
	return seg, nil
}

// seal stops writing to a segment and hands it to the compactor.
func (e *engine) seal(seg *segState) {
	delete(e.open, seg.sessionID+"\x00"+seg.day)
	if err := seg.file.Close(); err != nil {
		log.WithError(err).Warn("request logs: close segment")
	}
	if _, err := e.db.Exec(`UPDATE request_log_segments SET state = ? WHERE id = ?`, segmentSealing, seg.id); err != nil {
		log.WithError(err).Warn("request logs: mark segment sealing")
	}
	e.enqueueSeal(seg.id)
}

func (e *engine) enqueueSeal(id int64) {
	e.sealMu.Lock()
	e.sealQueue = append(e.sealQueue, id)
	e.sealMu.Unlock()
	select {
	case e.sealWake <- struct{}{}:
	default:
	}
}

func (e *engine) sealDue(now time.Time) {
	today := now.UTC().Format(time.DateOnly)
	var total int64
	for _, seg := range e.open {
		if seg.day != today || now.Sub(seg.lastWrite) >= e.cfg.SealIdle || now.Sub(seg.createdAt) >= e.cfg.SealAge {
			e.seal(seg)
			continue
		}
		total += seg.size
	}
	for total > int64(e.cfg.OpenMaxBytes) {
		var largest *segState
		for _, seg := range e.open {
			if largest == nil || seg.size > largest.size {
				largest = seg
			}
		}
		if largest == nil {
			return
		}
		total -= largest.size
		e.seal(largest)
	}
}

// sealAll seals every open segment; it must run on the writer goroutine.
func (e *engine) sealAll() {
	for _, seg := range e.open {
		e.seal(seg)
	}
}

func (e *engine) runCompactor(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		e.compactQueued(ctx)
		select {
		case <-ctx.Done():
			return
		case <-e.sealWake:
		case <-ticker.C:
			e.requeueFailed(ctx)
			e.applyRetention(ctx)
		}
	}
}

type compactRetry struct {
	attempts int
	next     time.Time
}

const (
	compactRetryBase = time.Minute
	compactRetryMax  = time.Hour
)

func (e *engine) compactQueued(ctx context.Context) {
	for ctx.Err() == nil {
		e.sealMu.Lock()
		if len(e.sealQueue) == 0 {
			e.sealMu.Unlock()
			return
		}
		id := e.sealQueue[0]
		e.sealQueue = e.sealQueue[1:]
		e.sealMu.Unlock()
		if err := e.compact(ctx, id); err != nil {
			r := e.retries[id]
			r.attempts++
			r.next = e.now().Add(min(compactRetryBase<<min(r.attempts-1, 6), compactRetryMax))
			e.retries[id] = r
			log.WithError(err).WithField("segment", id).WithField("attempts", r.attempts).Warn("request logs: seal segment")
			continue
		}
		delete(e.retries, id)
	}
}

// requeueFailed queues segments left in the sealing state by a failed
// compaction once their backoff has passed. Until they seal, retention cannot
// prune them and their raw bytes count against max-bytes.
func (e *engine) requeueFailed(ctx context.Context) {
	if len(e.retries) == 0 {
		return
	}
	now := e.now()
	var due []int64
	for id, r := range e.retries {
		if !now.Before(r.next) {
			due = append(due, id)
		}
	}
	for _, id := range due {
		var state string
		err := e.db.QueryRowContext(ctx, `SELECT state FROM request_log_segments WHERE id = ?`, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && state != segmentSealing) {
			delete(e.retries, id)
			continue
		}
		if err != nil {
			log.WithError(err).WithField("segment", id).Warn("request logs: check failed segment")
			continue
		}
		e.enqueueSeal(id)
	}
}

// compact compresses a closed raw segment. The raw file stays authoritative
// until the compressed copy is verified and recorded as sealed.
func (e *engine) compact(ctx context.Context, id int64) error {
	var day, state string
	if err := e.db.QueryRowContext(ctx, `SELECT day, state FROM request_log_segments WHERE id = ?`, id).Scan(&day, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load segment: %w", err)
	}
	if state == segmentSealed {
		return nil
	}
	rawPath := e.segmentPath(day, id, ".raw")
	zstPath := e.segmentPath(day, id, ".zst")
	validEnd, err := e.reindexRaw(ctx, id, rawPath)
	if err != nil {
		return err
	}
	if validEnd == 0 {
		return e.deleteSegments(ctx, []int64{id})
	}
	tmpPath := zstPath + ".tmp"
	diskBytes, err := compressFile(rawPath, tmpPath, validEnd)
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, zstPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish sealed segment: %w", err)
	}
	if _, err := e.db.ExecContext(ctx, `UPDATE request_log_segments SET state = ?, disk_bytes = ?, raw_bytes = ?, sealed_at = ? WHERE id = ?`,
		segmentSealed, diskBytes, validEnd, e.now().UnixMilli(), id); err != nil {
		return fmt.Errorf("mark segment sealed: %w", err)
	}
	if err := os.Remove(rawPath); err != nil {
		// Windows refuses while a reader holds the file; the sweep retries.
		e.sweepNeeded.Store(true)
		log.WithError(err).Debug("request logs: remove raw segment")
	}
	return nil
}

func compressFile(rawPath, outPath string, size int64) (int64, error) {
	in, err := os.Open(rawPath)
	if err != nil {
		return 0, fmt.Errorf("open raw segment: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create sealed segment: %w", err)
	}
	closeOut := func() error {
		if out == nil {
			return nil
		}
		errClose := out.Close()
		out = nil
		return errClose
	}
	defer func() { _ = closeOut() }()

	enc, err := zstd.NewWriter(out,
		zstd.WithEncoderLevel(zstd.SpeedBetterCompression),
		zstd.WithWindowSize(windowFor(size)),
		zstd.WithEncoderConcurrency(1))
	if err != nil {
		return 0, fmt.Errorf("create encoder: %w", err)
	}
	sum := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	if _, err := io.Copy(enc, io.TeeReader(io.LimitReader(in, size), sum)); err != nil {
		_ = enc.Close()
		return 0, fmt.Errorf("compress segment: %w", err)
	}
	if err := enc.Close(); err != nil {
		return 0, fmt.Errorf("finish segment: %w", err)
	}
	if err := out.Sync(); err != nil {
		return 0, fmt.Errorf("sync sealed segment: %w", err)
	}
	info, err := out.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat sealed segment: %w", err)
	}
	if err := closeOut(); err != nil {
		return 0, fmt.Errorf("close sealed segment: %w", err)
	}
	if err := verifyCompressed(outPath, size, sum.Sum32()); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func verifyCompressed(path string, size int64, want uint32) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open sealed segment for verify: %w", err)
	}
	defer func() { _ = f.Close() }()
	dec, err := newDecoder(f)
	if err != nil {
		return err
	}
	defer dec.Close()
	sum := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	n, err := io.Copy(sum, dec)
	if err != nil {
		return fmt.Errorf("verify sealed segment: %w", err)
	}
	if n != size || sum.Sum32() != want {
		return fmt.Errorf("sealed segment mismatch: %d of %d bytes", n, size)
	}
	return nil
}

func newDecoder(r io.Reader) (*zstd.Decoder, error) {
	dec, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(zstdWindow))
	if err != nil {
		return nil, fmt.Errorf("create decoder: %w", err)
	}
	return dec, nil
}

func windowFor(size int64) int {
	window := 1 << 20
	if size > int64(window) {
		window = 1 << (64 - bits.LeadingZeros64(uint64(size-1)))
	}
	return min(window, zstdWindow)
}

// reindexRaw scans a raw segment, indexes any complete frame the database
// missed, cuts a torn tail, and returns the length of the valid prefix.
func (e *engine) reindexRaw(ctx context.Context, id int64, rawPath string) (int64, error) {
	f, err := os.OpenFile(rawPath, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open raw segment: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat raw segment: %w", err)
	}
	var rows []indexRow
	var firstTs, lastTs int64
	validEnd, err := scanFrames(f, 0, info.Size(), func(offset int64, length int, meta *recordMeta) {
		rows = append(rows, indexRow{meta: meta, segment: id, offset: offset, length: length})
		if firstTs == 0 || meta.TimeMs < firstTs {
			firstTs = meta.TimeMs
		}
		lastTs = max(lastTs, meta.TimeMs)
	})
	if err != nil {
		return 0, fmt.Errorf("scan raw segment: %w", err)
	}
	if info.Size() > validEnd {
		if err := f.Truncate(validEnd); err != nil {
			return 0, fmt.Errorf("truncate torn segment tail: %w", err)
		}
	}
	if err := insertRows(ctx, e.db, rows); err != nil {
		return 0, err
	}
	if _, err := e.db.ExecContext(ctx, `DELETE FROM request_logs WHERE segment_id = ? AND offset + length > ?`, id, validEnd); err != nil {
		return 0, fmt.Errorf("drop index rows past segment end: %w", err)
	}
	if _, err := e.db.ExecContext(ctx, `UPDATE request_log_segments SET raw_bytes = ?, first_ts = ?, last_ts = ? WHERE id = ?`,
		validEnd, firstTs, lastTs, id); err != nil {
		return 0, fmt.Errorf("update segment size: %w", err)
	}
	return validEnd, nil
}

// recover runs before the writer starts. Segments left open by the last run
// are re-indexed from their frames and queued for sealing; sealed segments are
// untouched apart from removing raw files a previous run could not delete.
func (e *engine) recover(ctx context.Context) error {
	rows, err := e.db.QueryContext(ctx, `SELECT id, day, state FROM request_log_segments`)
	if err != nil {
		return fmt.Errorf("list segments: %w", err)
	}
	type seg struct {
		id         int64
		day, state string
	}
	var segs []seg
	for rows.Next() {
		var s seg
		if err := rows.Scan(&s.id, &s.day, &s.state); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan segment: %w", err)
		}
		segs = append(segs, s)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("list segments: %w", err)
	}
	// A restored database can be older than the files: segments created
	// after its snapshot have ids past its sequence. New ids must not collide
	// with them, and the sweep below then removes them as unowned.
	if err := e.reserveDiskIDs(ctx); err != nil {
		return err
	}
	for _, s := range segs {
		if s.state == segmentSealed {
			continue
		}
		zstPath := e.segmentPath(s.day, s.id, ".zst")
		_ = os.Remove(zstPath + ".tmp")
		// A restored database may list as open a segment that sealed after
		// its snapshot; the sealed copy is then the only copy.
		if err := restoreRawFromSealed(e.segmentPath(s.day, s.id, ".raw"), zstPath); err != nil {
			log.WithError(err).WithField("segment", s.id).Warn("request logs: recover sealed copy of open segment")
		}
		_ = os.Remove(zstPath)
		if _, err := e.db.ExecContext(ctx, `UPDATE request_log_segments SET state = ? WHERE id = ?`, segmentSealing, s.id); err != nil {
			return fmt.Errorf("mark segment sealing: %w", err)
		}
		e.sealQueue = append(e.sealQueue, s.id)
	}
	e.sweep(ctx)
	return nil
}

// reserveDiskIDs raises the segment id sequence to the largest id on disk.
func (e *engine) reserveDiskIDs(ctx context.Context) error {
	var maxDisk int64
	days, err := os.ReadDir(e.dir)
	if err != nil {
		return fmt.Errorf("list request log dir: %w", err)
	}
	for _, day := range days {
		if !day.IsDir() || day.Name() == "tmp" {
			continue
		}
		files, err := os.ReadDir(filepath.Join(e.dir, day.Name()))
		if err != nil {
			continue
		}
		for _, file := range files {
			stem, _, _ := strings.Cut(file.Name(), ".")
			if id, err := strconv.ParseInt(stem, 10, 64); err == nil && id > maxDisk {
				maxDisk = id
			}
		}
	}
	if maxDisk == 0 {
		return nil
	}
	var seq int64
	err = e.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = 'request_log_segments'`).Scan(&seq)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = e.db.ExecContext(ctx, `INSERT INTO sqlite_sequence (name, seq) VALUES ('request_log_segments', ?)`, maxDisk)
	case err == nil && seq < maxDisk:
		_, err = e.db.ExecContext(ctx, `UPDATE sqlite_sequence SET seq = ? WHERE name = 'request_log_segments'`, maxDisk)
	}
	if err != nil {
		return fmt.Errorf("reserve segment ids: %w", err)
	}
	return nil
}

// restoreRawFromSealed decompresses a sealed file back to its raw name when
// the raw file is gone, so the segment can be re-indexed and sealed again.
func restoreRawFromSealed(rawPath, zstPath string) error {
	if _, err := os.Stat(rawPath); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	in, err := os.Open(zstPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	dec, err := newDecoder(in)
	if err != nil {
		return err
	}
	defer dec.Close()
	tmpPath := rawPath + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, errCopy := io.Copy(out, dec)
	errClose := out.Close()
	if err := errors.Join(errCopy, errClose); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, rawPath)
}

// sweep removes files that no segment row owns, plus raw copies of sealed
// segments. It runs beside the writer, so files numbered past the snapshot
// belong to segments created since and are left alone; ids are never reused.
func (e *engine) sweep(ctx context.Context) {
	var maxID int64
	err := e.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = 'request_log_segments'`).Scan(&maxID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.WithError(err).Warn("request logs: sweep")
		return
	}
	known := make(map[int64]string)
	rows, err := e.db.QueryContext(ctx, `SELECT id, state FROM request_log_segments`)
	if err != nil {
		log.WithError(err).Warn("request logs: sweep")
		return
	}
	for rows.Next() {
		var id int64
		var state string
		if rows.Scan(&id, &state) == nil {
			known[id] = state
		}
	}
	_ = rows.Close()
	days, err := os.ReadDir(e.dir)
	if err != nil {
		return
	}
	failed := false
	quietBefore := e.now().UTC().Add(-48 * time.Hour).Format(time.DateOnly)
	for _, day := range days {
		if !day.IsDir() || day.Name() == "tmp" {
			continue
		}
		dayDir := filepath.Join(e.dir, day.Name())
		files, err := os.ReadDir(dayDir)
		if err != nil {
			continue
		}
		remaining := len(files)
		for _, file := range files {
			name := file.Name()
			stem, ext, _ := strings.Cut(name, ".")
			id, errID := strconv.ParseInt(stem, 10, 64)
			if errID == nil && id > maxID {
				continue
			}
			state, ok := known[id]
			orphan := errID != nil || !ok || (state == segmentSealed && ext != "zst")
			if !orphan {
				continue
			}
			if err := os.Remove(filepath.Join(dayDir, name)); err != nil {
				failed = true
				continue
			}
			remaining--
		}
		// The writer may still create segments for a recent day.
		if remaining == 0 && day.Name() < quietBefore {
			_ = os.Remove(dayDir)
		}
	}
	e.sweepNeeded.Store(failed)
}

// applyRetention drops whole sealed segments: first those past max-days, then
// the oldest until the directory fits max-bytes. Open segments are live and
// only count toward the total.
func (e *engine) applyRetention(ctx context.Context) {
	cfg := e.retention.Load()
	if cfg.MaxDays > 0 {
		cutoff := e.now().Add(-time.Duration(cfg.MaxDays) * 24 * time.Hour).UnixMilli()
		ids, err := querySegmentIDs(ctx, e.db, `SELECT id FROM request_log_segments WHERE state = ? AND last_ts < ?`, segmentSealed, cutoff)
		if err == nil {
			err = e.deleteSegments(ctx, ids)
		}
		if err != nil {
			log.WithError(err).Warn("request logs: age retention")
		}
	}
	if cfg.MaxBytes > 0 {
		for ctx.Err() == nil {
			var total int64
			if err := e.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN state = ? THEN disk_bytes ELSE raw_bytes END), 0) FROM request_log_segments`, segmentSealed).Scan(&total); err != nil {
				log.WithError(err).Warn("request logs: size retention")
				break
			}
			if total <= int64(cfg.MaxBytes) {
				break
			}
			ids, err := querySegmentIDs(ctx, e.db, `SELECT id FROM request_log_segments WHERE state = ? ORDER BY last_ts, id LIMIT 16`, segmentSealed)
			if err != nil || len(ids) == 0 {
				break
			}
			if err := e.deleteSegments(ctx, ids[:1]); err != nil {
				log.WithError(err).Warn("request logs: size retention")
				break
			}
		}
	}
	if e.sweepNeeded.Load() {
		e.sweep(ctx)
	}
}

func querySegmentIDs(ctx context.Context, db *store.DB, query string, args ...any) ([]int64, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query segments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan segment id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (e *engine) deleteSegments(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		var day string
		if err := e.db.QueryRowContext(ctx, `SELECT day FROM request_log_segments WHERE id = ?`, id).Scan(&day); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("load segment: %w", err)
		}
		tx, err := e.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin delete: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM request_logs WHERE segment_id = ?`, id); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("delete records: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM request_log_segments WHERE id = ?`, id); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("delete segment: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit delete: %w", err)
		}
		for _, ext := range []string{".zst", ".raw"} {
			if err := os.Remove(e.segmentPath(day, id, ext)); err != nil && !errors.Is(err, os.ErrNotExist) {
				e.sweepNeeded.Store(true)
			}
		}
	}
	return nil
}

func insertRows(ctx context.Context, db *store.DB, rows []indexRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin index: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO request_logs (
		request_id, trace_id, session_id, parent_session_id, ts, duration_ms, method, path, url,
		status, model, auth_index, streaming, error_only, dropped, req_bytes, resp_bytes,
		segment_id, offset, length
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prepare index: %w", err)
	}
	segStmt, err := tx.PrepareContext(ctx, `UPDATE request_log_segments SET
		raw_bytes = MAX(raw_bytes, ?),
		first_ts = CASE WHEN first_ts IS NULL OR first_ts > ? THEN ? ELSE first_ts END,
		last_ts = CASE WHEN last_ts IS NULL OR last_ts < ? THEN ? ELSE last_ts END
		WHERE id = ?`)
	if err != nil {
		_ = stmt.Close()
		_ = tx.Rollback()
		return fmt.Errorf("prepare segment update: %w", err)
	}
	defer func() {
		_ = stmt.Close()
		_ = segStmt.Close()
	}()
	for _, r := range rows {
		m := r.meta
		if _, err := stmt.ExecContext(ctx, m.RequestID, m.TraceID, m.SessionID, m.ParentSessionID, m.TimeMs, m.DurationMs,
			m.Method, m.Path, m.URL, m.Status, m.Model, m.AuthIndex, m.Streaming, m.ErrorOnly, m.Dropped,
			m.RequestBytes, m.ResponseBytes, r.segment, r.offset, r.length); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("insert record: %w", err)
		}
		end := r.offset + int64(r.length)
		if _, err := segStmt.ExecContext(ctx, end, m.TimeMs, m.TimeMs, m.TimeMs, m.TimeMs, r.segment); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("update segment: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit index: %w", err)
	}
	return nil
}

// readFrame returns one stored frame. Open segments are read in place; sealed
// ones are decompressed from the start up to the record.
func (e *engine) readFrame(ctx context.Context, loc recordLocation) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if loc.state != segmentSealed {
			frame, err := readRawAt(e.segmentPath(loc.day, loc.segment, ".raw"), loc.offset, loc.length)
			if err == nil || !errors.Is(err, os.ErrNotExist) {
				return frame, err
			}
			// Sealed between the index lookup and the read.
			var state string
			if errState := e.db.QueryRowContext(ctx, `SELECT state FROM request_log_segments WHERE id = ?`, loc.segment).Scan(&state); errState != nil {
				return nil, fmt.Errorf("reload segment: %w", errState)
			}
			loc.state = state
			continue
		}
		select {
		case e.readSem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		frame, err := readSealedAt(e.segmentPath(loc.day, loc.segment, ".zst"), loc.offset, loc.length)
		<-e.readSem
		return frame, err
	}
	return nil, fmt.Errorf("segment %d unavailable", loc.segment)
}

func readRawAt(path string, offset int64, length int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, length)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return nil, fmt.Errorf("read raw segment: %w", err)
	}
	return buf, nil
}

func readSealedAt(path string, offset int64, length int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open sealed segment: %w", err)
	}
	defer func() { _ = f.Close() }()
	dec, err := newDecoder(f)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	w := &rangeWriter{start: offset, out: make([]byte, 0, length)}
	if _, err := dec.WriteTo(w); err != nil && !errors.Is(err, errRangeDone) {
		return nil, fmt.Errorf("read sealed segment: %w", err)
	}
	if len(w.out) != length {
		return nil, fmt.Errorf("sealed segment ended %d bytes short", length-len(w.out))
	}
	return w.out, nil
}

var errRangeDone = errors.New("range complete")

// rangeWriter keeps one byte range of a decoded stream. Decoding through
// WriteTo hands over whole blocks, which is several times faster than reading
// through a small buffer.
type rangeWriter struct {
	start int64
	pos   int64
	out   []byte
}

func (w *rangeWriter) Write(p []byte) (int, error) {
	n := len(p)
	if skip := w.start - w.pos; skip > 0 {
		if skip >= int64(n) {
			w.pos += int64(n)
			return n, nil
		}
		p = p[skip:]
		w.pos += skip
	}
	take := min(len(p), cap(w.out)-len(w.out))
	w.out = append(w.out, p[:take]...)
	w.pos += int64(take)
	if len(w.out) == cap(w.out) {
		return n, errRangeDone
	}
	return n, nil
}
