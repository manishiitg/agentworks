package events

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Turn speed (#3724): one log line per message saying where the wait before
// the agent's first visible activity went, so start-up time can be measured
// and kept small. Grep server logs for [TURN_SPEED].
//
//	[TURN_SPEED] session=… first=tool_call_start first_ms=3912 steps=setup:122,config:550,…  spans=vault_list:316,…
//	[TURN_SPEED] session=… first_text_ms=5200
//	[TURN_SPEED] session=… turn_ms=48211 end=unified_completion
//
// steps are consecutive stages of the server's per-message setup; spans are
// named pieces timed inside them (they may overlap and run in parallel).
type turnSpeed struct {
	start, last time.Time
	steps       []string
	spans       []string
	provider    string
	process     string
	firstSeen   bool
	textSeen    bool
}

var (
	turnSpeedsMu sync.Mutex
	turnSpeeds   = map[string]*turnSpeed{}
)

// turnSpeedForget drops records whose message never produced an event.
const turnSpeedForget = 30 * time.Minute

// TurnSpeedStart begins the record for a person's message at the moment it arrived.
func TurnSpeedStart(sessionID string, arrived time.Time) {
	if sessionID == "" {
		return
	}
	turnSpeedsMu.Lock()
	defer turnSpeedsMu.Unlock()
	for id, s := range turnSpeeds {
		if time.Since(s.start) > turnSpeedForget {
			delete(turnSpeeds, id)
		}
	}
	turnSpeeds[sessionID] = &turnSpeed{start: arrived, last: arrived}
}

// TurnSpeedStep closes the stage that ran since the previous step.
func TurnSpeedStep(sessionID, step string) {
	turnSpeedsMu.Lock()
	defer turnSpeedsMu.Unlock()
	if s := turnSpeeds[sessionID]; s != nil {
		now := time.Now()
		s.steps = append(s.steps, fmt.Sprintf("%s:%d", step, now.Sub(s.last).Milliseconds()))
		s.last = now
	}
}

// TurnSpeedSpan times a named piece of the setup: defer events.TurnSpeedSpan(id, "vault_list")().
func TurnSpeedSpan(sessionID, name string) func() {
	started := time.Now()
	return func() {
		turnSpeedsMu.Lock()
		defer turnSpeedsMu.Unlock()
		if s := turnSpeeds[sessionID]; s != nil && !s.firstSeen {
			s.spans = append(s.spans, fmt.Sprintf("%s:%d", name, time.Since(started).Milliseconds()))
		}
	}
}

// TurnSpeedProvider names the program and model answering this message.
func TurnSpeedProvider(sessionID, provider string) {
	turnSpeedsMu.Lock()
	defer turnSpeedsMu.Unlock()
	if s := turnSpeeds[sessionID]; s != nil {
		s.provider = provider
	}
}

// TurnSpeedProcess records whether the agent program was started for this
// message or an already running one was reused, and why.
func TurnSpeedProcess(sessionID, provider, event, reason string) {
	if event != "launch" && event != "reuse" {
		return
	}
	turnSpeedsMu.Lock()
	defer turnSpeedsMu.Unlock()
	if s := turnSpeeds[sessionID]; s != nil && !s.firstSeen {
		s.process = event + ":" + reason
		s.spans = append(s.spans, fmt.Sprintf("process_%s_at:%d", event, time.Since(s.last).Milliseconds()))
	}
}

func turnSpeedActivity(typ string) bool {
	switch typ {
	case "coding_agent_native_activity", "conversation_thinking", "streaming_chunk", "tool_call_start", "llm_generation_start":
		return true
	}
	return false
}

// trackTurnSpeed logs the first activity, the first text and the turn's end.
func trackTurnSpeed(sessionID string, event Event) {
	typ := event.Type
	if typ == "" || (!turnSpeedActivity(typ) && !IsTurnEndType(typ)) {
		return
	}
	turnSpeedsMu.Lock()
	s := turnSpeeds[sessionID]
	if s == nil {
		turnSpeedsMu.Unlock()
		return
	}
	var line string
	elapsed := time.Since(s.start).Milliseconds()
	switch {
	case IsTurnEndType(typ):
		delete(turnSpeeds, sessionID)
		line = fmt.Sprintf("[TURN_SPEED] session=%s turn_ms=%d end=%s", sessionID, elapsed, typ)
		if !s.firstSeen {
			line += fmt.Sprintf(" provider=%s steps=%s spans=%s first=none", s.provider, strings.Join(s.steps, ","), strings.Join(s.spans, ","))
		}
	case !s.firstSeen:
		s.firstSeen = true
		if typ == "streaming_chunk" {
			s.textSeen = true
		}
		line = fmt.Sprintf("[TURN_SPEED] session=%s provider=%s process=%s first=%s first_ms=%d agent_ms=%d steps=%s spans=%s",
			sessionID, s.provider, s.process, typ, elapsed, time.Since(s.last).Milliseconds(), strings.Join(s.steps, ","), strings.Join(s.spans, ","))
	case typ == "streaming_chunk" && !s.textSeen:
		s.textSeen = true
		line = fmt.Sprintf("[TURN_SPEED] session=%s first_text_ms=%d", sessionID, elapsed)
	}
	turnSpeedsMu.Unlock()
	if line != "" {
		log.Print(line)
	}
}
