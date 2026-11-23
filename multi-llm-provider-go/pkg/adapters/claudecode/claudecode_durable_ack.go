package claudecode

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
)

// Durable submit confirmation for the Claude Code tmux lane: the
// observe-only transcript arbiter behind AwaitClaudeInputDurable.
//
// Live evidence (2026-09-19 probes, haiku): a mid-turn steer lands a
// `queue-operation/enqueue` row ~500ms after send-keys with the full
// text. A steer during text generation then drains via `dequeue`
// followed by its `user` row; a steer during a tool call drains via
// `remove` (full text, no user row ever — the CLI folds it into a
// system-reminder with the tool result instead). So the transcript
// itself carries both halves of the verdict — no pane scrape needed:
//
//   - user row with our text, or a queue drain (remove with our
//     text, or dequeue after our enqueue) -> confirmed (the CLI
//     consumed the send; model obedience is out of scope)
//   - enqueue row but no drain at budget expiry ->
//     accepted_but_unflushed (the CLI holds it; unconfirmed, not failed)
//   - neither -> timeout error (failed)
//
// Pasted sends are wrapped in <pasted_content> tags; matching strips
// them. Offset scoping (plus exact text) means an identical earlier
// message already in the stream cannot confirm a new one.

type ClaudeDurableAckOutcome string

const (
	// ClaudeDurableAckConfirmed means the session transcript contains
	// the user row for this send.
	ClaudeDurableAckConfirmed ClaudeDurableAckOutcome = "confirmed"
	// ClaudeDurableAckUnflushed means the budget expired while the
	// transcript showed the send enqueued but not yet a user row.
	// The CLI holds it; durability is unconfirmed, not failed.
	ClaudeDurableAckUnflushed ClaudeDurableAckOutcome = "accepted_but_unflushed"
)

// ClaudeDurableAck carries the transcript proof for one send.
type ClaudeDurableAck struct {
	Outcome      ClaudeDurableAckOutcome
	Latency      time.Duration
	ProofPath    string
	RowTimestamp time.Time
}

// ClaudeInputUnflushedError reports accepted-but-unflushed through the
// error return so existing err != nil callers stay safe (they treat it
// as a failure) while confirmation-aware callers can distinguish it
// with errors.As.
type ClaudeInputUnflushedError struct {
	OwnerSessionID string
	Latency        time.Duration
}

func (e *ClaudeInputUnflushedError) Error() string {
	return fmt.Sprintf("Claude input accepted but unflushed after %s (still natively queued; durability unconfirmed, not failed)", e.Latency.Round(100*time.Millisecond))
}

const ()

const ()

// InteractiveSessionRegistered reports whether the owner's Claude Code
// TUI is registered — the live-injection transport exists. Cheap
// registry lookup (no tmux round-trip) for steer gating; delivery
// re-verifies liveness. Mirrors the SendClaudeCodeInput registry.
func InteractiveSessionRegistered(ownerSessionID string) bool {
	_, ok := activeClaudeInteractiveOwner(ownerSessionID)
	return ok
}

// AwaitClaudeInputDurable waits for the transcript proof that a previous
// SendClaudeCodeInput reached the CLI. It is the durability half of
// the two-stage delivery receipt (fast pane ack, then this): the
// server watcher calls it after every fast ack and promotes the chat
// tick on confirmation. A zero timeout selects the env-tuned budget.
func AwaitClaudeInputDurable(ctx context.Context, ownerSessionID, message string, timeout time.Duration) (ClaudeDurableAck, error) {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return ClaudeDurableAck{}, fmt.Errorf("owner session id is required")
	}
	if strings.TrimSpace(message) == "" {
		return ClaudeDurableAck{}, fmt.Errorf("message is empty")
	}
	if session, ok := claudeInteractivePersistentRegistry.Get(ownerSessionID); !ok || session == nil {
		return ClaudeDurableAck{}, fmt.Errorf("no active Claude Code tmux session registered for owner session %s", ownerSessionID)
	}
	// The mod confirmed the send once Claude took it: a new turn started, or a
	// row stored in the running turn.
	input, ok := takeClaudeModInputTaken(ownerSessionID, message)
	if !ok {
		return ClaudeDurableAck{}, fmt.Errorf("no input sent to Claude Code session %s matches this message", ownerSessionID)
	}
	log.Printf("[claude-durable-ack] owner=%s latency=%dms proof=claude_mod", ownerSessionID, input.latency.Milliseconds())
	return ClaudeDurableAck{Outcome: ClaudeDurableAckConfirmed, Latency: input.latency, ProofPath: "claude_mod", RowTimestamp: input.at}, nil
}
