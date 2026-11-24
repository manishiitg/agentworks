package claudecode

import (
	"strings"
	"testing"
)

// The Context pane opens each part of a Claude Code chat to its items; a compaction drops earlier messages but
// keeps the system prompt and tools.
func TestContextTextsListEachPartsItemsSinceTheLastCompaction(t *testing.T) {
	transcript := strings.Join([]string{
		`{"type":"attachment","attachment":{"type":"skill_listing","content":"- old: dropped by the summary"},"rendered":[{"content":"- old: dropped by the summary"}]}`,
		`{"type":"system","subtype":"compact_boundary"}`,
		`{"type":"attachment","attachment":{"type":"prompt_snapshot","systemPrompt":["Be helpful.","Project rules."],"tools":[{"name":"Read","description":"Reads a file.","schema":{"type":"object"}}]}}`,
		`{"type":"attachment","attachment":{"type":"skill_listing","content":"- deploy: Ship it\n  carefully\n- review: Check work"},"rendered":[{"content":"x"}]}`,
		`{"type":"attachment","attachment":{"type":"nested_memory","path":"/work/app/AGENTS.md","content":"Use worktrees."},"rendered":[{"content":"Use worktrees."}]}`,
		`{"type":"attachment","attachment":{"type":"date"},"rendered":[{"content":"Today is Monday."}]}`,
		`{"type":"attachment","attachment":{"type":"date"},"rendered":[{"content":"Today is Tuesday."}]}`,
	}, "\n")
	parts := contextTexts(strings.NewReader(transcript), "/work/app")
	setup := parts[ContextPartSetup]
	if len(setup) != 2 || setup[0].Text != "Be helpful.\n\nProject rules." || setup[1].Label != "Tool: Read" || !strings.Contains(setup[1].Text, "Inputs:") {
		t.Fatalf("setup = %+v", setup)
	}
	skills := parts[ContextPartSkills]
	if len(skills) != 2 || skills[0].Label != "deploy" || !strings.Contains(skills[0].Text, "carefully") {
		t.Fatalf("skills = %+v (the skill listed before the compaction must be gone)", skills)
	}
	if files := parts[ContextPartFiles]; len(files) != 1 || files[0].Label != "AGENTS.md" {
		t.Fatalf("files = %+v", files)
	}
	if notes := parts[ContextPartNotes]; len(notes) != 1 || notes[0].Count != 2 || notes[0].Text != "Today is Tuesday." {
		t.Fatalf("notes = %+v (copies add up, latest text shown)", notes)
	}
}
