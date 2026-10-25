package server

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestConversationTargetAccessUsesCrewProjectAndWorkflowGrant(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":false,"products":["work"]},{"id":"other","username":"other","can_create":true,"products":["work"]}]}`)
	const projectID = "project-1"
	const publicPath = "Crew/my-crew"
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	registerTestCrew(t, "my-crew", "owner", true)
	workspace := &mockWorkspaceAPI{files: map[string]string{
		publicPath + "/product.json":    `{"schema_version":1,"product":"work","id":"project-1","title":"My Crew","session_id":"crew-session"}`,
		manifestPath("Workflow/shared"): `{"schema_version":1,"id":"wf_shared","access":{"owners":["other"],"readers":["owner"]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	profiles := agentprofiles.NewRegistry()
	profile := routeTestProfile("work", true, "")
	profile.Product = "work"
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Crew"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	if err := profiles.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: profiles}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	crew := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", AgentProfileConversationKey: projectID, SelectedFolder: publicPath}
	if access, err := api.conversationTargetAccess(ctx, crew); err != nil || access != WorkflowAccessOwner {
		t.Fatalf("own Crew access=%q err=%v, want owner", access, err)
	}
	botClaims := &UserClaims{UserID: "owner", Provider: "bot_route", BotRouteProfileID: "work", BotRouteConversationKey: projectID, BotRouteWorkspacePath: publicPath, BotRouteGrant: "run"}
	botCtx := context.WithValue(context.Background(), UserContextKey, botClaims)
	if access, err := api.conversationTargetAccess(botCtx, crew); err != nil || access != WorkflowAccessRead {
		t.Fatalf("Slack Crew route access=%q err=%v, want read", access, err)
	}
	if access, err := api.conversationTargetAccess(ctx, QueryRequest{SelectedFolder: "Workflow/shared"}); err != nil || access != WorkflowAccessRead {
		t.Fatalf("shared workflow access=%q err=%v, want read", access, err)
	}
	for _, forged := range []QueryRequest{
		{AgentProfileID: "work", AgentProfileConversationKey: projectID, SelectedFolder: "Crew/other"},
		{AgentProfileID: "work", AgentProfileConversationKey: "missing", SelectedFolder: publicPath},
		{AgentProfileID: "work", AgentProfileConversationKey: projectID, SelectedFolder: "Chats/Work/projects/my-crew"},
	} {
		if access, err := api.conversationTargetAccess(ctx, forged); err == nil || access != WorkflowAccessNone {
			t.Fatalf("forged Crew request allowed: %+v => %q, %v", forged, access, err)
		}
	}
	workspace.mu.Lock()
	delete(workspace.files, publicPath+"/product.json")
	workspace.mu.Unlock()
	if err := defaultProjectOwners().Remove("work", "my-crew"); err != nil {
		t.Fatal(err)
	}
	resetCrewOwnerCache()
	if access, err := api.conversationTargetAccess(ctx, crew); err == nil || access != WorkflowAccessNone {
		t.Fatalf("deleted Crew allowed: %q, %v", access, err)
	}
}

func TestBackfillWorkflowPhaseFolderResolvesPresetBeforeAccess(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"contrib","username":"contrib","can_create":true},{"id":"owner","username":"owner","can_create":true}]}`)
	workspace := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/shared"): `{"schema_version":1,"id":"wf_shared","access":{"owners":["owner"],"readers":["contrib"]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)

	api := &StreamingAPI{}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "contrib", Username: "contrib"})

	// A preset-only workflow-phase chat is what the client sends. The folder
	// must resolve so manifest membership — not the write-tier account —
	// answers for the turn.
	req := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", PresetQueryID: "wf_shared"}
	api.backfillWorkflowPhaseFolder(ctx, &req)
	if req.SelectedFolder != "Workflow/shared" {
		t.Fatalf("SelectedFolder=%q, want Workflow/shared", req.SelectedFolder)
	}
	if access, err := api.conversationTargetAccess(ctx, req); err != nil || access != WorkflowAccessRead {
		t.Fatalf("preset-only access=%q err=%v, want read", access, err)
	}

	// Without the backfill the same request fails open to the account tier.
	bare := QueryRequest{AgentMode: "workflow_phase", PhaseID: "workflow-builder", PresetQueryID: "wf_shared"}
	if access, err := api.conversationTargetAccess(ctx, bare); err != nil || access != WorkflowAccessWrite {
		t.Fatalf("unbackfilled access=%q err=%v, want write (the fail-open the backfill prevents)", access, err)
	}

	// Unknown presets, explicit folders, and other modes are left untouched.
	unknown := QueryRequest{AgentMode: "workflow_phase", PresetQueryID: "wf_missing"}
	api.backfillWorkflowPhaseFolder(ctx, &unknown)
	if unknown.SelectedFolder != "" {
		t.Fatalf("unknown preset set folder=%q, want empty", unknown.SelectedFolder)
	}
	explicit := QueryRequest{AgentMode: "workflow_phase", PresetQueryID: "wf_shared", SelectedFolder: "Workflow/elsewhere"}
	api.backfillWorkflowPhaseFolder(ctx, &explicit)
	if explicit.SelectedFolder != "Workflow/elsewhere" {
		t.Fatalf("explicit folder overwritten: %q", explicit.SelectedFolder)
	}
	other := QueryRequest{AgentMode: "multi-agent", PresetQueryID: "wf_shared"}
	api.backfillWorkflowPhaseFolder(ctx, &other)
	if other.SelectedFolder != "" {
		t.Fatalf("non-phase request set folder=%q, want empty", other.SelectedFolder)
	}
}
