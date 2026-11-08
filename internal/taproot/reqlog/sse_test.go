package reqlog

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

func sseDelta(index int, kind, field, piece string, spaces int, rest string) string {
	return fmt.Sprintf(`data: {"type":"content_block_delta","index":%d,"delta":{"type":%q,%q:"%s"%s}%s}`, index, kind, field, piece, rest, strings.Repeat(" ", spaces))
}

func TestSSETransformRoundTrips(t *testing.T) {
	cases := map[string]string{
		"thinking with padding": "event: content_block_delta\n" + sseDelta(0, "thinking_delta", "thinking", "I'm going", 9, `,"estimated_tokens":null`) + "\n\n" +
			"event: content_block_delta\n" + sseDelta(0, "thinking_delta", "thinking", " to check", 0, `,"estimated_tokens":null`) + "\n\n",
		"tool json":        sseDelta(1, "input_json_delta", "partial_json", `{\"comma`, 4, "") + "\n" + sseDelta(1, "input_json_delta", "partial_json", `nd\": \"ls\"}`, 0, "") + "\n",
		"empty piece":      sseDelta(1, "input_json_delta", "partial_json", "", 3, "") + "\n",
		"escapes":          sseDelta(0, "text_delta", "text", `a\nb\t\"c\" \\ é \u0001 \/`, 2, "") + "\n",
		"surrogate pair":   sseDelta(0, "text_delta", "text", `smile \ud83d\ude00`, 1, "") + "\n",
		"no final newline": sseDelta(0, "text_delta", "text", "end", 5, ""),
		"crlf":             sseDelta(0, "text_delta", "text", "x", 2, "") + "\r\n",
		"brace in rest":    sseDelta(0, "text_delta", "text", "x", 2, `,"k":"a}b"`) + "\n",
		"mixed":            "=== RESPONSE ===\nStatus: 200\n\nevent: message_start\ndata: {\"type\":\"message_start\"}\n\n" + sseDelta(0, "text_delta", "text", "Hello", 7, "") + "\n\n",
	}
	untransformable := map[string]bool{"crlf": true, "brace in rest": true}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			parts, ok := encodeSSE([]byte(text))
			if ok == untransformable[name] {
				t.Fatalf("transformed = %v", ok)
			}
			if !ok {
				return
			}
			if name == "surrogate pair" && parts.unescaped {
				t.Fatal("an escaped surrogate pair cannot round-trip unescaped")
			}
			back, err := decodeSSE(parts)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(back, []byte(text)) {
				t.Fatalf("round trip changed text:\n%q\n%q", text, back)
			}
		})
	}
}

// TestSSEFramesRebuildExactly drives every case through encodeFrame and
// decodeFrame, which keep the transform only when it reproduces the text.
func TestSSEFramesRebuildExactly(t *testing.T) {
	inputs := []string{
		sseDelta(0, "text_delta", "text", `smile \ud83d\ude00`, 1, "") + "\n",
		sseDelta(0, "text_delta", "text", "x", 2, "") + "\r\n",
		sseDelta(0, "text_delta", "text", "x", 2, `,"k":"a}b"`) + "\n",
		strings.Repeat(sseDelta(2, "thinking_delta", "thinking", "step", 3, `,"estimated_tokens":null`)+"\n\n", 50),
		"plain text without events\n",
	}
	for i, in := range inputs {
		meta := &recordMeta{RequestID: fmt.Sprint(i), Method: "POST", URL: "/", Path: "/"}
		f, _, err := encodeFrame(meta, []byte(in), func(blobHash, []byte) {})
		if err != nil {
			t.Fatal(err)
		}
		_, text, refs, err := decodeFrame(f.bytes())
		if err != nil || len(refs) != 0 || string(text) != in {
			t.Fatalf("input %d: err %v, got %q", i, err, text)
		}
	}
}

// TestStreamingRecordWithDeltasRebuilds sends a streamed reply shaped like
// Anthropic's through the logger and reads it back after sealing.
func TestStreamingRecordWithDeltasRebuilds(t *testing.T) {
	h := newHarness(t, Config{})
	lg := h.logs.logger
	w, err := lg.LogStreamingRequest("/v1/messages", "POST", map[string][]string{"X-Claude-Code-Session-Id": {"sse"}}, []byte(`{"model":"m"}`), "sse-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = w.WriteStatus(200, map[string][]string{"Content-Type": {"text/event-stream"}})
	var body bytes.Buffer
	for i := 0; i < 300; i++ {
		chunk := "event: content_block_delta\n" + sseDelta(0, "thinking_delta", "thinking", fmt.Sprintf(" word%d \\\"q\\\"", i), i%13, `,"estimated_tokens":null`) + "\n\n"
		w.WriteChunkAsync([]byte(chunk))
		body.WriteString(chunk)
	}
	_ = w.WriteAPIResponse(apiResponseText(body.String()))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	h.flush()
	h.sealAll()
	d, err := h.logs.Get(context.Background(), "sse-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Response.Body != body.String() || !strings.Contains(d.Upstream.Response, body.String()) {
		t.Fatal("streamed reply did not rebuild exactly")
	}
	loc, err := h.logs.locateForTest("sse-1")
	if err != nil {
		t.Fatal(err)
	}
	fr, err := h.logs.engine.readFrame(context.Background(), loc)
	if err != nil {
		t.Fatal(err)
	}
	hdr, err := parseFrameHeader(fr[:bytes.IndexByte(fr, '\n')])
	if err != nil || hdr.flags&frameSSE == 0 {
		t.Fatalf("frame flags %d (err %v): the SSE transform did not apply", hdr.flags, err)
	}
}
