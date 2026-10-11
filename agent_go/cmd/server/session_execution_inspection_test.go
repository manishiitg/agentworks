package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	agentEvents "github.com/manishiitg/mcpagent/events"
)

// Uses the actual durable transcript writer and HTTP reader. Two dispatches of
// one step remain distinct after completion, and read-only inspection cannot
// cancel either one or expose another chat's evidence.
func TestInspectSessionExecutionDurableIdentityAndAccess(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	srv := httptest.NewServer(workspace)
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	api := lifecycleTestAPI()
	api.activeSessions = map[string]*ActiveSessionInfo{"chat": {SessionID: "chat", WorkspacePath: "Workflow/example", UserID: GetDefaultUserID()}}
	for _, id := range []string{"dispatch-1", "dispatch-2"} {
		api.bgAgentRegistry.Register("chat", &BackgroundAgent{ID: id, Name: "Research", Kind: "workflow_step", Status: BGAgentRunning, CreatedAt: time.Now(), Metadata: map[string]string{"step_id": "research", "run_folder": "iteration-1/default"}})
		api.emitBackgroundAgentStarted("chat", id, "Research", "", "", orchEvents.ExecutionKindWorkflowStep)
	}
	file := orchEvents.BackgroundAgentTranscriptPath("Workflow/example", "chat", "dispatch-1")
	content, _, _ := readFileFromWorkspace(context.Background(), file)
	transcript, err := orchEvents.ParseBackgroundAgentTranscript(content)
	if err != nil || transcript == nil {
		t.Fatalf("start record: %v", err)
	}
	for _, event := range []*agentEvents.AgentEvent{
		{Data: &agentEvents.SystemPromptEvent{Content: "Inspect the provided sources MCP_API_TOKEN=fixture-prompt-secret"}},
		{Data: &agentEvents.UserMessageEvent{Role: "user", Content: "Find the relevant facts"}},
		{Data: &agentEvents.ToolCallStartEvent{ToolCallID: "tool-1", ToolName: "read_file", ToolParams: agentEvents.ToolParams{Arguments: `{"path":"sources.txt","api_key":"fixture-argument-secret"}`}}},
		{Data: &agentEvents.ToolCallEndEvent{ToolCallID: "tool-1", ToolName: "read_file", Result: "Recorded facts Authorization: Bearer fixture-result-secret"}},
	} {
		normalized, ok := orchEvents.NormalizeBackgroundTranscriptEvent(event)
		if !ok {
			t.Fatalf("missing inspection event: %T", event.Data)
		}
		transcript.AppendEvent(normalized)
	}
	transcript.Error = "PASSWORD=fixture-error-secret"
	saved, _ := transcript.Marshal()
	for _, secret := range []string{"fixture-prompt-secret", "fixture-argument-secret", "fixture-result-secret", "fixture-error-secret"} {
		if strings.Contains(saved, secret) {
			t.Fatalf("secret was persisted: %s", secret)
		}
	}
	if !strings.Contains(transcript.Events[0].Text, "fixture-prompt-secret") || !strings.Contains(transcript.Events[2].ToolCall.Arguments, "fixture-argument-secret") {
		t.Fatal("saving diagnostics changed the live event")
	}
	if !strings.Contains(saved, "sources.txt") || !strings.Contains(saved, "Recorded facts") || !strings.Contains(saved, "[redacted]") {
		t.Fatal("redaction lost ordinary evidence")
	}
	if err := writeFileToWorkspace(context.Background(), file, saved); err != nil {
		t.Fatal(err)
	}
	api.emitBackgroundAgentCompleted("chat", "dispatch-1", "Research", "completed", "Finished first dispatch", "", "1s")
	router := mux.NewRouter()
	router.HandleFunc("/sessions/{session_id}/background-work/{execution_id}/inspect", api.handleInspectSessionExecution)
	call := func(id, workspacePath string, claims *UserClaims) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/sessions/chat/background-work/"+id+"/inspect?workspace_path="+workspacePath, nil)
		if claims != nil {
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, claims))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	response := call("dispatch-1", "Workflow/example", nil)
	var body struct {
		Transcript *orchEvents.BackgroundAgentTranscript `json:"transcript"`
		Result     string                                `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("inspect: %d %s", response.Code, response.Body.String())
	}
	if body.Transcript.AgentID != "dispatch-1" || body.Transcript.Status != "completed" || len(body.Transcript.Events) != 4 || body.Transcript.Inspection.StepID != "research" || body.Result != "Finished first dispatch" {
		t.Fatalf("wrong dispatch evidence: %+v", body)
	}
	// Older persisted records are also scrubbed when read, without returning raw secrets.
	legacy := *body.Transcript
	legacy.Events[0].Text = "MCP_API_TOKEN=legacy-fixture-secret"
	legacyJSON, _ := json.Marshal(legacy)
	if err := writeFileToWorkspace(context.Background(), file, string(legacyJSON)); err != nil {
		t.Fatal(err)
	}
	legacyResponse := call("dispatch-1", "Workflow/example", nil)
	if legacyResponse.Code != http.StatusOK || strings.Contains(legacyResponse.Body.String(), "legacy-fixture-secret") || !strings.Contains(legacyResponse.Body.String(), "[redacted]") {
		t.Fatalf("legacy transcript was not redacted: %d", legacyResponse.Code)
	}
	if api.bgAgentRegistry.Get("chat", "dispatch-2").GetStatus() != BGAgentRunning {
		t.Fatal("viewing changed the other dispatch")
	}
	// Simulate a server restart: no active session or registry is required to inspect a saved record.
	directory := filepath.Join(docsRoot, "Workflow/example/builder/conversation/2026-10-11")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	identity, _ := json.Marshal(map[string]interface{}{"session_id": "chat", "user_id": GetDefaultUserID(), "conversation_history": []interface{}{}})
	if err := os.WriteFile(filepath.Join(directory, "session-chat-conversation.json"), identity, 0600); err != nil {
		t.Fatal(err)
	}
	delete(api.activeSessions, "chat")
	api.bgAgentRegistry = NewBackgroundAgentRegistry()
	if afterRestart := call("dispatch-1", "Workflow/example", nil); afterRestart.Code != http.StatusOK {
		t.Fatalf("restart inspection: %d %s", afterRestart.Code, afterRestart.Body.String())
	}

	for _, result := range []*httptest.ResponseRecorder{
		call("dispatch-1", "Workflow/other", nil),
		call("dispatch-1", "Workflow/example", &UserClaims{UserID: "other-account"}),
		call("unknown", "Workflow/example", nil),
	} {
		if result.Code != http.StatusNotFound {
			t.Fatalf("unowned/missing execution exposed: %d", result.Code)
		}
	}
}
