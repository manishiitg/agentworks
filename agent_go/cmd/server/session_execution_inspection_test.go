package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/executioninspection"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	agentEvents "github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type inspectionRoundTripper func(*http.Request) (*http.Response, error)

func (f inspectionRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type inspectionCountedBody struct {
	io.ReadCloser
	read int
}

func (b *inspectionCountedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.read += n
	return n, err
}

func TestInspectDelegatedConversationBounds(t *testing.T) {
	for _, test := range []struct {
		name                    string
		children, events, bytes int
		wantEvents              int
		knownLength             bool
	}{
		{"children", maxInspectionConversationChildren + 1, 1, 0, maxInspectionConversationChildren, false},
		{"events", 1, maxInspectionConversationEvents + 1, 0, maxInspectionConversationEvents, false},
		{"bytes_chunked", 1, 1, maxInspectionConversationBytes, 0, false},
		{"bytes_known", 1, 1, maxInspectionConversationBytes, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
			workspace := &mockWorkspaceAPI{files: map[string]string{}}
			srv := httptest.NewServer(workspace)
			defer srv.Close()
			t.Setenv("WORKSPACE_API_URL", srv.URL)
			api := lifecycleTestAPI()
			api.activeSessions = map[string]*ActiveSessionInfo{"chat": {SessionID: "chat", WorkspacePath: "Workflow/example", UserID: GetDefaultUserID()}}
			api.bgAgentRegistry.Register("chat", &BackgroundAgent{ID: "parent", Kind: "sub_agent", Status: BGAgentRunning})
			api.emitBackgroundAgentStarted("chat", "parent", "Delegated task", "", "", orchEvents.ExecutionKindSubAgent)
			var path, saved string
			for index := 0; index < test.children; index++ {
				id := fmt.Sprintf("task-%d", index)
				api.bgAgentRegistry.Register("chat", &BackgroundAgent{ID: id, Kind: "agent_item", ParentExecutionID: "parent", Status: BGAgentRunning})
				api.emitBackgroundAgentStarted("chat", id, "Task", "", "parent", orchEvents.ExecutionKindAgentItem)
				path = orchEvents.BackgroundAgentTranscriptPath("Workflow/example", "chat", id)
				saved, _, _ = readFileFromWorkspace(context.Background(), path)
				transcript, err := orchEvents.ParseBackgroundAgentTranscript(saved)
				if err != nil || transcript == nil {
					t.Fatalf("source transcript: %v", err)
				}
				for event := 0; event < test.events; event++ {
					transcript.Events = append(transcript.Events, orchEvents.BackgroundAgentTranscriptEvent{Timestamp: time.Now(), Type: "user_message", Role: "user", Text: "Recorded instruction" + strings.Repeat("x", test.bytes)})
				}
				saved, err = transcript.Marshal()
				if err != nil {
					t.Fatal(err)
				}
				if err := writeFileToWorkspace(context.Background(), path, saved); err != nil {
					t.Fatal(err)
				}
			}
			var counted *inspectionCountedBody
			if test.bytes > 0 {
				previousClient := workspaceHTTPClient
				transport := previousClient.Transport
				if transport == nil {
					transport = http.DefaultTransport
				}
				boundedClient := *previousClient
				boundedClient.Transport = inspectionRoundTripper(func(request *http.Request) (*http.Response, error) {
					response, err := transport.RoundTrip(request)
					if err == nil && strings.HasSuffix(request.URL.Path, "/"+path) {
						counted = &inspectionCountedBody{ReadCloser: response.Body}
						response.Body = counted
						response.ContentLength = -1
						if test.knownLength {
							response.ContentLength = maxInspectionConversationBytes + 1
						}
					}
					return response, err
				})
				workspaceHTTPClient = &boundedClient
				defer func() { workspaceHTTPClient = previousClient }()
			}
			req := httptest.NewRequest(http.MethodGet, "/sessions/chat/background-work/parent/inspect?workspace_path=Workflow/example", nil)
			req = mux.SetURLVars(req, map[string]string{"session_id": "chat", "execution_id": "parent"})
			w := httptest.NewRecorder()
			api.handleInspectSessionExecution(w, req)
			var response struct {
				Conversation *inspectionConversation `json:"conversation"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK || response.Conversation == nil || !response.Conversation.Truncated || len(response.Conversation.Events) != test.wantEvents {
				t.Fatalf("bounded projection: status=%d err=%v", w.Code, err)
			}
			if test.bytes > 0 {
				wantRead := maxInspectionConversationBytes + 1
				if test.knownLength {
					wantRead = 0
				}
				if counted == nil || counted.read != wantRead {
					t.Fatalf("oversized response was not bounded before decoding: %+v", counted)
				}
			}
			if current, _, err := readFileFromWorkspace(context.Background(), path); err != nil || current != saved {
				t.Fatal("inspection changed the full saved transcript")
			}
		})
	}
}

func TestInspectionTargetsOnlyWorkflowSteps(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	srv := httptest.NewServer(workspace)
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	api := lifecycleTestAPI()
	api.activeSessions = map[string]*ActiveSessionInfo{"chat": {SessionID: "chat", WorkspacePath: "Workflow/example", UserID: GetDefaultUserID()}}
	api.bgAgentRegistry.Register("chat", &BackgroundAgent{ID: "step", Kind: "workflow_step", Metadata: map[string]string{"step_id": "research"}})
	api.bgAgentRegistry.Register("chat", &BackgroundAgent{ID: "generic", Kind: "sub_agent", Metadata: map[string]string{"step_id": "research"}})
	api.bgAgentRegistry.Register("chat", &BackgroundAgent{ID: "review", Kind: "workflow_step", Metadata: map[string]string{}})
	if err := writeFileToWorkspace(context.Background(), "Workflow/example/workflow.json", `{"kind":"workflow"}`); err != nil {
		t.Fatal(err)
	}
	if target := api.executionInspectionTarget("chat", "step"); target == nil || target.WorkflowKind != "workflow" || target.ExecutionKind != "workflow_step" {
		t.Fatalf("workflow step target missing: %+v", target)
	}
	if api.executionInspectionTarget("chat", "generic") != nil || api.executionInspectionTarget("chat", "review") != nil {
		t.Fatal("ordinary background work advertised step inspection")
	}
	checkTreeTargets := func(want int) {
		t.Helper()
		tree := api.buildSessionExecutionTree(api.activeSessions["chat"])
		count := 0
		var walk func(*SessionExecutionTreeNode)
		walk = func(node *SessionExecutionTreeNode) {
			if node.Inspection != nil {
				count++
				if node.ExecutionID != "step" || node.Inspection.SessionID != "chat" || node.Inspection.ExecutionID != node.ExecutionID || node.Inspection.WorkspacePath != "Workflow/example" {
					t.Fatalf("execution tree advertised a different inspector: %+v", node.Inspection)
				}
			}
			for _, child := range node.Children {
				walk(child)
			}
		}
		walk(tree.Root)
		if count != want {
			t.Fatalf("execution tree advertised %d inspection targets; want %d", count, want)
		}
	}
	checkTreeTargets(1)
	if err := writeFileToWorkspace(context.Background(), "Workflow/example/workflow.json", `{"kind":"relay"}`); err != nil {
		t.Fatal(err)
	}
	if api.executionInspectionTarget("chat", "step") != nil || workflowStepInspectionWorkspace(context.Background(), "Workflow/example") {
		t.Fatal("Relay advertised workflow step inspection")
	}
	checkTreeTargets(0)
	api.activeSessions["chat"].WorkspacePath = "_users/default/Chats/Work/projects/example"
	if api.executionInspectionTarget("chat", "step") != nil {
		t.Fatal("Crew advertised workflow step inspection")
	}
	checkTreeTargets(0)
}

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

// Follow provider-shaped events through the production bridge/writer, durable
// storage and authenticated HTTP reader. A reused provider session must never
// be mistaken for the chat or mix two task executions.
func TestInspectAgentItemCapturesProviderRuntimeEvents(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	srv := httptest.NewServer(workspace)
	defer srv.Close()
	t.Setenv("WORKSPACE_API_URL", srv.URL)
	api := lifecycleTestAPI()
	api.activeSessions = map[string]*ActiveSessionInfo{"chat": {SessionID: "chat", WorkspacePath: "Workflow/example", UserID: GetDefaultUserID()}}
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), discardEvents{}, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/example")
	bridge := base.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge)
	for _, id := range []string{"call-first", "call-second"} {
		parent := ""
		if id == "call-second" {
			parent = "call-first"
		}
		api.bgAgentRegistry.Register("chat", &BackgroundAgent{ID: id, ParentExecutionID: parent, Name: "Delegated task", Kind: "sub_agent", Status: BGAgentRunning, CreatedAt: time.Now()})
		api.emitBackgroundAgentStarted("chat", id, "Delegated task", "", parent, orchEvents.ExecutionKindSubAgent)
	}
	for _, id := range []string{"item-first", "item-second", "item-foreign", "item-mismatch"} {
		parent := "call-first"
		if id == "item-foreign" {
			parent = "call-second"
		}
		api.bgAgentRegistry.Register("chat", &BackgroundAgent{ID: id, ParentExecutionID: parent, Name: "Task", Kind: "agent_item", Status: BGAgentRunning, CreatedAt: time.Now(), Metadata: map[string]string{"step_id": "research", "item_id": id}})
		api.emitBackgroundAgentStarted("chat", id, "Task", "", parent, orchEvents.ExecutionKindAgentItem)
		ctx := orchestrator.WithBackgroundTranscriptSession(context.Background(), "chat")
		ctx = context.WithValue(ctx, orchEvents.ParentExecutionIDKey, id)
		for _, event := range []*agentEvents.AgentEvent{
			{Type: agentEvents.SystemPrompt, Data: &agentEvents.SystemPromptEvent{Content: "System for " + id + " MCP_API_TOKEN=fixture-capture-secret"}},
			{Type: agentEvents.UserMessage, Data: &agentEvents.UserMessageEvent{Role: "user", Content: "User for " + id}},
			{Type: agentEvents.ToolCallStart, Data: &agentEvents.ToolCallStartEvent{ToolCallID: "reused-tool-id", ToolName: "read_file", ToolParams: agentEvents.ToolParams{Arguments: `{"path":"sources.txt"}`}}},
			{Type: agentEvents.ToolCallEnd, Data: &agentEvents.ToolCallEndEvent{ToolCallID: "reused-tool-id", ToolName: "read_file", Result: "Facts for " + id}},
			// Tool-only calls still carry model evidence; the next response changes model.
			{Type: agentEvents.LLMGenerationEnd, Data: &agentEvents.LLMGenerationEndEvent{BaseEventData: agentEvents.BaseEventData{Metadata: map[string]interface{}{"provider": "codex-cli", "model_id": "before-" + id}}}},
			{Type: agentEvents.LLMGenerationEnd, Data: &agentEvents.LLMGenerationEndEvent{BaseEventData: agentEvents.BaseEventData{Metadata: map[string]interface{}{"provider": "codex-cli", "model_id": "actual-" + id, llmtypes.CodingProviderSessionHandleAdditionalKey: llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", NativeSessionID: "native-" + id}}}, Content: "Result for " + id}},
		} {
			event.TurnID = "turn-" + id
			event.SessionID = "dedicated-provider-runtime"
			event.Timestamp = time.Now()
			if err := bridge.HandleEvent(ctx, event); err != nil {
				t.Fatal(err)
			}
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := bridge.WaitForBackgroundTranscriptPersistence(persistCtx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		api.emitBackgroundAgentCompleted("chat", id, "Task", "completed", "Result for "+id, "", "1s")
	}
	// Even a matching index row cannot import a transcript whose own durable
	// parent identity disagrees. Nested agents also keep separate conversations.
	mismatchPath := orchEvents.BackgroundAgentTranscriptPath("Workflow/example", "chat", "item-mismatch")
	rawMismatch, _, _ := readFileFromWorkspace(context.Background(), mismatchPath)
	mismatch, _ := orchEvents.ParseBackgroundAgentTranscript(rawMismatch)
	mismatch.ParentExecutionID = "call-second"
	changed, _ := mismatch.Marshal()
	if err := writeFileToWorkspace(context.Background(), mismatchPath, changed); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"call-first", "call-second"} {
		api.emitBackgroundAgentCompleted("chat", id, "Delegated task", "completed", "Delegation result", "", "1s")
		req := httptest.NewRequest(http.MethodGet, "/sessions/chat/background-work/"+id+"/inspect?workspace_path=Workflow/example", nil)
		req = mux.SetURLVars(req, map[string]string{"session_id": "chat", "execution_id": id})
		w := httptest.NewRecorder()
		api.handleInspectSessionExecution(w, req)
		var result struct {
			Transcript   *orchEvents.BackgroundAgentTranscript `json:"transcript"`
			Conversation *inspectionConversation               `json:"conversation"`
			Model        executioninspection.ModelInfo         `json:"model"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != http.StatusOK || result.Conversation == nil {
			t.Fatalf("delegated conversation: status=%d err=%v", w.Code, err)
		}
		want := 16
		if id == "call-second" {
			want = 8
		}
		if len(result.Transcript.Events) != 0 || len(result.Conversation.Events) != want {
			t.Fatalf("conversation replaced raw evidence or mixed tasks: %s", w.Body.String())
		}
		wantModel, wantPrevious := "actual-item-second", 3
		if id == "call-second" {
			wantModel, wantPrevious = "actual-item-foreign", 1
		}
		if result.Model.ModelID != wantModel || len(result.Model.Previous) != wantPrevious {
			t.Fatalf("wrong delegated model: %+v", result.Model)
		}
		for _, event := range result.Conversation.Events {
			if event.ExecutionID == "item-mismatch" || id == "call-first" && event.ExecutionID == "item-foreign" {
				t.Fatal("conversation imported an unrelated execution")
			}
		}
		if strings.Contains(w.Body.String(), "fixture-capture-secret") {
			t.Fatal("conversation projection bypassed redaction")
		}
	}
	for _, id := range []string{"item-first", "item-second"} {
		req := httptest.NewRequest(http.MethodGet, "/sessions/chat/background-work/"+id+"/inspect?workspace_path=Workflow/example", nil)
		req = mux.SetURLVars(req, map[string]string{"session_id": "chat", "execution_id": id})
		w := httptest.NewRecorder()
		api.handleInspectSessionExecution(w, req)
		var response struct {
			Transcript *orchEvents.BackgroundAgentTranscript `json:"transcript"`
			Result     string                                `json:"result"`
			Model      executioninspection.ModelInfo         `json:"model"`
			Prompts    executioninspection.PromptHistory     `json:"system_prompts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != http.StatusOK {
			t.Fatalf("inspect status=%d err=%v", w.Code, err)
		}
		if response.Transcript == nil || response.Transcript.SessionID != "chat" || response.Transcript.AgentID != id || response.Transcript.Status != "completed" || len(response.Transcript.Events) != 8 || response.Result != "Result for "+id {
			t.Fatalf("wrong capture for %s: %+v", id, response)
		}
		if response.Model.Label != "Codex · actual-"+id || len(response.Model.Previous) != 1 || response.Model.Previous[0].ModelID != "before-"+id {
			t.Fatalf("model did not follow recorded calls: %+v", response.Model)
		}
		if len(response.Prompts.Versions) != 1 || response.Prompts.Versions[0].SessionCount != 1 {
			t.Fatalf("prompt session did not survive capture and HTTP projection: %+v", response.Prompts)
		}
		body := w.Body.String()
		for _, expected := range []string{"System for " + id, "User for " + id, "Facts for " + id, "sources.txt", "[redacted]"} {
			if !strings.Contains(body, expected) {
				t.Fatalf("missing %q in capture for %s", expected, id)
			}
		}
		other := "item-first"
		if id == other {
			other = "item-second"
		}
		if strings.Contains(body, "for "+other) || strings.Contains(body, "fixture-capture-secret") {
			t.Fatal("cross-execution evidence or secret leaked")
		}
	}
	if _, exists, err := readFileFromWorkspace(context.Background(), orchEvents.BackgroundAgentTranscriptPath("Workflow/example", "dedicated-provider-runtime", "item-first")); err != nil || exists {
		t.Fatalf("capture created a provider-session orphan: exists=%v err=%v", exists, err)
	}
}
