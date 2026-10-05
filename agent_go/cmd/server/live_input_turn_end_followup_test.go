package server

import (
	"context"
	"testing"
	"time"

	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

// Excellence 2026-09-29 06:27: live input reached the Muse CLI as the running
// Session.Run completed. The Run's completion settled the input's retained
// turn, so the CLI's later reply had no owner and the chat went idle. A
// completion marked live_input_followup must keep the input's turn open until
// the Session's follow-up completion arrives.
func TestLiveInputFollowupCompletionKeepsInputTurnOpen(t *testing.T) {
	const sessionID = "live-input-turn-end-followup"
	const tmuxSession = "mlp-muse-int-turn-end"
	terminalID := sessionID + ":main:" + sessionID
	terminalStore := terminals.NewStore()
	terminalStore.HandleEvent(sessionID, codingAgentTmuxReaperChunkEvent(time.Now(), sessionID, "main:"+sessionID, tmuxSession))
	if _, ok := terminalStore.MarkTurnCompleted(terminalID); !ok {
		t.Fatal("could not prepare retained main terminal")
	}
	eventStore := internalevents.NewEventStore(100)
	defer eventStore.Stop()
	api := &StreamingAPI{
		eventStore:                   eventStore,
		terminalStore:                terminalStore,
		activeSessions:               map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, Status: "running"}},
		retainedMainTurns:            make(map[string]time.Time),
		retainedMainTurnWatchCancels: make(map[string]context.CancelFunc),
	}
	eventStore.SetEventAddedCallback(func(ownerSessionID string, event internalevents.Event) {
		terminalStore.HandleEventWithChange(ownerSessionID, event)
		api.observeRetainedMainTurnEvent(ownerSessionID, event)
	})
	add := func(id, final string, meta map[string]interface{}) {
		now := time.Now()
		completion := unifiedevents.NewUnifiedCompletionEvent("coding_agent", "retained", "q", final, "completed", time.Second, 1)
		completion.SessionID = sessionID
		completion.Metadata["source"] = "mcpagent_session"
		for k, v := range meta {
			completion.Metadata[k] = v
		}
		eventStore.AddEvent(sessionID, internalevents.Event{
			ID: id, Type: "unified_completion", Timestamp: now, SessionID: sessionID,
			ExecutionKind: "main_agent", TerminalOwnerID: "main:" + sessionID,
			Data: &unifiedevents.AgentEvent{Type: unifiedevents.EventType("unified_completion"), Timestamp: now, SessionID: sessionID, Data: completion},
		})
	}

	api.markMCPAgentSessionTurnRunning(sessionID, "live-turn:tables")
	time.Sleep(5 * time.Millisecond)
	add("run-completion", "Supabase MCP works.", map[string]interface{}{mcpagent.LiveInputFollowupMetadataKey: true})
	if !api.isSessionBusy(sessionID) || !api.retainedMainTurnTracked(sessionID) {
		t.Fatal("the running turn's completion settled the live input's turn")
	}

	add("followup-completion", "The project is paused; no tables.", nil)
	if api.isSessionBusy(sessionID) || api.retainedMainTurnTracked(sessionID) {
		t.Fatal("the follow-up completion did not settle the live input's turn")
	}
}
