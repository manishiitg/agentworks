package server

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

func TestAgentProfileSessionKeyTracksDefinitionAndIdentity(t *testing.T) {
	base := &resolvedAgentProfile{Definition: agentprofiles.Profile{ID: "work", Version: 1, Skills: []string{"work-mcp"}}}
	key := agentProfileSessionKey(base)
	if key == "" {
		t.Fatal("profile key must not be empty")
	}
	changedDefinition := &resolvedAgentProfile{Definition: base.Definition}
	changedDefinition.Definition.Tools = append(changedDefinition.Definition.Tools, agentprofiles.ToolBinding{ID: "update_project_mcp_server_selection"})
	if got := agentProfileSessionKey(changedDefinition); got == key {
		t.Fatal("profile key did not change with tool definition")
	}
	changedIdentity := &resolvedAgentProfile{Definition: base.Definition, IdentityKey: "Reviewer\nShip it."}
	if got := agentProfileSessionKey(changedIdentity); got == key {
		t.Fatal("profile key did not change with Crew identity")
	}
	// A secret attached later, or a connection whose sign-in finished, reaches a retained CLI only
	// through a relaunch, so both are part of the key (names and state, never values).
	withSecret := &resolvedAgentProfile{Definition: base.Definition, ChatSecrets: []string{"GITHUB_TOKEN"}}
	if got := agentProfileSessionKey(withSecret); got == key {
		t.Fatal("profile key did not change when a secret was attached")
	}
	signedOut := &resolvedAgentProfile{Definition: base.Definition, ChatConnections: []string{"u1__notion#signed-out"}}
	signedIn := &resolvedAgentProfile{Definition: base.Definition, ChatConnections: []string{"u1__notion#signed-in"}}
	if agentProfileSessionKey(signedOut) == agentProfileSessionKey(signedIn) {
		t.Fatal("profile key did not change when a connection finished signing in")
	}
}

func TestWorkspacePathsMatchForUserTreatsOnlyOwnPhysicalPathAsPublic(t *testing.T) {
	public := "Chats/Work/projects/project-1"
	if !workspacePathsMatchForUser("user-1", public, "_users/user-1/Chats/Work/projects/project-1") {
		t.Fatal("expected the caller's public and physical Work paths to match")
	}
	if workspacePathsMatchForUser("user-1", public, "_users/user-2/Chats/Work/projects/project-1") {
		t.Fatal("another user's physical Work path must not match the caller's public path")
	}
}

func TestResolveAgentProfileForQueryResolvesGlobalScopeWithoutFolderOrTitle(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "global-assistant", Name: "Global Assistant", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "placeholder",
		Scope:                agentprofiles.ProfileScopeGlobal,
		Runtime:              agentprofiles.RuntimePolicy{Transport: "auto"},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry}
	// No SelectedFolder, no AgentProfileContext.ProjectTitle -- exactly what a
	// global-scoped profile must resolve without, unlike a project-scoped one.
	req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "global-assistant"}
	resolved, err := api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "session-1")
	if err != nil {
		t.Fatalf("global-scoped profile should resolve without selected_folder/project_title, got: %v", err)
	}
	if resolved == nil {
		t.Fatal("expected a resolved profile")
	}
	if req.SelectedFolder != "Chats" {
		t.Fatalf("expected the Chats alias to be defaulted, got %q", req.SelectedFolder)
	}
	if req.AgentProfileContext.ProjectTitle != "Global Assistant" {
		t.Fatalf("expected project_title to default to the profile Name, got %q", req.AgentProfileContext.ProjectTitle)
	}
	if got := agentProfileRuntimeWorkspace("user-1", req.SelectedFolder); got != "_users/user-1/Chats" {
		t.Fatalf("runtime folder = %q, want the same folder a profile-less turn already uses", got)
	}
}

// A global-scoped profile is meant to feel like a
// profile-less multi-agent chat, where the user's own chat-level model
// selection (any published LLM) already wins -- unlike a project-scoped
// product (Video Studio), whose pinned runtime binding is deliberately
// authoritative. The declared runtime.provider/model_id is only the default
// for a brand-new chat with no selection yet.
func TestResolveAgentProfileForQueryGlobalScopeDefersToRequestedModel(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "global-assistant", Name: "Global Assistant", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "placeholder",
		Scope:                agentprofiles.ProfileScopeGlobal,
		Runtime:              agentprofiles.RuntimePolicy{Transport: "auto", Provider: "claude-code", ModelID: "claude-sonnet-5-5"},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry}
	req := QueryRequest{
		AgentMode: "multi-agent", AgentProfileID: "global-assistant",
		Provider: "codex-cli", ModelID: "gpt-6-sol",
	}
	if _, err := api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "session-1"); err != nil {
		t.Fatalf("resolveAgentProfileForQuery() error = %v", err)
	}
	if req.Provider != "codex-cli" || req.ModelID != "gpt-6-sol" {
		t.Fatalf("expected the request's own model selection to win, got provider=%q model=%q", req.Provider, req.ModelID)
	}
}

func TestResolveAgentProfileForQueryGlobalScopeFallsBackToPinnedModelWhenRequestEmpty(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "global-assistant", Name: "Global Assistant", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "placeholder",
		Scope:                agentprofiles.ProfileScopeGlobal,
		Runtime:              agentprofiles.RuntimePolicy{Transport: "auto", Provider: "claude-code", ModelID: "claude-sonnet-5-5"},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry}
	req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "global-assistant"}
	if _, err := api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "session-1"); err != nil {
		t.Fatalf("resolveAgentProfileForQuery() error = %v", err)
	}
	if req.Provider != "claude-code" || req.ModelID != "claude-sonnet-5-5" {
		t.Fatalf("expected the declared default when the request had no selection, got provider=%q model=%q", req.Provider, req.ModelID)
	}
}

// The project-scoped path must keep its authoritative-pin behavior
// unchanged: this is what the isGlobalScope gate exists to preserve.
func TestResolveAgentProfileForQueryProjectScopeStaysAuthoritative(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "video-studio", Name: "Video Studio", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "placeholder",
		Runtime:              agentprofiles.RuntimePolicy{Transport: "auto", Provider: "claude-code", ModelID: "claude-sonnet-5-5"},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry}
	req := QueryRequest{
		AgentMode: "multi-agent", AgentProfileID: "video-studio",
		SelectedFolder:      "Chats/Video Studio/projects/demo",
		AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Demo"},
		Provider:            "codex-cli", ModelID: "gpt-6-sol",
	}
	if _, err := api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "session-1"); err != nil {
		t.Fatalf("resolveAgentProfileForQuery() error = %v", err)
	}
	if req.Provider != "claude-code" || req.ModelID != "claude-sonnet-5-5" {
		t.Fatalf("expected the project-scoped pin to stay authoritative, got provider=%q model=%q", req.Provider, req.ModelID)
	}
}

func TestResolveAgentProfileForQueryRejectsProjectScopeWithoutTitle(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "video-studio", Name: "Video Studio", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "placeholder",
		Runtime:              agentprofiles.RuntimePolicy{Transport: "auto"},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry}
	req := QueryRequest{
		AgentMode: "multi-agent", AgentProfileID: "video-studio",
		SelectedFolder: "Chats/Video Studio/projects/launch",
	}
	if _, err := api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "session-1"); err == nil || !strings.Contains(err.Error(), "project_title") {
		t.Fatalf("expected project-scoped profile to still require project_title, got: %v", err)
	}
}

func TestResolveAgentProfileForQueryPinsVersionAndSkills(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "video-studio", Name: "Video Studio", Version: 2, BuiltIn: true,
		SystemPromptTemplate: "Project {{.ProjectTitle}} at {{.LocalDateTime}}",
		Skills:               []string{"video-creation"},
		Runtime: agentprofiles.RuntimePolicy{
			Transport: "auto", Provider: "claude-code", ModelID: "claude-sonnet-5-5",
			Capabilities: agentprofiles.RuntimeCapabilities{Browser: agentprofiles.CapabilityRequired},
		},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry}
	req := QueryRequest{
		AgentMode: "multi-agent", AgentProfileID: "video-studio",
		AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Launch"},
		SelectedFolder:      "Chats/Video Studio/projects/launch",
		SelectedSkills:      []string{"existing"},
		Provider:            "codex-cli",
		ModelID:             "gpt-5.4",
		LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{
			Provider: "codex-cli", ModelID: "gpt-5.4",
		}},
	}
	resolved, err := api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if resolved == nil || !strings.Contains(resolved.Prompt, "Project Launch") {
		t.Fatalf("unexpected resolved profile: %+v", resolved)
	}
	if req.AgentProfileVersion != 2 || strings.Join(req.SelectedSkills, ",") != "existing,video-creation" {
		t.Fatalf("request not pinned: version=%d skills=%v", req.AgentProfileVersion, req.SelectedSkills)
	}
	if req.EnableBrowserAccess == nil || !*req.EnableBrowserAccess || req.BrowserMode != "auto" {
		t.Fatalf("profile browser capability was not applied: enabled=%v mode=%q", req.EnableBrowserAccess, req.BrowserMode)
	}
	if req.SelectedFolder != "Chats/Video Studio/projects/launch" {
		t.Fatalf("selected folder = %q", req.SelectedFolder)
	}
	if req.Provider != "claude-code" || req.ModelID != "claude-sonnet-5-5" || req.LLMConfig == nil || req.LLMConfig.Primary.Provider != "claude-code" || req.LLMConfig.Primary.ModelID != "claude-sonnet-5-5" {
		t.Fatalf("profile model was not pinned over the global selection: provider=%q model=%q llm=%+v", req.Provider, req.ModelID, req.LLMConfig)
	}
	if req.LLMConfigSource != llmConfigSourceAgentProfile || !requestLLMConfigOverridesManifest(req) {
		t.Fatalf("profile LLM source is not authoritative: source=%q", req.LLMConfigSource)
	}
	if got := agentProfileRuntimeWorkspace("user-1", req.SelectedFolder); got != "_users/user-1/Chats/Video Studio/projects/launch" {
		t.Fatalf("runtime folder = %q", got)
	}
}

// Delegation consults the profile to build its tool gate; it does not enter the
// product. resolveAgentProfileForQuery runs agentProfiles.Initialize, which for
// a real product seeds the workspace, writes a plan refresh, initializes the
// workflow DB, and runs productdeps.Ensure — work that must happen once per
// turn, not once per sub-agent.
func TestLookupAgentProfileDefinitionDoesNotRunTheRuntimeInitializer(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	profile := agentprofiles.Profile{
		ID: "video-studio", Name: "Video Studio", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "{{.ProjectTitle}}",
		ToolPolicy: agentprofiles.ToolPolicy{
			Mode:    agentprofiles.ToolPolicyModeAllowlist,
			Enabled: []string{"show_video"},
		},
		Runtime: agentprofiles.RuntimePolicy{Transport: "auto"},
	}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	initializerCalls := 0
	if err := registry.RegisterInitializer("video-studio", func(context.Context, agentprofiles.RuntimeContext) error {
		initializerCalls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	api := &StreamingAPI{agentProfiles: registry}
	req := QueryRequest{
		AgentMode: "multi-agent", AgentProfileID: "video-studio",
		SelectedFolder:      "Chats/Video Studio/projects/launch",
		AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Launch"},
	}

	// The per-turn path enters the product, so it initializes exactly once.
	if _, err := api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "session-1"); err != nil {
		t.Fatal(err)
	}
	if initializerCalls != 1 {
		t.Fatalf("resolveAgentProfileForQuery ran the initializer %d times, want 1", initializerCalls)
	}

	// The delegation path only reads the declared surface, so it must not.
	before := req
	resolved, err := api.lookupAgentProfileDefinition(context.Background(), &req, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if initializerCalls != 1 {
		t.Fatalf("lookupAgentProfileDefinition ran the initializer (total %d); delegation would re-seed the workspace per sub-agent", initializerCalls)
	}

	// It must still return what the tool gate consumes...
	if resolved == nil || resolved.Definition.ID != "video-studio" ||
		len(resolved.Definition.ToolPolicy.Enabled) != 1 || resolved.Definition.ToolPolicy.Enabled[0] != "show_video" {
		t.Fatalf("lookup did not return the declared tool surface: %+v", resolved)
	}
	if gate := newProductToolGate(resolved); !gate.enforcing() {
		t.Fatal("tool gate built from the lookup is not enforcing; delegation would get a wider surface than the product declared")
	}

	// ...without rewriting the caller's request the way the per-turn path does.
	if req.Provider != before.Provider || req.ModelID != before.ModelID || len(req.SelectedSkills) != len(before.SelectedSkills) {
		t.Fatalf("lookup mutated the request: provider=%q model=%q skills=%v", req.Provider, req.ModelID, req.SelectedSkills)
	}
}

func TestResolveAgentProfileRejectsWorkspaceEscape(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	_ = registry.RegisterProfile(agentprofiles.Profile{
		ID: "video-studio", Name: "Video Studio", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "{{.ProjectTitle}}", Runtime: agentprofiles.RuntimePolicy{Transport: "auto"},
	})
	api := &StreamingAPI{agentProfiles: registry}
	req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "video-studio", SelectedFolder: "../outside", AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Bad"}}
	if _, err := api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "session-1"); err == nil {
		t.Fatal("workspace escape was accepted")
	}
}

func TestResolveAgentProfileInjectsProjectScopedSecretsIntoNativeEnvironment(t *testing.T) {
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const userID = "user-1"
	const workspacePath = "Chats/Video Studio/projects/launch"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/product.json": `{"schema_version":1,"product":"video-studio","capabilities":{"selected_secrets":["PEXELS_API_KEY"]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	if err := store.UpsertWorkflowSecret(context.Background(), userID, workspacePath, "PEXELS_API_KEY", encryptProfileSecretForTest(t, userID, "test-key")); err != nil {
		t.Fatalf("store project secret: %v", err)
	}

	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(agentprofiles.Profile{
		ID: "video-studio", Name: "Video Studio", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "{{.ProjectTitle}}",
		Runtime:              agentprofiles.RuntimePolicy{Transport: "structured", Capabilities: agentprofiles.RuntimeCapabilities{Secrets: agentprofiles.CapabilityRequired}},
	}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry, chatStore: store}
	req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "video-studio", SelectedFolder: workspacePath, AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Launch"}}
	if _, err := api.resolveAgentProfileForQuery(context.Background(), &req, userID, "session-1"); err != nil {
		t.Fatal(err)
	}
	if len(req.DecryptedSecrets) != 1 || req.DecryptedSecrets[0].Name != "PEXELS_API_KEY" || req.DecryptedSecrets[0].Value != "test-key" {
		t.Fatalf("profile secret selection = %#v, want only the project-scoped secret", req.DecryptedSecrets)
	}
}

func TestResolveCrewProfileUsesOnlyExplicitlySelectedGlobalSecrets(t *testing.T) {
	withVaultSecretGrant(t, "user-1", "SHARED_TOKEN")
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const userID = "user-1"
	const workspacePath = "Chats/Work/projects/site"
	const physicalWorkspacePath = "_users/user-1/Chats/Work/projects/site"
	workspace := &mockWorkspaceAPI{files: map[string]string{
		physicalWorkspacePath + "/product.json":  `{"schema_version":1,"product":"work","id":"site","title":"Site","session_id":"work:site"}`,
		physicalWorkspacePath + "/workflow.json": `{"capabilities":{"selected_secrets":[],"selected_global_secret_names":["SHARED_TOKEN"]}}`,
		workspacePath + "/workflow.json":         `{"capabilities":{"selected_secrets":[],"selected_global_secret_names":["SHARED_TOKEN"]}}`,
	}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	previousGlobals := globalSecrets
	globalSecrets = []globalSecretEntry{
		{Name: "SHARED_TOKEN", Value: "selected-value"},
		{Name: "UNRELATED_TOKEN", Value: "must-not-be-granted"},
	}
	t.Cleanup(func() { globalSecrets = previousGlobals })

	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(agentprofiles.Profile{
		ID: "work", Name: "Crew", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "{{.ProjectTitle}}",
		Runtime: agentprofiles.RuntimePolicy{
			Transport:    "structured",
			Workspace:    agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Work/projects"},
			Conversation: agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject},
			Capabilities: agentprofiles.RuntimeCapabilities{Secrets: agentprofiles.CapabilityRequired},
		},
	}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry, chatStore: store}
	req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "work", AgentProfileConversationKey: "site", SelectedFolder: workspacePath, AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Site"}}
	if _, err := api.resolveAgentProfileForQuery(context.Background(), &req, userID, "session-1"); err != nil {
		t.Fatal(err)
	}
	if req.SelectedGlobalSecrets == nil || len(*req.SelectedGlobalSecrets) != 1 || (*req.SelectedGlobalSecrets)[0] != "SHARED_TOKEN" {
		t.Fatalf("Crew global selection = %#v", req.SelectedGlobalSecrets)
	}
	resolved := api.mergeGlobalSecretsFor(context.Background(), userID, req.DecryptedSecrets, req.SelectedGlobalSecrets)
	if len(resolved) != 1 || resolved[0].Name != "SHARED_TOKEN" || resolved[0].Value != "selected-value" {
		t.Fatalf("Crew received globals outside its explicit allowlist: %#v", resolved)
	}
}

func encryptProfileSecretForTest(t *testing.T, userID, value string) string {
	t.Helper()
	block, err := aes.NewCipher(deriveSecretsKey())
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(value), []byte(userID))
	return base64.StdEncoding.EncodeToString(append(nonce, sealed...))
}

// A per-project Cursor API key must reach the agent the same way a per-project
// Claude Code token does. This is the regression for a live bug: the
// credential branch here originally checked strings.EqualFold(provider,
// "claude-code") explicitly, so a project scoped to cursor-cli saved a key
// through the UI, the save succeeded, and every following turn still failed
// "Authentication required" because this function silently never loaded it.
func TestResolveAgentProfileInjectsProjectScopedCursorAPIKey(t *testing.T) {
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const userID = "user-1"
	const workspacePath = "Chats/Video Studio/projects/launch"
	const cursorKey = "crsr_project_scoped_key"
	if err := store.UpsertWorkflowProviderCredential(context.Background(), userID, workspacePath, cursorCLIProviderID, encryptProviderCredentialForTest(t, cursorKey, userID)); err != nil {
		t.Fatalf("store project Cursor credential: %v", err)
	}

	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(agentprofiles.Profile{
		ID: "video-studio", Name: "Video Studio", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "{{.ProjectTitle}}",
		Runtime: agentprofiles.RuntimePolicy{
			Transport: "auto", Provider: "cursor-cli", ModelID: "auto",
		},
	}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry, chatStore: store}
	req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: "video-studio", SelectedFolder: workspacePath, AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Launch"}}
	resolved, err := api.resolveAgentProfileForQuery(context.Background(), &req, userID, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	// The credential is carried on the RESOLVER'S RESULT, not on req.LLMConfig.
	// req is deserialized from the client body, so routing a credential through
	// it would make the request body a credential source.
	if resolved == nil || resolved.APIKeys == nil || resolved.APIKeys.CursorCLI == nil || *resolved.APIKeys.CursorCLI != cursorKey {
		t.Fatalf("project-scoped Cursor key was not returned on the resolved profile: %+v", resolved)
	}
	if req.LLMConfig != nil && req.LLMConfig.APIKeys != nil {
		t.Fatal("resolver wrote a credential back onto req.LLMConfig; the query path must never read keys from the request")
	}
}

// The query path must take provider keys only from the resolver. A client can
// set llm_config.api_keys on the wire — every ProviderAPIKeys field except
// ClaudeCodeOAuthToken is JSON-visible — so honoring it would let any
// authenticated caller replace the turn's credentials (e.g. redirect
// Azure.Endpoint). It also fires on ordinary chats: the frontend always sends
// `api_keys: {}`, and an empty JSON object unmarshals to a NON-NIL pointer,
// which would wipe every server-resolved key.
func TestClientSuppliedAPIKeysAreNotACredentialSource(t *testing.T) {
	body := []byte(`{"agent_mode":"multi-agent","llm_config":{"api_keys":{"CursorCLI":"attacker-key"}}}`)
	var req QueryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	// Deserialization does populate the field — this is the trap, not a no-op.
	if req.LLMConfig == nil || req.LLMConfig.APIKeys == nil || req.LLMConfig.APIKeys.CursorCLI == nil {
		t.Fatal("expected client-supplied api_keys to deserialize; if this fails the wire contract changed")
	}

	// Empty object is the shape the real frontend sends on every request.
	var empty QueryRequest
	if err := json.Unmarshal([]byte(`{"llm_config":{"api_keys":{}}}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.LLMConfig.APIKeys == nil {
		t.Fatal("expected `api_keys: {}` to produce a NON-NIL pointer; the old branch keyed off exactly this")
	}

	// With no profile resolved there is no credential to apply, so the query
	// path must fall through to the server-resolved keys untouched.
	var resolved *resolvedAgentProfile
	if resolved != nil && resolved.APIKeys != nil {
		t.Fatal("unreachable: guards the shape the query path branches on")
	}
}

func TestProviderOptionRuntimeOptionsFollowTheResolvedBinding(t *testing.T) {
	runtime := agentprofiles.RuntimePolicy{
		ProviderOptions: []agentprofiles.ProviderOption{
			{ID: "claude-code", Provider: "claude-code", ModelID: "claude-fable-5-1", Default: true, Options: map[string]interface{}{"reasoning_effort": "medium"}},
			{ID: "codex-cli", Provider: "codex-cli", ModelID: "gpt-6-astra"},
		},
	}
	got := providerOptionRuntimeOptions(runtime, "claude-code", "claude-fable-5-1")
	if got["reasoning_effort"] != "medium" {
		t.Fatalf("options for the declared binding = %v, want reasoning_effort=medium", got)
	}
	got["reasoning_effort"] = "high"
	if runtime.ProviderOptions[0].Options["reasoning_effort"] != "medium" {
		t.Fatal("the returned map must be a copy, not the profile's own")
	}
	if got := providerOptionRuntimeOptions(runtime, "codex-cli", "gpt-6-astra"); got != nil {
		t.Fatalf("a binding with no options must yield nil, got %v", got)
	}
	if got := providerOptionRuntimeOptions(runtime, "codex-cli", "gpt-5.4"); got != nil {
		t.Fatalf("an undeclared binding must yield nil, got %v", got)
	}
}

func TestResolveProfileRuntimeModelUsesOnlyYAMLProviderOptions(t *testing.T) {
	runtime := agentprofiles.RuntimePolicy{
		Provider: "claude-code", ModelID: "claude-sonnet-5-5",
		ProviderOptions: []agentprofiles.ProviderOption{
			{ID: "claude-code", Label: "Claude Code", Provider: "claude-code", ModelID: "claude-sonnet-5-5", Default: true},
			{ID: "codex", Label: "Codex", Provider: "codex-cli", ModelID: "gpt-6-sol", Models: []string{"gpt-6-sol"}},
			{ID: "cursor", Label: "Cursor", Provider: "cursor-cli", ModelID: "auto"},
		},
	}
	if provider, model := resolveProfileRuntimeModel(runtime, "codex-cli", "gpt-6-sol"); provider != "codex-cli" || model != "gpt-6-sol" {
		t.Fatalf("approved YAML option was not selected: provider=%q model=%q", provider, model)
	}
	if provider, model := resolveProfileRuntimeModel(runtime, "codex-cli", "gpt-5.6-sol"); provider != "claude-code" || model != "claude-sonnet-5-5" {
		t.Fatalf("unapproved provider/model escaped profile allow-list: provider=%q model=%q", provider, model)
	}
}

func TestResolveProfileRuntimeModelAcceptsCatalogModelsWhenUncurated(t *testing.T) {
	runtime := agentprofiles.RuntimePolicy{
		Provider: "claude-code", ModelID: "claude-sonnet-5-5",
		ProviderOptions: []agentprofiles.ProviderOption{
			{ID: "codex", Label: "Codex", Provider: "codex-cli", ModelID: "gpt-6-luna"},
		},
	}
	if provider, model := resolveProfileRuntimeModel(runtime, "codex-cli", "gpt-6-sol"); provider != "codex-cli" || model != "gpt-6-sol" {
		t.Fatalf("catalog model was not accepted: provider=%q model=%q", provider, model)
	}
}

// A provider option's ModelID is only its own default model — Models is the
// full curated list a turn may request within it (matches SparkQuill's real
// product.yaml: one codex-cli option with model_id gpt-6-luna and
// models: [gpt-6-luna, gpt-6.1-sol, gpt-6-sol, gpt-6-astra]). Caught
// live: switching the composer's model from Luna to another Codex model
// only ever matched against ModelID, missed every Models entry, and fell
// through to the profile's unrelated default provider option (claude-code)
// — losing both the requested provider and the conversation's context.
func TestResolveProfileRuntimeModelAcceptsCuratedModelsListEntries(t *testing.T) {
	runtime := agentprofiles.RuntimePolicy{
		Provider: "claude-code", ModelID: "claude-fable-5-1",
		ProviderOptions: []agentprofiles.ProviderOption{
			{ID: "claude-code", Label: "Claude Code", Provider: "claude-code", ModelID: "claude-fable-5-1", Default: true},
			{
				ID: "codex-cli", Label: "Codex", Provider: "codex-cli", ModelID: "gpt-6-luna",
				Models: []string{"gpt-6-luna", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-astra"},
			},
		},
	}
	for _, modelID := range []string{"gpt-6-luna", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-astra"} {
		t.Run(modelID, func(t *testing.T) {
			if provider, model := resolveProfileRuntimeModel(runtime, "codex-cli", modelID); provider != "codex-cli" || model != modelID {
				t.Fatalf("resolveProfileRuntimeModel(codex-cli, %q) = (%q, %q), want (codex-cli, %q)", modelID, provider, model, modelID)
			}
		})
	}
	// A model genuinely outside the curated list still falls back to the
	// profile's default rather than being accepted.
	if provider, model := resolveProfileRuntimeModel(runtime, "codex-cli", "gpt-7-made-up"); provider != "claude-code" || model != "claude-fable-5-1" {
		t.Fatalf("uncurated model escaped the Models allow-list: provider=%q model=%q", provider, model)
	}
}

// A profile turn's selected_folder becomes the agent's read/write root, and
// agentProfileRuntimeWorkspace only re-scopes paths beginning with "Chats" --
// anything else is passed through verbatim. Blocking "../" traversal alone
// therefore left an explicit "_users/<victim>/..." fully usable, which is a
// cross-user workspace read/write, so this pins the authorization gate itself.
func TestCleanAgentProfileWorkspaceRejectsCrossUserPaths(t *testing.T) {
	const caller = "alice"

	for _, tc := range []struct {
		name string
		path string
	}{
		{"another user's chats", "_users/bob/Chats/Video Studio/projects/x"},
		{"another user's root", "_users/bob"},
		{"the users root itself", "_users"},
		{"traversal out of own space", "_users/alice/../bob/Chats"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := cleanAgentProfileWorkspace(tc.path, caller); err == nil {
				t.Fatalf("cleanAgentProfileWorkspace(%q, %q) = %q, want an error", tc.path, caller, got)
			}
		})
	}

	// The legitimate shapes must keep working: the user-relative form the
	// frontend actually sends, and the caller's own canonical _users path.
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"user-relative chats path", "Chats/Video Studio/projects/x", "Chats/Video Studio/projects/x"},
		{"caller's own canonical path", "_users/alice/Chats/x", "_users/alice/Chats/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cleanAgentProfileWorkspace(tc.path, caller)
			if err != nil {
				t.Fatalf("cleanAgentProfileWorkspace(%q, %q) error = %v, want success", tc.path, caller, err)
			}
			if got != tc.want {
				t.Fatalf("cleanAgentProfileWorkspace(%q, %q) = %q, want %q", tc.path, caller, got, tc.want)
			}
		})
	}
}

func TestAgentProfileSessionKeyTracksMCPSelection(t *testing.T) {
	profile := &resolvedAgentProfile{Definition: agentprofiles.Profile{ID: "work", Version: 1}}
	before := agentProfileSessionKey(profile)
	profile.SelectedServers = []string{"Notion"}
	selected := agentProfileSessionKey(profile)
	if before == selected {
		t.Fatal("adding Notion retained stale native tool scope")
	}
	profile.SelectedServers = []string{"Linear", "Notion"}
	ordered := agentProfileSessionKey(profile)
	profile.SelectedServers = []string{"Notion", "Linear"}
	if agentProfileSessionKey(profile) != ordered {
		t.Fatal("selection order changed native scope")
	}
	profile.SelectedServers = nil
	if agentProfileSessionKey(profile) != before {
		t.Fatal("removing all servers retained native tool scope")
	}
}

func TestResolveAgentProfileUsesSavedMCPScopeBeforeRetainedDelivery(t *testing.T) {
	const folder = "Chats/Video Studio/projects/scope"
	workspace := &mockWorkspaceAPI{files: map[string]string{folder + "/product.json": `{"capabilities":{"selected_servers":["Notion"]}}`}}
	host := httptest.NewServer(workspace)
	defer host.Close()
	t.Setenv("WORKSPACE_API_URL", host.URL)
	registry := agentprofiles.NewRegistry()
	if err := registry.RegisterProfile(agentprofiles.Profile{ID: "video-studio", Name: "Video Studio", Version: 1, BuiltIn: true, SystemPromptTemplate: "{{.ProjectTitle}}", Features: []agentprofiles.FeatureBinding{{ID: "mcp"}}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{agentProfiles: registry}
	request := func() QueryRequest {
		return QueryRequest{AgentMode: "multi-agent", AgentProfileID: "video-studio", SelectedFolder: folder, EnabledServers: []string{"Linear"}, AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Scope"}}
	}
	req := request()
	profile, err := api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(req.EnabledServers, ",") != "Notion" {
		t.Fatalf("stale request won over saved selection: %v", req.EnabledServers)
	}
	previous := agentProfileSessionKey(profile)
	workspace.mu.Lock()
	workspace.files[folder+"/product.json"] = `{"capabilities":{"selected_servers":[]}}`
	workspace.mu.Unlock()
	req = request()
	profile, err = api.resolveAgentProfileForQuery(context.Background(), &req, "user-1", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if len(req.EnabledServers) != 0 || previous == agentProfileSessionKey(profile) {
		t.Fatal("deselection retained previous scope or native fingerprint")
	}
}
