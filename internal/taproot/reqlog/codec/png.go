package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	pngVersion = 1
	// maxPlain bounds the inflated IDAT data Pack accepts, and so the memory
	// Unpack may need.
	maxPlain = 128 << 20
	// zlibHeaderLen is where the deflate stream starts in the IDAT stream;
	// PNG forbids the preset dictionary that would move it.
	zlibHeaderLen = 2
	// maxChunkLen is the PNG limit on a chunk's data length.
	maxChunkLen = 1<<31 - 1
)

var errPNGShort = errors.New("codec: png skeleton truncated")

// pngChunk is one chunk header at off: data starts at off+8 and its CRC at
// off+8+n in the original file.
type pngChunk struct {
	off  int
	n    int
	idat bool
	iend bool
}

// pngChunks parses b's chunks up to and including IEND and returns them with
// the offset just past IEND.
func pngChunks(b []byte) ([]pngChunk, int, error) {
	var chunks []pngChunk
	pos := len(pngMagic)
	for {
		if len(b)-pos < 12 {
			return nil, 0, errors.New("codec: png chunk truncated or IEND missing")
		}
		n := int(binary.BigEndian.Uint32(b[pos:]))
		if n > maxChunkLen || len(b)-pos-12 < n {
			return nil, 0, errors.New("codec: png chunk length out of range")
		}
		typ := string(b[pos+4 : pos+8])
		c := pngChunk{off: pos, n: n, idat: typ == "IDAT", iend: typ == "IEND"}
		chunks = append(chunks, c)
		pos += 12 + n
		if c.iend {
			return chunks, pos, nil
		}
	}
}

// packPNG encodes a PNG as described in the package comment.
func packPNG(b []byte) ([]byte, error) {
	chunks, end, idat, err := splitPNG(b)
	if err != nil {
		return nil, err
	}
	// The IHDR size bounds preflate's memory, which nativePlan charged.
	plainLen, ok := pngPlainLen(b)
	if !ok || plainLen > maxPlain {
		return nil, errors.New("codec: png IHDR unusable")
	}
	plain, corrections, deflateLen, err := deflateUnpack(idat[zlibHeaderLen:], int(plainLen))
	if err != nil {
		return nil, err
	}
	if deflateLen <= 0 {
		return nil, errors.New("codec: png deflate stream empty")
	}
	skeleton := pngSkeleton(b, chunks, end, deflateLen)

	layout, pixels := layoutFiltered, plain
	if g, ok := readGeometry(skeleton); ok {
		if u, ok := unfilterRows(plain, g); ok && compressesBetter(u, plain) {
			layout, pixels = layoutUnfiltered, u
		}
	}

	body := make([]byte, 0, 4*binary.MaxVarintLen64+len(skeleton)+len(corrections)+len(pixels))
	body = binary.AppendUvarint(body, uint64(len(skeleton)))
	body = binary.AppendUvarint(body, uint64(len(corrections)))
	body = binary.AppendUvarint(body, uint64(len(pixels)))
	body = binary.AppendUvarint(body, uint64(deflateLen))
	body = append(body, skeleton...)
	body = append(body, corrections...)
	body = append(body, pixels...)

	out := []byte{pngVersion, byte(layout)}
	out = binary.AppendUvarint(out, uint64(len(body)))
	frame := zstdEncode(body)
	if frame == nil {
		return nil, errors.New("codec: zstd encode failed")
	}
	return append(out, frame...), nil
}

// splitPNG parses a PNG and returns its chunks, the offset just past IEND,
// and the IDAT stream, whose zlib header it checks.
func splitPNG(b []byte) (chunks []pngChunk, end int, idat []byte, err error) {
	chunks, end, err = pngChunks(b)
	if err != nil {
		return nil, 0, nil, err
	}
	for _, c := range chunks {
		if c.idat {
			idat = append(idat, b[c.off+8:c.off+8+c.n]...)
		}
	}
	if len(idat) < zlibHeaderLen {
		return nil, 0, nil, errors.New("codec: png has no zlib stream")
	}
	cmf, flg := idat[0], idat[1]
	if cmf&0x0f != 8 || cmf>>4 > 7 || (uint16(cmf)<<8|uint16(flg))%31 != 0 || flg&0x20 != 0 {
		return nil, 0, nil, errors.New("codec: png zlib header unsupported")
	}
	return chunks, end, idat, nil
}

// pngSkeleton returns b without the deflateLen bytes that follow the zlib
// header in the IDAT stream.
func pngSkeleton(b []byte, chunks []pngChunk, end, deflateLen int) []byte {
	lo, hi := zlibHeaderLen, zlibHeaderLen+deflateLen
	skeleton := make([]byte, 0, len(b)-deflateLen)
	skeleton = append(skeleton, b[:len(pngMagic)]...)
	v := 0
	for _, c := range chunks {
		data := c.off + 8
		if !c.idat {
			skeleton = append(skeleton, b[c.off:data+c.n+4]...)
			continue
		}
		a, z := removedSpan(v, c.n, lo, hi)
		skeleton = append(skeleton, b[c.off:data+a]...)
		skeleton = append(skeleton, b[data+z:data+c.n+4]...)
		v += c.n
	}
	return append(skeleton, b[end:]...)
}

// removedSpan returns the part [a, z) of an IDAT payload, covering IDAT-stream
// offsets [v, v+n), that falls inside the removed range [lo, hi).
func removedSpan(v, n, lo, hi int) (a, z int) {
	a = min(max(lo-v, 0), n)
	z = min(max(hi-v, 0), n)
	return a, max(a, z)
}

// unpackPNG reverses packPNG. It bounds-checks every field, since stored
// bytes may be corrupt.
func unpackPNG(stored []byte, rawLen int) ([]byte, error) {
	if len(stored) < 2 || stored[0] != pngVersion {
		return nil, errors.New("codec: png version unsupported")
	}
	layout := pixelLayout(stored[1])
	if layout != layoutFiltered && layout != layoutUnfiltered {
		return nil, fmt.Errorf("codec: png pixel layout %d unknown", layout)
	}
	bodyLen, k := binary.Uvarint(stored[2:])
	if k <= 0 || bodyLen > uint64(2*rawLen+maxPlain+4*binary.MaxVarintLen64) {
		return nil, errors.New("codec: png body length invalid")
	}
	body, err := zstdDecode(stored[2+k:], int(bodyLen))
	if err != nil {
		return nil, err
	}

	// The section lengths are bounded by the body; deflateLen is only
	// compared with the rebuilt stream.
	var lens [4]uint64
	for i := range lens {
		v, k := binary.Uvarint(body)
		if k <= 0 || (i < 3 && v > uint64(len(body))) {
			return nil, errors.New("codec: png body header invalid")
		}
		lens[i], body = v, body[k:]
	}
	skelLen, corrLen, plainLen, deflateLen := lens[0], lens[1], lens[2], lens[3]
	if skelLen+corrLen+plainLen != uint64(len(body)) {
		return nil, errors.New("codec: png body sections do not add up")
	}
	skeleton := body[:skelLen]
	corrections := body[skelLen : skelLen+corrLen]
	plain := body[skelLen+corrLen:]
	if layout == layoutUnfiltered {
		g, ok := readGeometry(skeleton)
		if !ok {
			return nil, errors.New("codec: png IHDR unusable for unfiltered pixels")
		}
		if plain, err = refilterRows(plain, g); err != nil {
			return nil, err
		}
	}

	deflate, err := deflatePack(plain, corrections)
	if err != nil {
		return nil, err
	}
	if uint64(len(deflate)) != deflateLen {
		return nil, fmt.Errorf("codec: png deflate rebuilt %d bytes, want %d", len(deflate), deflateLen)
	}
	return rebuildPNG(skeleton, deflate, rawLen)
}

// rebuildPNG refills the skeleton's IDAT payloads with deflate.
func rebuildPNG(skeleton, deflate []byte, rawLen int) ([]byte, error) {
	if len(skeleton) < len(pngMagic) || len(skeleton)+len(deflate) != rawLen {
		return nil, errors.New("codec: png sizes do not match raw length")
	}
	out := make([]byte, 0, rawLen)
	out = append(out, skeleton[:len(pngMagic)]...)
	sk := skeleton[len(pngMagic):]
	take := func(n int) ([]byte, error) {
		if n < 0 || n > len(sk) {
			return nil, errPNGShort
		}
		p := sk[:n]
		sk = sk[n:]
		return p, nil
	}

	lo, hi := zlibHeaderLen, zlibHeaderLen+len(deflate)
	v := 0
	for {
		hdr, err := take(8)
		if err != nil {
			return nil, err
		}
		n := int(binary.BigEndian.Uint32(hdr))
		if n > maxChunkLen {
			return nil, errors.New("codec: png chunk length out of range")
		}
		typ := string(hdr[4:])
		out = append(out, hdr...)
		if typ == "IDAT" {
			a, z := removedSpan(v, n, lo, hi)
			head, err := take(a)
			if err != nil {
				return nil, err
			}
			tail, err := take(n - z)
			if err != nil {
				return nil, err
			}
			out = append(out, head...)
			if z > a {
				out = append(out, deflate[v+a-lo:v+z-lo]...)
			}
			out = append(out, tail...)
			v += n
		} else {
			data, err := take(n)
			if err != nil {
				return nil, err
			}
			out = append(out, data...)
		}
		crc, err := take(4)
		if err != nil {
			return nil, err
		}
		out = append(out, crc...)
		if typ == "IEND" {
			break
		}
	}
	if v < hi {
		return nil, errors.New("codec: png IDAT chunks shorter than the deflate stream")
	}
	return append(out, sk...), nil
}
