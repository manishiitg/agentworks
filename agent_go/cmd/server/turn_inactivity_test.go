package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

func idleEvent(kind string, at time.Time, data unifiedevents.EventData) events.Event {
	return events.Event{Type: kind, Timestamp: at, SessionID: "chat", ExecutionID: "main:chat", ExecutionKind: "main_agent", Data: unifiedevents.NewAgentEvent(data)}
}

func TestInactivityClockIgnoresHumanHeartbeatAndPaneRepaint(t *testing.T) {
	start := time.Now().Add(-9 * time.Minute)
	state := &turnInactivityState{sessionID: "chat", executionID: "root", startedAt: start, lastOutputAt: start, tools: map[string]idleTool{}}
	now := time.Now()
	for _, kind := range []string{"user_message", "live_input_confirmed", "heartbeat", "turn_phase", "synthetic_turn_ready"} {
		state.observe(events.Event{Type: kind, Timestamp: now}, now)
	}
	state.observe(idleEvent("streaming_chunk", now, &unifiedevents.StreamingChunkEvent{Source: "terminal", Content: "screen repaint"}), now)
	child := idleEvent("tool_call_start", now, &unifiedevents.ToolCallStartEvent{ToolCallID: "child", ToolName: "shell"})
	child.ExecutionKind = "sub_agent"
	child.TerminalOwnerID = "child"
	state.observe(child, now)
	if !state.lastOutputAt.Equal(start) || len(state.openToolNames()) != 0 {
		t.Fatal("non-agent events changed foreground progress")
	}
	state.observe(idleEvent("streaming_chunk", now, &unifiedevents.StreamingChunkEvent{Source: "transcript", Content: "actual reply"}), now)
	if !state.lastOutputAt.Equal(now) {
		t.Fatal("real agent output did not reset progress")
	}
}

func TestInactivityProtectsEachToolUntilItsOwnEnd(t *testing.T) {
	now := time.Now()
	state := &turnInactivityState{sessionID: "chat", executionID: "root", startedAt: now.Add(-time.Minute), lastOutputAt: now.Add(-time.Minute), tools: map[string]idleTool{}}
	for _, id := range []string{"a", "b"} {
		state.observe(idleEvent("tool_call_start", now, &unifiedevents.ToolCallStartEvent{ToolCallID: id, ToolName: "shell"}), now)
	}
	state.observe(idleEvent("tool_call_end", now, &unifiedevents.ToolCallEndEvent{ToolCallID: "a", ToolName: "shell"}), now)
	if len(state.openToolNames()) != 1 {
		t.Fatal("one end incorrectly closed two parallel tools")
	}
	state.observe(idleEvent("tool_call_error", now, &unifiedevents.ToolCallErrorEvent{ToolCallID: "b", ToolName: "shell"}), now)
	state.observe(idleEvent("tool_call_start", now, &unifiedevents.ToolCallStartEvent{ToolCallID: "a", ToolName: "shell"}), now)
	if len(state.openToolNames()) != 0 {
		t.Fatal("error or replay left a tool open")
	}
}

func TestInactivityDoesNotEndAQuestionWaitingForThePerson(t *testing.T) {
	now := time.Now()
	state := &turnInactivityState{sessionID: "chat", startedAt: now.Add(-time.Hour), lastOutputAt: now.Add(-time.Hour), tools: map[string]idleTool{}}
	state.observe(idleEvent("coding_agent_question", now, &unifiedevents.GenericEventData{}), now)
	if !state.waiting {
		t.Fatal("human question not protected")
	}
	state.observe(idleEvent("human_feedback_resolved", now, &unifiedevents.GenericEventData{}), now)
	if state.waiting {
		t.Fatal("resolved question remains protected")
	}
}

func TestInactivityWatcherCancelsOnlyItsExactQuietTurn(t *testing.T) {
	oldInterval := turnInactivityCheckInterval
	turnInactivityCheckInterval = 10 * time.Millisecond
	defer func() { turnInactivityCheckInterval = oldInterval }()
	api := &StreamingAPI{}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := api.watchTurnInactivity(ctx, "chat", "old-root", time.Now().Add(-11*time.Minute), nil, func(reason error) bool { done <- reason; return true })
	defer stop()
	select {
	case reason := <-done:
		if !strings.Contains(reason.Error(), "10 minutes") {
			t.Fatal(reason)
		}
	case <-time.After(time.Second):
		t.Fatal("quiet turn never timed out")
	}
	// Removing an old watcher must not remove a replacement for the same root.
	replacement := api.watchTurnInactivity(ctx, "chat", "old-root", time.Now(), nil, func(error) bool { t.Error("fresh replacement canceled"); return true })
	defer replacement()
	stop()
	api.turnInactivityMu.Lock()
	remaining := len(api.turnInactivity["chat"])
	api.turnInactivityMu.Unlock()
	if remaining != 1 {
		t.Fatalf("old cleanup removed new watcher: %d", remaining)
	}
}

func TestCommonSyntheticDrainStopsWithoutProducerClosing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reason, _ := drainTurnTextStream(ctx, make(chan string), nil)
	if reason == "" {
		t.Fatal("cancellation did not release silent stream")
	}
}

func TestInactivityWatchProtectsToolsAlreadyRunningBeforeRegistration(t *testing.T) {
	oldInterval := turnInactivityCheckInterval
	turnInactivityCheckInterval = 10 * time.Millisecond
	defer func() { turnInactivityCheckInterval = oldInterval }()
	store := events.NewEventStore(100)
	api := &StreamingAPI{eventStore: store}
	tool := idleEvent("tool_call_start", time.Now().Add(-12*time.Minute), &unifiedevents.ToolCallStartEvent{ToolCallID: "long-tool", ToolName: "long_shell"})
	store.AddEvent("chat", tool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := make(chan bool, 1)
	stop := api.watchTurnInactivity(ctx, "chat", "root", time.Now().Add(-11*time.Minute), nil, func(error) bool { ended <- true; return true })
	defer stop()
	select {
	case <-ended:
		t.Fatal("an in-flight tool was timed out")
	case <-time.After(60 * time.Millisecond):
	}
	// Completing the old tool resets progress, rather than instantly expiring
	// the turn based on the timestamp from before the tool was running.
	store.SetEventAddedCallback(api.observeTurnInactivity)
	store.AddEvent("chat", idleEvent("tool_call_end", time.Now(), &unifiedevents.ToolCallEndEvent{ToolCallID: "long-tool", ToolName: "long_shell"}))
	select {
	case <-ended:
		t.Fatal("tool completion did not reset the inactivity clock")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestCanceledTextDrainReleasesCallerForFinalHistoryCapture(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	chunks := make(chan string)
	finalHistory := make(chan []string, 1)
	go func() {
		var seen []string
		drainTurnTextStream(ctx, chunks, func(chunk string) { seen = append(seen, chunk) })
		// The query's normal post-stream cleanup and persistence now follow
		// this shared drain even when the provider leaves the stream open.
		finalHistory <- seen
	}()
	chunks <- "partial reply"
	cancel()
	select {
	case history := <-finalHistory:
		if len(history) != 1 || history[0] != "partial reply" {
			t.Fatalf("history=%v", history)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation stranded final history capture")
	}
}

func TestLiveInputInheritsActualActivityInsteadOfHumanAdmission(t *testing.T) {
	api := &StreamingAPI{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	origin := time.Now().Add(-5 * time.Minute)
	lastOutput := origin.Add(time.Minute)
	stop := api.watchTurnInactivity(ctx, "chat", "root", origin, nil, func(error) bool { return false })
	defer stop()
	api.turnInactivityMu.Lock()
	for _, state := range api.turnInactivity["chat"] {
		state.lastOutputAt = lastOutput
		state.quietMinute = 4
		state.tools["in-flight"] = idleTool{name: "shell", open: true}
	}
	api.turnInactivityMu.Unlock()
	admitted := time.Now()
	stopInput := api.watchTurnInactivity(ctx, "chat", "steer", admitted, nil, func(error) bool { return false })
	defer stopInput()
	api.turnInactivityMu.Lock()
	defer api.turnInactivityMu.Unlock()
	found := false
	for _, state := range api.turnInactivity["chat"] {
		if state.executionID != "steer" {
			continue
		}
		found = true
		if !state.startedAt.Equal(admitted) || !state.turnStartedAt.Equal(origin) || !state.lastOutputAt.Equal(lastOutput) || state.quietMinute != 4 || len(state.openToolNames()) != 1 {
			t.Fatalf("live input lost shared progress or its generation fence: %+v", state)
		}
	}
	if !found {
		t.Fatal("live input watcher was not registered")
	}
}

func TestActivityMetadataUsesTheInactivityClock(t *testing.T) {
	store := events.NewEventStore(100)
	api := &StreamingAPI{eventStore: store}
	origin := time.Now().Add(-3 * time.Minute).UTC()
	lastOutput := origin.Add(time.Minute)
	state := &turnInactivityState{sessionID: "activity-clock", executionID: "root", startedAt: origin, turnStartedAt: origin, lastOutputAt: lastOutput, quietMinute: 2}
	api.emitTurnActivityState(state)
	all := store.GetAllEventsRaw("activity-clock")
	if len(all) != 1 || all[0].Type != "turn_phase" {
		t.Fatalf("expected one backwards-compatible phase event: %+v", all)
	}
	payload, ok := all[0].Data.Data.(*unifiedevents.GenericEventData)
	if !ok || payload.Data["turn_started_at"] != origin.Format(time.RFC3339Nano) || payload.Data["last_activity_at"] != lastOutput.Format(time.RFC3339Nano) || payload.Data["phase"] != "quiet" {
		t.Fatalf("wrong activity metadata: %+v", payload)
	}
	state.observe(all[0], time.Now())
	if !state.lastOutputAt.Equal(lastOutput) {
		t.Fatal("publishing the clock reset its own inactivity")
	}
}
