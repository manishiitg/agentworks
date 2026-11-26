package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2942 (owner, 17 Nov): each person's PROJECT_INSTRUCTIONS.md stays theirs like memory; a teammate's agent rewrote
// the rules every agent read when it was always shared. Team rules travel as a Team Todo in a shared folder.
func TestCrewTeamSharesSetupButKeepsInstructionsAndMemoryPersonal(t *testing.T) {
	for _, folder := range []crewTeamFolder{{Folders: []string{"team"}}, {Folders: []string{"memory"}}} {
		for _, path := range []string{crewTeamSetupFile, crewTeamInstructionsFile, "skills/example/SKILL.md"} {
			if !folder.holds(path) || !crewTeamAllowed(path) {
				t.Fatalf("shared setup refused for %+v: %s", folder, path)
			}
		}
		for _, path := range []string{"PROJECT_INSTRUCTIONS.md", "MEMORY.md", "memory", "memory/notes.md"} {
			if folder.holds(path) {
				t.Fatalf("personal file shared for %+v: %s", folder, path)
			}
		}
	}
	if !(crewTeamFolder{Folders: []string{"team"}}).holds("team/team-rules.md") {
		t.Fatal("a Team rules file in a shared folder must sync")
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
		{"memory/notes.md", false}, {"PROJECT_INSTRUCTIONS.md", false},
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

// #3041, owner 18 Nov: every Team member may change the Team instructions, like any other Team file. A member's save
// commits and shows them as its author. Server prompts read the committed version only.
func TestCrewTeamInstructionsAnyMemberSaves(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: "rules-test", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}, Folder: crewTeamFolder{ID: "f1", Folders: []string{"team"}}}); err != nil {
		t.Fatal(err)
	}
	store, err := crewTeamChunkStore("rules-test")
	if err != nil {
		t.Fatal(err)
	}
	put := func(content string) *chunkedFile {
		t.Helper()
		v, err := chunkReader(strings.NewReader(content), int64(len(content)), false, func(id string, chunk []byte) error { return putChunk(store, id, chunk) })
		if err != nil {
			t.Fatal(err)
		}
		return &v
	}
	save := func(actor, transfer, path, content string) error {
		_, err := commitCrewTeamChanges(t.Context(), "rules-test", actor, transfer, crewTransferDigest(transfer), []crewTeamChange{{Path: path, After: put(content)}})
		return err
	}
	if err := save("member", "member-rules", crewTeamInstructionsFile, "a teammate's rules"); err != nil {
		t.Fatalf("a teammate could not change the Team instructions: %v", err)
	}
	if text, found := committedTeamInstructions("rules-test"); !found || text != "a teammate's rules" {
		t.Fatalf("prompts would not read the teammate's committed rules: %q %v", text, found)
	}
	state, err := readCrewTeamCurrentState("rules-test")
	if err != nil {
		t.Fatal(err)
	}
	if v := state.Versions[crewTeamInstructionsFile]; v.Author != "member" {
		t.Fatalf("the Team instructions do not show the member as author: %+v", v)
	}
}
