package codec

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"io"

	"golang.org/x/sync/semaphore"
)

// Native codecs allocate working sets far larger than their input: Lepton a
// coefficient buffer of 128 bytes per 8x8 block (805 MB for a 4 MB flat
// 16384 x 16384 JPEG), preflate about 6 bytes per plaintext byte, and
// level-19 zstd up to about 100 MB of match state. Callers choose the input,
// so Pack estimates each blob's peak from its header, skips a codec whose
// estimate exceeds maxBlobCost, and holds the estimate in nativeWork while it
// runs, which bounds the sum across concurrent calls.
//
// Pack also holds its estimate in packWork, which is smaller than nativeWork,
// so the compactor never holds the whole budget and its queued packs wait on
// packWork rather than ahead of reads in nativeWork's FIFO: a record view
// decodes while a pack is running instead of after every queued one.
const (
	nativeBudget = 512 << 20
	packBudget   = 384 << 20
	maxBlobCost  = 256 << 20

	coeffBytesPerBlock = 128
	zstdStateCap       = 112 << 20
	preflatePerPlain   = 6
	// maxLeptonHeader bounds how much of a Lepton blob's JPEG header Unpack
	// inflates to find the frame size.
	maxLeptonHeader = 16 << 20
)

var (
	nativeWork = semaphore.NewWeighted(nativeBudget)
	packWork   = semaphore.NewWeighted(packBudget)
)

// reserve waits until cost bytes of nativeWork are free, or ctx is done, and
// returns their release. A cost over the budget takes the whole budget, so it
// runs alone.
func reserve(ctx context.Context, cost int64) (release func(), err error) {
	cost = min(cost, nativeBudget)
	if cost <= 0 {
		return func() {}, nil
	}
	if err := nativeWork.Acquire(ctx, cost); err != nil {
		return nil, err
	}
	return func() { nativeWork.Release(cost) }, nil
}

// reservePack is reserve for Pack, which first takes its share of packWork.
func reservePack(ctx context.Context, cost int64) (release func(), err error) {
	cost = min(cost, packBudget)
	if cost <= 0 {
		return func() {}, nil
	}
	if err := packWork.Acquire(ctx, cost); err != nil {
		return nil, err
	}
	releaseNative, err := reserve(ctx, cost)
	if err != nil {
		packWork.Release(cost)
		return nil, err
	}
	return func() {
		releaseNative()
		packWork.Release(cost)
	}, nil
}

func zstdCost(n int) int64 { return min(16*int64(n), zstdStateCap) + 2*int64(n) }

func leptonCost(coeff int64, n int) int64 { return coeff + 2*int64(n) }

func pngCost(plain int64) int64 { return preflatePerPlain*plain + zstdStateCap }

// nativePlan returns the native codec Pack tries on data, with its estimated
// peak memory, or Raw when none applies or the estimate exceeds maxBlobCost.
func nativePlan(data []byte) (Kind, int64) {
	if !Native() {
		return Raw, 0
	}
	switch {
	case bytes.HasPrefix(data, jpegMagic):
		if coeff, ok := jpegCoeffBytes(data[2:]); ok {
			if c := leptonCost(coeff, len(data)); c <= maxBlobCost {
				return Lepton, c
			}
		}
	case bytes.HasPrefix(data, pngMagic):
		if plain, ok := pngPlainLen(data); ok {
			if c := pngCost(plain); c <= maxBlobCost {
				return PNG, c
			}
		}
	}
	return Raw, 0
}

// unpackCost estimates the peak memory of decoding stored. A Lepton blob
// whose header cannot be read is charged the whole budget.
func unpackCost(kind Kind, stored []byte, rawLen int) int64 {
	switch kind {
	case Lepton:
		if coeff, ok := leptonCoeffBytes(stored); ok {
			return leptonCost(coeff, rawLen)
		}
		return nativeBudget
	case PNG:
		if len(stored) > 2 {
			if bodyLen, k := binary.Uvarint(stored[2:]); k > 0 {
				// Decoding needs no zstd match state, only preflate's.
				return preflatePerPlain * int64(min(bodyLen, nativeBudget))
			}
		}
		return nativeBudget
	}
	return 0
}

// jpegCoeffBytes returns the size of the DCT coefficient buffer that Lepton
// allocates for the frame described by segs, a JPEG's marker segments after
// SOI. It reports false when no usable SOF precedes the first SOS.
func jpegCoeffBytes(segs []byte) (int64, bool) {
	for pos := 0; pos+4 <= len(segs); {
		if segs[pos] != 0xFF {
			return 0, false
		}
		m := segs[pos+1]
		switch {
		case m == 0xFF: // fill byte
			pos++
			continue
		case m == 0x01 || (m >= 0xD0 && m <= 0xD8): // no length
			pos += 2
			continue
		case m == 0xDA || m == 0xD9:
			return 0, false
		}
		n := int(binary.BigEndian.Uint16(segs[pos+2:]))
		if n < 2 || pos+2+n > len(segs) {
			return 0, false
		}
		if m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC {
			return sofCoeffBytes(segs[pos+4 : pos+2+n])
		}
		pos += 2 + n
	}
	return 0, false
}

// sofCoeffBytes sizes the coefficient buffer the way lepton_jpeg does: every
// component gets mcuh*H by mcuv*V blocks.
func sofCoeffBytes(sof []byte) (int64, bool) {
	if len(sof) < 6 {
		return 0, false
	}
	height, width, comps := int64(binary.BigEndian.Uint16(sof[1:])), int64(binary.BigEndian.Uint16(sof[3:])), int(sof[5])
	if height == 0 || width == 0 || comps < 1 || comps > 4 || len(sof) < 6+3*comps {
		return 0, false
	}
	var hmax, vmax, units int64
	for i := range comps {
		hv := sof[6+3*i+1]
		h, v := int64(hv>>4), int64(hv&0x0F)
		if h < 1 || h > 4 || v < 1 || v > 4 {
			return 0, false
		}
		hmax, vmax, units = max(hmax, h), max(vmax, v), units+h*v
	}
	mcuh := (width + 8*hmax - 1) / (8 * hmax)
	mcuv := (height + 8*vmax - 1) / (8 * vmax)
	return mcuh * mcuv * units * coeffBytesPerBlock, true
}

// leptonCoeffBytes reads the JPEG header that a lepton_jpeg file keeps
// zlib-compressed after its 28-byte fixed header ("HDR", a little-endian
// length, then the JPEG's marker segments after SOI) and sizes its frame.
func leptonCoeffBytes(stored []byte) (int64, bool) {
	const fixed = 28
	if len(stored) < fixed || stored[0] != 0xCF || stored[1] != 0x84 {
		return 0, false
	}
	size := uint64(binary.LittleEndian.Uint32(stored[24:]))
	if size > uint64(len(stored)-fixed) {
		return 0, false
	}
	zr, err := zlib.NewReader(bytes.NewReader(stored[fixed : fixed+int(size)]))
	if err != nil {
		return 0, false
	}
	defer func() { _ = zr.Close() }()
	var head [7]byte
	if _, err := io.ReadFull(zr, head[:]); err != nil || string(head[:3]) != "HDR" {
		return 0, false
	}
	n := min(int64(binary.LittleEndian.Uint32(head[3:])), maxLeptonHeader)
	segs, err := io.ReadAll(io.LimitReader(zr, n))
	if err != nil {
		return 0, false
	}
	return jpegCoeffBytes(segs)
}

// adam7 lists each interlace pass's x and y start and step.
var adam7 = [7][4]int64{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}}

// pngPlainLen returns the inflated IDAT length that b's IHDR implies: per
// scanline (of every interlace pass), a filter type byte and the packed row.
func pngPlainLen(b []byte) (int64, bool) {
	const ihdr = 8 + 8
	if len(b) < ihdr+13 || binary.BigEndian.Uint32(b[8:]) != 13 || string(b[12:16]) != "IHDR" {
		return 0, false
	}
	h := b[ihdr:]
	width, height := int64(binary.BigEndian.Uint32(h)), int64(binary.BigEndian.Uint32(h[4:]))
	depth, colorType, interlace := int64(h[8]), h[9], h[12]
	channels := map[byte]int64{0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[colorType]
	if channels == 0 || width == 0 || height == 0 || width > 1<<24 || height > 1<<24 ||
		(depth != 1 && depth != 2 && depth != 4 && depth != 8 && depth != 16) || interlace > 1 {
		return 0, false
	}
	bits := channels * depth
	scanlines := func(w, h int64) int64 { return h * ((w*bits+7)/8 + 1) }
	if interlace == 0 {
		return scanlines(width, height), true
	}
	var total int64
	for _, p := range adam7 {
		w, h := (width-p[0]+p[2]-1)/p[2], (height-p[1]+p[3]-1)/p[3]
		if w > 0 && h > 0 {
			total += scanlines(w, h)
		}
	}
	return total, true
}
