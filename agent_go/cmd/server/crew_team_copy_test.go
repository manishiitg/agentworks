package server

import (
	"os"
	"path/filepath"
	"testing"
)

// The team copy gets the agent's own record only (case B3: never the rest of the Mac): a record cannot write or
// remove anything else in the Crew's online folder, nor write through a link. Each agent's chat with its owner stays
// out of that folder and is read for its own agent only (reviewer, 4 Nov: A's people must not read the chat with B).
func TestTeamCopyRecordTouchesOnlyTheAgentsRecord(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	base := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(base, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("code/app.go", "project file")
	write("skills/old/SKILL.md", "removed on the Mac")
	write("MEMORY.md", "old memory")
	for _, bad := range []string{"code/app.go", "../outside.md", "skills", ".env", "db/crew.db"} {
		if err := saveTeamCopyRecord(base, "crew-1", crewTeamCopyRecord{Files: map[string][]byte{bad: []byte("x")}}); err == nil {
			t.Fatalf("record path %q was accepted", bad)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "learnings")); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamCopyRecord(base, "crew-1", crewTeamCopyRecord{Files: map[string][]byte{"learnings/x.md": []byte("x")}}); err == nil {
		t.Fatal("wrote through a link")
	}
	os.Remove(filepath.Join(base, "learnings"))
	record := crewTeamCopyRecord{Files: map[string][]byte{"MEMORY.md": []byte("new memory"), "skills/new/SKILL.md": []byte("skill")},
		Chats: map[string]string{"main": "chat with main", "chat_b": "chat with b"}, Programs: map[string]teamCopyProgram{"main": {Provider: "claude-code"}}}
	if err := saveTeamCopyRecord(base, "crew-1", record); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"MEMORY.md": "new memory", "skills/new/SKILL.md": "skill", "code/app.go": "project file"} {
		if got, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(rel))); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", rel, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "skills/old/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("a skill removed on the Mac stayed in the team copy")
	}
	if _, err := os.Stat(filepath.Join(base, ".team-copy")); !os.IsNotExist(err) {
		t.Fatal("owner chats were written into the Crew's folder, where any shared agent's turn can read them")
	}
	if main, ok := readTeamCopyAgent("crew-1", "main"); !ok || main.Chat != "chat with main" || main.Provider != "claude-code" {
		t.Fatalf("main's part = %+v, %v", main, ok)
	}
	if b, _ := readTeamCopyAgent("crew-1", "chat_b"); b.Chat != "chat with b" {
		t.Fatalf("b's part = %+v", b)
	}
}
