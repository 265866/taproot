package codec

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"slices"
	"sync"
	"testing"
	"time"
)

// sofJPEG returns SOI, an APP0 segment, and a baseline SOF for a w x h frame
// with the given sampling factors per component, then SOS.
func sofJPEG(w, h int, sampling ...byte) []byte {
	b := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 6, 'J', 'F', 'I', 'F'}
	sof := []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), byte(len(sampling))}
	for i, hv := range sampling {
		sof = append(sof, byte(i+1), hv, 0)
	}
	b = append(b, 0xFF, 0xC0)
	b = binary.BigEndian.AppendUint16(b, uint16(2+len(sof)))
	b = append(b, sof...)
	return append(b, 0xFF, 0xDA, 0, 2)
}

func TestJPEGCoeffBytes(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want int64
	}{
		{"16384 square 4:2:0", sofJPEG(16384, 16384, 0x22, 0x11, 0x11), (2048*2048 + 2*1024*1024) * 128},
		{"phone photo 4:2:0", sofJPEG(4032, 3024, 0x22, 0x11, 0x11), 252 * 189 * 6 * 128},
		{"gray, partial blocks", sofJPEG(100, 100, 0x11), 13 * 13 * 128},
		{"4:4:4", sofJPEG(9, 9, 0x11, 0x11, 0x11), 2 * 2 * 3 * 128},
	}
	for _, tc := range cases {
		got, ok := jpegCoeffBytes(tc.data[2:])
		if !ok || got != tc.want {
			t.Errorf("%s: got %d, %v; want %d", tc.name, got, ok, tc.want)
		}
	}
	for name, data := range map[string][]byte{
		"no SOF before SOS": {0xFF, 0xD8, 0xFF, 0xDA, 0, 2},
		"zero height":       sofJPEG(64, 0, 0x11),
		"bad sampling":      sofJPEG(64, 64, 0x51),
		"truncated":         sofJPEG(64, 64, 0x22, 0x11, 0x11)[:14],
	} {
		if got, ok := jpegCoeffBytes(data[2:]); ok {
			t.Errorf("%s: got %d, want no estimate", name, got)
		}
	}
}

func TestNativePlanSkipsOversizedImages(t *testing.T) {
	if !Native() {
		t.Skip("native codecs not compiled in")
	}
	if k, _ := nativePlan(encodeJPEG(t, photo(384, 256, 1), 88)); k != Lepton {
		t.Fatalf("small JPEG planned as kind %d, want Lepton", k)
	}
	// 805 MB of coefficients, the size that OOM-killed a server.
	if k, cost := nativePlan(sofJPEG(16384, 16384, 0x22, 0x11, 0x11)); k != Raw {
		t.Fatalf("16384 x 16384 JPEG planned as kind %d at %d bytes, want Raw", k, cost)
	}
	small := readFixture(t, "zlib.png")
	if k, _ := nativePlan(small); k != PNG {
		t.Fatalf("small PNG planned as kind %d, want PNG", k)
	}
	huge := slices.Clone(small)
	binary.BigEndian.PutUint32(huge[16:], 8192)
	binary.BigEndian.PutUint32(huge[20:], 8192)
	if k, cost := nativePlan(huge); k != Raw {
		t.Fatalf("PNG with an 8192 x 8192 IHDR planned as kind %d at %d bytes, want Raw", k, cost)
	}
	// Pack must still store the oversized image, just without its native codec.
	if kind := roundTrip(t, huge); kind == PNG {
		t.Fatal("Pack used preflate on a PNG over the memory cap")
	}
}

// TestLeptonHeaderSizesFrame checks that Unpack charges a Lepton blob what
// Pack charged its JPEG, by reading the JPEG header Lepton stores.
func TestLeptonHeaderSizesFrame(t *testing.T) {
	if !Native() {
		t.Skip("native codecs not compiled in")
	}
	for name, data := range map[string][]byte{
		"baseline":    encodeJPEG(t, photo(384, 256, 1), 88),
		"progressive": readFixture(t, "progressive.jpg"),
		"gray":        encodeJPEG(t, image.NewGray(image.Rect(0, 0, 200, 120)), 75),
	} {
		stored, err := leptonEncode(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, ok := jpegCoeffBytes(data[2:])
		got, ok2 := leptonCoeffBytes(stored)
		if !ok || !ok2 || got != want {
			t.Fatalf("%s: lepton header gives %d (%v), JPEG gives %d (%v)", name, got, ok2, want, ok)
		}
		if c := unpackCost(Lepton, stored, len(data)); c != leptonCost(want, len(data)) {
			t.Fatalf("%s: unpack cost %d, want %d", name, c, leptonCost(want, len(data)))
		}
	}
	if c := unpackCost(Lepton, []byte("not lepton"), 10); c != nativeBudget {
		t.Fatalf("unreadable Lepton blob costs %d, want the whole budget", c)
	}
}

// inflatedIDAT returns the length of b's inflated IDAT stream.
func inflatedIDAT(t *testing.T, b []byte) int64 {
	t.Helper()
	chunks, _, err := pngChunks(b)
	if err != nil {
		t.Fatal(err)
	}
	var idat []byte
	for _, c := range chunks {
		if c.idat {
			idat = append(idat, b[c.off+8:c.off+8+c.n]...)
		}
	}
	zr, err := zlib.NewReader(bytes.NewReader(idat))
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, zr)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPNGPlainLen(t *testing.T) {
	r := image.Rect(0, 0, 37, 21)
	pal := image.NewPaletted(r, color.Palette{color.Black, color.White, color.Gray{128}})
	for name, img := range map[string]image.Image{
		"gray":      image.NewGray(r),
		"gray16":    image.NewGray16(r),
		"rgba":      photo(37, 21, 2),
		"nrgba64":   image.NewNRGBA64(r),
		"paletted2": pal,
	} {
		b := encodePNG(t, img, png.DefaultCompression)
		got, ok := pngPlainLen(b)
		if want := inflatedIDAT(t, b); !ok || got != want {
			t.Errorf("%s: got %d (%v), want %d", name, got, ok, want)
		}
	}

	// Interlaced: image/png accepts exactly the length every Adam7 pass needs.
	ihdr := binary.BigEndian.AppendUint32(nil, 37)
	ihdr = binary.BigEndian.AppendUint32(ihdr, 21)
	ihdr = append(ihdr, 8, 2, 0, 0, 1)
	interlaced := func(plain int64) []byte {
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		_, _ = zw.Write(make([]byte, plain))
		_ = zw.Close()
		out := slices.Clone(pngMagic)
		out = append(out, chunk("IHDR", ihdr)...)
		out = append(out, chunk("IDAT", z.Bytes())...)
		return append(out, chunk("IEND", nil)...)
	}
	n, ok := pngPlainLen(interlaced(0))
	if !ok {
		t.Fatal("interlaced IHDR rejected")
	}
	if _, err := png.Decode(bytes.NewReader(interlaced(n))); err != nil {
		t.Fatalf("interlaced PNG of %d plaintext bytes: %v", n, err)
	}
	if _, err := png.Decode(bytes.NewReader(interlaced(n - 1))); err == nil {
		t.Fatalf("interlaced PNG of %d plaintext bytes decoded, so %d is not the exact length", n-1, n)
	}
}

func TestReserveClampsToBudget(t *testing.T) {
	release, err := reserve(context.Background(), 4*nativeBudget)
	if err != nil {
		t.Fatal(err)
	}
	if nativeWork.TryAcquire(1) {
		t.Fatal("budget not fully held by an oversized reservation")
	}
	release()
	if !nativeWork.TryAcquire(nativeBudget) {
		t.Fatal("budget not returned")
	}
	nativeWork.Release(nativeBudget)
}

// holdPacks takes all of packWork, as running packs would, and queues more
// packs behind it. It returns once the holder releases everything.
func holdPacks(t *testing.T) {
	t.Helper()
	release, err := reservePack(context.Background(), packBudget)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var queued sync.WaitGroup
	for range 8 {
		queued.Go(func() {
			if _, _, err := Pack(ctx, make([]byte, 6<<20), nil); err == nil {
				t.Error("queued pack ran while the compactor's budget was held")
			}
		})
	}
	t.Cleanup(func() {
		cancel()
		queued.Wait()
		release()
	})
}

// TestReadsNotStarvedByPacks holds the compactor's whole share, with more
// packs waiting, and checks that a read still gets the rest of the budget
// rather than queuing behind them.
func TestReadsNotStarvedByPacks(t *testing.T) {
	var stored []byte
	jpeg := encodeJPEG(t, photo(1024, 768, 3), 88)
	if Native() {
		var err error
		if stored, err = leptonEncode(jpeg); err != nil {
			t.Fatal(err)
		}
	}
	holdPacks(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release, err := reserve(ctx, 64<<20) // about a 12 MP photo's Lepton decode
	if err != nil {
		t.Fatalf("read reservation waited behind packs: %v", err)
	}
	release()
	if stored != nil {
		got, err := Unpack(ctx, Lepton, stored, len(jpeg))
		if err != nil || !bytes.Equal(got, jpeg) {
			t.Fatalf("Lepton read while packs hold their budget: %v", err)
		}
	}
}

// TestPackStopsWhenContextEnds checks that a pack waiting for the budget
// returns when its context ends, without encoding or calling start.
func TestPackStopsWhenContextEnds(t *testing.T) {
	release, err := reservePack(context.Background(), packBudget)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	started := false
	done := make(chan error, 1)
	go func() {
		_, _, err := Pack(ctx, make([]byte, 64<<10), func() error {
			started = true
			return nil
		})
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Pack returned %v, want context.Canceled", err)
	}
	if started {
		t.Fatal("Pack called start without holding the budget")
	}
}

func TestPackReturnsStartError(t *testing.T) {
	errStart := errors.New("mark failed")
	kind, stored, err := Pack(context.Background(), make([]byte, 64<<10), func() error { return errStart })
	if !errors.Is(err, errStart) || kind != Raw || stored != nil {
		t.Fatalf("Pack = %d, %d bytes, %v; want Raw, nil, the start error", kind, len(stored), err)
	}
}
