package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

const crewRunnerTriggers = `[
	{"id":"trig-1","name":"Review","enabled":true,"message":"Review it","kind":"internal","caller":{"type":"workflow","id":"wf-1"}},
	{"id":"trig-off","name":"Off","enabled":false,"message":"Off","kind":"internal","caller":{"type":"workflow","id":"wf-1"}}
]`

const testPreflightManifestAttached = `{"id":"wf-1","label":"WF1","crew_attachments":[{"id":"att-1","alias":"project-a","crew_profile_id":"crewx","crew_project_id":"project-a","crew_workspace_path":"_users/owner/Chats/Work/projects/project-a"}]}`

func testPreflightWorkspace(t *testing.T, plan string, manifestJSON string) *ProjectActionDispatcher {
	t.Helper()
	svc, files := newInternalTriggerTestCrew(t)
	svc.api = &StreamingAPI{}
	crewPath := "_users/owner/Chats/Work/projects/project-a/product.json"
	crewProduct := `{"schema_version":1,"product":"crewx","id":"project-a","title":"project-a","session_id":"sess-1","triggers":` + crewRunnerTriggers + `}`
	files[crewPath] = crewProduct
	mockFiles := map[string]string{crewPath: crewProduct}
	if plan != "" {
		mockFiles["Workflow/wf1/planning/plan.json"] = plan
	}
	if manifestJSON != "" {
		mockFiles["Workflow/wf1/workflow.json"] = manifestJSON
	}
	mock := &mockWorkspaceAPI{files: mockFiles}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	return svc
}

func TestPreflightCrewAttachmentsPreserveAccessAndBinding(t *testing.T) {
	ctx := context.Background()
	plain := `{"objective":"t","steps":[{"type":"scripted","id":"a","title":"A"}]}`
	// A healthy attachment on a crew-free plan passes: downstream steps
	// may read through the alias.
	if err := preflightCrewAttachments(ctx, testPreflightWorkspace(t, plain, testPreflightManifestAttached), "owner", "Workflow/wf1"); err != nil {
		t.Fatalf("healthy attachment rejected: %v", err)
	}
	// A revoked crew fails the run before any step executes, even with
	// no crew step in the plan: ordinary steps could read the alias.
	revoked := testPreflightWorkspaceWithoutCrew(t, plain, testPreflightManifestAttached)
	if err := preflightCrewAttachments(ctx, revoked, "owner", "Workflow/wf1"); err == nil || !strings.Contains(err.Error(), `"project-a"`) {
		t.Fatalf("revoked attachment err = %v, want failure naming the alias", err)
	}
	// A retargeted binding fails even though the crew itself exists.
	retargeted := `{"id":"wf-1","label":"WF1","crew_attachments":[{"id":"att-1","alias":"project-a","crew_profile_id":"crewx","crew_project_id":"project-a","crew_workspace_path":"_users/owner/secrets"}]}`
	if err := preflightCrewAttachments(ctx, testPreflightWorkspace(t, plain, retargeted), "owner", "Workflow/wf1"); err == nil || !strings.Contains(err.Error(), `"project-a"`) {
		t.Fatalf("retargeted attachment err = %v, want failure naming the alias", err)
	}
	// Same project-name suffix under another owner: shape-valid, and the
	// named crew exists for this user, but the stored root is not the
	// freshly authorized binding. Authorizing the project without comparing
	// roots would grant a workspace the check never authorized.
	wrongOwner := `{"id":"wf-1","label":"WF1","crew_attachments":[{"id":"att-1","alias":"project-a","crew_profile_id":"crewx","crew_project_id":"project-a","crew_workspace_path":"_users/other/Chats/Work/projects/project-a"}]}`
	if err := preflightCrewAttachments(ctx, testPreflightWorkspace(t, plain, wrongOwner), "owner", "Workflow/wf1"); err == nil || !strings.Contains(err.Error(), `"project-a"`) {
		t.Fatalf("wrong-owner attachment err = %v, want failure naming the alias", err)
	}
}

// testPreflightWorkspaceWithoutCrew mirrors testPreflightWorkspace but seeds
// no crew project: the attachment's crew is gone (deleted or unshared).
func testPreflightWorkspaceWithoutCrew(t *testing.T, plan string, manifestJSON string) *ProjectActionDispatcher {
	t.Helper()
	svc, _ := newInternalTriggerTestCrew(t)
	svc.api = &StreamingAPI{}
	mockFiles := map[string]string{}
	if plan != "" {
		mockFiles["Workflow/wf1/planning/plan.json"] = plan
	}
	if manifestJSON != "" {
		mockFiles["Workflow/wf1/workflow.json"] = manifestJSON
	}
	mock := &mockWorkspaceAPI{files: mockFiles}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	return svc
}

// A step run with no execution identity (Builder/Workshop) is a new request
// every time: it must not adopt the previous run's stored answer just
// because both share the run folder.
