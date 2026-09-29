package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func profileRouteRequest(method, target string, body []byte, userID string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	claims := &UserClaims{UserID: userID, Username: userID}
	return req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
}

func routeTestProfile(id string, builtIn bool, ownerID string) agentprofiles.Profile {
	return agentprofiles.Profile{
		ID:                   id,
		Name:                 id,
		Version:              1,
		SystemPromptTemplate: "Work on {{.ProjectTitle}}.",
		Runtime:              agentprofiles.RuntimePolicy{Transport: "auto"},
		BuiltIn:              builtIn,
		OwnerID:              ownerID,
	}
}

func TestQueryRequestForAgentProfileChatUsesOnlyServerOwnedProfileConfiguration(t *testing.T) {
	profile := routeTestProfile("dominion", true, "")
	profile.Name = "Dominion"

	query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{
		Message: "What changed in the portfolio today?",
	}, ProductConversationRecord{
		ConversationID:  "conversation-1",
		ConversationKey: "main",
		SessionID:       "session-1",
		WorkspacePath:   "Chats",
		Title:           "Dominion",
	})
	if err != nil {
		t.Fatal(err)
	}

	if query.Query != "What changed in the portfolio today?" {
		t.Fatalf("query=%q", query.Query)
	}
	if query.AgentProfileID != "dominion" || query.AgentProfileVersion != 1 {
		t.Fatalf("unexpected profile binding: id=%q version=%d", query.AgentProfileID, query.AgentProfileVersion)
	}
	if query.SelectedFolder != "Chats" || query.AgentProfileContext.ProjectTitle != "Dominion" {
		t.Fatalf("unexpected server-owned workspace binding: folder=%q context=%+v", query.SelectedFolder, query.AgentProfileContext)
	}
	if query.RestoredConversationPath != "" || query.RestoredConversationSessionID != "session-1" {
		t.Fatalf("server-owned resume identity missing or browser path leaked: path=%q session=%q", query.RestoredConversationPath, query.RestoredConversationSessionID)
	}
	if query.AgentMode != "multi-agent" || query.DisableLiveInputDelivery {
		t.Fatalf("unexpected runner configuration: mode=%q disable_live_input=%v", query.AgentMode, query.DisableLiveInputDelivery)
	}
}

func TestWorkResumeKeyIsDerivedFromProjectAndSavedChat(t *testing.T) {
	input := AgentProfileConversationRequest{ResourceID: "project-1", SessionID: "saved-chat"}
	if got := agentProfileResumeConversationKey("work", input); got != "project-1:saved-chat" {
		t.Fatalf("resume key=%q", got)
	}
	input.ConversationKey = "existing:key"
	if got := agentProfileResumeConversationKey("work", input); got != "existing:key" {
		t.Fatalf("existing key was replaced: %q", got)
	}
	if got := agentProfileResumeConversationKey("video-studio", AgentProfileConversationRequest{ResourceID: "launch", SessionID: "saved-chat"}); got != "launch" {
		t.Fatalf("non-Work project key=%q", got)
	}
}

func TestQueryRequestForAgentProfileChatRequiresServerOwnedWorkspace(t *testing.T) {
	_, err := queryRequestForAgentProfileChat(
		routeTestProfile("project-product", true, ""),
		AgentProfileChatRequest{Message: "hello"},
		ProductConversationRecord{SessionID: "session-1"},
	)
	if err == nil {
		t.Fatal("expected a conversation without runtime workspace to be rejected")
	}
}

func sparkQuillTestProfile() agentprofiles.Profile {
	profile := routeTestProfile("sparkquill", true, "")
	profile.Name = "SparkQuill"
	profile.Runtime.ProviderOptions = []agentprofiles.ProviderOption{
		{ID: "claude-code", Label: "Claude Code", Provider: "claude-code", ModelID: "claude-sonnet-5-5", Default: true},
		{ID: "codex-cli", Label: "Codex", Provider: "codex-cli", ModelID: "gpt-5.4"},
	}
	return profile
}

func TestQueryRequestForAgentProfileChatWithNoEngineLeavesProviderUnset(t *testing.T) {
	query, err := queryRequestForAgentProfileChat(sparkQuillTestProfile(), AgentProfileChatRequest{
		Message: "hello",
	}, ProductConversationRecord{SessionID: "session-1", WorkspacePath: "Chats/SparkQuill"})
	if err != nil {
		t.Fatal(err)
	}
	if query.Provider != "" || query.ModelID != "" {
		t.Fatalf("no engine requested must leave provider/model unset (the profile's own default applies), got provider=%q model=%q", query.Provider, query.ModelID)
	}
}

func TestQueryRequestForAgentProfileChatResolvesDeclaredEngine(t *testing.T) {
	query, err := queryRequestForAgentProfileChat(sparkQuillTestProfile(), AgentProfileChatRequest{
		Message: "hello",
		Engine:  "codex-cli",
	}, ProductConversationRecord{SessionID: "session-1", WorkspacePath: "Chats/SparkQuill"})
	if err != nil {
		t.Fatal(err)
	}
	if query.Provider != "codex-cli" || query.ModelID != "gpt-5.4" {
		t.Fatalf("engine %q must resolve to its declared (provider, model_id), got provider=%q model=%q", "codex-cli", query.Provider, query.ModelID)
	}
}

func TestQueryRequestForAgentProfileChatRejectsUndeclaredEngine(t *testing.T) {
	_, err := queryRequestForAgentProfileChat(sparkQuillTestProfile(), AgentProfileChatRequest{
		Message: "hello",
		Engine:  "gemini-anything-goes",
	}, ProductConversationRecord{SessionID: "session-1", WorkspacePath: "Chats/SparkQuill"})
	if err == nil {
		t.Fatal("an engine id not declared in the profile's provider_options must be rejected, never silently ignored or passed through")
	}
}

func TestQueryRequestForAgentProfileChatAcceptsDeclaredChatExtras(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	profile.Runtime.Capabilities.MCPSelection = agentprofiles.CapabilityPreferred
	profile.Runtime.Capabilities.SkillSelection = agentprofiles.CapabilityPreferred
	profile.Runtime.Capabilities.WorkflowReferences = agentprofiles.CapabilityPreferred
	query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{
		Message:              "hello",
		EnabledServers:       []string{"github", "github"},
		SelectedSkills:       []string{"code-reviewer", "code-reviewer"},
		WorkflowContextPaths: []string{"Workflow/customer-research", "Workflow/customer-research"},
	}, ProductConversationRecord{SessionID: "session-1", WorkspacePath: "Chats/Work/projects/demo"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(query.EnabledServers, ",") != "github" || strings.Join(query.SelectedSkills, ",") != "code-reviewer" {
		t.Fatalf("chat extras not applied or deduplicated: servers=%v skills=%v", query.EnabledServers, query.SelectedSkills)
	}
	if strings.Join(query.WorkflowContextPaths, ",") != "Workflow/customer-research" {
		t.Fatalf("workflow references not applied or deduplicated: %v", query.WorkflowContextPaths)
	}
}

func TestProjectChatMergesDurableAndMessageWorkflowReferences(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	profile.Runtime.Capabilities.WorkflowReferences = agentprofiles.CapabilityPreferred
	query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{
		Message:              "compare them",
		WorkflowContextPaths: []string{"Workflow/temporary", "Workflow/saved"},
	}, ProductConversationRecord{
		SessionID:                   "session-1",
		WorkspacePath:               "Chats/Work/projects/demo",
		ResourceID:                  "demo",
		ProjectWorkflowContextPaths: []string{"Workflow/saved"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(query.WorkflowContextPaths, ","); got != "Workflow/saved,Workflow/temporary" {
		t.Fatalf("workflow references = %q, want durable first and deduplicated", got)
	}
}

func TestProjectChatUsesManifestMCPAndSkillsInsteadOfBrowserInput(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	profile.Runtime.Capabilities.MCPSelection = agentprofiles.CapabilityPreferred
	profile.Runtime.Capabilities.SkillSelection = agentprofiles.CapabilityPreferred
	query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{
		Message:        "hello",
		EnabledServers: []string{"browser-leaked-server"},
		SelectedSkills: []string{"browser-leaked-skill"},
	}, ProductConversationRecord{
		SessionID:              "session-1",
		WorkspacePath:          "Chats/Work/projects/demo",
		ResourceID:             "demo",
		ProjectSelectedServers: []string{"github"},
		ProjectSelectedSkills:  []string{"code-reviewer"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(query.EnabledServers, ",") != "github" || strings.Join(query.SelectedSkills, ",") != "code-reviewer" {
		t.Fatalf("project manifest was not authoritative: servers=%v skills=%v", query.EnabledServers, query.SelectedSkills)
	}
	empty, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{
		Message:        "hello",
		EnabledServers: []string{"browser-leaked-server"},
	}, ProductConversationRecord{
		SessionID:     "session-2",
		WorkspacePath: "Chats/Work/projects/empty",
		ResourceID:    "empty",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(empty.EnabledServers, ",") != "NO_SERVERS" {
		t.Fatalf("an empty project selection was not preserved as none: %v", empty.EnabledServers)
	}
}

func TestQueryRequestForAgentProfileChatRejectsUndeclaredChatExtras(t *testing.T) {
	profile := routeTestProfile("dominion", true, "")
	_, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{
		Message:              "hello",
		EnabledServers:       []string{"github"},
		WorkflowContextPaths: []string{"Workflow/customer-research"},
	}, ProductConversationRecord{SessionID: "session-1", WorkspacePath: "Chats"})
	if err == nil {
		t.Fatal("expected a fixed-purpose profile to reject user-selected MCP servers")
	}
}

func TestQueryRequestForAgentProfileChatAcceptsNoServersSentinelOnFixedPurposeProfile(t *testing.T) {
	profile := routeTestProfile("dominion", true, "")
	_, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{
		Message:        "hello",
		EnabledServers: []string{"NO_SERVERS"},
	}, ProductConversationRecord{SessionID: "session-1", WorkspacePath: "Chats"})
	if err != nil {
		t.Fatalf("expected the NO_SERVERS sentinel to be accepted even when MCP selection is disabled, got %v", err)
	}
}

func TestQueryRequestForAgentProfileChatRejectsUndeclaredWorkflowReferences(t *testing.T) {
	profile := routeTestProfile("dominion", true, "")
	_, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{
		Message:              "hello",
		WorkflowContextPaths: []string{"Workflow/customer-research"},
	}, ProductConversationRecord{SessionID: "session-1", WorkspacePath: "Chats"})
	if err == nil || !strings.Contains(err.Error(), "does not accept workflow references") {
		t.Fatalf("expected a fixed-purpose profile to reject workflow references, got %v", err)
	}
}

func TestAgentProfileChatEndpointRejectsBroadAgentWorksFields(t *testing.T) {
	api := &StreamingAPI{}
	req := profileRouteRequest(
		http.MethodPost,
		"/api/agent-profiles/dominion/query",
		[]byte(`{"message":"hello","provider":"codex-cli"}`),
		"user-1",
	)
	req = mux.SetURLVars(req, map[string]string{"id": "dominion"})
	recorder := httptest.NewRecorder()

	api.handleAgentProfileChatQuery(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("unknown field")) {
		t.Fatalf("expected unknown-field rejection, body=%s", recorder.Body.String())
	}
}

func TestListAgentProfilesFiltersOtherOwners(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	for _, profile := range []agentprofiles.Profile{
		routeTestProfile("built-in", true, ""),
		routeTestProfile("mine", false, "user-1"),
		routeTestProfile("theirs", false, "user-2"),
	} {
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatal(err)
		}
	}

	recorder := httptest.NewRecorder()
	listAgentProfilesHandler(registry)(recorder, profileRouteRequest(http.MethodGet, "/api/agent-profiles", nil, "user-1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Profiles []agentprofiles.Profile `json:"profiles"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Profiles) != 2 || response.Profiles[0].ID != "built-in" || response.Profiles[1].ID != "mine" {
		t.Fatalf("unexpected visible profiles: %+v", response.Profiles)
	}
}

func TestAgentProfileCatalogHidesAdminOnlyProductFromMember(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES", "work")
	withMemoryUserDirectory(t, `{"users":[
		{"id":"admin","username":"admin","admin":true,"can_create":true,"products":[]},
		{"id":"member","username":"member","can_create":true,"products":[]}
	]}`)
	registry := agentprofiles.NewRegistry()
	work := routeTestProfile("work", true, "")
	work.Product = "work"
	if err := registry.RegisterProfile(work); err != nil {
		t.Fatal(err)
	}

	memberRequest := profileRouteRequest(http.MethodGet, "/api/agent-profiles", nil, "member")
	memberRecorder := httptest.NewRecorder()
	listAgentProfilesHandler(registry)(memberRecorder, memberRequest)
	if !bytes.Contains(memberRecorder.Body.Bytes(), []byte(`"profiles":[]`)) {
		t.Fatalf("member catalog leaked Work: %s", memberRecorder.Body.String())
	}

	adminRequest := profileRouteRequest(http.MethodGet, "/api/agent-profiles", nil, "admin")
	adminRecorder := httptest.NewRecorder()
	listAgentProfilesHandler(registry)(adminRecorder, adminRequest)
	if !bytes.Contains(adminRecorder.Body.Bytes(), []byte(`"id":"work"`)) {
		t.Fatalf("admin catalog did not include Work: %s", adminRecorder.Body.String())
	}

	memberGet := mux.SetURLVars(profileRouteRequest(http.MethodGet, "/api/agent-profiles/work", nil, "member"), map[string]string{"id": "work"})
	memberGetRecorder := httptest.NewRecorder()
	getAgentProfileHandler(registry)(memberGetRecorder, memberGet)
	if memberGetRecorder.Code != http.StatusNotFound {
		t.Fatalf("member direct profile access status=%d body=%s", memberGetRecorder.Code, memberGetRecorder.Body.String())
	}
}

func TestGetAgentProfileDoesNotLeakAnotherOwner(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(routeTestProfile("mine", false, "user-1")); err != nil {
		t.Fatal(err)
	}
	req := profileRouteRequest(http.MethodGet, "/api/agent-profiles/mine", nil, "user-2")
	req = mux.SetURLVars(req, map[string]string{"id": "mine"})
	recorder := httptest.NewRecorder()
	getAgentProfileHandler(registry)(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestValidateAgentProfileCannotClaimBuiltInAuthority(t *testing.T) {
	profile := routeTestProfile("custom-agent", true, "server-owner")
	body, _ := json.Marshal(profile)
	recorder := httptest.NewRecorder()
	validateAgentProfileHandler()(recorder, profileRouteRequest(http.MethodPost, "/api/agent-profiles/validate", body, "user-1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Valid   bool                  `json:"valid"`
		Profile agentprofiles.Profile `json:"profile"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Valid || response.Profile.BuiltIn || response.Profile.OwnerID != "user-1" {
		t.Fatalf("validation did not enforce user ownership: %+v", response)
	}
}

func accountSwitchTestSetup(t *testing.T, key string) (*StreamingAPI, *http.Request, agentprofiles.Profile, ProductConversationRecord) {
	t.Helper()
	server, _ := newFakeWorkspaceServer(t)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	profile := singletonConversationProfile()
	profile.Runtime.ProviderOptions = []agentprofiles.ProviderOption{
		{ID: "muse-cli", Label: "Muse", Provider: "muse-cli", ModelID: "muse-spark-1.3-contributor", Default: true},
		{ID: "codex-cli", Label: "Codex", Provider: "codex-cli", ModelID: "gpt-6-astra"},
	}
	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry}
	req := profileRouteRequest("POST", "/", nil, "alice")
	conversation, err := api.resolveAgentProfileConversation(req, profile, key)
	if err != nil {
		t.Fatal(err)
	}
	return api, req, profile, conversation
}

// A provider switch with no private account on either side rebinds and
// restarts the CLI instead of failing: a stale engine pick (or a project
// default that drifted from the last-bound runtime) must not lock the user
// out of their own chat.
func TestProductTurnAllowsGlobalProviderSwitch(t *testing.T) {
	api, req, profile, conversation := accountSwitchTestSetup(t, "main")
	if _, err := prepareProductConversationTurn(req.Context(), "alice", profile, AgentProfileChatRequest{Message: "hi", Engine: "codex-cli"}, conversation); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	// Production resolves the binding fresh on every turn.
	conversation, err := api.resolveAgentProfileConversation(req, profile, "main")
	if err != nil {
		t.Fatal(err)
	}
	query, err := prepareProductConversationTurn(req.Context(), "alice", profile, AgentProfileChatRequest{Message: "follow-up", Engine: "muse-cli"}, conversation)
	if err != nil {
		t.Fatalf("global provider switch should rebind, got: %v", err)
	}
	if query.Provider != "muse-cli" {
		t.Fatalf("rebound provider = %q", query.Provider)
	}
	rebound, err := api.resolveAgentProfileConversation(req, profile, "main")
	if err != nil {
		t.Fatal(err)
	}
	if rebound.Provider != "muse-cli" {
		t.Fatalf("registry still bound to %q", rebound.Provider)
	}
}

// A real account change still needs a new conversation: moving onto a
// private connection, or between two private connections, must stay
// explicit. (Omitting a connection inherits the bound one, so it cannot
// silently drop back to shared use.)
func TestProductTurnStillRejectsPrivateAccountChange(t *testing.T) {
	api, req, profile, conversation := accountSwitchTestSetup(t, "main")
	if _, err := prepareProductConversationTurn(req.Context(), "alice", profile, AgentProfileChatRequest{Message: "hi", Engine: "muse-cli"}, conversation); err != nil {
		t.Fatalf("first turn: %v", err)
	}
	conversation, err := api.resolveAgentProfileConversation(req, profile, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareProductConversationTurn(req.Context(), "alice", profile, AgentProfileChatRequest{Message: "hi", Engine: "muse-cli", ConnectionID: "account-B"}, conversation); err == nil || !strings.Contains(err.Error(), "account change requires a new conversation") {
		t.Fatalf("shared-to-private switch should be refused, got: %v", err)
	}

	api, req, profile, conversation = accountSwitchTestSetup(t, "main")
	if _, err := prepareProductConversationTurn(req.Context(), "alice", profile, AgentProfileChatRequest{Message: "hi", Engine: "muse-cli", ConnectionID: "account-A"}, conversation); err != nil {
		t.Fatalf("private first turn: %v", err)
	}
	conversation, err = api.resolveAgentProfileConversation(req, profile, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareProductConversationTurn(req.Context(), "alice", profile, AgentProfileChatRequest{Message: "hi", Engine: "muse-cli", ConnectionID: "account-B"}, conversation); err == nil || !strings.Contains(err.Error(), "account change requires a new conversation") {
		t.Fatalf("private-to-private switch should be refused, got: %v", err)
	}
}
