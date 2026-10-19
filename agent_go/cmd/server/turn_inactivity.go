package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

const turnQuietAfter = 2 * time.Minute
const turnInactiveAfter = 10 * time.Minute

var turnInactivityCheckInterval = 15 * time.Second

var errTurnInactive = errors.New("Turn ended after 10 minutes without agent output or tool activity.")

type idleTool struct {
	name string
	open bool
}
type turnInactivityState struct {
	sessionID, executionID  string
	startedAt, lastOutputAt time.Time
	tools                   map[string]idleTool
	waiting, ending         bool
	quietMinute             int
	warnedTools             bool
}

// This is turn activity, not session activity. Human input, browser heartbeats,
// terminal repaints and background results waiting for the foreground do not
// reset this clock. Tools with no end event are conservatively protected.
func (state *turnInactivityState) observe(event events.Event, now time.Time) bool {
	if state.ending || (!event.Timestamp.IsZero() && event.Timestamp.Before(state.startedAt)) {
		return false
	}
	owner := events.ResolveTerminalOwnerID(state.sessionID, event, nil)
	if owner != "" && owner != "main:"+state.sessionID && owner != state.executionID {
		return false
	}
	if kind := strings.ToLower(event.ExecutionKind); kind != "" && kind != "main_agent" && kind != "main" && kind != "chat" {
		return false
	}
	progress := false
	if event.Data != nil {
		if tool, ok := unifiedevents.ToolCallRecordFromEvent(event.Data.Data); ok {
			key := tool.ToolCallID
			if key == "" {
				key = "unnamed:" + tool.ToolName
			}
			old, known := state.tools[key]
			if tool.Status == "running" {
				if !known {
					state.tools[key] = idleTool{name: tool.ToolName, open: true}
				}
			} else {
				state.tools[key] = idleTool{name: tool.ToolName, open: false}
			}
			// A replay of an already closed start is not a running tool.
			progress = !known || old.open || tool.Status != "running"
			state.waiting = false
		}
		if chunk, ok := event.Data.Data.(*unifiedevents.StreamingChunkEvent); ok && chunk != nil {
			progress = chunk.Source != "terminal" && strings.TrimSpace(chunk.Content) != "" && chunk.Metadata["kind"] != "terminal"
		}
	}
	switch event.Type {
	case "conversation_thinking", "llm_generation_end":
		progress = true
	case "request_human_feedback", "blocking_human_feedback", "coding_agent_question":
		state.waiting = true
		progress = true
	case "human_feedback_resolved":
		state.waiting = false
		progress = true
	}
	if !progress {
		return false
	}
	at := event.Timestamp
	if at.IsZero() || at.After(now) {
		at = now
	}
	if at.After(state.lastOutputAt) {
		state.lastOutputAt = at
	}
	wasQuiet := state.quietMinute > 0
	state.quietMinute = 0
	state.warnedTools = false
	return wasQuiet
}

func (state *turnInactivityState) openToolNames() []string {
	var names []string
	for _, tool := range state.tools {
		if tool.open {
			names = append(names, tool.name)
		}
	}
	sort.Strings(names)
	return names
}

func (api *StreamingAPI) observeTurnInactivity(sessionID string, event events.Event) {
	api.turnInactivityMu.Lock()
	var resumed []*turnInactivityState
	for _, state := range api.turnInactivity[sessionID] {
		if state.observe(event, time.Now()) {
			copied := *state
			resumed = append(resumed, &copied)
		}
	}
	api.turnInactivityMu.Unlock()
	for _, state := range resumed {
		api.emitTurnQuietPhase(state, false, 0)
	}
}

// The caller owns cancellation/settlement and supplies its exact turn context.
// An older watcher can never cancel the session's next turn. A false onIdle
// means ownership changed or native interruption failed; it remains protected.
func (api *StreamingAPI) watchTurnInactivity(ctx context.Context, sessionID, executionID string, startedAt time.Time, eligible func() bool, onIdle func(error) bool) func() {
	state := &turnInactivityState{sessionID: sessionID, executionID: executionID, startedAt: startedAt, lastOutputAt: startedAt, tools: map[string]idleTool{}}
	watchKey := fmt.Sprintf("%s:%p", executionID, state)
	api.turnInactivityMu.Lock()
	if api.turnInactivity == nil {
		api.turnInactivity = map[string]map[string]*turnInactivityState{}
	}
	if api.turnInactivity[sessionID] == nil {
		api.turnInactivity[sessionID] = map[string]*turnInactivityState{}
	}
	// A live input shares the CLI's current progress and in-flight tools.
	for _, previous := range api.turnInactivity[sessionID] {
		if previous.ending {
			continue
		}
		if previous.lastOutputAt.After(state.lastOutputAt) {
			state.lastOutputAt = previous.lastOutputAt
		}
		for id, tool := range previous.tools {
			state.tools[id] = tool
		}
		state.waiting = state.waiting || previous.waiting
	}
	api.turnInactivity[sessionID][watchKey] = state
	api.turnInactivityMu.Unlock()
	watchCtx, cancel := context.WithCancel(ctx)
	stop := func() {
		cancel()
		api.turnInactivityMu.Lock()
		if api.turnInactivity[sessionID][watchKey] == state {
			delete(api.turnInactivity[sessionID], watchKey)
			if len(api.turnInactivity[sessionID]) == 0 {
				delete(api.turnInactivity, sessionID)
			}
		}
		api.turnInactivityMu.Unlock()
	}
	interval := turnInactivityCheckInterval
	go func() {
		defer stop()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case now := <-ticker.C:
				if eligible != nil && !eligible() {
					continue
				}
				api.turnInactivityMu.Lock()
				current := api.turnInactivity[sessionID][watchKey] == state
				idle := now.Sub(state.lastOutputAt)
				tools := state.openToolNames()
				if api.eventStore != nil {
					tools = append(tools, api.eventStore.OpenMainToolNames(sessionID)...)
					sort.Strings(tools)
				}
				timeout := current && !state.ending && !state.waiting && len(tools) == 0 && idle >= turnInactiveAfter
				if timeout {
					state.ending = true
				}
				warn := current && len(tools) > 0 && idle >= turnInactiveAfter && !state.warnedTools
				if warn {
					state.warnedTools = true
				}
				quietMinute := int(idle / time.Minute)
				quiet := current && !state.waiting && len(tools) == 0 && idle >= turnQuietAfter && quietMinute != state.quietMinute
				if quiet {
					state.quietMinute = quietMinute
				}
				copied := *state
				api.turnInactivityMu.Unlock()
				if !current {
					return
				}
				if warn {
					log.Printf("[TURN IDLE] protected in-flight tools session=%s turn=%s idle=%s tools=%q", sessionID, executionID, idle.Round(time.Second), strings.Join(tools, ", "))
				}
				if timeout {
					if watchCtx.Err() != nil {
						return
					}
					if onIdle(errTurnInactive) {
						log.Printf("[TURN IDLE] ended session=%s turn=%s idle=%s", sessionID, executionID, idle.Round(time.Second))
						return
					}
					api.turnInactivityMu.Lock()
					state.ending = false
					api.turnInactivityMu.Unlock()
				} else if quiet {
					api.emitTurnQuietPhase(&copied, true, quietMinute)
				}
			}
		}
	}()
	return stop
}

func (api *StreamingAPI) emitTurnQuietPhase(state *turnInactivityState, quiet bool, minutes int) {
	if api.eventStore == nil {
		return
	}
	phase, text := "running", ""
	if quiet {
		phase, text = "quiet", fmt.Sprintf("Still working · no update for %d min", minutes)
	}
	now := time.Now()
	data := &unifiedevents.GenericEventData{BaseEventData: unifiedevents.BaseEventData{Timestamp: now}, Data: map[string]interface{}{"phase": phase, "text": text, "since": state.startedAt.UTC().Format(time.RFC3339Nano), "last_output_at": state.lastOutputAt.UTC().Format(time.RFC3339Nano)}}
	api.eventStore.AddEvent(state.sessionID, events.Event{ID: fmt.Sprintf("turn-quiet-%d", now.UnixNano()), Type: "turn_phase", Timestamp: now, SessionID: state.sessionID, ExecutionID: state.executionID, ExecutionKind: "main_agent", Data: unifiedevents.NewAgentEvent(data)})
}

func (api *StreamingAPI) emitIdleTurnFailure(sessionID, executionID string) {
	if api.eventStore == nil {
		return
	}
	now := time.Now()
	completion := unifiedevents.NewUnifiedCompletionEventWithError("server", "", "", errTurnInactive.Error(), turnInactiveAfter, 0)
	completion.Metadata["source"] = "turn_idle_timeout"
	api.eventStore.AddEvent(sessionID, events.Event{ID: fmt.Sprintf("turn-idle-end-%d", now.UnixNano()), Type: "unified_completion", Timestamp: now, SessionID: sessionID, ExecutionID: executionID, ExecutionKind: "main_agent", Data: unifiedevents.NewAgentEvent(completion)})
}

func (api *StreamingAPI) watchRetainedTurnInactivity(ctx context.Context, sessionID string, snapshot terminals.Snapshot, startedAt time.Time) func() {
	api.retainedMainTurnsMu.Lock()
	executionID := api.retainedMainTurnExecutionIDs[sessionID]
	api.retainedMainTurnsMu.Unlock()
	return api.watchTurnInactivity(ctx, sessionID, executionID, startedAt,
		func() bool { return !api.hasActiveTurnCancel(sessionID) },
		func(reason error) bool {
			// Retained input bypasses the foreground lane. Hold the lane while
			// interrupting it, so queued work cannot start between interruption
			// and recording this exact turn's failure.
			release := api.lockSessionInputLane(sessionID)
			defer release()
			api.retainedMainTurnsMu.Lock()
			current := api.retainedMainTurns[sessionID].Equal(startedAt) && api.retainedMainTurnExecutionIDs[sessionID] == executionID
			_, nativeOwned := api.retainedMainTurnCanonicalIDs[executionID]
			api.retainedMainTurnsMu.Unlock()
			if !current || ctx.Err() != nil || api.hasActiveTurnCancel(sessionID) {
				return false
			}
			provider := retainedCodingAgentProvider(snapshot)
			if provider == "" {
				return false
			}
			var acknowledge func()
			if native, ok := mcpagent.LookupSession(sessionID); ok && native != nil {
				acknowledge = native.PrepareNativeInterrupt()
			}
			interruptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := deliverCLIPaneInterrupt(interruptCtx, llmproviders.Provider(provider), sessionID)
			cancel()
			if err != nil {
				log.Printf("[TURN IDLE] could not interrupt session=%s turn=%s; preserving turn ownership: %v", sessionID, executionID, err)
				return false
			}
			if nativeOwned {
				api.failMCPAgentSessionTurn(sessionID, snapshot, startedAt, reason.Error(), false)
			} else {
				api.emitRetainedMainTurnStreamCompletion(sessionID, snapshot, llmproviders.Provider(provider), "failed", reason.Error())
			}
			if acknowledge != nil {
				acknowledge()
			}
			return true
		})
}
