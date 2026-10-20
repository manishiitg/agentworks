package server

import (
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

const codePrivacyOwnerRoot = "Crew/app-c0de0001"

// newCodePrivacyFixture is one owner's Code workspace on a multi-user server
// with the code profile registered.
func newCodePrivacyFixture(t *testing.T) (*StreamingAPI, agentprofiles.Profile) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "app-c0de0001", ProjectID: "c0de0001-0000", OwnerID: "owner", Shared: true, Private: true, AudienceConfigured: true}); err != nil {
		t.Fatal(err)
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"other","username":"other","can_create":true}]}`)
	mock := &mockWorkspaceAPI{files: map[string]string{
		codePrivacyOwnerRoot + "/product.json":  `{"schema_version":1,"product":"work","id":"c0de0001-0000","title":"App","session_id":"code:project:c0de0001-0000"}`,
		codePrivacyOwnerRoot + "/workflow.json": `{"schema_version":1,"id":"c0de0001-0000","label":"App","capabilities":{}}`,
		codePrivacyOwnerRoot + "/code/main.go":  "package main",
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "work", Name: "Crew", Version: 3, SystemPromptTemplate: "hi", BuiltIn: true, Product: "work",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "auto",
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, Root: "Chats", ProjectsRoot: "Crew"},
		},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	return &StreamingAPI{agentProfiles: registry}, profile
}
