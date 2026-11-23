package cursorcli

import (
	"crypto/md5" //nolint:gosec

	"encoding/binary"

	"encoding/json"

	"testing"

	_ "modernc.org/sqlite"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCursorTranscriptUnwrapsCallMcpToolArguments(t *testing.T) {
	parts := cursorAssistantPartsFromContent(json.RawMessage(`[
		{"type":"tool-call","toolCallId":"call-1","toolName":"CallMcpTool","args":{"arguments":{"command":"pwd"},"server":"api-bridge","toolName":"execute_shell_command"}}
	]`))
	if len(parts) != 1 {
		t.Fatalf("parts = %+v, want one tool call", parts)
	}
	call, ok := parts[0].(llmtypes.ToolCall)
	if !ok || call.FunctionCall == nil {
		t.Fatalf("part = %#v, want ToolCall", parts[0])
	}
	if call.FunctionCall.Name != "mcp__api-bridge__execute_shell_command" {
		t.Fatalf("tool name = %q, want concrete api-bridge shell tool", call.FunctionCall.Name)
	}
	if call.FunctionCall.Arguments != `{"command":"pwd"}` {
		t.Fatalf("tool arguments = %q, want unwrapped command payload", call.FunctionCall.Arguments)
	}
}

// synthBlobID returns a deterministic 32-byte id for the test (one
// repeated marker byte). Real cursor IDs are SHA256 of content; the
// parser only uses them as opaque lookup keys, so any 32 bytes work.
func synthBlobID(marker byte) []byte {
	id := make([]byte, 32)
	for i := range id {
		id[i] = marker
	}
	// Embed marker count to keep ids distinguishable when debugging.
	binary.BigEndian.PutUint16(id[:2], uint16(marker))
	return id
}

// buildCursorRootBlob constructs a protobuf root blob with one
// length-delimited field-1 entry per child ref, in source order.
func buildCursorRootBlob(t *testing.T, refs [][]byte) []byte {
	t.Helper()
	var buf []byte
	for _, r := range refs {
		if len(r) != 32 {
			t.Fatalf("ref must be 32 bytes, got %d", len(r))
		}
		// tag = field<<3 | wire_type
		// field 1, wire_type 2 (length-delimited) → 0x0a
		buf = append(buf, 0x0a)
		// length = 32 (single varint byte)
		buf = append(buf, 32)
		buf = append(buf, r...)
	}
	// Add a couple of unrelated fields the parser should ignore.
	buf = append(buf, 0x10, 0x01) // field 2, varint, value 1
	buf = append(buf, 0xb0, 0x01, // field 22 (22<<3|0 = 176 = 0xb0)
		0x03, 'c', 'l', 'i')
	_ = md5.Sum //nolint:gosec // suppress unused import warning when not exercised
	return buf
}
