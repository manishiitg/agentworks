package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

const codePrivacyOwnerRoot = "_users/owner/Chats/Code/projects/app-c0de0001"

// newCodePrivacyFixture is one owner's Code workspace on a multi-user server
// with the code profile registered.
func newCodePrivacyFixture(t *testing.T) (*StreamingAPI, agentprofiles.Profile) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true}]}`)
	mock := &mockWorkspaceAPI{files: map[string]string{
		codePrivacyOwnerRoot + "/product.json":  `{"schema_version":1,"product":"code","id":"c0de0001-0000","title":"App","session_id":"code:project:c0de0001-0000"}`,
		codePrivacyOwnerRoot + "/workflow.json": `{"schema_version":1,"id":"c0de0001-0000","label":"App","capabilities":{}}`,
		codePrivacyOwnerRoot + "/code/main.go":  "package main",
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "code", Name: "Code", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Product: "code",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Chats/Code/projects"},
		},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	return &StreamingAPI{agentProfiles: registry}, profile
}

func TestCodeWorkspaceResolvesForItsOwnerOnly(t *testing.T) {
	api, profile := newCodePrivacyFixture(t)
	ctx := context.Background()

	owned, err := resolveCrewProjectBinding(ctx, "owner", profile, "c0de0001-0000", "")
	if err != nil || !owned.OwnedByCaller {
		t.Fatalf("owner resolve = %+v err=%v", owned, err)
	}
	// Crew scans every owner for a readable project; a Code never does,
	// with or without the owner's physical folder as a hint.
	for _, hint := range []string{"", codePrivacyOwnerRoot} {
		if got, err := resolveCrewProjectBinding(ctx, "other", profile, "c0de0001-0000", hint); err == nil {
			t.Fatalf("another user resolved the owner's Code (hint %q): %+v", hint, got)
		}
	}
	if _, owned, err := resolveConversationBindingForUser(ctx, "other", profile, "c0de0001-0000"); err == nil {
		t.Fatalf("another user bound the owner's Code (owned=%v)", owned)
	}

	claimsCtx := func(userID string) context.Context {
		return context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: userID, Username: userID})
	}
	req := QueryRequest{AgentProfileID: "code", AgentProfileConversationKey: "c0de0001-0000", SelectedFolder: codePrivacyOwnerRoot}
	if level, err := api.conversationTargetAccess(claimsCtx("owner"), req); err != nil || level != WorkflowAccessOwner {
		t.Fatalf("owner access = %v err=%v", level, err)
	}
	if level, err := api.conversationTargetAccess(claimsCtx("other"), req); err == nil || level != WorkflowAccessNone {
		t.Fatalf("another user reached the owner's Code: %v err=%v", level, err)
	}
	// Naming only the folder (no Code profile) is not a way around it.
	req.AgentProfileID = ""
	if level, err := api.conversationTargetAccess(claimsCtx("other"), req); err == nil || level != WorkflowAccessNone {
		t.Fatalf("another user reached the owner's Code by folder: %v err=%v", level, err)
	}
}

func TestSharedProjectsNeverListsCode(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	req := mux.SetURLVars(profileRouteRequest(http.MethodGet, "/api/agent-profiles/code/shared-projects", nil, "other"), map[string]string{"id": "code"})
	rec := httptest.NewRecorder()
	api.handleListSharedProjects(rec, req)
	var decoded struct {
		Projects []sharedProjectSummary `json:"projects"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &decoded) != nil || len(decoded.Projects) != 0 {
		t.Fatalf("shared Code listing = %d %s", rec.Code, rec.Body.String())
	}
}

func TestProjectProductPaths(t *testing.T) {
	cases := []struct {
		path          string
		project, code bool
	}{
		{"Chats/Work/projects/alpha", true, false},
		{"_users/u/Chats/Work/projects/alpha/code", true, false},
		{"Chats/Code/projects/app-1", true, true},
		{"_users/u/Chats/Code/projects/app-1/code/x.go", true, true},
		{"Chats/Code/projects", false, false},
		{"Chats/Code/projects/", false, false},
		{"Chats/Other/projects/x", false, false},
	}
	for _, tc := range cases {
		if got := isProjectWorkspacePath(tc.path); got != tc.project {
			t.Errorf("isProjectWorkspacePath(%q) = %v", tc.path, got)
		}
		if got := isCodeProjectPath(tc.path); got != tc.code {
			t.Errorf("isCodeProjectPath(%q) = %v", tc.path, got)
		}
		// A Code is never a Crew: Crew-only callers keep rejecting it.
		if tc.code && isCrewProjectPath(tc.path) {
			t.Errorf("isCrewProjectPath(%q) accepted a Code", tc.path)
		}
	}
	if contract := uiContractForScope("Chats/Code/projects/app-1"); contract.Product != "code" {
		t.Fatalf("Code UI contract = %q", contract.Product)
	}
	for _, view := range codeUIControlContract.Views {
		if view.ID == "identity" || view.ID == "schedules" || view.ID == "workshop" {
			t.Fatalf("Code UI contract exposes %s", view.ID)
		}
	}
}
