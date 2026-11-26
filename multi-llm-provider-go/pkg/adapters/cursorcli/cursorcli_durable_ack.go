package cursorcli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Durable submit confirmation for the Cursor tmux lane: the observe-only
// store.db arbiter behind AwaitCursorInputDurable.
//
// Live evidence (2026-09-19 probe, auto model): an idle steer commits
// its <user_query> blob ~9.5s after send-keys. Cursor's blobs carry no
// per-message timestamps, so ref-identity (not time/offset) scopes one
// send's proof: the pre-send snapshot of latest-root refs, plus exact
// text, means an identical earlier message already in the store cannot
// confirm a new one. Verdicts:
//
//   - user_query row under a new ref -> confirmed (durably committed)
//   - no new row at budget expiry but the pane still shows the message
//     natively queued -> accepted_but_unflushed (held, not failed)
//   - neither -> timeout error (failed)
//
// NOTE (live-unverified 2026-09-19): login quota exhausted before the
// busy-steer probe, so the busy commit path and the queued-followups
// pane matcher below are shaped by code inspection + fixtures only.
// Re-verify live (TestCursorCLIRealDurableAckContract) once quota resets.

type CursorDurableAckOutcome string

const (
	// CursorDurableAckConfirmed means the session store holds the
	// user_query row for this send.
	CursorDurableAckConfirmed CursorDurableAckOutcome = "confirmed"
	// CursorDurableAckUnflushed means the budget expired while the pane
	// still positively showed the message natively queued. The CLI
	// holds it; durability is unconfirmed, not failed.
	CursorDurableAckUnflushed CursorDurableAckOutcome = "accepted_but_unflushed"
)

// CursorDurableAck carries the store proof for one send. RowTimestamp is
// always zero: cursor blobs carry no per-message timestamps.
type CursorDurableAck struct {
	Outcome      CursorDurableAckOutcome
	Latency      time.Duration
	ProofPath    string
	RowTimestamp time.Time
}

// CursorInputUnflushedError reports accepted-but-unflushed through the
// error return so existing err != nil callers stay safe (they treat it
// as a failure) while confirmation-aware callers can distinguish it
// with errors.As.
type CursorInputUnflushedError struct {
	OwnerSessionID string
	Latency        time.Duration
}

func (e *CursorInputUnflushedError) Error() string {
	return fmt.Sprintf("Cursor input accepted but unflushed after %s (still natively queued; durability unconfirmed, not failed)", e.Latency.Round(100*time.Millisecond))
}

const (
	// Default 180s: idle commit measured at ~9.5s but the busy path is
	// unverified (quota). The watch is secondary (never blocks the
	// user), so the generous budget only delays a verdict that would
	// otherwise be a false negative. Revisit after the live P0.
	cursorDurableAckBudgetDefault = 180 * time.Second
	cursorDurableAckBudgetMin     = 5 * time.Second
	cursorDurableAckBudgetMax     = 300 * time.Second
	cursorDurableAckPollInterval  = time.Second
	// cursorDurableAckCaptureTimeout bounds the final pane read on
	// budget expiry. It runs under a fresh context derived from the
	// poll's parent (the budget deadline is expired there by
	// construction); 5s is generous for one tmux scrape.
	cursorDurableAckCaptureTimeout = 5 * time.Second
)

// cursorDurableAckBudget bounds the observe-only store arbiter.
// CURSOR_DURABLE_ACK_SECONDS overrides the default; values outside
// [5,300] are clamped so a broken env cannot hang sends or neuter
// the arbiter.
func cursorDurableAckBudget() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CURSOR_DURABLE_ACK_SECONDS"))
	if raw == "" {
		return cursorDurableAckBudgetDefault
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return cursorDurableAckBudgetDefault
	}
	budget := time.Duration(seconds) * time.Second
	if budget < cursorDurableAckBudgetMin {
		return cursorDurableAckBudgetMin
	}
	if budget > cursorDurableAckBudgetMax {
		return cursorDurableAckBudgetMax
	}
	return budget
}

// cursorPendingDurableAck is the pre-send snapshot that scopes one send's
// durability proof. Baseline holds the latest-root refs already present;
// only rows under NEW refs can confirm, which self-disambiguates repeats
// without timestamps. Occurrence disambiguates identical sends queued
// before any row commits: the nth stashed send needs the nth matching
// new-ref row.
type cursorPendingDurableAck struct {
	message    string
	storeDB    string
	baseline   map[string]struct{}
	since      time.Time
	occurrence int
}

const (
	cursorMaxPendingDurableAcks = 8
	cursorPendingDurableAckTTL  = 10 * time.Minute
)

func stashCursorDurableReceipt(session *cursorInteractiveSession, message, storeDB string, baseline map[string]struct{}, since time.Time) {
	if session == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	session.durableMu.Lock()
	defer session.durableMu.Unlock()
	kept := session.pendingDurable[:0]
	for _, pending := range session.pendingDurable {
		if time.Since(pending.since) < cursorPendingDurableAckTTL {
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
	kept = append(kept, cursorPendingDurableAck{message: message, storeDB: storeDB, baseline: baseline, since: since, occurrence: occurrence})
	if len(kept) > cursorMaxPendingDurableAcks {
		kept = append([]cursorPendingDurableAck(nil), kept[len(kept)-cursorMaxPendingDurableAcks:]...)
	}
	session.pendingDurable = kept
}

// peekCursorDurableReceipt returns the earliest pending snapshot for an
// identical message without consuming it. Production waits use
// takeCursorDurableReceipt; peek remains for introspection and tests.
func peekCursorDurableReceipt(session *cursorInteractiveSession, message string) (cursorPendingDurableAck, bool) {
	if session == nil {
		return cursorPendingDurableAck{}, false
	}
	message = strings.TrimSpace(message)
	session.durableMu.Lock()
	defer session.durableMu.Unlock()
	for _, pending := range session.pendingDurable {
		if pending.message == message && time.Since(pending.since) < cursorPendingDurableAckTTL {
			return pending, true
		}
	}
	return cursorPendingDurableAck{}, false
}

// takeCursorDurableReceipt removes and returns the earliest pending
// snapshot for an identical message. FIFO consumption plus the
// receipt's occurrence binds each wait to distinct proof: the nth
// take needs the nth matching new-ref row, so identical sends queued
// before any row commits (same baseline on every receipt) cannot
// share one row. Takes serialize under this lock, but take order is
// acquisition order, not send order — the occurrence threshold keeps
// k takes needing k distinct rows however watchers schedule.
func takeCursorDurableReceipt(session *cursorInteractiveSession, message string) (cursorPendingDurableAck, bool) {
	if session == nil {
		return cursorPendingDurableAck{}, false
	}
	message = strings.TrimSpace(message)
	session.durableMu.Lock()
	defer session.durableMu.Unlock()
	for i, pending := range session.pendingDurable {
		if pending.message == message && time.Since(pending.since) < cursorPendingDurableAckTTL {
			out := pending
			session.pendingDurable = append(session.pendingDurable[:i], session.pendingDurable[i+1:]...)
			return out, true
		}
	}
	return cursorPendingDurableAck{}, false
}

func cursorUserQueryMatches(rowText, message string) bool {
	rowText = strings.TrimSpace(rowText)
	message = strings.TrimSpace(message)
	if rowText == message {
		return true
	}
	const prefixLen = 200
	if len(message) > prefixLen && strings.HasPrefix(rowText, message[:prefixLen]) {
		return true
	}
	return false
}

// cursorSnapshotStoreRefs returns the current latest-root refs of storeDB,
// or nil when the store is unreadable. Read-only open: WAL-safe against
// the live CLI writer.
func cursorSnapshotStoreRefs(storeDB string) map[string]struct{} {
	refs := make(map[string]struct{})
	if strings.TrimSpace(storeDB) == "" {
		return refs
	}
	db, err := sql.Open("sqlite", "file:"+storeDB+"?mode=ro")
	if err != nil {
		return refs
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	latest, err := cursorStoreLatestRootRefs(ctx, db)
	if err != nil {
		return refs
	}
	for _, ref := range latest {
		refs[ref] = struct{}{}
	}
	return refs
}

// cursorStoreUserQueryCountSince counts the user_query rows with our text
// under refs NOT in baseline (committed after the send snapshot). A
// nil baseline matches any row — no repeat protection, only for
// callers without a receipt. A missing/unreadable store simply has no
// proof yet.
func cursorStoreUserQueryCountSince(storeDB, message string, baseline map[string]struct{}) int {
	if strings.TrimSpace(storeDB) == "" || strings.TrimSpace(message) == "" {
		return 0
	}
	db, err := sql.Open("sqlite", "file:"+storeDB+"?mode=ro")
	if err != nil {
		return 0
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	refs, err := cursorStoreLatestRootRefs(ctx, db)
	if err != nil {
		return 0
	}
	matched := 0
	for _, ref := range refs {
		if _, seen := baseline[ref]; seen {
			continue
		}
		data := readCursorBlob(ctx, db, ref)
		if len(data) == 0 || data[0] != '{' {
			continue
		}
		var msg cursorMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
			continue
		}
		if query := cursorUserQueryFromContent(msg.Content); query != "" && cursorUserQueryMatches(query, message) {
			matched++
		}
	}
	return matched
}

// cursorStoreUserQuerySince reports whether the store holds a user_query
// row with our text under a ref NOT in baseline (committed after the
// send snapshot). A nil baseline matches any row — no repeat protection,
// only for callers without a receipt. A missing/unreadable store simply
// has no proof yet.
func cursorStoreUserQuerySince(storeDB, message string, baseline map[string]struct{}) bool {
	return cursorStoreUserQueryCountSince(storeDB, message, baseline) >= 1
}

// cursorPaneShowsQueuedMessage reports that message is sitting in Cursor's
// native follow-ups queue right now: the queued-followups banner with the
// message text visible. LIVE-UNVERIFIED (quota): shaped by the banner
// helpers + fixtures; confirm against a real busy pane after 9/21.
func cursorPaneShowsQueuedMessage(captured, message string) bool {
	if !hasCursorQueuedFollowupsSendPrompt(captured) {
		return false
	}
	needle := strings.TrimSpace(strings.Split(strings.TrimSpace(message), "\n")[0])
	if len(needle) > 80 {
		needle = needle[:80]
	}
	if needle == "" {
		return false
	}
	return strings.Contains(stripCursorANSI(cursorVisiblePaneText(captured)), needle)
}

// cursorDurableAckPoll tunes pollCursorDurableAck. Zero values select live
// defaults; tests inject resolve/capture and a shorter interval.
// Occurrence is the taken receipt's 1-based position among identical
// queued sends: the wait needs the occurrence-th matching new-ref row.
// Values below 1 mean 1.
type cursorDurableAckPoll struct {
	resolve     func() string
	capture     func(context.Context, string) (string, error)
	sessionName string
	interval    time.Duration
	occurrence  int
	// submit presses Enter once in the session. Nil disables draft recovery.
	submit func(context.Context, string) error
}

var (
	// A paste followed at once by Enter can leave the text in an idle composer
	// (a short steer sat unsent for 10 minutes and the
	// turn failed). Muse has the same recovery (PLAT-861-117).
	cursorDraftRecoveryAfter    = 3 * time.Second
	cursorDraftRecoveryInterval = 2 * time.Second
)

const cursorDraftRecoveryMax = 2

// cursorLostAfter is when a send that left no trace is reported as not
// delivered: no store row and no new store write, the CLI idle at its composer,
// the text neither in the composer nor queued, on two looks in a row. Before,
// such a send waited for the full budget and the reply for its idle limit.
var cursorLostAfter = 15 * time.Second

// CursorInputNotDeliveredError reports a send that left no trace in Cursor.
// It is a failure, not a timeout: the person is told to send again.
type CursorInputNotDeliveredError struct{ After time.Duration }

func (e *CursorInputNotDeliveredError) Error() string {
	return fmt.Sprintf("Cursor did not take the message: after %s it is idle, the text is not in its input and its record has nothing new", e.After.Round(time.Second))
}

// pollCursorDurableAck waits for the user_query row matching one send. The
// only key it may send is Enter, and only while the store has no row, the
// pane is the idle composer and that composer still holds this exact draft:
// an accepted message has left the composer, so the Enter cannot duplicate
// it, and nothing is ever retyped. On budget expiry it distinguishes
// still-queued (unflushed, held by the CLI) from truly lost via one final
// pane read.
func pollCursorDurableAck(ctx context.Context, message string, since time.Time, baseline map[string]struct{}, timeout time.Duration, poll cursorDurableAckPoll) (CursorDurableAck, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	interval := poll.interval
	if interval <= 0 {
		interval = cursorDurableAckPollInterval
	}
	occurrence := poll.occurrence
	if occurrence < 1 {
		occurrence = 1
	}
	deadline, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	resolve := poll.resolve
	if resolve == nil {
		resolve = func() string { return "" }
	}
	check := func() bool {
		path := resolve()
		if strings.TrimSpace(path) == "" {
			return false
		}
		return cursorStoreUserQueryCountSince(path, message, baseline) >= occurrence
	}
	if check() {
		return CursorDurableAck{Outcome: CursorDurableAckConfirmed, Latency: time.Since(since), ProofPath: resolve()}, nil
	}
	nextRecovery := time.Now().Add(cursorDraftRecoveryAfter)
	recoveries := 0
	recoverDraft := func() {
		if poll.submit == nil || poll.capture == nil || strings.TrimSpace(poll.sessionName) == "" ||
			recoveries >= cursorDraftRecoveryMax || time.Now().Before(nextRecovery) {
			return
		}
		nextRecovery = time.Now().Add(cursorDraftRecoveryInterval)
		captured, err := poll.capture(deadline, poll.sessionName)
		if err != nil || !hasCursorReadyPrompt(captured) || !cursorPaneShowsPromptDraftWithMode(captured, message, false) || check() {
			return
		}
		recoveries++
		log.Printf("[cursor-durable-ack] session=%s attempt=%d draft still in the idle composer; pressing Enter without retyping", poll.sessionName, recoveries)
		if err := poll.submit(deadline, poll.sessionName); err != nil {
			log.Printf("[cursor-durable-ack] session=%s Enter failed: %v", poll.sessionName, err)
		}
	}
	started := time.Now()
	lostLooks := 0
	nextLostLook := started.Add(cursorLostAfter)
	looksLost := func() bool {
		path := strings.TrimSpace(resolve())
		if poll.submit == nil || poll.capture == nil || baseline == nil || path == "" ||
			strings.TrimSpace(poll.sessionName) == "" || time.Now().Before(nextLostLook) {
			return false
		}
		nextLostLook = time.Now().Add(cursorDraftRecoveryInterval)
		for ref := range cursorSnapshotStoreRefs(path) {
			if _, seen := baseline[ref]; !seen {
				lostLooks = 0 // Cursor wrote something since the send: not lost.
				return false
			}
		}
		captured, err := poll.capture(deadline, poll.sessionName)
		if err != nil || !hasCursorReadyPrompt(captured) || cursorPaneShowsPromptDraftWithMode(captured, message, false) ||
			cursorPaneShowsQueuedMessage(captured, message) || check() {
			lostLooks = 0
			return false
		}
		lostLooks++
		return lostLooks >= 2
	}
	for {
		select {
		case <-deadline.Done():
			if poll.capture != nil && strings.TrimSpace(poll.sessionName) != "" {
				captureCtx, captureCancel := context.WithTimeout(ctx, cursorDurableAckCaptureTimeout)
				captured, captureErr := poll.capture(captureCtx, poll.sessionName)
				captureCancel()
				if captureErr == nil && cursorPaneShowsQueuedMessage(captured, message) {
					return CursorDurableAck{Outcome: CursorDurableAckUnflushed, Latency: time.Since(since)}, nil
				}
			}
			if ctx.Err() != nil {
				return CursorDurableAck{}, ctx.Err()
			}
			return CursorDurableAck{}, fmt.Errorf("Cursor input not durably acknowledged after %s", timeout)
		case <-ticker.C:
			if check() {
				return CursorDurableAck{Outcome: CursorDurableAckConfirmed, Latency: time.Since(since), ProofPath: resolve()}, nil
			}
			recoverDraft()
			if looksLost() {
				log.Printf("[cursor-durable-ack] session=%s message not delivered: no trace after %s", poll.sessionName, time.Since(started).Round(time.Second))
				return CursorDurableAck{}, &CursorInputNotDeliveredError{After: time.Since(started)}
			}
		}
	}
}

// resolveCursorStoreNoMu resolves the session store from
// immutable-after-construction identity without taking session.mu: the
// live send path and the durability watcher both run concurrent with a
// turn that owns mu for its full lifetime. retainedMu is safe: it is
// independent of mu by design for live input.
func resolveCursorStoreNoMu(session *cursorInteractiveSession) string {
	if session == nil {
		return ""
	}
	session.retainedMu.Lock()
	defer session.retainedMu.Unlock()
	return session.resolveRetainedStoreLocked()
}

// pinnedCursorStoreNoMu returns the session's store only if one is already
// pinned; unlike resolveCursorStoreNoMu it never discovers (and so never pins).
func pinnedCursorStoreNoMu(session *cursorInteractiveSession) string {
	if session == nil {
		return ""
	}
	session.retainedMu.Lock()
	defer session.retainedMu.Unlock()
	return session.retainedStoreDB
}

// stashCursorDurableReceiptForSend snapshots the store refs before a
// live-input send so the later durability watch only matches rows the
// CLI committed for this send.
func stashCursorDurableReceiptForSend(ownerSessionID, message string, since time.Time) {
	session, ok := cursorPersistentRegistry.Get(strings.TrimSpace(ownerSessionID))
	if !ok || session == nil {
		return
	}
	storeDB := resolveCursorStoreNoMu(session)
	stashCursorDurableReceipt(session, message, storeDB, cursorSnapshotStoreRefs(storeDB), since)
}

// InteractiveSessionRegistered reports whether the owner's Cursor TUI is
// registered — the live-injection transport exists. Cheap registry lookup
// (no tmux round-trip) for steer gating; delivery re-verifies liveness.
// Mirrors the SendCursorInteractiveInput registry.
func TmuxSessionRegistered(ownerSessionID string) bool {
	_, ok := activeCursorInteractiveSession(ownerSessionID)
	return ok
}

// AwaitCursorInputDurable waits for the store proof that a previous
// SendCursorInteractiveInput reached the CLI. It is the durability half
// of the two-stage delivery receipt (fast pane ack, then this): the
// server watcher calls it after every fast ack and promotes the chat
// tick on confirmation. A zero timeout selects the env-tuned budget.
func AwaitCursorInputDurable(ctx context.Context, ownerSessionID, message string, timeout time.Duration) (CursorDurableAck, error) {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return CursorDurableAck{}, fmt.Errorf("owner session id is required")
	}
	if strings.TrimSpace(message) == "" {
		return CursorDurableAck{}, fmt.Errorf("message is empty")
	}
	if timeout <= 0 {
		timeout = cursorDurableAckBudget()
	}
	session, ok := cursorPersistentRegistry.Get(ownerSessionID)
	if !ok || session == nil {
		return CursorDurableAck{}, fmt.Errorf("no active Cursor interactive session registered for owner session %s", ownerSessionID)
	}
	receipt, ok := takeCursorDurableReceipt(session, message)
	awaitStart := time.Now()
	since := awaitStart.Add(-2 * time.Minute)
	var baseline map[string]struct{}
	occurrence := 1
	if ok {
		since = receipt.since
		baseline = receipt.baseline
		occurrence = receipt.occurrence
	}
	tmuxName := session.tmuxSessionName
	ack, err := pollCursorDurableAck(ctx, message, since, baseline, timeout, cursorDurableAckPoll{
		// Re-pins to the chat that holds the message when the pinned one
		// does not (several chats can share the folder after a relaunch).
		resolve:     func() string { return resolveCursorStoreNoMu(session) },
		capture:     captureCursorPane,
		sessionName: tmuxName,
		occurrence:  occurrence,
		submit: func(ctx context.Context, sessionName string) error {
			return runCursorCommand(ctx, nil, "tmux", "send-keys", "-t", sessionName, "C-m")
		},
	})
	if err != nil {
		return CursorDurableAck{}, err
	}
	// With no pending receipt the search window starts two minutes back, so the poll reports that offset as
	// latency (a constant 120 s on every receipt); report the time actually waited.
	if !ok {
		if waited := time.Since(awaitStart); ack.Latency > waited {
			ack.Latency = waited
		}
	}
	if ack.Outcome == CursorDurableAckConfirmed {
		log.Printf("[cursor-durable-ack] owner=%s latency=%dms proof=%s", ownerSessionID, ack.Latency.Milliseconds(), ack.ProofPath)
	} else {
		log.Printf("[cursor-durable-ack] owner=%s UNFLUSHED after %dms (still natively queued)", ownerSessionID, ack.Latency.Milliseconds())
	}
	return ack, nil
}
