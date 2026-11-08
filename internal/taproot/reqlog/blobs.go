package reqlog

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/265866/taproot/internal/taproot/reqlog/codec"
	"github.com/265866/taproot/internal/taproot/store"
	log "github.com/sirupsen/logrus"
)

// Claude Code resends every image of a conversation on every turn, and
// taproot logs each request twice (as received and as sent upstream), so
// base64 payloads were three quarters of all logged bytes. Each one is stored
// once, by the SHA-256 of its decoded bytes, in pack files under blobs/:
//
//	ingest  the writer appends new blobs raw; it never waits on a codec
//	full    a closed ingest pack waiting for the compactor
//	writing the compactor's output, garbage if a run dies before commit
//	packed  encoded blobs (Lepton, PNG, zstd, or raw), immutable
//
// Every pack entry carries its own hash and lengths,
//
//	TPB1 <codec.Kind> <raw bytes> <stored bytes> <hex SHA-256 of the raw bytes>\n<stored bytes>\n
//
// so rows lost in a crash are rebuilt by scanning the file. A blob lives
// while a stored record references it: last_ts is the newest referencing
// record's time, and a blob older than every remaining record is garbage.
const (
	packIngest  = "ingest"
	packFull    = "full"
	packWriting = "writing"
	packPacked  = "packed"

	// minBlobRun is the shortest base64 run stored as a blob. Shorter runs
	// gain little and are left in the text.
	minBlobRun = 1024

	ingestPackMax = 64 << 20
	ingestPackAge = 10 * time.Minute
	blobCacheMax  = 256 << 20

	entryMagic = "TPB1 "
	// entryOverhead is what one pack entry costs beyond its stored bytes:
	// magic, codec, two lengths, the hex hash, separators, and two newlines,
	// about 85 bytes for blobs under 100 KB.
	entryOverhead = 85
)

type blobHash [sha256.Size]byte

// hashOf reads a stored hash column.
func hashOf(b []byte) (blobHash, error) {
	if len(b) != len(blobHash{}) {
		return blobHash{}, fmt.Errorf("blob hash of %d bytes", len(b))
	}
	return blobHash(b), nil
}

func isBase64Byte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/'
}

// splitBlobs cuts every canonical standard-base64 run of at least
// minBlobRun bytes out of text. Re-encoding the decoded bytes reproduces each
// run exactly, so splicing base64 of the blobs back in at the refs restores
// text.
func splitBlobs(text []byte, onBlob func(blobHash, []byte)) ([]byte, []blobRef) {
	var residual []byte
	var refs []blobRef
	last := 0
	for i := 0; i < len(text); {
		if !isBase64Byte(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && isBase64Byte(text[j]) {
			j++
		}
		end := j
		for k := 0; k < 2 && end < len(text) && text[end] == '='; k++ {
			end++
		}
		run := text[i:end]
		if len(run) >= minBlobRun && len(run)%4 == 0 {
			raw := make([]byte, base64.StdEncoding.DecodedLen(len(run)))
			n, err := base64.StdEncoding.Strict().Decode(raw, run)
			if err == nil && base64.StdEncoding.EncodedLen(n) == len(run) {
				raw = raw[:n]
				h := blobHash(sha256.Sum256(raw))
				if residual == nil {
					residual = make([]byte, 0, len(text)/4)
				}
				residual = append(residual, text[last:i]...)
				refs = append(refs, blobRef{pos: len(residual), hash: h})
				onBlob(h, raw)
				last = end
			}
		}
		i = end
	}
	if refs == nil {
		return text, nil
	}
	return append(residual, text[last:]...), refs
}

// spliceBlobs is splitBlobs in reverse.
func spliceBlobs(residual []byte, refs []blobRef, blobs map[blobHash][]byte) ([]byte, error) {
	if len(refs) == 0 {
		return residual, nil
	}
	size := len(residual)
	for _, r := range refs {
		raw, ok := blobs[r.hash]
		if !ok {
			return nil, fmt.Errorf("blob %x missing", r.hash[:8])
		}
		size += base64.StdEncoding.EncodedLen(len(raw))
	}
	out := make([]byte, 0, size)
	last := 0
	for _, r := range refs {
		out = append(out, residual[last:r.pos]...)
		raw := blobs[r.hash]
		out = base64.StdEncoding.AppendEncode(out, raw)
		last = r.pos
	}
	return append(out, residual[last:]...), nil
}

type blobRow struct {
	hash   blobHash
	pack   int64
	offset int64
	stored int
	raw    int
	codec  codec.Kind
	lastTs int64
}

type blobStore struct {
	db  *store.DB
	dir string
	now func() time.Time

	// known and the ingest fields belong to the writer goroutine; GC runs
	// there too, through engine.do.
	known map[blobHash]struct{}

	ingest       *os.File
	ingestID     int64
	ingestSize   int64
	ingestOpened time.Time
	pending      []blobRow
	touched      map[blobHash]int64
	// syncFile is (*os.File).Sync; tests make it fail.
	syncFile func(*os.File) error

	cache *blobCache
}

func newBlobStore(db *store.DB, dir string, now func() time.Time) *blobStore {
	return &blobStore{db: db, dir: dir, now: now, known: make(map[blobHash]struct{}), touched: make(map[blobHash]int64),
		syncFile: (*os.File).Sync, cache: newBlobCache(blobCacheMax)}
}

func (b *blobStore) packPath(id int64) string {
	return filepath.Join(b.dir, strconv.FormatInt(id, 10)+".pack")
}

func entryHeader(r blobRow) []byte {
	return []byte(entryMagic + strconv.Itoa(int(r.codec)) + " " + strconv.Itoa(r.raw) + " " + strconv.Itoa(r.stored) + " " + hex.EncodeToString(r.hash[:]) + "\n")
}

// add records a blob the writer saw and reports whether it was new. New
// blobs go raw into the ingest pack at once; their rows are committed with
// the records that reference them.
func (b *blobStore) add(h blobHash, raw []byte) (bool, error) {
	if _, ok := b.known[h]; ok {
		return false, nil
	}
	if b.ingest == nil {
		if err := b.openIngest(); err != nil {
			return false, err
		}
	}
	r := blobRow{hash: h, pack: b.ingestID, stored: len(raw), raw: len(raw), codec: codec.Raw}
	head := entryHeader(r)
	r.offset = b.ingestSize + int64(len(head))
	for _, part := range [][]byte{head, raw, {'\n'}} {
		if _, err := b.ingest.Write(part); err != nil {
			// A torn entry is cut by recovery; later blobs go to a new pack.
			b.closeIngest(false)
			return false, fmt.Errorf("write blob: %w", err)
		}
	}
	b.ingestSize = r.offset + int64(len(raw)) + 1
	b.pending = append(b.pending, r)
	b.known[h] = struct{}{}
	return true, nil
}

// sync makes new blobs durable before a frame that references them is
// written, so a power loss can cut a record but never leave one whose blob
// is gone.
func (b *blobStore) sync() error {
	if b.ingest == nil {
		return nil
	}
	if err := b.syncFile(b.ingest); err != nil {
		b.closeIngest(false)
		return fmt.Errorf("sync blob pack: %w", err)
	}
	return nil
}

// forget undoes add for the blobs pending from index mark on, after a failed
// write or sync left their bytes unsafe to point at. A later reference
// stores them again.
func (b *blobStore) forget(mark int) {
	for _, r := range b.pending[mark:] {
		delete(b.known, r.hash)
	}
	clear(b.pending[mark:])
	b.pending = b.pending[:mark]
}

func (b *blobStore) touch(refs []blobHash, ts int64) {
	for _, h := range refs {
		if ts > b.touched[h] {
			b.touched[h] = ts
		}
	}
}

func (b *blobStore) openIngest() error {
	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		return fmt.Errorf("create blob dir: %w", err)
	}
	now := b.now()
	res, err := b.db.Exec(`INSERT INTO reqlog_packs (state, created_at) VALUES (?, ?)`, packIngest, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("insert pack: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("pack id: %w", err)
	}
	f, err := os.OpenFile(b.packPath(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("create pack: %w", err)
	}
	b.ingest, b.ingestID, b.ingestSize, b.ingestOpened = f, id, 0, now
	return nil
}

// closeIngest stops appending to the ingest pack. With markFull, the
// compactor may encode it; callers do that only when no row of the pack is
// pending, because the compactor drops a pack once its committed rows moved.
// Without it (after a write error) the pack waits for recovery at the next
// start.
func (b *blobStore) closeIngest(markFull bool) {
	if b.ingest == nil {
		return
	}
	if err := b.ingest.Close(); err != nil {
		log.WithError(err).Warn("request logs: close blob pack")
	}
	b.ingest = nil
	if !markFull {
		return
	}
	if _, err := b.db.Exec(`UPDATE reqlog_packs SET state = ?, bytes = ? WHERE id = ?`, packFull, b.ingestSize, b.ingestID); err != nil {
		log.WithError(err).Warn("request logs: mark blob pack full")
	}
}

// roll closes the ingest pack when it is due and nothing is pending, and
// reports whether it did.
func (b *blobStore) roll(now time.Time) bool {
	if !b.rollDue(now) || len(b.pending) > 0 {
		return false
	}
	b.closeIngest(true)
	return true
}

func (b *blobStore) rollDue(now time.Time) bool {
	return b.ingest != nil && (b.ingestSize >= ingestPackMax || now.Sub(b.ingestOpened) >= ingestPackAge)
}

// commit writes pending blob rows and last_ts updates in tx.
func (b *blobStore) commit(ctx context.Context, tx *sql.Tx) error {
	if len(b.pending) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO reqlog_blobs (hash, pack_id, offset, stored_bytes, raw_bytes, codec, last_ts) VALUES (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare blob insert: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for _, r := range b.pending {
			if _, err := stmt.ExecContext(ctx, r.hash[:], r.pack, r.offset, r.stored, r.raw, int(r.codec), b.touched[r.hash]); err != nil {
				return fmt.Errorf("insert blob: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE reqlog_packs SET bytes = MAX(bytes, ?) WHERE id = ?`, b.ingestSize, b.ingestID); err != nil {
			return fmt.Errorf("update pack size: %w", err)
		}
	}
	if len(b.touched) > 0 {
		stmt, err := tx.PrepareContext(ctx, `UPDATE reqlog_blobs SET last_ts = ? WHERE hash = ? AND last_ts < ?`)
		if err != nil {
			return fmt.Errorf("prepare blob touch: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for h, ts := range b.touched {
			if _, err := stmt.ExecContext(ctx, ts, h[:], ts); err != nil {
				return fmt.Errorf("touch blob: %w", err)
			}
		}
	}
	return nil
}

func (b *blobStore) committed() {
	clear(b.pending)
	b.pending = b.pending[:0]
	clear(b.touched)
}

// recover runs before the writer starts. It finishes or discards whatever a
// previous run left half done and rebuilds rows the database lost.
func (b *blobStore) recover(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, `SELECT id, state FROM reqlog_packs`)
	if err != nil {
		return fmt.Errorf("list packs: %w", err)
	}
	type pack struct {
		id    int64
		state string
	}
	var packs []pack
	for rows.Next() {
		var p pack
		if err := rows.Scan(&p.id, &p.state); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan pack: %w", err)
		}
		packs = append(packs, p)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("list packs: %w", err)
	}
	owned := make(map[int64]bool, len(packs))
	for _, p := range packs {
		owned[p.id] = true
		if p.state == packIngest || p.state == packFull {
			if err := b.reindexPack(ctx, p.id); err != nil {
				return err
			}
		}
	}
	// A restored database is older than the pack files. Packs the compactor
	// wrote since are unknown to it or still listed as writing, and its rows
	// may point at packs since rewritten and deleted. Every entry describes
	// itself, so such packs are adopted and stale rows repointed. A writing
	// pack left by a crash holds only copies of blobs whose rows still point
	// at their source, so adopting it costs one dead pack until compaction.
	onDisk, err := b.packFiles()
	if err != nil {
		return err
	}
	for _, p := range packs {
		if p.state == packWriting {
			owned[p.id] = false
		}
	}
	for _, id := range onDisk {
		if !owned[id] {
			if err := b.adoptPack(ctx, id, onDisk); err != nil {
				return err
			}
		}
	}
	if err := b.dropMissingPacks(ctx, onDisk); err != nil {
		return err
	}
	hashes, err := b.db.QueryContext(ctx, `SELECT hash FROM reqlog_blobs`)
	if err != nil {
		return fmt.Errorf("load blob hashes: %w", err)
	}
	defer func() { _ = hashes.Close() }()
	for hashes.Next() {
		var raw []byte
		if err := hashes.Scan(&raw); err != nil {
			return fmt.Errorf("scan blob hash: %w", err)
		}
		h, err := hashOf(raw)
		if err != nil {
			return err
		}
		b.known[h] = struct{}{}
	}
	return hashes.Err()
}

// dropMissingPacks forgets packs whose file is gone, and the blobs no
// adopted pack could replace, which no record can be rebuilt with any more.
func (b *blobStore) dropMissingPacks(ctx context.Context, onDisk []int64) error {
	ids, err := queryIDs(ctx, b.db, `SELECT id FROM reqlog_packs`)
	if err != nil {
		return err
	}
	exists := make(map[int64]bool, len(onDisk))
	for _, id := range onDisk {
		exists[id] = true
	}
	for _, id := range ids {
		if exists[id] {
			continue
		}
		res, err := b.db.ExecContext(ctx, `DELETE FROM reqlog_blobs WHERE pack_id = ?`, id)
		if err != nil {
			return fmt.Errorf("drop blobs of missing pack: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log.WithField("pack", id).WithField("blobs", n).Warn("request logs: blob pack missing; records using its blobs cannot be read")
		}
		if _, err := b.db.ExecContext(ctx, `DELETE FROM reqlog_packs WHERE id = ?`, id); err != nil {
			return fmt.Errorf("drop missing pack: %w", err)
		}
	}
	return nil
}

func (b *blobStore) packFiles() ([]int64, error) {
	files, err := os.ReadDir(b.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list blob packs: %w", err)
	}
	var ids []int64
	for _, f := range files {
		stem, ext, _ := strings.Cut(f.Name(), ".")
		if id, err := strconv.ParseInt(stem, 10, 64); err == nil && ext == "pack" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// adoptPack records a pack file as packed and points at it every blob whose
// row is missing or names a pack file that no longer exists.
func (b *blobStore) adoptPack(ctx context.Context, id int64, onDisk []int64) error {
	exists := make(map[int64]bool, len(onDisk))
	for _, p := range onDisk {
		exists[p] = true
	}
	f, err := os.OpenFile(b.packPath(id), os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open pack: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat pack: %w", err)
	}
	var entries []blobRow
	validEnd, err := scanPack(f, info.Size(), func(r blobRow) { entries = append(entries, r) })
	if err != nil {
		return fmt.Errorf("scan pack %d: %w", id, err)
	}
	if info.Size() > validEnd {
		if err := f.Truncate(validEnd); err != nil {
			return fmt.Errorf("truncate torn pack tail: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync pack %d: %w", id, err)
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pack adoption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := b.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO reqlog_packs (id, state, bytes, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET state = excluded.state, bytes = excluded.bytes`, id, packPacked, validEnd, now); err != nil {
		return fmt.Errorf("adopt pack: %w", err)
	}
	for _, r := range entries {
		var cur int64
		err := tx.QueryRowContext(ctx, `SELECT pack_id FROM reqlog_blobs WHERE hash = ?`, r.hash[:]).Scan(&cur)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `INSERT INTO reqlog_blobs (hash, pack_id, offset, stored_bytes, raw_bytes, codec, last_ts) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				r.hash[:], id, r.offset, r.stored, r.raw, int(r.codec), now)
		case err == nil && !exists[cur]:
			_, err = tx.ExecContext(ctx, `UPDATE reqlog_blobs SET pack_id = ?, offset = ?, stored_bytes = ?, codec = ? WHERE hash = ?`,
				id, r.offset, r.stored, int(r.codec), r.hash[:])
		}
		if err != nil {
			return fmt.Errorf("adopt blob: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pack adoption: %w", err)
	}
	log.WithField("pack", id).WithField("entries", len(entries)).Info("request logs: adopted blob pack")
	return nil
}

// reindexPack restores rows for complete raw entries, cuts a torn tail, and
// marks the pack full.
func (b *blobStore) reindexPack(ctx context.Context, id int64) error {
	f, err := os.OpenFile(b.packPath(id), os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		return b.dropPack(ctx, id)
	}
	if err != nil {
		return fmt.Errorf("open pack: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat pack: %w", err)
	}
	created := b.now().UnixMilli()
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pack reindex: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO reqlog_blobs (hash, pack_id, offset, stored_bytes, raw_bytes, codec, last_ts) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare pack reindex: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	var errInsert error
	validEnd, err := scanPack(f, info.Size(), func(r blobRow) {
		if errInsert == nil {
			_, errInsert = stmt.ExecContext(ctx, r.hash[:], id, r.offset, r.stored, r.raw, int(r.codec), created)
		}
	})
	if err != nil {
		return fmt.Errorf("scan pack %d: %w", id, err)
	}
	if errInsert != nil {
		return fmt.Errorf("reindex pack %d: %w", id, errInsert)
	}
	if info.Size() > validEnd {
		if err := f.Truncate(validEnd); err != nil {
			return fmt.Errorf("truncate torn pack tail: %w", err)
		}
	}
	// Rebuilt rows let new records skip the pre-frame sync, so the bytes
	// they name must be on disk first.
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync pack %d: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE reqlog_packs SET state = ?, bytes = ? WHERE id = ?`, packFull, validEnd, id); err != nil {
		return fmt.Errorf("mark pack full: %w", err)
	}
	return tx.Commit()
}

// scanPack visits complete entries and returns where the last one ended. A
// raw entry is checked against its hash; an encoded one would need decoding,
// so get checks it when it is read.
func scanPack(r io.Reader, size int64, visit func(blobRow)) (int64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var pos int64
	for {
		line, err := br.ReadSlice('\n')
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, bufio.ErrBufferFull) {
				return pos, nil
			}
			return pos, err
		}
		row, ok := parseEntryHeader(line)
		if !ok || int64(len(line))+int64(row.stored)+1 > size-pos {
			return pos, nil
		}
		payload := make([]byte, row.stored)
		if _, err := io.ReadFull(br, payload); err != nil {
			return pos, ignoreEOF(err)
		}
		last, err := br.ReadByte()
		if err != nil {
			return pos, ignoreEOF(err)
		}
		if last != '\n' || (row.codec == codec.Raw && blobHash(sha256.Sum256(payload)) != row.hash) {
			return pos, nil
		}
		row.offset = pos + int64(len(line))
		visit(row)
		pos = row.offset + int64(row.stored) + 1
	}
}

func parseEntryHeader(line []byte) (blobRow, bool) {
	var r blobRow
	if len(line) > 160 || !bytes.HasPrefix(line, []byte(entryMagic)) {
		return r, false
	}
	f := bytes.Fields(line[len(entryMagic):])
	if len(f) != 4 || len(f[3]) != 2*len(blobHash{}) {
		return r, false
	}
	kind, errKind := strconv.Atoi(string(f[0]))
	raw, errRaw := strconv.Atoi(string(f[1]))
	stored, errStored := strconv.Atoi(string(f[2]))
	if errKind != nil || errRaw != nil || errStored != nil || kind < 0 || kind > 255 || raw < 0 || stored < 0 {
		return r, false
	}
	if _, err := hex.Decode(r.hash[:], f[3]); err != nil {
		return r, false
	}
	r.codec, r.raw, r.stored = codec.Kind(kind), raw, stored
	return r, true
}

func (b *blobStore) dropPack(ctx context.Context, id int64) error {
	res, err := b.db.ExecContext(ctx, `DELETE FROM reqlog_packs WHERE id = ? AND NOT EXISTS (SELECT 1 FROM reqlog_blobs WHERE pack_id = ?)`, id, id)
	if err != nil {
		return fmt.Errorf("drop pack: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	if err := os.Remove(b.packPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		// The sweep removes it once no reader holds it.
		log.WithError(err).WithField("pack", id).Debug("request logs: remove pack")
	}
	return nil
}

// gc drops blobs no remaining record references. It runs on the writer
// goroutine, so no record can gain a reference to a blob it deletes, and it
// waits while a failed commit left blob rows or last_ts updates pending.
func (b *blobStore) gc(ctx context.Context) (int, error) {
	if len(b.pending) > 0 || len(b.touched) > 0 {
		return 0, nil
	}
	var oldest sql.NullInt64
	if err := b.db.QueryRowContext(ctx, `SELECT MIN(ts) FROM reqlog_records`).Scan(&oldest); err != nil {
		return 0, fmt.Errorf("oldest record: %w", err)
	}
	cutoff := int64(1<<62 - 1)
	if oldest.Valid {
		cutoff = oldest.Int64
	}
	rows, err := b.db.QueryContext(ctx, `SELECT hash FROM reqlog_blobs WHERE last_ts < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("find dead blobs: %w", err)
	}
	var dead []blobHash
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan dead blob: %w", err)
		}
		h, err := hashOf(raw)
		if err != nil {
			_ = rows.Close()
			return 0, err
		}
		dead = append(dead, h)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("find dead blobs: %w", err)
	}
	if len(dead) == 0 {
		return 0, nil
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin blob gc: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, h := range dead {
		if _, err := tx.ExecContext(ctx, `DELETE FROM reqlog_blobs WHERE hash = ?`, h[:]); err != nil {
			return 0, fmt.Errorf("delete blob: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit blob gc: %w", err)
	}
	for _, h := range dead {
		delete(b.known, h)
	}
	return len(dead), nil
}

// get returns a blob's decoded bytes, checked against its hash.
func (b *blobStore) get(ctx context.Context, h blobHash) ([]byte, error) {
	if raw, ok := b.cache.get(h); ok {
		return raw, nil
	}
	// A compaction can move the blob between the lookup and the read.
	for attempt := 0; ; attempt++ {
		var r blobRow
		var kind int
		err := b.db.QueryRowContext(ctx, `SELECT pack_id, offset, stored_bytes, raw_bytes, codec FROM reqlog_blobs WHERE hash = ?`, h[:]).
			Scan(&r.pack, &r.offset, &r.stored, &r.raw, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("blob %x not stored", h[:8])
		}
		if err != nil {
			return nil, fmt.Errorf("find blob: %w", err)
		}
		stored, err := readAt(b.packPath(r.pack), r.offset, r.stored)
		if errors.Is(err, os.ErrNotExist) && attempt == 0 {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read blob %x: %w", h[:8], err)
		}
		raw, err := codec.Unpack(codec.Kind(kind), stored, r.raw)
		if err != nil {
			return nil, fmt.Errorf("decode blob %x: %w", h[:8], err)
		}
		if blobHash(sha256.Sum256(raw)) != h {
			return nil, fmt.Errorf("blob %x does not match its hash", h[:8])
		}
		b.cache.put(h, raw)
		return raw, nil
	}
}

// getAll decodes the distinct blobs of a record in parallel.
func (b *blobStore) getAll(ctx context.Context, refs []blobRef) (map[blobHash][]byte, error) {
	out := make(map[blobHash][]byte, len(refs))
	var want []blobHash
	for _, r := range refs {
		if _, ok := out[r.hash]; !ok {
			out[r.hash] = nil
			want = append(want, r.hash)
		}
	}
	var mu sync.Mutex
	var firstErr error
	sem := make(chan struct{}, max(2, runtime.NumCPU()/2))
	var wg sync.WaitGroup
	for _, h := range want {
		wg.Add(1)
		sem <- struct{}{}
		go func(h blobHash) {
			defer wg.Done()
			defer func() { <-sem }()
			raw, err := b.get(ctx, h)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			out[h] = raw
		}(h)
	}
	wg.Wait()
	return out, firstErr
}

func readAt(path string, offset int64, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return nil, err
	}
	return buf, nil
}

// packFull encodes the live blobs of every full ingest pack into a new
// packed pack, then drops the ingest pack.
func (b *blobStore) packFull(ctx context.Context) error {
	ids, err := queryIDs(ctx, b.db, `SELECT id FROM reqlog_packs WHERE state = ? ORDER BY id`, packFull)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := b.rewrite(ctx, []int64{id}, true); err != nil {
			return fmt.Errorf("pack %d: %w", id, err)
		}
	}
	return nil
}

// compactPacked rewrites packed packs more than a quarter dead into one new
// pack, copying entries without decoding them. That bounds how far disk use
// can exceed retention's max-bytes, which counts live blobs only.
func (b *blobStore) compactPacked(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, `SELECT p.id FROM reqlog_packs p
		LEFT JOIN (SELECT pack_id, SUM(stored_bytes + ?) AS live FROM reqlog_blobs GROUP BY pack_id) l ON l.pack_id = p.id
		WHERE p.state = ? AND COALESCE(l.live, 0) * 4 < p.bytes * 3 ORDER BY p.id`, entryOverhead, packPacked)
	if err != nil {
		return fmt.Errorf("find sparse packs: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan sparse pack: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("find sparse packs: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	return b.rewrite(ctx, ids, false)
}

// rewrite moves the live entries of src packs into a new pack. With encode,
// each raw entry is replaced by codec.Pack's best verified encoding.
func (b *blobStore) rewrite(ctx context.Context, src []int64, encode bool) error {
	var live []blobRow
	for _, id := range src {
		rows, err := b.db.QueryContext(ctx, `SELECT hash, offset, stored_bytes, raw_bytes, codec FROM reqlog_blobs WHERE pack_id = ? ORDER BY offset`, id)
		if err != nil {
			return fmt.Errorf("list pack blobs: %w", err)
		}
		for rows.Next() {
			var r blobRow
			var hash []byte
			var kind int
			if err := rows.Scan(&hash, &r.offset, &r.stored, &r.raw, &kind); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan pack blob: %w", err)
			}
			if r.hash, err = hashOf(hash); err != nil {
				_ = rows.Close()
				return err
			}
			r.pack, r.codec = id, codec.Kind(kind)
			live = append(live, r)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("list pack blobs: %w", err)
		}
	}
	if len(live) == 0 {
		for _, id := range src {
			if err := b.dropPack(ctx, id); err != nil {
				return err
			}
		}
		return nil
	}
	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		return fmt.Errorf("create blob dir: %w", err)
	}
	res, err := b.db.ExecContext(ctx, `INSERT INTO reqlog_packs (state, created_at) VALUES (?, ?)`, packWriting, b.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("insert pack: %w", err)
	}
	outID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("pack id: %w", err)
	}
	out, err := os.OpenFile(b.packPath(outID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create pack: %w", err)
	}
	moved, size, errWrite := b.writeEntries(ctx, out, live, encode)
	if errWrite == nil {
		errWrite = out.Sync()
	}
	if errClose := out.Close(); errWrite == nil {
		errWrite = errClose
	}
	if errWrite != nil {
		_ = b.dropPack(ctx, outID)
		return errWrite
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pack move: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range moved {
		// A row GC removed meanwhile stays removed.
		if _, err := tx.ExecContext(ctx, `UPDATE reqlog_blobs SET pack_id = ?, offset = ?, stored_bytes = ?, codec = ? WHERE hash = ? AND pack_id = ?`,
			outID, r.offset, r.stored, int(r.codec), r.hash[:], r.pack); err != nil {
			return fmt.Errorf("move blob: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE reqlog_packs SET state = ?, bytes = ? WHERE id = ?`, packPacked, size, outID); err != nil {
		return fmt.Errorf("mark pack packed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pack move: %w", err)
	}
	for _, id := range src {
		if err := b.dropPack(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// writeEntries reads, optionally encodes, and appends entries in order.
// Encoding runs on several goroutines; at most 2*workers results wait to be
// written.
func (b *blobStore) writeEntries(ctx context.Context, out *os.File, live []blobRow, encode bool) ([]blobRow, int64, error) {
	type job struct {
		row  blobRow
		data []byte
		err  error
		done chan struct{}
	}
	workers := 1
	if encode {
		workers = max(1, runtime.NumCPU()/2)
	}
	jobs := make([]*job, len(live))
	for i := range live {
		jobs[i] = &job{row: live[i], done: make(chan struct{})}
	}
	run := func(j *job) {
		defer close(j.done)
		if err := ctx.Err(); err != nil {
			j.err = err
			return
		}
		data, err := readAt(b.packPath(j.row.pack), j.row.offset, j.row.stored)
		if err != nil {
			j.err = fmt.Errorf("read blob: %w", err)
			return
		}
		if encode && j.row.codec == codec.Raw {
			j.row.codec, data = codec.Pack(data)
		}
		j.row.stored = len(data)
		j.data = data
	}
	window := make(chan struct{}, 2*workers)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i, j := range jobs {
			select {
			case window <- struct{}{}:
				go run(j)
			case <-stop:
				for _, rest := range jobs[i:] {
					rest.err = context.Canceled
					close(rest.done)
				}
				return
			}
		}
	}()
	w := bufio.NewWriterSize(out, 1<<20)
	var pos int64
	moved := make([]blobRow, 0, len(live))
	for _, j := range jobs {
		<-j.done
		<-window
		if j.err != nil {
			return nil, 0, j.err
		}
		r := j.row
		head := entryHeader(r)
		r.offset = pos + int64(len(head))
		for _, part := range [][]byte{head, j.data, {'\n'}} {
			if _, err := w.Write(part); err != nil {
				return nil, 0, fmt.Errorf("write pack: %w", err)
			}
		}
		pos = r.offset + int64(r.stored) + 1
		moved = append(moved, r)
		j.data = nil
	}
	if err := w.Flush(); err != nil {
		return nil, 0, fmt.Errorf("write pack: %w", err)
	}
	return moved, pos, nil
}

// blobCache keeps recently decoded blobs. Consecutive turns of a
// conversation carry the same images, so the detail view of the next record
// mostly hits.
type blobCache struct {
	mu    sync.Mutex
	limit int
	size  int
	order *list.List
	items map[blobHash]*list.Element
}

type cacheEntry struct {
	hash blobHash
	raw  []byte
}

func newBlobCache(limit int) *blobCache {
	return &blobCache{limit: limit, order: list.New(), items: make(map[blobHash]*list.Element)}
}

func (c *blobCache) get(h blobHash) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[h]; ok {
		c.order.MoveToFront(el)
		return el.Value.(*cacheEntry).raw, true
	}
	return nil, false
}

func (c *blobCache) put(h blobHash, raw []byte) {
	if len(raw) > c.limit/4 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[h]; ok {
		return
	}
	c.items[h] = c.order.PushFront(&cacheEntry{hash: h, raw: raw})
	c.size += len(raw)
	for c.size > c.limit {
		el := c.order.Back()
		e := el.Value.(*cacheEntry)
		c.order.Remove(el)
		delete(c.items, e.hash)
		c.size -= len(e.raw)
	}
}
