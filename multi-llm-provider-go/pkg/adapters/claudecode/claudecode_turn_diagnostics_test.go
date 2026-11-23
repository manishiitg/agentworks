package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnoseTurnCompletionFindsCommittedEndTurn(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	const sessionID = "9b6a291d-04e1-41d3-b64a-fb70293db03a"
	projectDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	since := time.Date(2026, 8, 16, 13, 38, 0, 0, time.UTC)
	after := since.Add(time.Minute).Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"assistant","timestamp":"` + after + `","message":{"id":"final","stop_reason":"end_turn","content":[{"type":"text","text":"Completed the migration and stamped workflow contract version 1.0.26."}]}}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	found, text := DiagnoseTurnCompletion(sessionID, "", since)
	if !found {
		t.Fatal("DiagnoseTurnCompletion did not find the committed end_turn message")
	}
	if !strings.Contains(text, "Completed the migration") {
		t.Fatalf("text = %q, want it to contain the final assistant message", text)
	}
}

func TestDiagnoseTurnCompletionRejectsInterruptedTurn(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	const sessionID = "7ea267a7-9e91-4167-89ad-071ef14345bd"
	projectDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	transcript := filepath.Join(projectDir, sessionID+".jsonl")
	since := time.Date(2026, 8, 16, 13, 38, 0, 0, time.UTC)
	after := since.Add(time.Minute).Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"assistant","timestamp":"` + after + `","message":{"id":"tool-call","stop_reason":"tool_use","content":[{"type":"text","text":"still working"}]}}`,
	}
	if err := os.WriteFile(transcript, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	found, _ := DiagnoseTurnCompletion(sessionID, "", since)
	if found {
		t.Fatal("DiagnoseTurnCompletion must not report completion for a turn with no committed end_turn")
	}
}

func TestDiagnoseTurnCompletionNoTranscript(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	found, _ := DiagnoseTurnCompletion("11111111-1111-1111-1111-111111111111", "", time.Now())
	if found {
		t.Fatal("DiagnoseTurnCompletion must report not-found when no transcript exists")
	}
}
