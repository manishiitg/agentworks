package codexcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestReadCodexTranscriptMessagesShapesAToolLoop verifies the full
// codex rollout → llmtypes mapping for the common turn shape:
// assistant text → function_call → function_call_output → final
// assistant text. Confirms that:
//  1. response_item:message rows become AI MessageContent with
//     TextContent parts (output_text blocks);
//  2. response_item:function_call becomes AI with ToolCall;
//  3. response_item:function_call_output becomes Tool with
//     ToolCallResponse linked by call_id;
//  4. response_item:reasoning is skipped (encrypted, no usable text);
//  5. response_item:message with role=user/developer is skipped
//     (already in outer history).
func TestReadCodexTranscriptMessagesShapesAToolLoop(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	const cwd = "/tmp/test-codex-workdir"
	sessionsDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "05", "23")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rollout := filepath.Join(sessionsDir, "rollout-2026-05-23T12-00-00-deadbeef-0000-0000-0000-000000000001.jsonl")

	// turnStart must be in the past relative to wall-clock "now"
	// since the parser's mtime cutoff is turnStart-30s and the test
	// fixture's mtime defaults to now. An hour ago is safe.
	turnStart := time.Now().Add(-1 * time.Hour)
	ts := turnStart.Add(1 * time.Second).Format(time.RFC3339Nano)
	beforeTurn := turnStart.Add(-30 * time.Minute).Format(time.RFC3339Nano)

	lines := []string{
		// session_meta carries cwd — used to scope file selection.
		`{"timestamp":"` + ts + `","type":"session_meta","payload":{"id":"sess-1","cwd":"` + cwd + `"}}`,
		// developer/system message — must be skipped.
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"you are codex"}]}}`,
		// user message — must be skipped (already in outer history).
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"edit foo.go"}]}}`,
		// row from a PRIOR turn — must be skipped via turnStart filter.
		`{"timestamp":"` + beforeTurn + `","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"old-turn text"}]}}`,
		// reasoning — encrypted, must be skipped.
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"reasoning","encrypted_content":"AAA"}}`,
		// assistant text (commentary).
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Reading the file."}]}}`,
		// function call.
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"cat foo.go\"}","call_id":"call_A"}}`,
		// function output.
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"function_call_output","call_id":"call_A","output":"package main\nfunc Foo() {}\n"}}`,
		// custom tool (apply_patch — raw string input, no JSON).
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\nfoo\n*** End Patch","call_id":"call_B"}}`,
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_B","output":"applied"}}`,
		// final assistant text.
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}}`,
		// trailing event_msg telemetry — must be ignored (not response_item).
		`{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Done."}}`,
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	// Walk uses mtime; nudge to be safely > turnStart-30s.
	now := time.Now()
	if err := os.Chtimes(rollout, now, now); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	msgs := readCodexTranscriptMessages(turnStart, cwd)
	if len(msgs) != 6 {
		t.Fatalf("got %d messages, want 6 (text + tool_call + tool_output + custom_call + custom_output + final_text); msgs=%+v", len(msgs), msgs)
	}

	// [0] assistant text
	if msgs[0].Role != llmtypes.ChatMessageTypeAI {
		t.Fatalf("msgs[0].Role = %q, want AI", msgs[0].Role)
	}
	if tc, ok := msgs[0].Parts[0].(llmtypes.TextContent); !ok || tc.Text != "Reading the file." {
		t.Fatalf("msgs[0].Parts[0] = %+v, want TextContent 'Reading the file.'", msgs[0].Parts[0])
	}

	// [1] function call
	tcall, ok := msgs[1].Parts[0].(llmtypes.ToolCall)
	if !ok {
		t.Fatalf("msgs[1].Parts[0] = %T, want ToolCall", msgs[1].Parts[0])
	}
	if tcall.ID != "call_A" || tcall.FunctionCall == nil || tcall.FunctionCall.Name != "exec_command" {
		t.Fatalf("ToolCall = %+v, want ID=call_A Name=exec_command", tcall)
	}
	if !strings.Contains(tcall.FunctionCall.Arguments, `"cmd":"cat foo.go"`) {
		t.Fatalf("ToolCall.Arguments = %q, want to contain cmd:cat foo.go", tcall.FunctionCall.Arguments)
	}

	// [2] function output
	if msgs[2].Role != llmtypes.ChatMessageTypeTool {
		t.Fatalf("msgs[2].Role = %q, want Tool", msgs[2].Role)
	}
	tres, ok := msgs[2].Parts[0].(llmtypes.ToolCallResponse)
	if !ok {
		t.Fatalf("msgs[2].Parts[0] = %T, want ToolCallResponse", msgs[2].Parts[0])
	}
	if tres.ToolCallID != "call_A" || !strings.Contains(tres.Content, "package main") {
		t.Fatalf("ToolCallResponse = %+v, want ToolCallID=call_A content containing 'package main'", tres)
	}

	// [3] custom tool call — apply_patch with raw string input wrapped.
	customCall, ok := msgs[3].Parts[0].(llmtypes.ToolCall)
	if !ok {
		t.Fatalf("msgs[3].Parts[0] = %T, want ToolCall", msgs[3].Parts[0])
	}
	if customCall.ID != "call_B" || customCall.FunctionCall.Name != "apply_patch" {
		t.Fatalf("custom ToolCall = %+v, want ID=call_B Name=apply_patch", customCall)
	}
	if !strings.Contains(customCall.FunctionCall.Arguments, `"input":"*** Begin Patch`) {
		t.Fatalf("custom ToolCall.Arguments = %q, want JSON-wrapped raw input", customCall.FunctionCall.Arguments)
	}

	// [4] custom tool output
	customOut, ok := msgs[4].Parts[0].(llmtypes.ToolCallResponse)
	if !ok || customOut.ToolCallID != "call_B" || customOut.Content != "applied" {
		t.Fatalf("msgs[4].Parts[0] = %+v, want ToolCallResponse{call_B, 'applied'}", msgs[4].Parts[0])
	}

	// [5] final assistant text
	if tc, ok := msgs[5].Parts[0].(llmtypes.TextContent); !ok || tc.Text != "Done." {
		t.Fatalf("msgs[5].Parts[0] = %+v, want TextContent 'Done.'", msgs[5].Parts[0])
	}
}

// TestReadCodexTranscriptMessagesScopesByCWD proves that when an
// expectedWorkingDir is supplied, we ignore rollouts whose
// session_meta.cwd doesn't match — keeping a desktop Codex session
// running elsewhere from leaking into our turn enrichment.
func TestReadCodexTranscriptMessagesScopesByCWD(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	sessionsDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "05", "23")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// turnStart must be in the past relative to wall-clock "now"
	// since the parser's mtime cutoff is turnStart-30s and the test
	// fixture's mtime defaults to now. An hour ago is safe.
	turnStart := time.Now().Add(-1 * time.Hour)
	ts := turnStart.Add(1 * time.Second).Format(time.RFC3339Nano)

	// Two rollouts. The "other" rollout is fresher (later mtime) but
	// has a different cwd, so it must be skipped.
	otherFile := filepath.Join(sessionsDir, "rollout-2026-05-23T12-00-01-deadbeef-0000-0000-0000-000000000099.jsonl")
	wantedFile := filepath.Join(sessionsDir, "rollout-2026-05-23T12-00-00-deadbeef-0000-0000-0000-000000000001.jsonl")

	otherLines := []string{
		`{"timestamp":"` + ts + `","type":"session_meta","payload":{"id":"sess-other","cwd":"/different/path"}}`,
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"leak from other session"}]}}`,
	}
	wantedLines := []string{
		`{"timestamp":"` + ts + `","type":"session_meta","payload":{"id":"sess-wanted","cwd":"/tmp/wanted-cwd"}}`,
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"correct session text"}]}}`,
	}
	if err := os.WriteFile(otherFile, []byte(strings.Join(otherLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write other: %v", err)
	}
	if err := os.WriteFile(wantedFile, []byte(strings.Join(wantedLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write wanted: %v", err)
	}
	now := time.Now()
	// other is fresher
	_ = os.Chtimes(wantedFile, now.Add(-1*time.Second), now.Add(-1*time.Second))
	_ = os.Chtimes(otherFile, now, now)

	msgs := readCodexTranscriptMessages(turnStart, "/tmp/wanted-cwd")
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1; msgs=%+v", len(msgs), msgs)
	}
	if tc, ok := msgs[0].Parts[0].(llmtypes.TextContent); !ok || tc.Text != "correct session text" {
		t.Fatalf("msgs[0] = %+v, want 'correct session text' (other-session row leaked through)", msgs[0])
	}
}

func TestReadCodexTranscriptFinalAssistantTextExcludesMCPToolTrail(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", "")

	const cwd = "/tmp/mlp-cli-session-final-result"
	sessionsDir := filepath.Join(tmpHome, ".codex", "sessions", "2026", "07", "17")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	rollout := filepath.Join(sessionsDir, "rollout-2026-07-17T09-00-00-deadbeef-0000-0000-0000-000000000777.jsonl")
	turnStart := time.Now().UTC().Add(-time.Minute)
	ts := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	final := "Finalized the catalogue entry and queued it for execution.\n\nSTATUS: COMPLETED"
	lines := []string{
		`{"timestamp":"` + ts + `","type":"session_meta","payload":{"cwd":"` + cwd + `"}}`,
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"The catalogue commit succeeded; I am validating it now."}]}}`,
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"function_call","name":"api-bridge.diff_patch_workspace_file","arguments":"{\"filepath\":\"writing_tests_summary.json\",\"diff\":\"very large patch\"}","call_id":"call_patch"}}`,
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"function_call_output","call_id":"call_patch","output":"{\"data\":{\"applied\":true}}"}}`,
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"function_call","name":"api-bridge.execute_shell_command","arguments":"{\"command\":\"jq -e . writing_tests_summary.json\"}","call_id":"call_validate"}}`,
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"function_call_output","call_id":"call_validate","output":"{\"stdout\":\"validation=passed\"}"}}`,
		`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"` + strings.ReplaceAll(final, "\n", `\n`) + `"}]}}`,
		`{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"` + strings.ReplaceAll(final, "\n", `\n`) + `"}}`,
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	got, source := readCodexTranscriptFinalAssistantText(turnStart, cwd)
	if got != final {
		t.Fatalf("final assistant text = %q, want %q", got, final)
	}
	if source != "rollout_task_complete" {
		t.Fatalf("source = %q, want rollout_task_complete", source)
	}
	for _, forbidden := range []string{"api-bridge", "diff_patch_workspace_file", "execute_shell_command", "validation=passed", "very large patch"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("final assistant text leaked MCP trail %q: %q", forbidden, got)
		}
	}

	// Some Codex releases omit last_agent_message/task_complete from a
	// recoverable terminal fallback. Only an explicitly final_answer response
	// item may be used then; the earlier commentary must still be ignored.
	if err := os.WriteFile(rollout, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("rewrite rollout without task_complete: %v", err)
	}
	got, source = readCodexTranscriptFinalAssistantText(turnStart, cwd)
	if got != final || source != "rollout_assistant_message" {
		t.Fatalf("final_answer fallback = (%q, %q), want (%q, rollout_assistant_message)", got, source, final)
	}
	// PLAT-872: a queued turn starts inside the same read window. An older
	// final/task_complete is no longer the current native turn's answer.
	queued := append(lines, `{"timestamp":"`+ts+`","type":"event_msg","payload":{"type":"task_started","turn_id":"queued-turn"}}`)
	if err := os.WriteFile(rollout, []byte(strings.Join(queued, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := readCodexRolloutFinalAssistantText(rollout, turnStart); got != "" {
		t.Fatalf("queued native turn closed on the previous answer: %q", got)
	}
	queued = append(queued, `{"timestamp":"`+ts+`","type":"event_msg","payload":{"type":"task_complete","turn_id":"queued-turn","last_agent_message":"Queued work finished."}}`)
	if err := os.WriteFile(rollout, []byte(strings.Join(queued, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := readCodexRolloutFinalAssistantText(rollout, turnStart); got != "Queued work finished." {
		t.Fatalf("latest native turn final = %q", got)
	}
}

func TestNormalizeCodexFinalAssistantTextContentBlocks(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text", in: "  STATUS: COMPLETED  ", want: "STATUS: COMPLETED"},
		{name: "text block", in: `[{"type":"text","text":"BRIDGE_OK"}]`, want: "BRIDGE_OK"},
		{name: "output text blocks", in: `[{"type":"output_text","text":"first"},{"type":"output_text","text":"second"}]`, want: "first\nsecond"},
		{name: "unknown block remains lossless", in: `[{"type":"image","text":"do-not-rewrite"}]`, want: `[{"type":"image","text":"do-not-rewrite"}]`},
		{name: "invalid json remains lossless", in: `[not-json`, want: `[not-json`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeCodexFinalAssistantText(tt.in); got != tt.want {
				t.Fatalf("normalizeCodexFinalAssistantText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
