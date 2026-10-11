package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/backgroundtranscript"
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// fakeDocumentsAPI is a minimal stand-in for the workspace API's
// /api/documents/{filepath} GET/PUT contract, just enough to drive
// AppendBackgroundAgentTranscriptEvent's read-modify-write path without
// depending on cmd/server's mockWorkspaceAPI (a different package).
type fakeDocumentsAPI struct {
	mu       sync.Mutex
	files    map[string]string
	writes   int
	lastBody string
}

func (f *fakeDocumentsAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		content, ok := f.files[path]
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"message": "File does not exist",
				"data":    map[string]interface{}{},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"filepath": path, "content": content},
		})
	case http.MethodPut:
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.files[path] = body.Content
		f.writes++
		f.lastBody = body.Content
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func newTestBaseOrchestratorForTranscripts(t *testing.T, api *fakeDocumentsAPI) *BaseOrchestrator {
	t.Helper()
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return &BaseOrchestrator{
		WorkspaceClient: workspace.NewClient(server.URL),
		workspacePath:   "Workflow/test-workspace",
		logger:          silentLoggerV2{},
	}
}

// TestAppendBackgroundAgentTranscriptEventSkipsWhenNoHeaderExists pins the
// PLAT-164 scope fix: an execution owner id that was never registered via
// cmd/server's emitBackgroundAgentStarted (e.g. a plain workflow step's
// "exec-<step-id>-<timestamp>" ParentExecutionIDKey, which is
// indistinguishable in shape from a real background agent's id) must not get
// a transcript fabricated for it — that step already has its own durable
// conversation+timing record via controller_execution.go /
// controller_todo_task.go.
func TestAppendBackgroundAgentTranscriptEventSkipsWhenNoHeaderExists(t *testing.T) {
	api := &fakeDocumentsAPI{files: map[string]string{}}
	bo := newTestBaseOrchestratorForTranscripts(t, api)

	err := bo.AppendBackgroundAgentTranscriptEvent(context.Background(), "session-1", "exec-fetch-data-1787245962052297000", orchevents.BackgroundAgentTranscriptEvent{
		Timestamp: time.Now(),
		Type:      "user_message",
		Role:      "user",
		Text:      "step-internal prompt",
	})
	if err != nil {
		t.Fatalf("AppendBackgroundAgentTranscriptEvent: %v", err)
	}

	api.mu.Lock()
	writes := api.writes
	fileCount := len(api.files)
	api.mu.Unlock()
	if writes != 0 || fileCount != 0 {
		t.Fatalf("writes=%d files=%d, want no write and no fabricated transcript when no header was registered", writes, fileCount)
	}
}

// TestAppendBackgroundAgentTranscriptEventAppendsWhenHeaderAlreadyExists
// proves the intended case still works: cmd/server registered this agent
// (createBackgroundAgentTranscript already wrote the "running" header), so a
// granular event from the bridge appends into that same file.
func TestAppendBackgroundAgentTranscriptEventAppendsWhenHeaderAlreadyExists(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	const sessionID = "session-1"
	const agentID = "workshop-background-task-abc"
	path := orchevents.BackgroundAgentTranscriptPath("Workflow/test-workspace", sessionID, agentID)

	header := orchevents.NewBackgroundAgentTranscript(sessionID, agentID, "parent-exec-1", "Measurement Validator", "workshop_background", time.Now())
	headerContent, err := header.Marshal()
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}

	api := &fakeDocumentsAPI{files: map[string]string{path: headerContent}}
	bo := newTestBaseOrchestratorForTranscripts(t, api)

	err = bo.AppendBackgroundAgentTranscriptEvent(context.Background(), sessionID, agentID, orchevents.BackgroundAgentTranscriptEvent{
		Timestamp: time.Now(),
		Type:      "user_message",
		Role:      "user",
		Text:      "validate last week's metrics",
	})
	if err != nil {
		t.Fatalf("AppendBackgroundAgentTranscriptEvent: %v", err)
	}

	api.mu.Lock()
	writes := api.writes
	final := api.files[path]
	api.mu.Unlock()
	if writes != 1 {
		t.Fatalf("writes=%d, want exactly 1", writes)
	}
	transcript, err := orchevents.ParseBackgroundAgentTranscript(final)
	if err != nil {
		t.Fatalf("parse written transcript: %v", err)
	}
	if len(transcript.Events) != 1 || transcript.Events[0].Text != "validate last week's metrics" {
		t.Fatalf("events = %+v, want the appended event", transcript.Events)
	}
	if transcript.Name != "Measurement Validator" {
		t.Fatalf("name = %q, header metadata must survive the append", transcript.Name)
	}
	// Even a queued callback retaining an old header must respect chat deletion.
	if err := backgroundtranscript.Delete(bo.workspacePath, sessionID, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := bo.AppendBackgroundAgentTranscriptEvent(context.Background(), sessionID, agentID, orchevents.BackgroundAgentTranscriptEvent{Type: "user_message", Text: "late event"}); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.writes != 1 {
		t.Fatal("late orchestrator callback wrote to a deleted chat")
	}
}

// TestAppendBackgroundAgentTranscriptEventReturnsErrorOnCorruptHeader proves
// requirement 5 (write failures visible): a registered but corrupted header
// must not be silently replaced with a fresh, event-losing transcript.
func TestAppendBackgroundAgentTranscriptEventReturnsErrorOnCorruptHeader(t *testing.T) {
	const sessionID = "session-1"
	const agentID = "agent-1"
	path := orchevents.BackgroundAgentTranscriptPath("Workflow/test-workspace", sessionID, agentID)

	api := &fakeDocumentsAPI{files: map[string]string{path: "{not valid json"}}
	bo := newTestBaseOrchestratorForTranscripts(t, api)

	err := bo.AppendBackgroundAgentTranscriptEvent(context.Background(), sessionID, agentID, orchevents.BackgroundAgentTranscriptEvent{
		Timestamp: time.Now(),
		Type:      "user_message",
		Text:      "should not be silently dropped",
	})
	if err == nil {
		t.Fatal("expected an error for a corrupt existing transcript, got nil")
	}

	api.mu.Lock()
	writes := api.writes
	api.mu.Unlock()
	if writes != 0 {
		t.Fatalf("writes=%d, want 0 — a corrupt header must not be silently overwritten", writes)
	}
}
