package musecli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// wireLines loads a captured exec --json transcript (one envelope per line).
func wireLines(t *testing.T, path string) []json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("captured wire %s not present", path)
	}
	var lines []json.RawMessage
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, json.RawMessage(line))
	}
	return lines
}

func wireLinesOfType(lines []json.RawMessage, payloadType string) []json.RawMessage {
	var out []json.RawMessage
	for _, line := range lines {
		var envelope struct {
			PayloadType string `json:"payload_type"`
		}
		if err := json.Unmarshal(line, &envelope); err == nil && envelope.PayloadType == payloadType {
			out = append(out, line)
		}
	}
	return out
}

// TestMuseToolEndChunkFromLiveWire replays the tool.result event captured
// live 2026-09-10 (meta turn running `ls` via the shell tool): the chunk
// must name the tool, keep the call id, and carry command + result.
func TestMuseToolEndChunkFromLiveWire(t *testing.T) {
	lines := wireLinesOfType(wireLines(t, "testdata/live_meta_tool_turn.jsonl"), "tool.result")
	if len(lines) != 1 {
		t.Fatalf("tool.result events = %d, want 1", len(lines))
	}
	chunk := museToolEndChunk(lines[0])
	if chunk == nil {
		t.Fatal("museToolEndChunk = nil for live tool.result")
	}
	if chunk.Type != llmtypes.StreamChunkTypeToolCallEnd {
		t.Fatalf("chunk type = %q, want tool_call_end", chunk.Type)
	}
	if chunk.ToolName != "bash" {
		t.Fatalf("tool name = %q, want bash", chunk.ToolName)
	}
	if !strings.HasPrefix(chunk.ToolCallID, "call_") {
		t.Fatalf("call id = %q, want call_ prefix", chunk.ToolCallID)
	}
	if !strings.Contains(chunk.ToolArgs, "ls -1 /tmp/musetmux-turn") {
		t.Fatalf("tool args = %q, want the ls command", chunk.ToolArgs)
	}
	if !strings.Contains(chunk.ToolResult, "List files in musetmux directory") {
		t.Fatalf("tool result = %q, want description fallback (empty output)", chunk.ToolResult)
	}
}

// TestMuseStatusChunkFromLiveWire replays lifecycle status events: the
// captured wire holds ONLY stream open/complete attempt counters, and those
// must be dropped (they rendered as Thinking spam with no thinking
// attached). Genuine progress still surfaces as reasoning; garbage is nil.
func TestMuseStatusChunkFromLiveWire(t *testing.T) {
	lines := wireLinesOfType(wireLines(t, "testdata/live_meta_tool_turn.jsonl"), "task.lifecycle.status")
	if len(lines) == 0 {
		t.Fatal("no task.lifecycle.status events in capture")
	}
	for _, line := range lines {
		if chunk := museStatusChunk(line); chunk != nil {
			t.Fatalf("plumbing status surfaced as reasoning: %+v", chunk)
		}
	}
	real := museStatusChunk(json.RawMessage(`{"payload_type":"task.lifecycle.status","payload":{"event":{"message":"running tools"}}}`))
	if real == nil || real.Type != llmtypes.StreamChunkTypeReasoning || real.Content != "running tools" {
		t.Fatalf("genuine progress status = %+v, want non-empty reasoning", real)
	}
	if real.Metadata["presentation"] != "assistant_update" {
		t.Fatal("progress must render as an assistant update")
	}
	if museStatusChunk(json.RawMessage(`{broken`)) != nil {
		t.Fatal("museStatusChunk(garbage) must be nil, not an error")
	}
	if museToolEndChunk(json.RawMessage(`{"payload_type":"tool.result","payload":{}}`)) != nil {
		t.Fatal("museToolEndChunk(empty payload) must be nil")
	}
}

// TestMuseToolStreamChunksPair: the exec wire has no tool-started event, so
// one tool.result must yield a synthetic start immediately before its end —
// same call id, name, and args on both; result only on the end. Without the
// pair, product rows render an orphan end with no name, no arguments, and
// an unopenable detail card.
func TestMuseToolStreamChunksPair(t *testing.T) {
	lines := wireLinesOfType(wireLines(t, "testdata/live_meta_tool_turn.jsonl"), "tool.result")
	if len(lines) != 1 {
		t.Fatalf("tool.result events = %d, want 1", len(lines))
	}
	chunks := museToolStreamChunks(lines[0])
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want [start, end]", len(chunks))
	}
	start, end := chunks[0], chunks[1]
	if start.Type != llmtypes.StreamChunkTypeToolCallStart {
		t.Fatalf("first chunk = %q, want tool_call_start", start.Type)
	}
	if end.Type != llmtypes.StreamChunkTypeToolCallEnd {
		t.Fatalf("second chunk = %q, want tool_call_end", end.Type)
	}
	if start.ToolCallID == "" || start.ToolCallID != end.ToolCallID {
		t.Fatalf("call ids %q/%q, want one shared id", start.ToolCallID, end.ToolCallID)
	}
	if start.ToolName != "bash" || end.ToolName != "bash" {
		t.Fatalf("names %q/%q, want bash on both", start.ToolName, end.ToolName)
	}
	if start.ToolArgs == "" || start.ToolArgs != end.ToolArgs {
		t.Fatalf("args %q/%q, want the shared ls command", start.ToolArgs, end.ToolArgs)
	}
	if start.ToolResult != "" {
		t.Fatalf("start result = %q, want empty (nothing completed yet)", start.ToolResult)
	}
	if end.ToolResult == "" {
		t.Fatal("end result empty, want the tool output")
	}
	if synthetic, _ := start.Metadata["synthetic"].(bool); !synthetic {
		t.Fatal("start chunk must be marked synthetic (wire has no tool-started event)")
	}
	if museToolStreamChunks(json.RawMessage(`{"payload_type":"tool.result","payload":{}}`)) != nil {
		t.Fatal("pair builder on empty payload must be nil, not a hollow pair")
	}
}
