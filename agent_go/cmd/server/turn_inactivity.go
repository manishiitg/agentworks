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

var errTurnInactive = errors.New("Reply ended after 10 minutes without agent output or tool activity.")

type idleTool struct {
	name string
	open bool
}
type turnInactivityState struct {
	sessionID, executionID  string
	startedAt, lastOutputAt time.Time
	turnStartedAt           time.Time
	lastAnnouncedActivity   time.Time
	tools                   map[string]idleTool
	waiting, ending         bool
	quietMinute             int
	warnedTools             bool
	context                 context.Context
	eligible                func() bool
	activityOwners          map[string]bool
}

// This is turn activity, not session activity. Human input, browser heartbeats,
// terminal repaints and background results waiting for the foreground do not
// reset this clock. Tools with no end event are conservatively protected.
func (state *turnInactivityState) observe(event events.Event, now time.Time) bool {
	if !event.Timestamp.IsZero() && event.Timestamp.Before(state.startedAt) {
		return false
	}
	owner := events.ResolveTerminalOwnerID(state.sessionID, event, nil)
	if owner != "" && owner != "main:"+state.sessionID && owner != state.executionID && !state.activityOwners[owner] {
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
	var updates []*turnInactivityState
	for _, state := range api.turnInactivity[sessionID] {
		if state.observe(event, time.Now()) {
			state.lastAnnouncedActivity = state.lastOutputAt
			copied := *state
			updates = append(updates, &copied)
		}
	}
	api.turnInactivityMu.Unlock()
	for _, state := range updates {
		api.emitTurnActivityState(state)
	}
}

// The caller owns cancellation/settlement and supplies its exact turn context.
// An older watcher can never cancel the session's next turn. A false onIdle
// means ownership changed or native interruption failed; it remains protected.
func (api *StreamingAPI) watchTurnInactivity(ctx context.Context, sessionID, executionID string, startedAt time.Time, eligible func() bool, onIdle func(error) bool) func() {
	return api.watchTurnInactivityMode(ctx, sessionID, executionID, startedAt, eligible, onIdle, false)
}

// Only a retained live-input watcher may share the still-live root's progress.
// A fresh foreground/synthetic query always gets its own acceptance grace.
func (api *StreamingAPI) watchTurnInactivityMode(ctx context.Context, sessionID, executionID string, startedAt time.Time, eligible func() bool, onIdle func(error) bool, shareProgress bool) func() {
	candidates := map[string]*turnInactivityState{}
	if shareProgress {
		api.turnInactivityMu.Lock()
		for key, previous := range api.turnInactivity[sessionID] {
			candidates[key] = previous
		}
		api.turnInactivityMu.Unlock()
		// Eligibility can acquire other server locks; never call it under
		// turnInactivityMu. Revalidate membership and activity below.
		for key, previous := range candidates {
			if previous.context.Err() != nil || (previous.eligible != nil && !previous.eligible()) {
				delete(candidates, key)
			}
		}
	}
	state := &turnInactivityState{sessionID: sessionID, executionID: executionID, startedAt: startedAt, turnStartedAt: startedAt, lastOutputAt: startedAt, tools: map[string]idleTool{}, context: ctx, eligible: eligible, activityOwners: map[string]bool{executionID: true}}
	watchKey := fmt.Sprintf("%s:%p", executionID, state)
	api.turnInactivityMu.Lock()
	if api.turnInactivity == nil {
		api.turnInactivity = map[string]map[string]*turnInactivityState{}
	}
	if api.turnInactivity[sessionID] == nil {
		api.turnInactivity[sessionID] = map[string]*turnInactivityState{}
	}
	// A live input shares the CLI's current progress and in-flight tools.
	sharedProgress := false
	for key, previous := range candidates {
		if api.turnInactivity[sessionID][key] != previous || previous.ending || time.Since(previous.lastOutputAt) >= turnInactiveAfter || previous.context.Err() != nil {
			continue
		}
		// A new human steer shares this CLI's actual last progress, even when
		// that progress predates admission. Admission is not agent activity.
		if !sharedProgress || previous.lastOutputAt.After(state.lastOutputAt) {
			state.lastOutputAt = previous.lastOutputAt
		}
		sharedProgress = true
		// The CLI may attribute steered output to either accepted execution.
		// Share only these proven live aliases, keeping child/other owners out.
		for owner := range previous.activityOwners {
			state.activityOwners[owner] = true
		}
		previous.activityOwners[executionID] = true
		origin := previous.turnStartedAt
		if origin.IsZero() {
			origin = previous.startedAt
		}
		if origin.Before(state.turnStartedAt) {
			state.turnStartedAt = origin
		}
		for id, tool := range previous.tools {
			state.tools[id] = tool
		}
		state.waiting = state.waiting || previous.waiting
		if previous.quietMinute > state.quietMinute {
			state.quietMinute = previous.quietMinute
		}
	}
	api.turnInactivity[sessionID][watchKey] = state
	state.lastAnnouncedActivity = state.lastOutputAt
	initialActivity := *state
	api.turnInactivityMu.Unlock()
	api.emitTurnActivityState(&initialActivity)
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
				activityChanged := current && !state.lastOutputAt.Equal(state.lastAnnouncedActivity)
				if activityChanged {
					state.lastAnnouncedActivity = state.lastOutputAt
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
					// Re-check progress after claiming the deadline, before taking
					// action. A tool/output event racing the timer wins.
					api.turnInactivityMu.Lock()
					stillQuiet := api.turnInactivity[sessionID][watchKey] == state && !state.waiting && len(state.openToolNames()) == 0 && time.Since(state.lastOutputAt) >= turnInactiveAfter
					if api.eventStore != nil && len(api.eventStore.OpenMainToolNames(sessionID)) > 0 {
						stillQuiet = false
					}
					api.turnInactivityMu.Unlock()
					if stillQuiet && onIdle(errTurnInactive) {
						log.Printf("[TURN IDLE] ended session=%s turn=%s idle=%s", sessionID, executionID, idle.Round(time.Second))
						return
					}
					api.turnInactivityMu.Lock()
					state.ending = false
					api.turnInactivityMu.Unlock()
				} else if quiet {
					api.emitTurnQuietPhase(&copied, true, quietMinute)
				} else if activityChanged {
					api.emitTurnActivityState(&copied)
				}
			}
		}
	}()
	return stop
}

func (api *StreamingAPI) emitTurnQuietPhase(state *turnInactivityState, quiet bool, minutes int) {
	if quiet {
		state.quietMinute = minutes
	} else {
		state.quietMinute = 0
	}
	api.emitTurnActivityState(state)
}

func (api *StreamingAPI) emitTurnActivityState(state *turnInactivityState) {
	if api.eventStore == nil {
		return
	}
	phase, text, since := api.eventStore.TurnPhaseSnapshot(state.sessionID)
	if phase == "" {
		phase, text, since = "starting", "Starting the agent", state.startedAt
	}
	if state.waiting {
		phase, text = "waiting_for_you", "Waiting for your answer"
	} else if state.quietMinute > 0 {
		phase, text = "quiet", fmt.Sprintf("Still working · no update for %d min", state.quietMinute)
	}
	origin := state.turnStartedAt
	if origin.IsZero() {
		origin = state.startedAt
	}
	now := time.Now()
	data := &unifiedevents.GenericEventData{BaseEventData: unifiedevents.BaseEventData{Timestamp: now}, Data: map[string]interface{}{
		"phase": phase, "text": text, "since": since.UTC().Format(time.RFC3339Nano),
		"turn_started_at":  origin.UTC().Format(time.RFC3339Nano),
		"last_activity_at": state.lastOutputAt.UTC().Format(time.RFC3339Nano),
		"last_output_at":   state.lastOutputAt.UTC().Format(time.RFC3339Nano),
	}}
	api.eventStore.AddEvent(state.sessionID, events.Event{ID: fmt.Sprintf("turn-activity-%d", now.UnixNano()), Type: "turn_phase", Timestamp: now, SessionID: state.sessionID, ExecutionID: state.executionID, ExecutionKind: "main_agent", Data: unifiedevents.NewAgentEvent(data)})
}

func (api *StreamingAPI) emitIdleTurnFailure(sessionID, executionID string) {
	if api.eventStore == nil {
		return
	}
	now := time.Now()
	completion := unifiedevents.NewUnifiedCompletionEventWithError("server", "", "", errTurnInactive.Error(), turnInactiveAfter, 0)
	completion.SessionID = sessionID
	completion.Metadata["source"] = "turn_idle_timeout"
	agentEvent := unifiedevents.NewAgentEvent(completion)
	agentEvent.SessionID = sessionID
	api.eventStore.AddEvent(sessionID, events.Event{ID: fmt.Sprintf("turn-idle-end-%d", now.UnixNano()), Type: "unified_completion", Timestamp: now, SessionID: sessionID, ExecutionID: executionID, ExecutionKind: "main_agent", Data: agentEvent})
}

func (api *StreamingAPI) watchRetainedTurnInactivity(ctx context.Context, sessionID string, snapshot terminals.Snapshot, startedAt time.Time) func() {
	api.retainedMainTurnsMu.Lock()
	executionID := api.retainedMainTurnExecutionIDs[sessionID]
	api.retainedMainTurnsMu.Unlock()
	return api.watchTurnInactivityMode(ctx, sessionID, executionID, startedAt,
		func() bool { return !api.hasActiveTurnCancel(sessionID) },
		func(reason error) bool {
			// Retained input bypasses the foreground lane. Hold the lane while
			// interrupting it, so queued work cannot start between interruption
			// and recording this exact turn's failure.
			release, acquired := api.tryLockSessionInputLane(sessionID, 0)
			if !acquired {
				return false
			}
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
			// Live input bypasses the turn lane: re-check the generation and
			// foreground owner immediately before delivering the native key.
			api.retainedMainTurnsMu.Lock()
			current = api.retainedMainTurns[sessionID].Equal(startedAt) && api.retainedMainTurnExecutionIDs[sessionID] == executionID
			api.retainedMainTurnsMu.Unlock()
			if !current || ctx.Err() != nil || api.hasActiveTurnCancel(sessionID) {
				return false
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
		}, true)
}

func (api *StreamingAPI) emitWaitingBackgroundResults(sessionID string, count int) {
	if api.eventStore == nil || count == 0 {
		return
	}
	now := time.Now()
	data := &unifiedevents.GenericEventData{BaseEventData: unifiedevents.BaseEventData{Timestamp: now}, Data: map[string]interface{}{"message": fmt.Sprintf("%d results are waiting and will arrive with your next message.", count), "count": count}}
	agentEvent := unifiedevents.NewAgentEvent(data)
	agentEvent.SessionID = sessionID
	api.eventStore.AddEvent(sessionID, events.Event{ID: fmt.Sprintf("background-results-waiting-%d", now.UnixNano()), Type: "background_results_waiting", Timestamp: now, SessionID: sessionID, ExecutionID: "main:" + sessionID, ExecutionKind: "main_agent", Data: agentEvent})
}
