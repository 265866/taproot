package executor

import (
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
)

// Plain tool_use blocks pass through byte for byte, and blocks that carry
// provenance fields are still stripped wherever they sit in the conversation.
func TestSanitizeClaudeMessagesStripsOnlyToolUsesWithProvenance(t *testing.T) {
	plain := `{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}`
	body := `{"model":"claude-opus-4-1","messages":[` +
		`{"role":"assistant","content":[` + plain + `,` +
		`{"type":"tool_use","id":"toolu_2","name":"Read","input":{},"model":"gemini-3","extra_content":{"google":{"thought_signature":"abc"}}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"},{"type":"tool_result","tool_use_id":"toolu_2","content":"ok"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"tool_use","id":"toolu_3","name":"Bash","input":{},"thoughtSignature":"sig"}]}]}`
	doc := ojson.NewDoc([]byte(body))

	report, modified := sanitizeClaudeMessagesForClaudeUpstreamDoc(doc, "claude-opus-4-1", false)

	if !modified || report.DroppedSignatures != 2 {
		t.Fatalf("modified=%v droppedSignatures=%d, want true and 2", modified, report.DroppedSignatures)
	}
	out := doc.Bytes()
	if got := ojson.GetBytes(out, "messages.0.content.0").Raw; got != plain {
		t.Fatalf("plain tool_use changed:\n got %s\nwant %s", got, plain)
	}
	for _, path := range []string{"messages.0.content.1.model", "messages.0.content.1.extra_content", "messages.2.content.1.thoughtSignature"} {
		if ojson.GetBytes(out, path).Exists() {
			t.Fatalf("%s survived sanitizing: %s", path, out)
		}
	}
	if got := ojson.GetBytes(out, "messages.2.content.1.id").String(); got != "toolu_3" {
		t.Fatalf("messages.2.content.1.id = %q, want toolu_3", got)
	}
}

// Probing a tool_use part by its index path rescans every earlier message, so
// probing every part made sanitizing quadratic in conversation size. Each
// probe also builds its path strings, so the allocation count shows whether
// plain tool_use parts were probed: about 2 per part when skipped, 9 when not.
func TestSanitizeClaudeMessagesSkipsPlainToolUses(t *testing.T) {
	const parts = 500
	var b strings.Builder
	b.WriteString(`{"model":"claude-opus-4-1","messages":[`)
	for i := range parts {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}`)
	}
	b.WriteString(`]}`)
	body := []byte(b.String())

	allocs := testing.AllocsPerRun(3, func() {
		if _, modified := sanitizeClaudeMessagesForClaudeUpstreamDoc(ojson.NewDoc(body), "claude-opus-4-1", false); modified {
			t.Fatal("plain tool_use parts were modified")
		}
	})
	if perPart := allocs / parts; perPart > 5 {
		t.Fatalf("%.1f allocations per plain tool_use part, want at most 5: the parts are being probed by path", perPart)
	}
}
