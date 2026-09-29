package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCrewUsedByWorkflow(t *testing.T) {
	const ownerRoot = "_users/owner/Chats/Work/projects/notion-crew"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/attached"):       `{"id":"attached","crew_attachments":[{"crew_profile_id":"work","crew_project_id":"crew-1","crew_workspace_path":"_users/owner/Chats/Work/projects/notion-crew"}]}`,
		manifestPath("Workflow/other-owner"):    `{"id":"other-owner","crew_attachments":[{"crew_profile_id":"work","crew_project_id":"crew-1","crew_workspace_path":"_users/other/Chats/Work/projects/notion-crew"}]}`,
		manifestPath("Workflow/step-only"):      `{"id":"step-only"}`,
		"Workflow/step-only/planning/plan.json": `{"steps":[{"type":"crew","crew_profile_id":"work","crew_project_id":"crew-2"}]}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	used, err := crewUsedByWorkflow(context.Background(), "work", "crew-1", ownerRoot)
	if err != nil || !used {
		t.Fatalf("attached Crew: used=%v err=%v", used, err)
	}
	if got := workspace.files[manifestPath("Workflow/attached")]; !strings.Contains(got, `"crew_project_id":"crew-1"`) {
		t.Fatalf("dependency check changed workflow manifest: %s", got)
	}
	used, err = crewUsedByWorkflow(context.Background(), "work", "crew-2", ownerRoot)
	if err != nil || !used {
		t.Fatalf("Crew step without attachment: used=%v err=%v", used, err)
	}
	used, err = crewUsedByWorkflow(context.Background(), "work", "crew-3", ownerRoot)
	if err != nil || used {
		t.Fatalf("unreferenced Crew: used=%v err=%v", used, err)
	}

	delete(workspace.files, manifestPath("Workflow/attached"))
	workspace.files["Workflow/step-only/planning/plan.json"] = `{"steps":[{"type":"crew","crew_profile_id":"work","crew_project_id":"crew-1"}]}`
	used, err = crewUsedByWorkflow(context.Background(), "work", "crew-1", ownerRoot)
	if err != nil || !used {
		t.Fatalf("Crew step must still block after detaching: used=%v err=%v", used, err)
	}
	delete(workspace.files, "Workflow/step-only/planning/plan.json")
	used, err = crewUsedByWorkflow(context.Background(), "work", "crew-1", ownerRoot)
	if err != nil || used {
		t.Fatalf("removing the attachment and step should permit deletion; other owner's same ID must not block: used=%v err=%v", used, err)
	}
}

func TestCrewUsedByWorkflowFailsClosedWhenWorkspaceUnavailable(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	if used, err := crewUsedByWorkflow(context.Background(), "work", "crew-1", "_users/owner/Chats/Work/projects/notion-crew"); err == nil || used || !strings.Contains(err.Error(), "list workflows") {
		t.Fatalf("unavailable workspace must prevent deletion: used=%v err=%v", used, err)
	}
}
