//go:build taproot_codecs

package codec

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Stored Lepton and PNG bytes must keep decoding after any change to the
// pinned crates or to this package. The .stored files were written by Pack
// with the versions in native/Cargo.toml; regenerate them only together with
// a format version that still reads the old ones
// (TAPROOT_CODEC_GOLDEN=update).
func TestGoldenStoredBytesStillDecode(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind Kind
	}{
		{"progressive.jpg", Lepton},
		{"zlib.png", PNG},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original, err := os.ReadFile(filepath.Join("testdata", tc.name))
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", tc.name+".stored")
			if os.Getenv("TAPROOT_CODEC_GOLDEN") == "update" {
				kind, stored := Pack(original)
				if kind != tc.kind {
					t.Fatalf("Pack chose kind %d, want %d", kind, tc.kind)
				}
				if err := os.WriteFile(golden, stored, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			stored, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Unpack(tc.kind, stored, len(original))
			if err != nil {
				t.Fatalf("stored bytes no longer decode: %v", err)
			}
			if !bytes.Equal(got, original) {
				t.Fatal("stored bytes decode to different data")
			}
		})
	}
}
