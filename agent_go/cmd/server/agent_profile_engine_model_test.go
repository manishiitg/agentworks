package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func engineModelTestProfile() agentprofiles.Profile {
	return agentprofiles.Profile{
		ID: "p", Name: "P", Version: 1,
		Runtime: agentprofiles.RuntimePolicy{ProviderOptions: []agentprofiles.ProviderOption{
			{ID: "claude-code", Label: "Claude Code", Provider: "claude-code", ModelID: "claude-fable-5-1", Default: true},
			{ID: "codex-cli", Label: "Codex", Provider: "codex-cli", ModelID: "gpt-6-astra"},
		}},
	}
}

// A model choice must be one the platform's catalog lists for the engine's
// provider — the same list the composer's switcher was filled from.
func TestProfileQueryModelMustBelongToEngineProvider(t *testing.T) {
	profile := engineModelTestProfile()
	conversation := ProductConversationRecord{SessionID: "s", WorkspacePath: "Chats/P", ConversationKey: "main"}

	req, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "claude-code", ModelID: "claude-opus-5-5"}, conversation)
	if err != nil {
		t.Fatalf("catalog model rejected: %v", err)
	}
	if req.Provider != "claude-code" || req.ModelID != "claude-opus-5-5" {
		t.Fatalf("provider/model = %q/%q", req.Provider, req.ModelID)
	}

	req, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "claude-code"}, conversation)
	if err != nil || req.ModelID != "claude-fable-5-1" {
		t.Fatalf("no model_id should keep the option's own model: %q, %v", req.ModelID, err)
	}

	_, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "claude-code", ModelID: "gpt-6-astra"}, conversation)
	if err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Fatalf("a Codex model under Claude Code should be refused, got %v", err)
	}
}

func TestProfileQueryUsesProjectLLMConfigWithoutBrowserEngineFields(t *testing.T) {
	profile := engineModelTestProfile()
	conversation := ProductConversationRecord{
		SessionID:       "s",
		WorkspacePath:   "Chats/P",
		ConversationKey: "main",
		ProjectLLMConfig: &workflowtypes.PresetLLMConfig{
			SchemaVersion: 2,
			Mode:          "explicit",
			BuilderLLM: &workflowtypes.AgentLLMConfig{
				Provider: "codex-cli",
				ModelID:  "gpt-5.6-sol",
				Options:  map[string]interface{}{"reasoning_effort": "high"},
			},
		},
	}
	profile.Runtime.ProviderOptions[1].Models = []string{"gpt-6-astra", "gpt-5.6-sol"}
	profile.Runtime.ProviderOptions[1].ReasoningEfforts = []string{"medium", "high"}

	req, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "scheduled message"}, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if req.Provider != "codex-cli" || req.ModelID != "gpt-5.6-sol" || req.ReasoningEffort != "high" {
		t.Fatalf("project runtime = %q/%q/%q", req.Provider, req.ModelID, req.ReasoningEffort)
	}
}

func TestProfileQueryUsesLegacyConversationRuntimeWithoutProjectConfig(t *testing.T) {
	profile := engineModelTestProfile()
	profile.Runtime.ProviderOptions[1].Models = []string{"gpt-6-astra", "gpt-5.6-sol"}
	profile.Runtime.ProviderOptions[1].ReasoningEfforts = []string{"medium", "high"}
	conversation := ProductConversationRecord{
		SessionID:       "s",
		WorkspacePath:   "Chats/P",
		ConversationKey: "main",
		Provider:        "codex-cli",
		ModelID:         "gpt-5.6-sol",
		ReasoningEffort: "high",
	}

	req, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "scheduled message"}, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if req.Provider != "codex-cli" || req.ModelID != "gpt-5.6-sol" || req.ReasoningEffort != "high" {
		t.Fatalf("legacy project runtime = %q/%q/%q", req.Provider, req.ModelID, req.ReasoningEffort)
	}
}

// A project conversation can change coding-agent provider. The durable
// AgentWorks conversation remains stable while the native CLI is relaunched.
func TestConversationProviderChangeRequestsRestart(t *testing.T) {
	store, _ := memoryProductConversationStore()
	profile := engineModelTestProfile()
	binding := productConversationBinding{ConversationKey: "main", WorkspacePath: "Chats/P", Title: "P"}
	ctx := t.Context()
	if _, err := store.resolveOrCreate(ctx, "u", profile, binding, ""); err != nil {
		t.Fatalf("resolveOrCreate: %v", err)
	}
	bound, _, err := store.bindRuntime(ctx, "u", profile, "main", "codex-cli", "gpt-6-astra", "")
	if err != nil || bound != "codex-cli" {
		t.Fatalf("first bind = %q, %v", bound, err)
	}
	bound, restart, err := store.bindRuntime(ctx, "u", profile, "main", "claude-code", "claude-fable-5-1", "")
	if err != nil || bound != "claude-code" || !restart {
		t.Fatalf("provider change = bound %q restart %v, %v", bound, restart, err)
	}
	if _, err := store.rotate(ctx, "u", profile, binding); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	bound, _, err = store.bindRuntime(ctx, "u", profile, "main", "claude-code", "claude-fable-5-1", "")
	if err != nil || bound != "claude-code" {
		t.Fatalf("a new conversation should bind afresh, got %q, %v", bound, err)
	}
}

// An engine that curates its own Models offers exactly those, refusing
// anything the platform catalog would otherwise allow for its provider.
func TestProfileQueryModelCurationNarrowsAcceptedModels(t *testing.T) {
	profile := engineModelTestProfile()
	profile.Runtime.ProviderOptions[1].Models = []string{"gpt-6-astra", "gpt-5.6-sol"}
	conversation := ProductConversationRecord{SessionID: "s", WorkspacePath: "Chats/P", ConversationKey: "main"}

	if _, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "codex-cli", ModelID: "gpt-5.6-sol"}, conversation); err != nil {
		t.Fatalf("curated model rejected: %v", err)
	}
	_, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "codex-cli", ModelID: "gpt-5.5"}, conversation)
	if err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Fatalf("a model outside the curated list should be refused, got %v", err)
	}
}

// A reasoning effort must be one the engine declares; an engine with no
// declared levels accepts none at all.
func TestProfileQueryReasoningEffortMustBeDeclared(t *testing.T) {
	profile := engineModelTestProfile()
	profile.Runtime.ProviderOptions[0].ReasoningEfforts = []string{"low", "medium", "high"}
	conversation := ProductConversationRecord{SessionID: "s", WorkspacePath: "Chats/P", ConversationKey: "main"}

	req, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "claude-code", ReasoningEffort: "high"}, conversation)
	if err != nil || req.ReasoningEffort != "high" {
		t.Fatalf("declared reasoning effort rejected: %q, %v", req.ReasoningEffort, err)
	}
	_, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "claude-code", ReasoningEffort: "extreme"}, conversation)
	if err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Fatalf("an undeclared reasoning effort should be refused, got %v", err)
	}
	_, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "codex-cli", ReasoningEffort: "high"}, conversation)
	if err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Fatalf("an engine with no declared levels should refuse any reasoning effort, got %v", err)
	}
}

func TestCrewAgyHighEffortChatRequest(t *testing.T) {
	manifest, err := workproduct.WorkManifest()
	if err != nil {
		t.Fatal(err)
	}
	conversation := ProductConversationRecord{SessionID: "work:project:test", WorkspacePath: "Chats/Work/projects/test", ConversationKey: "test"}
	req, err := queryRequestForAgentProfileChat(manifest.Profile, AgentProfileChatRequest{
		Message: "hello", Engine: "agy-cli", ModelID: "gemini-3.8-flash-high", ReasoningEffort: "high",
	}, conversation)
	if err != nil {
		t.Fatalf("Crew AGY chat request rejected: %v", err)
	}
	if req.Provider != "agy-cli" || req.ModelID != "gemini-3.8-flash-high" || req.ReasoningEffort != "high" {
		t.Fatalf("Crew AGY runtime = %q/%q/%q", req.Provider, req.ModelID, req.ReasoningEffort)
	}
}

// A provider, model or reasoning-effort change on an existing conversation must
// be reported as needing a restart — the running tmux-backed CLI process
// only reads its launch flags once, so the caller must close it or the next
// turn silently keeps generating with the old value.
func TestConversationRuntimeChangeReportsRestartNeeded(t *testing.T) {
	store, _ := memoryProductConversationStore()
	profile := engineModelTestProfile()
	binding := productConversationBinding{ConversationKey: "main", WorkspacePath: "Chats/P", Title: "P"}
	ctx := t.Context()
	if _, err := store.resolveOrCreate(ctx, "u", profile, binding, ""); err != nil {
		t.Fatalf("resolveOrCreate: %v", err)
	}
	if _, restart, err := store.bindRuntime(ctx, "u", profile, "main", "codex-cli", "gpt-6-astra", "medium"); err != nil || restart {
		t.Fatalf("first bind should never need a restart: restart=%v err=%v", restart, err)
	}
	if _, restart, err := store.bindRuntime(ctx, "u", profile, "main", "codex-cli", "gpt-6-astra", "medium"); err != nil || restart {
		t.Fatalf("unchanged model/effort should not need a restart: restart=%v err=%v", restart, err)
	}
	if _, restart, err := store.bindRuntime(ctx, "u", profile, "main", "codex-cli", "gpt-5.6-sol", "medium"); err != nil || !restart {
		t.Fatalf("a model change should need a restart: restart=%v err=%v", restart, err)
	}
	if _, restart, err := store.bindRuntime(ctx, "u", profile, "main", "codex-cli", "gpt-5.6-sol", "high"); err != nil || !restart {
		t.Fatalf("a reasoning-effort change should need a restart: restart=%v err=%v", restart, err)
	}
	if _, restart, err := store.bindRuntime(ctx, "u", profile, "main", "codex-cli", "gpt-5.6-sol", "high"); err != nil || restart {
		t.Fatalf("the now-current model/effort should not need another restart: restart=%v err=%v", restart, err)
	}
	if bound, restart, err := store.bindRuntime(ctx, "u", profile, "main", "claude-code", "claude-sonnet-5-5", "high"); err != nil || !restart || bound != "claude-code" {
		t.Fatalf("a provider change should be recorded and need a restart: bound=%q restart=%v err=%v", bound, restart, err)
	}
}

func TestConversationRuntimeChangeReportsRestartForMCPAndSkills(t *testing.T) {
	store, _ := memoryProductConversationStore()
	profile := engineModelTestProfile()
	binding := productConversationBinding{ConversationKey: "main", WorkspacePath: "Chats/P", Title: "P"}
	ctx := t.Context()
	if _, err := store.resolveOrCreate(ctx, "u", profile, binding, ""); err != nil {
		t.Fatalf("resolveOrCreate: %v", err)
	}
	bind := func(servers, skills, workflowContextPaths []string) bool {
		_, restart, err := store.bindRuntimeConfiguration(ctx, "u", profile, "main", "muse-cli", "muse-spark-1.3-contributor", "", servers, skills, workflowContextPaths)
		if err != nil {
			t.Fatal(err)
		}
		return restart
	}
	if bind([]string{"NO_SERVERS"}, nil, nil) {
		t.Fatal("first runtime binding requested a restart")
	}
	if bind([]string{"NO_SERVERS"}, nil, nil) {
		t.Fatal("unchanged MCP selection requested a restart")
	}
	if !bind([]string{"google_sheets"}, nil, nil) {
		t.Fatal("changed MCP selection did not request a restart")
	}
	if bind([]string{"GOOGLE_SHEETS", "google_sheets"}, nil, nil) {
		t.Fatal("equivalent MCP selection requested a restart")
	}
	if !bind([]string{"google_sheets"}, []string{"work-dashboard"}, nil) {
		t.Fatal("changed skill selection did not request a restart")
	}
	if bind([]string{"google_sheets"}, []string{"work-dashboard"}, []string{"Workflow/research"}) {
		t.Fatal("changed workflow reference selection requested a runtime restart")
	}
	if bind([]string{"google_sheets"}, []string{"work-dashboard"}, []string{"workflow/RESEARCH", "Workflow/research"}) {
		t.Fatal("equivalent workflow reference selection requested a restart")
	}
}

// Switching provider mid-chat must not carry the old provider's account along.
func TestProfileQueryDropsInheritedAccountOnProviderSwitch(t *testing.T) {
	profile := engineModelTestProfile()
	conversation := ProductConversationRecord{SessionID: "s", WorkspacePath: "Chats/P", ConversationKey: "main", Provider: "claude-code", ConnectionID: "acct-claude"}

	req, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "codex-cli"}, conversation)
	if err != nil {
		t.Fatal(err)
	}
	if req.ConnectionID != "" {
		t.Fatalf("codex must not inherit the claude account, got %q", req.ConnectionID)
	}
	req, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "claude-code"}, conversation)
	if err != nil || req.ConnectionID != "acct-claude" {
		t.Fatalf("same provider keeps its account, got %q %v", req.ConnectionID, err)
	}
	req, err = queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: "hi", Engine: "codex-cli", ConnectionID: "acct-codex"}, conversation)
	if err != nil || req.ConnectionID != "acct-codex" {
		t.Fatalf("an explicit account is kept, got %q %v", req.ConnectionID, err)
	}
}
