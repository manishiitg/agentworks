package server

import (
	"os"
	"path/filepath"
	"testing"
)

// Two Teams named alike once shared one default folder: the second join was refused after the server recorded the
// member's Mac, so their agents never reached that Team (#2809).
func TestCrewTeamDefaultJoinFolderSkipsAnotherTeamsCrew(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	first, err := crewTeamDefaultJoinFolder("ai-work")
	if err != nil || first != filepath.Join(home, "AgentWorks", "ai-work") {
		t.Fatalf("first folder = %q, %v", first, err)
	}
	if err = os.MkdirAll(filepath.Join(first, ".agentworks"), 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := crewTeamDefaultJoinFolder("ai-work")
	if err != nil || second != filepath.Join(home, "AgentWorks", "ai-work (2)") {
		t.Fatalf("second folder = %q, %v", second, err)
	}
}
