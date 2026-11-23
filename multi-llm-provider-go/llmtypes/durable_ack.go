package llmtypes

import "time"

// DurableAckOutcome is the verdict of rollout/transcript-backed submit
// confirmation for live input into a retained coding CLI.
type DurableAckOutcome string

const (
	// DurableAckConfirmed means the CLI's own durable record contains
	// this send (the double tick).
	DurableAckConfirmed DurableAckOutcome = "confirmed"
	// DurableAckUnflushed means the send is provably held in the CLI's
	// native queue but the durable record has not landed within budget
	// (single tick plus clock). Held, not failed.
	DurableAckUnflushed DurableAckOutcome = "accepted_but_unflushed"
	// DurableAckFailed means neither the pane nor the durable record
	// confirmed the send.
	DurableAckFailed DurableAckOutcome = "failed"
)

// DurableAck is the provider-neutral durability half of the two-stage
// delivery receipt. Adapters with SupportsDurableAck map their native
// proof onto this shape so host applications render one tick model.
type DurableAck struct {
	Outcome      DurableAckOutcome
	Latency      time.Duration
	ProofSource  string
	RowTimestamp time.Time
}
