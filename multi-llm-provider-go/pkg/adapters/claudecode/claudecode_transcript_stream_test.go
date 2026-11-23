package claudecode

import (
	"os"
	"path/filepath"
	"reflect"
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

// TestReadClaudeTranscriptEventsIncremental verifies the mid-turn tailer:
//  1. rows older than turnStart are skipped (a resumed session's prior turns
//     don't replay);
//  2. assistant text → content event, tool_use → tool-call-start event;
//  3. reading from the returned offset yields ONLY newly-appended rows (no
//     re-emission);
//  4. a partial (not-yet-newline-terminated) trailing line is held back and
//     only surfaces once completed.
func TestReadClaudeTranscriptEventsIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")

	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	old := turnStart.Add(-time.Second).Format(time.RFC3339Nano)

	oldTurn := `{"type":"assistant","timestamp":"` + old + `","message":{"id":"msg_0","content":[{"type":"text","text":"PRIOR TURN"}]}}` + "\n"
	thinking := `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"msg_A","content":[{"type":"thinking","thinking":"I will inspect the requested file first."}]}}` + "\n"
	text1 := `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"msg_A","content":[{"type":"text","text":"Reading the file."}]}}` + "\n"
	tool1 := `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"msg_A","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"foo.go"}}]}}` + "\n"

	appendLine(t, path, oldTurn+thinking+text1+tool1)

	// First read from offset 0: skips the prior turn, emits reasoning, text,
	// then a tool call.
	events, off1, err := readClaudeTranscriptEventsFromFile(path, 0, turnStart, nil)
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("read 1: got %d events, want 3 (reasoning, text, tool); %+v", len(events), events)
	}
	if events[0].Reasoning != "I will inspect the requested file first." {
		t.Fatalf("read 1 events[0] = %+v, want reasoning", events[0])
	}
	if events[1].Text != "Reading the file." || events[1].ToolName != "" {
		t.Fatalf("read 1 events[1] = %+v, want text 'Reading the file.'", events[1])
	}
	if events[2].ToolName != "Read" || events[2].ToolCallID != "toolu_1" || events[2].Text != "" {
		t.Fatalf("read 1 events[2] = %+v, want tool Read/toolu_1", events[2])
	}

	// Append one complete line plus a PARTIAL line (no trailing newline yet).
	text2 := `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"msg_B","content":[{"type":"text","text":"Done."}]}}` + "\n"
	partial := `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"msg_C","content":[{"type":"text","text":"Aft`
	appendLine(t, path, text2+partial)

	events, off2, err := readClaudeTranscriptEventsFromFile(path, off1, turnStart, nil)
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if len(events) != 1 || events[0].Text != "Done." {
		t.Fatalf("read 2: got %+v, want single 'Done.' (partial line held back)", events)
	}
	if off2 <= off1 {
		t.Fatalf("read 2: offset did not advance (%d -> %d)", off1, off2)
	}

	// Complete the held-back partial line.
	rest := `er."}]}}` + "\n"
	appendLine(t, path, rest)

	events, _, err = readClaudeTranscriptEventsFromFile(path, off2, turnStart, nil)
	if err != nil {
		t.Fatalf("read 3: %v", err)
	}
	if len(events) != 1 || events[0].Text != "After." {
		t.Fatalf("read 3: got %+v, want single 'After.' (completed partial line)", events)
	}
}

func TestReadClaudeTranscriptEventsPreservesEmptyThinkingActivity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	turnStart := time.Date(2026, 9, 22, 7, 9, 0, 0, time.UTC)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	appendLine(t, path, `{"type":"assistant","timestamp":"`+ts+`","message":{"id":"msg_A","content":[{"type":"thinking","thinking":""}]}}`+"\n")

	events, _, err := readClaudeTranscriptEventsFromFile(path, 0, turnStart, nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 || !events[0].IsThinking || events[0].Reasoning != "" {
		t.Fatalf("events = %+v, want one explicit empty thinking activity event", events)
	}

	chunk := transcriptEventToChunk("session", events[0])
	if chunk.Type != llmtypes.StreamChunkTypeReasoning || chunk.Content != "" || chunk.Metadata["thinking_active"] != true {
		t.Fatalf("chunk = %+v, want empty reasoning chunk marked thinking_active", chunk)
	}
}

// TestReadClaudeTranscriptEventsInterleavedOrder proves the realistic shape a
// real turn produces — text → tool → result → text → tool → result → final
// text — streams in the correct ORDER across incremental (append-live) polls,
// and that a tool_result (user) row is emitted as the tool's end, not skipped.
//
// Before this, the tmux/interactive transport only ever emitted a tool-call
// START from the transcript: it read "assistant" rows for tool_use and never
// looked at the "user" rows carrying the result, so every tool call here had
// no result and no duration — worse than the structured-transport bug fixed
// earlier (that one only zeroed the duration; this dropped the result too).
func TestReadClaudeTranscriptEventsInterleavedOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	tsAt := func(offset time.Duration) string { return turnStart.Add(offset).Format(time.RFC3339Nano) }

	asst := func(id, ts, block string) string {
		return `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"` + id + `","content":[` + block + `]}}` + "\n"
	}
	text := func(s string) string { return `{"type":"text","text":"` + s + `"}` }
	toolUse := func(id, name string) string {
		return `{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":{}}`
	}
	toolResult := func(id, ts, result string) string {
		return `{"type":"user","timestamp":"` + ts + `","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"` + result + `"}]}}` + "\n"
	}

	rows := []string{
		asst("mA", tsAt(time.Second), text("Let me check the first file.")),
		asst("mA", tsAt(2*time.Second), toolUse("t1", "read_file")),
		toolResult("t1", tsAt(5*time.Second), "file one contents"), // 3s duration
		asst("mB", tsAt(6*time.Second), text("Now the second file.")),
		asst("mB", tsAt(7*time.Second), toolUse("t2", "read_file")),
		toolResult("t2", tsAt(9*time.Second), "file two contents"), // 2s duration
		asst("mC", tsAt(10*time.Second), text("Done. FINAL.")),
	}

	type emitted struct {
		kind     string // text | start | end
		value    string // text content, tool name, or tool result
		duration time.Duration
	}
	var got []emitted
	var offset int64
	pending := map[string]time.Time{} // shared across polls, exactly as streamClaudeTranscript does
	for _, row := range rows {        // append one row at a time, poll after each
		appendLine(t, path, row)
		events, next, err := readClaudeTranscriptEventsFromFile(path, offset, turnStart, pending)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		offset = next
		for _, e := range events {
			switch {
			case e.IsToolEnd:
				got = append(got, emitted{kind: "end", value: e.ToolResult, duration: e.ToolDuration})
			case e.ToolName != "":
				got = append(got, emitted{kind: "start", value: e.ToolName})
			default:
				got = append(got, emitted{kind: "text", value: e.Text})
			}
		}
	}

	want := []emitted{
		{kind: "text", value: "Let me check the first file."},
		{kind: "start", value: "read_file"},
		{kind: "end", value: "file one contents", duration: 3 * time.Second},
		{kind: "text", value: "Now the second file."},
		{kind: "start", value: "read_file"},
		{kind: "end", value: "file two contents", duration: 2 * time.Second},
		{kind: "text", value: "Done. FINAL."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interleaved emission order/content wrong:\n got=%+v\nwant=%+v", got, want)
	}
	if len(pending) != 0 {
		t.Fatalf("pending tool-start map not drained: %v", pending)
	}
}

// TestReadClaudeTranscriptEventsToolEndSpansPolls proves the exact shape that
// makes this hard: a tool_use row and its tool_result row can land in
// DIFFERENT polls (real tools take real time), so the pending-start map must
// survive across separate readClaudeTranscriptEventsFromFile calls for the
// duration to be measured at all.
func TestReadClaudeTranscriptEventsToolEndSpansPolls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	turnStart := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	startTS := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	endTS := turnStart.Add(4 * time.Second).Format(time.RFC3339Nano)

	pending := map[string]time.Time{}

	appendLine(t, path, `{"type":"assistant","timestamp":"`+startTS+`","message":{"id":"mA","content":[{"type":"tool_use","id":"t1","name":"read_file","input":{}}]}}`+"\n")
	events, offset, err := readClaudeTranscriptEventsFromFile(path, 0, turnStart, pending)
	if err != nil {
		t.Fatalf("read start: %v", err)
	}
	if len(events) != 1 || events[0].ToolName != "read_file" {
		t.Fatalf("expected the tool start alone, got %+v", events)
	}
	if _, ok := pending["t1"]; !ok {
		t.Fatal("tool start was not recorded in the pending map for the next poll to find")
	}

	// A later, separate poll delivers only the result.
	appendLine(t, path, `{"type":"user","timestamp":"`+endTS+`","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`+"\n")
	events, _, err = readClaudeTranscriptEventsFromFile(path, offset, turnStart, pending)
	if err != nil {
		t.Fatalf("read end: %v", err)
	}
	if len(events) != 1 || !events[0].IsToolEnd {
		t.Fatalf("expected the tool end alone, got %+v", events)
	}
	if events[0].ToolDuration != 3*time.Second {
		t.Fatalf("ToolDuration = %v, want 3s (measured across two separate polls)", events[0].ToolDuration)
	}
}
