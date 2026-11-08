package codec

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// pixelLayout says how the body stores the inflated IDAT data. The values are
// persisted.
type pixelLayout byte

const (
	// layoutFiltered stores the inflated IDAT data as is: per row, a filter
	// type byte and the filtered row.
	layoutFiltered pixelLayout = 0
	// layoutUnfiltered stores every row's filter type byte first, then every
	// row with its filter undone. zstd often does better on screenshots this
	// way; reapplying the recorded filters restores the exact original data.
	layoutUnfiltered pixelLayout = 1
)

// geometry is the scanline shape of a non-interlaced PNG.
type geometry struct {
	rows   int
	stride int // bytes per row, without the filter type byte
	bpp    int // filter distance: bytes per complete pixel, at least 1
}

// readGeometry reads the IHDR chunk that must follow the signature. It reports
// false for interlaced or invalid headers, which keep layoutFiltered.
func readGeometry(b []byte) (geometry, bool) {
	const ihdr = 8 + 8 // signature, then the IHDR length and type
	if len(b) < ihdr+13 || binary.BigEndian.Uint32(b[8:]) != 13 || string(b[12:16]) != "IHDR" {
		return geometry{}, false
	}
	h := b[ihdr:]
	width, height := uint64(binary.BigEndian.Uint32(h)), uint64(binary.BigEndian.Uint32(h[4:]))
	depth, colorType, interlace := uint64(h[8]), h[9], h[12]
	channels := map[byte]uint64{0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[colorType]
	if channels == 0 || interlace != 0 || width == 0 || height == 0 ||
		(depth != 1 && depth != 2 && depth != 4 && depth != 8 && depth != 16) {
		return geometry{}, false
	}
	bits := channels * depth
	stride := (width*bits + 7) / 8
	if height*(stride+1) > maxPlain {
		return geometry{}, false
	}
	return geometry{rows: int(height), stride: int(stride), bpp: int(max(1, bits/8))}, true
}

func (g geometry) size() int { return g.rows * (g.stride + 1) }

// predict returns the PNG filter predictor for byte x of a row, where a, b,
// and c are the left, above, and upper-left unfiltered bytes.
func predict(filter, a, b, c byte) byte {
	switch filter {
	case 1:
		return a
	case 2:
		return b
	case 3:
		return byte((int(a) + int(b)) / 2)
	case 4:
		p := int(a) + int(b) - int(c)
		pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
		if pa <= pb && pa <= pc {
			return a
		}
		if pb <= pc {
			return b
		}
		return c
	default:
		return 0
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// unfilterRows converts filtered IDAT data to layoutUnfiltered. It reports
// false when the data does not match g or uses an unknown filter type.
func unfilterRows(plain []byte, g geometry) ([]byte, bool) {
	if len(plain) != g.size() {
		return nil, false
	}
	out := make([]byte, g.size())
	filters, pixels := out[:g.rows], out[g.rows:]
	prev := make([]byte, g.stride)
	for y := range g.rows {
		row := plain[y*(g.stride+1):][:g.stride+1]
		f := row[0]
		if f > 4 {
			return nil, false
		}
		filters[y] = f
		cur := pixels[y*g.stride:][:g.stride]
		for x, v := range row[1:] {
			var a, c byte
			if x >= g.bpp {
				a, c = cur[x-g.bpp], prev[x-g.bpp]
			}
			cur[x] = v + predict(f, a, prev[x], c)
		}
		prev = cur
	}
	return out, true
}

// refilterRows reverses unfilterRows.
func refilterRows(data []byte, g geometry) ([]byte, error) {
	if len(data) != g.size() {
		return nil, fmt.Errorf("codec: png unfiltered pixels are %d bytes, want %d", len(data), g.size())
	}
	filters, pixels := data[:g.rows], data[g.rows:]
	out := make([]byte, g.size())
	prev := make([]byte, g.stride)
	for y := range g.rows {
		f := filters[y]
		if f > 4 {
			return nil, fmt.Errorf("codec: png filter type %d invalid", f)
		}
		row := out[y*(g.stride+1):][:g.stride+1]
		row[0] = f
		cur := pixels[y*g.stride:][:g.stride]
		for x, v := range cur {
			var a, c byte
			if x >= g.bpp {
				a, c = cur[x-g.bpp], prev[x-g.bpp]
			}
			row[1+x] = v - predict(f, a, prev[x], c)
		}
		prev = cur
	}
	return out, nil
}

// trialEncoder is a fast zstd encoder used only to pick a pixel layout.
var trialEncoder = sync.OnceValue(func() *zstd.Encoder {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		panic(fmt.Sprintf("codec: zstd trial encoder: %v", err)) // only invalid options fail
	}
	return enc
})

// compressesBetter reports whether a beats b under a fast zstd trial. On 107
// real PNGs this came within 0.3% of compressing both layouts at the final
// level, for about 1 s of CPU in total instead of about 23 s.
func compressesBetter(a, b []byte) bool {
	enc := trialEncoder()
	return len(enc.EncodeAll(a, nil)) < len(enc.EncodeAll(b, nil))
}
