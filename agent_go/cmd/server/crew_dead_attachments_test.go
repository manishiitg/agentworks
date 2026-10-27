package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Saved unavailable references are omitted without restoring retired routes.
// New attachments and malformed current manifests remain strict.
func TestTurnSkipsAttachmentsThatNoLongerExist(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	ctx := context.WithValue(context.Background(), UserContextKey, crewWriter("owner"))
	stale := []string{"Chats/Work/projects/deleted", "_users/owner/Chats/Code/projects/deleted", "Crew/missing", "Workflow/missing"}
	req := &QueryRequest{WorkflowContextPaths: append(stale, externalCommandCrewRoot)}
	if err := admitTurnContextPaths(ctx, req); err != nil {
		t.Fatalf("a stale attachment failed the turn: %v", err)
	}
	if !reflect.DeepEqual(req.WorkflowContextPaths, []string{externalCommandCrewRoot}) || !reflect.DeepEqual(req.authorizedWorkflowContextReadPaths, []string{externalCommandCrewRoot}) || req.omittedWorkflowContextCount != len(stale) {
		t.Fatalf("turn context=%v reads=%v omitted=%d", req.WorkflowContextPaths, req.authorizedWorkflowContextReadPaths, req.omittedWorkflowContextCount)
	}
	for _, path := range stale {
		if _, _, err := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{path}); err == nil {
			t.Fatalf("strict attachment accepted %q", path)
		}
	}
	f.write(t, externalCommandCrewRoot+"/product.json", "{malformed")
	if _, _, err := authorizeContextPathsWithReadRoots(ctx, []string{externalCommandCrewRoot}, true); err == nil {
		t.Fatal("malformed current manifest was accepted")
	}
}

func TestExternalCrewDetachesSavedUnavailableReferences(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	stale := []string{"Chats/Work/projects/deleted", "_users/owner/Chats/Code/projects/deleted", "Crew/missing"}
	raw, _ := json.Marshal(map[string]any{"id": "team", "workflow_context_paths": append(stale, "Workflow/keep"), "custom": "preserved"})
	f.write(t, externalCommandCrewState+"/workflow.json", string(raw))
	// This removes only an exact saved entry in the caller's owned Crew.
	crewCapabilityHTTP(t, f, crewWriter("other"), "workspace", map[string]any{"action": "reference_detach", "workspace_path": stale[0]}, 403)
	for _, path := range stale {
		crewCapabilityHTTP(t, f, crewWriter("owner"), "workspace", map[string]any{"action": "reference_detach", "workspace_path": path}, 200)
	}
	var saved struct {
		Paths  []string `json:"workflow_context_paths"`
		Custom string   `json:"custom"`
	}
	if err := json.Unmarshal([]byte(f.read(t, externalCommandCrewState+"/workflow.json")), &saved); err != nil || !reflect.DeepEqual(saved.Paths, []string{"Workflow/keep"}) || saved.Custom != "preserved" {
		t.Fatalf("saved paths=%v custom=%q err=%v", saved.Paths, saved.Custom, err)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "workspace", map[string]any{"action": "reference_detach", "workspace_path": stale[0]}, 400)
}

// Deleting a crew removes it from the owner's crews and workflows (logical or
// physical path) and from other owners' crews (physical path only); another
// user's own crew of the same name is left alone.
func TestPruneDeletedCrewReferences(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"alice","username":"alice","role":"creator","products":["work"]},
		{"id":"bob","username":"bob","role":"creator","products":["work"]}]}`)
	manifest := func(paths ...string) string {
		raw, _ := json.Marshal(map[string]interface{}{"id": "x", "workflow_context_paths": paths, "capabilities": map[string]interface{}{"selected_skills": []string{"work-dashboard"}}})
		return string(raw)
	}
	workflow, _ := json.Marshal(WorkflowManifest{ID: "w", Label: "W", CreatedBy: "alice", WorkflowContextPaths: []string{"Chats/Work/projects/gone", "Workflow/other"}})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		"_users/alice/Chats/Work/projects/sde/workflow.json": manifest("Chats/Work/projects/gone", "Chats/Work/projects/keep"),
		"_users/bob/Chats/Work/projects/qa/workflow.json":    manifest("_users/alice/Chats/Work/projects/gone", "Chats/Work/projects/gone"),
		manifestPath("Workflow/w"):                           string(workflow),
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	pruneDeletedCrewReferences(context.Background(), "alice", "_users/alice/Chats/Work/projects/gone")

	paths := func(file string) []string {
		var parsed struct {
			Paths []string `json:"workflow_context_paths"`
		}
		_ = json.Unmarshal([]byte(workspace.files[file]), &parsed)
		return parsed.Paths
	}
	if got := paths("_users/alice/Chats/Work/projects/sde/workflow.json"); !reflect.DeepEqual(got, []string{"Chats/Work/projects/keep"}) {
		t.Fatalf("owner's crew: %v", got)
	}
	if got := paths("_users/bob/Chats/Work/projects/qa/workflow.json"); !reflect.DeepEqual(got, []string{"Chats/Work/projects/gone"}) {
		t.Fatalf("other owner's crew (their own 'gone' must stay): %v", got)
	}
	if !strings.Contains(workspace.files["_users/alice/Chats/Work/projects/sde/workflow.json"], "work-dashboard") {
		t.Fatal("rewriting the crew manifest dropped its other fields")
	}
	if got := paths(manifestPath("Workflow/w")); strings.Contains(strings.Join(got, ","), "projects/gone") {
		t.Fatalf("owner's workflow still attaches the deleted crew: %v", got)
	}
}
