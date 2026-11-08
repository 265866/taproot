//go:build !taproot_codecs

package codec

import (
	"fmt"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Native reports whether Lepton and PNG are compiled in.
func Native() bool { return false }

var zstdEncoder = sync.OnceValue(func() *zstd.Encoder {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		panic(fmt.Sprintf("codec: zstd encoder: %v", err)) // only invalid options fail
	}
	return enc
})

// zstdEncode returns one zstd frame of data.
func zstdEncode(data []byte) []byte { return zstdEncoder().EncodeAll(data, nil) }

func leptonEncode([]byte) ([]byte, error) { return nil, ErrUnavailable }

func leptonDecode([]byte, int) ([]byte, error) { return nil, ErrUnavailable }

func deflateUnpack([]byte, int) (plain, corrections []byte, consumed int, err error) {
	return nil, nil, 0, ErrUnavailable
}

func deflatePack(_, _ []byte) ([]byte, error) { return nil, ErrUnavailable }
