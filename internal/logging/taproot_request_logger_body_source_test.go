package logging

import (
	"os"
	"strings"
	"testing"
)

// TestFileBodySourceAppendsInOrder covers the cached append handle: many
// appends, a new part in between, reads while the handle is open, and cleanup.
func TestFileBodySourceAppendsInOrder(t *testing.T) {
	src, err := NewFileBodySourceInDir(t.TempDir(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for i := 0; i < 500; i++ {
		chunk := "data: chunk " + strings.Repeat("x", i%7) + "\n\n"
		if err := src.AppendBytes([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
		want.WriteString(chunk)
	}
	mid, err := src.Bytes()
	if err != nil || string(mid) != want.String() {
		t.Fatalf("read with the append handle open: %d bytes, err %v; want %d bytes", len(mid), err, want.Len())
	}
	if err := src.AppendPart([]byte("second part")); err != nil {
		t.Fatal(err)
	}
	// Parts are joined with a newline, and AppendPart ends its part with one.
	want.WriteString("\nsecond part\n")
	if err := src.AppendBytes([]byte(" and more")); err != nil {
		t.Fatal(err)
	}
	want.WriteString(" and more")
	got, err := src.Bytes()
	if err != nil || string(got) != want.String() {
		t.Fatalf("got %q, err %v; want %q", tail(got), err, tail([]byte(want.String())))
	}
	dir := src.dir
	if err := src.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("part directory left behind: %v", err)
	}
	if err := src.AppendBytes([]byte("late")); err == nil {
		t.Fatal("append after cleanup succeeded")
	}
}

// Executors append every streamed SSE line, so an append must not reopen the
// part file. Opening one allocates; writing to an open one does not.
func TestFileBodySourceAppendKeepsPartOpen(t *testing.T) {
	src, err := NewFileBodySourceInDir(t.TempDir(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Cleanup() })
	chunk := []byte("data: {\"type\":\"content_block_delta\"}\n\n")
	if err := src.AppendBytes(chunk); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		if err := src.AppendBytes(chunk); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("AppendBytes made %.0f allocations per chunk, want 0: it reopens the part file", allocs)
	}
}

func tail(b []byte) string {
	if len(b) > 40 {
		return string(b[len(b)-40:])
	}
	return string(b)
}
