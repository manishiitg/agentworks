package server

import "testing"

// PLAT-746-904: selected folders share the common instructions, never personal memory.
func TestCrewTeamSharedInstructionsKeepMemoryPersonal(t *testing.T) {
	for _, folder := range []crewTeamFolder{{Whole: true}, {Folders: []string{"team"}}, {Folders: []string{"memory"}}} {
		for _, path := range []string{"PROJECT_INSTRUCTIONS.md", crewTeamSetupFile, "skills/example/SKILL.md"} {
			if !folder.holds(path) || !crewTeamAllowed(path) {
				t.Fatalf("shared setup refused for %+v: %s", folder, path)
			}
		}
		for _, path := range []string{"MEMORY.md", "memory", "memory/notes.md"} {
			if folder.holds(path) {
				t.Fatalf("personal memory shared for %+v: %s", folder, path)
			}
		}
	}
	if (crewTeamFolder{}).holds("PROJECT_INSTRUCTIONS.md") {
		t.Fatal("a Crew with nothing shared must not share instructions")
	}
	if (crewTeamFolder{Folders: []string{"team"}}).holds("docs/PROJECT_INSTRUCTIONS.md") {
		t.Fatal("a nested namesake must not become automatically shared")
	}
}
