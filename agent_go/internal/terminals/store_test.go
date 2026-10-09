package terminals

import (
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"

	agentevents "github.com/manishiitg/mcpagent/events"
)

func genericTerminalLifecycleEvent(eventType, executionID string, metadata map[string]interface{}, timestamp time.Time) storeevents.Event {
	return storeevents.Event{
		Type:          eventType,
		Timestamp:     timestamp,
		SessionID:     "session-1",
		ExecutionID:   executionID,
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.EventType(eventType),
			Data: &agentevents.GenericEventData{
				BaseEventData: agentevents.BaseEventData{Metadata: metadata},
				Data:          map[string]interface{}{},
			},
		},
	}
}

func TestStoreKeepsStructuredWorkflowExecutionInDone(t *testing.T) {
	store := NewStore()
	now := time.Now()
	metadata := map[string]interface{}{
		"execution_owner_id": "workflow-step:exec-1:math-solver",
		"execution_kind":     "workflow_step",
		"step_id":            "math-solver",
		"step_title":         "Math Solver",
		"workflow_path":      "Workflow/testing",
	}

	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_start",
		"workflow-step:exec-1:math-solver",
		metadata,
		now,
	))

	running, ok := store.Get("session-1:workflow-step:exec-1:math-solver")
	if !ok {
		t.Fatal("expected structured child to appear as soon as it starts")
	}
	if !running.Active || running.State != "running" || running.StepTransport != "structured" {
		t.Fatalf("unexpected running lifecycle: active=%v state=%q transport=%q", running.Active, running.State, running.StepTransport)
	}

	store.HandleEvent("session-1", storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     now.Add(time.Second),
		SessionID:     "session-1",
		ExecutionID:   "workflow-step:exec-1:math-solver",
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: metadata},
				Content:       "The answer is 327.",
				ChunkIndex:    1,
				Source:        agentevents.StreamingChunkSourceTranscript,
			},
		},
	})
	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_end",
		"workflow-step:exec-1:math-solver",
		metadata,
		now.Add(2*time.Second),
	))

	done, ok := store.Get("session-1:workflow-step:exec-1:math-solver")
	if !ok {
		t.Fatal("expected completed structured child to remain available")
	}
	if done.Active || done.State != "completed" || done.ProcessState != "closed" || done.SnapshotKind != "archived" {
		t.Fatalf("unexpected completed lifecycle: active=%v state=%q process=%q snapshot=%q", done.Active, done.State, done.ProcessState, done.SnapshotKind)
	}
	if done.Content != "The answer is 327." {
		t.Fatalf("unexpected structured content: %q", done.Content)
	}
	if done.ClosesAt != nil || done.RetentionSeconds != 0 {
		t.Fatalf("completed structured child must stay in Done until dismissed: closes_at=%v retention=%d", done.ClosesAt, done.RetentionSeconds)
	}
	if snapshots := store.List("session-1"); len(snapshots) != 1 {
		t.Fatalf("expected one retained Done entry, got %d", len(snapshots))
	}
}

func TestStoreUsesExecutionIdentityWhenParallelStepContextIsStale(t *testing.T) {
	store := NewStore()
	now := time.Now()
	const executionID = "exec-word-task-1785053307584570000"
	const expectedOwner = "workflow-step:" + executionID + ":word-task"

	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_start",
		executionID,
		map[string]interface{}{
			"agent_id":        executionID,
			"execution_kind":  "workflow_step",
			"current_step_id": "calc-task",
			"step_id":         "calc-task",
			"step_title":      "Nested Calc Task",
		},
		now,
	))

	snapshot, ok := store.Get("session-1:" + expectedOwner)
	if !ok {
		t.Fatal("expected execution identity to retain word-task ownership")
	}
	if snapshot.StepID != "word-task" {
		t.Fatalf("step id = %q, want word-task", snapshot.StepID)
	}
	if snapshot.StepName == "Nested Calc Task" {
		t.Fatal("stale sibling title relabeled the word task")
	}
	if _, crossed := store.Get("session-1:workflow-step:" + executionID + ":calc-task"); crossed {
		t.Fatal("stale shared context created a crossed calc-task terminal")
	}
}

func TestStoreCanonicalizesCrossedWorkflowStepOwner(t *testing.T) {
	store := NewStore()
	now := time.Now()
	const executionID = "exec-calc-task-1785055325563679000"
	const correctOwner = "workflow-step:" + executionID + ":calc-task"
	const crossedOwner = "workflow-step:" + executionID + ":word-task"

	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_start",
		crossedOwner,
		map[string]interface{}{
			"execution_id":    crossedOwner,
			"execution_kind":  "message_sequence_item",
			"current_step_id": "word-task",
			"step_id":         "word-task",
			"step_title":      "Nested Word Task",
		},
		now,
	))

	snapshot, ok := store.Get("session-1:" + correctOwner)
	if !ok {
		t.Fatal("expected crossed owner to be canonicalized to calc-task")
	}
	if snapshot.StepID != "calc-task" {
		t.Fatalf("step id = %q, want calc-task", snapshot.StepID)
	}
	if snapshot.StepName == "Nested Word Task" {
		t.Fatal("stale sibling title relabeled the calc task")
	}
	if _, crossed := store.Get("session-1:" + crossedOwner); crossed {
		t.Fatal("crossed workflow-step owner was retained")
	}
}

func TestStorePreservesStepTitleWhenCompletionCarriesSiblingTitle(t *testing.T) {
	store := NewStore()
	now := time.Now()
	const executionID = "exec-calc-task-1785055325563679000"
	const ownerID = "workflow-step:" + executionID + ":calc-task"

	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_start",
		executionID,
		map[string]interface{}{
			"agent_id":        executionID,
			"execution_kind":  "workflow_step",
			"current_step_id": "calc-task",
			"step_id":         "calc-task",
			"step_title":      "Nested Calc Task",
		},
		now,
	))
	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_end",
		ownerID,
		map[string]interface{}{
			"execution_id":    ownerID,
			"execution_kind":  "workflow_step",
			"current_step_id": "calc-task",
			"step_id":         "calc-task",
			"step_title":      "Nested Word Task",
		},
		now.Add(time.Second),
	))

	snapshot, ok := store.Get("session-1:" + ownerID)
	if !ok {
		t.Fatal("expected calc-task transcript")
	}
	if snapshot.StepName != "Nested Calc Task" {
		t.Fatalf("step name = %q, want Nested Calc Task", snapshot.StepName)
	}
}

func TestStoreKeepsInterleavedParallelStepContentWithExecutionOwner(t *testing.T) {
	store := NewStore()
	now := time.Now()
	const calcExecutionID = "exec-calc-task-1785055325563679000"
	const wordExecutionID = "exec-word-task-1785055325563679001"

	chunk := func(executionID, staleStepID, staleTitle, content string, at time.Time) storeevents.Event {
		return storeevents.Event{
			Type:          "streaming_chunk",
			SessionID:     "session-1",
			ExecutionID:   "workflow-step:" + executionID + ":" + staleStepID,
			ExecutionKind: "message_sequence_item",
			Timestamp:     at,
			Data: &agentevents.AgentEvent{
				Type: agentevents.StreamingChunk,
				Data: &agentevents.StreamingChunkEvent{
					BaseEventData: agentevents.BaseEventData{Metadata: map[string]interface{}{
						"execution_id":    "workflow-step:" + executionID + ":" + staleStepID,
						"execution_kind":  "message_sequence_item",
						"current_step_id": staleStepID,
						"step_id":         staleStepID,
						"step_title":      staleTitle,
					}},
					Content:    content,
					ChunkIndex: 1,
					Source:     agentevents.StreamingChunkSourceTranscript,
				},
			},
		}
	}

	// Shared workflow context points at the opposite sibling for both chunks.
	// The execution ID still carries the authoritative owning plan step.
	store.HandleEvent("session-1", chunk(
		calcExecutionID,
		"word-task",
		"Nested Word Task",
		"42 is the arithmetic result.",
		now,
	))
	store.HandleEvent("session-1", chunk(
		wordExecutionID,
		"calc-task",
		"Nested Calc Task",
		"ALPHA is the word result.",
		now.Add(time.Second),
	))

	calc, ok := store.Get("session-1:workflow-step:" + calcExecutionID + ":calc-task")
	if !ok {
		t.Fatal("expected calc-task transcript")
	}
	word, ok := store.Get("session-1:workflow-step:" + wordExecutionID + ":word-task")
	if !ok {
		t.Fatal("expected word-task transcript")
	}
	if calc.StepID != "calc-task" || calc.Content != "42 is the arithmetic result." {
		t.Fatalf("calc transcript crossed: step=%q content=%q", calc.StepID, calc.Content)
	}
	if word.StepID != "word-task" || word.Content != "ALPHA is the word result." {
		t.Fatalf("word transcript crossed: step=%q content=%q", word.StepID, word.Content)
	}
	if strings.Contains(calc.Content, "ALPHA") || strings.Contains(word.Content, "42") {
		t.Fatalf("parallel sibling content was mixed: calc=%q word=%q", calc.Content, word.Content)
	}
}

func TestStoreNestedMessageSequenceEndSettlesAndNextTurnReactivates(t *testing.T) {
	store := NewStore()
	now := time.Now()
	const parentExecutionID = "exec-calc-task-1785053307554142000"
	const ownerID = "workflow-step:" + parentExecutionID + ":calc-task"
	firstAgentID := "msgseq-calc-task-execute-and-verify-1785053307579788000"
	firstMetadata := map[string]interface{}{
		"agent_id":            firstAgentID,
		"parent_execution_id": parentExecutionID,
		"execution_kind":      "message_sequence_item",
		"step_id":             "calc-task",
		"step_title":          "Nested Calc Task",
	}

	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_start",
		firstAgentID,
		firstMetadata,
		now,
	))
	endMetadata := map[string]interface{}{}
	for key, value := range firstMetadata {
		endMetadata[key] = value
	}
	endMetadata["result"] = "Calculation completed."
	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_end",
		firstAgentID,
		endMetadata,
		now.Add(time.Second),
	))

	done, ok := store.Get("session-1:" + ownerID)
	if !ok {
		t.Fatal("expected owning message-sequence transcript")
	}
	if done.Active || done.State != "completed" {
		t.Fatalf("nested item end must settle transcript: active=%v state=%q", done.Active, done.State)
	}
	if !strings.Contains(done.Content, "Calculation completed.") {
		t.Fatalf("nested result missing from transcript: %q", done.Content)
	}

	nextAgentID := "msgseq-calc-task-automatic-final-validation-1785053307579788001"
	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_start",
		nextAgentID,
		map[string]interface{}{
			"agent_id":            nextAgentID,
			"parent_execution_id": parentExecutionID,
			"execution_kind":      "message_sequence_item",
			"step_id":             "calc-task",
		},
		now.Add(2*time.Second),
	))
	running, _ := store.Get("session-1:" + ownerID)
	if !running.Active || running.State != "running" {
		t.Fatalf("next item must reactivate transcript: active=%v state=%q", running.Active, running.State)
	}
}

func TestStoreUnifiesBackgroundDelegationContentWithItsLifecycleTerminal(t *testing.T) {
	// A delegate-tool background agent from an AgentWorks chat
	// has two disjoint event sources for the SAME logical execution: the
	// lifecycle wrapper (background_agent_started/completed, tagged with
	// agent_id) and the real content (tool calls, messages), forwarded by
	// DelegationEventObserver tagged only with correlation_id — a freshly
	// minted delegationID, never agent_id. Without delegation_start linking
	// the two, the content lands under a second, disjoint terminal ID and the
	// lifecycle entry never leaves "running" (terminalExecutionKind used to
	// bail out on an empty kind before reaching the agent_id fallback).
	store := NewStore()
	now := time.Now()
	const agentID = "chat-0001"
	const delegationID = "delegation-0-1785039217239436000"

	store.HandleEvent("session-1", storeevents.Event{
		Type:      "delegation_start",
		Timestamp: now,
		SessionID: "session-1",
		Data: &agentevents.AgentEvent{
			Type:          agentevents.EventType("delegation_start"),
			SessionID:     "session-1",
			CorrelationID: delegationID,
			Data: &storeevents.DelegationStartEventData{
				DelegationID:      delegationID,
				BackgroundAgentID: agentID,
				Instruction:       "Audit the Chats folder",
			},
		},
	})

	store.HandleEvent("session-1", storeevents.Event{
		Type:      "background_agent_started",
		Timestamp: now,
		SessionID: "session-1",
		Data: &agentevents.AgentEvent{
			Type:      agentevents.EventType("background_agent_started"),
			SessionID: "session-1",
			Data: &orchestratorevents.BackgroundAgentStartedEvent{
				AgentID: agentID,
				Name:    "Chats folder age audit",
				Kind:    orchestratorevents.ExecutionKindSubAgent,
			},
		},
	})

	// The real content, tagged only with correlation_id — exactly what
	// DelegationEventObserver.HandleEvent produces for a background delegation.
	store.HandleEvent("session-1", storeevents.Event{
		Type:      "streaming_chunk",
		Timestamp: now.Add(time.Second),
		SessionID: "session-1",
		Data: &agentevents.AgentEvent{
			Type:          agentevents.StreamingChunk,
			SessionID:     "session-1",
			CorrelationID: delegationID,
			Data: &agentevents.StreamingChunkEvent{
				Content:    "65 items total: 45 very old, 14 stale, 6 recent.",
				ChunkIndex: 1,
				Source:     agentevents.StreamingChunkSourceTranscript,
			},
		},
	})

	store.HandleEvent("session-1", storeevents.Event{
		Type:      "background_agent_completed",
		Timestamp: now.Add(2 * time.Second),
		SessionID: "session-1",
		Data: &agentevents.AgentEvent{
			Type:      agentevents.EventType("background_agent_completed"),
			SessionID: "session-1",
			Data: &orchestratorevents.BackgroundAgentCompletedEvent{
				AgentID: agentID,
				Name:    "Chats folder age audit",
				Status:  "completed",
				Result:  "report written",
			},
		},
	})

	snapshots := store.List("session-1")
	if len(snapshots) != 1 {
		t.Fatalf("expected the lifecycle and content events to collapse into ONE terminal, got %d: %+v", len(snapshots), snapshots)
	}

	done, ok := store.Get("session-1:" + agentID)
	if !ok {
		t.Fatalf("expected the terminal to be keyed by the background agent's own id %q", agentID)
	}
	if done.Active || done.State != "completed" {
		t.Fatalf("expected the completion event to mark it done (stuck running means terminalExecutionKind rejected the completed event): active=%v state=%q", done.Active, done.State)
	}
	if !strings.Contains(done.Content, "65 items total") {
		t.Fatalf("expected the delegation's real content (tagged only with correlation_id) to land on the same terminal as its agent_id-tagged lifecycle events, got content: %q", done.Content)
	}
}

func TestStoreDoesNotCreateStructuredDuplicateForMainTranscript(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     time.Now(),
		SessionID:     "session-1",
		ExecutionID:   "main:session-1",
		ExecutionKind: "main_agent",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{"execution_kind": "main_agent"},
				},
				Content:    "clean main transcript",
				ChunkIndex: 1,
				Source:     agentevents.StreamingChunkSourceTranscript,
			},
		},
	})
	if snapshots := store.List("session-1"); len(snapshots) != 0 {
		t.Fatalf("main transcript must not create a second terminal entry: %+v", snapshots)
	}
}

func TestStoreCollapsesMessageSequenceLifecycleIntoOneDetailedStep(t *testing.T) {
	store := NewStore()
	now := time.Now()
	rootExecutionID := "exec-math-solver-1784967748400792000"
	ownerID := "workflow-step:" + rootExecutionID + ":math-solver"
	baseMetadata := map[string]interface{}{
		"current_step_id": "math-solver",
		"step_title":      "Math Solver",
		"workflow_path":   "Workflow/testing",
	}
	backgroundEvent := func(eventType string, data map[string]interface{}, at time.Time) storeevents.Event {
		return storeevents.Event{
			Type:      eventType,
			Timestamp: at,
			SessionID: "session-1",
			Data: &agentevents.AgentEvent{
				Type: agentevents.EventType(eventType),
				Data: &agentevents.GenericEventData{
					BaseEventData: agentevents.BaseEventData{Metadata: baseMetadata},
					Data:          data,
				},
			},
		}
	}

	store.HandleEvent("session-1", backgroundEvent("background_agent_started", map[string]interface{}{
		"agent_id": rootExecutionID,
		"name":     "Math Solver",
	}, now))
	store.HandleEvent("session-1", backgroundEvent("background_agent_started", map[string]interface{}{
		"agent_id":            "msgseq-math-solver-execute-and-verify-1784967748408960000",
		"parent_execution_id": rootExecutionID,
		"name":                "Message sequence item: execute and verify",
	}, now.Add(time.Second)))
	store.HandleEvent("session-1", storeevents.Event{
		Type:          string(agentevents.UserMessage),
		Timestamp:     now.Add(2 * time.Second),
		SessionID:     "session-1",
		ExecutionID:   ownerID,
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.UserMessage,
			Data: &agentevents.UserMessageEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: baseMetadata},
				Content:       "Solve the expression and verify the output.",
				Role:          "user",
			},
		},
	})
	store.HandleEvent("session-1", toolStartEvent(ownerID, "tool-1", "execute_shell_command", `{"command":"jq . fixture.json"}`, baseMetadata))
	store.HandleEvent("session-1", toolEndEvent(ownerID, "tool-1", "execute_shell_command", `{"expression":"17 * 19 + 4"}`, baseMetadata))
	store.HandleEvent("session-1", storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     now.Add(3 * time.Second),
		SessionID:     "session-1",
		ExecutionID:   ownerID,
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: baseMetadata},
				Content:       "< asst: The verified result is 327.",
				ChunkIndex:    1,
			},
		},
	})
	store.HandleEvent("session-1", backgroundEvent("background_agent_completed", map[string]interface{}{
		"agent_id":            "msgseq-math-solver-execute-and-verify-1784967748408960000",
		"parent_execution_id": rootExecutionID,
		"status":              "completed",
		"result":              "The item completed.",
	}, now.Add(4*time.Second)))
	store.HandleEvent("session-1", backgroundEvent("background_agent_started", map[string]interface{}{
		"agent_id":            "msgseq-math-solver-automatic-final-validation-1784967795644586000",
		"parent_execution_id": rootExecutionID,
		"name":                "Automatic final validation",
	}, now.Add(5*time.Second)))
	store.HandleEvent("session-1", backgroundEvent("background_agent_completed", map[string]interface{}{
		"agent_id":            "msgseq-math-solver-automatic-final-validation-1784967795644586000",
		"parent_execution_id": rootExecutionID,
		"status":              "completed",
		"result":              "Validation passed.",
	}, now.Add(6*time.Second)))
	store.HandleEvent("session-1", backgroundEvent("background_agent_completed", map[string]interface{}{
		"agent_id": rootExecutionID,
		"name":     "Math Solver",
		"status":   "completed",
	}, now.Add(7*time.Second)))

	snapshots := store.List("session-1")
	if len(snapshots) != 1 {
		t.Fatalf("expected one logical step terminal, got %d: %#v", len(snapshots), snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.TerminalID != "session-1:"+ownerID {
		t.Fatalf("terminal id = %q, want canonical workflow step owner", snapshot.TerminalID)
	}
	if snapshot.DisplayTitle != "Testing -> Math Solver" {
		t.Fatalf("display title = %q", snapshot.DisplayTitle)
	}
	for _, want := range []string{
		"> user: Solve the expression and verify the output.",
		"→ tool: execute_shell_command(",
		`✓ result execute_shell_command: {"expression":"17 * 19 + 4"}`,
		"< asst: The verified result is 327.",
		"< asst: Validation passed.",
	} {
		if !strings.Contains(snapshot.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, snapshot.Content)
		}
	}
	if strings.TrimSpace(snapshot.Content) == "The execution completed." {
		t.Fatalf("detailed execution must not collapse to the generic completion fallback")
	}
	if snapshot.Active || snapshot.State != "completed" {
		t.Fatalf("outer completion should close the logical step: active=%v state=%q", snapshot.Active, snapshot.State)
	}
}

func TestStoreFullRunCompletionDoesNotCreateConversationTerminal(t *testing.T) {
	store := NewStore()
	now := time.Now()
	store.HandleEvent("session-1", storeevents.Event{
		Type:      "background_agent_completed",
		Timestamp: now,
		SessionID: "session-1",
		Data: &agentevents.AgentEvent{
			Type:      "background_agent_completed",
			SessionID: "session-1",
			Data: &orchestratorevents.BackgroundAgentCompletedEvent{
				BaseEventData: agentevents.BaseEventData{Timestamp: now, SessionID: "session-1"},
				AgentID:       "workflow-full-abc",
				Name:          "Full Workflow Execution",
				Status:        "failed",
				Kind:          orchestratorevents.ExecutionKindFullRun,
			},
		},
	})

	if snapshots := store.List("session-1"); len(snapshots) != 0 {
		t.Fatalf("full-run container completion must not create a conversation terminal: %+v", snapshots)
	}
}

func TestStoreAsyncCompletionBatchSettlesVisibleStructuredTranscript(t *testing.T) {
	store := NewStore()
	now := time.Now()
	const routeID = "word-task"
	const executionID = "todo-sub-word-task-sub-word-task-todo-id-1785050966672382000"
	const ownerID = "workflow-step:exec-word-task-1785050966675717000:word-task"

	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"background_agent_started",
		executionID,
		map[string]interface{}{
			"agent_id":       executionID,
			"execution_kind": "background_agent",
			"step_id":        routeID,
			"step_title":     "Nested Word Task",
		},
		now,
	))
	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_start",
		ownerID,
		map[string]interface{}{
			"execution_owner_id": ownerID,
			"execution_kind":     "workflow_step",
			"step_id":            routeID,
			"step_title":         "Nested Word Task",
		},
		now.Add(time.Second),
	))
	store.HandleEvent("session-1", storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     now.Add(2 * time.Second),
		SessionID:     "session-1",
		ExecutionID:   ownerID,
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: map[string]interface{}{
					"execution_owner_id": ownerID,
					"step_id":            routeID,
				}},
				Content:    "The word probe completed with detailed evidence.",
				ChunkIndex: 1,
			},
		},
	})

	// Completion batches arrive as a main-conversation auto-notification, not
	// as a child event. The store must still consume their lifecycle payload.
	store.HandleEvent("session-1", storeevents.Event{
		Type:          string(agentevents.UserMessage),
		Timestamp:     now.Add(3 * time.Second),
		SessionID:     "session-1",
		ExecutionID:   "main:session-1",
		ExecutionKind: "main_agent",
		Data: &agentevents.AgentEvent{
			Type: agentevents.UserMessage,
			Data: &agentevents.UserMessageEvent{
				Content: `[AUTO-NOTIFICATION] SUB-AGENT COMPLETION BATCH

The runtime waited for every child launched in your previous turn. These are authoritative terminal results:

[
  {
    "execution_id": "` + executionID + `",
    "task_id": "sub-word-task-todo-id",
    "route_id": "` + routeID + `",
    "agent_type": "predefined",
    "status": "completed"
  }
]

Continue the same task now. Use successful results as evidence.`,
				Role: "user",
			},
		},
	})

	for _, terminalID := range []string{"session-1:" + executionID, "session-1:" + ownerID} {
		snapshot, ok := store.Get(terminalID)
		if !ok {
			t.Fatalf("expected terminal %q", terminalID)
		}
		if snapshot.Active || snapshot.State != "completed" || snapshot.ProcessState != "closed" || snapshot.SnapshotKind != "archived" {
			t.Fatalf("terminal %q was not settled: active=%v state=%q process=%q snapshot=%q", terminalID, snapshot.Active, snapshot.State, snapshot.ProcessState, snapshot.SnapshotKind)
		}
	}
	transcript, _ := store.Get("session-1:" + ownerID)
	if !strings.Contains(transcript.Content, "detailed evidence") {
		t.Fatalf("completion reconciliation must preserve the real transcript, got %q", transcript.Content)
	}
}

func TestStoreAsyncCompletionBatchPropagatesFailureToVisibleTranscript(t *testing.T) {
	store := NewStore()
	now := time.Now()
	const routeID = "calc-task"
	const ownerID = "workflow-step:exec-calc-task-1785050966675890000:calc-task"

	store.HandleEvent("session-1", genericTerminalLifecycleEvent(
		"orchestrator_agent_start",
		ownerID,
		map[string]interface{}{
			"execution_owner_id": ownerID,
			"execution_kind":     "workflow_step",
			"step_id":            routeID,
		},
		now,
	))
	store.HandleEvent("session-1", storeevents.Event{
		Type:        string(agentevents.UserMessage),
		Timestamp:   now.Add(time.Second),
		SessionID:   "session-1",
		ExecutionID: "main:session-1",
		Data: &agentevents.AgentEvent{
			Type: agentevents.UserMessage,
			Data: &agentevents.UserMessageEvent{
				Content: `[AUTO-NOTIFICATION] SUB-AGENT COMPLETION BATCH

[
  {
    "execution_id": "todo-sub-calc-task-sub-calc-task-todo-id-1",
    "route_id": "` + routeID + `",
    "status": "failed",
    "error": "validation failed"
  }
]

Continue the same task now.`,
				Role: "user",
			},
		},
	})

	snapshot, ok := store.Get("session-1:" + ownerID)
	if !ok {
		t.Fatal("expected visible transcript")
	}
	if snapshot.Active || snapshot.State != "failed" || snapshot.CloseReason != "validation failed" {
		t.Fatalf("failure was not propagated: active=%v state=%q reason=%q", snapshot.Active, snapshot.State, snapshot.CloseReason)
	}
}

func TestStoreTracksTerminalChunksByOwner(t *testing.T) {
	store := NewStore()
	now := time.Now()

	store.HandleEvent("session-1", storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     now,
		SessionID:     "session-1",
		ExecutionID:   "exec-1",
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{
						"kind":          "terminal",
						"step_id":       "step-sentry-evidence",
						"step_title":    "Pull Sentry Evidence",
						"step_type":     "todo_task",
						"workflow_path": "Workflow/rtsrca",
					},
				},
				Content:    "screen one",
				ChunkIndex: 3,
			},
		},
	})

	snapshots := store.List("session-1")
	if len(snapshots) != 1 {
		t.Fatalf("expected one terminal snapshot, got %d", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.TerminalID != "session-1:exec-1" {
		t.Fatalf("unexpected terminal id: %s", snapshot.TerminalID)
	}
	if snapshot.Content != "screen one" {
		t.Fatalf("unexpected content: %q", snapshot.Content)
	}
	if !snapshot.Active {
		t.Fatalf("expected terminal to be active")
	}
	if snapshot.State != "running" {
		t.Fatalf("unexpected state: %q", snapshot.State)
	}
	if snapshot.Label != "Pull Sentry Evidence" {
		t.Fatalf("unexpected label: %q", snapshot.Label)
	}
	if snapshot.Scope != "workflow_step" {
		t.Fatalf("unexpected scope: %q", snapshot.Scope)
	}
	if snapshot.WorkflowName != "Rtsrca" {
		t.Fatalf("unexpected workflow name: %q", snapshot.WorkflowName)
	}
	if snapshot.StepID != "step-sentry-evidence" {
		t.Fatalf("unexpected step id: %q", snapshot.StepID)
	}
	if snapshot.StepName != "Pull Sentry Evidence" {
		t.Fatalf("unexpected step name: %q", snapshot.StepName)
	}
	if snapshot.StepType != "todo_task" {
		t.Fatalf("unexpected step type: %q", snapshot.StepType)
	}
	// Display title prefers the human step title over the raw step ID
	// ("step-sentry-evidence"); the ID remains in snapshot.StepID for lookups.
	if snapshot.DisplayTitle != "Rtsrca -> Pull Sentry Evidence" {
		t.Fatalf("unexpected display title: %q", snapshot.DisplayTitle)
	}
	if snapshot.DisplayMeta != "Todo task · Workflow step" {
		t.Fatalf("unexpected display meta: %q", snapshot.DisplayMeta)
	}
	if snapshot.Status.StatusText == "" {
		t.Fatalf("expected derived status text")
	}
}

func TestStoreAttachesPreValidationStatusToStepTerminal(t *testing.T) {
	store := NewStore()
	ownerID := "workflow-step:workflow-full-123:route-pick-topic"
	now := time.Now()

	store.HandleEvent("session-1", terminalEventWithMetadataAt("streaming_chunk", ownerID, "route topic is running", 1, map[string]interface{}{
		"step_id":       "route-pick-topic",
		"step_title":    "Pick Trending Topic",
		"workflow_path": "Workflow/instagram",
	}, now))
	store.HandleEvent("session-1", storeevents.Event{
		Type:        "pre_validation_completed",
		Timestamp:   now.Add(time.Second),
		SessionID:   "session-1",
		ExecutionID: ownerID,
		Data: &agentevents.AgentEvent{
			Type: agentevents.EventType("pre_validation_completed"),
			Data: &agentevents.GenericEventData{
				Data: map[string]interface{}{
					"step_id":       "route-pick-topic",
					"step_path":     "step-1-sub-route-pick-topic",
					"step_title":    "Pick Trending Topic",
					"overall_pass":  true,
					"passed_checks": 7,
					"failed_checks": 0,
					"total_checks":  7,
				},
			},
		},
	})

	snapshot, ok := store.Get("session-1:" + ownerID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Status.PreValidationStatus != "passed" {
		t.Fatalf("pre-validation status = %q, want passed", snapshot.Status.PreValidationStatus)
	}
	if snapshot.Status.PreValidationSummary != "Pre-validation passed: 7/7 checks" {
		t.Fatalf("pre-validation summary = %q", snapshot.Status.PreValidationSummary)
	}

	store.HandleEvent("session-1", terminalEventWithMetadataAt("streaming_chunk", ownerID, "route topic finished", 2, map[string]interface{}{
		"step_id":       "route-pick-topic",
		"step_title":    "Pick Trending Topic",
		"workflow_path": "Workflow/instagram",
	}, now.Add(2*time.Second)))
	snapshot, ok = store.Get("session-1:" + ownerID)
	if !ok {
		t.Fatalf("expected terminal snapshot after refresh")
	}
	if snapshot.Status.PreValidationSummary != "Pre-validation passed: 7/7 checks" {
		t.Fatalf("pre-validation summary should survive terminal refresh, got %q", snapshot.Status.PreValidationSummary)
	}
}

func TestStoreCollapsesSnapshotsForSameTmuxSession(t *testing.T) {
	store := NewStore()
	now := time.Now()

	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:workflow-full-1:route-pick-topic",
		"route topic screen",
		1,
		map[string]interface{}{
			"tmux_session":    "shared-pane",
			"current_step_id": "route-pick-topic",
			"execution_kind":  "workflow_step",
			"scope":           "workflow_step",
			"workflow_path":   "Workflow/instagram",
		},
		now,
	))
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:workflow-full-1:step-create-reel",
		"create reel screen",
		2,
		map[string]interface{}{
			"tmux_session":    "shared-pane",
			"current_step_id": "route-pick-topic",
			"execution_kind":  "workflow_step",
			"scope":           "workflow_step",
			"workflow_path":   "Workflow/instagram",
		},
		now.Add(time.Second),
	))

	snapshots := store.List("session-1")
	if len(snapshots) != 1 {
		t.Fatalf("expected one terminal snapshot for one tmux pane, got %d", len(snapshots))
	}
	snapshot := snapshots[0]
	if snapshot.TerminalID != "session-1:workflow-step:workflow-full-1:step-create-reel" {
		t.Fatalf("unexpected terminal id: %s", snapshot.TerminalID)
	}
	if snapshot.Content != "create reel screen" {
		t.Fatalf("unexpected content: %q", snapshot.Content)
	}
	if snapshot.StepID != "step-create-reel" {
		t.Fatalf("step id should come from workflow-step owner, got %q", snapshot.StepID)
	}
	if _, ok := store.Get("session-1:workflow-step:workflow-full-1:route-pick-topic"); ok {
		t.Fatalf("old terminal alias for the same tmux pane should be removed")
	}
}

func TestStorePrefersWorkflowStepOwnerOverStaleCurrentStepMetadata(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:workflow-full-1:step-create-reel",
		"screen",
		1,
		map[string]interface{}{
			"current_step_id": "route-pick-topic",
			"execution_kind":  "workflow_step",
			"scope":           "workflow_step",
			"workflow_path":   "Workflow/instagram",
		},
		time.Time{},
	))

	snapshot, ok := store.Get("session-1:workflow-step:workflow-full-1:step-create-reel")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.StepID != "step-create-reel" {
		t.Fatalf("step id = %q, want step-create-reel", snapshot.StepID)
	}
	if snapshot.DisplayTitle != "Instagram -> step-create-reel" {
		t.Fatalf("display title = %q", snapshot.DisplayTitle)
	}
}

func TestStoreMergesNonTmuxWorkflowToolCallsIntoTerminalContent(t *testing.T) {
	store := NewStore()
	ownerID := "workflow-step:workflow-full-1:check-cdp"
	metadata := map[string]interface{}{
		"execution_owner_id": ownerID,
		"step_transport":     "api",
		"current_step_id":    "check-cdp",
		"execution_kind":     "workflow_step",
		"scope":              "workflow_step",
		"workflow_path":      "Workflow/upwork",
	}

	store.HandleEvent("session-1", terminalEventWithMetadata(
		ownerID,
		"$ gemini model=auto msgs=2\n> user: prompt",
		1,
		metadata,
		time.Now(),
	))
	store.HandleEvent("session-1", toolStartEvent(ownerID, "call-1", "mcp_api-bridge_execute_shell_command", `{"command":"env | grep MCP_API_TOKEN"}`, metadata))
	store.HandleEvent("session-1", toolEndEvent(ownerID, "call-1", "mcp_api-bridge_execute_shell_command", `{"stdout":"MCP_API_TOKEN=secret-token\nMCP_AUTH=Authorization: Bearer secret-token\nok"}`, metadata))
	store.HandleEvent("session-1", terminalEventWithMetadata(
		ownerID,
		"$ gemini model=auto msgs=2\n> user: prompt\n[done · 1s · 10 in · 2 out]",
		2,
		metadata,
		time.Now().Add(time.Second),
	))

	snapshot, ok := store.Get("session-1:" + ownerID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !strings.Contains(snapshot.Content, "→ tool: mcp_api-bridge_execute_shell_command") {
		t.Fatalf("expected tool start line in content:\n%s", snapshot.Content)
	}
	if !strings.Contains(snapshot.Content, "✓ result mcp_api-bridge_execute_shell_command") {
		t.Fatalf("expected tool result line in content:\n%s", snapshot.Content)
	}
	if strings.Contains(snapshot.Content, "secret-token") {
		t.Fatalf("expected MCP token to be redacted:\n%s", snapshot.Content)
	}
	if !strings.Contains(snapshot.Content, "MCP_API_TOKEN=[redacted]") {
		t.Fatalf("expected redacted token marker:\n%s", snapshot.Content)
	}
	if !strings.Contains(snapshot.Content, "MCP_AUTH=Authorization: Bearer [redacted]") {
		t.Fatalf("expected redacted auth marker:\n%s", snapshot.Content)
	}
	toolIdx := strings.Index(snapshot.Content, "→ tool:")
	doneIdx := strings.Index(snapshot.Content, "[done")
	if toolIdx < 0 || doneIdx < 0 || toolIdx > doneIdx {
		t.Fatalf("tool rows should appear before done footer:\n%s", snapshot.Content)
	}
}

func TestStoreUsesToolMetadataNameWhenStructuredEventOmitsIt(t *testing.T) {
	store := NewStore()
	ownerID := "workflow-step:workflow-full-1:check-cdp"
	metadata := map[string]interface{}{
		"execution_owner_id": ownerID,
		"step_transport":     "structured",
		"current_step_id":    "check-cdp",
		"execution_kind":     "workflow_step",
		"scope":              "workflow_step",
		"workflow_path":      "Workflow/upwork",
		"tool_name":          "execute_shell_command",
	}

	store.HandleEvent("session-1", terminalEventWithMetadata(
		ownerID,
		"> user: inspect the workspace",
		1,
		metadata,
		time.Now(),
	))
	store.HandleEvent("session-1", toolStartEvent(ownerID, "call-1", "", `{"command":"pwd"}`, metadata))
	store.HandleEvent("session-1", toolEndEvent(ownerID, "call-1", "", `{"stdout":"/workspace"}`, metadata))

	snapshot, ok := store.Get("session-1:" + ownerID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !strings.Contains(snapshot.Content, "→ tool: execute_shell_command") {
		t.Fatalf("expected metadata tool name in content:\n%s", snapshot.Content)
	}
	if strings.Contains(snapshot.Content, "→ tool: tool") {
		t.Fatalf("unexpected anonymous tool label in content:\n%s", snapshot.Content)
	}
}

func TestRedactSensitiveTerminalTextCoversProviderKeys(t *testing.T) {
	googleKey := strings.Join([]string{"AIza", "Syabcdefghijklmnopqrstuvwxyz1234567"}, "")
	input := strings.Join([]string{
		"OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz123456",
		"ANTHROPIC_API_KEY=sk-ant-api03-abcdefghijklmnopqrstuvwxyz123456",
		"raw key sk-or-v1-abcdefghijklmnopqrstuvwxyz123456",
		"GOOGLE_API_KEY=" + googleKey,
		"raw google " + googleKey,
		"Authorization: Bearer sk-proj-abcdefghijklmnopqrstuvwxyz123456",
	}, "\n")

	got := RedactSensitiveTerminalText(input)
	for _, leaked := range []string{
		"sk-proj-abcdefghijklmnopqrstuvwxyz123456",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz123456",
		"sk-or-v1-abcdefghijklmnopqrstuvwxyz123456",
		googleKey,
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted text leaked %q:\n%s", leaked, got)
		}
	}
	for _, want := range []string{
		"OPENAI_API_KEY=[redacted]",
		"ANTHROPIC_API_KEY=[redacted]",
		"sk-[redacted]",
		"GOOGLE_API_KEY=[redacted]",
		"AIza[redacted]",
		"Authorization: Bearer [redacted]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted text missing %q:\n%s", want, got)
		}
	}
}

func TestSnapshotWithContextFillsReadableDisplay(t *testing.T) {
	snapshot := Snapshot{
		TerminalID:    "session-1:main:821ee897-76aa-4b82-ae09-85250206d104",
		SessionID:     "session-1",
		OwnerID:       "main:821ee897-76aa-4b82-ae09-85250206d104",
		ExecutionKind: "main_agent",
		Label:         "main:821ee897-76aa-4b82-ae09-85250206d104",
		Scope:         "execution",
		Status:        Status{ToolSummary: "api-bridge x2"},
	}

	enriched := snapshot.WithContext(Context{
		WorkflowLabel: "Upwork",
		WorkspacePath: "Workflow/upwork",
		ExecutionName: "Workflow builder",
	})

	if enriched.DisplayTitle != "Upwork -> Main agent" {
		t.Fatalf("display title = %q", enriched.DisplayTitle)
	}
	if enriched.DisplayMeta != "" {
		t.Fatalf("display meta = %q", enriched.DisplayMeta)
	}
	if enriched.WorkflowName != "Upwork" {
		t.Fatalf("workflow name = %q", enriched.WorkflowName)
	}
	if enriched.AgentName != "" {
		t.Fatalf("agent name = %q", enriched.AgentName)
	}
}

func TestSnapshotWithContextDoesNotOverwriteTerminalIdentityFromCurrentExecution(t *testing.T) {
	snapshot := Snapshot{
		TerminalID:    "session-1:bg-agent-1",
		SessionID:     "session-1",
		OwnerID:       "bg-agent-1",
		ExecutionKind: "background_agent",
		Label:         "research-agent",
		Scope:         "background_agent",
	}

	enriched := snapshot.WithContext(Context{
		WorkflowLabel: "Upwork",
		WorkspacePath: "Workflow/upwork",
		ExecutionName: "Some other currently active child",
	})

	if enriched.DisplayTitle != "Upwork -> Research agent" {
		t.Fatalf("display title = %q", enriched.DisplayTitle)
	}
	if enriched.AgentName != "" || enriched.StepName != "" {
		t.Fatalf("terminal identity was overwritten: agent=%q step=%q", enriched.AgentName, enriched.StepName)
	}
}

func TestSnapshotWithContextKeepsWorkflowStepIDInTitle(t *testing.T) {
	snapshot := Snapshot{
		TerminalID:    "session-1:workflow-step:run:bid-submit",
		SessionID:     "session-1",
		OwnerID:       "workflow-step:run:bid-submit",
		ExecutionKind: "workflow_step",
		StepID:        "bid-submit",
		Scope:         "workflow_step",
	}

	enriched := snapshot.WithContext(Context{
		WorkflowLabel: "upwork",
		WorkspacePath: "Workflow/upwork",
		ExecutionName: "full-workflow [daily-bid / iteration-0]",
	})

	if enriched.DisplayTitle != "upwork -> bid-submit" {
		t.Fatalf("display title = %q", enriched.DisplayTitle)
	}
	if enriched.StepName != "" {
		t.Fatalf("step name should not be filled from execution name when step id exists, got %q", enriched.StepName)
	}
}

func TestStoreIgnoresOlderTerminalChunks(t *testing.T) {
	store := NewStore()

	for _, item := range []struct {
		index   int
		content string
	}{
		{index: 5, content: "new screen"},
		{index: 4, content: "old screen"},
	} {
		store.HandleEvent("session-1", storeevents.Event{
			Type:        "streaming_chunk",
			SessionID:   "session-1",
			ExecutionID: "exec-1",
			Data: &agentevents.AgentEvent{
				Type: agentevents.StreamingChunk,
				Data: &agentevents.StreamingChunkEvent{
					BaseEventData: agentevents.BaseEventData{
						Metadata: map[string]interface{}{"kind": "terminal"},
					},
					Content:    item.content,
					ChunkIndex: item.index,
				},
			},
		})
	}

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Content != "new screen" {
		t.Fatalf("older chunk overwrote newer content: %q", snapshot.Content)
	}
}

func TestStoreAllowsChunkIndexResetForNewTurn(t *testing.T) {
	store := NewStore()
	now := time.Now()

	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "old turn", 12, now))
	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "new turn", 1, now.Add(time.Second)))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Content != "new turn" {
		t.Fatalf("new turn did not update terminal content: %q", snapshot.Content)
	}
	if snapshot.ChunkIndex != 1 {
		t.Fatalf("chunk index = %d, want 1", snapshot.ChunkIndex)
	}
}

func TestStoreArchivesActiveTerminalWhenChunkIndexResetsForFastFollowUp(t *testing.T) {
	store := NewStore()
	now := time.Now()

	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "old turn", 12, now))
	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "new turn starts immediately", 1, now.Add(10*time.Millisecond)))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Content != "new turn starts immediately" {
		t.Fatalf("new turn did not replace canonical terminal content: %q", snapshot.Content)
	}

	snapshots := store.List("session-1")
	if len(snapshots) != 2 {
		t.Fatalf("snapshot count = %d, want canonical plus archived turn", len(snapshots))
	}
	var foundArchive bool
	for _, item := range snapshots {
		if strings.Contains(item.TerminalID, ":turn-") {
			foundArchive = true
			if item.Content != "old turn" {
				t.Fatalf("archived content = %q, want old turn", item.Content)
			}
		}
	}
	if !foundArchive {
		t.Fatalf("expected archived prior turn, got %#v", snapshots)
	}
}

func TestStoreDoesNotArchiveSameTurnWhenLowerChunkExtendsContent(t *testing.T) {
	store := NewStore()
	now := time.Now()
	partial := "$ gemini model=auto msgs=1\n" +
		"> user: run again\n" +
		"→ tool: mcp_api-bridge_execute_shell_command({\"command\":\"curl ...\"})\n" +
		"✓ result mcp_api-bridge_execute_shell_command: workflow started"
	complete := partial + "\n" +
		"< asst: I've triggered another full workflow execution.\n" +
		"[done · 12.3s · 20699 in · 184 out]"

	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "main:session-1", partial, 4, now))
	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "main:session-1", complete, 1, now.Add(10*time.Millisecond)))

	snapshots := store.List("session-1")
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want only canonical turn: %#v", len(snapshots), snapshots)
	}
	if snapshots[0].TerminalID != "session-1:main:session-1" {
		t.Fatalf("terminal id = %q, want canonical main terminal", snapshots[0].TerminalID)
	}
	if snapshots[0].Content != complete {
		t.Fatalf("content = %q, want complete transcript", snapshots[0].Content)
	}
	if snapshots[0].ChunkIndex != 4 {
		t.Fatalf("chunk index = %d, want previous max 4", snapshots[0].ChunkIndex)
	}
}

func TestStoreKeepsRestartedTurnRunningEvenIfFirstPaneLooksIdle(t *testing.T) {
	store := NewStore()
	now := time.Now()

	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "old turn", 12, now))
	store.HandleEvent("session-1", terminalEndEvent("exec-1", nil))
	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "STATUS: COMPLETED\n❯", 1, now.Add(time.Second)))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active || snapshot.State != "running" {
		t.Fatalf("restarted terminal should be running, active=%v state=%q", snapshot.Active, snapshot.State)
	}
	if snapshot.Content != "STATUS: COMPLETED\n❯" {
		t.Fatalf("restart content = %q", snapshot.Content)
	}
}

func TestStoreDoesNotArchiveMainAgentTmuxTurnOnContinuation(t *testing.T) {
	store := NewStore()
	now := time.Now()
	metadata := map[string]interface{}{
		"tmux_session":   "main-pane",
		"execution_kind": "main_agent",
	}

	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		"first user turn",
		8,
		metadata,
		now,
	))
	store.HandleEvent("session-1", terminalEndEvent("main:session-1", metadata))
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		"second user turn",
		1,
		metadata,
		now.Add(time.Second),
	))

	snapshots := store.List("session-1")
	if len(snapshots) != 1 {
		t.Fatalf("expected only canonical main-agent snapshot, got %d: %#v", len(snapshots), snapshots)
	}
	if strings.Contains(snapshots[0].TerminalID, ":turn-") {
		t.Fatalf("expected canonical terminal id, got archived id: %s", snapshots[0].TerminalID)
	}
	if snapshots[0].TerminalID != "session-1:main:session-1" {
		t.Fatalf("terminal id = %q, want canonical", snapshots[0].TerminalID)
	}
	if snapshots[0].Content != "second user turn" {
		t.Fatalf("content = %q, want second user turn", snapshots[0].Content)
	}
}

// TestUpsertStaticSnapshotDoesNotClobberLiveTmuxTerminal guards the resume race:
// the frontend's restore-terminal POST (which publishes the static buffer) can
// land AFTER /api/query has materialized the live tmux pane under the same
// canonical main-agent terminalID. The static publish must not reset the live
// terminal back to Active:false / TmuxSession:"" — that strips the tmux_session
// the frontend needs to fire /resize.
func TestUpsertStaticSnapshotDoesNotClobberLiveTmuxTerminal(t *testing.T) {
	store := NewStore()
	now := time.Now()

	// Materialize the live tmux terminal (what /api/query's re-launch does).
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		"live pane content\n❯",
		0,
		map[string]interface{}{
			"tmux_session":   "mlp-pi-cli-resumed-123",
			"execution_kind": "main_agent",
		},
		now,
	))

	live := store.List("session-1")
	if len(live) != 1 || !live[0].Active || live[0].TmuxSession != "mlp-pi-cli-resumed-123" {
		t.Fatalf("expected one live tmux terminal, got %#v", live)
	}
	canonicalID := live[0].TerminalID

	// A late static re-publish for the SAME canonical terminal must be ignored.
	stored, ok := store.UpsertStaticSnapshot("session-1", Snapshot{
		Content:       "stale persisted buffer",
		ExecutionKind: "main_agent",
	})
	if !ok {
		t.Fatalf("UpsertStaticSnapshot returned ok=false")
	}
	if stored.TerminalID != canonicalID {
		t.Fatalf("static upsert targeted %q, want canonical %q", stored.TerminalID, canonicalID)
	}
	if !stored.Active || stored.TmuxSession != "mlp-pi-cli-resumed-123" {
		t.Fatalf("live terminal was clobbered by static publish: active=%v tmux=%q content=%q",
			stored.Active, stored.TmuxSession, stored.Content)
	}
	if stored.Content != "live pane content\n❯" {
		t.Fatalf("live content overwritten by static buffer: %q", stored.Content)
	}

	after := store.List("session-1")
	if len(after) != 1 || !after[0].Active || after[0].TmuxSession != "mlp-pi-cli-resumed-123" {
		t.Fatalf("expected the live tmux terminal preserved, got %#v", after)
	}
}

// TestUpsertStaticSnapshotPublishesWhenNoLiveTerminal confirms the guard is
// scoped: with no live tmux terminal present (the normal first-restore case),
// the static snapshot still publishes.
func TestUpsertStaticSnapshotPublishesWhenNoLiveTerminal(t *testing.T) {
	store := NewStore()
	stored, ok := store.UpsertStaticSnapshot("session-1", Snapshot{
		Content:       "restored buffer\n❯",
		ExecutionKind: "main_agent",
	})
	if !ok {
		t.Fatalf("UpsertStaticSnapshot returned ok=false")
	}
	if stored.Active || strings.TrimSpace(stored.TmuxSession) != "" {
		t.Fatalf("static snapshot should be inactive with no tmux, got active=%v tmux=%q", stored.Active, stored.TmuxSession)
	}
	if stored.Content != "restored buffer\n❯" {
		t.Fatalf("static content = %q", stored.Content)
	}
}

func TestStoreKeepsArchivedTurnWhenRestartReusesTmuxSession(t *testing.T) {
	store := NewStore()
	now := time.Now()
	metadata := map[string]interface{}{
		"tmux_session":   "shared-retry-pane",
		"execution_kind": "workflow_step",
		"scope":          "workflow_step",
	}

	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:run-1:bid-submit",
		"old attempt",
		12,
		metadata,
		now,
	))
	store.HandleEvent("session-1", terminalEndEvent("workflow-step:run-1:bid-submit", metadata))
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:run-1:bid-submit",
		"new attempt",
		1,
		metadata,
		now.Add(time.Second),
	))

	snapshots := store.List("session-1")
	if len(snapshots) != 2 {
		t.Fatalf("expected archived previous + current terminal, got %d", len(snapshots))
	}
	var archived, current bool
	for _, snapshot := range snapshots {
		switch {
		case strings.Contains(snapshot.TerminalID, ":turn-") && !snapshot.Active && snapshot.Content == "old attempt":
			archived = true
		case snapshot.TerminalID == "session-1:workflow-step:run-1:bid-submit" && snapshot.Active && snapshot.Content == "new attempt":
			current = true
		}
	}
	if !archived || !current {
		t.Fatalf("missing archived/current rows: archived=%v current=%v snapshots=%+v", archived, current, snapshots)
	}
}

func TestStoreMarksTerminalInactiveOnStreamingEnd(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "screen", 1))
	store.HandleEvent("session-1", storeevents.Event{
		Type:        "streaming_end",
		SessionID:   "session-1",
		ExecutionID: "exec-1",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingEnd,
			Data: &agentevents.StreamingEndEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{"kind": "terminal"},
				},
			},
		},
	})

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Active {
		t.Fatalf("expected terminal to be inactive")
	}
	if snapshot.State != "completed" {
		t.Fatalf("expected completed state, got %q", snapshot.State)
	}
}

func TestStoreStreamingEndOverridesHistoricalBusyScrollback(t *testing.T) {
	store := NewStore()
	now := time.Now()
	content := `>_ OpenAI Codex

• Called api-bridge.execute_shell_command

• Working (43s • esc to interrupt)

• Finished the bounded maintenance pass.

› Improve documentation in @filename`
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		content,
		14,
		map[string]interface{}{
			"tmux_session":   "mlp-codex-cli-retained",
			"execution_kind": "main_agent",
			"provider":       "codex-cli",
		},
		now,
	))
	store.HandleEvent("session-1", storeevents.Event{
		Type:        "streaming_end",
		Timestamp:   now.Add(time.Second),
		SessionID:   "session-1",
		ExecutionID: "main:session-1",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingEnd,
			Data: &agentevents.StreamingEndEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: map[string]interface{}{
					"kind":           "terminal",
					"tmux_session":   "mlp-codex-cli-retained",
					"execution_kind": "main_agent",
				}},
			},
		},
	})

	snapshot, ok := store.Get("session-1:main:session-1")
	if !ok {
		t.Fatal("expected terminal snapshot")
	}
	if snapshot.Active || snapshot.State != "completed" {
		t.Fatalf("terminal active/state = %v/%q, want false/completed", snapshot.Active, snapshot.State)
	}
	if snapshot.ProcessState != "live" || snapshot.TmuxSession == "" {
		t.Fatalf("retained process state/tmux = %q/%q, want live/non-empty", snapshot.ProcessState, snapshot.TmuxSession)
	}

	// A harmless retained-pane redraw after completion must not resurrect the
	// turn merely because historical spinner text remains in scrollback.
	redrawn := content + "\n"
	store.RefreshContentWithSource(snapshot.TerminalID, redrawn, "tmux_capture")
	refreshed, ok := store.Get(snapshot.TerminalID)
	if !ok {
		t.Fatal("expected refreshed terminal snapshot")
	}
	if refreshed.Active || refreshed.State != "completed" {
		t.Fatalf("redrawn terminal active/state = %v/%q, want false/completed", refreshed.Active, refreshed.State)
	}
}

func TestStoreIgnoresDelayedStreamingEndFromOlderTurn(t *testing.T) {
	store := NewStore()
	oldTurn := time.Now()
	newTurn := oldTurn.Add(2 * time.Second)
	metadata := map[string]interface{}{
		"tmux_session":   "mlp-codex-cli-retained",
		"execution_kind": "main_agent",
		"provider":       "codex-cli",
	}
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1", "• Working (2s • esc to interrupt)", 1, metadata, newTurn,
	))
	store.HandleEvent("session-1", storeevents.Event{
		Type:        "streaming_end",
		Timestamp:   oldTurn,
		SessionID:   "session-1",
		ExecutionID: "main:session-1",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingEnd,
			Data: &agentevents.StreamingEndEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: map[string]interface{}{
					"kind":           "terminal",
					"tmux_session":   "mlp-codex-cli-retained",
					"execution_kind": "main_agent",
				}},
			},
		},
	})

	snapshot, ok := store.Get("session-1:main:session-1")
	if !ok {
		t.Fatal("expected terminal snapshot")
	}
	if !snapshot.Active || snapshot.State != "running" {
		t.Fatalf("terminal active/state = %v/%q, want true/running", snapshot.Active, snapshot.State)
	}
}

func TestStoreMarksTerminalClosingWithRetention(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "screen", 1))
	store.HandleEvent("session-1", storeevents.Event{
		Type:        "streaming_end",
		SessionID:   "session-1",
		ExecutionID: "exec-1",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingEnd,
			Data: &agentevents.StreamingEndEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{
						"kind":                       "terminal",
						"terminal_retention_seconds": 300,
					},
				},
			},
		},
	})

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Active {
		t.Fatalf("expected terminal to be inactive")
	}
	if snapshot.State != "closing" {
		t.Fatalf("state = %q, want closing", snapshot.State)
	}
	if snapshot.RetentionSeconds != 300 {
		t.Fatalf("retention_seconds = %d, want 300", snapshot.RetentionSeconds)
	}
	if snapshot.ClosesAt == nil {
		t.Fatalf("expected closes_at")
	}
	if time.Until(*snapshot.ClosesAt) < 4*time.Minute {
		t.Fatalf("closes_at is too soon: %s", snapshot.ClosesAt.Format(time.RFC3339))
	}
}

func TestStoreMarksInactiveWhenEndEventUsesShorterStepOwner(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:review-plan",
		"⏺ Review complete\n\n✻ Cogitated for 4m 37s\n❯",
		12,
		map[string]interface{}{
			"tmux_session":    "mlp-claude-code-exp-123",
			"current_step_id": "review-plan",
			"execution_kind":  "workflow_step",
			"scope":           "workflow_step",
		},
		time.Now(),
	))
	store.HandleEvent("session-1", terminalEndEvent("review-plan", map[string]interface{}{
		"tmux_session":               "mlp-claude-code-exp-123",
		"current_step_id":            "review-plan",
		"terminal_retention_seconds": 300,
	}))

	snapshot, ok := store.Get("session-1:workflow-step:review-plan")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Active {
		t.Fatalf("expected terminal to be inactive")
	}
	if snapshot.State != "closing" {
		t.Fatalf("state = %q, want closing", snapshot.State)
	}
	if snapshot.RetentionSeconds != 300 {
		t.Fatalf("retention_seconds = %d, want 300", snapshot.RetentionSeconds)
	}
}

func TestStorePrunesExpiredClosingTerminalsOnListAndGet(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "screen", 1))
	store.HandleEvent("session-1", terminalEndEvent("exec-1", map[string]interface{}{"terminal_retention_seconds": 300}))

	terminalID := "session-1:exec-1"
	expiredAt := time.Now().Add(-time.Second)
	store.mu.Lock()
	snapshot := store.byID[terminalID]
	snapshot.ClosesAt = &expiredAt
	store.byID[terminalID] = snapshot
	store.mu.Unlock()

	if _, ok := store.Get(terminalID); ok {
		t.Fatalf("expired closing terminal should be pruned on Get")
	}
	if snapshots := store.List("session-1"); len(snapshots) != 0 {
		t.Fatalf("expired closing terminal should be pruned on List, got %d", len(snapshots))
	}
}

func TestStoreDetectsFailedTerminalState(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "LLM Generation Error\nSTATUS: FAILED", 1))
	store.HandleEvent("session-1", terminalEndEvent("exec-1", nil))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.State != "failed" {
		t.Fatalf("state = %q, want failed", snapshot.State)
	}
}

func TestStoreKeepsActiveTerminalRunningWhenScreenContainsFailureText(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "Checking if the browser failed to start...\n• Working (2m • esc to interrupt)", 1))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreDoesNotImmediatelyCompleteBoundedTerminalFromProviderIdlePromptWithoutEndEvent(t *testing.T) {
	store := NewStore()
	screen := `╭─────────────────────────────────────────────────────────╮
│ >_ OpenAI Codex                                        │
╰─────────────────────────────────────────────────────────╯

• STATUS: COMPLETED

─ Worked for 7m 13s ──────────────────────────────────────

› Use /skills to list available skills`
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:exec-step-check-reddit-1:step-check-reddit",
		screen,
		344,
		map[string]interface{}{
			"execution_kind":  "workflow_step",
			"tmux_session":    "mlp-codex-cli-int-test",
			"provider":        "codex-cli",
			"current_step_id": "step-check-reddit",
		},
		time.Now(),
	))

	snapshot, ok := store.Get("session-1:workflow-step:exec-step-check-reddit-1:step-check-reddit")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("bounded completed-looking pane should stay active until streaming_end or stable fallback")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreDoesNotImmediatelyCompleteCodexWorkflowTerminalWithDraftPromptAfterCompletion(t *testing.T) {
	store := NewStore()
	screen := `╭─────────────────────────────────────────────────────────╮
│ >_ OpenAI Codex                                        │
╰─────────────────────────────────────────────────────────╯

• STATUS: COMPLETED

─ Worked for 10m 10s ─────────────────────────────────────

› Write tests for @filename

  gpt-5.4 xhigh · ~/ai-work/workspace-docs/Workflow/substack`
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:exec-step-check-x-1:step-check-x",
		screen,
		492,
		map[string]interface{}{
			"execution_kind":  "workflow_step",
			"tmux_session":    "mlp-codex-cli-int-test",
			"provider":        "codex-cli",
			"current_step_id": "step-check-x",
		},
		time.Now(),
	))

	snapshot, ok := store.Get("session-1:workflow-step:exec-step-check-x-1:step-check-x")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("completed-looking Codex workflow pane should stay active until streaming_end or stable fallback")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreDoesNotImmediatelyCompleteCodexWorkflowTerminalFromWorkedForMarker(t *testing.T) {
	store := NewStore()
	screen := `╭─────────────────────────────────────────────────────────╮
│ >_ OpenAI Codex                                        │
╰─────────────────────────────────────────────────────────╯

─ Worked for 9m 23s ──────────────────────────────────────

› Write tests for @filename

  gpt-5.4 xhigh · ~/ai-work/workspace-docs/Workflow/substack`
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:exec-step-check-x-1:step-check-x",
		screen,
		492,
		map[string]interface{}{
			"execution_kind":  "workflow_step",
			"tmux_session":    "mlp-codex-cli-int-test",
			"provider":        "codex-cli",
			"current_step_id": "step-check-x",
		},
		time.Now(),
	))

	snapshot, ok := store.Get("session-1:workflow-step:exec-step-check-x-1:step-check-x")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("Codex workflow pane with worked-for marker should stay active until streaming_end or stable fallback")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreDoesNotImmediatelySelfCompleteBoundedTerminalFromPromptStatusAlone(t *testing.T) {
	store := NewStore()
	screen := `• STATUS: COMPLETED

The provider has not yet returned to an idle prompt.`
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:exec-step-check-reddit-1:step-check-reddit",
		screen,
		344,
		map[string]interface{}{
			"execution_kind":  "workflow_step",
			"tmux_session":    "mlp-codex-cli-int-test",
			"provider":        "codex-cli",
			"current_step_id": "step-check-reddit",
		},
		time.Now(),
	))

	snapshot, ok := store.Get("session-1:workflow-step:exec-step-check-reddit-1:step-check-reddit")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("fresh workflow prompt status alone should not close the terminal")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreDoesNotCompleteLiveTmuxTerminalFromStalePromptStatus(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:workflow-step:exec-step-check-reddit-1:step-check-reddit"
	oldUpdate := time.Now().Add(-time.Hour)
	store.mu.Lock()
	store.byID[terminalID] = Snapshot{
		TerminalID:    terminalID,
		SessionID:     "session-1",
		OwnerID:       "workflow-step:exec-step-check-reddit-1:step-check-reddit",
		ExecutionID:   "workflow-step:exec-step-check-reddit-1:step-check-reddit",
		ExecutionKind: "workflow_step",
		Scope:         "workflow_step",
		TmuxSession:   "mlp-codex-cli-int-test",
		Content:       "• STATUS: COMPLETED\n\nThe provider did not show a recognizable idle prompt.",
		Active:        true,
		State:         "running",
		CreatedAt:     oldUpdate,
		UpdatedAt:     oldUpdate,
	}
	store.bySession["session-1"] = map[string]struct{}{terminalID: {}}
	store.mu.Unlock()

	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("elapsed time alone should not close a live tmux terminal")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreDoesNotUsePromptStatusAsImmediateIdlePromptFallback(t *testing.T) {
	store := NewStore()
	screen := `╭─────────────────────────────────────────────────────────╮
│ >_ OpenAI Codex                                        │
╰─────────────────────────────────────────────────────────╯

• STATUS: COMPLETED

› Use /skills to list available skills`
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:exec-step-check-reddit-1:step-check-reddit",
		screen,
		344,
		map[string]interface{}{
			"execution_kind":  "workflow_step",
			"tmux_session":    "mlp-codex-cli-int-test",
			"provider":        "codex-cli",
			"current_step_id": "step-check-reddit",
		},
		time.Now(),
	))

	snapshot, ok := store.Get("session-1:workflow-step:exec-step-check-reddit-1:step-check-reddit")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("prompt status with idle prompt should stay active until stable fallback")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreReconcilesExistingIdleBoundedTerminalOnRead(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:workflow-step:exec-step-check-reddit-1:step-check-reddit"
	screen := `╭─────────────────────────────────────────────────────────╮
│ >_ OpenAI Codex                                        │
╰─────────────────────────────────────────────────────────╯

─ Worked for 7m 13s ──────────────────────────────────────

› Use /skills to list available skills`
	store.mu.Lock()
	store.byID[terminalID] = Snapshot{
		TerminalID:    terminalID,
		SessionID:     "session-1",
		OwnerID:       "workflow-step:exec-step-check-reddit-1:step-check-reddit",
		ExecutionID:   "workflow-step:exec-step-check-reddit-1:step-check-reddit",
		ExecutionKind: "workflow_step",
		Scope:         "workflow_step",
		TmuxSession:   "mlp-codex-cli-int-test",
		Content:       screen,
		Active:        true,
		State:         "running",
		UpdatedAt:     time.Now().Add(-time.Minute),
	}
	store.bySession["session-1"] = map[string]struct{}{terminalID: {}}
	store.mu.Unlock()

	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("Get should not immediately reconcile an idle-looking bounded terminal to inactive")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}

	listed := store.List("session-1")
	if len(listed) != 1 {
		t.Fatalf("List returned %d snapshots, want 1", len(listed))
	}
	if !listed[0].Active || listed[0].State != "running" {
		t.Fatalf("List snapshot active/state = %v/%q, want true/running", listed[0].Active, listed[0].State)
	}
}

func TestStoreDoesNotImmediatelySelfCompleteMainAgentFromIdlePromptStatus(t *testing.T) {
	store := NewStore()
	screen := `Claude Code v2.1.144

• STATUS: COMPLETED

❯`
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		screen,
		12,
		map[string]interface{}{
			"execution_kind": "main_agent",
			"tmux_session":   "mlp-claude-main-test",
			"provider":       "claude-code",
		},
		time.Now(),
	))

	snapshot, ok := store.Get("session-1:main:session-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("main-agent terminal should stay active until streaming_end or stable fallback")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestRebindLiveTmuxReplacesClosedMainPane(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		"retained old screen",
		1,
		map[string]interface{}{
			"execution_kind": "main_agent",
			"tmux_session":   "mlp-claude-code-old",
		},
		time.Now(),
	))
	store.MarkProcessClosed("session-1:main:session-1", "old pane exited during retry")

	rebound, ok := store.RebindLiveTmux("session-1:main:session-1", "mlp-claude-code-new")
	if !ok {
		t.Fatal("expected terminal to be rebound")
	}
	if rebound.TmuxSession != "mlp-claude-code-new" || !rebound.Active || rebound.ProcessState != "live" {
		t.Fatalf("rebound terminal = tmux %q active=%v process=%q", rebound.TmuxSession, rebound.Active, rebound.ProcessState)
	}
	if rebound.Content != "retained old screen" {
		t.Fatalf("content = %q, want retained screen until fresh capture", rebound.Content)
	}
}

func TestStoreCanonicalizesCurrentMainAgentOwner(t *testing.T) {
	store := NewStore()
	now := time.Now()

	store.HandleEvent("session-1", terminalEventWithMetadata(
		"exec-main-a",
		"first main pane",
		1,
		map[string]interface{}{
			"execution_kind": "main_agent",
			"tmux_session":   "mlp-main-a",
		},
		now,
	))
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"exec-main-b",
		"second main pane",
		1,
		map[string]interface{}{
			"execution_kind": "main_agent",
			"tmux_session":   "mlp-main-b",
		},
		now.Add(time.Second),
	))

	if _, ok := store.Get("session-1:exec-main-a"); ok {
		t.Fatalf("main-agent alias exec-main-a should not be addressable as a terminal")
	}
	if _, ok := store.Get("session-1:exec-main-b"); ok {
		t.Fatalf("main-agent alias exec-main-b should not be addressable as a terminal")
	}
	snapshot, ok := store.Get("session-1:main:session-1")
	if !ok {
		t.Fatalf("expected canonical main-agent terminal")
	}
	if snapshot.OwnerID != "main:session-1" {
		t.Fatalf("owner id = %q, want main:session-1", snapshot.OwnerID)
	}
	if snapshot.Content != "second main pane" {
		t.Fatalf("content = %q, want latest main pane", snapshot.Content)
	}
	snapshots := store.List("session-1")
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want one canonical main agent: %#v", len(snapshots), snapshots)
	}
}

func TestStoreChildReviewerCannotReplaceMainAgentTmux(t *testing.T) {
	store := NewStore()
	now := time.Now()
	sessionID := "schedule-cron--pulse"
	childOwnerID := "pulse-reviewer-cost-123"

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		"main:"+sessionID,
		"parent pulse screen",
		1,
		map[string]interface{}{
			"execution_kind": "main_agent",
			"tmux_session":   "mlp-parent",
		},
		now,
	))
	store.HandleEvent(sessionID, terminalEventWithMetadata(
		"main:"+sessionID,
		"child reviewer screen",
		1,
		map[string]interface{}{
			"execution_owner_id":  childOwnerID,
			"background_agent_id": childOwnerID,
			"execution_kind":      "background_agent",
			"scope":               "background_agent",
			"tmux_session":        "mlp-reviewer",
		},
		now.Add(time.Second),
	))

	parent, ok := store.Get(sessionID + ":main:" + sessionID)
	if !ok {
		t.Fatalf("expected canonical parent terminal")
	}
	if parent.TmuxSession != "mlp-parent" || parent.Content != "parent pulse screen" {
		t.Fatalf("child replaced parent terminal: tmux=%q content=%q", parent.TmuxSession, parent.Content)
	}

	child, ok := store.Get(sessionID + ":" + childOwnerID)
	if !ok {
		t.Fatalf("expected separate child reviewer terminal")
	}
	if child.TmuxSession != "mlp-reviewer" || child.ExecutionKind != "background_agent" {
		t.Fatalf("child terminal = tmux %q kind %q", child.TmuxSession, child.ExecutionKind)
	}
}

func TestStoreInheritedMainKindCannotReplaceMainAgentTmux(t *testing.T) {
	store := NewStore()
	now := time.Now()
	sessionID := "schedule-cron--pulse"
	childOwnerID := "pulse-reviewer-learning-456"

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		"main:"+sessionID,
		"parent pulse screen",
		1,
		map[string]interface{}{
			"execution_kind": "main_agent",
			"tmux_session":   "mlp-parent",
		},
		now,
	))
	childEvent := terminalEventWithMetadata(
		childOwnerID,
		"child reviewer screen",
		1,
		map[string]interface{}{
			"execution_kind": "background_agent",
			"scope":          "background_agent",
			"tmux_session":   "mlp-reviewer",
		},
		now.Add(time.Second),
	)
	// Provider callbacks can inherit this parent field even though enriched
	// metadata correctly identifies the background child.
	childEvent.ExecutionKind = "main_agent"
	store.HandleEvent(sessionID, childEvent)

	parent, ok := store.Get(sessionID + ":main:" + sessionID)
	if !ok {
		t.Fatalf("expected canonical parent terminal")
	}
	if parent.TmuxSession != "mlp-parent" || parent.Content != "parent pulse screen" {
		t.Fatalf("mislabelled child replaced parent terminal: tmux=%q content=%q", parent.TmuxSession, parent.Content)
	}
	child, ok := store.Get(sessionID + ":" + childOwnerID)
	if !ok {
		t.Fatalf("expected separate child reviewer terminal")
	}
	if child.ExecutionKind != "background_agent" || child.Scope != "background_agent" {
		t.Fatalf("child identity = kind %q scope %q", child.ExecutionKind, child.Scope)
	}
}

func TestStoreListDedupesLegacyCurrentMainAgentAliases(t *testing.T) {
	store := NewStore()
	oldUpdate := time.Now().Add(-time.Minute)
	newUpdate := time.Now()
	canonicalID := "session-1:main:session-1"
	aliasID := "session-1:exec-main-legacy"
	store.byID[aliasID] = Snapshot{
		TerminalID:    aliasID,
		SessionID:     "session-1",
		OwnerID:       "exec-main-legacy",
		ExecutionKind: "main_agent",
		Content:       "old alias",
		Active:        true,
		State:         "running",
		UpdatedAt:     oldUpdate,
		CreatedAt:     oldUpdate,
	}
	store.byID[canonicalID] = Snapshot{
		TerminalID:    canonicalID,
		SessionID:     "session-1",
		OwnerID:       "main:session-1",
		ExecutionKind: "main_agent",
		Content:       "canonical",
		Active:        true,
		State:         "running",
		UpdatedAt:     newUpdate,
		CreatedAt:     newUpdate,
	}
	store.bySession["session-1"] = map[string]struct{}{
		aliasID:     {},
		canonicalID: {},
	}

	snapshots := store.List("session-1")
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want one current main agent: %#v", len(snapshots), snapshots)
	}
	if snapshots[0].TerminalID != canonicalID {
		t.Fatalf("terminal id = %q, want %s", snapshots[0].TerminalID, canonicalID)
	}
}

func TestStoreTerminalOwnerPrefersMetadataExecutionOwnerOverEventExecutionID(t *testing.T) {
	store := NewStore()
	sessionID := "session-1"
	stepOwnerID := "workflow-step:workflow-full-1:prepare-fixtures"

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		"main:"+sessionID,
		"$ vertex.generateContent model=gemini-3.1-flash-lite-preview msgs=2 tools=15\n> user: **DESCRIPTION**: Create test_fixtures.json",
		1,
		map[string]interface{}{
			"execution_owner_id": stepOwnerID,
			"execution_kind":     "workflow_step",
			"current_step_id":    "prepare-fixtures",
			"step_transport":     "api",
			"provider":           "vertex",
		},
		time.Now(),
	))

	if _, ok := store.Get(sessionID + ":main:" + sessionID); ok {
		t.Fatalf("workflow step terminal content was incorrectly routed to the main-agent terminal")
	}
	snapshot, ok := store.Get(sessionID + ":" + stepOwnerID)
	if !ok {
		t.Fatalf("expected workflow-step terminal snapshot")
	}
	if snapshot.OwnerID != stepOwnerID {
		t.Fatalf("owner id = %q, want %q", snapshot.OwnerID, stepOwnerID)
	}
	if snapshot.ExecutionKind != "workflow_step" {
		t.Fatalf("execution kind = %q, want workflow_step", snapshot.ExecutionKind)
	}
}

func TestStoreTerminalOwnerPrefersWorkflowStepExecutionOverWorkflowRunID(t *testing.T) {
	store := NewStore()
	sessionID := "session-1"
	workflowID := "workflow-full-1779513614303714000"
	stepOwnerID := "workflow-step:" + workflowID + ":prepare-test-fixtures"

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		stepOwnerID,
		"$ vertex.generateContent model=gemini-3.1-flash-lite-preview msgs=2 tools=15\n> user: create fixtures",
		1,
		map[string]interface{}{
			"execution_id":      workflowID,
			"execution_kind":    "workflow_step",
			"current_step_id":   "prepare-test-fixtures",
			"current_step_type": "api",
			"provider":          "vertex",
		},
		time.Now(),
	))

	if _, ok := store.Get(sessionID + ":" + workflowID); ok {
		t.Fatalf("workflow-step terminal was incorrectly owned by workflow run id")
	}
	snapshot, ok := store.Get(sessionID + ":" + stepOwnerID)
	if !ok {
		t.Fatalf("expected workflow-step terminal snapshot")
	}
	if snapshot.OwnerID != stepOwnerID {
		t.Fatalf("owner id = %q, want %q", snapshot.OwnerID, stepOwnerID)
	}
}

func TestStoreTerminalStepTypePrefersPlanStepType(t *testing.T) {
	store := NewStore()
	sessionID := "session-1"
	workflowID := "workflow-full-1779518447634380000"
	stepOwnerID := "workflow-step:" + workflowID + ":route-by-mode"

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		stepOwnerID,
		"$ codex exec route by mode",
		1,
		map[string]interface{}{
			"execution_kind":    "workflow_step",
			"current_step_id":   "route-by-mode",
			"current_step_type": "code_exec",
			"plan_step_type":    "routing",
			"provider":          "codex-cli",
			"tmux_session":      "mlp-codex-cli-int-test",
		},
		time.Now(),
	))

	snapshot, ok := store.Get(sessionID + ":" + stepOwnerID)
	if !ok {
		t.Fatalf("expected workflow-step terminal snapshot")
	}
	if snapshot.StepType != "routing" {
		t.Fatalf("step type = %q, want routing", snapshot.StepType)
	}
}

func TestStoreTerminalStepContextSurvivesMetadataSparseChunks(t *testing.T) {
	store := NewStore()
	sessionID := "session-1"
	workflowID := "workflow-full-1779518447634380000"
	stepOwnerID := "workflow-step:" + workflowID + ":route-by-mode"
	now := time.Now()

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		stepOwnerID,
		"$ codex exec route by mode",
		1,
		map[string]interface{}{
			"execution_kind":      "workflow_step",
			"current_step_id":     "route-by-mode",
			"step_name":           "Route by mode",
			"current_step_type":   "code_exec",
			"plan_step_type":      "routing",
			"step_index":          3,
			"step_total":          8,
			"step_execution_mode": "code_exec",
			"step_transport":      "tmux",
			"step_triggered_by":   "workflow_executor",
			"tmux_session":        "mlp-codex-cli-int-test",
		},
		now,
	))

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		stepOwnerID,
		"$ codex exec route by mode\nstreamed pane text",
		2,
		map[string]interface{}{
			"execution_kind": "workflow_step",
		},
		now.Add(time.Second),
	))

	snapshot, ok := store.Get(sessionID + ":" + stepOwnerID)
	if !ok {
		t.Fatalf("expected workflow-step terminal snapshot")
	}
	if snapshot.StepType != "routing" {
		t.Fatalf("step type = %q, want routing", snapshot.StepType)
	}
	if snapshot.StepName != "Route by mode" {
		t.Fatalf("step name = %q, want Route by mode", snapshot.StepName)
	}
	if snapshot.StepIndex != 3 || snapshot.StepTotal != 8 {
		t.Fatalf("step position = %d/%d, want 3/8", snapshot.StepIndex, snapshot.StepTotal)
	}
	if snapshot.StepTransport != "tmux" {
		t.Fatalf("step transport = %q, want tmux", snapshot.StepTransport)
	}
	if snapshot.StepTriggeredBy != "workflow_executor" {
		t.Fatalf("step triggered by = %q, want workflow_executor", snapshot.StepTriggeredBy)
	}
	if snapshot.TmuxSession != "mlp-codex-cli-int-test" {
		t.Fatalf("tmux session = %q, want mlp-codex-cli-int-test", snapshot.TmuxSession)
	}
}

func TestStoreTerminalOwnerPrefersWorkflowStepExecutionOverParentExecutionOwner(t *testing.T) {
	store := NewStore()
	sessionID := "session-1"
	workflowID := "workflow-full-1779518447634380000"
	stepOwnerID := "workflow-step:" + workflowID + ":step-critic"

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		stepOwnerID,
		"$ codex exec step critic",
		1,
		map[string]interface{}{
			"execution_owner_id": workflowID,
			"execution_kind":     "workflow_step",
			"current_step_id":    "step-critic",
			"current_step_type":  "code_exec",
			"provider":           "codex-cli",
			"tmux_session":       "mlp-codex-cli-int-test",
		},
		time.Now(),
	))

	if _, ok := store.Get(sessionID + ":" + workflowID); ok {
		t.Fatalf("workflow-step terminal was incorrectly owned by parent workflow execution")
	}
	snapshot, ok := store.Get(sessionID + ":" + stepOwnerID)
	if !ok {
		t.Fatalf("expected workflow-step terminal snapshot")
	}
	if snapshot.OwnerID != stepOwnerID {
		t.Fatalf("owner id = %q, want %q", snapshot.OwnerID, stepOwnerID)
	}
	if snapshot.StepID != "step-critic" {
		t.Fatalf("step id = %q, want step-critic", snapshot.StepID)
	}
}

func TestStoreTerminalOwnerSynthesizesWorkflowStepFromParentExecutionOwner(t *testing.T) {
	store := NewStore()
	sessionID := "session-1"
	workflowID := "workflow-full-1779518447634380000"
	stepOwnerID := "workflow-step:" + workflowID + ":step-critic"

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		"main:"+sessionID,
		"$ codex exec step critic",
		1,
		map[string]interface{}{
			"execution_owner_id": workflowID,
			"execution_kind":     "workflow_step",
			"current_step_id":    "step-critic",
			"current_step_type":  "code_exec",
			"provider":           "codex-cli",
			"tmux_session":       "mlp-codex-cli-int-test",
		},
		time.Now(),
	))

	if _, ok := store.Get(sessionID + ":" + workflowID); ok {
		t.Fatalf("workflow-step terminal was incorrectly owned by parent workflow execution")
	}
	if _, ok := store.Get(sessionID + ":main:" + sessionID); ok {
		t.Fatalf("workflow-step terminal was incorrectly routed to main terminal")
	}
	snapshot, ok := store.Get(sessionID + ":" + stepOwnerID)
	if !ok {
		t.Fatalf("expected synthesized workflow-step terminal snapshot")
	}
	if snapshot.OwnerID != stepOwnerID {
		t.Fatalf("owner id = %q, want %q", snapshot.OwnerID, stepOwnerID)
	}
}

func TestStoreTerminalOwnerSynthesizesWorkflowStepWhenOnlyWorkflowRunIDIsPresent(t *testing.T) {
	store := NewStore()
	sessionID := "session-1"
	workflowID := "workflow-full-1779513614303714000"
	stepOwnerID := "workflow-step:" + workflowID + ":prepare-test-fixtures"

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		workflowID,
		"$ vertex.generateContent model=gemini-3.1-flash-lite-preview msgs=2 tools=15\n> user: create fixtures",
		1,
		map[string]interface{}{
			"execution_id":    workflowID,
			"execution_kind":  "workflow_step",
			"current_step_id": "prepare-test-fixtures",
			"provider":        "vertex",
		},
		time.Now(),
	))

	if _, ok := store.Get(sessionID + ":" + workflowID); ok {
		t.Fatalf("workflow-step terminal was incorrectly owned by workflow run id")
	}
	snapshot, ok := store.Get(sessionID + ":" + stepOwnerID)
	if !ok {
		t.Fatalf("expected synthesized workflow-step terminal snapshot")
	}
	if snapshot.OwnerID != stepOwnerID {
		t.Fatalf("owner id = %q, want %q", snapshot.OwnerID, stepOwnerID)
	}
}

func TestStorePreservesSyntheticTerminalRowsFromMetadata(t *testing.T) {
	store := NewStore()
	rows := []interface{}{
		map[string]interface{}{"kind": "banner", "text": "gemini model=auto msgs=1"},
		map[string]interface{}{"kind": "user", "text": "[AUTO-NOTIFICATION] Background agent started.\nAck briefly; do not call tools."},
		map[string]interface{}{"kind": "asst", "text": "Acknowledged. I will wait for completion."},
	}

	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		"$ gemini model=auto msgs=1\n> user: [AUTO-NOTIFICATION] Background agent started.\n  Ack briefly; do not call tools.\n  Acknowledged. I will wait for completion.",
		1,
		map[string]interface{}{
			"execution_kind": "main_agent",
			"step_transport": "api",
			"provider":       "codex-cli",
			"rows":           rows,
		},
		time.Now(),
	))

	snapshot, ok := store.Get("session-1:main:session-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if len(snapshot.Rows) != 3 {
		t.Fatalf("rows len = %d, want 3: %#v", len(snapshot.Rows), snapshot.Rows)
	}
	if snapshot.Rows[1].Kind != "user" {
		t.Fatalf("row[1].Kind = %q, want user", snapshot.Rows[1].Kind)
	}
	if snapshot.Rows[2].Kind != "asst" {
		t.Fatalf("row[2].Kind = %q, want asst", snapshot.Rows[2].Kind)
	}
	if snapshot.Rows[2].Text != "Acknowledged. I will wait for completion." {
		t.Fatalf("assistant row text = %q", snapshot.Rows[2].Text)
	}
}

func TestStoreKeepsUnchangedTmuxTerminalActiveUntilLivenessIsKnown(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:workflow-step:exec-step-old:step-old"
	oldUpdate := time.Now().Add(-time.Hour)
	store.mu.Lock()
	store.byID[terminalID] = Snapshot{
		TerminalID:    terminalID,
		SessionID:     "session-1",
		OwnerID:       "workflow-step:exec-step-old:step-old",
		ExecutionID:   "workflow-step:exec-step-old:step-old",
		ExecutionKind: "workflow_step",
		Scope:         "workflow_step",
		TmuxSession:   "mlp-codex-cli-int-test",
		Content:       "Working...\nNo provider idle prompt was ever observed.",
		Active:        true,
		State:         "running",
		CreatedAt:     oldUpdate,
		UpdatedAt:     oldUpdate,
	}
	store.bySession["session-1"] = map[string]struct{}{terminalID: {}}
	store.mu.Unlock()

	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("unchanged tmux terminal should remain active until the server checks pane liveness")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
	if !snapshot.UpdatedAt.Equal(oldUpdate) {
		t.Fatalf("inactive reconciliation should preserve last update time")
	}
}

func TestStoreDoesNotIdleTimeoutNonTmuxWorkflowTerminal(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:workflow-step:exec-step-old:step-old"
	oldUpdate := time.Now().Add(-time.Hour)
	store.mu.Lock()
	store.byID[terminalID] = Snapshot{
		TerminalID:    terminalID,
		SessionID:     "session-1",
		OwnerID:       "workflow-step:exec-step-old:step-old",
		ExecutionID:   "workflow-step:exec-step-old:step-old",
		ExecutionKind: "workflow_step",
		Scope:         "workflow_step",
		StepTransport: "api",
		Content:       "Working...\nNo provider idle prompt was ever observed.",
		Active:        true,
		State:         "running",
		CreatedAt:     oldUpdate,
		UpdatedAt:     oldUpdate,
	}
	store.bySession["session-1"] = map[string]struct{}{terminalID: {}}
	store.mu.Unlock()

	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("non-tmux terminal should not be completed by idle timeout")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreDoesNotMarkRecentBoundedTerminalInactive(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:workflow-step:exec-step-recent:step-recent"
	recentUpdate := time.Now().Add(-time.Minute)
	store.mu.Lock()
	store.byID[terminalID] = Snapshot{
		TerminalID:    terminalID,
		SessionID:     "session-1",
		OwnerID:       "workflow-step:exec-step-recent:step-recent",
		ExecutionID:   "workflow-step:exec-step-recent:step-recent",
		ExecutionKind: "workflow_step",
		Scope:         "workflow_step",
		TmuxSession:   "mlp-codex-cli-int-test",
		Content:       "Working...\nNo provider idle prompt was ever observed.",
		Active:        true,
		State:         "running",
		CreatedAt:     recentUpdate,
		UpdatedAt:     recentUpdate,
	}
	store.bySession["session-1"] = map[string]struct{}{terminalID: {}}
	store.mu.Unlock()

	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("recent bounded terminal should stay active")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreKeepsUnchangedMainAgentTerminalActive(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:main:session-1"
	oldUpdate := time.Now().Add(-time.Hour)
	store.mu.Lock()
	store.byID[terminalID] = Snapshot{
		TerminalID:    terminalID,
		SessionID:     "session-1",
		OwnerID:       "main:session-1",
		ExecutionID:   "main:session-1",
		ExecutionKind: "main_agent",
		Scope:         "main_agent",
		TmuxSession:   "mlp-main-agent",
		Content:       "Ready for the next user turn.",
		Active:        true,
		State:         "running",
		CreatedAt:     oldUpdate,
		UpdatedAt:     oldUpdate,
	}
	store.bySession["session-1"] = map[string]struct{}{terminalID: {}}
	store.mu.Unlock()

	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("main-agent terminal should remain active while its real tmux pane is live")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreIdenticalTerminalChunksDoNotRefreshUpdatedAt(t *testing.T) {
	store := NewStore()
	now := time.Now()
	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "same pane", 10, now))
	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "same pane", 11, now.Add(time.Minute)))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.UpdatedAt.Equal(now) {
		t.Fatalf("unchanged pane should preserve updated_at = %s, got %s", now, snapshot.UpdatedAt)
	}
}

func TestStoreRefreshContentDoesNotRefreshUpdatedAtWhenPaneUnchanged(t *testing.T) {
	store := NewStore()
	oldUpdate := time.Now().Add(-time.Hour)
	metadata := map[string]interface{}{"execution_kind": "workflow_step", "scope": "workflow_step", "tmux_session": "mlp-codex-cli-int-test"}
	store.HandleEvent("session-1", terminalEventWithMetadata("exec-1", "same pane", 10, metadata, oldUpdate))

	refreshed, ok := store.RefreshContent("session-1:exec-1", "same pane")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !refreshed.UpdatedAt.Equal(oldUpdate) {
		t.Fatalf("unchanged refresh should preserve updated_at = %s, got %s", oldUpdate, refreshed.UpdatedAt)
	}

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("unchanged refreshed pane should stay active until tmux liveness is checked")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreStoresLatestShortTmuxScreenSnapshot(t *testing.T) {
	store := NewStore()
	metadata := map[string]interface{}{"execution_kind": "main_agent", "scope": "main", "tmux_session": "mlp-claude-main-test"}
	store.HandleEvent("session-1", terminalEventWithMetadata("main:session-1", "old answer\nshared prompt", 10, metadata, time.Now()))
	store.HandleEvent("session-1", terminalEventWithMetadata("main:session-1", "shared prompt\nnew answer", 11, metadata, time.Now().Add(time.Second)))

	snapshot, ok := store.Get("session-1:main:session-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	// Snapshot accumulation was removed: the store keeps the latest capture only.
	want := "shared prompt\nnew answer"
	if snapshot.Content != want {
		t.Fatalf("content = %q, want latest snapshot %q", snapshot.Content, want)
	}
}

func TestStoreReplacesMostlySameTmuxScreenRedraw(t *testing.T) {
	store := NewStore()
	metadata := map[string]interface{}{"execution_kind": "main_agent", "scope": "main", "tmux_session": "mlp-claude-main-test"}
	oldScreen := "╭ Claude Code\n│ Editing files\n│ Running checks\n│ Press esc to interrupt\n╰ Ready"
	newScreen := "╭ Claude Code\n│ Editing files\n│ Thinking...\n│ Press esc to interrupt\n╰ Ready"
	store.HandleEvent("session-1", terminalEventWithMetadata("main:session-1", oldScreen, 10, metadata, time.Now()))
	store.HandleEvent("session-1", terminalEventWithMetadata("main:session-1", newScreen, 11, metadata, time.Now().Add(time.Second)))

	snapshot, ok := store.Get("session-1:main:session-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Content != newScreen {
		t.Fatalf("content = %q, want latest redraw %q", snapshot.Content, newScreen)
	}
	if strings.Contains(snapshot.Content, oldScreen+"\n"+newScreen) {
		t.Fatalf("mostly identical redraws should not be appended:\n%s", snapshot.Content)
	}
}

func TestStoreDoesNotAccumulateShortNonTmuxSnapshots(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "old screen", 10))
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "new screen", 11))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Content != "new screen" {
		t.Fatalf("content = %q, want latest non-tmux screen", snapshot.Content)
	}
}

func TestStoreRefreshContentReplacesTmuxScrollback(t *testing.T) {
	store := NewStore()
	metadata := map[string]interface{}{"execution_kind": "workflow_step", "scope": "workflow_step", "tmux_session": "mlp-codex-cli-int-test"}
	store.HandleEvent("session-1", terminalEventWithMetadata("exec-1", "old output\ncurrent prompt", 10, metadata, time.Now()))

	refreshed, ok := store.RefreshContent("session-1:exec-1", "current prompt\nfresh output")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	// Snapshot accumulation was removed: RefreshContent stores the latest capture.
	want := "current prompt\nfresh output"
	if refreshed.Content != want {
		t.Fatalf("content = %q, want replacement %q", refreshed.Content, want)
	}
}

func TestStoreReplaceContentDoesNotPreserveTmuxScrollback(t *testing.T) {
	store := NewStore()
	metadata := map[string]interface{}{"execution_kind": "workflow_step", "scope": "workflow_step", "tmux_session": "mlp-codex-cli-int-test"}
	store.HandleEvent("session-1", terminalEventWithMetadata("exec-1", "old output", 10, metadata, time.Now()))

	refreshed, ok := store.ReplaceContent("session-1:exec-1", "fresh authoritative capture")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if refreshed.Content != "fresh authoritative capture" {
		t.Fatalf("content = %q, want replacement", refreshed.Content)
	}
}

func TestStoreRefreshContentCompletesCapturedIdleWorkflowStep(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:workflow-step:exec-step-check-reddit-1:step-check-reddit"
	busyScreen := `╭─────────────────────────────────────────────────────────╮
│ >_ OpenAI Codex                                        │
╰─────────────────────────────────────────────────────────╯

⠙ Generating...`
	idleScreen := `╭─────────────────────────────────────────────────────────╮
│ >_ OpenAI Codex                                        │
╰─────────────────────────────────────────────────────────╯

─ Worked for 7m 13s ──────────────────────────────────────

› Use /skills to list available skills`

	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:exec-step-check-reddit-1:step-check-reddit",
		busyScreen,
		10,
		map[string]interface{}{
			"execution_kind":  "workflow_step",
			"scope":           "workflow_step",
			"tmux_session":    "mlp-codex-cli-int-test",
			"provider":        "codex-cli",
			"current_step_id": "step-check-reddit",
		},
		time.Now(),
	))

	refreshed, ok := store.RefreshContent(terminalID, idleScreen)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if refreshed.Active {
		t.Fatalf("captured idle workflow-step pane should become inactive")
	}
	if refreshed.State != "completed" {
		t.Fatalf("state = %q, want completed", refreshed.State)
	}
}

func TestStoreRefreshContentDoesNotCompleteCapturedIdleMainAgent(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:main:session-1"
	idleScreen := `╭─────────────────────────────────────────────────────────╮
│ >_ OpenAI Codex                                        │
╰─────────────────────────────────────────────────────────╯

› Use /skills to list available skills`

	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		"⠙ Generating...",
		10,
		map[string]interface{}{
			"execution_kind": "main_agent",
			"tmux_session":   "mlp-codex-cli-main-test",
			"provider":       "codex-cli",
		},
		time.Now(),
	))

	refreshed, ok := store.RefreshContent(terminalID, idleScreen)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !refreshed.Active {
		t.Fatalf("captured idle main-agent pane should remain active")
	}
	if refreshed.State != "running" {
		t.Fatalf("state = %q, want running", refreshed.State)
	}
}

func TestStoreRepeatedIdenticalChunksRemainActiveWithoutLifecycleEvidence(t *testing.T) {
	store := NewStore()
	oldUpdate := time.Now().Add(-time.Hour)
	now := time.Now()
	metadata := map[string]interface{}{"execution_kind": "workflow_step", "scope": "workflow_step", "tmux_session": "mlp-codex-cli-int-test"}
	store.HandleEvent("session-1", terminalEventWithMetadata("exec-1", "same pane", 10, metadata, oldUpdate))
	store.HandleEvent("session-1", terminalEventWithMetadata("exec-1", "same pane", 40, metadata, now))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("repeated unchanged pane should stay active without real completion evidence")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreKeepsTerminalRunningWhenEndArrivesDuringBusyPane(t *testing.T) {
	store := NewStore()
	screen := `╭──────────────────────────────────────────────────────────────────────────╮
│ ✓ agent_browser (api-bridge MCP Server) {"action":"wait","timeout":10} │
╰──────────────────────────────────────────────────────────────────────────╯
ERROR: Custom tool execution failed: command timed out after 2m0s

⊷ agent_browser (api-bridge MCP Server) {"action":"wait","timeout":10}

⠇ Thinking... (esc to cancel, 14m 30s)
──────────────────────────────────────────────────────────────────────────────
 >   Type your message or @path/to/file
workspace (/directory)                         sandbox                  /model
/tmp/project                                   no sandbox       Auto (Gemini 3)`
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", screen, 1))
	store.HandleEvent("session-1", terminalEndEvent("exec-1", nil))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active {
		t.Fatalf("terminal should remain active while pane is still thinking")
	}
	if snapshot.State != "running" {
		t.Fatalf("state = %q, want running", snapshot.State)
	}
}

func TestStoreLaterCompletionOverridesRecoveredFailureText(t *testing.T) {
	store := NewStore()
	screen := "PRE-VALIDATION FAILED on retry 1\nRecovered and wrote cdp_status.json\nSTATUS: COMPLETED"
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", screen, 1))
	store.HandleEvent("session-1", terminalEndEvent("exec-1", nil))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.State != "completed" {
		t.Fatalf("state = %q, want completed", snapshot.State)
	}
}

func TestStoreLaterFailureOverridesEarlierCompletionText(t *testing.T) {
	store := NewStore()
	screen := "STATUS: COMPLETED\nStarted post-validation\nPRE-VALIDATION FAILED on retry 2"
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", screen, 1))
	store.HandleEvent("session-1", terminalEndEvent("exec-1", nil))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.State != "failed" {
		t.Fatalf("state = %q, want failed", snapshot.State)
	}
}

func TestStoreDismissHidesTerminalButRetainsOwnershipAndFutureChunks(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "screen one", 1))

	if !store.Dismiss("session-1:exec-1") {
		t.Fatalf("expected dismiss to hide terminal")
	}
	if _, ok := store.Get("session-1:exec-1"); ok {
		t.Fatalf("terminal should be hidden after dismiss")
	}
	if raw, ok := store.GetRaw("session-1:exec-1"); !ok || raw.Content != "screen one" {
		t.Fatalf("dismissal lost ownership snapshot: ok=%v content=%q", ok, raw.Content)
	}

	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "screen two", 2))
	if _, ok := store.Get("session-1:exec-1"); ok {
		t.Fatalf("dismissed terminal should stay hidden")
	}
	if raw, ok := store.GetRaw("session-1:exec-1"); !ok || raw.Content != "screen two" {
		t.Fatalf("future chunk did not update hidden ownership snapshot: ok=%v content=%q", ok, raw.Content)
	}
}

func TestStoreMarkCompletedOperatorOverride(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "still working", 12))

	snapshot, ok := store.MarkCompleted("session-1:exec-1")
	if !ok {
		t.Fatalf("expected mark completed to find terminal")
	}
	if snapshot.Active {
		t.Fatalf("manually completed terminal should be inactive")
	}
	if snapshot.State != "completed" {
		t.Fatalf("state = %q, want completed", snapshot.State)
	}
	if snapshot.ClosesAt != nil || snapshot.RetentionSeconds != 0 {
		t.Fatalf("manual completion should clear retention, closes_at=%v retention=%d", snapshot.ClosesAt, snapshot.RetentionSeconds)
	}
}

func TestStoreSyntheticTurnLifecycleReactivatesRetainedMainTerminal(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:main:session-1"
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		"previous turn",
		7,
		map[string]interface{}{
			"tmux_session":   "mlp-claude-code-retained",
			"execution_kind": "main_agent",
		},
		time.Now(),
	))

	if _, ok := store.MarkCompleted(terminalID); !ok {
		t.Fatalf("expected retained main terminal %q", terminalID)
	}
	before, _ := store.Get(terminalID)

	running, ok := store.MarkTurnRunning(terminalID)
	if !ok {
		t.Fatalf("expected synthetic turn to reactivate retained main terminal")
	}
	if !running.Active || running.State != "running" {
		t.Fatalf("running turn lifecycle = active %v state %q", running.Active, running.State)
	}
	if running.ProcessState != "live" || running.SnapshotKind != "live" {
		t.Fatalf("retained tmux lifecycle = process %q snapshot %q", running.ProcessState, running.SnapshotKind)
	}
	if running.ChunkIndex <= before.ChunkIndex {
		t.Fatalf("running transition did not advance revision: before=%d after=%d", before.ChunkIndex, running.ChunkIndex)
	}

	completed, ok := store.MarkTurnCompleted(terminalID)
	if !ok {
		t.Fatalf("expected synthetic turn to settle retained main terminal")
	}
	if completed.Active || completed.State != "completed" {
		t.Fatalf("completed turn lifecycle = active %v state %q", completed.Active, completed.State)
	}
	if completed.ProcessState != "live" || completed.SnapshotKind != "live" {
		t.Fatalf("settled retained tmux must remain live, process=%q snapshot=%q", completed.ProcessState, completed.SnapshotKind)
	}
	if !store.SessionHasRetainedCodingTmux("session-1") {
		t.Fatal("settled turn with a live tmux must remain available for follow-up")
	}
	if completed.ChunkIndex <= running.ChunkIndex {
		t.Fatalf("completed transition did not advance revision: running=%d completed=%d", running.ChunkIndex, completed.ChunkIndex)
	}
}

func TestStoreMarkCompletedSuppressesSameTurnChunks(t *testing.T) {
	store := NewStore()
	now := time.Now()
	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "still working", 12, now))

	if _, ok := store.MarkCompleted("session-1:exec-1"); !ok {
		t.Fatalf("expected mark completed to find terminal")
	}
	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "same turn keeps scraping", 13, now.Add(time.Second)))

	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Active || snapshot.State != "completed" {
		t.Fatalf("same-turn chunk revived manual completion: active=%v state=%q", snapshot.Active, snapshot.State)
	}
	if snapshot.Content != "still working" {
		t.Fatalf("same-turn chunk should be ignored after manual completion, content=%q", snapshot.Content)
	}
}

func TestStoreMarkCompletedAllowsFreshTurn(t *testing.T) {
	store := NewStore()
	terminalID := "session-1:exec-1"
	now := time.Now()
	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "old turn", 12, now))

	if _, ok := store.MarkCompleted(terminalID); !ok {
		t.Fatalf("expected mark completed to find terminal")
	}
	store.mu.Lock()
	past := now.Add(-time.Second)
	store.forcedInactive[terminalID] = past
	snapshot := store.byID[terminalID]
	snapshot.UpdatedAt = past
	store.byID[terminalID] = snapshot
	store.mu.Unlock()

	store.HandleEvent("session-1", terminalEventAt("streaming_chunk", "exec-1", "fresh turn", 1, now.Add(time.Second)))

	snapshot, ok := store.Get(terminalID)
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if !snapshot.Active || snapshot.State != "running" {
		t.Fatalf("fresh turn should revive terminal, active=%v state=%q", snapshot.Active, snapshot.State)
	}
	if snapshot.Content != "fresh turn" {
		t.Fatalf("fresh turn content = %q", snapshot.Content)
	}
}

func TestStoreMarkFailedOperatorOverride(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "still working", 12))

	snapshot, ok := store.MarkFailed("session-1:exec-1")
	if !ok {
		t.Fatalf("expected mark failed to find terminal")
	}
	if snapshot.Active {
		t.Fatalf("manually failed terminal should be inactive")
	}
	if snapshot.State != "failed" {
		t.Fatalf("state = %q, want failed", snapshot.State)
	}

	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "same turn keeps scraping", 13))
	snapshot, ok = store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected terminal snapshot")
	}
	if snapshot.Active || snapshot.State != "failed" {
		t.Fatalf("same-turn chunk revived manual failure: active=%v state=%q", snapshot.Active, snapshot.State)
	}
}

func TestStoreRefreshContentUpdatesPaneTextWithoutRevivingForcedInactive(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEvent("streaming_chunk", "exec-1", "old content", 12))

	if _, ok := store.MarkCompleted("session-1:exec-1"); !ok {
		t.Fatalf("expected mark completed to find terminal")
	}
	snapshot, ok := store.RefreshContent("session-1:exec-1", "freshly captured pane")
	if !ok {
		t.Fatalf("expected refresh to find terminal")
	}
	if snapshot.Content != "freshly captured pane" {
		t.Fatalf("content = %q, want refreshed pane", snapshot.Content)
	}
	if snapshot.Active || snapshot.State != "completed" {
		t.Fatalf("manual completion should stay inactive after refresh, active=%v state=%q", snapshot.Active, snapshot.State)
	}
}

func TestStoreIgnoresNonTerminalStreamingChunks(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", storeevents.Event{
		Type:        "streaming_chunk",
		SessionID:   "session-1",
		ExecutionID: "exec-1",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				Content:    "regular assistant text",
				ChunkIndex: 1,
			},
		},
	})

	if got := store.List("session-1"); len(got) != 0 {
		t.Fatalf("expected no terminal snapshots, got %d", len(got))
	}
}

func terminalEvent(eventType, executionID, content string, chunkIndex int) storeevents.Event {
	return terminalEventAt(eventType, executionID, content, chunkIndex, time.Time{})
}

func terminalEventAt(eventType, executionID, content string, chunkIndex int, timestamp time.Time) storeevents.Event {
	return terminalEventWithMetadataAt(eventType, executionID, content, chunkIndex, nil, timestamp)
}

func terminalEventWithMetadata(executionID, content string, chunkIndex int, metadata map[string]interface{}, timestamp time.Time) storeevents.Event {
	return terminalEventWithMetadataAt("streaming_chunk", executionID, content, chunkIndex, metadata, timestamp)
}

func terminalEventWithMetadataAt(eventType, executionID, content string, chunkIndex int, metadata map[string]interface{}, timestamp time.Time) storeevents.Event {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata["kind"] = "terminal"
	return storeevents.Event{
		Type:        eventType,
		Timestamp:   timestamp,
		SessionID:   "session-1",
		ExecutionID: executionID,
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: metadata,
				},
				Content:    content,
				ChunkIndex: chunkIndex,
			},
		},
	}
}

func terminalEndEvent(executionID string, metadata map[string]interface{}) storeevents.Event {
	if metadata == nil {
		metadata = map[string]interface{}{}
	}
	metadata["kind"] = "terminal"
	return storeevents.Event{
		Type:        "streaming_end",
		SessionID:   "session-1",
		ExecutionID: executionID,
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingEnd,
			Data: &agentevents.StreamingEndEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: metadata,
				},
			},
		},
	}
}

func toolStartEvent(executionID, toolCallID, toolName, args string, metadata map[string]interface{}) storeevents.Event {
	return storeevents.Event{
		Type:        string(agentevents.ToolCallStart),
		SessionID:   "session-1",
		ExecutionID: executionID,
		Timestamp:   time.Now(),
		Data: &agentevents.AgentEvent{
			Type: agentevents.ToolCallStart,
			Data: &agentevents.ToolCallStartEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: metadata},
				ToolCallID:    toolCallID,
				ToolName:      toolName,
				ToolParams:    agentevents.ToolParams{Arguments: args},
			},
		},
	}
}

func toolEndEvent(executionID, toolCallID, toolName, result string, metadata map[string]interface{}) storeevents.Event {
	return storeevents.Event{
		Type:        string(agentevents.ToolCallEnd),
		SessionID:   "session-1",
		ExecutionID: executionID,
		Timestamp:   time.Now(),
		Data: &agentevents.AgentEvent{
			Type: agentevents.ToolCallEnd,
			Data: &agentevents.ToolCallEndEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: metadata},
				ToolCallID:    toolCallID,
				ToolName:      toolName,
				Result:        result,
			},
		},
	}
}

func TestDeriveStatusExtractsClaudeAssistantPreview(t *testing.T) {
	screen := `╭─── Claude Code v2.1.143 ───╮
❯ check the current state
  Calling api-bridge 2 times… (ctrl+o to expand)
⏺ Let me check the current state of saved jobs and then run the bidding workflow.

  Calling api-bridge 3 times… (ctrl+o to expand)
✻ Sautéed for 8s
❯`

	status := DeriveStatus(screen, map[string]interface{}{"provider": "claude-code"})
	if status.ProviderLabel != "Claude Code" {
		t.Fatalf("provider = %q", status.ProviderLabel)
	}
	if status.AssistantPreview != "Let me check the current state of saved jobs and then run the bidding workflow." {
		t.Fatalf("assistant preview = %q", status.AssistantPreview)
	}
	if status.ToolSummary != "api-bridge x3" {
		t.Fatalf("tool summary = %q", status.ToolSummary)
	}
}

func TestDeriveStatusKeepsCodexConservative(t *testing.T) {
	screen := `╭────────────────────────────╮
│ >_ OpenAI Codex (v0.130.0) │
╰────────────────────────────╯
• Called codex.list_mcp_resources({"cursor":""})
  └ {"resources": []}
• Working (6m 32s • esc to interrupt)
`

	status := DeriveStatus(screen, map[string]interface{}{"provider": "codex-cli"})
	if status.ProviderLabel != "Codex CLI" {
		t.Fatalf("provider = %q", status.ProviderLabel)
	}
	if status.AssistantPreview != "" {
		t.Fatalf("codex assistant preview should stay empty when ambiguous, got %q", status.AssistantPreview)
	}
	if status.StatusText != "Codex CLI is working" {
		t.Fatalf("status text = %q", status.StatusText)
	}
	if status.ToolSummary != "codex.list_mcp_resources" {
		t.Fatalf("tool summary = %q", status.ToolSummary)
	}
}

// Live-pane fixtures captured from a real Cursor Agent tmux session
// (mlp-cursor-cli-int-…). Used by the cursor completion-detection tests below.

const cursorIdlePaneFixture = `  Cursor Agent
  v2026.05.20-2b5dd59
  Use /mcp to connect Cursor to your tools and data sources.


  User: which workflows are there


  Here are the workflows: alpha, beta, gamma.


  → Add a follow-up


  Ask (shift+tab to cycle)
  Composer 2.5 · 15.8%                                                                                                                                Auto-run
  ~/ai-work/coding-agent-loop/workspace-docs/_users/default/Chats · main`

const cursorBusyPaneFixture = `  Cursor Agent
  v2026.05.20-2b5dd59
  Use /mcp to connect Cursor to your tools and data sources.


  User: which workflows are there


 ⠰⠰ Composing  1.87k tokens
    Tip: Use /mcp to connect Cursor to your tools and data sources.


  → Add a follow-up                                                                                                                             ctrl+c to stop


  Ask (shift+tab to cycle)
  Composer 2.5 · 15.3%                                                                                                                                Auto-run
  ~/ai-work/coding-agent-loop/workspace-docs/_users/default/Chats · main`

func TestProviderLabelDetectsCursor(t *testing.T) {
	// Metadata path.
	if got := providerLabel("", map[string]interface{}{"provider": "cursor-cli"}); got != "Cursor CLI" {
		t.Fatalf("metadata provider=cursor-cli → %q, want %q", got, "Cursor CLI")
	}
	// Content fallback path (no metadata).
	if got := providerLabel(cursorIdlePaneFixture, nil); got != "Cursor CLI" {
		t.Fatalf("content fallback → %q, want %q", got, "Cursor CLI")
	}
}

// Reproduces the root cause of terminal 669ff3d0…: when Cursor returns to its
// input prompt, the busy detector must not fire and the idle detector MUST
// fire — otherwise completion only happens via the 2-minute stability timeout.
func TestCursorIdlePaneIsDetectedAsIdleNotBusy(t *testing.T) {
	if terminalContentLooksBusy(cursorIdlePaneFixture) {
		t.Fatalf("idle cursor pane should not look busy")
	}
	if !terminalContentLooksIdle(cursorIdlePaneFixture) {
		t.Fatalf("idle cursor pane should be detected as idle (was: not idle)")
	}
}

// Reproduces the opposite half: while Cursor is still composing, the pane
// must look busy so we don't prematurely mark the terminal completed.
func TestCursorBusyPaneIsDetectedAsBusy(t *testing.T) {
	if !terminalContentLooksBusy(cursorBusyPaneFixture) {
		t.Fatalf("busy cursor pane (Composing + ctrl+c to stop) should look busy")
	}
	if terminalContentLooksIdle(cursorBusyPaneFixture) {
		t.Fatalf("busy cursor pane should not be detected as idle")
	}
}

// TestSessionHasBusyCodingTmuxIgnoresCompletedTerminal guards the follow-up-submit
// fix: a coding-agent (codex) pane that EXITED mid-spinner is marked completed
// (Active=false) but its snapshot still holds the busy-looking content. It must
// NOT keep the session "busy" — otherwise session_status never completes, the
// chat's isStreaming stays stuck, and the next message routes to live-input on the
// dead pane instead of a new /api/query turn.
func TestSessionHasBusyCodingTmuxIgnoresCompletedTerminal(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		cursorBusyPaneFixture,
		0,
		map[string]interface{}{
			"tmux_session":   "mlp-codex-cli-busy",
			"execution_kind": "main_agent",
		},
		time.Now(),
	))

	// Live pane with busy content → busy (steering / no-complete is correct).
	if !store.SessionHasBusyCodingTmux("session-1") {
		t.Fatal("a LIVE coding tmux with busy content should be reported busy")
	}

	// The codex pane exits mid-spinner → terminal marked completed (Active=false),
	// snapshot content unchanged (still busy-looking), TmuxSession still set.
	terminalID := "session-1:main:session-1"
	completed, ok := store.MarkCompleted(terminalID)
	if !ok {
		t.Fatalf("expected to mark terminal %q completed", terminalID)
	}
	if completed.Active || strings.TrimSpace(completed.TmuxSession) == "" {
		t.Fatalf("precondition: completed terminal must be inactive with tmux_session retained (active=%v tmux=%q)", completed.Active, completed.TmuxSession)
	}

	if store.SessionHasBusyCodingTmux("session-1") {
		t.Fatal("a COMPLETED coding tmux (stale busy content) must NOT be reported busy")
	}
}

func TestSessionHasBusyCodingTmuxIgnoresHistoricalSpinnerBeforeSettledPrompt(t *testing.T) {
	store := NewStore()
	content := `Working (1m 36s • esc to interrupt)

› Find and fix a bug in @filename

No—137 of 143 USD rows are verified.

─ Worked for 2m 13s ──────────────────────────────────────

› Find and fix a bug in @filename`
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		content,
		0,
		map[string]interface{}{
			"tmux_session":   "mlp-codex-cli-idle",
			"execution_kind": "main_agent",
			"provider":       "codex-cli",
		},
		time.Now(),
	))

	if store.SessionHasBusyCodingTmux("session-1") {
		t.Fatal("a settled Codex prompt after an old spinner must be reported idle")
	}
	if !store.SessionHasRetainedCodingTmux("session-1") {
		t.Fatal("the idle pane must remain retained for the next message")
	}
}

func TestSessionHasBusyMainCodingTmuxIgnoresBusyChild(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:collect-data",
		cursorBusyPaneFixture,
		0,
		map[string]interface{}{
			"tmux_session":   "mlp-cursor-cli-child",
			"execution_kind": "workflow_step",
		},
		time.Now(),
	))

	if !store.SessionHasBusyCodingTmux("session-1") {
		t.Fatal("busy child should count as aggregate session activity")
	}
	if store.SessionHasBusyMainCodingTmux("session-1") {
		t.Fatal("busy child must not make the main chat agent steerable")
	}

	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		cursorBusyPaneFixture,
		0,
		map[string]interface{}{
			"tmux_session":   "mlp-cursor-cli-main",
			"execution_kind": "main_agent",
		},
		time.Now(),
	))
	if !store.SessionHasBusyMainCodingTmux("session-1") {
		t.Fatal("busy main pane should make the main chat agent steerable")
	}
}

func TestSessionHasRetainedCodingTmuxDoesNotRequireBusyContent(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		"π • codex-cli • ready\n> ",
		0,
		map[string]interface{}{
			"tmux_session":   "mlp-codex-cli-idle",
			"execution_kind": "main_agent",
		},
		time.Now(),
	))

	if store.SessionHasBusyCodingTmux("session-1") {
		t.Fatal("idle retained tmux should not be reported busy")
	}
	if !store.SessionHasRetainedCodingTmux("session-1") {
		t.Fatal("idle live tmux should still be reported retained")
	}

	terminalID := "session-1:main:session-1"
	if _, ok := store.MarkCompleted(terminalID); !ok {
		t.Fatalf("expected to mark terminal %q completed", terminalID)
	}
	if store.SessionHasRetainedCodingTmux("session-1") {
		t.Fatal("completed terminal should not be reported as retained/live")
	}
}

func TestDismissedLiveCodingTmuxRemainsOperationallyVisible(t *testing.T) {
	store := NewStore()
	store.HandleEvent("session-1", terminalEventWithMetadata(
		"main:session-1",
		cursorBusyPaneFixture,
		0,
		map[string]interface{}{
			"tmux_session":   "mlp-codex-cli-dismissed",
			"execution_kind": "main_agent",
		},
		time.Now(),
	))
	terminalID := "session-1:main:session-1"
	if !store.Dismiss(terminalID) {
		t.Fatal("expected terminal dismissal to succeed")
	}
	if store.SessionHasBusyCodingTmux("session-1") != true {
		t.Fatal("dismissed live pane must remain visible to busy routing")
	}
	if store.SessionHasRetainedCodingTmux("session-1") != true {
		t.Fatal("dismissed live pane must remain visible to lifecycle routing")
	}
	if _, visible := store.Get(terminalID); visible {
		t.Fatal("dismissed pane should remain hidden from presentation")
	}
}

func TestDeriveStatusLabelsCursor(t *testing.T) {
	status := DeriveStatus(cursorBusyPaneFixture, map[string]interface{}{"provider": "cursor-cli"})
	if status.ProviderLabel != "Cursor CLI" {
		t.Fatalf("provider = %q", status.ProviderLabel)
	}
	if status.StatusText != "Cursor CLI is working" {
		t.Fatalf("status text = %q (want fallback %q)", status.StatusText, "Cursor CLI is working")
	}
}

// Cursor's workspace-trust modal: pane LOOKS settled (no spinner, "Ask
// (shift+tab to cycle)" is visible) but the agent is actually blocked. Must
// NOT be marked idle — otherwise the 2-min timeout would wrongly mark the
// terminal completed while the operator is being asked to accept the modal.
func TestCursorTrustPromptIsNotIdle(t *testing.T) {
	pane := `  Cursor Agent
  v2026.05.20-2b5dd59

  ⚠ Workspace Trust Required
  This will also enable the MCP servers configured for this workspace.
  Do you trust the contents of this directory?
  /tmp/workspace
  [a] Trust this workspace
  [w] Trust this workspace, but don't enable all MCP servers
  [q] Quit


  → Plan, search, build anything


  Ask (shift+tab to cycle)`

	if terminalContentLooksIdle(pane) {
		t.Fatalf("trust prompt must not be treated as idle (would let timeout mark terminal completed)")
	}
	if !terminalContentHasBlockingModal(pane) {
		t.Fatalf("trust prompt must register as a blocking modal")
	}
}

// A Claude/Gemini pane that happens to mention "cursor agent" in prose must
// NOT be misclassified as Cursor — otherwise downstream Cursor-specific filters
// would corrupt the preview and idle detection for the wrong provider.
func TestProviderLabelIgnoresCasualCursorMentions(t *testing.T) {
	pane := `╭─── Claude Code v2.1.143 ───╮
⏺ To debug this you can open in the Cursor agent — paste the file path.
❯`
	if got := providerLabel(pane, nil); got != "Claude Code" {
		t.Fatalf("pane should stay classified as Claude Code, got %q", got)
	}
}

// Prose containing a Cursor-shaped token ("Use /etc/foo", "User: something",
// "→ Step 1") must NOT be filtered as chrome from a Claude/Gemini preview.
// This locks in the tightening fix that removed the overly-broad noisy
// prefixes introduced earlier.
func TestNoisyTerminalLineKeepsLegitimateProse(t *testing.T) {
	keep := []string{
		"Use /etc/hosts to override DNS for that domain.",
		"User: enter your username and press return.",
		"→ Step 1: install dependencies",
		"Composer Smith wrote the original score.",
		"The Cursor agent lets you edit code interactively.", // single mention, not header
	}
	for _, line := range keep {
		if isNoisyTerminalLine(line) {
			t.Fatalf("legitimate prose %q was incorrectly flagged as noisy", line)
		}
	}
}

// Pin Cursor's actual chrome lines as noisy. These are the lines markerPreview
// must stop at when collecting an "Assistant:" block.
func TestNoisyTerminalLineFiltersCursorChrome(t *testing.T) {
	chrome := []string{
		"→ Add a follow-up",
		"Ask (shift+tab to cycle)",
		"Composer 2.5 · 15.8%",
		"Composer 2 Fast · 5.5%",
		"Use /mcp to connect Cursor to your tools and data sources.",
		"Cursor Agent v2026.05.20-2b5dd59",
	}
	for _, line := range chrome {
		if !isNoisyTerminalLine(line) {
			t.Fatalf("cursor chrome line %q must be treated as noisy", line)
		}
	}
}

// Multi-turn pane: when several "Assistant:" blocks are on screen, the live
// preview should surface the LATEST one (matches Claude/Gemini markerPreview
// behavior).
func TestDeriveStatusCursorPreviewPicksLatestAssistantTurn(t *testing.T) {
	screen := `  Cursor Agent

  User: first question

  Assistant: First answer that is now stale.

  User: second question

  Assistant: Second and current answer.


  → Add a follow-up


  Ask (shift+tab to cycle)
  Composer 2.5 · 12.0%`

	status := DeriveStatus(screen, map[string]interface{}{"provider": "cursor-cli"})
	if !strings.Contains(status.AssistantPreview, "Second and current answer.") {
		t.Fatalf("preview should pick the latest Assistant: block, got %q", status.AssistantPreview)
	}
	if strings.Contains(status.AssistantPreview, "First answer") {
		t.Fatalf("preview leaked a stale Assistant: block, got %q", status.AssistantPreview)
	}
}

// Live capture from a Cursor v2026-05-20 pane that drops the "Assistant:"
// label entirely. assistantPreview must fall back to a section-aware scan and
// still surface the response prose (not the echoed "hi" prompt, not the
// "Cursor Agent" header, not the input-box chrome).
func TestDeriveStatusCursorPreviewWorksWithoutAssistantLabel(t *testing.T) {
	screen := `  Cursor Agent
  v2026.05.20-2b5dd59
  Use /config to customize Cursor settings and behavior.


  hi


  Hi — how can I help today?

  I can answer questions about your workspace (workflows, agents, banking/finance automations, code, architecture, and so on). I'm in Ask mode, so I'll
  explain and explore read-only; if you want me to change files or run things, switch to Agent mode.

  What would you like to look at?




  → Add a follow-up


  Ask (shift+tab to cycle)
  Composer 2.5 · 14.4%                                                                                                                                Auto-run
  ~/ai-work/coding-agent-loop/workspace-docs/_users/default/Chats · main`

	status := DeriveStatus(screen, map[string]interface{}{"provider": "cursor-cli"})
	if status.ProviderLabel != "Cursor CLI" {
		t.Fatalf("provider = %q", status.ProviderLabel)
	}
	preview := status.AssistantPreview
	// Trailing "?" is stripped by the shared cleanPreviewLine cleaner (legacy
	// Gemini-chrome rule). Assert on the question-mark-free form.
	if !strings.Contains(preview, "Hi — how can I help today") {
		t.Fatalf("preview missing response opening, got %q", preview)
	}
	if !strings.Contains(preview, "What would you like to look at") {
		t.Fatalf("preview should reach the closing question, got %q", preview)
	}
	forbidden := []string{
		"hi\n", // echoed user prompt should not survive (line is "hi")
		"Cursor Agent",
		"v2026.05",
		"Use /config",
		"Add a follow-up",
		"shift+tab",
		"Composer 2.5",
	}
	for _, bad := range forbidden {
		if strings.Contains(preview, bad) {
			t.Fatalf("preview leaked chrome/prompt %q, got %q", bad, preview)
		}
	}
}

// Multi-turn pane on the new no-label format: preview must pick only the
// latest response block, not earlier turns or echoed user prompts.
func TestCursorMarkerlessPreviewPicksLatestTurnInMultiTurnPane(t *testing.T) {
	screen := `  Cursor Agent
  v2026.05.20-2b5dd59


  what is two plus two


  Four.


  what is three plus three


  Six.


  → Add a follow-up


  Ask (shift+tab to cycle)
  Composer 2.5 · 12.0%`

	preview := cursorMarkerlessPreview(screen)
	if preview != "Six." {
		t.Fatalf("preview should be only the latest response %q, got %q", "Six.", preview)
	}
}

// Busy pane on the new no-label format: no preview should be surfaced — the
// only candidate text is the echoed user prompt, which would mislead.
func TestCursorMarkerlessPreviewReturnsEmptyWhenBusy(t *testing.T) {
	screen := `  Cursor Agent

  show me the failing tests

 ⠰⠰ Composing  1.20k tokens

  → Add a follow-up                                              ctrl+c to stop

  Ask (shift+tab to cycle)
  Composer 2.5 · 12.0%`

	if got := cursorMarkerlessPreview(screen); got != "" {
		t.Fatalf("busy pane preview should be empty, got %q", got)
	}
}

// A short response should still extract cleanly even though its length is
// similar to the echoed prompt. The 2-blank-line gap is the discriminator.
func TestCursorMarkerlessPreviewKeepsShortResponse(t *testing.T) {
	screen := `  Cursor Agent
  v2026.05.20-2b5dd59


  what is two plus two


  Four.


  → Add a follow-up


  Ask (shift+tab to cycle)
  Composer 2.5 · 11.0%`

	preview := cursorMarkerlessPreview(screen)
	if preview != "Four." {
		t.Fatalf("preview = %q, want %q", preview, "Four.")
	}
}

// TestDeriveStatusExtractsCursorAssistantPreview verifies the live preview
// surfaces Cursor's assistant reply (used by the workspace status UI while a
// turn is mid-flight), and that it does NOT include the input-box chrome.
func TestDeriveStatusExtractsCursorAssistantPreview(t *testing.T) {
	screen := `  Cursor Agent
  v2026.05.20-2b5dd59

  User: list the workflows

  Assistant: There are 33 workflows in this workspace.
  The largest groups are banking ops and retail.


  → Add a follow-up


  Ask (shift+tab to cycle)
  Composer 2.5 · 12.0%
  ~/ai-work · main`

	status := DeriveStatus(screen, map[string]interface{}{"provider": "cursor-cli"})
	if status.ProviderLabel != "Cursor CLI" {
		t.Fatalf("provider = %q", status.ProviderLabel)
	}
	preview := status.AssistantPreview
	want := "There are 33 workflows in this workspace."
	if !strings.Contains(preview, want) {
		t.Fatalf("preview missing %q, got %q", want, preview)
	}
	forbidden := []string{
		"Add a follow-up",
		"shift+tab",
		"Composer 2.5",
		"User:",
		"Cursor Agent",
	}
	for _, bad := range forbidden {
		if strings.Contains(preview, bad) {
			t.Fatalf("preview should not contain %q, got %q", bad, preview)
		}
	}
}

// TestStoreMarkStaleClearsTmuxSession locks in the resize-502 fix: once the
// store has detected the backing tmux session is gone, downstream handlers
// (resize-window, send-keys, paste-buffer) should see an empty TmuxSession on
// the snapshot and short-circuit to their "no live pane" branch instead of
// invoking tmux and bubbling up "can't find session" as a 502 Bad Gateway.
func TestStoreMarkStaleClearsTmuxSession(t *testing.T) {
	store := NewStore()
	now := time.Now()

	store.HandleEvent("session-1", terminalEventWithMetadata(
		"workflow-step:wf-1:step-1",
		"codex chat screen",
		1,
		map[string]interface{}{
			"tmux_session":    "mlp-codex-cli-int-9999-deadbeef",
			"current_step_id": "step-1",
			"execution_kind":  "workflow_step",
			"scope":           "workflow_step",
			"workflow_path":   "Workflow/test",
		},
		now,
	))

	snapshots := store.List("session-1")
	if len(snapshots) != 1 {
		t.Fatalf("expected one terminal snapshot, got %d", len(snapshots))
	}
	before := snapshots[0]
	if strings.TrimSpace(before.TmuxSession) == "" {
		t.Fatalf("seed snapshot must carry tmux session; got %q", before.TmuxSession)
	}

	stale, ok := store.MarkStale(before.TerminalID)
	if !ok {
		t.Fatalf("MarkStale must succeed for an existing terminal")
	}
	if stale.State != "stale" || stale.Active {
		t.Fatalf("MarkStale must mark state=stale + Active=false; got state=%q active=%v", stale.State, stale.Active)
	}
	if strings.TrimSpace(stale.TmuxSession) != "" {
		t.Fatalf("MarkStale must clear TmuxSession so the resize handler short-circuits; got %q", stale.TmuxSession)
	}

	// Idempotent: a second MarkStale call must not flap the snapshot back into
	// having a tmux session, and must keep state=stale.
	stale2, ok := store.MarkStale(before.TerminalID)
	if !ok {
		t.Fatalf("MarkStale must remain idempotent for already-stale terminal")
	}
	if strings.TrimSpace(stale2.TmuxSession) != "" || stale2.State != "stale" {
		t.Fatalf("repeat MarkStale must remain stale + tmux-cleared; got state=%q tmux=%q", stale2.State, stale2.TmuxSession)
	}
}

func TestStoreHandlesStatusLineUpdate(t *testing.T) {
	store := NewStore()

	// Seed the terminal snapshot first so we have something to update
	meta := map[string]interface{}{
		"kind":         "terminal",
		"tmux_session": "mlp-codex-int-1",
	}
	store.HandleEvent("session-1", terminalEventWithMetadata("exec-1", "active terminal pane", 1, meta, time.Now()))

	// Create a status_line event. Provider is used verbatim — the adapter owns
	// its display name ("codex-cli"); the store must not re-map it. TmuxSession
	// scopes the update to the owning pane.
	statusLineEvent := storeevents.Event{
		Type:      "status_line",
		SessionID: "session-1",
		Timestamp: time.Now(),
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingStatusLine,
			Data: &agentevents.StreamingStatusLineEvent{
				Provider:     "codex-cli",
				Model:        "claude-3-5-sonnet",
				TmuxSession:  "mlp-codex-int-1",
				InputTokens:  1200,
				OutputTokens: 350,
				CostUSD:      0.0088,
			},
		},
	}

	store.HandleEvent("session-1", statusLineEvent)

	// Fetch updated snapshot
	snapshot, ok := store.Get("session-1:exec-1")
	if !ok {
		t.Fatalf("expected to find terminal session-1:exec-1")
	}

	if snapshot.Status.ProviderLabel != "codex-cli · claude-3-5-sonnet" {
		t.Errorf("got ProviderLabel = %q, want 'codex-cli · claude-3-5-sonnet'", snapshot.Status.ProviderLabel)
	}
	if snapshot.Status.InputTokens != 1200 {
		t.Errorf("got InputTokens = %d, want 1200", snapshot.Status.InputTokens)
	}
	if snapshot.Status.OutputTokens != 350 {
		t.Errorf("got OutputTokens = %d, want 350", snapshot.Status.OutputTokens)
	}
	if snapshot.Status.CostUSD != 0.0088 {
		t.Errorf("got CostUSD = %f, want 0.0088", snapshot.Status.CostUSD)
	}

	// Now refresh terminal content to simulate a constant pane update and verify telemetry is preserved!
	refreshed, ok := store.RefreshContent("session-1:exec-1", "active terminal pane - newly refreshed content")
	if !ok {
		t.Fatalf("expected RefreshContent to succeed")
	}

	if refreshed.Status.ProviderLabel != "codex-cli · claude-3-5-sonnet" {
		t.Errorf("after RefreshContent: got ProviderLabel = %q, want 'codex-cli · claude-3-5-sonnet'", refreshed.Status.ProviderLabel)
	}
	if refreshed.Status.InputTokens != 1200 {
		t.Errorf("after RefreshContent: got InputTokens = %d, want 1200", refreshed.Status.InputTokens)
	}
	if refreshed.Status.OutputTokens != 350 {
		t.Errorf("after RefreshContent: got OutputTokens = %d, want 350", refreshed.Status.OutputTokens)
	}
	if refreshed.Status.CostUSD != 0.0088 {
		t.Errorf("after RefreshContent: got CostUSD = %f, want 0.0088", refreshed.Status.CostUSD)
	}

	// A second pane in the same session, owned by a different tmux session, must
	// NOT inherit the first pane's telemetry — the status_line carries a
	// tmux_session and the update is scoped to the owning pane only.
	otherMeta := map[string]interface{}{"kind": "terminal", "tmux_session": "mlp-codex-int-2"}
	store.HandleEvent("session-1", terminalEventWithMetadata("exec-2", "second pane", 1, otherMeta, time.Now()))
	store.HandleEvent("session-1", statusLineEvent) // still targets mlp-codex-int-1

	other, ok := store.Get("session-1:exec-2")
	if !ok {
		t.Fatalf("expected to find terminal session-1:exec-2")
	}
	if other.Status.InputTokens != 0 || other.Status.OutputTokens != 0 || other.Status.CostUSD != 0 {
		t.Errorf("unrelated pane received telemetry: in=%d out=%d cost=%f",
			other.Status.InputTokens, other.Status.OutputTokens, other.Status.CostUSD)
	}
	if other.Status.ProviderLabel == "codex-cli · claude-3-5-sonnet" {
		t.Errorf("unrelated pane received provider label %q", other.Status.ProviderLabel)
	}
}

func TestStoreStatusLineCreatesLiveTerminalWhenNoSnapshotExists(t *testing.T) {
	store := NewStore()
	now := time.Now()

	store.HandleEvent("session-2", storeevents.Event{
		Type:      "status_line",
		SessionID: "session-2",
		Timestamp: now,
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingStatusLine,
			Data: &agentevents.StreamingStatusLineEvent{
				Provider:    "pi-cli",
				Model:       "google/gemini-3.5-flash",
				TmuxSession: "mlp-pi-cli-int-new",
				InputTokens: 120,
				CostUSD:     0.0012,
			},
		},
	})

	snapshot, ok := store.Get("session-2:main:session-2")
	if !ok {
		t.Fatalf("status_line with tmux_session should create the canonical main terminal")
	}
	if !snapshot.Active || snapshot.State != "running" {
		t.Fatalf("created terminal should be live/running; active=%v state=%q", snapshot.Active, snapshot.State)
	}
	if snapshot.TmuxSession != "mlp-pi-cli-int-new" {
		t.Fatalf("tmux session = %q, want mlp-pi-cli-int-new", snapshot.TmuxSession)
	}
	if snapshot.StepTransport != "tmux" || snapshot.ExecutionKind != "main_agent" || snapshot.Scope != "main_agent" {
		t.Fatalf("unexpected terminal identity: transport=%q kind=%q scope=%q", snapshot.StepTransport, snapshot.ExecutionKind, snapshot.Scope)
	}
	if snapshot.Status.ProviderLabel != "pi-cli · google/gemini-3.5-flash" {
		t.Fatalf("provider label = %q", snapshot.Status.ProviderLabel)
	}
	if snapshot.Status.InputTokens != 120 || snapshot.Status.CostUSD != 0.0012 {
		t.Fatalf("telemetry missing: in=%d cost=%f", snapshot.Status.InputTokens, snapshot.Status.CostUSD)
	}
}

func TestStoreStatusLineCreatesLiveTerminalFromStatusMetadataTmux(t *testing.T) {
	store := NewStore()
	now := time.Now()

	store.HandleEvent("session-3", storeevents.Event{
		Type:      "status_line",
		SessionID: "session-3",
		Timestamp: now,
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingStatusLine,
			Data: &agentevents.GenericEventData{
				Data: map[string]interface{}{
					"provider": "pi-cli",
					"model":    "google/gemini-3.5-flash",
					"metadata": map[string]interface{}{
						"pi_interactive_session": "mlp-pi-cli-int-meta",
						"working_dir":            "/tmp/chats",
					},
				},
			},
		},
	})

	snapshot, ok := store.Get("session-3:main:session-3")
	if !ok {
		t.Fatalf("status_line metadata with pi_interactive_session should create the canonical main terminal")
	}
	if snapshot.TmuxSession != "mlp-pi-cli-int-meta" {
		t.Fatalf("tmux session = %q, want mlp-pi-cli-int-meta", snapshot.TmuxSession)
	}
	if snapshot.Status.StatusMeta == nil || snapshot.Status.StatusMeta["working_dir"] != "/tmp/chats" {
		t.Fatalf("status metadata not preserved: %+v", snapshot.Status.StatusMeta)
	}
}

func TestStoreStatusLineInheritedMainKindCannotReplaceMainAgentTmux(t *testing.T) {
	store := NewStore()
	now := time.Now()
	sessionID := "schedule-cron--pulse"
	childOwnerID := "pulse-reviewer-eval-789"

	store.HandleEvent(sessionID, terminalEventWithMetadata(
		"main:"+sessionID,
		"parent pulse screen",
		1,
		map[string]interface{}{
			"execution_kind": "main_agent",
			"tmux_session":   "mlp-parent",
		},
		now,
	))
	store.HandleEvent(sessionID, storeevents.Event{
		Type:          "status_line",
		SessionID:     sessionID,
		ExecutionID:   "main:" + sessionID,
		ExecutionKind: "main_agent",
		Timestamp:     now.Add(time.Second),
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingStatusLine,
			Data: &agentevents.StreamingStatusLineEvent{
				Provider:    "claudecode",
				Model:       "claude-opus-4-8",
				TmuxSession: "mlp-reviewer",
				Metadata: map[string]interface{}{
					"background_agent_id": childOwnerID,
					"execution_kind":      "background_agent",
					"scope":               "background_agent",
				},
			},
		},
	})

	parent, ok := store.Get(sessionID + ":main:" + sessionID)
	if !ok || parent.TmuxSession != "mlp-parent" || parent.Content != "parent pulse screen" {
		t.Fatalf("status line replaced parent terminal: found=%v parent=%+v", ok, parent)
	}
	child, ok := store.Get(sessionID + ":" + childOwnerID)
	if !ok {
		t.Fatalf("expected status line to create a separate child terminal")
	}
	if child.ExecutionKind != "background_agent" || child.Scope != "background_agent" {
		t.Fatalf("child identity = kind %q scope %q", child.ExecutionKind, child.Scope)
	}
}
