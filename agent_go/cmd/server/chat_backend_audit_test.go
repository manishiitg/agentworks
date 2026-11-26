package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	agent "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentwrapper"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestEvictSessionStateClearsPerSessionMaps(t *testing.T) {
	sid := "sess-evict-audit-test"
	api := &StreamingAPI{
		conversationHistory:                map[string][]llmtypes.MessageContent{sid: {{Role: llmtypes.ChatMessageTypeHuman}}},
		resumeHistorySources:               map[string]*resumeHistorySource{sid: {}},
		lastWorkshopModeBySession:          map[string]string{sid: "builder"},
		lastChatPolicyBySession:            map[string]string{sid: "p"},
		lastChatPolicyRoleBySession:        map[string]string{sid: "r"},
		lastAgentProfileKeyBySession:       map[string]string{sid: "k"},
		lastAgentToolsModeBySession:        map[string]string{sid: "full"},
		lastWorkflowToolsModeBySession:     map[string]string{sid: "full"},
		launchedAgentProfileKeyBySession:   map[string]string{sid: "k"},
		restoredConversationPersistTargets: map[string]restoredChatHistoryPersistTarget{sid: {}},
		workflowObjectives:                 map[string]string{sid: "obj"},
		workflowOrchestrators:              map[string]orchestrator.Orchestrator{sid: nil},
		sessionAgents:                      map[string]*agent.LLMAgentWrapper{sid: nil},
		lastQueryRequests:                  map[string]QueryRequest{sid: {Query: "hi"}},
		sessionWorkspaceFolders:            map[string]string{sid: "Crew/x"},
		stoppedSessions:                    map[string]bool{sid: true},
		interruptedTurns:                   map[string]bool{sid: true},
		pendingTurnStops:                   map[string]time.Time{sid: time.Now()},
		completionLoopStarted:              map[string]bool{sid: true},
		nativeBackground:                   map[string]map[string]llmtypes.NativeRecord{sid: {}},
		nativeObservers:                    map[string]*nativeTerminalObserver{sid: nil},
		nativeCosts:                        map[string]*nativeCostAccountant{sid: {running: true, done: make(chan struct{})}},
	}
	api.promptParts.Store(sid, "parts")
	api.questionsRestored.Store(sid, true)
	retainedLastSidecarFinal.Store(sid, "reply")
	t.Cleanup(func() { retainedLastSidecarFinal.Delete(sid) })

	doneCh := api.nativeCosts[sid].done
	api.evictSessionState(sid)

	select {
	case <-doneCh:
	default:
		t.Fatalf("expected nativeCostAccountant done channel to be closed on eviction")
	}
	if len(api.conversationHistory) != 0 || len(api.resumeHistorySources) != 0 || len(api.lastWorkshopModeBySession) != 0 ||
		len(api.restoredConversationPersistTargets) != 0 || len(api.workflowObjectives) != 0 ||
		len(api.workflowOrchestrators) != 0 || len(api.sessionAgents) != 0 || len(api.lastQueryRequests) != 0 ||
		len(api.sessionWorkspaceFolders) != 0 || len(api.stoppedSessions) != 0 || len(api.interruptedTurns) != 0 ||
		len(api.pendingTurnStops) != 0 || len(api.completionLoopStarted) != 0 || len(api.nativeBackground) != 0 ||
		len(api.nativeObservers) != 0 || len(api.nativeCosts) != 0 {
		t.Fatalf("expected all per-session maps to be empty after evictSessionState")
	}
	if _, ok := api.promptParts.Load(sid); ok {
		t.Fatalf("expected promptParts to be cleared")
	}
	if _, ok := api.questionsRestored.Load(sid); ok {
		t.Fatalf("expected questionsRestored to be cleared")
	}
	if _, ok := retainedLastSidecarFinal.Load(sid); ok {
		t.Fatalf("expected retainedLastSidecarFinal to be cleared")
	}
}

func TestWaitForCodingAgentClarificationCancelAfterRemovalDoesNotDeadlock(t *testing.T) {
	api := &StreamingAPI{
		eventStore:                events.NewEventStore(100),
		codingAgentClarifications: make(map[string]*pendingCodingAgentClarification),
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := api.waitForCodingAgentClarification(ctx, "sess-clarify-cancel", "claude-code", clarificationTestArgs())
		done <- err
	}()

	// Wait for the pending entry to register.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		api.codingAgentClarificationsMu.Lock()
		_, exists := api.codingAgentClarifications["sess-clarify-cancel"]
		api.codingAgentClarificationsMu.Unlock()
		if exists {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Simulate Stop/teardown removing the pending entry without sending an answer, then canceling ctx.
	api.codingAgentClarificationsMu.Lock()
	delete(api.codingAgentClarifications, "sess-clarify-cancel")
	api.codingAgentClarificationsMu.Unlock()
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("waitForCodingAgentClarification deadlocked on cancel after entry removal")
	}
}

func TestWorkspaceProxyMultipartInspectionValidatesPathAfterFile(t *testing.T) {
	buildMultipart := func(filepathValue string) (*http.Request, string) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		fw, err := w.CreateFormFile("file", "sample.bin")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		_, _ = fw.Write(bytes.Repeat([]byte("A"), 4096))
		if err := w.WriteField("folder_path", filepathValue); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
		_ = w.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/wp/api/documents/upload", bytes.NewReader(buf.Bytes()))
		req.Header.Set("Content-Type", w.FormDataContentType())
		return req, buf.String()
	}

	// Denied path appearing AFTER the file part must still return 403.
	deniedReq, _ := buildMultipart("_users/user-b/secret.txt")
	status, _, cleanup := workspaceProxyCrossUserBlock(deniedReq, "user-a")
	if cleanup != nil {
		cleanup()
	}
	if status != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-user filepath after file part, got %d", status)
	}

	// Allowed path must replay the exact original multipart bytes.
	allowedReq, originalBody := buildMultipart("_users/user-a/ok.txt")
	status, _, cleanup = workspaceProxyCrossUserBlock(allowedReq, "user-a")
	if cleanup != nil {
		defer cleanup()
	}
	if status != 0 {
		t.Fatalf("expected 0 (allowed) for own filepath, got %d", status)
	}
	replayed, err := io.ReadAll(allowedReq.Body)
	if err != nil {
		t.Fatalf("read replayed body: %v", err)
	}
	if string(replayed) != originalBody {
		t.Fatalf("replayed multipart body mismatch: got %d bytes, want %d bytes", len(replayed), len(originalBody))
	}
}

func TestListChatHistorySessionsFromDiskFiltersKindAndSkipsCleanIndexRewrite(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)

	userID := "audit-user"
	dateDir := filepath.Join(docsRoot, "_users", userID, "chat_history", "2026-11-26")
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeConv := func(sessionID, query string, modOffset time.Duration) {
		p := filepath.Join(dateDir, "session-"+sessionID+"-conversation.json")
		body, _ := json.Marshal(map[string]any{
			"session_id": sessionID,
			"query":      query,
			"updated_at": time.Now().Add(modOffset).UTC().Format(time.RFC3339),
			"messages":   []map[string]any{{"role": "user", "content": query}},
		})
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatalf("write conv %s: %v", sessionID, err)
		}
		ts := time.Now().Add(modOffset)
		_ = os.Chtimes(p, ts, ts)
	}

	// Newer schedule sessions + older regular chat session.
	writeConv("schedule-cron-1", "cron run 1", 0)
	writeConv("schedule-cron-2", "cron run 2", -1*time.Minute)
	writeConv("regular-chat-1", "hello world", -2*time.Minute)

	// First call with kind=chat and limit=1 must return regular-chat-1 (filtering before pagination).
	sessions, ok, err := listChatHistorySessionsFromDisk(userID, chatHistoryRoot(userID), "", 1, 0, "chat")
	if err != nil || !ok {
		t.Fatalf("listChatHistorySessionsFromDisk failed: ok=%v err=%v", ok, err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "regular-chat-1" {
		t.Fatalf("expected [regular-chat-1], got %+v", sessions)
	}

	// Verify chat-index.json was written once, then stamp its mtime back and confirm a clean read does NOT rewrite it.
	indexPath := filepath.Join(docsRoot, "_users", userID, "chat_history", chatHistoryIndexFileName)
	past := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	if err := os.Chtimes(indexPath, past, past); err != nil {
		t.Fatalf("chtimes index: %v", err)
	}

	sessions2, ok, err := listChatHistorySessionsFromDisk(userID, chatHistoryRoot(userID), "", 1, 0, "chat")
	if err != nil || !ok || len(sessions2) != 1 || sessions2[0].SessionID != "regular-chat-1" {
		t.Fatalf("second read failed: ok=%v err=%v sessions=%+v", ok, err, sessions2)
	}
	info, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	if !info.ModTime().Equal(past) {
		t.Fatalf("expected clean read not to rewrite chat-index.json; mtime changed from %v to %v", past, info.ModTime())
	}
}

func TestChatUsageProbeDeferClearsRunningOnPanic(t *testing.T) {
	key := "claude-code|panic-test-conn"
	chatUsageProbes.mu.Lock()
	chatUsageProbes.running[key] = true
	chatUsageProbes.mu.Unlock()

	func() {
		defer recoverDetachedGoroutine("test_probe", key)
		refreshChatUsageProbe(context.Background(), key, "owner", "claude-code", "panic-test-conn", func() []string {
			panic("simulated probe crash")
		})
	}()

	chatUsageProbes.mu.Lock()
	stillRunning := chatUsageProbes.running[key]
	chatUsageProbes.mu.Unlock()
	if stillRunning {
		t.Fatalf("expected chatUsageProbes.running[%q] to be cleared after panic", key)
	}
}

func TestWorkflowStepIDLookupDeleteIsConcurrentSafe(t *testing.T) {
	api := &StreamingAPI{
		workflowStepIDs: map[string]string{"preset-1": "step-a"},
	}
	// Verify Lock/Unlock path deletes cleanly.
	api.workflowStepIDMux.Lock()
	got := api.workflowStepIDs["preset-1"]
	delete(api.workflowStepIDs, "preset-1")
	api.workflowStepIDMux.Unlock()
	if !strings.EqualFold(got, "step-a") || len(api.workflowStepIDs) != 0 {
		t.Fatalf("expected step-a deleted, got %q len=%d", got, len(api.workflowStepIDs))
	}
}
