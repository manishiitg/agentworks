package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746-265: an editable executable cannot choose its owner or gain another
// Crew's grants; revoking a co-owner affects the very next access check.
func TestCrewExecutableUsesTrustedParentAndLiveAccess(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks,relays")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"builder","username":"builder","role":"creator"},{"id":"stranger","username":"stranger","role":"creator"}]}`)
	registry := defaultProjectOwners()
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "lab", ProjectID: "crew-lab", OwnerID: "owner", Shared: true, Private: true, AudienceConfigured: true, CoOwners: []string{"builder"}}); err != nil {
		t.Fatal(err)
	}
	binding := crewExecutableBinding{ID: "executable", CrewID: "crew-lab", CrewRoot: "Crew/lab", Root: "Workflow/executable", Kind: "workflow"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	files := &mockWorkspaceAPI{files: map[string]string{"Workflow/executable/workflow.json": `{"id":"executable","label":"Example","created_by":"stranger","access":{"owners":["stranger"]},"team":{"leader_id":"stranger"}}`}}
	host := httptest.NewServer(files)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	for _, spelling := range []string{"Workflow/executable", "/Workflow/executable/", "Workflow/executable/./", "Workflow/executable/code/main.py"} {
		resolved, err := findCrewExecutable(spelling)
		if err != nil || resolved == nil || resolved.ID != binding.ID {
			t.Fatalf("binding %q: %v %v", spelling, resolved, err)
		}
	}
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	if resolved, err := findCrewExecutable(filepath.Join(docs, binding.Root)); err != nil || resolved == nil {
		t.Fatalf("absolute binding: %v", err)
	}
	manifest, found, err := ReadWorkflowManifest(context.Background(), binding.Root)
	if err != nil || !found {
		t.Fatalf("read: %v", err)
	}
	if workflowOwner(binding.Root) != "owner" || manifest.CreatedBy != "owner" || manifest.Team != nil || workflowExecutionOwnerUserID(manifest) != "owner" {
		t.Fatal("manifest overrode execution owner")
	}
	for _, user := range []string{"owner", "builder", "stranger"} {
		got := workflowAccessForManifest(&UserClaims{UserID: user, Username: user}, manifest)
		want := WorkflowAccessOwner
		if user == "stranger" {
			want = WorkflowAccessNone
		}
		if got != want {
			t.Fatalf("%s access=%s, want %s", user, got, want)
		}
	}
	if err := registry.modify(func(records map[string]projectOwnerRecord) (bool, error) {
		record := records["work/lab"]
		record.CoOwners = nil
		records["work/lab"] = record
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := workflowAccessForManifest(&UserClaims{UserID: "builder", Username: "builder"}, manifest); got != WorkflowAccessNone {
		t.Fatal("revoked co-owner retained access", got)
	}
	file, _ := crewExecutableFile(binding.Root)
	if err := os.WriteFile(file, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := findCrewExecutable(binding.Root); err == nil {
		t.Fatal("corrupt binding fell back to manifest")
	}
	if other, err := findCrewExecutable("Workflow/unrelated"); err != nil || other != nil {
		t.Fatal("unrelated workflow blocked", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	files.mu.Lock()
	files.files["Workflow/executable/workflow.json"] = `{"id":"other","label":"Spoof"}`
	files.mu.Unlock()
	if _, _, err := ReadWorkflowManifest(context.Background(), binding.Root); err == nil {
		t.Fatal("accepted changed executable identity")
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator","disabled":true},{"id":"builder","username":"builder","role":"creator"}]}`)
	if got := workflowExecutionOwnerUserID(manifest); got != "" {
		t.Fatal("disabled Crew owner fell back to another execution account", got)
	}
}
