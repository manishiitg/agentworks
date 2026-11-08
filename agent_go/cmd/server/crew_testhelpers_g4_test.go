package server

import (
	"context"
	"encoding/json"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	agentevents "github.com/manishiitg/mcpagent/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func terminalRouteChunkEvent(sessionID, executionID, tmuxSession, content string, chunkIndex int) storeevents.Event {
	return storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     time.Now(),
		SessionID:     sessionID,
		ExecutionID:   executionID,
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{
						"kind":            "terminal",
						"tmux_session":    tmuxSession,
						"current_step_id": "review-plan",
						"execution_kind":  "workflow_step",
						"scope":           "workflow_step",
						"workflow_path":   "Workflow/instagram",
					},
				},
				Content:    content,
				ChunkIndex: chunkIndex,
			},
		},
	}
}

func terminalRouteEndEvent(sessionID, executionID, tmuxSession string, retentionSeconds int) storeevents.Event {
	return storeevents.Event{
		Type:        "streaming_end",
		Timestamp:   time.Now(),
		SessionID:   sessionID,
		ExecutionID: executionID,
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingEnd,
			Data: &agentevents.StreamingEndEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{
						"kind":                       "terminal",
						"tmux_session":               tmuxSession,
						"current_step_id":            "review-plan",
						"terminal_retention_seconds": retentionSeconds,
					},
				},
			},
		},
	}
}

func terminalRouteToolStartEvent(sessionID, executionID, toolCallID, toolName, args string, metadata map[string]interface{}) storeevents.Event {
	return storeevents.Event{
		Type:          string(agentevents.ToolCallStart),
		Timestamp:     time.Now(),
		SessionID:     sessionID,
		ExecutionID:   executionID,
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.ToolCallStart,
			Data: &agentevents.ToolCallStartEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: cloneTerminalRouteMetadata(metadata)},
				ToolCallID:    toolCallID,
				ToolName:      toolName,
				ToolParams:    agentevents.ToolParams{Arguments: args},
			},
		},
	}
}

func cloneTerminalRouteMetadata(metadata map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

type recordedTool struct {
	desc   string
	group  string
	params map[string]interface{}
	exec   func(context.Context, map[string]interface{}) (string, error)
}

type recordingRegistrar struct {
	tools map[string]recordedTool
}

func (r *recordingRegistrar) RegisterCustomTool(name, desc string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), category string) error {
	if r.tools == nil {
		r.tools = map[string]recordedTool{}
	}
	r.tools[name] = recordedTool{desc: desc, group: category, params: params, exec: exec}
	return nil
}

func (r *recordingRegistrar) RegisterCustomToolWithTimeout(name, desc string, params map[string]interface{}, exec func(context.Context, map[string]interface{}) (string, error), _ time.Duration, category string) error {
	return r.RegisterCustomTool(name, desc, params, exec, category)
}

// fakeWorkspaceDocumentStore is a minimal in-memory stand-in for the
// workspace API's GET/PUT /api/documents/<path> routes, just enough for
// readFileFromWorkspace/writeFileToWorkspace to round-trip against it.
type fakeWorkspaceDocumentStore struct {
	mu    sync.Mutex
	files map[string]string
}

func newFakeWorkspaceServer(t *testing.T) (*httptest.Server, *fakeWorkspaceDocumentStore) {
	t.Helper()
	store := &fakeWorkspaceDocumentStore{files: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		store.mu.Lock()
		defer store.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			content, ok := store.files[path]
			if !ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "File does not exist", "error": "File not found: " + path, "data": map[string]any{}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"filepath": path, "content": content},
			})
		case http.MethodPut:
			var body struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			store.files[path] = body.Content
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, store
}

func serveContractGuardManifest(t *testing.T, version string) string {
	t.Helper()
	ensureProjectStateRoot(t)
	const workspacePath = "Workflow/contract-guard"
	data, err := json.Marshal(map[string]interface{}{
		"schema_version":      1,
		"version":             version,
		"code_layout_version": 1,
		"id":                  "wf-contract-guard",
		"label":               "Contract guard",
		"capabilities":        map[string]interface{}{},
		"schedules":           []interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(data),
	}}
	server := httptest.NewServer(workspace)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	return workspacePath
}

func installWorkflowCostTestLedger(t *testing.T) *costledger.Ledger {
	t.Helper()
	previous := costledger.DefaultLedger()
	ledger, err := costledger.NewSQLiteLedger(filepath.Join(t.TempDir(), "costs.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLiteLedger() error = %v", err)
	}
	costledger.SetDefaultLedger(ledger)
	t.Cleanup(func() {
		costledger.SetDefaultLedger(previous)
		_ = ledger.Close()
	})
	return ledger
}

func createSlackConnectionForTest(t *testing.T, api *StreamingAPI, userID, body string) {
	t.Helper()
	w := httptest.NewRecorder()
	createSlackConnectionHandler(api)(w, slackConnectionRequest(t, "POST", userID, body))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", w.Code, w.Body.String())
	}
}

func nativeInputObserverFixture(t *testing.T) *nativeTerminalObserver {
	t.Helper()
	snapshot := terminals.Snapshot{SessionID: t.Name(), TmuxSession: "native-test-" + t.Name()}
	t.Cleanup(func() { tmuxinput.Default.ClearInteractiveDraft(snapshot.TmuxSession) })
	return &nativeTerminalObserver{api: &StreamingAPI{eventStore: storeevents.NewEventStore(100)}, snapshot: snapshot,
		nativeCounts: map[string]int{}, coveredCounts: map[string]int{}, eventCounts: map[string]int{}}
}

func nativeInputUserEvent(id, text string) storeevents.Event {
	return storeevents.Event{ID: id, Type: "user_message", Data: unifiedevents.NewAgentEvent(unifiedevents.NewUserMessageEvent(0, text, "user"))}
}
