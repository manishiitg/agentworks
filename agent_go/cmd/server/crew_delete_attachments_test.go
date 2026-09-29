package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeletingCrewDetachesFirstClassWorkflowReferences(t *testing.T) {
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/attached"): `{"id":"attached","label":"Attached","crew_attachments":[{"id":"a1","alias":"notion","crew_profile_id":"work","crew_project_id":"crew-1","crew_workspace_path":"_users/owner/Chats/Work/projects/notion-crew"},{"id":"a2","alias":"other","crew_profile_id":"work","crew_project_id":"crew-2","crew_workspace_path":"_users/owner/Chats/Work/projects/other-crew"},{"id":"a4","alias":"same-id-other-owner","crew_profile_id":"work","crew_project_id":"crew-1","crew_workspace_path":"_users/other/Chats/Work/projects/notion-crew"}]}`,
		manifestPath("Workflow/other"):    `{"id":"other","label":"Other","crew_attachments":[{"id":"a3","alias":"another","crew_profile_id":"work","crew_project_id":"crew-1","crew_workspace_path":"_users/owner/Chats/Work/projects/notion-crew"}]}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	count, err := detachDeletedCrewFromWorkflows(context.Background(), "work", "crew-1", "_users/owner/Chats/Work/projects/notion-crew")
	if err != nil || count != 2 {
		t.Fatalf("detached workflows = %d, %v; want 2", count, err)
	}
	attached, found, err := ReadWorkflowManifest(context.Background(), "Workflow/attached")
	if err != nil || !found || len(attached.CrewAttachments) != 2 || attached.CrewAttachments[0].CrewProjectID != "crew-2" || attached.CrewAttachments[1].Alias != "same-id-other-owner" {
		t.Fatalf("unrelated attachment was not preserved: %+v, found=%v, err=%v", attached, found, err)
	}
	other, found, err := ReadWorkflowManifest(context.Background(), "Workflow/other")
	if err != nil || !found || len(other.CrewAttachments) != 0 {
		t.Fatalf("deleted Crew attachment remained: %+v, found=%v, err=%v", other, found, err)
	}
	count, err = detachDeletedCrewFromWorkflows(context.Background(), "work", "crew-1", "_users/owner/Chats/Work/projects/notion-crew")
	if err != nil || count != 0 {
		t.Fatalf("repeat detach = %d, %v; want 0", count, err)
	}
}

func TestDeletingCrewReportsAttachmentCleanupFailure(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	if count, err := detachDeletedCrewFromWorkflows(context.Background(), "work", "crew-1", "_users/owner/Chats/Work/projects/notion-crew"); err == nil || count != 0 || !strings.Contains(err.Error(), "list workflows") {
		t.Fatalf("unavailable cleanup must be reported: detached=%d err=%v", count, err)
	}
}
