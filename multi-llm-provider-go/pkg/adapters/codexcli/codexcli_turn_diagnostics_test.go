package codexcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnoseTurnCompletionFindsTaskCompleteAfterSince(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "08", "16")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	workingDir := filepath.Join(t.TempDir(), "social-media")
	since := time.Now().UTC().Add(-time.Minute)
	completionTime := since.Add(2 * time.Minute).Format(time.RFC3339Nano)

	rollout := filepath.Join(dayDir, "rollout-2026-08-16T19-09-01-01a00acc-124d-77f3-a00a-785a7da4a905.jsonl")
	lines := []string{
		fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"Completed the migration and stamped workflow contract version 1.0.26."}}`, completionTime),
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	found, completedAt, message := DiagnoseTurnCompletion(workingDir, since)
	if !found {
		t.Fatal("DiagnoseTurnCompletion did not find the task_complete event")
	}
	if !strings.Contains(message, "Completed the migration") {
		t.Fatalf("message = %q, want it to contain the last agent message", message)
	}
	wantTime, _ := time.Parse(time.RFC3339Nano, completionTime)
	if !completedAt.Equal(wantTime) {
		t.Fatalf("completedAt = %v, want %v", completedAt, wantTime)
	}
}

func TestDiagnoseTurnCompletionIgnoresEventsBeforeSince(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	dayDir := filepath.Join(home, ".codex", "sessions", "2026", "08", "16")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	workingDir := filepath.Join(t.TempDir(), "social-media")
	since := time.Now().UTC()
	staleCompletion := since.Add(-time.Hour).Format(time.RFC3339Nano)

	rollout := filepath.Join(dayDir, "rollout-2026-08-16T10-00-00-11111111-1111-4111-8111-111111111111.jsonl")
	lines := []string{
		fmt.Sprintf(`{"type":"session_meta","payload":{"cwd":%q}}`, workingDir),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"stale turn from an hour ago"}}`, staleCompletion),
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	found, _, _ := DiagnoseTurnCompletion(workingDir, since)
	if found {
		t.Fatal("DiagnoseTurnCompletion must not report a task_complete that predates `since`")
	}
}

func TestDiagnoseTurnCompletionNoRolloutFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	found, _, _ := DiagnoseTurnCompletion(filepath.Join(t.TempDir(), "no-such-workflow"), time.Now())
	if found {
		t.Fatal("DiagnoseTurnCompletion must report not-found when no rollout exists")
	}
}
