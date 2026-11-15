package mcpagent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type canonicalTurnLifecycleContextKey struct{}

// canonicalTurnLifecycle is the single owner of one accepted message's
// terminal event. Provider adapters translate their native completion signal;
// this lifecycle gives every provider the same stable identity and
// exactly-once outward completion semantics (PLAT-116).
type canonicalTurnLifecycle struct {
	id        string
	startedAt time.Time

	mu       sync.Mutex
	terminal bool
	plan     llmtypes.ClaudeTaskPlan
	// liveInputFollowup marks a Session.Run turn that received live input
	// through a tmux CLI while it ran. That input may be answered only after
	// this turn's completion, by a follow-up watch the Session starts when Run
	// returns. The completion carries the marker so hosts keep the input's
	// turn open instead of settling it on this (older) response.
	liveInputFollowup bool
	nativeSessionID   string
	nativeRoots       []string
}

func (l *canonicalTurnLifecycle) setNativePublication(session string, roots []string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nativeSessionID = session
	l.nativeRoots = append([]string(nil), roots...)
}

func newTurnID() string {
	return "turn_" + events.GenerateEventID()
}

func newCanonicalTurnLifecycle(requestedID string) *canonicalTurnLifecycle {
	id := strings.TrimSpace(requestedID)
	if id == "" {
		id = newTurnID()
	}
	return &canonicalTurnLifecycle{id: id, startedAt: time.Now()}
}

func withCanonicalTurnLifecycle(ctx context.Context, lifecycle *canonicalTurnLifecycle) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, canonicalTurnLifecycleContextKey{}, lifecycle)
}

func canonicalTurnLifecycleFromContext(ctx context.Context) *canonicalTurnLifecycle {
	if ctx == nil {
		return nil
	}
	lifecycle, _ := ctx.Value(canonicalTurnLifecycleContextKey{}).(*canonicalTurnLifecycle)
	return lifecycle
}

// prepareEvent stamps every event in a turn and admits at most one canonical
// completion. Returning false means the event is a duplicate terminal event
// and must not leave mcpagent.
func (l *canonicalTurnLifecycle) prepareEvent(eventData events.EventData) bool {
	if l == nil || eventData == nil {
		return true
	}
	if base, ok := eventData.(interface{ GetBaseEventData() *events.BaseEventData }); ok {
		data := base.GetBaseEventData()
		if data.Metadata == nil {
			data.Metadata = make(map[string]interface{})
		}
		data.Metadata["turn_id"] = l.id
	}
	completion, terminal := eventData.(*events.UnifiedCompletionEvent)
	if !terminal {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.terminal {
		return false
	}
	l.terminal = true
	if completion.Metadata == nil {
		completion.Metadata = make(map[string]interface{})
	}
	completion.Metadata["turn_id"] = l.id
	completion.Metadata["canonical_turn_completion"] = true
	completion.Metadata["native_session_id"] = l.nativeSessionID
	completion.Metadata["native_continuation_roots"] = append([]string(nil), l.nativeRoots...)
	if l.liveInputFollowup {
		completion.Metadata[LiveInputFollowupMetadataKey] = true
	}
	return true
}

// LiveInputFollowupMetadataKey is set on a turn's canonical completion when
// live input was sent to its CLI while it ran. The Session answers that input
// with a separate follow-up completion (source "mcpagent_session"); hosts must
// not treat this completion as the answer to that input.
const LiveInputFollowupMetadataKey = "live_input_followup"

// AnsweredByPreviousResponseMetadataKey is set on a follow-up completion whose
// live input the previous turn's own response already answered (the input
// steered that turn). It carries no final text so hosts never add a second
// copy of the previous answer; it only closes the input's turn.
const AnsweredByPreviousResponseMetadataKey = "answered_by_previous_response"

// RetainedTurnOutcomeMetadataKey is set on a retained turn's completion that carries no reply because the CLI's own
// record shows the turn ended without one; the value is "interrupted" (the person stopped it, or the model call failed).
const RetainedTurnOutcomeMetadataKey = "retained_turn_outcome"

func (l *canonicalTurnLifecycle) markLiveInputFollowup() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.liveInputFollowup = true
	l.mu.Unlock()
}

func (l *canonicalTurnLifecycle) isTerminal() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.terminal
}
