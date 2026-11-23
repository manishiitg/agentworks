package musecli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Durable submit acknowledgement for Muse live input.
//
// The native session transcript ($XDG_DATA_HOME/muse/sessions/.../session.jsonl)
// is the authoritative second opinion behind the pane: intake appends a
// runtime.user_intent.accepted row carrying the exact message text, and a
// busy steer additionally appends an inbox_item_queued event the same
// second — both well before the queue drains. Rows carry a monotonic
// sequence, so scoping the match above the pre-send baseline means an
// identical earlier message cannot confirm a new one.
//
// The live-input path is otherwise pane-only ("fire-and-report"), which
// makes this the highest-value durable ack of the rollout: without it a
// pane misread is a user-visible false error with no second opinion.
// See docs/refactor/durable_ack_p0.md in mcp-agent-builder-go.

// MuseDurableAckOutcome is the verdict of the transcript arbiter.
type MuseDurableAckOutcome string

const (
	// MuseDurableAckConfirmed means the transcript contains the user
	// row for this send above the pre-send baseline.
	MuseDurableAckConfirmed MuseDurableAckOutcome = "confirmed"
	// MuseDurableAckUnflushed means the budget expired while the pane
	// still positively showed the message held (natively queued, or
	// taken by an active turn whose transcript row is still pending).
	// Held by the CLI, durability unconfirmed, not failed.
	MuseDurableAckUnflushed MuseDurableAckOutcome = "accepted_but_unflushed"
)

// MuseDurableAck carries the transcript proof for one send.
type MuseDurableAck struct {
	Outcome      MuseDurableAckOutcome
	Latency      time.Duration
	ProofPath    string
	RowTimestamp time.Time
}

// MuseInputUnflushedError reports accepted-but-unflushed through the
// error return so existing err != nil callers stay safe while
// confirmation-aware callers can distinguish it with errors.As.
type MuseInputUnflushedError struct {
	OwnerSessionID string
	Latency        time.Duration
}

func (e *MuseInputUnflushedError) Error() string {
	return fmt.Sprintf("Muse input accepted but unflushed after %s (still held by the CLI; durability unconfirmed, not failed)", e.Latency.Round(100*time.Millisecond))
}

const (
	museDurableAckBudgetDefault = 60 * time.Second
	museDurableAckBudgetMin     = 5 * time.Second
	museDurableAckBudgetMax     = 300 * time.Second
	museDurableAckPollInterval  = 250 * time.Millisecond
	// museDurableAckCaptureTimeout bounds the final pane read on
	// budget expiry. It runs under a fresh context derived from the
	// poll's parent (the budget deadline is expired there by
	// construction); 5s is generous for one tmux scrape.
	museDurableAckCaptureTimeout = 5 * time.Second
)

// museDurableAckBudget bounds the observe-only transcript arbiter.
// MUSE_DURABLE_ACK_SECONDS overrides the default; values outside
// [5,300] are clamped so a broken env cannot hang sends or neuter
// the arbiter.
func museDurableAckBudget() time.Duration {
	raw := strings.TrimSpace(os.Getenv("MUSE_DURABLE_ACK_SECONDS"))
	if raw == "" {
		return museDurableAckBudgetDefault
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return museDurableAckBudgetDefault
	}
	budget := time.Duration(seconds) * time.Second
	if budget < museDurableAckBudgetMin {
		return museDurableAckBudgetMin
	}
	if budget > museDurableAckBudgetMax {
		return museDurableAckBudgetMax
	}
	return budget
}

// musePendingDurableAck is the pre-send snapshot that scopes one send's
// durability proof to transcript rows this send could have produced.
// Occurrence disambiguates identical sends queued before any row lands:
// the nth stashed send needs the nth matching transcript row.
type musePendingDurableAck struct {
	message     string
	logPath     string
	baselineSeq int64
	since       time.Time
	occurrence  int
}

const (
	museMaxPendingDurableAcks = 8
	musePendingDurableAckTTL  = 10 * time.Minute
)

// stashMuseDurableReceipt records a pre-send snapshot on the pooled
// entry. Pending receipts ride the existing pool lock: turns never
// hold it while running, so the arbiter and the server watcher can
// take it briefly concurrent with a turn.
func stashMuseDurableReceipt(owner, message, logPath string, baselineSeq int64, since time.Time) {
	owner = strings.TrimSpace(owner)
	message = strings.TrimSpace(message)
	if owner == "" || message == "" {
		return
	}
	key, err := musePersistentKey(owner)
	if err != nil {
		return
	}
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		return
	}
	kept := entry.pendingDurable[:0]
	for _, pending := range entry.pendingDurable {
		if time.Since(pending.since) < musePendingDurableAckTTL {
			kept = append(kept, pending)
		}
	}
	// Occurrence is send-ordered: stashes run inside the serialized
	// send, so 1 + identical unexpired already pending is this send's
	// position among identical queued sends.
	occurrence := 1
	for _, pending := range kept {
		if pending.message == message {
			occurrence++
		}
	}
	kept = append(kept, musePendingDurableAck{message: message, logPath: logPath, baselineSeq: baselineSeq, since: since, occurrence: occurrence})
	if len(kept) > museMaxPendingDurableAcks {
		kept = append([]musePendingDurableAck(nil), kept[len(kept)-museMaxPendingDurableAcks:]...)
	}
	entry.pendingDurable = kept
}

// peekMuseDurableReceipt returns the earliest pending snapshot for an
// identical message without consuming it. Production waits use
// takeMuseDurableReceipt; peek remains for introspection and tests.
func peekMuseDurableReceipt(owner, message string) (musePendingDurableAck, bool) {
	owner = strings.TrimSpace(owner)
	message = strings.TrimSpace(message)
	key, err := musePersistentKey(owner)
	if err != nil {
		return musePendingDurableAck{}, false
	}
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		return musePendingDurableAck{}, false
	}
	for _, pending := range entry.pendingDurable {
		if pending.message == message && time.Since(pending.since) < musePendingDurableAckTTL {
			return pending, true
		}
	}
	return musePendingDurableAck{}, false
}

// takeMuseDurableReceipt removes and returns the earliest pending
// snapshot for an identical message. FIFO consumption plus the
// receipt's occurrence binds each wait to distinct proof: the nth
// take needs the nth matching row, so identical sends queued before
// any row lands (same baseline on every receipt) cannot share one
// row. Takes serialize under the pool lock, but take order is
// acquisition order, not send order — the occurrence threshold keeps
// k takes needing k distinct rows however watchers schedule.
func takeMuseDurableReceipt(owner, message string) (musePendingDurableAck, bool) {
	owner = strings.TrimSpace(owner)
	message = strings.TrimSpace(message)
	key, err := musePersistentKey(owner)
	if err != nil {
		return musePendingDurableAck{}, false
	}
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		return musePendingDurableAck{}, false
	}
	for i, pending := range entry.pendingDurable {
		if pending.message == message && time.Since(pending.since) < musePendingDurableAckTTL {
			out := pending
			entry.pendingDurable = append(entry.pendingDurable[:i], entry.pendingDurable[i+1:]...)
			return out, true
		}
	}
	return musePendingDurableAck{}, false
}

// museUserAckRowOccurrence returns the occurrence-th native accepted intent
// above minSeq with the exact send text. Queue and assistant rows may copy the
// same text, but neither is a distinct acceptance and must not confirm a
// second identical send.
func museUserAckRowOccurrence(logPath, message string, minSeq int64, occurrence int) (rowSeq int64, rowTime time.Time, found bool) {
	if occurrence < 1 {
		occurrence = 1
	}
	want := strings.TrimSpace(museTerminalPrompt(message))
	if strings.TrimSpace(logPath) == "" || want == "" {
		return 0, time.Time{}, false
	}
	f, err := os.Open(logPath)
	if err != nil {
		return 0, time.Time{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	matched := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if !strings.Contains(string(line), `"runtime.user_intent.accepted"`) {
			continue
		}
		var env museIntakeRecord
		if json.Unmarshal(line, &env) != nil || env.Sequence <= minSeq || env.PayloadType != "runtime.user_intent.accepted" {
			continue
		}
		var accepted strings.Builder
		for _, block := range env.Payload.RefillBlocks {
			if block.Kind == "text" {
				accepted.WriteString(block.Text)
			}
		}
		if strings.TrimSpace(accepted.String()) != want {
			continue
		}
		matched++
		if matched == occurrence {
			at := time.UnixMicro(env.RecordedAt)
			if env.RecordedAt == 0 {
				at = time.Now()
			}
			return env.Sequence, at, true
		}
	}
	return 0, time.Time{}, false
}

// museUserAckRow returns the first accepted intent above minSeq carrying the
// exact message text.
// text; sequence scoping excludes identical earlier messages, and the
// per-session log path excludes other sessions' traffic, so no row
// typing is needed to stay sound.
func museUserAckRow(logPath, message string, minSeq int64) (rowSeq int64, rowTime time.Time, found bool) {
	return museUserAckRowOccurrence(logPath, message, minSeq, 1)
}

// musePaneShowsQueuedMessage reports that message is sitting in Muse's
// native queue right now: a "Queued input" banner with the message
// text next to it (observed live layout: "• Queued input" plus a
// "↳ <text>" line). No completion disqualifier is needed — this only
// runs when no transcript row matched, and intake always precedes any
// answer, so a visible queue with no row is genuinely still held.
func musePaneShowsQueuedMessage(captured, message string) bool {
	needle := strings.TrimSpace(strings.Split(strings.TrimSpace(message), "\n")[0])
	if len([]rune(needle)) > 80 {
		needle = string([]rune(needle)[:80])
	}
	if needle == "" {
		return false
	}
	lines := strings.Split(captured, "\n")
	const window = 120
	start := 0
	if len(lines) > window {
		start = len(lines) - window
	}
	for i := start; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if !strings.Contains(line, "Queued input") {
			continue
		}
		if strings.Contains(line, needle) {
			return true
		}
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			if strings.Contains(strings.TrimSpace(lines[j]), needle) {
				return true
			}
		}
	}
	return false
}

// musePaneHoldsDraft reports whether the composer's visible text still
// contains the message's first line. It errs toward held, which is the
// safe direction for the unflushed verdict (held-not-failed).
func musePaneHoldsDraft(captured, message string) bool {
	needle := strings.TrimSpace(strings.Split(strings.TrimSpace(message), "\n")[0])
	if len([]rune(needle)) > 80 {
		needle = string([]rune(needle)[:80])
	}
	if needle == "" {
		return false
	}
	for _, line := range strings.Split(captured, "\n") {
		if strings.Contains(strings.TrimSpace(line), needle) {
			return true
		}
	}
	return false
}

// musePaneLooksActive reports a turn in flight: a running tool block or
// a live thinking/composing indicator. Deliberately not museTUIAtPrompt —
// Muse keeps the composer painted while streaming, so at-prompt stays
// true mid-turn and cannot distinguish idle from busy.
func musePaneLooksActive(captured string) bool {
	return musePaneHasRunningTool(captured) || strings.Contains(captured, "esc to interrupt")
}

// museDurableAckPoll tunes pollMuseDurableAck. Zero values select live
// defaults; tests inject resolve/capture and a shorter interval.
// Occurrence is the taken receipt's 1-based position among identical
// queued sends: the wait needs the occurrence-th matching row. Values
// below 1 mean 1.
type museDurableAckPoll struct {
	resolve     func() string
	capture     func(context.Context, string) (string, error)
	sessionName string
	interval    time.Duration
	occurrence  int
}

// pollMuseDurableAck waits observe-only for the transcript row matching
// one send. It never sends keys: on entry the message may already be
// accepted, so any keystroke risks duplicating it. On budget expiry it
// distinguishes still-held (natively queued, or taken by an active
// turn) from truly lost via one final pane read.
func pollMuseDurableAck(ctx context.Context, message string, since time.Time, minSeq int64, timeout time.Duration, poll museDurableAckPoll) (MuseDurableAck, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	interval := poll.interval
	if interval <= 0 {
		interval = museDurableAckPollInterval
	}
	occurrence := poll.occurrence
	if occurrence < 1 {
		occurrence = 1
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	check := func() (int64, time.Time, bool) {
		if poll.resolve == nil {
			return 0, time.Time{}, false
		}
		path := poll.resolve()
		if strings.TrimSpace(path) == "" {
			return 0, time.Time{}, false
		}
		return museUserAckRowOccurrence(path, message, minSeq, occurrence)
	}
	if _, at, ok := check(); ok {
		return MuseDurableAck{Outcome: MuseDurableAckConfirmed, Latency: time.Since(since), ProofPath: poll.resolve(), RowTimestamp: at}, nil
	}
	for {
		select {
		case <-deadline.Done():
			if poll.capture != nil && strings.TrimSpace(poll.sessionName) != "" {
				captureCtx, captureCancel := context.WithTimeout(ctx, museDurableAckCaptureTimeout)
				captured, captureErr := poll.capture(captureCtx, poll.sessionName)
				captureCancel()
				if captureErr == nil {
					if musePaneShowsQueuedMessage(captured, message) {
						return MuseDurableAck{Outcome: MuseDurableAckUnflushed, Latency: time.Since(since)}, nil
					}
					if !musePaneHoldsDraft(captured, message) && musePaneLooksActive(captured) {
						return MuseDurableAck{Outcome: MuseDurableAckUnflushed, Latency: time.Since(since)}, nil
					}
				}
			}
			if ctx.Err() != nil {
				return MuseDurableAck{}, ctx.Err()
			}
			return MuseDurableAck{}, fmt.Errorf("Muse input not durably acknowledged after %s", timeout)
		case <-ticker.C:
			if _, at, ok := check(); ok {
				return MuseDurableAck{Outcome: MuseDurableAckConfirmed, Latency: time.Since(since), ProofPath: poll.resolve(), RowTimestamp: at}, nil
			}
		}
	}
}

// museResolveDurableLogPath returns the pooled transcript path for an
// owner without holding the pool lock across I/O.
func museResolveDurableLogPath(owner string) string {
	owner = strings.TrimSpace(owner)
	key, err := musePersistentKey(owner)
	if err != nil {
		return ""
	}
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		return ""
	}
	if entry.logPath != "" {
		return entry.logPath
	}
	return museSessionLogPath(entry.nativeSessionID, entry.accountDataHome)
}

// museArbiterAfterSubmitFailure is the long-budget observe-only
// transcript arbitration after the pane submitter failed. It never
// sends keys. A transcript match converts the failure into success
// (logging the disagreement as provider drift); a still-held pane
// becomes accepted-but-unflushed; anything else keeps the original
// error with the arbiter note attached.
func museArbiterAfterSubmitFailure(ctx context.Context, ownerSessionID, sessionName, message string, since time.Time, minSeq int64, submitErr error) error {
	// Consume this send's receipt like the other adapters' arbiters: the
	// sender stashed it just before the failed submit, and leaving it
	// pending would let a later identical send's watcher take this
	// send's (wider) baseline and match this send's row. On a take miss
	// the caller's snapshot is this send's own scope, so it stays a
	// sound fallback.
	occurrence := 1
	if receipt, ok := takeMuseDurableReceipt(ownerSessionID, message); ok {
		since = receipt.since
		minSeq = receipt.baselineSeq
		occurrence = receipt.occurrence
	}
	budget := museDurableAckBudget()
	ack, err := pollMuseDurableAck(ctx, message, since, minSeq, budget, museDurableAckPoll{
		resolve:     func() string { return museResolveDurableLogPath(ownerSessionID) },
		capture:     museTmuxCapturePane,
		sessionName: sessionName,
		occurrence:  occurrence,
	})
	if err == nil && ack.Outcome == MuseDurableAckConfirmed {
		log.Printf("[muse-durable-ack] PANE/FILE DISAGREE owner=%s latency=%dms proof=%s submit=%v",
			ownerSessionID, ack.Latency.Milliseconds(), ack.ProofPath, submitErr)
		return nil
	}
	if err == nil && ack.Outcome == MuseDurableAckUnflushed {
		return &MuseInputUnflushedError{OwnerSessionID: ownerSessionID, Latency: ack.Latency}
	}
	if ctx.Err() != nil {
		return submitErr
	}
	return fmt.Errorf("%w; durable arbiter found no transcript proof within %s", submitErr, budget)
}

// AwaitMuseInputDurable waits for the transcript proof that a previous
// SendMuseInteractiveInput reached the CLI. It is the durability half
// of the two-stage delivery receipt (fast pane ack, then this): the
// server watcher calls it after every fast ack and promotes the chat
// tick on confirmation. A zero timeout selects the env-tuned budget.
func AwaitMuseInputDurable(ctx context.Context, ownerSessionID, message string, timeout time.Duration) (MuseDurableAck, error) {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return MuseDurableAck{}, fmt.Errorf("owner session id is required")
	}
	if strings.TrimSpace(message) == "" {
		return MuseDurableAck{}, fmt.Errorf("message is empty")
	}
	if timeout <= 0 {
		timeout = museDurableAckBudget()
	}
	if s := museMSPSessionFor(ownerSessionID); s != nil {
		// turn/steer has answered. A turn that ends without reading the message
		// runs it next (museMSPMissedSteers), so it is taken either way.
		return s.steerReceipt(message)
	}
	key, err := musePersistentKey(ownerSessionID)
	if err != nil {
		return MuseDurableAck{}, err
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		musePersistentPool.Unlock()
		return MuseDurableAck{}, fmt.Errorf("no active Muse interactive session registered for owner session %s", ownerSessionID)
	}
	tmuxName := entry.tmuxName
	musePersistentPool.Unlock()
	receipt, ok := takeMuseDurableReceipt(ownerSessionID, message)
	awaitStart := time.Now()
	since := awaitStart.Add(-2 * time.Minute)
	var minSeq int64
	occurrence := 1
	if ok {
		since = receipt.since
		minSeq = receipt.baselineSeq
		occurrence = receipt.occurrence
	}
	ack, err := pollMuseDurableAck(ctx, message, since, minSeq, timeout, museDurableAckPoll{
		resolve:     func() string { return museResolveDurableLogPath(ownerSessionID) },
		capture:     museTmuxCapturePane,
		sessionName: tmuxName,
		occurrence:  occurrence,
	})
	if err != nil {
		return MuseDurableAck{}, err
	}
	// With no pending receipt the search window starts two minutes back, so the poll reports that offset as
	// latency (a constant 120 s on every receipt); report the time actually waited.
	if !ok {
		if waited := time.Since(awaitStart); ack.Latency > waited {
			ack.Latency = waited
		}
	}
	if ack.Outcome == MuseDurableAckConfirmed {
		log.Printf("[muse-durable-ack] owner=%s latency=%dms proof=%s", ownerSessionID, ack.Latency.Milliseconds(), ack.ProofPath)
	} else {
		log.Printf("[muse-durable-ack] owner=%s UNFLUSHED after %dms (still held by Muse)", ownerSessionID, ack.Latency.Milliseconds())
	}
	return ack, nil
}
