package events

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/events"
)

// The turn phase (PLAT-861-220). The Working line used to show a playful word from the moment the person sent a
// message, even while the agent had not started (a launch stuck on a screen, a retry waiting out its delay). The
// server now says what the turn is really doing, from the events it already sees, and the browser only displays it:
//
//	starting         the turn began; the agent has produced nothing yet
//	retrying         a launch or call failed and the next attempt is waiting
//	waiting_for_you  the agent asked a question and waits for the answer
//	running          the agent produced output
//	compacting       an idle compaction is tidying the chat (PLAT-872); no person turn is running
//
// A change is announced as one "turn_phase" event (application-defined, carried as GenericEventData). It is not
// stored in the durable history: the in-memory session events replay it, and a chat without one keeps the old line.
const turnPhaseEventType = "turn_phase"

const (
	turnPhaseStarting       = "starting"
	turnPhaseRetrying       = "retrying"
	turnPhaseWaitingForYou  = "waiting_for_you"
	turnPhaseRunning        = "running"
	turnPhaseCompacting     = "compacting"
	turnPhaseCompactingText = "Tidying this chat while you were away"
	turnPhaseStaleAfter     = 6 * time.Hour
	turnPhaseRetrySuffix    = " - retrying original model"
	turnPhaseWaitingMessage = "Waiting for your answer"
)

type turnPhaseState struct {
	phase    string
	label    string // "Muse", "Claude" or "the agent"
	reason   string
	attempt  int
	total    int
	nextIn   string
	updated  time.Time
	announce string // signature of the last announced state
}

var (
	turnPhasesMu     sync.Mutex
	turnPhases       = map[string]*turnPhaseState{}
	turnPhaseSweptAt time.Time
)

// Only a current server-issued label identifies the CLI. A model family is
// not an agent identity: Cursor and Pi can run Claude models too.
func turnPhaseEventLabel(event Event) string {
	if event.Data == nil || event.Data.Data == nil {
		return ""
	}
	if data, ok := event.Data.Data.(interface{ GetBaseEventData() *events.BaseEventData }); ok {
		if base := data.GetBaseEventData(); base != nil {
			label, _ := base.Metadata["agent_label"].(string)
			return strings.TrimSpace(label)
		}
	}
	return ""
}

func turnPhaseText(s *turnPhaseState) string {
	label := s.label
	if label == "" {
		label = "the agent"
	}
	switch s.phase {
	case turnPhaseStarting:
		return "Starting " + label
	case turnPhaseRetrying:
		text := "Retrying"
		if s.reason != "" {
			text += ": " + s.reason
		}
		if s.attempt > 0 && s.total > 0 {
			text += fmt.Sprintf(" (attempt %d of %d", s.attempt, s.total)
			if s.nextIn != "" {
				text += ", next in " + s.nextIn
			}
			text += ")"
		}
		return text
	case turnPhaseWaitingForYou:
		return turnPhaseWaitingMessage
	case turnPhaseCompacting:
		return turnPhaseCompactingText
	}
	return ""
}

// trackTurnPhase moves the session's turn phase on one event and announces the change.
// TurnInProgress reports whether the server sees a turn running for the session: one began (a user message or the
// agent's first event) and no turn-end event has cleared it yet. Stop uses it to interrupt only a turn that is running.
func (es *EventStore) TurnInProgress(sessionID string) bool {
	turnPhasesMu.Lock()
	defer turnPhasesMu.Unlock()
	st := turnPhases[sessionID]
	return st != nil && time.Since(st.updated) <= turnPhaseStaleAfter
}

// PersistedTurnOpen reports whether the session's stored events end inside a turn: the newest turn marker is a user
// message, not a turn end. It reads the stored events, so it also answers after a restart, when the live turn state is
// gone.
func (es *EventStore) PersistedTurnOpen(sessionID string) bool {
	// The newest turn marker is almost always in the last few events; only a session whose recent events hold none of them
	// is read in full.
	const recent = 300
	if open, found := lastTurnMarkerOpen(es.LastEventsRaw(sessionID, recent)); found {
		return open
	}
	open, _ := lastTurnMarkerOpen(es.GetAllEventsRaw(sessionID))
	return open
}

// lastTurnMarkerOpen reads the newest turn marker in events: open is true when it is a user message, found is false when
// there is no marker at all.
func lastTurnMarkerOpen(all []Event) (open, found bool) {
	for i := len(all) - 1; i >= 0; i-- {
		switch all[i].Type {
		case "unified_completion", "conversation_end", "conversation_error", "context_cancelled", "agent_error":
			return false, true
		case "user_message":
			return true, true
		}
	}
	return false, false
}

// EnableTurnPhase makes the store announce turn phases (the server calls it once on its own store).
func (es *EventStore) EnableTurnPhase() {
	es.mu.Lock()
	es.turnPhaseEnabled = true
	es.mu.Unlock()
}

func (es *EventStore) turnPhaseOn() bool {
	es.mu.RLock()
	defer es.mu.RUnlock()
	return es.turnPhaseEnabled
}

// TurnPhaseSnapshot lets server-owned activity metadata retain the current
// phase without announcing a new lifecycle state or restarting its clock.
func (es *EventStore) TurnPhaseSnapshot(sessionID string) (phase, text string, since time.Time) {
	turnPhasesMu.Lock()
	defer turnPhasesMu.Unlock()
	if state := turnPhases[sessionID]; state != nil {
		return state.phase, turnPhaseText(state), state.updated
	}
	return "", "", time.Time{}
}

func (es *EventStore) trackTurnPhase(sessionID string, event Event) {
	if sessionID == "" || event.Type == turnPhaseEventType || !es.turnPhaseOn() {
		return
	}
	now := time.Now()
	turnPhasesMu.Lock()
	// Forget abandoned sessions, at most once a minute: this runs on every event.
	if now.Sub(turnPhaseSweptAt) >= time.Minute {
		turnPhaseSweptAt = now
		for id, st := range turnPhases {
			if now.Sub(st.updated) > turnPhaseStaleAfter {
				delete(turnPhases, id)
			}
		}

	}
	st := turnPhases[sessionID]
	if st != nil && st.phase == turnPhaseCompacting {
		// The idle compaction's own model-call events are not a person's turn;
		// only EndIdleCompaction, a person's message or a turn end leaves this phase.
		switch event.Type {
		case "user_message", "unified_completion", "conversation_end", "conversation_error", "context_cancelled", "agent_error":
			delete(turnPhases, sessionID)
			st = nil
		default:
			turnPhasesMu.Unlock()
			return
		}
	}
	var announce *turnPhaseState
	set := func(phase string) {
		if st == nil {
			st = &turnPhaseState{}
			turnPhases[sessionID] = st
		}
		if st.phase != phase {
			st.reason, st.attempt, st.total, st.nextIn = "", 0, 0, ""
		}
		st.phase, st.updated = phase, now
	}
	switch event.Type {
	case "user_message":
		if st == nil {
			set(turnPhaseStarting)
		}
	case "llm_generation_start":
		if st == nil {
			set(turnPhaseStarting)
		} else if st.phase == turnPhaseRetrying {
			set(turnPhaseStarting)
		}

	case "retry_attempt":
		if st == nil {
			set(turnPhaseStarting)
		}
		if event.Data != nil {
			if d, ok := event.Data.Data.(*events.RetryAttemptEvent); ok {
				set(turnPhaseRetrying)
				st.reason = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(d.Error), turnPhaseRetrySuffix))
				st.reason = strings.ReplaceAll(st.reason, "_", " ")
				st.attempt, st.total, st.nextIn = d.AttemptIndex+1, d.TotalAttempts, d.Duration
				if st.attempt > st.total && st.total > 0 {
					st.attempt = st.total
				}
			}
		}
	case "request_human_feedback", "blocking_human_feedback", "coding_agent_question":
		if st != nil {
			set(turnPhaseWaitingForYou)
		}
	case "human_feedback_resolved", "tool_call_start", "conversation_thinking", "llm_generation_end":
		if st != nil && st.phase != turnPhaseRunning {
			set(turnPhaseRunning)
		}
	case "streaming_chunk":
		if st != nil && st.phase != turnPhaseRunning {
			// The agent's own words. A terminal-screen snapshot is the pane, not output: a sign-in or update screen
			// streamed as terminal text must not turn "starting" into "working".
			source, _, contentLen := streamingChunkFields(event)
			// "prompt_accepted" is the terminal CLI saying it took the prompt: the turn really began, even if it is
			// quiet. Otherwise only the agent's own words count.
			if source == events.StreamingChunkSourcePromptAccepted || (source != "terminal" && contentLen > 0) {
				set(turnPhaseRunning)
			}
		}
	case "streaming_end":
		// A failed launch ends its stream with nothing in it while the retry is still to come.
		if st != nil && st.phase == turnPhaseRunning {
			delete(turnPhases, sessionID)
			st = nil
		}
	case "unified_completion", "conversation_end", "conversation_error", "context_cancelled", "agent_error":
		if st != nil {
			delete(turnPhases, sessionID)
			st = nil
		}
	}
	if st != nil {
		if label := turnPhaseEventLabel(event); label != "" {
			st.label = label
		}
		sig := fmt.Sprintf("%s|%s|%s|%d|%d|%s", st.phase, st.label, st.reason, st.attempt, st.total, st.nextIn)
		if sig != st.announce {
			st.announce = sig
			copied := *st
			announce = &copied
		}
	}
	turnPhasesMu.Unlock()
	es.announceTurnPhase(sessionID, announce, now)
}

// BeginIdleCompaction shows the idle compaction (PLAT-872) as the chat's turn
// phase, so the Working line says the chat is being tidied instead of
// "running" while the CLI compacts.
func (es *EventStore) BeginIdleCompaction(sessionID string) {
	if sessionID == "" || !es.turnPhaseOn() {
		return
	}
	now := time.Now()
	st := &turnPhaseState{phase: turnPhaseCompacting, updated: now}
	st.announce = fmt.Sprintf("%s|%s|%s|%d|%d|%s", st.phase, st.label, st.reason, st.attempt, st.total, st.nextIn)
	turnPhasesMu.Lock()
	turnPhases[sessionID] = st
	turnPhasesMu.Unlock()
	copied := *st
	es.announceTurnPhase(sessionID, &copied, now)
}

// EndIdleCompaction clears the compaction phase; the next person turn starts fresh.
func (es *EventStore) EndIdleCompaction(sessionID string) {
	turnPhasesMu.Lock()
	if st := turnPhases[sessionID]; st != nil && st.phase == turnPhaseCompacting {
		delete(turnPhases, sessionID)
	}
	turnPhasesMu.Unlock()
}

func (es *EventStore) announceTurnPhase(sessionID string, announce *turnPhaseState, now time.Time) {
	if announce == nil {
		return
	}
	data := map[string]interface{}{
		"phase": announce.phase,
		"text":  turnPhaseText(announce),
		"since": now.UTC().Format(time.RFC3339Nano),
	}
	if announce.label != "" {
		data["agent_label"] = announce.label
	}
	agentEvent := events.NewAgentEvent(&events.GenericEventData{
		BaseEventData: events.BaseEventData{Timestamp: now},
		Data:          data,
	})
	agentEvent.SessionID = sessionID
	agentEvent.Component = "turn_phase"
	log.Printf("[TURN] phase=%s session=%s text=%q", announce.phase, sessionID, data["text"])
	es.AddEvent(sessionID, Event{
		ID:        fmt.Sprintf("turn-phase-%s-%d", sessionID, now.UnixNano()),
		Type:      turnPhaseEventType,
		Timestamp: now,
		Data:      agentEvent,
		SessionID: sessionID,
	})
}
