package server

import (
	"strings"
	"testing"
)

// Owner, 24 Nov: without a parent purpose the fork note had no closing bracket, so the chat showed it as the
// person's own message.
func TestForkFirstTurnNoteIsAlwaysBracketed(t *testing.T) {
	for i, purpose := range []string{"", "Reviews tools"} {
		note := forkFirstTurnNote("Parent", "Fork", purpose, i == 0)
		if !strings.HasPrefix(note, "[This chat is a fork.") || !strings.HasSuffix(note, "]") || strings.Count(note, "]") != 1 {
			t.Fatalf("purpose %q: note is not one bracketed block: %q", purpose, note)
		}
		if purpose != "" && !strings.Contains(note, "The parent's job was: "+purpose) {
			t.Fatalf("purpose missing: %q", note)
		}
	}
}

func TestForkCrossProjectFirstTurnNoteIsAlwaysBracketed(t *testing.T) {
	for _, purpose := range []string{"", "Tracks [Q4] budgets"} {
		note := forkCrossProjectFirstTurnNote(
			"Planner [old]",
			"Planner (fork)",
			purpose,
			"Source [Proj]",
			"/Users/example/source",
			"Dest [Proj]",
			"/Users/example/dest",
			"source-123",
		)
		if !strings.HasPrefix(note, "[This chat is a fork moved from another project.") || !strings.HasSuffix(note, "]") || strings.Count(note, "]") != 1 {
			t.Fatalf("purpose %q: cross-project note is not one bracketed block: %q", purpose, note)
		}
		for _, want := range []string{"/Users/example/source", "/Users/example/dest", "Source (Proj)", "Dest (Proj)", "workspace action=references_list", "copy_from=", "from_project=", "database action=describe/query", "by project name, never a raw ID"} {
			if !strings.Contains(note, want) {
				t.Fatalf("cross-project note missing %q: %q", want, note)
			}
		}
		if strings.Contains(note, "#source-123") {
			t.Fatalf("cross-project note must not tell the agent to ask for a raw project ID tag: %q", note)
		}
		if purpose != "" && !strings.Contains(note, "The parent's job in Source (Proj) was: Tracks (Q4) budgets") {
			t.Fatalf("purpose missing or unescaped: %q", note)
		}
	}
}
