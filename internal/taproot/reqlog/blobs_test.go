package reqlog

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/265866/taproot/internal/taproot/reqlog/codec"
)

func testImage(seed int64, w, h int) *image.RGBA {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 3), uint8(y * 5), uint8(rng.Intn(32)), 255})
		}
	}
	return img
}

func testJPEG(t *testing.T, seed int64) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, testImage(seed, 160, 120), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func testPNG(t *testing.T, seed int64) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, testImage(seed, 120, 90)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func testGIF(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := gif.Encode(&b, testImage(5, 64, 48), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// testWebP is a WebP header padded with zeros: enough to sniff, and
// compressible, so the compactor stores it as zstd.
func testWebP() []byte {
	b := make([]byte, 4096)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WEBPVP8L")
	return b
}

// imageTurn is a Claude-style request carrying every image so far, as Claude
// Code resends the whole conversation each turn.
func imageTurn(images [][]byte) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"claude-opus-5-5","messages":[`)
	for i, img := range images {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"%s"}},{"type":"text","text":"look %d"}]}`,
			base64.StdEncoding.EncodeToString(img), i)
	}
	b.WriteString("]}")
	return []byte(b.String())
}

func blobCount(t *testing.T, h *harness) int {
	t.Helper()
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM reqlog_blobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestBlobsStoredOnceAndRebuiltExactly sends a conversation whose every turn
// resends all earlier images in both the request and the upstream copy. Each
// image must be stored once, and every record must read back byte for byte,
// before and after sealing and after the compactor encodes the blobs.
func TestBlobsStoredOnceAndRebuiltExactly(t *testing.T) {
	h := newHarness(t, Config{})
	// Preflate cannot model Go's PNG encoder, so the PNG is a zlib-made one.
	zlibPNG, err := os.ReadFile(filepath.Join("codec", "testdata", "zlib.png"))
	if err != nil {
		t.Fatal(err)
	}
	images := [][]byte{testJPEG(t, 1), zlibPNG, testJPEG(t, 3), make([]byte, 8192), testGIF(t), testWebP()}
	ts := h.clock.now()
	bodies := map[string][]byte{}
	raws := map[string][]byte{}
	for turn := 1; turn <= len(images); turn++ {
		id := fmt.Sprintf("img-%d", turn)
		bodies[id] = imageTurn(images[:turn])
		logSimple(t, h.logs.logger, id, "/v1/messages", "images", 200, ts.Add(time.Duration(turn)*time.Second), bodies[id])
	}
	h.flush()
	if got := blobCount(t, h); got != len(images) {
		t.Fatalf("stored %d blobs for %d distinct images", got, len(images))
	}
	check := func(stage string) {
		t.Helper()
		for id, body := range bodies {
			d, err := h.logs.Get(context.Background(), id, true)
			if err != nil {
				t.Fatalf("%s: get %s: %v", stage, id, err)
			}
			if d.Request.Body != string(body) || !strings.Contains(d.Upstream.Request, string(body)) {
				t.Fatalf("%s: %s did not rebuild exactly", stage, id)
			}
			checkRefs(t, h, id, d)
			_, raw, err := h.logs.Raw(context.Background(), id)
			if err != nil {
				t.Fatalf("%s: raw %s: %v", stage, id, err)
			}
			if prev, ok := raws[id]; ok && !bytes.Equal(prev, raw) {
				t.Fatalf("%s: raw text of %s changed", stage, id)
			}
			raws[id] = raw
		}
	}
	check("open")
	h.sealAll()
	check("sealed")

	if err := h.logs.engine.do(context.Background(), func() {
		h.logs.engine.blobs.ingestOpened = time.Time{}
		h.logs.engine.rollBlobs()
	}); err != nil {
		t.Fatal(err)
	}
	h.logs.engine.blobs.cache = newBlobCache(blobCacheMax)
	if err := h.logs.engine.blobs.packFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	var full, packed int
	if err := h.db.QueryRow(`SELECT COALESCE(SUM(state = ?), 0), COALESCE(SUM(state = ?), 0) FROM reqlog_packs`, packFull, packPacked).Scan(&full, &packed); err != nil {
		t.Fatal(err)
	}
	if full != 0 || packed != 1 {
		t.Fatalf("after packing: %d full and %d packed packs, want 0 and 1", full, packed)
	}
	var zeros, webp int
	if err := h.db.QueryRow(`SELECT (SELECT codec FROM reqlog_blobs WHERE raw_bytes = 8192), (SELECT codec FROM reqlog_blobs WHERE raw_bytes = 4096)`).Scan(&zeros, &webp); err != nil {
		t.Fatal(err)
	}
	if codec.Kind(zeros) != codec.Zstd || codec.Kind(webp) != codec.Zstd {
		t.Fatalf("zero-filled blob and WebP packed as kinds %d and %d, want zstd", zeros, webp)
	}
	if codec.Native() {
		var lepton, pngs int
		if err := h.db.QueryRow(`SELECT COALESCE(SUM(codec = ?), 0), COALESCE(SUM(codec = ?), 0) FROM reqlog_blobs`, int(codec.Lepton), int(codec.PNG)).Scan(&lepton, &pngs); err != nil {
			t.Fatal(err)
		}
		if lepton != 2 || pngs != 1 {
			t.Fatalf("native codecs packed %d JPEGs and %d PNGs, want 2 and 1", lepton, pngs)
		}
	}
	check("packed")

	// The packs survive a restart, including rows the cache never saw.
	h.stop()
	h.open(Config{})
	h.logs.engine.blobs.cache = newBlobCache(blobCacheMax)
	check("reopened")
}

var placeholderRe = regexp.MustCompile(`\[taproot-blob:([0-9a-f]{64}):([0-9]+)\]`)

// checkRefs reads id without blobs and checks that putting each blob's base64
// in place of its placeholder gives the inline detail back, that every blob
// was left out, and that each image's type was sniffed from its stored bytes.
func checkRefs(t *testing.T, h *harness, id string, inline *Detail) {
	t.Helper()
	// An empty cache makes the type come from the pack, not cached bytes.
	h.logs.engine.blobs.cache = newBlobCache(blobCacheMax)
	d, err := h.logs.Get(context.Background(), id, false)
	if err != nil {
		t.Fatalf("get %s without blobs: %v", id, err)
	}
	expand := func(s string) string {
		return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
			sub := placeholderRe.FindStringSubmatch(m)
			info, ok := d.Blobs[sub[1]]
			if !ok || strconv.Itoa(info.Bytes) != sub[2] {
				t.Fatalf("%s: placeholder %s not described in blobs", id, m)
			}
			hb, _ := hex.DecodeString(sub[1])
			raw, err := h.logs.engine.blobs.get(context.Background(), blobHash(hb))
			if err != nil || len(raw) != info.Bytes {
				t.Fatalf("%s: blob %s: %v (%d bytes)", id, sub[1], err, len(raw))
			}
			want := http.DetectContentType(raw)
			if !strings.HasPrefix(want, "image/") {
				want = ""
			}
			if info.Type != want {
				t.Fatalf("%s: blob %s typed %q, want %q", id, sub[1][:8], info.Type, want)
			}
			return base64.StdEncoding.EncodeToString(raw)
		})
	}
	sections := func(d *Detail) []string {
		return []string{d.Request.Body, d.Response.Body, d.Upstream.Request, d.Upstream.Response, d.Upstream.WebsocketTimeline, d.Upstream.APIWebsocketTimeline}
	}
	want := sections(inline)
	for i, got := range sections(d) {
		if expand(got) != want[i] {
			t.Fatalf("%s: section %d differs once placeholders are expanded", id, i)
		}
		if json.Valid([]byte(want[i])) && !json.Valid([]byte(got)) {
			t.Fatalf("%s: section %d is JSON, but not with placeholders", id, i)
		}
		// Only a run after an escaping backslash keeps its base64.
		residual, refs := splitBlobs([]byte(got), func(blobHash, []byte) {})
		for _, r := range refs {
			if !escapedAt(residual[:r.pos]) {
				t.Fatalf("%s: section %d still holds a base64 payload at %d", id, i, r.pos)
			}
		}
	}
}

// TestLetterRunsThatLookLikeBase64 checks that text which happens to be valid
// base64, such as a long run of letters, still reads back as written.
func TestLetterRunsThatLookLikeBase64(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	bodies := map[string][]byte{
		"letters":  []byte(`{"text":"` + strings.Repeat("abcd", 600) + `"}`),
		"padded":   []byte(`{"text":"` + strings.Repeat("QUJD", 400) + `QQ=="}`),
		"unpadded": []byte(`{"text":"` + strings.Repeat("QUJD", 400) + `QQ"}`),
		"noncanon": []byte(`{"text":"` + strings.Repeat("QUJD", 400) + `QR=="}`),
		"short":    []byte(`{"text":"` + strings.Repeat("QUJD", 255) + `"}`),
	}
	for id, body := range bodies {
		logSimple(t, h.logs.logger, id, "/v1/messages", "letters", 200, ts, body)
	}
	h.flush()
	h.sealAll()
	for id, body := range bodies {
		d, err := h.logs.Get(context.Background(), id, true)
		if err != nil || d.Request.Body != string(body) {
			t.Fatalf("%s did not rebuild exactly: %v", id, err)
		}
	}
	// "letters" and "padded" are canonical and long enough; the rest stay text.
	if got := blobCount(t, h); got != 2 {
		t.Fatalf("stored %d blobs, want 2", got)
	}
}

// TestRefDetailKeepsEscapedRunsValidJSON stores base64-looking runs that
// start with the letter of a JSON escape. A placeholder there would leave an
// invalid "\[" escape, so such a run keeps its base64 in the ref detail.
func TestRefDetailKeepsEscapedRunsValidJSON(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	letters := strings.Repeat("abcd", 511) + "abc"
	bodies := map[string][]byte{
		"escape":    []byte(`{"text":"line\n` + letters + `"}`),
		"backslash": []byte(`{"text":"a\\` + letters + `d"}`),
	}
	for id, body := range bodies {
		logSimple(t, h.logs.logger, id, "/v1/messages", "escapes", 200, ts, body)
	}
	h.flush()
	h.sealAll()
	if got := blobCount(t, h); got != 2 {
		t.Fatalf("stored %d blobs, want 2", got)
	}
	for id, body := range bodies {
		inline, err := h.logs.Get(context.Background(), id, true)
		if err != nil || inline.Request.Body != string(body) {
			t.Fatalf("%s did not rebuild exactly: %v", id, err)
		}
		checkRefs(t, h, id, inline)
		d, err := h.logs.Get(context.Background(), id, false)
		if err != nil {
			t.Fatal(err)
		}
		if placeholder := strings.Contains(d.Request.Body, "[taproot-blob:"); placeholder != (id == "backslash") {
			t.Fatalf("%s: placeholder in ref body = %v", id, placeholder)
		}
	}
}

// TestBlobGCKeepsSharedBlobs drops old segments and checks that a blob only
// they used is collected while one a remaining record shares survives, and
// that the sparse pack left behind is rewritten.
func TestBlobGCKeepsSharedBlobs(t *testing.T) {
	h := newHarness(t, Config{MaxDays: 2, MaxBytes: -1})
	now := h.clock.now()
	shared, onlyOld := testJPEG(t, 10), testPNG(t, 11)
	old := now.Add(-5 * 24 * time.Hour)
	logSimple(t, h.logs.logger, "old", "/v1/messages", "s-old", 200, old, imageTurn([][]byte{shared, onlyOld}))
	logSimple(t, h.logs.logger, "new", "/v1/messages", "s-new", 200, now, imageTurn([][]byte{shared}))
	h.flush()
	h.sealAll()
	if err := h.logs.engine.do(context.Background(), func() {
		h.logs.engine.blobs.ingestOpened = time.Time{}
		h.logs.engine.rollBlobs()
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.logs.engine.blobs.packFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := blobCount(t, h); got != 2 {
		t.Fatalf("before retention: %d blobs, want 2", got)
	}
	h.logs.engine.applyRetention(context.Background())
	if _, err := h.logs.Get(context.Background(), "old", true); err == nil {
		t.Fatal("old record survived max-days")
	}
	if got := blobCount(t, h); got != 1 {
		t.Fatalf("after retention: %d blobs, want only the shared one", got)
	}
	h.logs.engine.blobs.cache = newBlobCache(blobCacheMax)
	d, err := h.logs.Get(context.Background(), "new", true)
	if err != nil || d.Request.Body != string(imageTurn([][]byte{shared})) {
		t.Fatalf("shared blob lost: %v", err)
	}
	var packs int
	var bytesOnDisk, live int64
	if err := h.db.QueryRow(`SELECT COUNT(*), SUM(bytes) FROM reqlog_packs`).Scan(&packs, &bytesOnDisk); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(`SELECT SUM(stored_bytes) FROM reqlog_blobs`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if packs != 1 || bytesOnDisk > 2*live {
		t.Fatalf("sparse pack not rewritten: %d packs, %d bytes for %d live", packs, bytesOnDisk, live)
	}
	files, _ := filepath.Glob(filepath.Join(h.dataDir, "request-logs", "blobs", "*.pack"))
	if len(files) != 1 {
		t.Fatalf("pack files on disk: %v", files)
	}
}

// TestBlobPackRecoveredAfterCrash loses the database's blob rows the way a
// crash between writing a blob and committing its row would, and checks that
// the next start rebuilds them from the pack and cuts a torn entry.
func TestBlobPackRecoveredAfterCrash(t *testing.T) {
	h := newHarness(t, Config{})
	img := testJPEG(t, 20)
	logSimple(t, h.logs.logger, "crash", "/v1/messages", "s", 200, h.clock.now(), imageTurn([][]byte{img}))
	h.flush()
	h.stop()
	if _, err := h.db.Exec(`DELETE FROM reqlog_blobs`); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(h.dataDir, "request-logs", "blobs", "*.pack"))
	if len(files) != 1 {
		t.Fatalf("pack files: %v", files)
	}
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	torn := entryHeader(blobRow{raw: 5000, stored: 5000})
	_, _ = f.Write(append(torn, "only part of it"...))
	_ = f.Close()

	h.open(Config{})
	if got := blobCount(t, h); got != 1 {
		t.Fatalf("recovered %d blobs, want 1", got)
	}
	d, err := h.logs.Get(context.Background(), "crash", true)
	if err != nil || d.Request.Body != string(imageTurn([][]byte{img})) {
		t.Fatalf("record after recovery: %v", err)
	}
	info, err := os.Stat(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var size int64
	if err := h.db.QueryRow(`SELECT bytes FROM reqlog_packs`).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if info.Size() != size {
		t.Fatalf("torn tail kept: file %d bytes, pack row %d", info.Size(), size)
	}
	// A rebuilt row must not be collectable while its record exists.
	var lastTs int64
	if err := h.db.QueryRow(`SELECT last_ts FROM reqlog_blobs`).Scan(&lastTs); err != nil {
		t.Fatal(err)
	}
	if lastTs < h.clock.now().UnixMilli() {
		t.Fatalf("recovered blob last_ts %d is older than its record %d", lastTs, h.clock.now().UnixMilli())
	}
	if n, err := h.logs.engine.blobs.gc(context.Background()); err != nil || n != 0 {
		t.Fatalf("gc collected %d blobs (err %v) still in use", n, err)
	}
}

// TestDictionaryTrainedAndUsed seals enough segments to train a dictionary,
// then checks that later segments use it and every record still reads back.
func TestDictionaryTrainedAndUsed(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	tools := strings.Repeat(`{"name":"Bash","description":"Executes a given bash command and returns its output.","input_schema":{"type":"object"}},`, 60)
	body := func(i int) []byte {
		return []byte(fmt.Sprintf(`{"model":"claude-opus-5-5","messages":[{"role":"user","content":"task %d %s"}],"tools":[%s{}]}`, i, strings.Repeat("words ", i%9), tools))
	}
	for i := 0; i < dictMinSegments; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("before-%d", i), "/v1/messages", fmt.Sprintf("sb-%d", i), 200, ts, body(i))
	}
	h.flush()
	h.sealAll()
	id, err := h.logs.engine.trainDict(context.Background())
	if err != nil || id == 0 {
		t.Fatalf("train: id %d err %v", id, err)
	}
	for i := 0; i < 4; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("after-%d", i), "/v1/messages", fmt.Sprintf("sa-%d", i), 200, ts, body(100+i))
	}
	h.flush()
	h.sealAll()
	var withDict int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM reqlog_segments WHERE dict_id = ?`, id).Scan(&withDict); err != nil {
		t.Fatal(err)
	}
	if withDict != 4 {
		t.Fatalf("%d segments sealed with the dictionary, want 4", withDict)
	}
	h.stop()
	h.open(Config{})
	for i := 0; i < dictMinSegments; i++ {
		if d, err := h.logs.Get(context.Background(), fmt.Sprintf("before-%d", i), true); err != nil || d.Request.Body != string(body(i)) {
			t.Fatalf("before-%d: %v", i, err)
		}
	}
	for i := 0; i < 4; i++ {
		if d, err := h.logs.Get(context.Background(), fmt.Sprintf("after-%d", i), true); err != nil || d.Request.Body != string(body(100+i)) {
			t.Fatalf("after-%d: %v", i, err)
		}
	}
	// A newer dictionary does not drop one segments still use, and an unused
	// older one goes.
	unused, err := h.logs.engine.dicts.add(bytes.Repeat([]byte("u"), 8192))
	if err != nil {
		t.Fatal(err)
	}
	newest, err := h.logs.engine.dicts.add(bytes.Repeat([]byte("x"), 8192))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.logs.engine.dicts.prune(context.Background(), h.clock.now(), 0, nil); err != nil {
		t.Fatal(err)
	}
	ids, err := h.logs.engine.dicts.ids()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []int64{id, newest}) {
		t.Fatalf("dictionaries after prune %v, want %d and %d (not %d)", ids, id, newest, unused)
	}
}

// TestRestoreKeepsBlobsAndDictionaries restores a database snapshot taken
// before blobs were packed, packs were compacted, and a segment sealed with
// a dictionary the snapshot never saw. Every record from before the snapshot
// must still read back exactly.
func TestRestoreKeepsBlobsAndDictionaries(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	tools := strings.Repeat(`{"name":"Read","description":"Reads a file from the local filesystem."},`, 40)
	sealed := imageTurn([][]byte{testJPEG(t, 40)})
	for i := 0; i < dictMinSegments; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("seed-%d", i), "/v1/messages", fmt.Sprintf("seed-%d", i), 200, ts, []byte(fmt.Sprintf(`{"n":%d,"tools":[%s{}]}`, i, tools)))
	}
	logSimple(t, h.logs.logger, "sealed-before", "/v1/messages", "a", 200, ts, sealed)
	h.flush()
	h.sealAll()
	open := imageTurn([][]byte{testPNG(t, 41), testJPEG(t, 40)})
	logSimple(t, h.logs.logger, "open-at-snapshot", "/v1/messages", "b", 200, ts, open)
	h.flush()

	snapshot := filepath.Join(t.TempDir(), "snap.db")
	if err := h.db.Snapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	// After the snapshot: a dictionary appears, the open segment seals with
	// it, the ingest pack is encoded and deleted, and the packed pack is
	// rewritten once the shared blob's sibling dies.
	if _, err := h.logs.engine.trainDict(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.sealAll()
	if err := h.logs.engine.do(context.Background(), func() {
		h.logs.engine.blobs.ingestOpened = time.Time{}
		h.logs.engine.rollBlobs()
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.logs.engine.blobs.packFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`UPDATE reqlog_packs SET bytes = bytes * 8`); err != nil {
		t.Fatal(err)
	}
	if err := h.logs.engine.blobs.compactPacked(context.Background()); err != nil {
		t.Fatal(err)
	}

	h.restoreSnapshot(snapshot)
	h.logs.engine.compactQueued(context.Background())
	h.logs.engine.blobs.cache = newBlobCache(blobCacheMax)
	for id, body := range map[string][]byte{"sealed-before": sealed, "open-at-snapshot": open} {
		d, err := h.logs.Get(context.Background(), id, true)
		if err != nil {
			t.Fatalf("%s after restore: %v", id, err)
		}
		if d.Request.Body != string(body) {
			t.Fatalf("%s after restore did not rebuild exactly", id)
		}
	}
}

func TestStatsCountBlobsAndOriginalBytes(t *testing.T) {
	h := newHarness(t, Config{})
	body := imageTurn([][]byte{testJPEG(t, 30)})
	logSimple(t, h.logs.logger, "s1", "/v1/messages", "s", 200, h.clock.now(), body)
	h.flush()
	st, err := h.logs.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := h.logs.Raw(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if st.RawBytes != int64(len(raw)) {
		t.Fatalf("rawBytes %d, want the record's %d", st.RawBytes, len(raw))
	}
	var packBytes int64
	if err := h.db.QueryRow(`SELECT SUM(bytes) FROM reqlog_packs`).Scan(&packBytes); err != nil {
		t.Fatal(err)
	}
	if packBytes == 0 || st.BytesOnDisk < packBytes {
		t.Fatalf("bytesOnDisk %d does not include %d pack bytes", st.BytesOnDisk, packBytes)
	}
}

// TestBlobGCWaitsForPendingCommits keeps a failed commit's last_ts update
// pending and checks that GC leaves the blob it would refresh.
func TestBlobGCWaitsForPendingCommits(t *testing.T) {
	h := newHarness(t, Config{})
	logSimple(t, h.logs.logger, "g", "/v1/messages", "s", 200, h.clock.now(), imageTurn([][]byte{testJPEG(t, 50)}))
	h.flush()
	if _, err := h.db.Exec(`UPDATE reqlog_blobs SET last_ts = 0`); err != nil {
		t.Fatal(err)
	}
	var collected int
	if err := h.logs.engine.do(context.Background(), func() {
		for hash := range h.logs.engine.blobs.known {
			h.logs.engine.blobs.touched[hash] = h.clock.now().UnixMilli()
		}
		collected, _ = h.logs.engine.blobs.gc(context.Background())
		clear(h.logs.engine.blobs.touched)
	}); err != nil {
		t.Fatal(err)
	}
	if collected != 0 || blobCount(t, h) != 1 {
		t.Fatalf("gc collected %d blobs while a last_ts update was pending", collected)
	}
}

// TestMaxBytesStopsWhenGCIsPinned leaves the oldest record in a segment that
// cannot seal and checks that size retention keeps the sealed history
// instead of deleting it for blob bytes it cannot free.
func TestMaxBytesStopsWhenGCIsPinned(t *testing.T) {
	h := newHarness(t, Config{MaxDays: -1, MaxBytes: 1})
	now := h.clock.now()
	logSimple(t, h.logs.logger, "stuck", "/v1/messages", "stuck", 200, now.Add(-2*time.Hour), imageTurn([][]byte{testJPEG(t, 60)}))
	h.flush()
	if err := h.logs.engine.do(context.Background(), h.logs.engine.sealAll); err != nil {
		t.Fatal(err)
	}
	// Leave "stuck" in the sealing state, as a compaction failing on a full
	// disk would.
	h.logs.engine.sealMu.Lock()
	h.logs.engine.sealQueue = nil
	h.logs.engine.sealMu.Unlock()
	for i := 0; i < 3; i++ {
		logSimple(t, h.logs.logger, fmt.Sprintf("kept-%d", i), "/v1/messages", fmt.Sprintf("k-%d", i), 200, now.Add(time.Duration(-60+i)*time.Minute), []byte(`{"n":"`+strings.Repeat("words ", 500)+`"}`))
	}
	h.flush()
	h.sealAll()
	h.logs.engine.applyRetention(context.Background())
	for i := 0; i < 3; i++ {
		if _, err := h.logs.Get(context.Background(), fmt.Sprintf("kept-%d", i), true); err != nil {
			t.Fatalf("kept-%d deleted while blob GC was pinned: %v", i, err)
		}
	}
}

// TestFailedBlobSyncIsStoredAgain fails the fsync after a record's blob was
// written and checks that the next record using it stores it again, so no
// committed row points at bytes that may never have reached the disk.
func TestFailedBlobSyncIsStoredAgain(t *testing.T) {
	h := newHarness(t, Config{})
	ts := h.clock.now()
	img := testJPEG(t, 71)
	var failedPack int64
	if err := h.logs.engine.do(context.Background(), func() {
		h.logs.engine.blobs.syncFile = func(f *os.File) error {
			failedPack = h.logs.engine.blobs.ingestID
			return errors.New("injected EIO")
		}
	}); err != nil {
		t.Fatal(err)
	}
	logSimple(t, h.logs.logger, "lost", "/v1/messages", "s", 200, ts, imageTurn([][]byte{img}))
	h.flush()
	if got := h.logs.engine.stats.writeErrors.Load(); got != 1 || failedPack == 0 {
		t.Fatalf("write errors %d, failed pack %d; want 1 and a pack", got, failedPack)
	}
	if err := h.logs.engine.do(context.Background(), func() { h.logs.engine.blobs.syncFile = (*os.File).Sync }); err != nil {
		t.Fatal(err)
	}
	logSimple(t, h.logs.logger, "after", "/v1/messages", "s", 200, ts, imageTurn([][]byte{img}))
	h.flush()
	var pack int64
	if err := h.db.QueryRow(`SELECT pack_id FROM reqlog_blobs`).Scan(&pack); err != nil {
		t.Fatal(err)
	}
	if pack == failedPack {
		t.Fatalf("blob row points into pack %d, whose sync failed", pack)
	}
	h.stop()
	h.open(Config{})
	h.logs.engine.blobs.cache = newBlobCache(blobCacheMax)
	d, err := h.logs.Get(context.Background(), "after", true)
	if err != nil || d.Request.Body != string(imageTurn([][]byte{img})) {
		t.Fatalf("record after a failed blob sync: %v", err)
	}
}

// TestPackOfSmallLiveBlobsIsNotRewritten checks that entry headers count as
// live, so a pack of tiny encoded blobs with nothing dead stays put.
func TestPackOfSmallLiveBlobsIsNotRewritten(t *testing.T) {
	h := newHarness(t, Config{})
	var blobs [][]byte
	for i := 0; i < 20; i++ {
		b := make([]byte, 8192)
		b[0] = byte(i)
		blobs = append(blobs, b)
	}
	logSimple(t, h.logs.logger, "small", "/v1/messages", "s", 200, h.clock.now(), imageTurn(blobs))
	h.flush()
	if err := h.logs.engine.do(context.Background(), func() {
		h.logs.engine.blobs.ingestOpened = time.Time{}
		h.logs.engine.rollBlobs()
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.logs.engine.blobs.packFull(context.Background()); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := h.db.QueryRow(`SELECT id FROM reqlog_packs`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := h.logs.engine.blobs.compactPacked(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(`SELECT id FROM reqlog_packs`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("fully live pack %d was rewritten as %d", before, after)
	}
}
