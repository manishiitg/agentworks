package musecli

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

var (
	// museInitializingClearWait bounds the pre-typing wait for a boot in
	// progress. Resume shows "Initializing runtime" for ~1-2s (live probe,
	// 2026-10-12); the gate is soft — on timeout typing proceeds and the
	// existing took-effect retry still catches swallowed keystrokes.
	museInitializingClearWait = 30 * time.Second
	// museResubmitShortConfirm bounds the fast intake/recovery window.
	// Unchanged drafts can receive a delayed Enter while native acceptance
	// and queued/running state always stop recovery.
	museResubmitShortConfirm = 20 * time.Second
	// museResubmitMaxRetries bounds Enter-only recovery of the same draft.
	museResubmitMaxRetries = 2
)

// musePaneInitializing reports the TUI boot marker. It shows while the
// runtime is still coming up, before the composer accepts input.
func musePaneInitializing(pane string) bool {
	return strings.Contains(pane, "Initializing")
}

// museWaitInitializingClear polls until the boot marker clears or the bound
// passes. It returns false on timeout or capture failure; callers proceed
// anyway — this is a fast-path guard, not readiness proof.
func museWaitInitializingClear(ctx context.Context, session string) bool {
	deadline := time.Now().Add(museInitializingClearWait)
	for {
		pane, err := museTmuxCapturePane(ctx, session)
		if err == nil && !musePaneInitializing(pane) {
			return true
		}
		if err := ctx.Err(); err != nil {
			return false
		}
		if time.Now().After(deadline) {
			log.Printf("[MUSE_SUBMIT_NOTICE] session=%s still initializing after %s; typing anyway", session, museInitializingClearWait)
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func museIntakeFound(dataHome string, since time.Time, snippet, workdir string) bool {
	if strings.TrimSpace(snippet) == "" {
		return false
	}
	_, _, err := museDiscoverSessionSince(dataHome, since, snippet, workdir)
	return err == nil
}

// musePaneHasExactDraft inspects only the bordered bottom editor. A matching
// user echo or stale refusal in scrollback cannot authorize another Enter.
func musePaneHasExactDraft(pane, prompt string) bool {
	lines := strings.Split(pane, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "❯") {
			start = i
		}
	}
	if start < 1 {
		return false
	}
	previous := start - 1
	for previous >= 0 && strings.TrimSpace(lines[previous]) == "" {
		previous--
	}
	if previous < 0 || !strings.HasPrefix(strings.TrimSpace(lines[previous]), "─") {
		return false
	}
	body := strings.TrimPrefix(strings.TrimSpace(lines[start]), "❯")
	for _, line := range lines[start+1:] {
		if strings.HasPrefix(strings.TrimSpace(line), "─") {
			// Capture wraps words and indents continuation rows. Compare the whole
			// text, not a shared first-line prefix such as the bridge resume note.
			actual := strings.Join(strings.Fields(body), "")
			expected := strings.Join(strings.Fields(museTerminalPrompt(prompt)), "")
			return expected != "" && actual == expected
		}
		body += "\n" + line
	}
	return false
}

// museConfirmSubmitOrResubmit recovers an Enter that only changed the editor
// (for example by adding newlines). Native intake always wins. Only an idle,
// exact bottom draft gets a bounded delayed Enter; nothing is cleared/retyped.
func museConfirmSubmitOrResubmit(ctx context.Context, dataHome, session, prompt string, turnStart time.Time, workdir string) error {
	snippet := promptSnippet(prompt)
	deadline := time.Now().Add(museResubmitShortConfirm)
	nextRetry := time.Now().Add(time.Second)
	retries := 0
	for {
		if museIntakeFound(dataHome, turnStart, snippet, workdir) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return nil
		} // long intake wait remains authoritative
		pane, err := museTmuxCapturePane(ctx, session)
		if err != nil || !museTUIAtPrompt(pane) ||
			musePaneInitializing(pane) || musePaneLooksActive(pane) ||
			museHooksReviewScreen(pane) || musePendingUserInputError(pane) != nil ||
			musePaneShowsQueuedMessage(pane, prompt) || !musePaneHasExactDraft(pane, prompt) {
			// Taken/queued/busy input stays with its native receipt watcher;
			// neither a missing pane nor a changed draft authorizes a key.
			return nil
		}
		if retries < museResubmitMaxRetries && time.Now().After(nextRetry) {
			// A late native receipt cancels recovery.
			if museIntakeFound(dataHome, turnStart, snippet, workdir) {
				return nil
			}
			retries++
			nextRetry = time.Now().Add(time.Second)
			log.Printf("[MUSE_RESUBMIT] session=%s attempt=%d exact draft still unsent; pressing Enter without retyping", session, retries)
			if out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "Enter").CombinedOutput(); err != nil {
				log.Printf("[MUSE_RESUBMIT] session=%s Enter failed: %v (output_bytes=%d); waiting for native intake", session, err, len(out))
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
