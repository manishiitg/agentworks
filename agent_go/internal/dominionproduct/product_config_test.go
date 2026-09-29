package dominionproduct

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestDominionManifestDeclaresProjectScopeAndNarrowAllowlist(t *testing.T) {
	manifest, err := DominionManifest()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Profile.ID != "dominion" {
		t.Fatalf("unexpected profile id: %q", manifest.Profile.ID)
	}
	// Project, not global -- same reasoning as Finance's own profile: global
	// scope makes provider_options non-authoritative and skips this
	// profile's own prompt.file in favor of the dynamic delegation prompt.
	if manifest.Profile.Scope != agentprofiles.ProfileScopeProject {
		t.Fatalf("dominion must declare scope: project, got %q", manifest.Profile.Scope)
	}
	if manifest.Profile.Runtime.Provider != "claude-code" || manifest.Profile.Runtime.ModelID != "claude-sonnet-5-5" {
		t.Fatalf("expected provider=claude-code model_id=claude-sonnet-5-5, got provider=%q model_id=%q", manifest.Profile.Runtime.Provider, manifest.Profile.Runtime.ModelID)
	}
	// Dominion shares Video Studio's persistent Claude session shape. Its
	// mcp_only tool policy remains the containment boundary.
	if manifest.Profile.Runtime.Transport != "auto" {
		t.Fatalf("dominion must declare runtime.transport: auto for persistent Claude sessions, got transport=%q", manifest.Profile.Runtime.Transport)
	}
	if manifest.Profile.Runtime.Workspace.Root != "Chats" {
		t.Fatalf("dominion profile-chat workspace root = %q, want Chats", manifest.Profile.Runtime.Workspace.Root)
	}
	if manifest.Profile.Runtime.Workspace.Mode != agentprofiles.WorkspaceModeFixed || manifest.Profile.Runtime.Conversation.Mode != agentprofiles.ConversationModeSingleton {
		t.Fatalf("dominion must use one server-owned durable conversation: workspace=%+v conversation=%+v", manifest.Profile.Runtime.Workspace, manifest.Profile.Runtime.Conversation)
	}
	if !strings.EqualFold(manifest.Profile.Runtime.AgentTools.Mode, "mcp_only") {
		t.Fatalf("dominion must declare agent_tools.mode: mcp_only explicitly, got %q", manifest.Profile.Runtime.AgentTools.Mode)
	}
	if len(manifest.Profile.Runtime.ProviderOptions) != 1 || manifest.Profile.Runtime.ProviderOptions[0].Provider != "claude-code" {
		t.Fatalf("dominion must curate runtime.provider_options to exactly claude-code, got %+v", manifest.Profile.Runtime.ProviderOptions)
	}
	// Dominion has no legitimate desktop use case -- it must never silently
	// fall back to an ambient `claude` CLI login, regardless of how the
	// server happens to be configured.
	if !manifest.Profile.Runtime.RequireProviderToken {
		t.Fatal("dominion must declare runtime.require_provider_token: true")
	}
	if !manifest.Profile.ToolPolicy.IsAllowlist() {
		t.Fatal("dominion must declare tool_policy.mode: allowlist -- an unrestricted chat over trading data is exactly the gap this profile exists to close")
	}
	// execute_shell_command is intentionally included -- it's the platform's
	// only call path to a custom tool (get_api_spec discovery + curl to
	// $MCP_CUSTOM/<tool>), the same pattern Video Studio's product.yaml
	// documents. See the product.yaml comment for why this isn't "general
	// shell access" in practice, and prompts/system-prompt.md for how the
	// model is told to scope its use.
	// add_dominion_watchlist_symbol is deliberately ADD-only -- see
	// dominion_watchlist_tool.go's own comment. No matching remove/edit tool
	// should ever be added to this list without a very deliberate decision.
	wantEnabled := map[string]bool{"query_dominion_source": false, "add_dominion_watchlist_symbol": false, "execute_shell_command": false}
	for _, name := range manifest.Profile.ToolPolicy.Enabled {
		if _, expected := wantEnabled[name]; !expected {
			t.Fatalf("unexpected tool in allowlist: %q -- dominion chat should have no write beyond adding a watchlist symbol, no delegation, no schedule, no workflow-execution tools", name)
		}
		wantEnabled[name] = true
	}
	for name, found := range wantEnabled {
		if !found {
			t.Fatalf("expected tool_policy.enabled to include %q", name)
		}
	}
	wantTools := map[string]bool{"dominion.query-source": false, "dominion.add-watchlist-symbol": false}
	for _, binding := range manifest.Profile.Tools {
		if _, expected := wantTools[binding.ID]; !expected {
			t.Fatalf("unexpected tool binding %q", binding.ID)
		}
		wantTools[binding.ID] = true
	}
	for id, found := range wantTools {
		if !found {
			t.Fatalf("expected profile.tools[] to bind %q -- without this, registerAgentProfileTools registers nothing for a dominion chat even though the ToolFactory exists", id)
		}
	}
	if manifest.UI.Surface != "dominion" {
		t.Fatalf("unexpected ui.surface: %q", manifest.UI.Surface)
	}
	if len(manifest.Workflows.Enabled) != 0 {
		t.Fatalf("dominion has no fixed pipelines; workflows.enabled should stay empty: %v", manifest.Workflows.Enabled)
	}
}

func TestBuiltinAgentProfileValidatesAndResolvesProjectScope(t *testing.T) {
	profile := BuiltinAgentProfile()
	if err := agentprofiles.Validate(profile); err != nil {
		t.Fatalf("built-in profile failed validation: %v", err)
	}
	if profile.EffectiveScope() != agentprofiles.ProfileScopeProject {
		t.Fatalf("EffectiveScope() = %q, want project", profile.EffectiveScope())
	}
	if profile.SystemPromptTemplate == "" {
		t.Fatal("expected a non-empty rendered system prompt template")
	}
	if !profile.BuiltIn {
		t.Fatal("expected BuiltIn to be true")
	}
}

func TestBuiltinAgentProfilesReturnsExactlyOneVersion(t *testing.T) {
	profiles := BuiltinAgentProfiles()
	if len(profiles) != 1 {
		t.Fatalf("expected exactly one built-in profile, got %d", len(profiles))
	}
	if profiles[0].Version != 1 {
		t.Fatalf("expected version 1, got %d", profiles[0].Version)
	}
}

func TestRenderPromptSucceedsAgainstAPromptContext(t *testing.T) {
	profile := BuiltinAgentProfile()
	rendered, err := agentprofiles.RenderPrompt(profile, agentprofiles.PromptContext{
		ProjectTitle:  "Dominion",
		LocalDateTime: "Monday, 1 January 2026 at 9:00 AM UTC",
	})
	if err != nil {
		t.Fatalf("RenderPrompt failed: %v", err)
	}
	if rendered == "" {
		t.Fatal("expected non-empty rendered prompt")
	}
}

func TestRegisterAgentProfileRuntimeRegistersTheQueryToolFactory(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	if err := RegisterAgentProfileRuntime(registry, "http://127.0.0.1:0"); err != nil {
		t.Fatalf("RegisterAgentProfileRuntime failed: %v", err)
	}
	for _, profile := range BuiltinAgentProfiles() {
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatalf("RegisterProfile failed: %v", err)
		}
	}
	tool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "dominion.query-source"}, agentprofiles.ToolRuntimeContext{UserID: "u1", SessionID: "s1"})
	if err != nil {
		t.Fatalf("BuildTool failed: %v", err)
	}
	if tool.Name != "query_dominion_source" {
		t.Fatalf("unexpected tool name: %q", tool.Name)
	}
}

func TestRegisterAgentProfileRuntimeRegistersTheAddWatchlistToolFactory(t *testing.T) {
	registry := agentprofiles.NewRegistry()
	if err := RegisterAgentProfileRuntime(registry, "http://127.0.0.1:0"); err != nil {
		t.Fatalf("RegisterAgentProfileRuntime failed: %v", err)
	}
	for _, profile := range BuiltinAgentProfiles() {
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatalf("RegisterProfile failed: %v", err)
		}
	}
	tool, err := registry.BuildTool(agentprofiles.ToolBinding{ID: "dominion.add-watchlist-symbol"}, agentprofiles.ToolRuntimeContext{UserID: "u1", SessionID: "s1"})
	if err != nil {
		t.Fatalf("BuildTool failed: %v", err)
	}
	if tool.Name != "add_dominion_watchlist_symbol" {
		t.Fatalf("unexpected tool name: %q", tool.Name)
	}
	// Pin the parameter schema names -- if these drift, the model's tool call
	// silently stops matching what dominion_watchlist_tool.go actually reads
	// out of args via stringArg().
	params, _ := tool.Parameters["properties"].(map[string]interface{})
	if params == nil {
		t.Fatal("expected tool.Parameters.properties to be a non-nil object")
	}
	for _, want := range []string{"symbol", "tier"} {
		if _, ok := params[want]; !ok {
			t.Fatalf("expected parameter %q in add_dominion_watchlist_symbol's schema", want)
		}
	}
}

func TestDominionSourceDBPathsMatchTheFrontendAdapters(t *testing.T) {
	// Duplicated by hand in frontend/src/products/dominion/adapters/*.ts
	// (see dominion_query_tool.go's own comment). Pins the Go side's exact
	// set so a source can't silently go missing or get renamed without a
	// test failing.
	want := map[string]string{
		"trading": "Workflow/tectonicusadaytrading/db/db.sqlite",
	}
	if len(dominionSourceDBPaths) != len(want) {
		t.Fatalf("expected %d sources, got %d: %v", len(want), len(dominionSourceDBPaths), dominionSourceDBPaths)
	}
	for source, path := range want {
		if got := dominionSourceDBPaths[source]; got != path {
			t.Fatalf("source %q: got path %q, want %q", source, got, path)
		}
	}
}
