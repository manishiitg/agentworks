package server

import (
	"os"
	"path/filepath"
	"testing"
)

// The team copy gets the agent's own record only (case B3: never the rest of the Mac): a record cannot write or
// remove anything else in the Crew's online folder, nor write through a link.
func TestTeamCopyRecordTouchesOnlyTheAgentsRecord(t *testing.T) {
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
		if err := saveTeamCopyRecord(base, crewTeamCopyRecord{Files: map[string][]byte{bad: []byte("x")}}); err == nil {
			t.Fatalf("record path %q was accepted", bad)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "learnings")); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamCopyRecord(base, crewTeamCopyRecord{Files: map[string][]byte{"learnings/x.md": []byte("x")}}); err == nil {
		t.Fatal("wrote through a link")
	}
	os.Remove(filepath.Join(base, "learnings"))
	record := crewTeamCopyRecord{Files: map[string][]byte{"MEMORY.md": []byte("new memory"), "skills/new/SKILL.md": []byte("skill")}, Chats: map[string]string{"main": "chat so far"}}
	if err := saveTeamCopyRecord(base, record); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"MEMORY.md": "new memory", "skills/new/SKILL.md": "skill", "code/app.go": "project file", teamCopyChatDir + "/main.md": "chat so far"} {
		if got, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(rel))); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", rel, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "skills/old/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("a skill removed on the Mac stayed in the team copy")
	}
}
