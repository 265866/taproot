package session

import (
	"strings"
	"testing"

	"github.com/265866/taproot/internal/util"
)

func TestHasExplicitSessionDoesNotRescanLargePayload(t *testing.T) {
	payload := []byte(`{"messages":["` + strings.Repeat("x", 1<<20) + `"]}`)

	singleLookup := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			root := util.ParseGJSONBytesNoCopy(payload)
			if root.Get("missing_session_id").Exists() {
				b.Fatal("unexpected session ID")
			}
		}
	})
	detection := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if hasExplicitSession(nil, payload) {
				b.Fatal("unexpected explicit session")
			}
		}
	})

	if detection.NsPerOp() > singleLookup.NsPerOp()*12 {
		t.Fatalf("explicit session detection rescans large payload: detection %d ns/op, single lookup %d ns/op", detection.NsPerOp(), singleLookup.NsPerOp())
	}
}

func TestExtractSessionInfoDoesNotRescanLargePayload(t *testing.T) {
	payload := []byte(`{"messages":["` + strings.Repeat("x", 1<<20) + `"]}`)

	singleLookup := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			root := util.ParseGJSONBytesNoCopy(payload)
			if root.Get("missing_session_id").Exists() {
				b.Fatal("unexpected session ID")
			}
		}
	})
	extraction := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if info, ok := ExtractSessionInfo(nil, payload, nil); ok {
				b.Fatalf("unexpected session: %q", info.SessionID)
			}
		}
	})

	// A request without session fields must not rescan a large message for every candidate key.
	// Extraction measured about 5 single lookups, and a rescan for each of 100 keys 65 to 104.
	// A loaded machine pushed extraction to 17, so the bound sits between the two.
	if extraction.NsPerOp() > singleLookup.NsPerOp()*40 {
		t.Fatalf("session extraction rescans large payload: extraction %d ns/op, single lookup %d ns/op", extraction.NsPerOp(), singleLookup.NsPerOp())
	}
}
