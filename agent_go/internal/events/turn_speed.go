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
//	[TURN_SPEED] session=… first_text_ms=5200 agent=prompt_sent:4,cli_output:30,model_start:1900,first_delta:2600
//	[TURN_SPEED] session=… turn_ms=48211 end=unified_completion
//
// steps are consecutive stages of the server's per-message setup; spans are
// named pieces timed inside them (they may overlap and run in parallel); a
// piece timed more than once shows its total and count (secrets:12x4). agent=
// splits the wait after the server handed the message to the agent program,
// in ms since then: the prompt written, its first output line, the model's
// first event and its first streamed text or thinking. A message with no steps
// went into an already running turn (path=live_input).
type turnSpeed struct {
	start, last time.Time
	steps       []string
	spans       []string
	spanTotals  map[string]*turnSpeedSpan
	stages      []string
	stageSeen   map[string]bool
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
			s.addSpan(name, time.Since(started).Milliseconds())
		}
	}
}

type turnSpeedSpan struct{ ms, count int64 }

func (s *turnSpeed) addSpan(name string, ms int64) {
	if s.spanTotals == nil {
		s.spanTotals = map[string]*turnSpeedSpan{}
	}
	total := s.spanTotals[name]
	if total == nil {
		total = &turnSpeedSpan{}
		s.spanTotals[name] = total
		s.spans = append(s.spans, name)
	}
	total.ms += ms
	total.count++
}

func (s *turnSpeed) spanList() string {
	parts := make([]string, 0, len(s.spans))
	for _, name := range s.spans {
		total := s.spanTotals[name]
		if total.count > 1 {
			parts = append(parts, fmt.Sprintf("%s:%dx%d", name, total.ms, total.count))
		} else {
			parts = append(parts, fmt.Sprintf("%s:%d", name, total.ms))
		}
	}
	return strings.Join(parts, ",")
}

func (s *turnSpeed) path() string {
	if len(s.steps) == 0 {
		return " path=live_input"
	}
	return ""
}

// TurnSpeedStage records a moment inside the agent program's turn (see agent=
// above); only the first of each name counts, until the first text arrives.
func TurnSpeedStage(sessionID, provider, stage string) {
	turnSpeedsMu.Lock()
	defer turnSpeedsMu.Unlock()
	s := turnSpeeds[sessionID]
	if s == nil || s.textSeen || s.stageSeen[stage] {
		return
	}
	if s.stageSeen == nil {
		s.stageSeen = map[string]bool{}
	}
	s.stageSeen[stage] = true
	s.stages = append(s.stages, fmt.Sprintf("%s:%d", stage, time.Since(s.last).Milliseconds()))
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
		s.addSpan("process_"+event+"_at", time.Since(s.last).Milliseconds())
	}
}

func turnSpeedActivity(typ string) bool {
	switch typ {
	case "coding_agent_native_activity", "conversation_thinking", "streaming_chunk", "tool_call_start":
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
			line += fmt.Sprintf(" provider=%s steps=%s spans=%s first=none", s.provider, strings.Join(s.steps, ","), s.spanList())
		}
		if !s.textSeen && len(s.stages) > 0 {
			line += " agent=" + strings.Join(s.stages, ",")
		}
	case !s.firstSeen:
		s.firstSeen = true
		if typ == "streaming_chunk" {
			s.textSeen = true
		}
		line = fmt.Sprintf("[TURN_SPEED] session=%s provider=%s process=%s first=%s first_ms=%d agent_ms=%d steps=%s spans=%s agent=%s%s",
			sessionID, s.provider, s.process, typ, elapsed, time.Since(s.last).Milliseconds(), strings.Join(s.steps, ","), s.spanList(), strings.Join(s.stages, ","), s.path())
	case typ == "streaming_chunk" && !s.textSeen:
		s.textSeen = true
		line = fmt.Sprintf("[TURN_SPEED] session=%s first_text_ms=%d agent=%s", sessionID, elapsed, strings.Join(s.stages, ","))
	}
	turnSpeedsMu.Unlock()
	if line != "" {
		log.Print(line)
	}
}
