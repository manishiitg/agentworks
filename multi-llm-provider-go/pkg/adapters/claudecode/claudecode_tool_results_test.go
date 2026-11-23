package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTranscript(t *testing.T, rows []map[string]interface{}) (sessionID, workingDir string) {
	t.Helper()
	sessionID = "11111111-2222-3333-4444-555555555555"
	workingDir = t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", claudeTranscriptProjectSlug(workingDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, sessionID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range rows {
		body, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(body, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	return sessionID, workingDir
}

func assistantToolUse(id, name, at string) map[string]interface{} {
	return map[string]interface{}{
		"type": "assistant", "timestamp": at,
		"message": map[string]interface{}{"content": []map[string]interface{}{
			{"type": "tool_use", "id": id, "name": name},
		}},
	}
}

func userToolResult(id, text, at string) map[string]interface{} {
	return map[string]interface{}{
		"type": "user", "timestamp": at,
		"message": map[string]interface{}{"content": []map[string]interface{}{
			{"type": "tool_result", "tool_use_id": id, "content": []map[string]interface{}{{"type": "text", "text": text}}},
		}},
	}
}

// TestToolResultsCarryTheRealOutputAndDuration is why this exists.
//
// PLAT-141: a call whose transcript shows tool_use at 15:52:26.430Z and
// tool_result 41ms later produced no end event at all, so the UI showed a
// finished command as unresolved and the settle displayed 45.4s — the interval
// it had waited, not the tool's runtime. Both are here in the transcript.
func TestToolResultsCarryTheRealOutputAndDuration(t *testing.T) {
	sessionID, workingDir := writeTranscript(t, []map[string]interface{}{
		assistantToolUse("toolu_A", "execute_shell_command", "2026-08-18T15:52:26.430Z"),
		userToolResult("toolu_A", "yfinance 1.2.2\nnews items: 10", "2026-08-18T15:52:26.471Z"),
	})

	got := ToolResultsFromTranscript(sessionID, workingDir)
	entry, ok := got["toolu_A"]
	if !ok {
		t.Fatal("the completed call was not found in the transcript")
	}
	if entry.Result != "yfinance 1.2.2\nnews items: 10" {
		t.Errorf("result = %q, want the tool's real output", entry.Result)
	}
	if d := entry.Duration(); d != 41*time.Millisecond {
		t.Errorf("duration = %v, want 41ms — the real runtime, not how long we waited", d)
	}
	if entry.ToolName != "execute_shell_command" {
		t.Errorf("tool name = %q", entry.ToolName)
	}
}

// TestUnfinishedCallsAreNotReported. A tool_use with no result is a call that
// genuinely has no answer yet; returning it would let a caller present an empty
// output as the tool's own, which is the failure this work has already shipped
// twice in other forms.
func TestUnfinishedCallsAreNotReported(t *testing.T) {
	sessionID, workingDir := writeTranscript(t, []map[string]interface{}{
		assistantToolUse("toolu_done", "read_file", "2026-08-18T15:52:26.430Z"),
		userToolResult("toolu_done", "ok", "2026-08-18T15:52:26.500Z"),
		assistantToolUse("toolu_pending", "execute_shell_command", "2026-08-18T15:52:27.000Z"),
	})

	got := ToolResultsFromTranscript(sessionID, workingDir)
	if _, ok := got["toolu_pending"]; ok {
		t.Error("a call with no result was reported as complete")
	}
	if _, ok := got["toolu_done"]; !ok {
		t.Error("a completed call was dropped")
	}
}

// TestPlainStringResultsAreRead: Claude Code writes tool_result content either
// as a list of typed blocks or as a bare string.
func TestPlainStringResultsAreRead(t *testing.T) {
	sessionID, workingDir := writeTranscript(t, []map[string]interface{}{
		assistantToolUse("toolu_S", "execute_shell_command", "2026-08-18T15:52:26.430Z"),
		{"type": "user", "timestamp": "2026-08-18T15:52:26.480Z",
			"message": map[string]interface{}{"content": []map[string]interface{}{
				{"type": "tool_result", "tool_use_id": "toolu_S", "content": "plain string output"},
			}}},
	})

	if got := ToolResultsFromTranscript(sessionID, workingDir)["toolu_S"].Result; got != "plain string output" {
		t.Errorf("result = %q, want the bare-string form to be read", got)
	}
}

// TestMissingTranscriptIsNotAnError. The caller's fallback is to show what it
// already has, so an absent transcript must be silent rather than fatal.
func TestMissingTranscriptIsNotAnError(t *testing.T) {
	if got := ToolResultsFromTranscript("11111111-2222-3333-4444-555555555555", t.TempDir()); len(got) != 0 {
		t.Errorf("expected nothing from an absent transcript, got %d", len(got))
	}
	if got := ToolResultsFromTranscript("not-a-session-id", t.TempDir()); len(got) != 0 {
		t.Errorf("expected nothing for a non-transcript session id, got %d", len(got))
	}
}
