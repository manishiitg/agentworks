package codexcli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func appendLine(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// Real Codex (0.144+) rollout rows: assistant prose is an event_msg
// agent_message; MCP calls are event_msg mcp_tool_call_begin/_end, two
// distinct rows, with the tool under invocation.tool. Native tools
// (function_call/custom_tool_call + their _output rows) are the
// response_item form and are built inline where used below.
func codexAgentMsg(ts, text string) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"agent_message","message":"` + text + `"}}` + "\n"
}
func codexMCPCallBegin(ts, tool, callID string) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"mcp_tool_call_begin","call_id":"` + callID + `","invocation":{"server":"api-bridge","tool":"` + tool + `"}}}` + "\n"
}
func codexMCPCallEnd(ts, callID string) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"` + callID + `"}}` + "\n"
}

// TestReadCodexTranscriptEventsIncremental verifies the mid-turn rollout tailer
// against the REAL schema: prior-turn rows skipped, agent_message → content,
// mcp_tool_call_begin → tool start (name from invocation.tool), reading from the
// returned offset yields only new rows, and a partial trailing line is held back.
func TestReadCodexTranscriptEventsIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	old := turnStart.Add(-time.Second).Format(time.RFC3339Nano)

	appendLine(t, path, codexAgentMsg(old, "PRIOR")+codexAgentMsg(ts, "Reading the file.")+codexMCPCallBegin(ts, "echo_contract", "c1"))

	events, off1, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, nil)
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("read 1: got %d events, want 2 (text, tool); %+v", len(events), events)
	}
	if events[0].Text != "Reading the file." || events[0].ToolName != "" {
		t.Fatalf("read 1 events[0] = %+v", events[0])
	}
	if events[1].ToolName != "echo_contract" || events[1].ToolCallID != "c1" {
		t.Fatalf("read 1 events[1] = %+v", events[1])
	}

	// Complete row + partial (no-newline) row.
	text2 := codexAgentMsg(ts, "Done.")
	partial := `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"agent_message","message":"Aft`
	appendLine(t, path, text2+partial)

	events, off2, err := readCodexTranscriptEventsFromFile(path, off1, turnStart, nil)
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if len(events) != 1 || events[0].Text != "Done." {
		t.Fatalf("read 2: got %+v, want single 'Done.' (partial held)", events)
	}
	if off2 <= off1 {
		t.Fatalf("read 2: offset did not advance")
	}

	appendLine(t, path, `er."}}`+"\n")
	events, _, err = readCodexTranscriptEventsFromFile(path, off2, turnStart, nil)
	if err != nil {
		t.Fatalf("read 3: %v", err)
	}
	if len(events) != 1 || events[0].Text != "After." {
		t.Fatalf("read 3: got %+v, want single 'After.' (completed partial)", events)
	}
}

// TestReadCodexTranscriptEventsInterleavedOrder proves the realistic
// text → tool → text → tool → final-text shape streams in correct order across
// incremental (append-live) polls, using the real event_msg schema.
func TestReadCodexTranscriptEventsInterleavedOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)

	rows := []string{
		codexAgentMsg(ts, "Let me check the first file."),
		codexMCPCallBegin(ts, "echo_contract", "c1"),
		codexAgentMsg(ts, "Now the second file."),
		codexMCPCallBegin(ts, "echo_contract", "c2"),
		codexAgentMsg(ts, "Done. FINAL."),
	}

	var got []string
	var offset int64
	for _, r := range rows {
		appendLine(t, path, r)
		events, next, err := readCodexTranscriptEventsFromFile(path, offset, turnStart, nil)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		offset = next
		for _, e := range events {
			if e.ToolName != "" {
				got = append(got, "tool:"+e.ToolName)
			} else {
				got = append(got, "text:"+e.Text)
			}
		}
	}

	want := []string{
		"text:Let me check the first file.",
		"tool:echo_contract",
		"text:Now the second file.",
		"tool:echo_contract",
		"text:Done. FINAL.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interleaved emission order wrong:\n got=%v\nwant=%v", got, want)
	}
}

// TestReadCodexTranscriptEventsResponseItemForm verifies the older response_item
// form is still parsed (assistant output_text + function_call), so both Codex
// schemas are covered.
func TestReadCodexTranscriptEventsResponseItemForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)

	appendLine(t, path,
		`{"timestamp":"`+ts+`","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}`+"\n"+
			`{"timestamp":"`+ts+`","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call_A"}}`+"\n")

	events, _, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 || events[0].Text != "hi" || events[1].ToolName != "exec_command" {
		t.Fatalf("response_item form parse wrong: %+v", events)
	}
}

// TestReadCodexTranscriptEventsRobustToNoiseAndUnicode covers production-variety
// input the happy-path tests miss: a garbage/non-JSON line and an unrelated event
// type interleaved with real rows must be skipped without derailing parsing, and
// multi-byte unicode (emoji, accents, CJK) in assistant text must survive
// byte-for-byte.
func TestReadCodexTranscriptEventsRobustToNoiseAndUnicode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	unicodeMsg := "Café ✅ 完了 — result is ZEBRA_✨"

	appendLine(t, path,
		"this is not json at all\n"+
			`{"timestamp":"`+ts+`","type":"session_meta","payload":{"type":"whatever"}}`+"\n"+
			codexAgentMsg(ts, unicodeMsg)+
			`{"broken":`+"\n"+ // truncated/garbage JSON
			codexMCPCallBegin(ts, "echo_contract", "c1"))

	events, _, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("noise not skipped cleanly: got %d events, want 2 (unicode text + tool); %+v", len(events), events)
	}
	if events[0].Text != unicodeMsg {
		t.Fatalf("unicode text mangled:\n got=%q\nwant=%q", events[0].Text, unicodeMsg)
	}
	if events[1].ToolName != "echo_contract" {
		t.Fatalf("tool after noise not parsed: %+v", events[1])
	}
}

// TestReadCodexTranscriptEventsLargeContentLine proves a large assistant message
// (multi-KB, the kind a real coding turn emits) is read whole, not truncated at
// the tailer's read boundary.
func TestReadCodexTranscriptEventsLargeContentLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	big := strings.Repeat("A", 200*1024) // 200KB assistant message

	appendLine(t, path, codexAgentMsg(ts, big))
	events, _, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 || len(events[0].Text) != len(big) {
		t.Fatalf("large content not read whole: got %d events, text len %d (want 1 event, len %d)", len(events), func() int {
			if len(events) > 0 {
				return len(events[0].Text)
			}
			return 0
		}(), len(big))
	}
}

// TestReadCodexTranscriptEventsMCPCallHasEndWithDuration proves the actual bug
// fix: mcp_tool_call_begin/_end used to be deduped down to a single Start —
// the caller (a tmux/interactive session) had no idea the call ever finished,
// no duration, no result. Now begin produces a Start and end produces a real
// End carrying the measured duration. ToolResult stays empty for MCP calls:
// codex's own event stream never includes one, in this transport or the
// structured one (see codexcli_structured_adapter.go).
func TestReadCodexTranscriptEventsMCPCallHasEndWithDuration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	beginTS := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	endTS := turnStart.Add(4 * time.Second).Format(time.RFC3339Nano)

	appendLine(t, path, codexMCPCallBegin(beginTS, "echo_contract", "c1")+codexMCPCallEnd(endTS, "c1"))

	pending := map[string]time.Time{}
	events, _, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, pending)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (start, end); %+v", len(events), events)
	}
	if events[0].IsToolEnd || events[0].ToolName != "echo_contract" || events[0].ToolCallID != "c1" {
		t.Fatalf("events[0] (start) = %+v", events[0])
	}
	if !events[1].IsToolEnd || events[1].ToolCallID != "c1" {
		t.Fatalf("events[1] (end) = %+v", events[1])
	}
	if events[1].ToolResult != "" {
		t.Fatalf("MCP call end should carry no result text, got %q", events[1].ToolResult)
	}
	if events[1].ToolDuration != 3*time.Second {
		t.Fatalf("ToolDuration = %v, want 3s", events[1].ToolDuration)
	}
	if len(pending) != 0 {
		t.Fatalf("pending map did not drain: %+v", pending)
	}
}

// Codex 0.145 code-mode rollouts can omit mcp_tool_call_begin and persist only
// an mcp_tool_call_end carrying the invocation. The tailer must still expose
// the real MCP tool name; otherwise consumers see only the outer generic
// custom_tool_call name "exec".
func TestReadCodexTranscriptEventsMCPCallEndOnlySynthesizesStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)

	// This is the observed code-mode shape: Codex persists only the end row,
	// but that row still holds the input inside invocation.arguments.
	appendLine(t, path, `{"timestamp":"`+ts+`","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"exec-1","invocation":{"server":"api-bridge","tool":"execute_shell_command","arguments":{"command":"printf hello","timeout":30}}}}`+"\n")

	pending := map[string]time.Time{}
	events, _, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, pending)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want synthesized start plus end; %+v", len(events), events)
	}
	if events[0].IsToolEnd || events[0].ToolName != "execute_shell_command" || events[0].ToolCallID != "exec-1" {
		t.Fatalf("events[0] (synthesized start) = %+v", events[0])
	}
	if events[0].ToolArgs != `{"command":"printf hello","timeout":30}` {
		t.Fatalf("events[0].ToolArgs = %q", events[0].ToolArgs)
	}
	if chunk := codexTranscriptEventToChunk(events[0]); chunk.ToolArgs != events[0].ToolArgs {
		t.Fatalf("start chunk ToolArgs = %q, want %q", chunk.ToolArgs, events[0].ToolArgs)
	}
	if !events[1].IsToolEnd || events[1].ToolCallID != "exec-1" {
		t.Fatalf("events[1] (end) = %+v", events[1])
	}
}

func TestReadCodexTranscriptEventsItemCompletedUsesSemanticMCPTool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	appendLine(t, path, `{"timestamp":"`+ts+`","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"exec-123","server":"workbench","tool":"write_file","arguments":{"name":"result.txt","content":"ZEBRA"},"status":"completed","result":{"content":[{"type":"text","text":"WROTE result.txt"}],"isError":false},"duration":{"secs":1,"nanos":250000000}}}}`+"\n")

	events, _, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, map[string]time.Time{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want semantic start plus end: %+v", len(events), events)
	}
	if events[0].IsToolEnd || events[0].ToolName != "write_file" || events[0].ToolCallID != "exec-123" {
		t.Fatalf("start = %+v", events[0])
	}
	if events[0].ToolArgs != `{"name":"result.txt","content":"ZEBRA"}` {
		t.Fatalf("start args = %q", events[0].ToolArgs)
	}
	if !events[1].IsToolEnd || events[1].ToolCallID != "exec-123" || events[1].ToolResult != "WROTE result.txt" {
		t.Fatalf("end = %+v", events[1])
	}
	if events[1].ToolDuration != 1250*time.Millisecond {
		t.Fatalf("duration = %v, want 1.25s", events[1].ToolDuration)
	}
}

// TestReadCodexTranscriptEventsNativeToolCallHasEndWithResult proves the same
// fix for codex's native tools (response_item function_call/custom_tool_call):
// the _output row used to be silently dropped entirely, so a tmux-transport
// native tool call had a start and nothing else. It now yields a proper End
// carrying the real output text and measured duration.
func TestReadCodexTranscriptEventsNativeToolCallHasEndWithResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	callTS := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	outputTS := turnStart.Add(3 * time.Second).Format(time.RFC3339Nano)

	appendLine(t, path,
		`{"timestamp":"`+callTS+`","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call_A"}}`+"\n"+
			`{"timestamp":"`+outputTS+`","type":"response_item","payload":{"type":"function_call_output","call_id":"call_A","output":"file written"}}`+"\n")

	pending := map[string]time.Time{}
	events, _, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, pending)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (start, end); %+v", len(events), events)
	}
	if events[0].IsToolEnd || events[0].ToolName != "exec_command" {
		t.Fatalf("events[0] (start) = %+v", events[0])
	}
	if !events[1].IsToolEnd || events[1].ToolCallID != "call_A" {
		t.Fatalf("events[1] (end) = %+v", events[1])
	}
	if events[1].ToolResult != "file written" {
		t.Fatalf("ToolResult = %q, want %q", events[1].ToolResult, "file written")
	}
	if events[1].ToolDuration != 2*time.Second {
		t.Fatalf("ToolDuration = %v, want 2s", events[1].ToolDuration)
	}
}

// TestReadCodexTranscriptEventsToolEndSpansPolls proves duration measurement
// survives across two SEPARATE calls sharing one pending map — the begin row
// and the end row can land in different polling cycles, since Codex only
// writes the end once the call actually finishes.
func TestReadCodexTranscriptEventsToolEndSpansPolls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	beginTS := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	endTS := turnStart.Add(6 * time.Second).Format(time.RFC3339Nano)

	pending := map[string]time.Time{}

	appendLine(t, path, codexMCPCallBegin(beginTS, "echo_contract", "c1"))
	events, offset, err := readCodexTranscriptEventsFromFile(path, 0, turnStart, pending)
	if err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	if len(events) != 1 || events[0].IsToolEnd {
		t.Fatalf("poll 1: got %+v, want a single start", events)
	}
	if len(pending) != 1 {
		t.Fatalf("poll 1: pending map = %+v, want 1 entry", pending)
	}

	appendLine(t, path, codexMCPCallEnd(endTS, "c1"))
	events, _, err = readCodexTranscriptEventsFromFile(path, offset, turnStart, pending)
	if err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	if len(events) != 1 || !events[0].IsToolEnd {
		t.Fatalf("poll 2: got %+v, want a single end", events)
	}
	if events[0].ToolDuration != 5*time.Second {
		t.Fatalf("poll 2: ToolDuration = %v, want 5s", events[0].ToolDuration)
	}
	if len(pending) != 0 {
		t.Fatalf("poll 2: pending map did not drain: %+v", pending)
	}
}

// TestCodexTranscriptStreamStatePollWorksWithBackgroundContext (PLAT-160).
//
// waitForCodexInteractiveResponse's ctx.Done() branch now calls poll once
// more with context.Background() before returning, mirroring cursorcli's
// transcript tailer, so a tool call's rollout row written in the gap between
// the last tick and cancellation is not lost. This proves that final call
// actually delivers content on a channel, using the exact context shape it
// runs with (a background context, not the already-cancelled turn ctx).
func TestCodexTranscriptStreamStatePollWorksWithBackgroundContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	// Written before poll is ever called, standing in for a tool call whose
	// row landed on disk right as the turn's context was being cancelled.
	appendLine(t, path, codexMCPCallBegin(ts, "echo_contract", "c1"))

	state := newCodexTranscriptStreamState(turnStart, "", func(time.Time) string { return path })
	ch := make(chan llmtypes.StreamChunk, 8)
	state.poll(context.Background(), ch)

	select {
	case c := <-ch:
		if c.Type != llmtypes.StreamChunkTypeToolCallStart || c.ToolName != "echo_contract" {
			t.Fatalf("chunk = %+v, want ToolCallStart echo_contract", c)
		}
	default:
		t.Fatal("poll with context.Background() did not deliver the tool-call chunk already on disk")
	}
}
