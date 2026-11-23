package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompletedAssistantResponseFromTranscriptUsesOnlyCommittedEndTurn(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	const sessionID = "9b6a291d-04e1-41d3-b64a-fb70293db03a"
	projectDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	turnStart := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	before := turnStart.Add(-time.Second).Format(time.RFC3339Nano)
	after := turnStart.Add(time.Second).Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"assistant","timestamp":"` + before + `","message":{"id":"old","stop_reason":"end_turn","content":[{"type":"text","text":"prior turn"}]}}`,
		`{"type":"assistant","timestamp":"` + after + `","message":{"id":"tool-call","stop_reason":"tool_use","content":[{"type":"text","text":"Running deeper technical checks now."}]}}`,
		`{"type":"assistant","timestamp":"` + after + `","message":{"id":"tool-call","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool-1","name":"api-bridge","input":{}}]}}`,
		`{"type":"assistant","timestamp":"` + after + `","message":{"id":"final","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"done"}]}}`,
		`{"type":"assistant","timestamp":"` + after + `","message":{"id":"final","stop_reason":"end_turn","content":[{"type":"text","text":"The quality check finished.\n\n**Result: ready.**"}]}}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	want := "The quality check finished.\n\n**Result: ready.**"
	got := completedAssistantResponseFromTranscript(sessionID, "", turnStart)
	if !got.Found || !got.Completed || got.Text != want {
		t.Fatalf("completed response = %+v, want found completed text %q", got, want)
	}
	// The narration Claude wrote before its tool call is part of the turn as a
	// reader sees it, and must not include the previous turn.
	wantTurn := "Running deeper technical checks now.\n\n" + want
	if got.TurnText != wantTurn {
		t.Fatalf("turn text = %q, want %q", got.TurnText, wantTurn)
	}
}

func TestCompletedAssistantResponseFromTranscriptRejectsInterruptedTurn(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	const sessionID = "7ea267a7-9e91-4167-89ad-071ef14345bd"
	projectDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	ts := time.Now().UTC()
	line := `{"type":"assistant","timestamp":"` + ts.Format(time.RFC3339Nano) + `","message":{"id":"active","stop_reason":"tool_use","content":[{"type":"text","text":"Running deeper technical checks now."}]}}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	got := completedAssistantResponseFromTranscript(sessionID, "", ts.Add(-time.Second))
	if !got.Found || got.Completed || got.Text != "" {
		t.Fatalf("interrupted response = %+v, want found but incomplete without end_turn", got)
	}
}

// RTS 2026-09-24: a tool call ran past 120s, Claude moved it to the background
// and ended its turn, then continued on its own when the task finished. The
// turn is not over until that task's notification arrives and Claude answers
// again, so the chat keeps streaming the continuation.
func TestBackgroundedToolTaskKeepsTurnOpen(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	const sessionID = "08e5b6a5-0770-472e-93dc-53dabb23225e"
	projectDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	turnStart := time.Date(2026, 9, 24, 15, 54, 0, 0, time.UTC)
	at := func(sec int) string { return turnStart.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano) }
	lines := []string{
		`{"type":"assistant","timestamp":"` + at(1) + `","message":{"id":"poll","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool-1","name":"mcp__api-bridge__execute_shell_command","input":{}}]}}`,
		`{"type":"user","timestamp":"` + at(121) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":[{"type":"text","text":"MCP tool \"api-bridge/execute_shell_command\" is still running after 120s. It was moved to the background as task ks06egn4s. You will be notified when it completes."}]}]}}`,
		`{"type":"assistant","timestamp":"` + at(125) + `","message":{"id":"interim","stop_reason":"end_turn","content":[{"type":"text","text":"Deploy is running in the background."}]}}`,
		`{"type":"system","subtype":"turn_duration","timestamp":"` + at(125) + `","pendingBackgroundAgentCount":0}`,
	}
	write := func() {
		if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	got := completedAssistantResponseFromTranscript(sessionID, "", turnStart)
	if got.LastIsEndTurn || got.PendingBackgroundTasks != 1 {
		t.Fatalf("interim answer ended the turn: last=%v pending=%d", got.LastIsEndTurn, got.PendingBackgroundTasks)
	}
	lines = append(lines,
		`{"type":"user","timestamp":"`+at(160)+`","message":{"role":"user","content":"<task-notification>\n<task-id>ks06egn4s</task-id>\n<status>completed</status>\n<summary>MCP task ks06egn4 (api-bridge/execute_shell_command) completed.</summary>\n</task-notification>"}}`,
		`{"type":"assistant","timestamp":"`+at(170)+`","message":{"id":"final","stop_reason":"end_turn","content":[{"type":"text","text":"Deploy succeeded."}]}}`,
	)
	write()
	got = completedAssistantResponseFromTranscript(sessionID, "", turnStart)
	if !got.LastIsEndTurn || got.PendingBackgroundTasks != 0 || got.Text != "Deploy succeeded." {
		t.Fatalf("after the notification: last=%v pending=%d text=%q", got.LastIsEndTurn, got.PendingBackgroundTasks, got.Text)
	}
	if !strings.Contains(got.TurnText, "Deploy is running in the background.") {
		t.Fatalf("turn text must keep the interim message: %q", got.TurnText)
	}
}
