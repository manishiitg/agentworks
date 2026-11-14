package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746-904: selected folders share the common instructions, never personal memory.
func TestCrewTeamSharedInstructionsKeepMemoryPersonal(t *testing.T) {
	for _, folder := range []crewTeamFolder{{Folders: []string{"team"}}, {Folders: []string{"memory"}}} {
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

// PLAT-746-1020: selected-folder admission cannot regain blanket root access,
// and old whole-sharing payloads must be converted rather than read as a mode.
func TestCrewTeamChosenFoldersRejectWholePayloadAndUnselectedFiles(t *testing.T) {
	var folder crewTeamFolder
	if err := json.Unmarshal([]byte(`{"path_ids_version":1,"id":"selection","whole":true}`), &folder); err == nil {
		t.Fatal("retired whole-sharing payload accepted")
	}
	if err := json.Unmarshal([]byte(`{"path_ids_version":1,"id":"selection","folders":[{"id":"folder-id","path":"team"}]}`), &folder); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		path    string
		allowed bool
	}{
		{"team/report.md", true}, {"private/report.md", false}, {"report.md", false},
		{"memory/notes.md", false}, {"PROJECT_INSTRUCTIONS.md", true},
	} {
		if got := folder.holds(item.path); got != item.allowed {
			t.Fatalf("folder admission %s: %v", item.path, got)
		}
	}
	if _, err := cleanCrewTeamFolders([]string{"memory"}, nil); err == nil {
		t.Fatal("personal memory selectable as a shared folder")
	}
	if _, err := cleanCrewTeamFolders([]string{"team-copies"}, nil); err == nil {
		t.Fatal("private member copies selectable as a shared folder")
	}
}

// PLAT-746-1020: the offline migration plan resolves the same owner-admitted
// Crew as the runtime, checks the real folder policy, and never exceeds its cap.
func TestCrewTeamFolderSelectionPlanUsesAdmittedRootAndPolicy(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	crewTeamTestDocs(t, "plan", "plan")
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "plan", OwnerID: "owner", ProjectID: "plan", Shared: true}); err != nil {
		t.Fatal(err)
	}
	roots, found, err := crewStorageRoots(crewProjectRootByID("plan"))
	if err != nil || !found {
		t.Fatal(found, err)
	}
	for _, name := range []string{"memory", "team-copies", "keys", "node_modules"} {
		if err = os.MkdirAll(filepath.Join(roots.Files, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 24; n++ {
		if err = os.MkdirAll(filepath.Join(roots.Files, fmt.Sprintf("folder-%02d", n)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := crewTeamDir("plan")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "team.json"), []byte(`{"project_id":"plan","owner_id":"owner","folder":{"whole":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = planCrewTeamFolderSelections(&output); err != nil {
		t.Fatal(err)
	}
	var plan map[string]crewTeamFolderSelectionPlan
	if err = json.Unmarshal(output.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	row := plan["plan"]
	if row.Root != "found" || row.Owner != "owner" || len(row.Folders) != crewTeamMaxFolders || row.Omitted != 4 {
		t.Fatalf("wrong conversion plan: %+v", row)
	}
	if _, err = cleanCrewTeamFolders(row.Folders, nil); err != nil {
		t.Fatal(err)
	}
}
