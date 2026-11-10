package codec

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/adler32"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// photo returns a w x h image with smooth gradients and mild noise, which
// compresses like a screenshot or photo rather than like flat color.
func photo(w, h int, seed uint64) *image.RGBA {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			n := uint8(r.IntN(24))
			img.Set(x, y, color.RGBA{uint8(x*255/w) + n, uint8(y*255/h) + n, uint8((x^y)&0xff) / 2, 255})
		}
	}
	// A few flat UI-like panels.
	for i := range 6 {
		x0, y0 := r.IntN(w/2), r.IntN(h/2)
		c := color.RGBA{uint8(40 * i), 200, 180, 255}
		for y := y0; y < y0+h/5; y++ {
			for x := x0; x < x0+w/4; x++ {
				img.Set(x, y, c)
			}
		}
	}
	return img
}

func encodeJPEG(t testing.TB, img image.Image, quality int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodePNG(t testing.TB, img image.Image, level png.CompressionLevel) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: level}).Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func chunk(typ string, data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, typ...)
	out = append(out, data...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(typ), data...)))
}

// rechunkPNG rewrites b's IDAT stream into IDAT chunks of the given sizes,
// cycling through them, and inserts extra chunks right after IHDR.
func rechunkPNG(t testing.TB, b []byte, sizes []int, extra ...[]byte) []byte {
	t.Helper()
	chunks, end, idat, err := splitPNG(b)
	if err != nil {
		t.Fatal(err)
	}
	out := slices.Clone(pngMagic)
	wroteIDAT := false
	for _, c := range chunks {
		raw := b[c.off : c.off+12+c.n]
		switch {
		case string(raw[4:8]) == "IHDR":
			out = append(out, raw...)
			for _, e := range extra {
				out = append(out, e...)
			}
		case c.idat:
			if wroteIDAT {
				continue
			}
			wroteIDAT = true
			for i, rest := 0, idat; len(rest) > 0; i++ {
				n := min(sizes[i%len(sizes)], len(rest))
				out = append(out, chunk("IDAT", rest[:n])...)
				rest = rest[n:]
			}
		default:
			out = append(out, raw...)
		}
	}
	return append(out, b[end:]...)
}

func countIDAT(t testing.TB, b []byte) int {
	t.Helper()
	chunks, _, err := pngChunks(b)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range chunks {
		if c.idat {
			n++
		}
	}
	return n
}

// packed is Pack without a deadline or start hook, which never fails.
func packed(data []byte) (Kind, []byte) {
	kind, stored, _ := Pack(context.Background(), data, nil)
	return kind, stored
}

func roundTrip(t testing.TB, data []byte) Kind {
	t.Helper()
	kind, stored := packed(data)
	got, err := Unpack(context.Background(), kind, stored, len(data))
	if err != nil {
		t.Fatalf("Unpack(kind %d): %v", kind, err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("kind %d round trip differs", kind)
	}
	if kind != Raw && !worthKeeping(stored, data) {
		t.Fatalf("kind %d kept %d of %d bytes", kind, len(stored), len(data))
	}
	return kind
}

func randomBytes(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, 1))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func TestPackGeneric(t *testing.T) {
	text := []byte(strings.Repeat(`{"role":"user","content":"hello there, taproot"}`+"\n", 400))
	cases := []struct {
		name string
		data []byte
		want Kind
	}{
		{"nil", nil, Raw},
		{"empty", []byte{}, Raw},
		{"one byte", []byte{0xFF}, Raw},
		{"jpeg magic only", []byte{0xFF, 0xD8, 0xFF}, Raw},
		{"png magic only", slices.Clone(pngMagic), Raw},
		{"signature-like random", randomBytes(256, 1), Raw},
		{"random 64k", randomBytes(64<<10, 2), Raw},
		{"compressible under 4k", text[:minZstdLen-1], Raw},
		{"compressible text", text, Zstd},
		{"jpeg-prefixed text", append([]byte{0xFF, 0xD8, 0xFF}, text...), Zstd},
		{"png-prefixed text", append(slices.Clone(pngMagic), text...), Zstd},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := roundTrip(t, tc.data); got != tc.want {
				t.Fatalf("kind = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestJPEG(t *testing.T) {
	progressive := readFixture(t, "progressive.jpg")
	baseline := encodeJPEG(t, photo(384, 256, 1), 88)
	cases := []struct {
		name string
		data []byte
	}{
		{"baseline", baseline},
		{"progressive", progressive},
		{"gray", encodeJPEG(t, image.NewGray(image.Rect(0, 0, 200, 120)), 75)},
		{"trailing bytes", append(slices.Clone(baseline), "trailer"...)},
		{"truncated", baseline[:len(baseline)/2]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind := roundTrip(t, tc.data)
			if !Native() && kind == Lepton {
				t.Fatal("stub build returned Lepton")
			}
			if Native() && tc.name == "baseline" && kind != Lepton {
				t.Fatalf("kind = %d, want Lepton", kind)
			}
			if !Native() {
				return
			}
			// Exercise Lepton directly even where Pack may prefer Raw.
			stored, err := leptonEncode(tc.data)
			if err != nil {
				t.Logf("lepton rejects %s: %v", tc.name, err)
				return
			}
			got, err := Unpack(context.Background(), Lepton, stored, len(tc.data))
			if err != nil || !bytes.Equal(got, tc.data) {
				t.Fatalf("lepton round trip: err=%v equal=%v", err, bytes.Equal(got, tc.data))
			}
			t.Logf("%s: %d -> %d bytes", tc.name, len(tc.data), len(stored))
		})
	}
}

func TestPNG(t *testing.T) {
	zl := readFixture(t, "zlib.png")
	img := photo(512, 384, 2)
	goDefault := encodePNG(t, img, png.DefaultCompression)
	if n := countIDAT(t, goDefault); n < 2 {
		t.Fatalf("Go fixture has %d IDAT chunks, want several", n)
	}
	manyIDAT := rechunkPNG(t, zl, []int{1, 1, 1, 3, 1000})
	if n := countIDAT(t, manyIDAT); n < 8 {
		t.Fatalf("rechunked fixture has %d IDAT chunks", n)
	}
	text := chunk("tEXt", []byte("Comment\x00made by a test"))
	// want is the kind a native build must choose; anyKind skips the check.
	const anyKind Kind = 255
	cases := []struct {
		name string
		data []byte
		want Kind
	}{
		{"zlib", zl, PNG},
		{"zlib with many IDAT chunks", manyIDAT, PNG},
		{"zlib with empty IDAT chunks", rechunkPNG(t, zl, []int{0, 2, 0, 3000}), PNG},
		{"zlib with extra chunks", rechunkPNG(t, zl, []int{1 << 20}, text), PNG},
		{"zlib with bytes after IEND", append(slices.Clone(zl), "after IEND"...), PNG},
		{"zlib missing IEND", zl[:len(zl)-12], anyKind},
		{"zlib corrupt deflate", corruptAt(zl, len(zl)/2), anyKind},
		{"zlib truncated", zl[:len(zl)/2], anyKind},
		// preflate cannot model Go's compress/flate matcher, so these fall back.
		{"go default", goDefault, anyKind},
		{"go best speed", encodePNG(t, img, png.BestSpeed), anyKind},
		{"go stored blocks", encodePNG(t, img, png.NoCompression), PNG},
		{"sub-filtered stored blocks", subFilteredPNG(300, 200), PNG},
		{"go tiny 1x1", encodePNG(t, image.NewGray(image.Rect(0, 0, 1, 1)), png.DefaultCompression), anyKind},
	}
	layouts := map[pixelLayout]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind := roundTrip(t, tc.data)
			if !Native() && kind == PNG {
				t.Fatal("stub build returned PNG")
			}
			if Native() && tc.want != anyKind && kind != tc.want {
				t.Fatalf("kind = %d, want %d", kind, tc.want)
			}
			if kind == PNG {
				_, stored := packed(tc.data)
				layouts[pixelLayout(stored[1])] = true
			}
		})
	}
	if Native() && (!layouts[layoutFiltered] || !layouts[layoutUnfiltered]) {
		t.Fatalf("fixtures exercised layouts %v, want both", layouts)
	}
}

func corruptAt(b []byte, i int) []byte {
	b = slices.Clone(b)
	for j := i; j < i+64 && j < len(b); j++ {
		b[j] ^= 0x5A
	}
	return b
}

// TestPNGSkeleton checks the container without preflate: removing the deflate
// bytes and refilling them must rebuild every file exactly.
func TestPNGSkeleton(t *testing.T) {
	def := encodePNG(t, photo(320, 200, 3), png.DefaultCompression)
	for _, b := range [][]byte{
		def,
		rechunkPNG(t, def, []int{1, 1, 1, 1, 1, 1, 7}),
		rechunkPNG(t, def, []int{0, 3, 0, 5000}, chunk("tEXt", []byte("k\x00v"))),
		append(slices.Clone(def), 1, 2, 3),
	} {
		chunks, end, idat, err := splitPNG(b)
		if err != nil {
			t.Fatal(err)
		}
		// Go's encoder writes one zlib stream: header, deflate, Adler-32.
		deflateLen := len(idat) - zlibHeaderLen - 4
		skeleton := pngSkeleton(b, chunks, end, deflateLen)
		if len(skeleton) != len(b)-deflateLen {
			t.Fatalf("skeleton is %d bytes, want %d", len(skeleton), len(b)-deflateLen)
		}
		got, err := rebuildPNG(skeleton, idat[zlibHeaderLen:zlibHeaderLen+deflateLen], len(b))
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("rebuild: err=%v equal=%v", err, bytes.Equal(got, b))
		}
		// Bytes after IEND are opaque, so only cuts before them are detectable.
		for n := 0; n < len(skeleton)-(len(b)-end); n += 1 + n/64 {
			if _, err := rebuildPNG(skeleton[:n], idat[2:2+deflateLen], len(b)-len(skeleton)+n); err == nil {
				t.Fatalf("truncated skeleton at %d rebuilt without error", n)
			}
		}
	}
}

// packedFixtures returns one packed blob per kind available in this build.
func packedFixtures(t testing.TB) map[Kind][]byte {
	t.Helper()
	text := []byte(strings.Repeat("request log line with some repetition\n", 300))
	inputs := [][]byte{
		randomBytes(100, 3),
		text,
		encodeJPEG(t, photo(256, 192, 4), 85),
		readFixture(t, "zlib.png"),
	}
	out := map[Kind][]byte{}
	for _, in := range inputs {
		k, _ := packed(in)
		out[k] = in
	}
	return out
}

func TestUnpackCorrupt(t *testing.T) {
	for kind, data := range packedFixtures(t) {
		if kind == Raw {
			continue
		}
		_, stored := packed(data)
		step := max(1, len(stored)/300)
		for n := 0; n < len(stored); n += step {
			if got, err := Unpack(context.Background(), kind, stored[:n], len(data)); err == nil && !bytes.Equal(got, data) {
				t.Fatalf("kind %d truncated to %d decoded wrong bytes without error", kind, n)
			}
		}
		r := rand.New(rand.NewPCG(uint64(kind), 7))
		wrong := 0
		for range 300 {
			c := slices.Clone(stored)
			c[r.IntN(len(c))] ^= byte(1 + r.IntN(255))
			if got, err := Unpack(context.Background(), kind, c, len(data)); err == nil && !bytes.Equal(got, data) {
				wrong++
			}
		}
		t.Logf("kind %d: %d of 300 single-byte corruptions decoded to different bytes without error", kind, wrong)
		if kind == Zstd || kind == PNG {
			// The zstd frame checksum covers these; a silent mismatch is a bug.
			if wrong != 0 {
				t.Fatalf("kind %d: %d corruptions went undetected", kind, wrong)
			}
		}
		if _, err := Unpack(context.Background(), kind, stored, len(data)+1); err == nil {
			t.Fatalf("kind %d accepted a wrong raw length", kind)
		}
	}
	if _, err := Unpack(context.Background(), Raw, []byte("abc"), 2); err == nil {
		t.Fatal("Raw accepted a wrong raw length")
	}
	if _, err := Unpack(context.Background(), Raw, nil, -1); err == nil {
		t.Fatal("negative raw length accepted")
	}
	if _, err := Unpack(context.Background(), Kind(9), []byte("abc"), 3); err == nil {
		t.Fatal("unknown kind accepted")
	}
	for _, k := range []Kind{Lepton, PNG} {
		for _, junk := range [][]byte{nil, {pngVersion}, {pngVersion, 0x80}, randomBytes(64, 4)} {
			_, err := Unpack(context.Background(), k, junk, 100)
			if err == nil {
				t.Fatalf("kind %d accepted junk %x", k, junk)
			}
			if !Native() && !errors.Is(err, ErrUnavailable) {
				t.Fatalf("stub kind %d error %v, want ErrUnavailable", k, err)
			}
		}
	}
}

func TestConcurrent(t *testing.T) {
	fixtures := packedFixtures(t)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for range 16 {
		wg.Go(func() {
			for range 4 {
				for _, data := range fixtures {
					kind, stored := packed(data)
					got, err := Unpack(context.Background(), kind, stored, len(data))
					if err != nil || !bytes.Equal(got, data) {
						errs <- errors.New("concurrent round trip failed")
						return
					}
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestSamples round-trips every file in $TAPROOT_CODEC_SAMPLES and reports
// sizes and Unpack time per file extension.
func TestSamples(t *testing.T) {
	dir := os.Getenv("TAPROOT_CODEC_SAMPLES")
	if dir == "" {
		t.Skip("TAPROOT_CODEC_SAMPLES not set")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	type stat struct {
		files, in, out int
		kinds          map[Kind]int
		unfiltered     int
		unpack         []time.Duration
		pack           time.Duration
	}
	stats := map[string]*stat{}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		ext := filepath.Ext(e.Name())
		s := stats[ext]
		if s == nil {
			s = &stat{kinds: map[Kind]int{}}
			stats[ext] = s
		}
		start := time.Now()
		kind, stored := packed(data)
		s.pack += time.Since(start)
		start = time.Now()
		got, err := Unpack(context.Background(), kind, stored, len(data))
		s.unpack = append(s.unpack, time.Since(start))
		if err != nil || !bytes.Equal(got, data) {
			t.Errorf("%s: kind %d round trip failed: %v", e.Name(), kind, err)
		}
		s.files++
		s.in += len(data)
		s.out += len(stored)
		s.kinds[kind]++
		if kind == PNG && pixelLayout(stored[1]) == layoutUnfiltered {
			s.unfiltered++
		}
	}
	for ext, s := range stats {
		slices.Sort(s.unpack)
		var total time.Duration
		for _, d := range s.unpack {
			total += d
		}
		t.Logf("%s: %d files, %d -> %d bytes (%.1f%% smaller), kinds %v (%d PNG unfiltered), pack total %v, unpack mean %v median %v max %v",
			ext, s.files, s.in, s.out, 100*(1-float64(s.out)/float64(s.in)), s.kinds, s.unfiltered,
			s.pack.Round(time.Millisecond), (total / time.Duration(len(s.unpack))).Round(time.Microsecond),
			s.unpack[len(s.unpack)/2].Round(time.Microsecond), s.unpack[len(s.unpack)-1].Round(time.Microsecond))
	}
}

func BenchmarkUnpack(b *testing.B) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"jpeg-1280x800", encodeJPEG(b, photo(1280, 800, 6), 85)},
		{"png-240x160-zlib", readFixture(b, "zlib.png")},
	} {
		kind, stored := packed(tc.data)
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.data)))
			for b.Loop() {
				if _, err := Unpack(context.Background(), kind, stored, len(tc.data)); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(stored))/float64(len(tc.data)), "ratio")
			b.ReportMetric(float64(kind), "kind")
		})
	}
}

func FuzzRebuildPNG(f *testing.F) {
	def := encodePNG(f, photo(64, 48, 8), png.DefaultCompression)
	chunks, end, idat, err := splitPNG(def)
	if err != nil {
		f.Fatal(err)
	}
	deflateLen := len(idat) - zlibHeaderLen - 4
	f.Add(pngSkeleton(def, chunks, end, deflateLen), idat[2:2+deflateLen])
	f.Fuzz(func(t *testing.T, skeleton, deflate []byte) {
		got, err := rebuildPNG(skeleton, deflate, len(skeleton)+len(deflate))
		if err == nil && len(got) != len(skeleton)+len(deflate) {
			t.Fatalf("rebuilt %d bytes", len(got))
		}
	})
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// subFilteredPNG returns an RGB gradient PNG whose rows all use the Sub
// filter, deflated as stored blocks. Filtered, every row is the same bytes;
// unfiltered, no two rows match, so Pack must keep layoutFiltered.
func subFilteredPNG(w, h int) []byte {
	stride := 3 * w
	plain := make([]byte, 0, h*(stride+1))
	for y := range h {
		plain = append(plain, 1)
		for x := range w {
			// Sub stores each byte minus its left neighbour: 3, 5, 7 after x=0.
			if x == 0 {
				plain = append(plain, byte(y), byte(2*y), byte(3*y))
			} else {
				plain = append(plain, 3, 5, 7)
			}
		}
	}
	z := []byte{0x78, 0x01}
	for rest := plain; ; {
		n := min(len(rest), 65535)
		final := byte(0)
		if n == len(rest) {
			final = 1
		}
		z = append(z, final, byte(n), byte(n>>8), ^byte(n), ^byte(n>>8))
		z = append(z, rest[:n]...)
		rest = rest[n:]
		if final == 1 {
			break
		}
	}
	z = binary.BigEndian.AppendUint32(z, adler32.Checksum(plain))
	ihdr := binary.BigEndian.AppendUint32(nil, uint32(w))
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(h))
	ihdr = append(ihdr, 8, 2, 0, 0, 0)
	out := slices.Clone(pngMagic)
	out = append(out, chunk("IHDR", ihdr)...)
	out = append(out, chunk("IDAT", z)...)
	return append(out, chunk("IEND", nil)...)
}
