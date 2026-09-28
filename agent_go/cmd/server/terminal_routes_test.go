package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

func TestTerminalSizeHintResizesLiveTerminalsForSession(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-resize"
	otherSessionID := "session-other-resize"

	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "tmux-main-resize", "main pane", 1))
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", "tmux-step-resize", "step pane", 1))
	store.HandleEvent(otherSessionID, terminalRouteChunkEvent(otherSessionID, "main:"+otherSessionID, "tmux-other-resize", "other pane", 1))

	originalRunTerminalTmuxCommand := runTerminalTmuxCommand
	defer func() { runTerminalTmuxCommand = originalRunTerminalTmuxCommand }()
	var calls [][]string
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	// The geometry guard reads the current tmux window size; return a size that
	// DIFFERS from the requested 132x41 so the guard proceeds with the resize.
	originalRunTerminalTmuxOutputCommand := runTerminalTmuxOutputCommand
	defer func() { runTerminalTmuxOutputCommand = originalRunTerminalTmuxOutputCommand }()
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		return "80\t24", nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/size-hint", bytes.NewBufferString(`{"cols":132,"rows":41,"session_id":"`+sessionID+`"}`))
	rec := httptest.NewRecorder()
	api.handleTerminalSizeHint(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("size hint status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := int(resp["resized"].(float64)); got != 2 {
		t.Fatalf("resized = %d, want 2", got)
	}

	// Each resized session must be pinned to window-size manual (so resize-window is
	// authoritative) and then resized to the requested geometry.
	resizeTargets := map[string]bool{}
	manualTargets := map[string]bool{}
	for _, call := range calls {
		joined := strings.Join(call, " ")
		target := ""
		for i, arg := range call {
			if arg == "-t" && i+1 < len(call) {
				target = call[i+1]
			}
		}
		switch {
		case strings.Contains(joined, "resize-window"):
			if !strings.Contains(joined, "-x 132") || !strings.Contains(joined, "-y 41") {
				t.Fatalf("resize call = %#v, want resize-window -x 132 -y 41", call)
			}
			resizeTargets[target] = true
		case strings.Contains(joined, "window-size") && strings.Contains(joined, "manual"):
			manualTargets[target] = true
		default:
			t.Fatalf("unexpected tmux call = %#v", call)
		}
	}
	if !resizeTargets["tmux-main-resize"] || !resizeTargets["tmux-step-resize"] {
		t.Fatalf("resize targets = %#v, want main and step tmux sessions", resizeTargets)
	}
	if resizeTargets["tmux-other-resize"] {
		t.Fatalf("resize targets = %#v, must not resize other sessions", resizeTargets)
	}
	if !manualTargets["tmux-main-resize"] || !manualTargets["tmux-step-resize"] {
		t.Fatalf("window-size manual not set for resized sessions: %#v", manualTargets)
	}
}

func TestGetMainTerminalReturnsOnlyMainAgentPane(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-main-terminal-product-view"

	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:child", "tmux-child", "child output", 1))
	main := terminalRouteChunkEvent(sessionID, "main:"+sessionID, "tmux-main", "main output", 1)
	main.ExecutionKind = "main_agent"
	chunk := main.Data.Data.(*agentevents.StreamingChunkEvent)
	chunk.Metadata["execution_kind"] = "main_agent"
	chunk.Metadata["scope"] = "main_agent"
	chunk.Metadata["current_step_id"] = "main_agent:" + sessionID
	store.HandleEvent(sessionID, main)

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/main-terminal", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rec := httptest.NewRecorder()
	api.handleGetMainTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("main terminal status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode main terminal response: %v", err)
	}
	if response.TmuxSession != "tmux-main" || response.Content != "main output" {
		t.Fatalf("main terminal = tmux=%q content=%q, want main pane", response.TmuxSession, response.Content)
	}
}

func TestGetMainTerminalMetadataOmitsContentAndTmuxCapture(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-main-terminal-metadata"
	main := terminalRouteChunkEvent(sessionID, "main:"+sessionID, "tmux-main", strings.Repeat("large output\n", 2000), 1)
	main.ExecutionKind = "main_agent"
	chunk := main.Data.Data.(*agentevents.StreamingChunkEvent)
	chunk.Metadata["execution_kind"] = "main_agent"
	chunk.Metadata["scope"] = "main_agent"
	store.HandleEvent(sessionID, main)

	original := runTerminalTmuxOutputCommand
	defer func() { runTerminalTmuxOutputCommand = original }()
	runTerminalTmuxOutputCommand = func(_ context.Context, args ...string) (string, error) {
		t.Fatalf("metadata request unexpectedly ran tmux %s", strings.Join(args, " "))
		return "", nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/main-terminal?content=none", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rec := httptest.NewRecorder()
	api.handleGetMainTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("main terminal metadata status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode main terminal metadata response: %v", err)
	}
	if response.Content != "" || len(response.Rows) != 0 {
		t.Fatalf("metadata response carried terminal body: content=%d bytes rows=%d", len(response.Content), len(response.Rows))
	}
	if response.TmuxSession != "tmux-main" || !response.Active {
		t.Fatalf("metadata lost live identity: tmux=%q active=%v", response.TmuxSession, response.Active)
	}
}

func TestLatestOwnedCodingTmuxSessionSelectsNewestMatchingPane(t *testing.T) {
	original := runTerminalTmuxOutputCommand
	defer func() { runTerminalTmuxOutputCommand = original }()
	runTerminalTmuxOutputCommand = func(_ context.Context, args ...string) (string, error) {
		return strings.Join([]string{
			"mlp-claude-code-old\t100\tsession-1",
			"unrelated-shell\t300\tsession-1",
			"mlp-claude-code-other\t400\tsession-2",
			"mlp-claude-code-new\t200\tsession-1",
		}, "\n"), nil
	}

	if got := latestOwnedCodingTmuxSession(context.Background(), "session-1"); got != "mlp-claude-code-new" {
		t.Fatalf("latest pane = %q, want mlp-claude-code-new", got)
	}
}

func TestMainTerminalForSessionRecoversReplacementPane(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-retry-replacement"
	_, ok := store.UpsertStaticSnapshot(sessionID, terminals.Snapshot{
		OwnerID:       "main:" + sessionID,
		ExecutionKind: "main_agent",
		Scope:         "main_agent",
		Content:       "screen retained from the failed pane",
	})
	if !ok {
		t.Fatal("expected static main terminal")
	}

	original := runTerminalTmuxOutputCommand
	defer func() { runTerminalTmuxOutputCommand = original }()
	runTerminalTmuxOutputCommand = func(_ context.Context, args ...string) (string, error) {
		return "mlp-claude-code-replacement\t200\t" + sessionID + "\n", nil
	}

	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID+"/main-terminal", nil)
	req = mux.SetURLVars(req, map[string]string{"session_id": sessionID})
	rec := httptest.NewRecorder()
	snapshot, found := api.mainTerminalForSession(rec, req)
	if !found {
		t.Fatalf("main terminal not found: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if snapshot.TmuxSession != "mlp-claude-code-replacement" || !snapshot.Active || snapshot.ProcessState != "live" {
		t.Fatalf("recovered terminal = tmux %q active=%v process=%q", snapshot.TmuxSession, snapshot.Active, snapshot.ProcessState)
	}
}

func TestTerminalSizeHintIgnoresTinyGeometry(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-tiny-resize"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "tmux-main-tiny", "main pane", 1))

	originalRunTerminalTmuxCommand := runTerminalTmuxCommand
	defer func() { runTerminalTmuxCommand = originalRunTerminalTmuxCommand }()
	var calls [][]string
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/size-hint", bytes.NewBufferString(`{"cols":20,"rows":8,"session_id":"`+sessionID+`"}`))
	rec := httptest.NewRecorder()
	api.handleTerminalSizeHint(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("size hint status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if ignored, _ := resp["ignored"].(bool); !ignored {
		t.Fatalf("ignored = %v, want true", resp["ignored"])
	}
	if len(calls) != 0 {
		t.Fatalf("tiny size hint must not call tmux, calls=%#v", calls)
	}
}

func TestTerminalSizeHintSkipsLiveAttachSessions(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store, liveAttach: newLiveAttachManager()}
	sessionID := "session-live-attach-resize"

	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, "tmux-live-resize", "main pane", 1))
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", "tmux-step-resize", "step pane", 1))

	api.liveAttach.mu.Lock()
	api.liveAttach.sessions["tmux-live-resize"] = &liveAttachStream{tmuxSession: "tmux-live-resize"}
	api.liveAttach.mu.Unlock()

	originalRunTerminalTmuxCommand := runTerminalTmuxCommand
	defer func() { runTerminalTmuxCommand = originalRunTerminalTmuxCommand }()
	var calls [][]string
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	originalRunTerminalTmuxOutputCommand := runTerminalTmuxOutputCommand
	defer func() { runTerminalTmuxOutputCommand = originalRunTerminalTmuxOutputCommand }()
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		return "80\t24", nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/size-hint", bytes.NewBufferString(`{"cols":132,"rows":41,"session_id":"`+sessionID+`"}`))
	rec := httptest.NewRecorder()
	api.handleTerminalSizeHint(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("size hint status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := int(resp["resized"].(float64)); got != 1 {
		t.Fatalf("resized = %d, want 1", got)
	}

	for _, call := range calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "tmux-live-resize") {
			t.Fatalf("live-attached session must not be resized by size-hint, call=%#v", call)
		}
	}
}

func TestListTerminalsExposesStatusLineCreatedLiveTerminal(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store, runtimeCoordinator: NewRuntimeCoordinator()}
	sessionID := "session-statusline-live-terminal"
	tmuxSession := "mlp-pi-cli-int-statusline"

	store.HandleEvent(sessionID, storeevents.Event{
		Type:      "status_line",
		SessionID: sessionID,
		Timestamp: time.Now(),
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingStatusLine,
			Data: &agentevents.StreamingStatusLineEvent{
				Provider:    "pi-cli",
				Model:       "google/gemini-3.5-flash",
				TmuxSession: tmuxSession,
				InputTokens: 321,
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/terminals?session_id="+sessionID+"&content=none", nil)
	rec := httptest.NewRecorder()
	api.handleListTerminals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response listTerminalsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Terminals) != 1 {
		t.Fatalf("terminals len = %d, want 1: %+v", len(response.Terminals), response.Terminals)
	}
	terminal := response.Terminals[0]
	if terminal.TerminalID != sessionID+":main:"+sessionID {
		t.Fatalf("terminal_id = %q", terminal.TerminalID)
	}
	if terminal.TmuxSession != tmuxSession || !terminal.Active || terminal.State != "running" {
		t.Fatalf("terminal not exposed as live: tmux=%q active=%v state=%q", terminal.TmuxSession, terminal.Active, terminal.State)
	}
	if terminal.Status.ProviderLabel != "pi-cli · google/gemini-3.5-flash" || terminal.Status.InputTokens != 321 {
		t.Fatalf("status not exposed: %+v", terminal.Status)
	}
	runtimeState, ok := response.RuntimeStates[sessionID]
	if !ok || runtimeState.Revision == 0 || len(runtimeState.Terminals) != 1 ||
		!runtimeState.Terminals[0].Active || runtimeState.BackgroundLive {
		t.Fatalf("terminal runtime state = %#v, present=%t", runtimeState, ok)
	}
}

func TestListTerminalsActiveOnlyFiltersStaleMetadataList(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	liveSessionID := "session-terminal-active-only-live"
	staleSessionID := "session-terminal-active-only-stale"

	store.HandleEvent(liveSessionID, terminalRouteChunkEvent(liveSessionID, "main:"+liveSessionID, "tmux-live-active-only", "live pane", 1))
	store.HandleEvent(staleSessionID, terminalRouteChunkEvent(staleSessionID, "main:"+staleSessionID, "tmux-stale-active-only", "stale pane", 1))
	if _, ok := store.MarkStale(staleSessionID + ":main:" + staleSessionID); !ok {
		t.Fatal("failed to mark stale terminal")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/terminals?content=none&active_only=1", nil)
	rec := httptest.NewRecorder()
	api.handleListTerminals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response listTerminalsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1: %+v", len(response.Terminals), response.Terminals)
	}
	if response.Terminals[0].SessionID != liveSessionID {
		t.Fatalf("session_id = %q, want %q", response.Terminals[0].SessionID, liveSessionID)
	}
	if response.Terminals[0].Content != "" || len(response.Terminals[0].Rows) != 0 {
		t.Fatalf("active-only metadata response leaked content/rows: content=%q rows=%d", response.Terminals[0].Content, len(response.Terminals[0].Rows))
	}
}

func TestTerminalRoutesCloseAndDismissMismatchedOwnerTerminal(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-e2e"
	tmuxSession := "mlp-claude-code-exp-test"

	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "⏺ Review complete\n\n✻ Cogitated for 4m 37s\n❯", 12))

	before := terminalRouteList(t, api, sessionID)
	if len(before.Terminals) != 1 {
		t.Fatalf("before terminal count = %d, want 1", len(before.Terminals))
	}
	terminalID := before.Terminals[0].TerminalID
	if terminalID != sessionID+":workflow-step:review-plan" {
		t.Fatalf("terminal id = %q, want chunk owner terminal id", terminalID)
	}
	if !before.Terminals[0].Active || before.Terminals[0].State != "running" {
		t.Fatalf("before terminal state = active:%v state:%q, want active running", before.Terminals[0].Active, before.Terminals[0].State)
	}

	store.HandleEvent(sessionID, terminalRouteEndEvent(sessionID, "review-plan", tmuxSession, 300))

	after := terminalRouteList(t, api, sessionID)
	if len(after.Terminals) != 1 {
		t.Fatalf("after terminal count = %d, want 1", len(after.Terminals))
	}
	if after.Terminals[0].TerminalID != terminalID {
		t.Fatalf("terminal id changed after end event: got %q want %q", after.Terminals[0].TerminalID, terminalID)
	}
	if after.Terminals[0].Active {
		t.Fatalf("expected terminal to be inactive after mismatched-owner end event")
	}
	if after.Terminals[0].State != "closing" {
		t.Fatalf("terminal state = %q, want closing", after.Terminals[0].State)
	}
	if after.Terminals[0].RetentionSeconds != 300 || after.Terminals[0].ClosesAt == nil {
		t.Fatalf("retention = %d closes_at=%v, want 300 and closes_at", after.Terminals[0].RetentionSeconds, after.Terminals[0].ClosesAt)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/terminals/"+terminalID, nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleDismissTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	removed := terminalRouteList(t, api, sessionID)
	if len(removed.Terminals) != 0 {
		t.Fatalf("terminal should be dismissed, got %d", len(removed.Terminals))
	}
}

func TestTerminalRoutesListCapsContentButGetKeepsFullContent(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-large"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-codex-cli-int-test"
	tailMarker := "latest terminal tail marker"
	fullContent := strings.Repeat("old terminal output line\n", 5000) + tailMarker

	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, fullContent, 44))

	listed := terminalRouteList(t, api, sessionID)
	if len(listed.Terminals) != 1 {
		t.Fatalf("listed terminal count = %d, want 1", len(listed.Terminals))
	}
	listContent := listed.Terminals[0].Content
	if len(listContent) > listTerminalContentMaxBytes+128 {
		t.Fatalf("list content len = %d, want capped near %d", len(listContent), listTerminalContentMaxBytes)
	}
	if !strings.Contains(listContent, tailMarker) {
		t.Fatalf("list content should keep latest terminal output tail")
	}
	if !strings.Contains(listContent, "truncated") {
		t.Fatalf("list content should indicate truncation")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID, nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var full terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&full); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if full.Content != fullContent {
		t.Fatalf("get content len = %d, want full len %d", len(full.Content), len(fullContent))
	}
}

func TestTerminalContentTailDropsPartialOSCTitle(t *testing.T) {
	fragment := "0;bad-title\a\x1b[31mactual output\x1b[0m"
	got := terminalContentTail(strings.Repeat("x", 100)+fragment, len(fragment))

	if !strings.HasPrefix(got, "[terminal output truncated; showing latest output]\n") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if strings.Contains(got, "0;bad-title") || strings.Contains(got, "\a") {
		t.Fatalf("expected orphaned OSC title fragment to be trimmed, got %q", got)
	}
	if !strings.Contains(got, "\x1b[31mactual output\x1b[0m") {
		t.Fatalf("expected remaining ANSI output, got %q", got)
	}
}

func TestGetTerminalEventsReturnsOnlySelectedTerminalCursorPage(t *testing.T) {
	eventStore := storeevents.NewEventStore(20)
	defer eventStore.Stop()
	terminalStore := terminals.NewStore()
	eventStore.SetEventAddedCallback(terminalStore.HandleEvent)
	api := &StreamingAPI{terminalStore: terminalStore, eventStore: eventStore}

	sessionID := "session-terminal-events-route"
	ownerID := "background:reviewer"
	terminalID := sessionID + ":" + ownerID
	now := time.Now()
	eventStore.AddEvent(sessionID, storeevents.Event{
		ID:              "reviewer-terminal-start",
		Type:            "streaming_chunk",
		Timestamp:       now,
		SessionID:       sessionID,
		ExecutionID:     ownerID,
		ExecutionKind:   "background_agent",
		TerminalOwnerID: ownerID,
		TerminalID:      terminalID,
		Data: &agentevents.AgentEvent{
			Type:      agentevents.StreamingChunk,
			Timestamp: now,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: map[string]interface{}{"kind": "terminal"}},
				Content:       "reviewer running",
				ChunkIndex:    1,
			},
		},
	})
	for i := 1; i <= 3; i++ {
		eventStore.AddEvent(sessionID, storeevents.Event{
			ID:            fmt.Sprintf("reviewer-%d", i),
			Type:          string(agentevents.ToolCallEnd),
			Timestamp:     now.Add(time.Duration(i) * time.Millisecond),
			SessionID:     sessionID,
			ExecutionID:   ownerID,
			ExecutionKind: "background_agent",
			Data: &agentevents.AgentEvent{
				Type:      agentevents.ToolCallEnd,
				Timestamp: now.Add(time.Duration(i) * time.Millisecond),
				Data: &agentevents.ToolCallEndEvent{
					ToolCallID: fmt.Sprintf("call-%d", i),
					ToolName:   "review_tool",
					Result:     "ok",
				},
			},
		})
	}
	eventStore.AddEvent(sessionID, storeevents.Event{
		ID:              "other-owner",
		Type:            string(agentevents.ToolCallEnd),
		Timestamp:       now.Add(4 * time.Millisecond),
		SessionID:       sessionID,
		TerminalOwnerID: "background:other",
		TerminalID:      sessionID + ":background:other",
		Data: &agentevents.AgentEvent{
			Type:      agentevents.ToolCallEnd,
			Timestamp: now.Add(4 * time.Millisecond),
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"/events?limit=2", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminalEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get terminal events status = %d body=%s", rec.Code, rec.Body.String())
	}

	var response struct {
		TerminalID string `json:"terminal_id"`
		Events     []struct {
			ID              string `json:"id"`
			TerminalID      string `json:"terminal_id"`
			TerminalOwnerID string `json:"terminal_owner_id"`
			Sequence        int64  `json:"sequence"`
		} `json:"events"`
		HasOlder bool `json:"has_older"`
		HasNewer bool `json:"has_newer"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode terminal events response: %v", err)
	}
	if response.TerminalID != terminalID {
		t.Fatalf("terminal id = %q, want %q", response.TerminalID, terminalID)
	}
	if len(response.Events) != 2 {
		t.Fatalf("event count = %d, want 2: %+v", len(response.Events), response.Events)
	}
	if got := []string{response.Events[0].ID, response.Events[1].ID}; fmt.Sprint(got) != "[reviewer-2 reviewer-3]" {
		t.Fatalf("event ids = %v, want selected terminal's latest page", got)
	}
	if !response.HasOlder || response.HasNewer {
		t.Fatalf("page flags = older:%t newer:%t", response.HasOlder, response.HasNewer)
	}
	for _, event := range response.Events {
		if event.TerminalID != terminalID || event.TerminalOwnerID != ownerID || event.Sequence <= 0 {
			t.Fatalf("event missing canonical ownership/cursor: %+v", event)
		}
	}
}

func TestTerminalCollapseBlankRunsKeepsCodexSeparatorTail(t *testing.T) {
	input := strings.Join([]string{
		"→ tool: api-bridge.execute_shell_command({\"command\":\"update plan\"})",
		"✓ result api-bridge.execute_shell_command: updated",
		"────────────────────────────────────────────────────────────────",
		"",
		"• I checked the persisted files, not just the notification text.",
		"",
		"› i think its best to start with from scratch",
		"",
	}, "\n")

	got := collapseBlankRuns(input)
	for _, want := range []string{
		"• I checked the persisted files",
		"› i think its best to start with from scratch",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("collapseBlankRuns removed Codex tail %q\noutput:\n%s", want, got)
		}
	}
}

func TestTerminalCollapseBlankRunsKeepsPromptBoxTrailer(t *testing.T) {
	input := strings.Join([]string{
		"real terminal output",
		"────────────────────────────────────────────────────────────────",
		"›",
		"────────────────────────────────────────────────────────────────",
		"flattened cursor artifact",
	}, "\n")

	got := collapseBlankRuns(input)
	for _, want := range []string{
		"real terminal output",
		"›",
		"flattened cursor artifact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("collapseBlankRuns should keep prompt box trailer %q, got:\n%s", want, got)
		}
	}
}

func TestQueryStepTmuxLookupUsesSharedBoundedCapture(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-query-step-shared-capture"
	tmuxSession := "mlp-cursor-query-step"
	store.HandleEvent(sessionID, terminalRouteSyntheticChunkEvent(
		sessionID,
		"workflow-step:review-plan",
		"old pane content",
		1,
		map[string]interface{}{
			"kind":            "terminal",
			"tmux_session":    tmuxSession,
			"current_step_id": "review-plan",
			"step_transport":  "tmux",
			"scope":           "workflow_step",
		},
	))

	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(_ context.Context, args ...string) (string, error) {
		if got := strings.Join(args, " "); got != "capture-pane -p -e -J -t "+tmuxSession+" -S -80" {
			t.Fatalf("shared capture args = %q", got)
		}
		return "\x1b[32mcurrent pane output\x1b[0m\n\n", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	gotSession, tail, ok := makeStepTmuxLookup(api)(context.Background(), sessionID, "review-plan")
	if !ok || gotSession != tmuxSession || tail != "current pane output" {
		t.Fatalf("lookup session=%q tail=%q ok=%t", gotSession, tail, ok)
	}
	snapshots := store.List(sessionID)
	if len(snapshots) != 1 || !strings.Contains(snapshots[0].Content, "\x1b[32mcurrent pane output\x1b[0m") {
		t.Fatalf("refreshed snapshots = %#v", snapshots)
	}
}

func TestTerminalRoutesListCanReturnMetadataOnly(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-metadata"
	tmuxSession := "mlp-claude-code-exp-test"

	fullContent := strings.Repeat("╭ tool output row with enough text to be expensive if parsed repeatedly ╮\n", 20000)
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, fullContent, 7))
	store.HandleEvent(sessionID, storeevents.Event{
		Type:      "status_line",
		SessionID: sessionID,
		Timestamp: time.Now(),
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingStatusLine,
			Data: &agentevents.GenericEventData{
				Data: map[string]interface{}{
					"provider": "claude-code",
					"model":    "sonnet",
					"metadata": map[string]interface{}{
						"tmux_session": tmuxSession,
						"working_dir":  "/tmp/workflow",
						"large_blob":   strings.Repeat("provider metadata ", 20000),
						"context_window": map[string]interface{}{
							"large_nested_blob": strings.Repeat("nested ", 20000),
						},
					},
				},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/terminals?session_id="+sessionID+"&content=none", nil)
	rec := httptest.NewRecorder()
	api.handleListTerminals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response listTerminalsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1", len(response.Terminals))
	}
	if response.Terminals[0].Content != "" {
		t.Fatalf("metadata-only list content = %q, want empty", response.Terminals[0].Content)
	}
	if len(response.Terminals[0].Rows) != 0 {
		t.Fatalf("metadata-only list rows len = %d, want 0", len(response.Terminals[0].Rows))
	}
	if response.Terminals[0].ChunkIndex != 7 {
		t.Fatalf("chunk index = %d, want metadata preserved", response.Terminals[0].ChunkIndex)
	}
	if rec.Body.Len() > 4096 {
		t.Fatalf("metadata-only response is too large: %d bytes", rec.Body.Len())
	}
}

func TestTerminalRoutesPreserveStepTypeInListAndDetail(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-step-type"
	terminalID := sessionID + ":workflow-step:workflow-full-1:route-by-mode"

	store.HandleEvent(sessionID, storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     time.Now(),
		SessionID:     sessionID,
		ExecutionID:   "workflow-step:workflow-full-1:route-by-mode",
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{
						"kind":              "terminal",
						"tmux_session":      "mlp-codex-step-type",
						"current_step_id":   "route-by-mode",
						"current_step_type": "code_exec",
						"plan_step_type":    "routing",
						"execution_kind":    "workflow_step",
						"scope":             "workflow_step",
					},
				},
				Content:    "routing step running",
				ChunkIndex: 1,
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/terminals?session_id="+sessionID+"&content=none", nil)
	rec := httptest.NewRecorder()
	api.handleListTerminals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listResponse listTerminalsResponse
	if err := json.NewDecoder(rec.Body).Decode(&listResponse); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResponse.Terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1", len(listResponse.Terminals))
	}
	if listResponse.Terminals[0].StepType != "routing" {
		t.Fatalf("list step_type = %q, want routing", listResponse.Terminals[0].StepType)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID, nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec = httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var detailResponse terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&detailResponse); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detailResponse.StepType != "routing" {
		t.Fatalf("detail step_type = %q, want routing", detailResponse.StepType)
	}
}

func TestTerminalRoutesDeriveMissingStepTypeFromWorkflowPlan(t *testing.T) {
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		"Workflow/upwork/planning/plan.json": `{
			"steps": [
				{"type": "regular", "id": "check-cdp", "title": "Check CDP"},
				{"type": "routing", "id": "route-by-mode", "title": "Route by mode"}
			]
		}`,
	}})
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-step-type-plan"
	terminalID := sessionID + ":workflow-step:workflow-full-1:route-by-mode"

	store.HandleEvent(sessionID, storeevents.Event{
		Type:          "streaming_chunk",
		Timestamp:     time.Now(),
		SessionID:     sessionID,
		ExecutionID:   "workflow-step:workflow-full-1:route-by-mode",
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.StreamingChunk,
			Data: &agentevents.StreamingChunkEvent{
				BaseEventData: agentevents.BaseEventData{
					Metadata: map[string]interface{}{
						"kind":            "terminal",
						"tmux_session":    "mlp-codex-step-type-plan",
						"current_step_id": "route-by-mode",
						"execution_kind":  "workflow_step",
						"workflow_path":   "Workflow/upwork",
						"scope":           "workflow_step",
					},
				},
				Content:    "routing step running",
				ChunkIndex: 1,
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/terminals?session_id="+sessionID+"&content=none", nil)
	rec := httptest.NewRecorder()
	api.handleListTerminals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var listResponse listTerminalsResponse
	if err := json.NewDecoder(rec.Body).Decode(&listResponse); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResponse.Terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1", len(listResponse.Terminals))
	}
	if listResponse.Terminals[0].StepType != "routing" {
		t.Fatalf("list step_type = %q, want routing", listResponse.Terminals[0].StepType)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID, nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec = httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var detailResponse terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&detailResponse); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detailResponse.StepType != "routing" {
		t.Fatalf("detail step_type = %q, want routing", detailResponse.StepType)
	}
}

func TestTerminalRoutesSyntheticWorkflowSnapshotIncludesToolEvents(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-synthetic"
	ownerID := "workflow-step:workflow-full-1:check-cdp"
	metadata := map[string]interface{}{
		"kind":               "terminal",
		"execution_owner_id": ownerID,
		"execution_kind":     "workflow_step",
		"scope":              "workflow_step",
		"workflow_path":      "Workflow/upwork",
		"workflow_name":      "upwork",
		"current_step_id":    "check-cdp",
		"step_name":          "Check CDP connection",
		"step_index":         1,
		"step_total":         28,
		"step_transport":     "api",
		"provider":           "codex-cli",
	}

	store.HandleEvent(sessionID, terminalRouteSyntheticChunkEvent(
		sessionID,
		ownerID,
		"$ gemini model=auto msgs=2\n> user: Verify CDP",
		1,
		metadata,
	))
	store.HandleEvent(sessionID, terminalRouteToolStartEvent(
		sessionID,
		ownerID,
		"call-1",
		"mcp_api-bridge_execute_shell_command",
		`{"command":"env | grep MCP_API_TOKEN"}`,
		metadata,
	))
	store.HandleEvent(sessionID, terminalRouteToolEndEvent(
		sessionID,
		ownerID,
		"call-1",
		"mcp_api-bridge_execute_shell_command",
		`{"stdout":"MCP_API_TOKEN=secret-token\nMCP_AUTH=Authorization: Bearer secret-token\nCDP status check successful. Version: Chrome/148.0.7778.169"}`,
		metadata,
	))
	store.HandleEvent(sessionID, terminalRouteSyntheticChunkEvent(
		sessionID,
		ownerID,
		"$ gemini model=auto msgs=2\n> user: Verify CDP\n[done · 29.9s · 83242 in · 1240 out]",
		2,
		metadata,
	))

	req := httptest.NewRequest(http.MethodGet, "/api/terminals?session_id="+sessionID+"&content=full", nil)
	rec := httptest.NewRecorder()
	api.handleListTerminals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response listTerminalsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1", len(response.Terminals))
	}
	terminal := response.Terminals[0]
	if terminal.TerminalID != sessionID+":"+ownerID {
		t.Fatalf("terminal id = %q, want %q", terminal.TerminalID, sessionID+":"+ownerID)
	}
	if terminal.StepTransport != "api" {
		t.Fatalf("step transport = %q, want api", terminal.StepTransport)
	}
	if terminal.Status.ProviderLabel != "Codex CLI" {
		t.Fatalf("provider label = %q, want Codex CLI", terminal.Status.ProviderLabel)
	}
	if !strings.Contains(terminal.Content, "$ gemini model=auto") {
		t.Fatalf("terminal content missing Gemini command:\n%s", terminal.Content)
	}
	if !strings.Contains(terminal.Content, "→ tool: mcp_api-bridge_execute_shell_command") {
		t.Fatalf("terminal content missing tool start row:\n%s", terminal.Content)
	}
	if !strings.Contains(terminal.Content, "✓ result mcp_api-bridge_execute_shell_command") {
		t.Fatalf("terminal content missing tool result row:\n%s", terminal.Content)
	}
	if strings.Contains(terminal.Content, "secret-token") {
		t.Fatalf("terminal content leaked MCP token:\n%s", terminal.Content)
	}
	if !strings.Contains(terminal.Content, "MCP_API_TOKEN=[redacted]") {
		t.Fatalf("terminal content missing redacted token marker:\n%s", terminal.Content)
	}
	if !strings.Contains(terminal.Content, "MCP_AUTH=Authorization: Bearer [redacted]") {
		t.Fatalf("terminal content missing redacted auth marker:\n%s", terminal.Content)
	}
	if len(terminal.Rows) == 0 {
		t.Fatalf("terminal rows were empty")
	}
	var toolRow *terminals.Row
	for i := range terminal.Rows {
		if terminal.Rows[i].Kind == "tool" {
			toolRow = &terminal.Rows[i]
			break
		}
	}
	if toolRow == nil {
		t.Fatalf("terminal rows missing typed tool row: %#v", terminal.Rows)
	}
	if toolRow.Name != "mcp_api-bridge_execute_shell_command" {
		t.Fatalf("tool row name = %q", toolRow.Name)
	}
	if !strings.Contains(toolRow.Result, "MCP_API_TOKEN=[redacted]") {
		t.Fatalf("tool row result missing redacted token marker: %q", toolRow.Result)
	}
	toolIdx := strings.Index(terminal.Content, "→ tool:")
	doneIdx := strings.Index(terminal.Content, "[done")
	if toolIdx < 0 || doneIdx < 0 || toolIdx > doneIdx {
		t.Fatalf("tool rows should appear before done footer:\n%s", terminal.Content)
	}
}

func TestTerminalRoutesMetadataListReturnsMandatoryEmptyRows(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-metadata"
	store.HandleEvent(sessionID, terminalRouteSyntheticChunkEvent(
		sessionID,
		"workflow-step:check",
		"$ gemini model=auto msgs=1\n> user: hello",
		1,
		map[string]interface{}{
			"kind":               "terminal",
			"execution_owner_id": "workflow-step:check",
			"execution_kind":     "workflow_step",
			"step_transport":     "api",
		},
	))

	req := httptest.NewRequest(http.MethodGet, "/api/terminals?session_id="+sessionID+"&content=none", nil)
	rec := httptest.NewRecorder()
	api.handleListTerminals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response listTerminalsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1", len(response.Terminals))
	}
	if response.Terminals[0].Content != "" {
		t.Fatalf("content = %q, want empty", response.Terminals[0].Content)
	}
	if response.Terminals[0].Rows == nil {
		t.Fatalf("rows = nil, want mandatory empty array")
	}
	if len(response.Terminals[0].Rows) != 0 {
		t.Fatalf("rows len = %d, want 0", len(response.Terminals[0].Rows))
	}
}

func TestTerminalRoutesCompleteTerminalMarksSnapshotCompleted(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-complete"
	terminalID := sessionID + ":workflow-step:review-plan"

	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", "mlp-claude-test", "still working", 12))

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/complete", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleCompleteTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	var response terminalActionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode complete response: %v", err)
	}
	if !response.OK {
		t.Fatalf("response ok = false")
	}
	if response.Terminal.Active || response.Terminal.State != "completed" {
		t.Fatalf("terminal active/state = %v/%q, want false/completed", response.Terminal.Active, response.Terminal.State)
	}
}

func TestTerminalRoutesCompleteTerminalSignalsProviderWaitLoop(t *testing.T) {
	terminalStore := terminals.NewStore()
	sessionID := "session-terminal-complete-force"
	ownerID := "workflow-step:exec-review:review-plan"
	terminalID := sessionID + ":" + ownerID
	tmuxSession := "mlp-claude-test"
	api := &StreamingAPI{terminalStore: terminalStore}

	var forcedSession string
	oldForceComplete := forceCompleteCodingAgentTmuxSession
	forceCompleteCodingAgentTmuxSession = func(sessionName string) bool {
		forcedSession = sessionName
		return true
	}
	defer func() { forceCompleteCodingAgentTmuxSession = oldForceComplete }()

	terminalStore.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, ownerID, tmuxSession, "final answer\n❯", 12))

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/complete", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleCompleteTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	if forcedSession != tmuxSession {
		t.Fatalf("force-complete tmux session = %q, want %q", forcedSession, tmuxSession)
	}
}

func TestTerminalRoutesFailTerminalMarksSnapshotFailed(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "session-terminal-fail"
	executionID := "workflow-step:review-plan"
	terminalID := sessionID + ":workflow-step:review-plan"
	api := &StreamingAPI{
		terminalStore: store,
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			executionID: {ExecutionID: executionID, SessionID: sessionID, Status: trackedExecutionStatusRunning, StartedAt: time.Now().Add(-time.Second)},
		},
	}

	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, executionID, "mlp-claude-test", "still working", 12))

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/fail", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleFailTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fail status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	var response terminalActionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode fail response: %v", err)
	}
	if response.Terminal.Active || response.Terminal.State != "failed" {
		t.Fatalf("terminal active/state = %v/%q, want false/failed", response.Terminal.Active, response.Terminal.State)
	}
	if tracked := api.trackedWorkflowExecutions[executionID]; tracked.Status != trackedExecutionStatusFailed || tracked.LastError != "failed by operator" {
		t.Fatalf("tracked execution status/error = %q/%q, want failed/operator reason", tracked.Status, tracked.LastError)
	}
}

func TestTerminalRoutesRefreshTerminalCapturesTmuxPane(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-refresh"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-codex-cli-int-test"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "old pane", 2))

	var gotArgs []string
	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		gotArgs = append([]string(nil), args...)
		return "fresh pane", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/refresh", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleRefreshTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotArgs, " "); got != "capture-pane -p -e -J -t "+tmuxSession+" -S -2000" {
		t.Fatalf("tmux args = %q, want capture-pane", got)
	}
	var response terminalActionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if response.Terminal.Content != "fresh pane" {
		t.Fatalf("terminal content = %q, want refreshed content", response.Terminal.Content)
	}
}

func TestTerminalRoutesGetTerminalCanHistoryRefreshSelectedPane(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-deep"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-codex-cli-int-deep"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "stored pane", 2))

	var gotArgs []string
	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		gotArgs = append([]string(nil), args...)
		return "deep pane", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"?content=history&lines=12000", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotArgs, " "); got != "capture-pane -p -e -J -t "+tmuxSession+" -S -12000" {
		t.Fatalf("tmux args = %q, want deep capture", got)
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode deep response: %v", err)
	}
	if response.Content != "deep pane" {
		t.Fatalf("terminal content = %q, want deep content", response.Content)
	}
}

func TestTerminalRoutesGetTerminalHistoryIdlePaneUsesCaptureNotPipe(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "session-terminal-pipe-history"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-claude-pipe-history"
	pipeDir := t.TempDir()
	pipePath := filepath.Join(pipeDir, terminalPipeRecorderFileName(tmuxSession))
	pipeContent := "first line from pipe\nsecond line from pipe\nthird line from pipe\n"
	if err := os.WriteFile(pipePath, []byte(pipeContent), 0o600); err != nil {
		t.Fatalf("write pipe recording: %v", err)
	}
	api := &StreamingAPI{
		terminalStore: store,
		terminalPipeRecorder: &terminalPipeRecorder{
			root: pipeDir,
			sessions: map[string]*terminalPipeRecording{
				tmuxSession: {
					tmuxSession: tmuxSession,
					path:        pipePath,
					started:     true,
				},
			},
		},
	}
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "short pane", 2))
	store.HandleEvent(sessionID, terminalRouteEndEvent(sessionID, "workflow-step:review-plan", tmuxSession, 60))

	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return "short pane", nil
		}
		t.Fatalf("unexpected tmux args: %v", args)
		return "", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"?content=history", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode history response: %v", err)
	}
	// An idle/completed pane must NOT use the appended pipe recording (it
	// accumulates duplicate repaint frames replayed on a mismatched seed buffer);
	// it serves the static full-buffer capture instead. The pipe recording exists
	// on disk here only to prove it is deliberately ignored once the pane is inactive.
	if response.Content != "short pane" {
		t.Fatalf("idle terminal content = %q, want capture %q", response.Content, "short pane")
	}
	if response.ContentSource == "tmux_pipe" {
		t.Fatalf("idle pane used the pipe recording (source %q); want the static capture", response.ContentSource)
	}
}

func TestTerminalRoutesGetTerminalHistoryIdlePanePreservesLiveStream(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-stream-history"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-claude-stream-history"
	streamContent := "\x1bcfirst retained line\r\nsecond retained line\r\nfinal retained line\n"

	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "short pane", 2))
	if _, ok := store.SetDisplayContent(terminalID, streamContent, "tmux_stream"); !ok {
		t.Fatal("expected live stream transcript to be stored")
	}
	store.HandleEvent(sessionID, terminalRouteEndEvent(sessionID, "workflow-step:review-plan", tmuxSession, 60))

	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(context.Context, ...string) (string, error) {
		t.Fatal("settled history attempted capture-pane despite a retained tmux stream")
		return "", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"?content=history", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode history response: %v", err)
	}
	if response.Content != streamContent || response.ContentSource != "tmux_stream" {
		t.Fatalf("idle stream = source %q content %q, want retained stream", response.ContentSource, response.Content)
	}
}

// A retained main agent (e.g. Cursor on tmux) keeps one pane across turns. A
// live-attach transcript persisted during an earlier turn must not keep owning
// the settled scrollback after a later turn ran with no viewer attached: the
// product terminal view froze on the old turn while the pane had moved on.
func TestTerminalRoutesGetTerminalHistoryRecapturesAfterLaterRetainedTurn(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-stream-later-turn"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-cursor-cli-stream-later-turn"
	oldTranscript := "\x1bcturn one as seen by the viewer\r\n"
	latestPane := "turn one\nturn two reply\n"

	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "turn one", 2))
	if _, ok := store.SetDisplayContent(terminalID, oldTranscript, "tmux_stream"); !ok {
		t.Fatal("expected live stream transcript to be stored")
	}
	if _, ok := store.MarkTurnCompleted(terminalID); !ok {
		t.Fatal("expected first turn to settle")
	}
	// Turn two runs and settles with no live-attach viewer.
	if _, ok := store.MarkTurnRunning(terminalID); !ok {
		t.Fatal("expected second turn to start")
	}
	settled, ok := store.MarkTurnCompleted(terminalID)
	if !ok {
		t.Fatal("expected second turn to settle")
	}

	captured := false
	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(_ context.Context, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			captured = true
			return latestPane, nil
		}
		return "", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"?content=history", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode history response: %v", err)
	}
	if !captured {
		t.Fatal("settled history served the earlier turn's live-attach transcript without capturing the pane")
	}
	if !strings.Contains(response.Content, "turn two reply") || response.ContentSource == "tmux_stream" {
		t.Fatalf("history = source %q content %q, want the latest pane capture", response.ContentSource, response.Content)
	}
	if response.ChunkIndex <= settled.ChunkIndex {
		t.Fatalf("chunk_index = %d, want > %d so pollers see the new content", response.ChunkIndex, settled.ChunkIndex)
	}
}

func TestTerminalRoutesGetTerminalCapturesRunningTmuxVisibleScreen(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-visible"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-codex-cli-int-visible"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "loading\nold frame", 2))

	var gotArgs []string
	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		gotArgs = append([]string(nil), args...)
		return "current visible pane", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"?content=screen", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotArgs, " "); got != "capture-pane -p -e -J -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want visible-screen capture", got)
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode visible response: %v", err)
	}
	if response.Content != "current visible pane" {
		t.Fatalf("terminal content = %q, want current visible content", response.Content)
	}
}

func TestTerminalRoutesGetTerminalScreenIgnoresCompletedTextInRunningMainAgent(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-main-visible"
	terminalID := sessionID + ":main:" + sessionID
	tmuxSession := "mlp-claude-code-main-visible"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, tmuxSession, "sub-step\nSTATUS: COMPLETED\nold spinner", 2))

	var gotArgs []string
	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		gotArgs = append([]string(nil), args...)
		return "current main visible pane", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"?content=screen", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotArgs, " "); got != "capture-pane -p -e -J -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want visible-screen capture", got)
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode visible response: %v", err)
	}
	if response.Content != "current main visible pane" {
		t.Fatalf("terminal content = %q, want current visible main content", response.Content)
	}
	if response.ContentSource != "tmux_capture" {
		t.Fatalf("content source = %q, want tmux_capture", response.ContentSource)
	}
}

func TestTerminalRoutesGetTerminalScreenIgnoresPipeRecording(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "session-terminal-screen-pipe"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-claude-screen-pipe"
	pipeDir := t.TempDir()
	pipePath := filepath.Join(pipeDir, terminalPipeRecorderFileName(tmuxSession))
	pipeContent := strings.Repeat("mcp-agent-20260627-080010\n", 8)
	if err := os.WriteFile(pipePath, []byte(pipeContent), 0o600); err != nil {
		t.Fatalf("write pipe recording: %v", err)
	}
	api := &StreamingAPI{
		terminalStore: store,
		terminalPipeRecorder: &terminalPipeRecorder{
			root: pipeDir,
			sessions: map[string]*terminalPipeRecording{
				tmuxSession: {
					tmuxSession: tmuxSession,
					path:        pipePath,
					started:     true,
				},
			},
		},
	}
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "old pane", 2))

	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "capture-pane" {
			return "current visible pane", nil
		}
		t.Fatalf("unexpected tmux args: %v", args)
		return "", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"?content=screen", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode screen response: %v", err)
	}
	if response.Content != "current visible pane" {
		t.Fatalf("terminal content = %q, want visible pane content", response.Content)
	}
	if response.ContentSource != "tmux_capture" {
		t.Fatalf("content source = %q, want tmux_capture", response.ContentSource)
	}
}

func TestTerminalRoutesGetTerminalDebugHeadersIncludeTmuxStats(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-debug"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-claude-code-debug"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "old pane", 2))

	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		switch {
		case len(args) > 0 && args[0] == "capture-pane":
			return "fresh pane", nil
		case len(args) > 0 && args[0] == "display-message":
			return "20000\t4\t1\t23\t70\t0\t0", nil
		default:
			t.Fatalf("unexpected tmux args: %v", args)
			return "", nil
		}
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"?content=screen&debug=1", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Runloop-Terminal-Preserve-Scrollback"); got != "false" {
		t.Fatalf("preserve scrollback header = %q, want false", got)
	}
	if got := rec.Header().Get("X-Runloop-Terminal-Tmux-History-Size"); got != "4" {
		t.Fatalf("tmux history size header = %q, want 4", got)
	}
	if got := rec.Header().Get("X-Runloop-Terminal-Tmux-Alternate-On"); got != "1" {
		t.Fatalf("tmux alternate header = %q, want 1", got)
	}
	if got := rec.Header().Get("X-Runloop-Terminal-Collapsed-Lines"); got != "1" {
		t.Fatalf("collapsed lines header = %q, want 1", got)
	}
}

func TestTerminalRoutesGetTerminalStoredSkipsInactiveTmuxCapture(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-stored"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-codex-cli-int-stored"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "stored pane", 2))
	store.HandleEvent(sessionID, terminalRouteEndEvent(sessionID, "workflow-step:review-plan", tmuxSession, 60))

	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		t.Fatalf("tmux capture should not be called for content=stored, args=%v", args)
		return "", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID+"?content=stored", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode stored response: %v", err)
	}
	if response.Content != "stored pane" {
		t.Fatalf("terminal content = %q, want stored content", response.Content)
	}
}

func TestTerminalRoutesGetTerminalRecapturesCompletedActiveTmuxPane(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-completed"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-codex-cli-int-completed"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "STATUS: COMPLETED\nold plain pane", 2))

	var gotArgs []string
	oldRunOutput := runTerminalTmuxOutputCommand
	runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
		gotArgs = append([]string(nil), args...)
		return "\x1b[32mSTATUS: COMPLETED\x1b[0m\nfresh colored pane", nil
	}
	defer func() { runTerminalTmuxOutputCommand = oldRunOutput }()

	req := httptest.NewRequest(http.MethodGet, "/api/terminals/"+terminalID, nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleGetTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotArgs, " "); got != "capture-pane -p -e -J -t "+tmuxSession+" -S -1000" {
		t.Fatalf("tmux args = %q, want completed-pane capture", got)
	}
	var response terminals.Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode completed response: %v", err)
	}
	if !strings.Contains(response.Content, "\x1b[32mSTATUS: COMPLETED\x1b[0m") {
		t.Fatalf("terminal content = %q, want ANSI-preserved completed capture", response.Content)
	}
}

func TestTerminalRoutesKillTerminalKillsTmuxAndMarksFailed(t *testing.T) {
	store := terminals.NewStore()
	sessionID := "session-terminal-kill"
	executionID := "workflow-step:review-plan"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-claude-code-exp-test"
	api := &StreamingAPI{
		terminalStore: store,
		trackedWorkflowExecutions: map[string]*TrackedWorkflowExecution{
			executionID: {ExecutionID: executionID, SessionID: sessionID, Status: trackedExecutionStatusRunning, StartedAt: time.Now().Add(-time.Second)},
		},
	}
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, executionID, tmuxSession, "pane", 2))

	var gotArgs []string
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	defer func() { runTerminalTmuxCommand = oldRun }()

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/kill", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleKillTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("kill status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotArgs, " "); got != "kill-session -t "+tmuxSession {
		t.Fatalf("tmux args = %q, want kill-session", got)
	}
	var response terminalActionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode kill response: %v", err)
	}
	if response.Terminal.Active || response.Terminal.State != "failed" {
		t.Fatalf("terminal active/state = %v/%q, want false/failed", response.Terminal.Active, response.Terminal.State)
	}
	if tracked := api.trackedWorkflowExecutions[executionID]; tracked.Status != trackedExecutionStatusFailed || tracked.LastError != "killed by operator" {
		t.Fatalf("tracked execution status/error = %q/%q, want failed/operator reason", tracked.Status, tracked.LastError)
	}
}

func TestTerminalRoutesKillTerminalClosesCompletedLiveProcessWithoutChangingOutcome(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-kill-completed"
	terminalID := sessionID + ":main:" + sessionID
	tmuxSession := "mlp-claude-code-exp-gone"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "main:"+sessionID, tmuxSession, "pane", 2))
	store.MarkCompleted(terminalID)

	var called bool
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		called = true
		return errors.New("tmux kill-session -t mlp-claude-code-exp-gone: exit status 1: can't find pane")
	}
	defer func() { runTerminalTmuxCommand = oldRun }()

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/kill", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleKillTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("kill completed status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatalf("completed terminal with a live process should call tmux kill")
	}
	var response terminalActionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode kill response: %v", err)
	}
	if response.Terminal.Active || response.Terminal.State != "completed" {
		t.Fatalf("terminal active/state = %v/%q, want false/completed", response.Terminal.Active, response.Terminal.State)
	}
	if response.Terminal.ProcessState != "closed" || response.Terminal.TmuxSession != "" {
		t.Fatalf("terminal process/tmux = %q/%q, want closed/empty", response.Terminal.ProcessState, response.Terminal.TmuxSession)
	}
}

func TestTerminalRoutesKillTerminalTreatsMissingTmuxAsStopped(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-kill-missing"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-claude-code-exp-missing"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "pane", 2))

	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		return errors.New("tmux kill-session -t mlp-claude-code-exp-missing: exit status 1: can't find pane")
	}
	defer func() { runTerminalTmuxCommand = oldRun }()

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/kill", nil)
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleKillTerminal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("kill missing status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response terminalActionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode kill response: %v", err)
	}
	if response.Terminal.Active || response.Terminal.State != "failed" {
		t.Fatalf("terminal active/state = %v/%q, want false/failed", response.Terminal.Active, response.Terminal.State)
	}
}

func TestTerminalRoutesSendInputUsesTmuxPasteAndOptionalEnter(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-input"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-codex-cli-int-test"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "pane", 2))

	type call struct {
		stdin string
		args  []string
	}
	var calls []call
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		calls = append(calls, call{stdin: stdin, args: append([]string(nil), args...)})
		return nil
	}
	defer func() { runTerminalTmuxCommand = oldRun }()

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/input", strings.NewReader(`{"text":"debug message","submit":true}`))
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleSendTerminalInput(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("send input status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if len(calls) != 3 {
		t.Fatalf("tmux call count = %d, want 3: %#v", len(calls), calls)
	}
	if calls[0].stdin != "debug message" || len(calls[0].args) < 3 || calls[0].args[0] != "load-buffer" {
		t.Fatalf("first tmux call = %#v, want load-buffer with stdin", calls[0])
	}
	if calls[1].args[0] != "paste-buffer" || !containsString(calls[1].args, tmuxSession) {
		t.Fatalf("second tmux call = %#v, want paste-buffer into session", calls[1])
	}
	if !containsString(calls[1].args, "-p") {
		t.Fatalf("second tmux call = %#v, want bracketed paste for non-Cursor terminal", calls[1])
	}
	if got := strings.Join(calls[2].args, " "); got != "send-keys -t "+tmuxSession+" Enter" {
		t.Fatalf("third tmux call = %q, want enter", got)
	}
}

func TestTerminalRoutesSendInputAvoidsBracketedPasteForCursor(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-cursor-input"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-cursor-cli-int-test"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "Cursor Agent\n→ Add a follow-up", 2))

	type call struct {
		stdin string
		args  []string
	}
	var calls []call
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		calls = append(calls, call{stdin: stdin, args: append([]string(nil), args...)})
		return nil
	}
	defer func() { runTerminalTmuxCommand = oldRun }()

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/input", strings.NewReader(`{"text":"line one\nline two"}`))
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleSendTerminalInput(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("send input status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if len(calls) != 2 {
		t.Fatalf("tmux call count = %d, want 2: %#v", len(calls), calls)
	}
	if calls[1].args[0] != "paste-buffer" || !containsString(calls[1].args, "-r") || !containsString(calls[1].args, tmuxSession) {
		t.Fatalf("second tmux call = %#v, want raw paste into Cursor session", calls[1])
	}
	if containsString(calls[1].args, "-p") {
		t.Fatalf("second tmux call = %#v, must not use bracketed paste for Cursor", calls[1])
	}
}

func TestPasteTerminalTextAvoidsBracketedPasteForCursorTmuxName(t *testing.T) {
	tmuxSession := "mlp-cursor-cli-int-direct"
	var calls [][]string
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	defer func() { runTerminalTmuxCommand = oldRun }()

	if err := pasteTerminalText(context.Background(), tmuxSession, "direct paste"); err != nil {
		t.Fatalf("pasteTerminalText: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("tmux call count = %d, want 2: %#v", len(calls), calls)
	}
	if calls[1][0] != "paste-buffer" || containsString(calls[1], "-p") {
		t.Fatalf("paste args = %#v, want non-bracketed paste for Cursor tmux name", calls[1])
	}
}

func TestTerminalRoutesSendInputAllowsWhitespaceKeystroke(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-space-input"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-pi-cli-int-test"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "pane", 2))

	type call struct {
		stdin string
		args  []string
	}
	var calls []call
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		calls = append(calls, call{stdin: stdin, args: append([]string(nil), args...)})
		return nil
	}
	defer func() { runTerminalTmuxCommand = oldRun }()

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/input", strings.NewReader(`{"text":" ","submit":false}`))
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleSendTerminalInput(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("send space input status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if len(calls) != 2 {
		t.Fatalf("tmux call count = %d, want 2: %#v", len(calls), calls)
	}
	if calls[0].stdin != " " || calls[0].args[0] != "load-buffer" {
		t.Fatalf("first tmux call = %#v, want load-buffer with single-space stdin", calls[0])
	}
	if calls[1].args[0] != "paste-buffer" || !containsString(calls[1].args, tmuxSession) {
		t.Fatalf("second tmux call = %#v, want paste-buffer into session", calls[1])
	}
}

func TestTerminalRoutesSendEscKeyUsesTmuxSendKeys(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-esc"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-claude-code-exp-test"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "pane", 2))

	var gotArgs []string
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	defer func() { runTerminalTmuxCommand = oldRun }()

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/key", strings.NewReader(`{"key":"esc"}`))
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleSendTerminalKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("send key status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotArgs, " "); got != "send-keys -t "+tmuxSession+" Escape" {
		t.Fatalf("tmux args = %q, want escape", got)
	}
}

func TestTerminalRoutesSendCtrlOKeyUsesTmuxSendKeys(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-ctrl-o"
	terminalID := sessionID + ":workflow-step:review-plan"
	tmuxSession := "mlp-codex-cli-int-test"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, "workflow-step:review-plan", tmuxSession, "pane", 2))

	var gotArgs []string
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	defer func() { runTerminalTmuxCommand = oldRun }()

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/key", strings.NewReader(`{"key":"ctrl-o"}`))
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleSendTerminalKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("send key status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotArgs, " "); got != "send-keys -t "+tmuxSession+" C-o" {
		t.Fatalf("tmux args = %q, want ctrl-o", got)
	}
}

func TestTerminalRoutesSendDownBypassesStartupReadinessForCompletedLivePane(t *testing.T) {
	store := terminals.NewStore()
	api := &StreamingAPI{terminalStore: store}
	sessionID := "session-terminal-completed-live"
	executionID := "main:" + sessionID
	terminalID := sessionID + ":" + executionID
	tmuxSession := "mlp-codex-cli-int-completed-live"
	store.HandleEvent(sessionID, terminalRouteChunkEvent(sessionID, executionID, tmuxSession, "Codex prompt", 1))
	store.MarkCompleted(terminalID)

	snapshot, ok := store.Get(terminalID)
	if !ok || snapshot.State != "completed" || snapshot.TmuxSession == "" {
		t.Fatalf("fixture state = %#v, want completed terminal with live tmux", snapshot)
	}

	// Reproduce the retained startup gate that used to swallow normal-priority
	// debug keys (Down/Up/Tab/Enter) until the HTTP request timed out.
	tmuxinput.MarkStarting(tmuxSession)
	t.Cleanup(func() { tmuxinput.RemoveReadiness(tmuxSession) })

	var gotArgs []string
	oldRun := runTerminalTmuxCommand
	runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
		gotArgs = append([]string(nil), args...)
		return nil
	}
	t.Cleanup(func() { runTerminalTmuxCommand = oldRun })

	req := httptest.NewRequest(http.MethodPost, "/api/terminals/"+terminalID+"/key", strings.NewReader(`{"key":"down"}`))
	req = mux.SetURLVars(req, map[string]string{"terminal_id": terminalID})
	rec := httptest.NewRecorder()
	api.handleSendTerminalKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("send Down status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := strings.Join(gotArgs, " "); got != "send-keys -t "+tmuxSession+" Down" {
		t.Fatalf("tmux args = %q, want Down delivered to completed live pane", got)
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func terminalRouteList(t *testing.T, api *StreamingAPI, sessionID string) listTerminalsResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/terminals?session_id="+sessionID, nil)
	rec := httptest.NewRecorder()
	api.handleListTerminals(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var response listTerminalsResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	return response
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

func terminalRouteSyntheticChunkEvent(sessionID, executionID, content string, chunkIndex int, metadata map[string]interface{}) storeevents.Event {
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
					Metadata: cloneTerminalRouteMetadata(metadata),
				},
				Content:    content,
				ChunkIndex: chunkIndex,
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

func terminalRouteToolEndEvent(sessionID, executionID, toolCallID, toolName, result string, metadata map[string]interface{}) storeevents.Event {
	return storeevents.Event{
		Type:          string(agentevents.ToolCallEnd),
		Timestamp:     time.Now(),
		SessionID:     sessionID,
		ExecutionID:   executionID,
		ExecutionKind: "workflow_step",
		Data: &agentevents.AgentEvent{
			Type: agentevents.ToolCallEnd,
			Data: &agentevents.ToolCallEndEvent{
				BaseEventData: agentevents.BaseEventData{Metadata: cloneTerminalRouteMetadata(metadata)},
				ToolCallID:    toolCallID,
				ToolName:      toolName,
				Result:        result,
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

func TestCompactTerminalStatusMetaForListKeepsPlanUsageLists(t *testing.T) {
	meta := map[string]interface{}{
		"status_extras": []interface{}{"5h 17% →3:30pm", "ctx 42%"},
		"rate_limit_windows": []llmtypes.RateLimitWindow{
			{Name: "five_hour", UsedPercent: 17, ResetsAt: time.Date(2026, 9, 27, 15, 30, 0, 0, time.UTC)},
		},
		"nested":      map[string]interface{}{"a": 1},
		"total_cost":  1.5,
		"huge_extras": []interface{}{strings.Repeat("x", 4096)},
	}
	for i := 0; i < 20; i++ {
		meta[fmt.Sprintf("a%02d", i)] = i
	}
	out := compactTerminalStatusMetaForList(meta)
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	extras, ok := decoded["status_extras"].([]interface{})
	if !ok || len(extras) != 2 || extras[0] != "5h 17% →3:30pm" {
		t.Fatalf("status_extras not kept: %s", encoded)
	}
	windows, ok := decoded["rate_limit_windows"].([]interface{})
	if !ok || len(windows) != 1 {
		t.Fatalf("rate_limit_windows not kept: %s", encoded)
	}
	window := windows[0].(map[string]interface{})
	if window["name"] != "five_hour" || window["used_percent"] != float64(17) || window["resets_at"] != "2026-09-27T15:30:00Z" {
		t.Fatalf("unexpected window: %#v", window)
	}
	if _, ok := decoded["nested"]; ok {
		t.Fatalf("non-allowlisted structured values must still be dropped: %s", encoded)
	}
	if _, ok := decoded["huge_extras"]; ok {
		t.Fatalf("huge list must be dropped: %s", encoded)
	}
	if len(decoded) != 12 {
		t.Fatalf("entry cap not honored: %d entries", len(decoded))
	}
}

func TestCompactTerminalStatusMetaForListDropsOversizedUsageList(t *testing.T) {
	out := compactTerminalStatusMetaForList(map[string]interface{}{
		"status_extras": []interface{}{strings.Repeat("x", 4096)},
	})
	if out != nil {
		t.Fatalf("oversized status_extras should be dropped, got %#v", out)
	}
}
