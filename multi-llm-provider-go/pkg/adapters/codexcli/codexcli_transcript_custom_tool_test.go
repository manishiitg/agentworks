package codexcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rows copied from a REAL codex rollout (~/.codex/sessions/.../rollout-*.jsonl,
// codex code-mode `exec`). The output field is an ARRAY of content blocks and
// the call body is "input", not "arguments" — the two shapes that previously
// made the completion row fail to unmarshal, so no ToolCallEnd was emitted and
// the tool's UI chip spun forever.
const codexCustomToolRollout = `{"timestamp":"2026-08-16T11:09:35.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"call_W3fI","name":"exec","input":"const r = await tools.mcp__api_bridge__execute_shell_command({command:\"cat build_id.txt\"});"}}
{"timestamp":"2026-08-16T11:09:36.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_W3fI","output":[{"type":"input_text","text":"Script completed\nWall time 0.0 seconds"},{"type":"input_text","text":"exit_code: 0\nstdout:\nBUILD_ID_57c142dbb6be\n"}]}}
`

func TestCodexTranscriptEmitsEndForCustomToolCallOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	if err := os.WriteFile(path, []byte(codexCustomToolRollout), 0o600); err != nil {
		t.Fatal(err)
	}
	turnStart := time.Date(2026, 8, 16, 11, 0, 0, 0, time.UTC)

	evs, _, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, map[string]time.Time{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var start, end *codexTranscriptEvent
	for i := range evs {
		switch {
		case evs[i].IsToolEnd:
			end = &evs[i]
		case evs[i].ToolName != "":
			start = &evs[i]
		}
	}

	if start == nil {
		t.Fatal("no tool START parsed from custom_tool_call")
	}
	if start.ToolName != "exec" || start.ToolCallID != "call_W3fI" {
		t.Fatalf("start mismatch: name=%q id=%q", start.ToolName, start.ToolCallID)
	}
	// The details the UI had nothing to render before: the call body.
	if !strings.Contains(start.ToolArgs, "mcp__api_bridge__execute_shell_command") {
		t.Fatalf("start carried no usable args; got %q", start.ToolArgs)
	}

	// The regression this pins: the completion row must not be dropped.
	if end == nil {
		t.Fatal("no tool END parsed from custom_tool_call_output — the UI chip would spin forever")
	}
	if end.ToolCallID != "call_W3fI" {
		t.Fatalf("end has call id %q, want call_W3fI (an unmatched id leaves the start unpaired)", end.ToolCallID)
	}
	if !strings.Contains(end.ToolResult, "BUILD_ID_57c142dbb6be") {
		t.Fatalf("end result did not flatten the content-block array; got %q", end.ToolResult)
	}
}

// The older shape must keep working: function_call_output sends output as a
// bare JSON string.
func TestCodexTranscriptStillHandlesStringOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")
	rows := `{"timestamp":"2026-08-16T11:09:35.000Z","type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"execute_shell_command","arguments":"{\"command\":\"ls\"}"}}
{"timestamp":"2026-08-16T11:09:36.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"total 0\n"}}
`
	if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	evs, _, err := readCodexTranscriptEventsFromFile(path, 0, time.Date(2026, 8, 16, 11, 0, 0, 0, time.UTC), map[string]time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var sawEnd bool
	for _, e := range evs {
		if e.IsToolEnd && e.ToolCallID == "c1" && strings.Contains(e.ToolResult, "total 0") {
			sawEnd = true
		}
	}
	if !sawEnd {
		t.Fatal("string-shaped function_call_output no longer produces a tool END")
	}
}

func TestCodexRolloutTextShapes(t *testing.T) {
	if got := codexRolloutText([]byte(`"plain"`)); got != "plain" {
		t.Fatalf("string shape: got %q", got)
	}
	if got := codexRolloutText([]byte(`[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"}]`)); got != "a\nb" {
		t.Fatalf("array shape: got %q", got)
	}
	if got := codexRolloutText(nil); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}
