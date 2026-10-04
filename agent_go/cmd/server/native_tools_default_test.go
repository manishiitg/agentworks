package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// Owner decision 2026-09-29: native agent tools (hybrid) are on for every
// coding-CLI turn type except workflow step agents, unless the workflow,
// Crew or Code turns its "Native agent tools" switch off. Read-only
// principals stay off.

// queryDecidedToolsMode sends req through handleQuery and returns the
// agent-tools mode the turn decided.
func (e *providerAccountsEnv) queryDecidedToolsMode(t *testing.T, user, sessionID string, req QueryRequest) string {
	t.Helper()
	decided := "<not decided>"
	e.api.internalAgentToolsModeDecided = func(_ string, mode string) bool {
		decided = normalizeAgentToolsMode(mode)
		return true
	}
	if req.LLMConfig == nil {
		req.LLMConfig = &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "claude-code", ModelID: "sonnet"}}
	}
	w := httptest.NewRecorder()
	httpReq := sharedSecretsRequest(http.MethodPost, "/api/query", user, req)
	httpReq.Header.Set("X-Session-ID", sessionID)
	e.api.handleQuery(w, httpReq)
	return decided
}

func (e *providerAccountsEnv) setWorkflowNativeTools(t *testing.T, enabled *bool) {
	t.Helper()
	capabilities := map[string]interface{}{}
	if enabled != nil {
		capabilities["native_agent_tools"] = *enabled
	}
	data, _ := json.Marshal(map[string]interface{}{
		"id": "wf-w", "label": "Weekly report", "created_by": "alice",
		"access":       map[string]interface{}{"owners": []string{"alice"}, "editors": []string{"bob"}, "readers": []string{"carol"}},
		"capabilities": capabilities,
	})
	e.mock.mu.Lock()
	e.mock.files["Workflow/w/workflow.json"] = string(data)
	e.mock.mu.Unlock()
}

func TestNativeToolsOnForEveryTurnTypeExceptSteps(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	env.setWorkflowNativeTools(t, nil)
	workflowTurn := func(req QueryRequest) QueryRequest {
		req.Query, req.AgentMode, req.SelectedFolder = "go", "workflow_phase", "Workflow/w"
		return req
	}
	cases := []struct {
		name, user, session string
		req                 QueryRequest
		want                string
	}{
		{"interactive Builder chat", "alice", "chat-1", workflowTurn(QueryRequest{}), "full"},
		{"scheduled workflow turn", "alice", "schedule-w-1", workflowTurn(QueryRequest{TriggeredBy: "cron"}), "full"},
		{"webhook turn", "alice", "hook-1", workflowTurn(QueryRequest{TriggeredBy: "webhook"}), "full"},
		{"Pulse turn", "alice", "pulse-1", workflowTurn(QueryRequest{PulseLifecycleTurn: true}), "full"},
		{"Slack turn", "alice", "bot-slack--1", workflowTurn(QueryRequest{BotPlatform: "slack"}), "full"},
		{"read-only viewer", "carol", "viewer-1", workflowTurn(QueryRequest{}), "mcp_only"},
		{"workflow step (child session of a run)", "alice", "step-1", workflowTurn(QueryRequest{ParentSessionID: "run-1"}), "mcp_only"},
		{"Pulse reviewer child", "alice", "pulse-child-1", workflowTurn(QueryRequest{ParentSessionID: "run-1", SessionKind: "pulse_reviewer"}), "full"},
	}
	for _, tc := range cases {
		if got := env.queryDecidedToolsMode(t, tc.user, tc.session, tc.req); got != tc.want {
			t.Errorf("%s: decided %q, want %q", tc.name, got, tc.want)
		}
	}

	// No switch any more (2026-10-03): a workflow that saved "off" earlier decides exactly what an untouched workflow decides, for every turn type.
	turns := []QueryRequest{workflowTurn(QueryRequest{}), workflowTurn(QueryRequest{TriggeredBy: "cron"}), workflowTurn(QueryRequest{BotPlatform: "slack"})}
	want := make([]string, len(turns))
	for i, req := range turns {
		want[i] = env.queryDecidedToolsMode(t, "alice", "default-1", req)
	}
	off := false
	env.setWorkflowNativeTools(t, &off)
	for i, req := range turns {
		if got := env.queryDecidedToolsMode(t, "alice", "off-1", req); got != want[i] {
			t.Errorf("a stored off decided %q, want %q like an untouched workflow (%+v)", got, want[i], req)
		}
	}

	// Step agents are built by the orchestrator and never take this path; the
	// headless workflow executor ("workflow" mode) stays off.
	if env.api.workflowChatNativeAgentTools(context.Background(), QueryRequest{AgentMode: "workflow", SelectedFolder: "Workflow/w"}, "s", false) {
		t.Error("headless workflow executor got native tools")
	}
}

// A Slack DM Crew turn by the Crew's owner keeps native tools; the Crew's
// switch set to false turns them off.
func TestNativeToolsOnForSlackCrewTurn(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	const crewRoot = "_users/alice/Chats/Work/projects/site"
	registry := agentprofiles.NewRegistry()
	for _, profile := range workproduct.BuiltinAgentProfiles() {
		profile.Product = crewProfileID
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	env.api.agentProfiles = registry
	crewTurn := func(workflowJSON string) string {
		env.mock.mu.Lock()
		env.mock.files[crewRoot+"/product.json"] = `{"schema_version":1,"product":"work","id":"site","title":"Site","session_id":"work:site"}`
		env.mock.files[crewRoot+"/workflow.json"] = workflowJSON
		env.mock.mu.Unlock()
		req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: crewProfileID, AgentProfileConversationKey: "site", SelectedFolder: "Chats/Work/projects/site", BotPlatform: "slack", AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Site"}}
		resolved, err := env.api.resolveAgentProfileForQuery(context.Background(), &req, "alice", "bot-slack--dm")
		if err != nil {
			t.Fatalf("resolve Crew turn: %v", err)
		}
		return normalizeAgentToolsMode(resolved.Definition.Runtime.AgentTools.Mode)
	}
	if mode := crewTurn(`{"capabilities":{}}`); mode != "full" {
		t.Fatalf("Slack DM Crew turn decided %q, want full", mode)
	}
	// No switch any more (2026-09-29): an older Crew's stored "off" is ignored.
	if mode := crewTurn(`{"capabilities":{"native_agent_tools":false}}`); mode != "full" {
		t.Fatalf("a Crew with a stored off decided %q, want full: native tools are always on", mode)
	}
}

// Exercise handleQuery's shared mode decision, rather than only the profile
// constant: Vault's prior mcp_only override installed Muse's denying hook.
func TestVaultBuilderRequestsFullNativeToolsThroughSharedQuery(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	registry := agentprofiles.NewRegistry()
	profile := caplayerproduct.BuiltinAgentProfile()
	profile.Product = "mcp-gateway"
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	env.api.agentProfiles = registry
	req := QueryRequest{Query: "Read vault-access", AgentMode: "multi-agent", AgentProfileID: profile.ID, SelectedFolder: caplayerproduct.WorkspaceRoot, AgentProfileContext: agentprofiles.PromptContext{ProjectTitle: "Vault"}, LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "muse-cli", ModelID: "us.anthropic.claude-sonnet-4-20250514-v1:0"}}}
	if mode := env.queryDecidedToolsMode(t, "admin", "vault-native-test", req); mode != "full" {
		t.Fatalf("Vault Muse query decided %q, want full native tools", mode)
	}
	// A retained CLI's launch definition must change, so the old mcp_only hook
	// cannot survive by resuming the previous provider process.
	previous := profile
	previous.Version--
	previous.Runtime.AgentTools.Mode = "mcp_only"
	if agentProfileSessionKey(&resolvedAgentProfile{Definition: previous}) == agentProfileSessionKey(&resolvedAgentProfile{Definition: profile}) {
		t.Fatal("native tool change would reuse the restricted CLI")
	}
}
