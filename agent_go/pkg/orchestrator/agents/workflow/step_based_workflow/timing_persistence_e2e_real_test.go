package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/mcpagent/events"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/spf13/viper"
)

type timingE2EMockLogger struct{}

func (timingE2EMockLogger) Infof(format string, v ...any)  { fmt.Printf("INFO: "+format+"\n", v...) }
func (timingE2EMockLogger) Errorf(format string, v ...any) { fmt.Printf("ERROR: "+format+"\n", v...) }
func (timingE2EMockLogger) Debugf(format string, v ...interface{}) {
	fmt.Printf("DEBUG: "+format+"\n", v...)
}

// TestRealClaudeStructuredToolCallPersistsNonZeroTimingToDisk proves the fix
// for a real bug end to end, across every layer between a live coding-CLI
// call and the file a Pulse reviewer reads.
//
// StreamChunk.ToolDuration existed on the wire type from the start, but no
// structured adapter ever set it. That meant a real production step reported
// tools.total_duration_ms=0 across 4 successful shell calls in its persisted
// -timing.json -- a 10-minute turn where the LLM leg (605,442ms) and the wall
// clock (605,455ms) were within 13ms of each other looked entirely
// generation-bound, hiding that part of that time was genuine tool work, and
// making a near-duplicate 19KB script rewrite invisible to any cost/time
// review reading the file.
//
// This test does not fabricate a StreamChunk or a ToolCallEntry. It:
//  1. Drives a REAL `claude -p --output-format stream-json` call with a
//     prompt that forces a real shell tool call, and captures the REAL
//     StreamChunk the adapter emits (proving the multi-llm-provider-go fix).
//  2. Feeds that into the REAL ContextAwareEventBridge via HandleEvent with
//     real ToolCallStart/ToolCallEnd events (the same path production code
//     uses), and drains the real captured orchestrator.ToolCallEntry.
//  3. Calls the REAL, unexported saveExecutionConversationLogs -- the exact
//     production function a workflow step calls -- against a real
//     workspace-docs HTTP server backed by a real temp directory.
//  4. Reads the resulting *-timing.json back OFF DISK and asserts the
//     duration, token, and byte fields a Pulse cost/time review depends on
//     are genuinely non-zero, not just present in the JSON schema.
func TestRealClaudeStructuredToolCallPersistsNonZeroTimingToDisk(t *testing.T) {
	if os.Getenv("RUN_CLAUDE_CODE_TIMING_PERSISTENCE_E2E") == "" {
		t.Skip("set RUN_CLAUDE_CODE_TIMING_PERSISTENCE_E2E=1 to run this real timing-persistence e2e")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude binary not found: %v", err)
	}

	// --- 1. Real live structured claude-code call, capturing real chunks ---
	model := "claude-haiku-5-5"
	adapter := claudecodeadapter.NewClaudeCodeInteractiveAdapter(model, timingE2EMockLogger{})

	ch := make(chan llmtypes.StreamChunk, 64)
	var chunks []llmtypes.StreamChunk
	chunksDone := make(chan struct{})
	go func() {
		defer close(chunksDone)
		for c := range ch {
			chunks = append(chunks, c)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	structuredOpt := func(o *llmtypes.CallOptions) {
		if o.Metadata == nil {
			o.Metadata = &llmtypes.Metadata{Custom: make(map[string]interface{})}
		}
		if o.Metadata.Custom == nil {
			o.Metadata.Custom = make(map[string]interface{})
		}
		o.Metadata.Custom[claudecodeadapter.MetadataKeyStructuredTransport] = true
	}
	_, err := adapter.GenerateContent(ctx,
		[]llmtypes.MessageContent{
			{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: "Run the shell command `echo timing-e2e-probe` and tell me exactly what it printed."},
			}},
		},
		llmtypes.WithStreamingChan(ch), structuredOpt,
	)
	if err != nil {
		t.Fatalf("claude structured GenerateContent: %v", err)
	}
	<-chunksDone

	var toolEnd *llmtypes.StreamChunk
	var toolStart *llmtypes.StreamChunk
	for i := range chunks {
		c := &chunks[i]
		if c.Type == llmtypes.StreamChunkTypeToolCallStart && toolStart == nil {
			toolStart = c
		}
		if c.Type == llmtypes.StreamChunkTypeToolCallEnd && toolEnd == nil {
			toolEnd = c
		}
	}
	if toolStart == nil || toolEnd == nil {
		t.Fatalf("expected a tool call start+end in the live stream (chunks=%d); the prompt should force a shell call", len(chunks))
	}
	t.Logf("real adapter ToolDuration on the ToolCallEnd chunk: %v", toolEnd.ToolDuration)
	if toolEnd.ToolDuration <= 0 {
		t.Fatalf("adapter reported ToolDuration=%v for a real tool call; a real call is never instant — this is the exact regression this test guards", toolEnd.ToolDuration)
	}

	// --- 2. Feed the real chunks through the real bridge, exactly as production does ---
	base, err := orchestrator.NewBaseOrchestrator(
		loggerv2.NewDefault(), nil, orchestrator.OrchestratorTypeWorkflow,
		"", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("NewBaseOrchestrator: %v", err)
	}
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}

	bridge, ok := hcpo.GetContextAwareBridge().(*orchestrator.ContextAwareEventBridge)
	if !ok {
		t.Fatal("GetContextAwareBridge did not return *ContextAwareEventBridge")
	}
	captureCtx := bridge.StartTimingCaptureFor(context.Background())

	// HandleEvent captures timing BEFORE it forwards to the underlying
	// bridge (see context_aware_bridge.go), and this test constructs a
	// BaseOrchestrator with no underlying bridge (nil, matching the "no
	// event bridge needed" pattern already used by other unit tests in this
	// package). That forwarding step then errors with "underlying bridge is
	// nil" -- expected here and irrelevant to what this test proves. Any
	// OTHER error is real and must fail the test.
	handleEvent := func(event *events.AgentEvent) {
		t.Helper()
		if err := bridge.HandleEvent(captureCtx, event); err != nil && err.Error() != "underlying bridge is nil" {
			t.Fatalf("HandleEvent(%s): %v", event.Type, err)
		}
	}

	toolCallID := toolStart.ToolCallID
	if toolCallID == "" {
		toolCallID = "timing-e2e-tool-1"
	}
	startedAt := time.Now()
	handleEvent(&events.AgentEvent{
		Type:      events.ToolCallStart,
		Timestamp: startedAt,
		Data: &events.ToolCallStartEvent{
			ToolName:   toolStart.ToolName,
			ToolCallID: toolCallID,
			ToolParams: events.ToolParams{Arguments: toolStart.ToolArgs},
		},
	})
	handleEvent(&events.AgentEvent{
		Type:      events.ToolCallEnd,
		Timestamp: startedAt.Add(toolEnd.ToolDuration),
		Data: &events.ToolCallEndEvent{
			ToolName:   toolEnd.ToolName,
			ToolCallID: toolCallID,
			Result:     toolEnd.ToolResult,
			// The real, previously-always-zero duration.
			Duration: toolEnd.ToolDuration,
		},
	})

	snapshot := bridge.DrainTimingCaptureFor(captureCtx)
	if len(snapshot.ToolCalls) != 1 {
		t.Fatalf("expected 1 captured tool call, got %d: %+v", len(snapshot.ToolCalls), snapshot.ToolCalls)
	}
	captured := snapshot.ToolCalls[0]
	if captured.Duration != toolEnd.ToolDuration {
		t.Fatalf("bridge captured Duration=%v, want the real adapter duration %v", captured.Duration, toolEnd.ToolDuration)
	}
	if captured.Args == "" {
		t.Error("bridge captured tool call with empty Args")
	}

	// --- 3. Real workspace-docs HTTP server backed by a real temp dir ---
	docsDir := t.TempDir()
	workflowRelPath := "Workflow/timing-persistence-e2e"
	if err := os.MkdirAll(filepath.Join(docsDir, workflowRelPath), 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)
	router := gin.New()
	router.Any("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	wsServer := httptest.NewServer(router)
	defer wsServer.Close()

	hcpo.WorkspaceClient = workspace.NewClient(wsServer.URL)
	hcpo.SetWorkspacePath(workflowRelPath)

	attemptStartedAt := startedAt
	attemptCompletedAt := startedAt.Add(toolEnd.ToolDuration)
	attemptDuration := toolEnd.ToolDuration

	if err := hcpo.saveExecutionConversationLogs(
		0, "timing-e2e-step", "timing-e2e-step",
		1, 0,
		"real e2e probe completed",
		model,
		nil, // conversationHistory: not the subject of this test
		nil, // executionAgent: saveExecutionConversationLogs tolerates nil
		snapshot.ToolCalls,
		snapshot.LLMCalls,
		attemptStartedAt, attemptCompletedAt, attemptDuration,
	); err != nil {
		t.Fatalf("saveExecutionConversationLogs: %v", err)
	}

	// --- 4. Read the persisted file back OFF DISK and assert real values ---
	logDir := getExecutionFolderPathForLogs(filepath.Join(docsDir, workflowRelPath), "timing-e2e-step", "timing-e2e-step")
	timingPath := filepath.Join(logDir, "execution-attempt-1-iteration-0-timing.json")
	raw, err := os.ReadFile(timingPath)
	if err != nil {
		t.Fatalf("read persisted timing file at %s: %v", timingPath, err)
	}

	var persisted struct {
		Tools struct {
			Count           int   `json:"count"`
			TotalDurationMs int64 `json:"total_duration_ms"`
			Calls           []struct {
				ToolName    string `json:"tool_name"`
				DurationMs  int64  `json:"duration_ms"`
				ArgsBytes   int    `json:"args_bytes"`
				ResultBytes int    `json:"result_bytes"`
			} `json:"calls"`
		} `json:"tools"`
		Breakdown struct {
			ToolArgsBytes  int   `json:"tool_args_bytes"`
			WallDurationMs int64 `json:"wall_duration_ms"`
		} `json:"breakdown"`
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode persisted timing JSON: %v\nraw=%s", err, raw)
	}

	if persisted.Tools.Count != 1 {
		t.Fatalf("persisted tools.count = %d, want 1", persisted.Tools.Count)
	}
	// This is the exact assertion that would have caught the bug: the
	// production file for the real 10-minute step read total_duration_ms=0
	// across 4 successful calls.
	if persisted.Tools.TotalDurationMs <= 0 {
		t.Errorf("persisted tools.total_duration_ms = %d, want > 0 (this is the field a Pulse cost/time review reads)", persisted.Tools.TotalDurationMs)
	}
	if len(persisted.Tools.Calls) != 1 {
		t.Fatalf("persisted tools.calls length = %d, want 1", len(persisted.Tools.Calls))
	}
	call := persisted.Tools.Calls[0]
	if call.DurationMs <= 0 {
		t.Errorf("persisted tools.calls[0].duration_ms = %d, want > 0", call.DurationMs)
	}
	if call.ArgsBytes <= 0 {
		t.Errorf("persisted tools.calls[0].args_bytes = %d, want > 0", call.ArgsBytes)
	}
	if persisted.Breakdown.ToolArgsBytes <= 0 {
		t.Errorf("persisted breakdown.tool_args_bytes = %d, want > 0", persisted.Breakdown.ToolArgsBytes)
	}

	t.Logf("✅ real timing chain verified end to end: live tool duration=%v -> persisted tools.total_duration_ms=%d, calls[0]={name=%q duration_ms=%d args_bytes=%d result_bytes=%d}, file=%s",
		toolEnd.ToolDuration, persisted.Tools.TotalDurationMs, call.ToolName, call.DurationMs, call.ArgsBytes, call.ResultBytes, timingPath)
}
