package events

import (
	"strings"
	"sync"
	"time"

	pkgevents "github.com/manishiitg/mcpagent/events"
)

// A steered message is journalled when the CLI confirms it took the message,
// which can be after the CLI started answering it (Cursor only writes its chat
// store once output begins). Rows of that answer must not land above the
// question: while a deferred steer awaits its row, answer rows that belong to
// it are held and released right after the user row. If the session was busy
// when the message was steered, the rows up to the in-flight answer's end still
// belong to that answer and pass through; an idle session holds immediately.

var deferredSteerHoldTimeout = 20 * time.Second

// deferredSteerInFlightCap bounds the wait while the session is still
// answering the previous message. A CLI takes a message sent mid-answer only
// when that answer ends, which can take minutes; the 20s timeout above starts
// at that end, not at the send. Counting from the send wrote the second
// message into the middle of the first answer (msg1, msg2, rest of reply1,
// reply2) although the CLI ran msg1 -> reply1 -> msg2 -> reply2.
var deferredSteerInFlightCap = 30 * time.Minute

// deferredSteerQuietWindow is how long a session may go without any answer row (and with no tool
// call running) before it counts as idle for a new steer. Answer rows that arrive after their
// turn's end (Cursor's late transcript chunks, native transcript catch-up) mark the session
// "in flight" with nothing left to end it, so the reply to the next message was let through above
// the message (RTS 2026-09-30). Silence past this window means the CLI is not mid-answer.
var deferredSteerQuietWindow = 15 * time.Second

// deferredSteerAckGrace is how long an acked steer waits for the in-flight answer's end to be
// recorded before its user row is written anyway. A CLI takes a queued message the moment its
// answer ends, but the platform records that answer's completion a moment later (Muse: ~0.5 s), so
// the row landed above the tail of the answer it followed (Muse, excellence 2026-09-30).
var deferredSteerAckGrace = 3 * time.Second

// steerNow is the clock for the quiet window (tests replace it).
var steerNow = time.Now

type pendingSteer struct {
	event   Event
	written bool
}

// PendingUserMessages lets native terminal observation recognize a Chat send
// whose durable acknowledgement has not published its user row yet.
func (es *EventStore) PendingUserMessages(sessionID string) []Event {
	state := es.steerHoldState()
	state.mu.Lock()
	defer state.mu.Unlock()
	var out []Event
	if hold := state.holds[sessionID]; hold != nil {
		for _, pending := range hold.pending {
			if !pending.written {
				out = append(out, pending.event)
			}
		}
	}
	return out
}

type deferredSteerHold struct {
	// answerEnded is closed once the in-flight answer's end row has been recorded.
	answerEnded     chan struct{}
	answerEndedOnce sync.Once
	pending         []*pendingSteer
	boundaryPassed  bool
	releasing       bool
	held            []Event
	timer           *time.Timer
}

type steerOrdering struct {
	mu       sync.Mutex
	holds    map[string]*deferredSteerHold
	inFlight map[string]bool
	// lastAnswerAt is when the session last produced an answer row, and pendingTools how many tool
	// calls have started without ending: together they tell a silent gap from a running answer.
	lastAnswerAt map[string]time.Time
	pendingTools map[string]int
	// User rows written by a timeout; the late ack must not write them again.
	timedOut map[string]bool
}

func (es *EventStore) steerHoldState() *steerOrdering {
	es.steerOrderingOnce.Do(func() {
		es.steerOrdering = &steerOrdering{
			holds:    make(map[string]*deferredSteerHold),
			inFlight: make(map[string]bool),
			timedOut: make(map[string]bool),

			lastAnswerAt: make(map[string]time.Time),
			pendingTools: make(map[string]int),
		}
	})
	return es.steerOrdering
}

// answerInFlightLocked reports whether the session is really mid-answer: an answer row without a
// later end, seen recently or with a tool call still running. Callers hold s.mu.
func (s *steerOrdering) answerInFlightLocked(sessionID string) bool {
	if !s.inFlight[sessionID] {
		return false
	}
	if s.pendingTools[sessionID] > 0 {
		return true
	}
	last := s.lastAnswerAt[sessionID]
	return last.IsZero() || steerNow().Sub(last) < deferredSteerQuietWindow
}

func steerKey(sessionID, eventID string) string { return sessionID + "\x00" + eventID }

// BeginDeferredSteer registers a steered message whose user row will be written
// at the CLI's durable ack. Pair every call with CompleteDeferredSteer.
func (es *EventStore) BeginDeferredSteer(sessionID string, user Event) {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	state := es.steerHoldState()
	state.mu.Lock()
	defer state.mu.Unlock()
	hold := state.holds[sessionID]
	if hold == nil {
		hold = &deferredSteerHold{boundaryPassed: !state.answerInFlightLocked(sessionID), answerEnded: make(chan struct{})}
		if hold.boundaryPassed {
			hold.answerEndedOnce.Do(func() { close(hold.answerEnded) })
		}
		state.holds[sessionID] = hold
	}
	hold.pending = append(hold.pending, &pendingSteer{event: user})
	es.armSteerHoldTimerLocked(sessionID, hold)
}

// armSteerHoldTimerLocked (re)starts the hold's timeout: the short one once
// the in-flight answer has ended, the long cap while it is still streaming.
// Callers hold state.mu.
func (es *EventStore) armSteerHoldTimerLocked(sessionID string, hold *deferredSteerHold) {
	if hold.timer != nil {
		hold.timer.Stop()
	}
	wait := deferredSteerHoldTimeout
	if !hold.boundaryPassed {
		wait = deferredSteerInFlightCap
	}
	hold.timer = time.AfterFunc(wait, func() { es.timeoutSteerHold(sessionID) })
}

// CompleteDeferredSteer writes the user row at the CLI's ack (unless a timeout
// already wrote it) and, once no steer is pending, releases the held answer
// rows after it in order.
func (es *EventStore) CompleteDeferredSteer(sessionID string, user Event) {
	if es == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	state := es.steerHoldState()
	state.mu.Lock()
	key := steerKey(sessionID, user.ID)
	if state.timedOut[key] {
		delete(state.timedOut, key)
		state.mu.Unlock()
		return
	}
	hold := state.holds[sessionID]
	if hold != nil && !hold.boundaryPassed && hold.answerEnded != nil {
		// The CLI took the message, so the answer it was writing is over; its end row follows the
		// ack by a moment. Wait for it (bounded) so the user row lands after that answer.
		ended := hold.answerEnded
		state.mu.Unlock()
		select {
		case <-ended:
		case <-time.After(deferredSteerAckGrace):
		}
		state.mu.Lock()
		if state.timedOut[key] {
			delete(state.timedOut, key)
			state.mu.Unlock()
			return
		}
		hold = state.holds[sessionID]
	}
	if hold == nil {
		state.mu.Unlock()
		_ = es.addEventUnheld(sessionID, user)
		return
	}
	for i, pending := range hold.pending {
		if pending.event.ID == user.ID {
			hold.pending = append(hold.pending[:i], hold.pending[i+1:]...)
			break
		}
	}
	remaining := len(hold.pending)
	user = datedBeforeHeldAnswer(user, hold.held)
	state.mu.Unlock()
	_ = es.addEventUnheld(sessionID, user)
	if remaining == 0 {
		es.releaseSteerHold(sessionID, hold)
	}
}

// timeoutSteerHold never strands rows: it writes every still-pending user row
// first, then releases the held answer rows, so the order stays user → reply.
func (es *EventStore) timeoutSteerHold(sessionID string) {
	state := es.steerHoldState()
	state.mu.Lock()
	hold := state.holds[sessionID]
	if hold == nil {
		state.mu.Unlock()
		return
	}
	var users []Event
	for _, pending := range hold.pending {
		if !pending.written {
			pending.written = true
			state.timedOut[steerKey(sessionID, pending.event.ID)] = true
			users = append(users, datedBeforeHeldAnswer(UserMessageAt(pending.event, time.Now()), hold.held))
		}
	}
	hold.pending = nil
	state.mu.Unlock()
	for _, user := range users {
		_ = es.addEventUnheld(sessionID, user)
	}
	es.releaseSteerHold(sessionID, hold)
}

// UserMessageAt dates a user row (envelope, agent event and message payload)
// at the given moment.
func UserMessageAt(event Event, at time.Time) Event {
	event.Timestamp = at
	if event.Data != nil {
		agentEvent := *event.Data
		agentEvent.Timestamp = at
		if message, ok := agentEvent.Data.(*pkgevents.UserMessageEvent); ok && message != nil {
			copied := *message
			copied.Timestamp = at
			agentEvent.Data = &copied
		}
		event.Data = &agentEvent
	}
	return event
}

// datedBeforeHeldAnswer keeps the user row's time no later than the first
// held answer row. The row is dated at the ack, but a held answer row keeps
// the time it was produced, which can precede the ack (Cursor writes its chat
// store only once output begins). The chat orders the main conversation by
// timestamp, so an ack-dated question sorted below its own reply: the reply was
// processed but rendered above the question, never as the newest row. Equal
// times fall back to journal sequence, where the user row comes first.
// Callers hold state.mu.
func datedBeforeHeldAnswer(user Event, held []Event) Event {
	var earliest time.Time
	for _, event := range held {
		if !event.Timestamp.IsZero() && (earliest.IsZero() || event.Timestamp.Before(earliest)) {
			earliest = event.Timestamp
		}
	}
	if earliest.IsZero() || !user.Timestamp.After(earliest) {
		return user
	}
	return UserMessageAt(user, earliest)
}

func (es *EventStore) releaseSteerHold(sessionID string, hold *deferredSteerHold) {
	state := es.steerHoldState()
	state.mu.Lock()
	if hold.timer != nil {
		hold.timer.Stop()
	}
	hold.releasing = true
	state.mu.Unlock()
	for {
		state.mu.Lock()
		batch := hold.held
		hold.held = nil
		if len(batch) == 0 {
			if state.holds[sessionID] == hold {
				delete(state.holds, sessionID)
			}
			state.mu.Unlock()
			return
		}
		state.mu.Unlock()
		for _, event := range batch {
			_ = es.addEventUnheld(sessionID, event)
		}
	}
}

// holdForDeferredSteer reports whether the event was taken into a hold. It also
// tracks whether the session has an answer in flight, which decides whether a
// new deferred steer must first let that answer finish.
func (es *EventStore) holdForDeferredSteer(sessionID string, event Event) bool {
	answerRow := isSteerOrderedAnswerRow(event)
	ends := answerRow && event.Type == "unified_completion" || isMainTurnEnd(event)
	// A turn is in flight from its user message on, before any answer row.
	// Deferred steer rows are written past this hook and never start a turn.
	startsTurn := event.Type == "user_message" && isMainKind(event)
	if !answerRow && !ends && !startsTurn {
		return false
	}
	state := es.steerHoldState()
	state.mu.Lock()
	defer state.mu.Unlock()
	if ends {
		delete(state.inFlight, sessionID)
		delete(state.lastAnswerAt, sessionID)
		delete(state.pendingTools, sessionID)
	} else {
		state.inFlight[sessionID] = true
		if answerRow {
			state.lastAnswerAt[sessionID] = steerNow()
		}
		switch event.Type {
		case "tool_call_start":
			state.pendingTools[sessionID]++
		case "tool_call_end", "tool_call_error":
			if state.pendingTools[sessionID] > 0 {
				state.pendingTools[sessionID]--
			}
		}
	}
	hold := state.holds[sessionID]
	if startsTurn {
		return false
	}
	if hold == nil || !answerRow {
		if hold != nil && ends && !hold.boundaryPassed {
			hold.boundaryPassed = true
			es.armSteerHoldTimerLocked(sessionID, hold)
		}
		return false
	}
	if hold.releasing {
		// Keep order behind rows still being released.
		hold.held = append(hold.held, event)
		return true
	}
	if !hold.boundaryPassed {
		// Rows up to the in-flight answer's end belong to that answer.
		if ends {
			hold.boundaryPassed = true
			es.armSteerHoldTimerLocked(sessionID, hold)
		}
		return false
	}
	hold.held = append(hold.held, event)
	return true
}

func isMainKind(event Event) bool {
	kind := strings.ToLower(strings.TrimSpace(event.ExecutionKind))
	return kind == "" || kind == "main" || kind == "main_agent" || kind == "chat"
}

// isMainTurnEnd covers turns that end without a unified_completion.
func isMainTurnEnd(event Event) bool {
	if !isMainKind(event) {
		return false
	}
	switch event.Type {
	case "agent_end", "conversation_end", "conversation_error", "agent_error", "context_cancelled":
		return true
	}
	return false
}

func isSteerOrderedAnswerRow(event Event) bool {
	if !isMainKind(event) {
		return false
	}
	switch event.Type {
	case "unified_completion", "tool_call_start", "tool_call_end", "tool_call_error":
		return true
	}
	return IsTranscriptMessage(event)
}

// signalAnswerEnded wakes acked steers waiting for the in-flight answer's end row. It runs after
// that row has been recorded, so a waiting user row is written behind it.
func (es *EventStore) signalAnswerEnded(sessionID string, event Event) {
	if es == nil {
		return
	}
	answerRow := isSteerOrderedAnswerRow(event)
	if !(answerRow && event.Type == "unified_completion" || isMainTurnEnd(event)) {
		return
	}
	state := es.steerHoldState()
	state.mu.Lock()
	hold := state.holds[sessionID]
	state.mu.Unlock()
	if hold != nil && hold.answerEnded != nil {
		hold.answerEndedOnce.Do(func() { close(hold.answerEnded) })
	}
}
