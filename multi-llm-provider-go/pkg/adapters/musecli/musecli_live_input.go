package musecli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Live-input transport for the muse tmux lane: steering follow-up text and
// control keys into the owner's pooled TUI. Muse was absent from both
// provider dispatches (structured-only era) — the layer-2 steer cert caught
// it: deliver failed with "provider has no live input transport".
//
// museSendPrompt is reused verbatim for follow-ups (chunked/paste routing +
// Enter) so steering delivery matches turn submission byte-for-byte. Both
// paths recover an idle stuck draft; native queued/running input returns
// immediately to its receipt watcher, and the caller tracks completion.

// musePersistentTmuxForOwner resolves the pooled tmux session name for an
// owner session id. False when nothing is pooled (never started, already
// killed, or key derivation failed).
func musePersistentTmuxForOwner(owner string) (string, bool) {
	key, err := musePersistentKey(owner)
	if err != nil {
		return "", false
	}
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	entry := musePersistentPool.m[key]
	if entry == nil || strings.TrimSpace(entry.tmuxName) == "" {
		return "", false
	}
	return entry.tmuxName, true
}

// InteractiveSessionRegistered reports whether the owner's pooled Muse TUI
// is registered — the live-injection transport exists. Cheap map lookup
// (no tmux round-trip) for steer gating; delivery re-verifies liveness.
func InteractiveSessionRegistered(ownerSessionID string) bool {
	if MuseMSPSessionActive(ownerSessionID) {
		return true
	}
	_, ok := musePersistentTmuxForOwner(ownerSessionID)
	return ok
}

// SendMuseInteractiveInput types a follow-up message into the owner's live
// pooled TUI and submits it.
func SendMuseInteractiveInput(ctx context.Context, ownerSessionID, message string) error {
	if MuseMSPSessionActive(ownerSessionID) {
		return SendMuseMSPInput(ctx, ownerSessionID, message)
	}
	key, err := musePersistentKey(ownerSessionID)
	if err != nil {
		return err
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		musePersistentPool.Unlock()
		return fmt.Errorf("no active Muse interactive session registered for owner session %s", ownerSessionID)
	}
	tmuxName := entry.tmuxName
	logPath := entry.logPath
	dataHome, workdir := entry.accountDataHome, entry.workdir
	userChoice := entry.userChoice
	if logPath == "" && entry.nativeSessionID != "" {
		logPath = museSessionLogPath(entry.nativeSessionID, entry.accountDataHome)
	}
	musePersistentPool.Unlock()
	if !museTmuxSessionAlive(ctx, tmuxName) {
		return fmt.Errorf("muse tmux session %q for owner %s is gone", tmuxName, ownerSessionID)
	}
	if userChoice {
		pane, err := museTmuxCapturePane(ctx, tmuxName)
		if err != nil {
			return err
		}
		if pendingErr := musePendingUserInputError(pane); pendingErr != nil {
			return pendingErr
		}
	}
	// entry.autoAnswer is only ever populated by the turn that first launched
	// this persistent session (generateContentTmux's museWithAutoAnswer +
	// museBindPersistentAutoAnswer). A live steer delivered before that has
	// happened -- or against a session whose launch predates this wiring --
	// found entry.autoAnswer nil and skipped auto-answer setup entirely, so a
	// native "Request user input" widget (e.g. Shape/Bounds) went straight to
	// museHandlePendingQuestion's raw pendingErr instead of being resolved:
	// live input steering surfaced "Live input unavailable: ... [user_input_required]"
	// even though the exact same widget auto-answers fine from a fresh turn.
	// Always enable auto-answer here too, and persist it the same way so a
	// later turn on this session reuses this state instead of re-creating it.
	if userChoice {
		ctx = museWithUserChoice(ctx)
	} else {
		ctx = museWithAutoAnswer(ctx, nil)
		ctx = museBindPersistentAutoAnswer(ctx, entry)
	}
	if err := museWaitLiveInputComposer(ctx, tmuxName); err != nil {
		return err
	}
	// Completing the old question can produce another assistant commit. The
	// new live turn starts after that boundary, not at the pre-dialog cursor.
	baseline := museTranscriptMaxSequence(logPath)
	since := time.Now()
	stashMuseDurableReceipt(ownerSessionID, message, logPath, baseline, since)
	musePersistentPool.Lock()
	if current := musePersistentPool.m[key]; current != nil && current.tmuxName == tmuxName {
		current.lastSubmittedPrompt = museTerminalPrompt(message)
	}
	musePersistentPool.Unlock()
	if err := museSendPrompt(ctx, tmuxName, message); err != nil {
		return museArbiterAfterSubmitFailure(ctx, ownerSessionID, tmuxName, message, since, baseline, err)
	}
	if err := museConfirmSubmitOrResubmit(ctx, dataHome, tmuxName, message, since, workdir); err != nil {
		return museArbiterAfterSubmitFailure(ctx, ownerSessionID, tmuxName, message, since, baseline, err)
	}
	musePersistentPool.Lock()
	if current := musePersistentPool.m[key]; current != nil && current.tmuxName == tmuxName {
		current.retainedBaselineSequence = baseline
		if current.logPath == "" {
			current.logPath = logPath
		}
	}
	musePersistentPool.Unlock()
	return nil
}

// SendMuseInteractiveControlKey injects a raw tmux key (Escape, C-c, Enter,
// Up, Down — see IsAllowedCodingAgentControlKey) into the owner's live
// pooled TUI.
func SendMuseInteractiveControlKey(ctx context.Context, ownerSessionID, key string) error {
	if MuseMSPSessionActive(ownerSessionID) {
		// A serve chat has no screen: Esc and Ctrl-C mean stop the turn.
		return InterruptMuseMSP(ctx, ownerSessionID)
	}
	tmuxName, ok := musePersistentTmuxForOwner(ownerSessionID)
	if !ok {
		return fmt.Errorf("no active Muse interactive session registered for owner session %s", ownerSessionID)
	}
	send := exec.CommandContext(ctx, "tmux", "send-keys", "-t", tmuxName, key)
	if out, err := send.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys %s: %w\n%s", key, err, out)
	}
	if key == "Escape" {
		poolKey, err := musePersistentKey(ownerSessionID)
		if err == nil {
			musePersistentPool.Lock()
			if current := musePersistentPool.m[poolKey]; current != nil && current.tmuxName == tmuxName {
				current.stoppedPrompt = current.lastSubmittedPrompt
			}
			musePersistentPool.Unlock()
		}
	}
	return nil
}

// A visible native question owns the keyboard even though Muse paints an
// empty composer underneath it. Drain those dialogs before typing user text.
func museWaitLiveInputComposer(ctx context.Context, session string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		pane, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			return err
		}
		pending, err := museHandlePendingQuestion(ctx, session, pane)
		if err != nil {
			return err
		}
		if !pending {
			if musePaneShowsBlockingGate(pane) {
				return fmt.Errorf("Muse is waiting at a trust/auth prompt; live input was not typed")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
