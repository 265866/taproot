// Package codec stores request-log blobs (decoded base64 payloads such as
// images and signatures) in the smallest encoding that rebuilds them byte for
// byte.
//
// Pack picks one Kind per blob and proves it by running Unpack and comparing
// before it returns anything but Raw, so a stored blob always rebuilds the
// exact original bytes:
//
//   - Raw stores the data itself.
//   - Zstd stores one zstd frame of the data, for data of at least 4 KiB.
//   - Lepton stores a Lepton-coded JPEG (lepton_jpeg).
//   - PNG stores a PNG with its IDAT deflate stream replaced by the preflate
//     plaintext and corrections that recreate it (preflate-rs).
//
// Lepton and PNG run in a Rust static library (native/) linked with cgo when
// the binary is built with the taproot_codecs tag. Without the tag, Pack
// never returns those kinds and Unpack returns ErrUnavailable for them.
//
// Native builds write zstd frames with the reference encoder (libzstd) at
// level 19, which on real PNG plaintexts comes out 17% smaller than
// klauspost's SpeedBestCompression; stub builds use klauspost. Frames are
// standard either way and always decode with klauspost.
//
// Stored Lepton and PNG bytes depend on the exact Rust crate versions, which
// native/Cargo.toml pins. preflate-rs changes its corrections format between
// minor versions (0.8 rejects 0.7 corrections), so upgrading it needs a new
// pngVersion whose decoder still reads old blobs, or a migration. Before
// upgrading lepton_jpeg, check that existing blobs still decode.
//
// # PNG format
//
// A PNG's IDAT payloads, concatenated in file order, form the "IDAT stream":
// a 2-byte zlib header, a raw deflate stream, a 4-byte Adler-32, and in rare
// files more bytes after it. Pack removes exactly the deflate bytes, the
// IDAT-stream range [2, 2+deflateLen), and keeps everything else of the file
// verbatim as the skeleton: the signature, every chunk's length, type, and
// CRC, every non-IDAT chunk, the zlib header, the Adler-32, and any bytes
// after IEND. Chunk lengths in the skeleton are the original ones, so a
// reader walking it knows how many bytes of each IDAT payload were removed,
// and rebuilding needs no checksum or length recomputation.
//
// Stored bytes:
//
//	version  byte     pngVersion (1)
//	layout   byte     pixelLayout of the plaintext section
//	bodyLen  uvarint  length of the decompressed body
//	frame    zstd frame of the body
//
// Body:
//
//	skelLen, corrLen, plainLen, deflateLen  uvarint each
//	skeleton     skelLen bytes
//	corrections  corrLen bytes, preflate's reconstruction data
//	plaintext    plainLen bytes, the inflated IDAT data in that layout
//
// For a non-interlaced PNG, Pack may store the plaintext unfiltered: every
// row's filter type byte, then every row with its filter undone, which zstd
// compresses better on most screenshots. It picks the layout with a fast
// zstd trial. Unpack reapplies the recorded filters, an exact inverse.
//
// Unpack recreates the deflate stream from plaintext and corrections, checks
// it is deflateLen bytes, then walks the skeleton chunk by chunk up to IEND.
// For an IDAT chunk of length L at IDAT-stream offset v, the bytes of
// [v, v+L) inside the removed range come from the recreated deflate stream
// and the rest from the skeleton. Everything after IEND is copied as is.
// A PNG this cannot represent exactly (a preset dictionary, an unfinished or
// unparsable deflate stream, a plaintext over maxPlain, no IEND) stays with
// Zstd or Raw. APNG frames (fdAT) and other chunks are kept verbatim.
package codec

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/klauspost/compress/zstd"
	log "github.com/sirupsen/logrus"
)

// Kind identifies how a blob's stored bytes encode its data. The values are
// persisted.
type Kind uint8

const (
	Raw    Kind = 0 // stored bytes are the data
	Zstd   Kind = 1 // one standard zstd frame of the data
	Lepton Kind = 2 // Lepton-coded JPEG
	PNG    Kind = 3 // PNG with its IDAT deflate stream replaced by preflate plaintext + corrections
)

// ErrUnavailable is returned by Unpack for a native kind when the binary was
// built without the taproot_codecs tag.
var ErrUnavailable = errors.New("codec: native codecs not compiled in (build with -tags taproot_codecs)")

const (
	// minZstdLen is the smallest input Pack tries zstd on.
	minZstdLen = 4096
	// maxDecoded bounds the memory one zstd frame may decode into.
	maxDecoded = 1 << 30
)

var (
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
	pngMagic  = []byte("\x89PNG\r\n\x1a\n")
)

var zstdDecoder = sync.OnceValue(func() *zstd.Decoder {
	dec, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(0),
		zstd.WithDecoderMaxMemory(maxDecoded),
		zstd.WithDecodeAllCapLimit(true))
	if err != nil {
		panic(fmt.Sprintf("codec: zstd decoder: %v", err)) // only invalid options fail
	}
	return dec
})

// Pack returns the smallest encoding of data that Unpack verifiably turns
// back into data, byte for byte. It never fails: on any error, panic, or a
// saving under 2%, it returns (Raw, data). A JPEG or PNG that its native codec
// packs is not also tried with whole-file zstd, which saves the CPU of a second
// high-level compression of an already compressed image.
func Pack(data []byte) (kind Kind, stored []byte) {
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("codec: pack panicked, storing raw: %v", r)
			kind, stored = Raw, data
		}
	}()
	if Native() {
		var (
			k   Kind
			s   []byte
			err error
		)
		switch {
		case bytes.HasPrefix(data, jpegMagic):
			k = Lepton
			s, err = leptonEncode(data)
		case bytes.HasPrefix(data, pngMagic):
			k = PNG
			s, err = packPNG(data)
		}
		if err == nil && s != nil && worthKeeping(s, data) && verified(k, s, data) {
			return k, s
		}
	}
	if len(data) >= minZstdLen {
		s := zstdEncode(data)
		if s != nil && worthKeeping(s, data) && verified(Zstd, s, data) {
			return Zstd, s
		}
	}
	return Raw, data
}

// worthKeeping reports whether stored saves at least 2% over data.
func worthKeeping(stored, data []byte) bool {
	return len(stored)*100 <= len(data)*98
}

func verified(kind Kind, stored, data []byte) bool {
	got, err := Unpack(kind, stored, len(data))
	return err == nil && bytes.Equal(got, data)
}

// Unpack reverses Pack. rawLen is the original length; the result must have
// exactly that length. For Raw the result is stored itself.
func Unpack(kind Kind, stored []byte, rawLen int) (data []byte, err error) {
	if rawLen < 0 {
		return nil, fmt.Errorf("codec: negative raw length %d", rawLen)
	}
	defer func() {
		if r := recover(); r != nil {
			data, err = nil, fmt.Errorf("codec: unpack kind %d panicked: %v", kind, r)
		}
	}()
	switch kind {
	case Raw:
		data = stored
	case Zstd:
		data, err = zstdDecode(stored, rawLen)
	case Lepton:
		if !Native() {
			return nil, ErrUnavailable
		}
		if uint64(rawLen) > math.MaxUint32 {
			return nil, fmt.Errorf("codec: lepton raw length %d too large", rawLen)
		}
		data, err = leptonDecode(stored, rawLen)
	case PNG:
		if !Native() {
			return nil, ErrUnavailable
		}
		data, err = unpackPNG(stored, rawLen)
	default:
		return nil, fmt.Errorf("codec: unknown kind %d", kind)
	}
	if err != nil {
		return nil, err
	}
	if len(data) != rawLen {
		return nil, fmt.Errorf("codec: kind %d decoded %d bytes, want %d", kind, len(data), rawLen)
	}
	return data, nil
}

// zstdDecode decodes one zstd frame that must hold exactly n bytes.
func zstdDecode(frame []byte, n int) ([]byte, error) {
	if n > maxDecoded {
		return nil, fmt.Errorf("codec: zstd length %d over limit", n)
	}
	// With the cap limit, DecodeAll fails rather than grow past cap(dst).
	out, err := zstdDecoder().DecodeAll(frame, make([]byte, 0, n))
	if err != nil {
		return nil, fmt.Errorf("codec: zstd: %w", err)
	}
	if len(out) != n {
		return nil, fmt.Errorf("codec: zstd decoded %d bytes, want %d", len(out), n)
	}
	return out, nil
}
