package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestWorkWorkflowReferenceToolsDiscoverAndPersistAuthorizedWorkflows(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"reader","username":"reader","products":[]},{"id":"owner","username":"owner","products":[]}]}`)
	// Crews are discovered from the owner registry, not from manifests found in the workspace.
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	bankingRoot := registerTestCrew(t, "banking", "reader", false)
	researchRoot := registerTestCrew(t, "research", "owner", true)
	shared, _ := json.Marshal(WorkflowManifest{
		ID: "hdfc-personal", Label: "HDFC Bank Personal Accounts",
		Access: &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"reader"}},
	})
	private, _ := json.Marshal(WorkflowManifest{
		ID: "private-bank", Label: "Private Bank",
		Access: &WorkflowAccess{Owners: []string{"owner"}},
	})
	productPath := bankingRoot + "/product.json"
	runtimePath := bankingRoot + "/workflow.json"
	researchProductPath := researchRoot + "/product.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/hdfc-personal"): string(shared),
		manifestPath("Workflow/private-bank"):  string(private),
		productPath:                            `{"schema_version":1,"product":"work","id":"banking","title":"Banking","custom":"keep","capabilities":{"selected_skills":["work-dashboard"]}}`,
		researchProductPath:                    `{"schema_version":1,"product":"work","id":"research","title":"Research","identity":{"name":"Research Crew","icon":"🔎"}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	api := &StreamingAPI{}
	registrar := &recordingRegistrar{}
	common.SetSessionFolderGuard("session-1", []string{bankingRoot}, []string{bankingRoot})
	t.Cleanup(func() { common.ClearSessionShellConfig("session-1") })
	if err := api.registerWorkWorkflowReferenceTools(registrar, "reader", "session-1", bankingRoot, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"list_accessible_workflows", "attach_workflow_reference", "detach_workflow_reference"} {
		if _, ok := registrar.tools[name]; !ok {
			t.Fatalf("tool %q was not registered", name)
		}
	}

	out, err := registrar.tools["list_accessible_workflows"].exec(context.Background(), map[string]interface{}{"query": "hdfc"})
	if err != nil || !strings.Contains(out, `"workspace_path": "Workflow/hdfc-personal"`) || !strings.Contains(out, `"name": "HDFC Bank Personal Accounts"`) || !strings.Contains(out, `"icon": "H"`) || strings.Contains(out, "Private Bank") {
		t.Fatalf("accessible search out=%s err=%v", out, err)
	}
	out, err = registrar.tools["list_accessible_workflows"].exec(context.Background(), map[string]interface{}{"query": "banking"})
	if err != nil || !strings.Contains(out, `"crews":`) || !strings.Contains(out, `"name": "Banking"`) || strings.Contains(out, `"icon": "B"`) || !strings.Contains(out, `"workspace_path": "`+bankingRoot+`"`) {
		t.Fatalf("Crew discovery out=%s err=%v", out, err)
	}
	if _, err := registrar.tools["attach_workflow_reference"].exec(context.Background(), map[string]interface{}{"workspace_path": "Workflow/private-bank"}); err == nil {
		t.Fatal("unauthorized workflow was attached")
	}
	if _, err := registrar.tools["attach_workflow_reference"].exec(context.Background(), map[string]interface{}{"workspace_path": bankingRoot}); err == nil {
		t.Fatal("Crew attached itself")
	}
	// A Crew links other Crews only: a workflow it may read is still refused (owner, 7 Nov).
	if _, err := registrar.tools["attach_workflow_reference"].exec(context.Background(), map[string]interface{}{"workspace_path": "Workflow/hdfc-personal"}); err == nil || !strings.Contains(err.Error(), "workflow cannot be attached") {
		t.Fatalf("a workflow was attached to a Crew: %v", err)
	}
	out, err = registrar.tools["attach_workflow_reference"].exec(context.Background(), map[string]interface{}{"workspace_path": researchRoot})
	if err != nil || !strings.Contains(out, "workflow references read-only") {
		t.Fatalf("attach Crew out=%s err=%v", out, err)
	}
	crewReadRoot := researchRoot
	if cfg := common.GetSessionShellConfig("session-1"); cfg == nil || !containsWorkReferencePath(cfg.ReadPaths, crewReadRoot) || containsWorkReferencePath(cfg.WritePaths, crewReadRoot) {
		t.Fatalf("attached Crew was not granted immediately as read-only: %+v", cfg)
	}
	workspace.mu.Lock()
	productSaved := workspace.files[productPath]
	saved := workspace.files[runtimePath]
	workspace.mu.Unlock()
	var productMetadata map[string]interface{}
	if json.Unmarshal([]byte(productSaved), &productMetadata) != nil || productMetadata["custom"] != "keep" || strings.Contains(productSaved, `Workflow/hdfc-personal`) {
		t.Fatalf("product metadata was changed: %s", productSaved)
	}
	if strings.Contains(saved, `Workflow/`) || !strings.Contains(saved, `"`+researchRoot+`"`) || !strings.Contains(saved, `"selected_skills"`) {
		t.Fatalf("workflow runtime manifest was not migrated and updated: %s", saved)
	}
	out, err = registrar.tools["list_accessible_workflows"].exec(context.Background(), map[string]interface{}{"query": "research"})
	if err != nil || !strings.Contains(out, `"attached": true`) || !strings.Contains(out, `"name": "Research"`) {
		t.Fatalf("attached Crew state out=%s err=%v", out, err)
	}
	// A deleted or otherwise unavailable reference must still be detachable so
	// a stale durable grant cannot trap the project in a permanently failing turn.
	workspace.mu.Lock()
	delete(workspace.files, researchProductPath)
	workspace.mu.Unlock()
	if _, err := registrar.tools["detach_workflow_reference"].exec(context.Background(), map[string]interface{}{"workspace_path": researchRoot}); err != nil {
		t.Fatalf("detach Crew: %v", err)
	}
	if cfg := common.GetSessionShellConfig("session-1"); cfg == nil || containsWorkReferencePath(cfg.ReadPaths, crewReadRoot) {
		t.Fatalf("detached Crew remained in active read guard: %+v", cfg)
	}
	paths, err := readWorkWorkflowReferences(context.Background(), bankingRoot)
	if err != nil || len(paths) != 0 {
		t.Fatalf("paths after detach=%v err=%v", paths, err)
	}
	if cfg := common.GetSessionShellConfig("session-1"); cfg == nil || containsWorkReferencePath(cfg.ReadPaths, "Workflow/hdfc-personal") {
		t.Fatalf("detached workflow remained in active read guard: %+v", cfg)
	}
}

func TestBuilderAccessibleWorkflowListUsesCurrentUserAccessWithoutCrewMutationTools(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"builder","username":"builder","products":[]},{"id":"owner","username":"owner","products":[]}]}`)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	releaseRoot := registerTestCrew(t, "release", "builder", false)
	shared, _ := json.Marshal(WorkflowManifest{
		ID: "shared", Label: "Shared workflow",
		Access: &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"builder"}},
	})
	private, _ := json.Marshal(WorkflowManifest{
		ID: "private", Label: "Private workflow",
		Access: &WorkflowAccess{Owners: []string{"owner"}},
	})
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/shared"):  string(shared),
		manifestPath("Workflow/private"): string(private),
		releaseRoot + "/product.json":    `{"schema_version":1,"product":"work","id":"release","title":"Release project","identity":{"name":"Release Crew","icon":"🚀"}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	api := &StreamingAPI{}
	registrar := &recordingRegistrar{}
	if err := api.registerAccessibleWorkflowListTool(registrar, "builder", nil); err != nil {
		t.Fatal(err)
	}
	if len(registrar.tools) != 1 {
		t.Fatalf("builder discovery registered %d tools, want only list_accessible_workflows", len(registrar.tools))
	}
	out, err := registrar.tools["list_accessible_workflows"].exec(context.Background(), map[string]interface{}{})
	if err != nil || !strings.Contains(out, `"workspace_path": "Workflow/shared"`) || !strings.Contains(out, `"name": "Shared workflow"`) || !strings.Contains(out, `"crews":`) || !strings.Contains(out, `"name": "Release project"`) || !strings.Contains(out, `"name": "Release Crew"`) || strings.Contains(out, "Private workflow") || strings.Contains(out, `"attached"`) {
		t.Fatalf("builder discovery out=%s err=%v", out, err)
	}
}

func containsWorkReferencePath(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// Crews are shared server-wide and read-write: attaching another owner's
// Crew by its physical path grants it in both the read and write sets,
// while a workflow reference stays read-only.
func TestUpdateWorkSessionWorkflowGuardCrewReferencesAreWritable(t *testing.T) {
	common.SetSessionFolderGuard("session-crew-rw", []string{"_users/me/Chats/Work/projects/mine/"}, []string{"_users/me/Chats/Work/projects/mine/"})
	updateWorkSessionWorkflowGuard("session-crew-rw", []string{"_users/other/Chats/Work/projects/theirs", "Workflow/reports"})
	cfg := common.GetSessionShellConfig("session-crew-rw")
	if cfg == nil || !containsWorkReferencePath(cfg.WritePaths, "_users/other/Chats/Work/projects/theirs/") || !containsWorkReferencePath(cfg.ReadPaths, "_users/other/Chats/Work/projects/theirs") {
		t.Fatalf("another owner's Crew reference must be read-write: %+v", cfg)
	}
	if containsWorkReferencePath(cfg.WritePaths, "Workflow/reports") {
		t.Fatalf("workflow reference must stay read-only: %+v", cfg)
	}
	updateWorkSessionWorkflowGuard("session-crew-rw", nil, "_users/other/Chats/Work/projects/theirs")
	if cfg = common.GetSessionShellConfig("session-crew-rw"); containsWorkReferencePath(cfg.WritePaths, "_users/other/Chats/Work/projects/theirs/") {
		t.Fatalf("detached Crew reference kept write access: %+v", cfg)
	}
}

func TestContextReferenceAcceptsOtherOwnerCrewPath(t *testing.T) {
	root, ok := contextReferenceReadRoot("me", "_users/other/Chats/Work/projects/theirs")
	if !ok || root != "_users/other/Chats/Work/projects/theirs" {
		t.Fatalf("root=%q ok=%v", root, ok)
	}
	for _, bad := range []string{"_users/other/Chats/Work/projects/..", "_users/../Chats/Work/projects/x", "_users/other/Chats/Work/other/x"} {
		if _, ok := contextReferenceReadRoot("me", bad); ok {
			t.Fatalf("accepted malformed crew path %q", bad)
		}
	}
	if _, ok := contextReferenceReadRoot("", "_users/other/Chats/Work/projects/theirs"); ok {
		t.Fatal("unauthenticated caller must not resolve a crew reference")
	}
}
