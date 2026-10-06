package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/clisecurity"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Use the production Builder query assembly, stopping before a model turn.
// Its saved retained context must describe the actual manifest effort even
// when browser tab state still carries Medium.
func TestWorkflowBuilderEffortMatchesPreparedAgentAndRetainedContext(t *testing.T) {
	env := newProviderAccountsEnv(t, "")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	if err := os.MkdirAll(filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), "Workflow", "w"), 0700); err != nil {
		t.Fatal(err)
	}
	origOS := cliHostOS
	cliHostOS = "darwin"
	t.Cleanup(func() { cliHostOS = origOS })
	t.Setenv("MULTI_USER_MODE", "false")
	env.mock.files["Workflow/w/workflow.json"] = `{"id":"wf-w","label":"Workflow","access":{"owners":["alice"]},"capabilities":{"llm_config":{"mode":"explicit","builder_llm":{"provider":"claude-code","model_id":"claude-sonnet-5-5","options":{"reasoning_effort":"high"}}}}}`
	env.api.eventStore = events.NewEventStore(100)
	env.api.logger = loggerv2.NewNoop()
	env.api.activeSessions = make(map[string]*ActiveSessionInfo)
	env.api.lastQueryRequests = make(map[string]QueryRequest)
	var err error
	env.api.cliSecurityStore, err = clisecurity.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env.api.mcpConfigPath = filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(env.api.mcpConfigPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	env.api.agentCancelFuncs = make(map[string]context.CancelFunc)
	env.api.sessionQueryIDs = make(map[string][]string)
	env.api.conversationHistory = make(map[string][]llmtypes.MessageContent)
	prepared := make(chan string, 1)
	env.api.internalPreparedAgent = func(_ context.Context, agent *mcpagent.Agent) bool {
		info := mcpagent.ReadAgentRuntimeInfo(agent)
		snapshot := env.api.captureChatHistoryAgentRuntime("chat", string(info.Provider), info.EffectiveModelID, "Workflow/w", agent)
		if snapshot.ModelOptionsKey != workflowModelOptionsKey(info.LLMConfig.Options) {
			t.Error("snapshot lost launched options")
		}
		raw, _ := json.Marshal(snapshot)
		var restored ChatHistoryAgentRuntime
		if err := json.Unmarshal(raw, &restored); err != nil || restored.ModelOptionsKey != snapshot.ModelOptionsKey {
			t.Error("runtime options fingerprint did not survive persistence")
		}
		effort, _ := info.LLMConfig.Options["reasoning_effort"].(string)
		prepared <- effort
		return true
	}
	req := QueryRequest{Query: "Inspect the plan", AgentMode: "workflow_phase", SelectedFolder: "Workflow/w", PhaseID: "workflow-builder", PresetQueryID: "wf-w", Provider: "claude-code", ModelID: "claude-sonnet-5-5", LLMConfig: &orchestrator.LLMConfig{Primary: orchestrator.LLMModel{Provider: "claude-code", ModelID: "claude-sonnet-5-5", Options: map[string]interface{}{"reasoning_effort": "medium"}}}}
	w := httptest.NewRecorder()
	r := sharedSecretsRequest(http.MethodPost, "/api/query", "alice", req)
	r.Header.Set("X-Session-ID", "chat")
	env.api.handleQuery(w, r)
	select {
	case effort := <-prepared:
		if effort != "high" {
			t.Fatalf("prepared effort=%q", effort)
		}
	case <-time.After(15 * time.Second):
		raw, _ := json.Marshal(env.api.eventStore.GetAllEventsRaw("chat"))
		t.Fatalf("query not prepared: status=%d body=%s events=%s", w.Code, w.Body.String(), raw)
	}
	deadline := time.Now().Add(5 * time.Second)
	for env.api.isSessionBusy("chat") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if env.api.isSessionBusy("chat") {
		t.Fatal("prepared query did not stop")
	}
	env.api.lastQueryMu.RLock()
	last := env.api.lastQueryRequests["chat"]
	env.api.lastQueryMu.RUnlock()
	if last.LLMConfig == nil || last.LLMConfig.Primary.Options["reasoning_effort"] != "high" {
		t.Fatal("retained context did not record launched High effort")
	}
	if req.LLMConfig.Primary.Options["reasoning_effort"] != "medium" {
		t.Fatal("mutated incoming tab configuration")
	}
}
