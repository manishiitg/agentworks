package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClaudeTurnEndFixture(t *testing.T, sessionID string, lines ...string) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	projectDir := filepath.Join(tmpHome, ".claude", "projects", "-tmp-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeRow(kind, ts, id, stop, text string) string {
	if kind == "user" {
		return `{"type":"user","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`
	}
	return `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"` + id + `","stop_reason":"` + stop + `","content":[{"type":"text","text":"` + text + `"}]}}`
}

// LastIsEndTurn must follow the turn's latest record, not "any end_turn":
// once Claude continues after an end_turn, the turn is not finished.
func TestClaudeTranscriptLastIsEndTurn(t *testing.T) {
	turnStart := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	before := turnStart.Add(-time.Second).Format(time.RFC3339Nano)
	at := func(s int) string { return turnStart.Add(time.Duration(s) * time.Second).Format(time.RFC3339Nano) }
	const sid = "3f1d2c4b-5a6e-4f70-8a91-b2c3d4e5f607"
	cases := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"final end_turn", []string{claudeRow("assistant", at(1), "a", "tool_use", "working"), claudeRow("user", at(2), "", "", ""), claudeRow("assistant", at(3), "b", "end_turn", "done")}, true},
		{"tool still running", []string{claudeRow("assistant", at(1), "a", "tool_use", "working")}, false},
		{"tool result not yet answered", []string{claudeRow("assistant", at(1), "a", "tool_use", "working"), claudeRow("user", at(2), "", "", "")}, false},
		{"continued after end_turn", []string{claudeRow("assistant", at(1), "a", "end_turn", "first"), claudeRow("user", at(2), "", "", ""), claudeRow("assistant", at(3), "b", "tool_use", "more")}, false},
		{"only a prior turn ended", []string{claudeRow("assistant", before, "old", "end_turn", "prior")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeClaudeTurnEndFixture(t, sid, tc.lines...)
			got := completedAssistantResponseFromTranscript(sid, "", turnStart)
			if got.LastIsEndTurn != tc.want {
				t.Fatalf("LastIsEndTurn = %v, want %v (%+v)", got.LastIsEndTurn, tc.want, got)
			}
		})
	}
}
