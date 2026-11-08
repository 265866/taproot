//go:build taproot_codecs

package codec

/*
#cgo LDFLAGS: ${SRCDIR}/native/target/release/libtaproot_codec.a -lgcc_s -lutil -lrt -lpthread -lm -ldl
#include <stddef.h>
#include <stdint.h>

typedef struct {
	uint8_t *ptr;
	size_t len;
} TpcBuf;

int32_t tpc_lepton_encode(const uint8_t *data, size_t len, TpcBuf *out);
int32_t tpc_lepton_decode(const uint8_t *data, size_t len, uint32_t max_jpeg_len, TpcBuf *out);
int32_t tpc_deflate_unpack(const uint8_t *data, size_t len, size_t plain_limit,
	TpcBuf *plain, TpcBuf *corrections, size_t *consumed);
int32_t tpc_deflate_pack(const uint8_t *plain, size_t plain_len,
	const uint8_t *corrections, size_t corrections_len, TpcBuf *out);
int32_t tpc_zstd_compress(const uint8_t *data, size_t len, int32_t level, TpcBuf *out);
void tpc_free(TpcBuf *buf);
*/
import "C"

import (
	"bytes"
	"fmt"
	"unsafe"
)

// Native reports whether Lepton and PNG are compiled in.
func Native() bool { return true }

func cBytes(b []byte) (*C.uint8_t, C.size_t) {
	return (*C.uint8_t)(unsafe.SliceData(b)), C.size_t(len(b))
}

// goBytes copies buf into Go memory and frees it.
func goBytes(buf *C.TpcBuf) []byte {
	defer C.tpc_free(buf)
	if buf.len == 0 {
		return []byte{}
	}
	return bytes.Clone(unsafe.Slice((*byte)(buf.ptr), int(buf.len)))
}

func status(op string, rc C.int32_t) error {
	switch rc {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("codec: %s rejected input", op)
	case 2:
		return fmt.Errorf("codec: %s panicked", op)
	case 4:
		return fmt.Errorf("codec: %s: deflate stream incomplete", op)
	default:
		return fmt.Errorf("codec: %s failed with status %d", op, int32(rc))
	}
}

func leptonEncode(jpeg []byte) ([]byte, error) {
	var out C.TpcBuf
	p, n := cBytes(jpeg)
	if err := status("lepton encode", C.tpc_lepton_encode(p, n, &out)); err != nil {
		C.tpc_free(&out)
		return nil, err
	}
	return goBytes(&out), nil
}

func leptonDecode(stored []byte, maxLen int) ([]byte, error) {
	var out C.TpcBuf
	p, n := cBytes(stored)
	if err := status("lepton decode", C.tpc_lepton_decode(p, n, C.uint32_t(maxLen), &out)); err != nil {
		C.tpc_free(&out)
		return nil, err
	}
	return goBytes(&out), nil
}

// deflateUnpack splits a raw deflate stream into plaintext and preflate
// corrections. consumed is the stream's length, which may be shorter than
// deflate when other bytes follow it.
func deflateUnpack(deflate []byte, plainLimit int) (plain, corrections []byte, consumed int, err error) {
	var outPlain, outCorr C.TpcBuf
	var used C.size_t
	p, n := cBytes(deflate)
	rc := C.tpc_deflate_unpack(p, n, C.size_t(plainLimit), &outPlain, &outCorr, &used)
	if err := status("preflate", rc); err != nil {
		C.tpc_free(&outPlain)
		C.tpc_free(&outCorr)
		return nil, nil, 0, err
	}
	return goBytes(&outPlain), goBytes(&outCorr), int(used), nil
}

func deflatePack(plain, corrections []byte) ([]byte, error) {
	var out C.TpcBuf
	p, pn := cBytes(plain)
	c, cn := cBytes(corrections)
	if err := status("preflate recreate", C.tpc_deflate_pack(p, pn, c, cn, &out)); err != nil {
		C.tpc_free(&out)
		return nil, err
	}
	return goBytes(&out), nil
}

// zstdLevel is the libzstd level for every frame Pack writes.
const zstdLevel = 19

// zstdEncode returns one zstd frame of data, or nil if libzstd fails.
func zstdEncode(data []byte) []byte {
	var out C.TpcBuf
	p, n := cBytes(data)
	if err := status("zstd", C.tpc_zstd_compress(p, n, zstdLevel, &out)); err != nil {
		C.tpc_free(&out)
		return nil
	}
	return goBytes(&out)
}
