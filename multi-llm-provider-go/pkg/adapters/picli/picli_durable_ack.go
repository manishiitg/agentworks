package picli

import (
	"context"
	"fmt"

	"os"
	"strconv"
	"strings"
	"time"
)

// PiDurableAckOutcome is the verdict of the native receipt wait.
type PiDurableAckOutcome string

const (
	// PiDurableAckConfirmed means the native session contains the user
	// row for this send.
	PiDurableAckConfirmed PiDurableAckOutcome = "confirmed"
	// PiDurableAckUnflushed means the budget expired while the pane
	// still positively showed the message held (natively queued, or
	// taken by an active turn whose marker row is still pending). Held
	// by the CLI, durability unconfirmed, not failed.
	PiDurableAckUnflushed PiDurableAckOutcome = "accepted_but_unflushed"
)

// PiDurableAck carries the native record proof for one send.
type PiDurableAck struct {
	Outcome      PiDurableAckOutcome
	Latency      time.Duration
	ProofPath    string
	RowTimestamp time.Time
}

// PiInputUnflushedError reports accepted-but-unflushed through the
// error return so existing err != nil callers stay safe while
// confirmation-aware callers can distinguish it with errors.As.
type PiInputUnflushedError struct {
	OwnerSessionID string
	Latency        time.Duration
}

func (e *PiInputUnflushedError) Error() string {
	return fmt.Sprintf("Pi input accepted but unflushed after %s (still held by the CLI; durability unconfirmed, not failed)", e.Latency.Round(100*time.Millisecond))
}

const (
	piDurableAckBudgetDefault = 60 * time.Second
	piDurableAckBudgetMin     = 5 * time.Second
	piDurableAckBudgetMax     = 300 * time.Second
)

// piDurableAckBudget bounds the native receipt wait.
// PI_DURABLE_ACK_SECONDS overrides the default; values outside
// [5,300] are clamped so a broken env cannot hang sends or neuter
// the arbiter.
func piDurableAckBudget() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PI_DURABLE_ACK_SECONDS"))
	if raw == "" {
		return piDurableAckBudgetDefault
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return piDurableAckBudgetDefault
	}
	budget := time.Duration(seconds) * time.Second
	if budget < piDurableAckBudgetMin {
		return piDurableAckBudgetMin
	}
	if budget > piDurableAckBudgetMax {
		return piDurableAckBudgetMax
	}
	return budget
}

// AwaitPiInputDurable waits for the native record proof that a previous
// SendPiInteractiveInput reached the CLI. It is the durability half
// of the two-stage delivery receipt (fast pane ack, then this): the
// server watcher calls it after every fast ack and promotes the chat
// tick on confirmation. A zero timeout selects the env-tuned budget.
func AwaitPiInputDurable(ctx context.Context, ownerSessionID, message string, timeout time.Duration) (PiDurableAck, error) {
	return awaitPiRPCInputDurable(ctx, ownerSessionID, message, timeout)
}
