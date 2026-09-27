package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// Only an owner's Workshop session may write workflow.json; editors (and
// anyone below owner) get it write-blocked, legacy workflows are unchanged.
func TestWorkflowManifestBlockedWriteForNonOwner(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"alice","username":"alice","role":"creator"},
		{"id":"bob","username":"bob","role":"editor","products":["agentworks"]}]}`)
	shared, _ := json.Marshal(WorkflowManifest{ID: "shared", Label: "Shared", Access: &WorkflowAccess{Owners: []string{"alice"}, Editors: []string{"bob"}}})
	workspace := &mockWorkspaceAPI{files: map[string]string{manifestPath("Workflow/shared"): string(shared)}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	ctx := context.Background()
	if got := workflowManifestBlockedWriteForNonOwner(ctx, &UserClaims{UserID: "bob", Username: "bob"}, "Workflow/shared/"); got != "Workflow/shared/workflow.json" {
		t.Fatalf("editor: %q", got)
	}
	if got := workflowManifestBlockedWriteForNonOwner(ctx, &UserClaims{UserID: "alice", Username: "alice"}, "Workflow/shared"); got != "" {
		t.Fatalf("owner must keep write: %q", got)
	}
	if got := workflowManifestBlockedWriteForNonOwner(ctx, &UserClaims{UserID: "bob", Username: "bob"}, "Workflow/missing"); got != "" {
		t.Fatalf("no workflow yet: %q", got)
	}
}
