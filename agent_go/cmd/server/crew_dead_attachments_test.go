package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// A deleted crew or workflow left in a crew's saved attachments is skipped for
// the turn instead of failing it (server A 2026-09-27: deleting new-project made
// every call into the SDE crew answer 403). Existing attachments still load;
// the strict check used by attach/secrets still refuses a missing path.
func TestTurnSkipsAttachmentsThatNoLongerExist(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"alice","username":"alice","role":"creator","products":["work","agentworks"]}]}`)
	workspace := &mockWorkspaceAPI{files: map[string]string{
		"_users/alice/Chats/Work/projects/live/product.json": `{"schema_version":1,"product":"work","id":"live","title":"Live"}`,
		"_users/alice/Chats/Work/projects/gone/.keep":        "",
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice", Username: "alice"})

	req := &QueryRequest{WorkflowContextPaths: []string{"Chats/Work/projects/gone", "Chats/Work/projects/live", "Workflow/missing"}}
	if err := admitTurnContextPaths(ctx, req); err != nil {
		t.Fatalf("a missing attachment failed the turn: %v", err)
	}
	if !reflect.DeepEqual(req.WorkflowContextPaths, []string{"Chats/Work/projects/live"}) {
		t.Fatalf("kept attachments = %v", req.WorkflowContextPaths)
	}
	if _, _, err := authorizeWorkflowContextPathsWithReadRoots(ctx, []string{"Chats/Work/projects/gone"}); err == nil {
		t.Fatal("the strict check must still refuse a missing crew")
	}
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
