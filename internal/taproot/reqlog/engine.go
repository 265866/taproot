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
//	blobs/<pack id>.pack            base64 payloads, stored once (blobs.go)
//	dicts/<id>.dict                 shared zstd dictionaries (dict.go)
//	tmp/                            spooled bodies of in-flight requests
//
// One segment holds one session's records for one UTC day. Records stay raw
// while the session is active and are compressed in one pass when the segment
// seals, against the newest shared dictionary (dict.go), so a single encoder
// runs at a time no matter how many sessions are open. A sealed file is
// written to a temp name, verified, and renamed; it is never modified
// afterwards.
//
// An unclean stop loses records still in the in-memory queue and a frame cut
// off mid-write. Complete frames in open segments carry their own metadata and
// are re-indexed when the next start seals those segments.
const (
	segmentOpen    = "open"
	segmentSealing = "sealing"
	segmentSealed  = "sealed"

	// zstdWindow is also the default SegmentMaxBytes, so every record
	// compresses against the earlier turns of its conversation. Sealing and a
	// lookup each hold about this much memory.
	zstdWindow = 128 << 20

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

	blobs *blobStore
	dicts *dictStore

	// open and pending belong to the writer goroutine.
	open    map[string]*segState
	pending []indexRow

	sealMu    sync.Mutex
	sealQueue []int64
	sealWake  chan struct{}

	// retries and dictTried belong to the compactor. retries holds segments
	// whose compaction failed, with the attempt count and when to try again.
	retries   map[int64]compactRetry
	dictTried time.Time

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
		blobs:    newBlobStore(db, filepath.Join(dir, "blobs"), now),
		dicts:    newDictStore(db, filepath.Join(dir, "dicts")),
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
	if err := e.blobs.recover(ctx); err != nil {
		return nil, err
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
	// Blob rows may still be pending, so the ingest pack is left for the next
	// start's recovery to reindex and mark full.
	e.blobs.closeIngest(false)
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
	e.rollBlobs()
}

func (e *engine) writeOne(ent *entry) {
	meta, text, err := ent.capture.build()
	if err != nil {
		e.stats.writeErrors.Add(1)
		log.WithError(err).Warn("request logs: build record")
		return
	}
	e.store(meta, text)
}

// encodedRecord is a record ready to append: its frame and the blobs it
// references, each once.
type encodedRecord struct {
	meta  *recordMeta
	frame frame
	refs  []blobRef
	blobs []newBlob
}

type newBlob struct {
	hash blobHash
	raw  []byte
}

// encodeRecord does a record's CPU work: base64 extraction, hashing, and the
// SSE transform.
func encodeRecord(meta *recordMeta, text []byte) (encodedRecord, error) {
	rec := encodedRecord{meta: meta}
	seen := make(map[blobHash]bool)
	f, refs, err := encodeFrame(meta, text, func(h blobHash, raw []byte) {
		if !seen[h] {
			seen[h] = true
			rec.blobs = append(rec.blobs, newBlob{h, raw})
		}
	})
	rec.frame, rec.refs = f, refs
	return rec, err
}

// store appends one record to its session's open segment. It runs on the
// writer goroutine.
func (e *engine) store(meta *recordMeta, text []byte) {
	rec, err := encodeRecord(meta, text)
	if err != nil {
		e.stats.writeErrors.Add(1)
		log.WithError(err).Warn("request logs: encode record")
		return
	}
	e.appendRecord(rec)
}

// appendRecord stores a record's new blobs and appends its frame. It runs on
// the writer goroutine.
func (e *engine) appendRecord(rec encodedRecord) {
	mark := len(e.blobs.pending)
	for _, b := range rec.blobs {
		if _, err := e.blobs.add(b.hash, b.raw); err != nil {
			e.blobs.forget(mark)
			e.stats.writeErrors.Add(1)
			log.WithError(err).Warn("request logs: store blob")
			return
		}
	}
	if len(e.blobs.pending) > mark {
		if err := e.blobs.sync(); err != nil {
			e.blobs.forget(mark)
			e.stats.writeErrors.Add(1)
			log.WithError(err).Warn("request logs: store blob")
			return
		}
	}
	meta, f, refs := rec.meta, rec.frame, rec.refs
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
	hashes := make([]blobHash, len(refs))
	for i, r := range refs {
		hashes[i] = r.hash
	}
	e.blobs.touch(hashes, meta.TimeMs)
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

// rollBlobs hands a due ingest pack to the compactor. It runs right after a
// commit, when no blob row of the pack is pending.
func (e *engine) rollBlobs() {
	if e.blobs.roll(e.now()) {
		e.wakeCompactor()
	}
}

// maxPendingRows bounds index rows kept for retry while the database fails.
const maxPendingRows = 16384

// commitPending indexes written records and their new blobs in one
// transaction. On failure, rows for segments still open stay pending and are
// retried with the next batch. Every frame is re-indexed from the raw file
// when its segment seals, so rows for segments no longer open are dropped:
// retrying them is redundant, and a row whose segment retention deleted would
// fail its foreign key on every attempt. Pending blob rows stay until a
// commit succeeds; their bytes are already in the ingest pack.
func (e *engine) commitPending() {
	if len(e.pending) == 0 && len(e.blobs.pending) == 0 && len(e.blobs.touched) == 0 {
		return
	}
	if err := insertRows(context.Background(), e.db, e.pending, e.blobs.commit); err != nil {
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
	e.blobs.committed()
	clear(e.pending)
	e.pending = e.pending[:0]
}

func (e *engine) segmentFor(sessionID, day string) (*segState, error) {
	key := sessionID + "\x00" + day
	if seg := e.open[key]; seg != nil {
		return seg, nil
	}
	now := e.now()
	res, err := e.db.Exec(`INSERT INTO reqlog_segments (session_id, day, state, created_at) VALUES (?, ?, ?, ?)`,
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
	if _, err := e.db.Exec(`UPDATE reqlog_segments SET state = ? WHERE id = ?`, segmentSealing, seg.id); err != nil {
		log.WithError(err).Warn("request logs: mark segment sealing")
	}
	e.enqueueSeal(seg.id)
}

func (e *engine) enqueueSeal(id int64) {
	e.sealMu.Lock()
	e.sealQueue = append(e.sealQueue, id)
	e.sealMu.Unlock()
	e.wakeCompactor()
}

func (e *engine) wakeCompactor() {
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
			break
		}
		total -= largest.size
		e.seal(largest)
	}
	e.commitPending()
	e.rollBlobs()
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
		if err := e.blobs.packFull(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).Warn("request logs: pack blobs")
		}
		select {
		case <-ctx.Done():
			return
		case <-e.sealWake:
		case <-ticker.C:
			e.requeueFailed(ctx)
			e.applyRetention(ctx)
			e.maybeTrainDict(ctx)
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
		err := e.db.QueryRowContext(ctx, `SELECT state FROM reqlog_segments WHERE id = ?`, id).Scan(&state)
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
	if err := e.db.QueryRowContext(ctx, `SELECT day, state FROM reqlog_segments WHERE id = ?`, id).Scan(&day, &state); err != nil {
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
	dictID, dict, err := e.dicts.latest()
	if err != nil {
		return err
	}
	tmpPath := zstPath + ".tmp"
	diskBytes, err := compressFile(rawPath, tmpPath, validEnd, dictID, dict)
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, zstPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publish sealed segment: %w", err)
	}
	if _, err := e.db.ExecContext(ctx, `UPDATE reqlog_segments SET state = ?, dict_id = ?, disk_bytes = ?, raw_bytes = ?, sealed_at = ? WHERE id = ?`,
		segmentSealed, dictID, diskBytes, validEnd, e.now().UnixMilli(), id); err != nil {
		return fmt.Errorf("mark segment sealed: %w", err)
	}
	if err := os.Remove(rawPath); err != nil {
		// Windows refuses while a reader holds the file; the sweep retries.
		e.sweepNeeded.Store(true)
		log.WithError(err).Debug("request logs: remove raw segment")
	}
	return nil
}

func compressFile(rawPath, outPath string, size, dictID int64, dict []byte) (int64, error) {
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

	opts := []zstd.EOption{
		zstd.WithEncoderLevel(zstd.SpeedBestCompression),
		zstd.WithWindowSize(windowFor(size)),
		zstd.WithEncoderConcurrency(1),
	}
	if dictID != 0 {
		opts = append(opts, zstd.WithEncoderDictRaw(uint32(dictID), dict))
	}
	enc, err := zstd.NewWriter(out, opts...)
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
	if err := verifyCompressed(outPath, size, sum.Sum32(), dictID, dict); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func verifyCompressed(path string, size int64, want uint32, dictID int64, dict []byte) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open sealed segment for verify: %w", err)
	}
	defer func() { _ = f.Close() }()
	dec, err := newDecoder(f, dictID, dict)
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

func newDecoder(r io.Reader, dictID int64, dict []byte) (*zstd.Decoder, error) {
	opts := []zstd.DOption{zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(zstdWindow)}
	if dictID != 0 {
		opts = append(opts, zstd.WithDecoderDictRaw(uint32(dictID), dict))
	}
	dec, err := zstd.NewReader(r, opts...)
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
	touched := make(map[blobHash]int64)
	validEnd, err := scanFrames(f, 0, info.Size(), func(offset int64, length int, meta *recordMeta, refs []blobHash) {
		rows = append(rows, indexRow{meta: meta, segment: id, offset: offset, length: length})
		if firstTs == 0 || meta.TimeMs < firstTs {
			firstTs = meta.TimeMs
		}
		lastTs = max(lastTs, meta.TimeMs)
		for _, h := range refs {
			touched[h] = max(touched[h], meta.TimeMs)
		}
	})
	if err != nil {
		return 0, fmt.Errorf("scan raw segment: %w", err)
	}
	if info.Size() > validEnd {
		if err := f.Truncate(validEnd); err != nil {
			return 0, fmt.Errorf("truncate torn segment tail: %w", err)
		}
	}
	touch := func(ctx context.Context, tx *sql.Tx) error {
		for h, ts := range touched {
			if _, err := tx.ExecContext(ctx, `UPDATE reqlog_blobs SET last_ts = ? WHERE hash = ? AND last_ts < ?`, ts, h[:], ts); err != nil {
				return fmt.Errorf("touch blob: %w", err)
			}
		}
		return nil
	}
	if err := insertRows(ctx, e.db, rows, touch); err != nil {
		return 0, err
	}
	if _, err := e.db.ExecContext(ctx, `DELETE FROM reqlog_records WHERE segment_id = ? AND offset + length > ?`, id, validEnd); err != nil {
		return 0, fmt.Errorf("drop index rows past segment end: %w", err)
	}
	if _, err := e.db.ExecContext(ctx, `UPDATE reqlog_segments SET raw_bytes = ?, first_ts = ?, last_ts = ? WHERE id = ?`,
		validEnd, firstTs, lastTs, id); err != nil {
		return 0, fmt.Errorf("update segment size: %w", err)
	}
	return validEnd, nil
}

// recover runs before the writer starts. Segments left open by the last run
// are re-indexed from their frames and queued for sealing; sealed segments are
// untouched apart from removing raw files a previous run could not delete.
func (e *engine) recover(ctx context.Context) error {
	rows, err := e.db.QueryContext(ctx, `SELECT id, day, state FROM reqlog_segments`)
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
	// A restored database can be older than the files: segments and packs
	// created after its snapshot have ids past its sequences. New ids must not
	// collide with them, and the sweep below then removes them as unowned.
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
		if err := e.restoreRawFromSealed(ctx, e.segmentPath(s.day, s.id, ".raw"), zstPath); err != nil {
			log.WithError(err).WithField("segment", s.id).Warn("request logs: recover sealed copy of open segment")
		}
		_ = os.Remove(zstPath)
		if _, err := e.db.ExecContext(ctx, `UPDATE reqlog_segments SET state = ? WHERE id = ?`, segmentSealing, s.id); err != nil {
			return fmt.Errorf("mark segment sealing: %w", err)
		}
		e.sealQueue = append(e.sealQueue, s.id)
	}
	e.sweep(ctx)
	return nil
}

// reserveDiskIDs raises the segment and pack id sequences to the largest ids
// on disk.
func (e *engine) reserveDiskIDs(ctx context.Context) error {
	var maxSeg, maxPack int64
	days, err := os.ReadDir(e.dir)
	if err != nil {
		return fmt.Errorf("list request log dir: %w", err)
	}
	for _, day := range days {
		if !day.IsDir() || day.Name() == "tmp" || day.Name() == "dicts" {
			continue
		}
		files, err := os.ReadDir(filepath.Join(e.dir, day.Name()))
		if err != nil {
			continue
		}
		for _, file := range files {
			stem, _, _ := strings.Cut(file.Name(), ".")
			id, err := strconv.ParseInt(stem, 10, 64)
			if err != nil {
				continue
			}
			if day.Name() == "blobs" {
				maxPack = max(maxPack, id)
			} else {
				maxSeg = max(maxSeg, id)
			}
		}
	}
	for table, maxDisk := range map[string]int64{"reqlog_segments": maxSeg, "reqlog_packs": maxPack} {
		if maxDisk == 0 {
			continue
		}
		var seq int64
		err := e.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = ?`, table).Scan(&seq)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = e.db.ExecContext(ctx, `INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)`, table, maxDisk)
		case err == nil && seq < maxDisk:
			_, err = e.db.ExecContext(ctx, `UPDATE sqlite_sequence SET seq = ? WHERE name = ?`, maxDisk, table)
		}
		if err != nil {
			return fmt.Errorf("reserve %s ids: %w", table, err)
		}
	}
	return nil
}

// restoreRawFromSealed decompresses a sealed file back to its raw name when
// the raw file is gone, so the segment can be re-indexed and sealed again.
func (e *engine) restoreRawFromSealed(ctx context.Context, rawPath, zstPath string) error {
	if _, err := os.Stat(rawPath); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(zstPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	dictID, err := frameDictID(zstPath)
	if err != nil {
		return err
	}
	dict, err := e.dicts.get(dictID)
	if err != nil {
		return err
	}
	return decompressTo(zstPath, rawPath, dictID, dict)
}

// frameDictID reads the dictionary id from a sealed segment's frame header.
func frameDictID(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, zstd.HeaderMaxSize)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return 0, err
	}
	var h zstd.Header
	if err := h.Decode(head[:n]); err != nil {
		return 0, fmt.Errorf("read segment frame header: %w", err)
	}
	return int64(h.DictionaryID), nil
}

func decompressTo(zstPath, rawPath string, dictID int64, dict []byte) error {
	in, err := os.Open(zstPath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	dec, err := newDecoder(in, dictID, dict)
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

// sweep removes files that no segment or pack row owns, plus raw copies of
// sealed segments. It runs beside the writer, so files numbered past the
// snapshot belong to segments or packs created since and are left alone; ids
// are never reused.
func (e *engine) sweep(ctx context.Context) {
	known := func(table string) (int64, map[int64]string, bool) {
		var maxID int64
		err := e.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = ?`, table).Scan(&maxID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.WithError(err).Warn("request logs: sweep")
			return 0, nil, false
		}
		ids := make(map[int64]string)
		rows, err := e.db.QueryContext(ctx, `SELECT id, state FROM `+table)
		if err != nil {
			log.WithError(err).Warn("request logs: sweep")
			return 0, nil, false
		}
		for rows.Next() {
			var id int64
			var state string
			if rows.Scan(&id, &state) == nil {
				ids[id] = state
			}
		}
		_ = rows.Close()
		return maxID, ids, true
	}
	maxSeg, segs, ok := known("reqlog_segments")
	if !ok {
		return
	}
	maxPack, packs, ok := known("reqlog_packs")
	if !ok {
		return
	}
	days, err := os.ReadDir(e.dir)
	if err != nil {
		return
	}
	failed := false
	quietBefore := e.now().UTC().Add(-48 * time.Hour).Format(time.DateOnly)
	for _, day := range days {
		if !day.IsDir() || day.Name() == "tmp" || day.Name() == "dicts" {
			continue
		}
		isBlobs := day.Name() == "blobs"
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
			var orphan bool
			if isBlobs {
				if errID == nil && id > maxPack {
					continue
				}
				_, owned := packs[id]
				orphan = errID != nil || !owned || ext != "pack"
			} else {
				if errID == nil && id > maxSeg {
					continue
				}
				state, owned := segs[id]
				orphan = errID != nil || !owned || (state == segmentSealed && ext != "zst")
			}
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
		if !isBlobs && remaining == 0 && day.Name() < quietBefore {
			_ = os.Remove(dayDir)
		}
	}
	e.sweepNeeded.Store(failed)
}

// applyRetention drops whole sealed segments: first those past max-days, then
// the oldest until stored bytes fit max-bytes. Open segments are live and only
// count toward the total, as do live blobs. Blobs the dropped records were the
// last to use go with them, and packs that end up mostly dead are rewritten.
func (e *engine) applyRetention(ctx context.Context) {
	cfg := e.retention.Load()
	dropped := false
	if cfg.MaxDays > 0 {
		cutoff := e.now().Add(-time.Duration(cfg.MaxDays) * 24 * time.Hour).UnixMilli()
		ids, err := queryIDs(ctx, e.db, `SELECT id FROM reqlog_segments WHERE state = ? AND last_ts < ?`, segmentSealed, cutoff)
		if err == nil {
			err = e.deleteSegments(ctx, ids)
		}
		if err != nil {
			log.WithError(err).Warn("request logs: age retention")
		}
		dropped = len(ids) > 0
	}
	if dropped {
		e.collectBlobs(ctx)
	}
	if cfg.MaxBytes > 0 {
		for ctx.Err() == nil {
			total, err := e.storedBytes(ctx)
			if err != nil {
				log.WithError(err).Warn("request logs: size retention")
				break
			}
			if total <= int64(cfg.MaxBytes) {
				break
			}
			// Blob GC cannot pass the oldest record. When that record sits in
			// a segment that has not sealed, dropping sealed history would
			// free segment bytes but no blobs, and could empty the store.
			if pinned, err := e.oldestRecordUnsealed(ctx); err != nil || pinned {
				if pinned {
					log.Warn("request logs: max-bytes waits for the oldest segment to seal")
				}
				break
			}
			ids, err := queryIDs(ctx, e.db, `SELECT id FROM reqlog_segments WHERE state = ? ORDER BY last_ts, id LIMIT 1`, segmentSealed)
			if err != nil || len(ids) == 0 {
				break
			}
			if err := e.deleteSegments(ctx, ids); err != nil {
				log.WithError(err).Warn("request logs: size retention")
				break
			}
			e.collectBlobs(ctx)
		}
	}
	if err := e.blobs.compactPacked(ctx); err != nil && ctx.Err() == nil {
		log.WithError(err).Warn("request logs: compact blob packs")
	}
	if err := e.dicts.prune(ctx); err != nil {
		log.WithError(err).Warn("request logs: prune dictionaries")
	}
	if e.sweepNeeded.Load() {
		e.sweep(ctx)
	}
}

// collectBlobs runs blob GC on the writer goroutine, so it never races a new
// reference to a blob it deletes.
func (e *engine) collectBlobs(ctx context.Context) {
	var errGC error
	if err := e.do(ctx, func() { _, errGC = e.blobs.gc(ctx) }); err != nil {
		errGC = err
	}
	if errGC != nil && ctx.Err() == nil {
		log.WithError(errGC).Warn("request logs: collect blobs")
	}
}

func (e *engine) oldestRecordUnsealed(ctx context.Context) (bool, error) {
	var state string
	err := e.db.QueryRowContext(ctx, `SELECT s.state FROM reqlog_records r JOIN reqlog_segments s ON s.id = r.segment_id ORDER BY r.ts, r.id LIMIT 1`).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && state != segmentSealed, err
}

// storedBytes is what retention's max-bytes counts: sealed segments on disk,
// open segments raw, and live blobs as stored.
func (e *engine) storedBytes(ctx context.Context) (int64, error) {
	var segs, blobs int64
	if err := e.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN state = ? THEN disk_bytes ELSE raw_bytes END), 0) FROM reqlog_segments`, segmentSealed).Scan(&segs); err != nil {
		return 0, err
	}
	if err := e.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(stored_bytes), 0) FROM reqlog_blobs`).Scan(&blobs); err != nil {
		return 0, err
	}
	return segs + blobs, nil
}

func queryIDs(ctx context.Context, db *store.DB, query string, args ...any) ([]int64, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (e *engine) deleteSegments(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		var day string
		if err := e.db.QueryRowContext(ctx, `SELECT day FROM reqlog_segments WHERE id = ?`, id).Scan(&day); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("load segment: %w", err)
		}
		tx, err := e.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin delete: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM reqlog_records WHERE segment_id = ?`, id); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("delete records: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM reqlog_segments WHERE id = ?`, id); err != nil {
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

// insertRows indexes records in one transaction with extra, which writes the
// blob rows and last_ts updates that belong with them.
func insertRows(ctx context.Context, db *store.DB, rows []indexRow, extra func(context.Context, *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin index: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if extra != nil {
		if err := extra(ctx, tx); err != nil {
			return err
		}
	}
	if len(rows) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO reqlog_records (
			request_id, trace_id, session_id, parent_session_id, ts, duration_ms, method, path, url,
			status, model, auth_index, streaming, error_only, dropped, req_bytes, resp_bytes, text_bytes,
			segment_id, offset, length
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare index: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		segStmt, err := tx.PrepareContext(ctx, `UPDATE reqlog_segments SET
			raw_bytes = MAX(raw_bytes, ?),
			first_ts = CASE WHEN first_ts IS NULL OR first_ts > ? THEN ? ELSE first_ts END,
			last_ts = CASE WHEN last_ts IS NULL OR last_ts < ? THEN ? ELSE last_ts END
			WHERE id = ?`)
		if err != nil {
			return fmt.Errorf("prepare segment update: %w", err)
		}
		defer func() { _ = segStmt.Close() }()
		for _, r := range rows {
			m := r.meta
			if _, err := stmt.ExecContext(ctx, m.RequestID, m.TraceID, m.SessionID, m.ParentSessionID, m.TimeMs, m.DurationMs,
				m.Method, m.Path, m.URL, m.Status, m.Model, m.AuthIndex, m.Streaming, m.ErrorOnly, m.Dropped,
				m.RequestBytes, m.ResponseBytes, m.TextBytes, r.segment, r.offset, r.length); err != nil {
				return fmt.Errorf("insert record: %w", err)
			}
			end := r.offset + int64(r.length)
			if _, err := segStmt.ExecContext(ctx, end, m.TimeMs, m.TimeMs, m.TimeMs, m.TimeMs, r.segment); err != nil {
				return fmt.Errorf("update segment: %w", err)
			}
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
			if errState := e.db.QueryRowContext(ctx, `SELECT state, dict_id FROM reqlog_segments WHERE id = ?`, loc.segment).Scan(&loc.state, &loc.dictID); errState != nil {
				return nil, fmt.Errorf("reload segment: %w", errState)
			}
			continue
		}
		dict, err := e.dicts.get(loc.dictID)
		if err != nil {
			return nil, err
		}
		select {
		case e.readSem <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		frame, err := readSealedAt(e.segmentPath(loc.day, loc.segment, ".zst"), loc.offset, loc.length, loc.dictID, dict)
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

func readSealedAt(path string, offset int64, length int, dictID int64, dict []byte) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open sealed segment: %w", err)
	}
	defer func() { _ = f.Close() }()
	dec, err := newDecoder(f, dictID, dict)
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
