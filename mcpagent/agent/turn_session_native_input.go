package mcpagent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

// ObserveNativeInput adopts an already accepted terminal prompt. In contrast
// to Send, it performs no input delivery. The host records the native user row;
// Session owns progress, history and canonical completion as for a chat send.
func (s *Session) ObserveNativeInput(ctx context.Context, input string, acceptedAt time.Time) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("native input is empty")
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.stateMu.Lock()
	if s.closed {
		s.stateMu.Unlock()
		return "", fmt.Errorf("session is closed")
	}
	provider := s.agent.provider
	runActive := s.runActive
	lifecycle := s.activeTurn
	if lifecycle == nil {
		lifecycle = newCanonicalTurnLifecycle("")
		s.activeTurn = lifecycle
	}
	var runInputs *runLiveInputState
	if runActive && s.runLiveInputs != nil {
		runInputs = s.runLiveInputs
		runInputs.inflight++
		runInputs.lifecycle.markLiveInputFollowup()
	} else {
		s.retainedStarting = true
	}
	s.stateMu.Unlock()
	if acceptedAt.IsZero() {
		acceptedAt = time.Now()
	}
	adoptErr := llmproviders.AdoptCodingAgentNativeInput(provider, s.agent.sessionID, input, acceptedAt)
	s.stateMu.Lock()
	if runInputs != nil {
		runInputs.inflight--
		if adoptErr == nil {
			runInputs.record(input, acceptedAt, provider, llm.CodingAgentTransportTmux)
		}
		followup := s.takeRunLiveInputFollowupLocked(runInputs)
		s.stateMu.Unlock()
		if followup != nil {
			s.runRetainedCompletionWatch(*followup)
		}
		return lifecycle.id, adoptErr
	}
	s.retainedStarting = false
	if adoptErr != nil {
		if !s.runActive && !s.retainedActive && s.activeTurn == lifecycle {
			s.activeTurn = nil
		}
		s.stateMu.Unlock()
		return "", adoptErr
	}
	if s.closed {
		s.stateMu.Unlock()
		return "", fmt.Errorf("session is closed")
	}
	watch := s.beginRetainedWatchLocked(lifecycle)
	s.stateMu.Unlock()
	watch.input, watch.provider, watch.transport = input, provider, llm.CodingAgentTransportTmux
	watch.startedAt, watch.liveInput = acceptedAt, true
	s.runRetainedCompletionWatch(watch)
	return lifecycle.id, nil
}

// PrepareNativeInterrupt returns an acknowledgement callback for the current
// retained watch. Call it only after Ctrl+C was delivered to the native CLI.
// A normal Run owns its own interruption/completion path. Capturing the watcher
// generation keeps a delayed acknowledgement from cancelling newer work.
func (s *Session) PrepareNativeInterrupt() func() {
	s.stateMu.Lock()
	lifecycle, seq := s.activeTurn, s.retainedSeq
	eligible := !s.closed && !s.runActive && s.retainedActive && lifecycle != nil
	s.stateMu.Unlock()
	if !eligible {
		return nil
	}
	return func() { s.observeNativeInterrupt(lifecycle, seq) }
}

func (s *Session) observeNativeInterrupt(lifecycle *canonicalTurnLifecycle, seq uint64) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.stateMu.Lock()
	if s.closed || s.runActive || !s.retainedActive || s.activeTurn != lifecycle || s.retainedSeq != seq {
		s.stateMu.Unlock()
		return
	}
	s.retainedActive = false
	s.retainedSeq++
	s.activeTurn = nil
	s.stateMu.Unlock()
	completion := events.NewUnifiedCompletionEvent("coding_agent", "retained", "", "", "cancelled", time.Since(lifecycle.startedAt), 1)
	completion.Metadata["source"] = "mcpagent_session"
	completion.Metadata["reason"] = "native_interrupt"
	endedAt := time.Now()
	s.agent.emitTypedEvent(withCanonicalTurnLifecycle(context.Background(), lifecycle), completion)
	s.emitRetainedStreamEnd(lifecycle, "", lifecycle.startedAt, endedAt)
}
