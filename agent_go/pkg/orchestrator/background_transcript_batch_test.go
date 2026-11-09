package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/backgroundtranscript"
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-515: a provider event burst on adopted Crew storage must retain
// every ordered event with zero workspace HTTP round trips, then honor deletion.
func TestBackgroundTranscriptBurstFlushesLocallyAndHonorsDeletion(t *testing.T) {
	docs, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	write := func(file string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	private := filepath.Join(docs, "_system/crew-data/crew")
	for _, dir := range []string{filepath.Join(docs, "Crew/example"), private} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(state, "ownership/projects.json"), []byte(`{"projects":{"work/example":{"product":"work","folder":"example","owner_id":"owner","project_id":"crew"}}}`))
	write(filepath.Join(state, "ownership/crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte("Workflow/example")))), []byte(`{"id":"task","crew_id":"crew","crew_root":"Crew/example","workspace_path":"Workflow/example","kind":"script","runtime_root":"workflows/task"}`))
	file := filepath.Join(private, "workflows/task/builder/conversation/background/chat/agent.json")
	header := orchevents.NewBackgroundAgentTranscript("chat", "agent", "", "Agent", "agent", time.Now())
	content, _ := header.Marshal()
	write(file, []byte(content))
	var requests atomic.Int64
	remote := &fakeDocumentsAPI{files: map[string]string{orchevents.BackgroundAgentTranscriptPath("Workflow/example", "chat", "agent"): content}}
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); remote.ServeHTTP(w, r) }))
	defer service.Close()
	bo := &BaseOrchestrator{WorkspaceClient: workspace.NewClient(service.URL), workspacePath: "Workflow/example", logger: silentLoggerV2{}}
	// Measure the existing HTTP transport on the same 120-event workload,
	// rather than projecting a request count from implementation inspection.
	t.Setenv("WORKSPACE_DOCS_PATH", "")
	for i := 0; i < 120; i++ {
		if err := bo.AppendBackgroundAgentTranscriptEvent(t.Context(), "chat", "agent", orchevents.BackgroundAgentTranscriptEvent{Type: "user_message", Text: fmt.Sprintf("message-%03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	referenceRequests := requests.Load()
	if referenceRequests != 240 {
		t.Fatalf("reference transport requests=%d", referenceRequests)
	}
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	requests.Store(0)
	bridge := NewContextAwareEventBridge(noopListener{}, silentLoggerV2{})
	bridge.SetBackgroundAgentTranscriptWriter(bo)
	ctx := context.WithValue(t.Context(), orchevents.ParentExecutionIDKey, "agent")
	start := time.Now()
	for i := 0; i < 120; i++ {
		if err := bridge.HandleEvent(ctx, &unifiedevents.AgentEvent{Type: unifiedevents.UserMessage, SessionID: "chat", Timestamp: time.Now(), Data: &unifiedevents.UserMessageEvent{Content: fmt.Sprintf("message-%03d", i), Role: "user"}}); err != nil {
			t.Fatal(err)
		}
	}
	// Pending events have no per-event read/modify/write. The existing completion
	// barrier flushes immediately rather than sleeping for the two-second interval.
	if err := bridge.WaitForBackgroundTranscriptPersistence(t.Context()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var saved orchevents.BackgroundAgentTranscript
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Events) != 120 {
		t.Fatalf("saved %d events", len(saved.Events))
	}
	for i, event := range saved.Events {
		if event.Text != fmt.Sprintf("message-%03d", i) {
			t.Fatalf("event %d out of order: %s", i, event.Text)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("local event burst made %d HTTP requests", requests.Load())
	}
	t.Logf("120 events: reference transport HTTP requests=%d; batched local HTTP requests=%d; local flush elapsed=%s", referenceRequests, requests.Load(), time.Since(start))
	if err := backgroundtranscript.Delete("Workflow/example", "chat", func() error { return os.Remove(file) }); err != nil {
		t.Fatal(err)
	}
	if err := bo.AppendBackgroundAgentTranscriptEvent(t.Context(), "chat", "agent", orchevents.BackgroundAgentTranscriptEvent{Text: "late event"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("late event recreated deleted chat: %v", err)
	}
}
