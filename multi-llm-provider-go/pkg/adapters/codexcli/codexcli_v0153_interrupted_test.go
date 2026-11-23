package codexcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Captured live from Codex 0.153.4 after the previous turn had been aborted:
// the first Enter only dismissed the interrupted notice, so the new prompt is
// still an unsent draft in the composer and nothing is running.
const codex0153InterruptedDraftPane = `› Reply with exactly: astra medium works.
• astra medium works.
• You have 2 usage limit resets available. Run /usage to use one.
■ Conversation interrupted - tell the model what to do differently. Something went wrong? Hit ` + "`/feedback`" + ` to report the
issue.
• You have 2 usage limit resets available. Run /usage to use one.
› Reply with exactly: codex 0.153 confirmed.
  gpt-6-astra medium · ~/Library/Application Support/sparkquill-desktop/workspace-docs/_users/default/Chats/SparkQuill
`

// The same pane a few seconds after a second Enter: the prompt moved into the
// transcript and the turn is running above the empty composer.
const codex0153ResubmittedPane = `■ Conversation interrupted - tell the model what to do differently. Something went wrong? Hit ` + "`/feedback`" + ` to report the
issue.
• You have 2 usage limit resets available. Run /usage to use one.
› Reply with exactly: codex 0.153 confirmed.
Working (0s • esc to interrupt)
› Ask Codex to do anything
  gpt-6-astra medium · ~/Library/Application Support/sparkquill-desktop/workspace-docs/_users/default/Chats/SparkQuill
`

const codex0153InterruptedPrompt = "Reply with exactly: codex 0.153 confirmed."

// Captured live ~2.7s after `codex resume`: the composer placeholder is
// already drawn under the resuming banner.
const codex0153ResumingPane = `╰────────────────────────────────────────────────────────╯
  Resuming session…
› Ask Codex to do anything
  ? for shortcuts
`

// The same pane ~1s later, once the thread had loaded.
const codex0153ResumedPane = `Agent pid 17708
Identity added: /Users/mipl/.ssh/id_ed25519 (user@example.com)
› Ask Codex to do anything
  gpt-6-astra medium · ~/Library/Application Support/sparkquill-desktop/workspace-docs/_users/default/Chats/SparkQuill
`

func TestCodex0153ResumingBannerIsNotAReadyComposer(t *testing.T) {
	if !codexPaneIsResuming(codex0153ResumingPane) {
		t.Fatal("the resuming banner was not recognized")
	}
	if hasCodexReadyPrompt(codex0153ResumingPane) || codexPaneHasEmptyComposer(codex0153ResumingPane) {
		t.Fatal("a pane still resuming must not read as a ready or empty composer")
	}
	if codexPaneIsResuming(codex0153ResumedPane) {
		t.Fatal("a resumed pane was still treated as resuming")
	}
	if !hasCodexReadyPrompt(codex0153ResumedPane) || !codexPaneHasEmptyComposer(codex0153ResumedPane) {
		t.Fatal("the resumed pane must read as ready")
	}
}

func TestWaitForCodexPaneOutOfStartupHoldsWhileResuming(t *testing.T) {
	var polls int32
	capture := func(context.Context, string) (string, error) {
		switch atomic.AddInt32(&polls, 1) {
		case 1:
			return "", nil
		case 2, 3:
			return codex0153ResumingPane, nil
		default:
			return codex0153ResumedPane, nil
		}
	}
	start := time.Now()
	waitForCodexPaneOutOfStartup(context.Background(), "resume", 5*time.Second, capture)
	if got := atomic.LoadInt32(&polls); got != 4 {
		t.Fatalf("returned after %d polls, want 4 (blank, resuming, resuming, ready)", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("waited far longer than the three 100ms polls")
	}

	atomic.StoreInt32(&polls, 0)
	stuck := func(context.Context, string) (string, error) {
		atomic.AddInt32(&polls, 1)
		return codex0153ResumingPane, nil
	}
	start = time.Now()
	waitForCodexPaneOutOfStartup(context.Background(), "stuck", 250*time.Millisecond, stuck)
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("timeout not honored: %s", elapsed)
	}
	if !hasCodexReadyPrompt(codex0153IdlePane) {
		t.Fatal("the idle pane fixture must still read as ready")
	}
}

func TestCodex0153InterruptedNoticeLeavesAnUnsentDraft(t *testing.T) {
	if !codexPaneHoldsUnsentDraft(codex0153InterruptedDraftPane, codex0153InterruptedPrompt) {
		t.Fatal("the prompt sitting in the composer under the interrupted notice must read as an unsent draft")
	}
	if codexPaneHoldsUnsentDraft(codex0153ResubmittedPane, codex0153InterruptedPrompt) {
		t.Fatal("a running turn must not read as an unsent draft")
	}
	if codexPaneHoldsUnsentDraft(codex0153IdlePane, codex0153InterruptedPrompt) {
		t.Fatal("an idle pane without the prompt must not read as an unsent draft")
	}
}

func TestWaitForCodexInputSubmittedPressesEnterAgainForAnUnsentDraft(t *testing.T) {
	var resubmits int32
	capture := func(context.Context, string) (string, error) {
		if atomic.LoadInt32(&resubmits) == 0 {
			return codex0153InterruptedDraftPane, nil
		}
		return codex0153ResubmittedPane, nil
	}
	err := waitForCodexInputSubmittedWith(context.Background(), "interrupted", codex0153InterruptedPrompt, codex0153IdlePane, 2*time.Second, codexSubmissionWait{
		capture:       capture,
		resubmitAfter: 20 * time.Millisecond,
		resubmit: func(context.Context) error {
			atomic.AddInt32(&resubmits, 1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("resubmitted draft was not confirmed: %v", err)
	}
	if got := atomic.LoadInt32(&resubmits); got != 1 {
		t.Fatalf("Enter pressed again %d times, want 1", got)
	}
}

func TestWaitForCodexInputSubmittedDoesNotConfirmAnUnsentDraft(t *testing.T) {
	capture := func(context.Context, string) (string, error) {
		return codex0153InterruptedDraftPane, nil
	}
	err := waitForCodexInputSubmittedWith(context.Background(), "interrupted", codex0153InterruptedPrompt, codex0153IdlePane, 150*time.Millisecond, codexSubmissionWait{capture: capture})
	if err == nil {
		t.Fatal("an unsent draft under the interrupted notice was reported as submitted")
	}
	if !strings.Contains(err.Error(), "remained unconfirmed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForCodexInputSubmittedTrustsTheRolloutOverThePane(t *testing.T) {
	var polls int32
	oracle := func() (bool, bool) {
		return atomic.AddInt32(&polls, 1) >= 4, true
	}
	capture := func(context.Context, string) (string, error) {
		return codex0153WorkingPane, nil
	}
	start := time.Now()
	err := waitForCodexInputSubmittedWith(context.Background(), "strict", "Count from 1 to 5, one number per line.", codex0153IdlePane, 2*time.Second, codexSubmissionWait{
		oracle:  oracle,
		capture: capture,
	})
	if err != nil {
		t.Fatalf("rollout-confirmed submission failed: %v", err)
	}
	if got := atomic.LoadInt32(&polls); got < 4 {
		t.Fatalf("pane heuristics confirmed after %d oracle polls; an authoritative rollout must decide", got)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("confirmation returned before the rollout reported the turn")
	}

	fallback := func() (bool, bool) { return false, false }
	if err := waitForCodexInputSubmittedWith(context.Background(), "fallback", "Count from 1 to 5, one number per line.", codex0153IdlePane, time.Second, codexSubmissionWait{
		oracle:  fallback,
		capture: capture,
	}); err != nil {
		t.Fatalf("without a bound rollout the pane heuristics must still confirm: %v", err)
	}

	captureErr := func(context.Context, string) (string, error) { return "", errors.New("tmux gone") }
	if err := waitForCodexInputSubmittedWith(context.Background(), "gone", "x", "", 100*time.Millisecond, codexSubmissionWait{capture: captureErr}); err == nil {
		t.Fatal("capture failures must not confirm a submission")
	}
}

func writeCodexTurnRollout(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-2026-09-06T12-06-44-thread.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func rolloutRow(at time.Time, payload string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":%s}`, at.UTC().Format(time.RFC3339Nano), payload)
}

func TestCodexRolloutTurnStartedSince(t *testing.T) {
	sent := time.Now()
	path := writeCodexTurnRollout(t,
		`{"type":"session_meta","payload":{"id":"thread","cwd":"/tmp/w"}}`,
		rolloutRow(sent.Add(-4*time.Minute), `{"type":"task_started","turn_id":"old-turn"}`),
		rolloutRow(sent.Add(-4*time.Minute+time.Second), `{"type":"task_complete","turn_id":"old-turn","last_agent_message":"astra medium works."}`),
	)
	if got := codexRolloutTurnStartedSince(path, sent); got != "" {
		t.Fatalf("a turn that started before the prompt was sent counted as this turn: %q", got)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(rolloutRow(sent.Add(300*time.Millisecond), `{"type":"task_started","turn_id":"new-turn"}`) + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got := codexRolloutTurnStartedSince(path, sent); got != "new-turn" {
		t.Fatalf("turn started after the prompt = %q, want new-turn", got)
	}
	if got := codexRolloutTurnStartedSince("", sent); got != "" {
		t.Fatalf("empty path returned %q", got)
	}
}

func TestCodexTurnCompletionIgnoresAnotherTurnsTaskComplete(t *testing.T) {
	turnStart := time.Now().Add(-time.Second)
	path := writeCodexTurnRollout(t,
		`{"type":"session_meta","payload":{"id":"thread","cwd":"/tmp/w"}}`,
		rolloutRow(turnStart.Add(100*time.Millisecond), `{"type":"task_started","turn_id":"this-turn"}`),
		// An interrupted predecessor flushing its completion late, after the
		// new prompt went out, carrying the previous answer.
		rolloutRow(turnStart.Add(200*time.Millisecond), `{"type":"task_complete","turn_id":"previous-turn","last_agent_message":"astra medium works."}`),
	)
	tracker := newCodexTurnCompletionTracker(turnStart, "/tmp/w", func(time.Time) string { return path })
	if tracker.completed() {
		t.Fatal("another turn's task_complete ended this turn")
	}
	if text, source := readCodexRolloutFinalAssistantText(path, turnStart); text != "" {
		t.Fatalf("another turn's last_agent_message was returned as this turn's answer: %q (%s)", text, source)
	}
	if got := readCodexRolloutTurnError(path, turnStart); got != "" {
		t.Fatalf("another turn's task_complete produced an error: %q", got)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(rolloutRow(turnStart.Add(900*time.Millisecond), `{"type":"task_complete","turn_id":"this-turn","last_agent_message":"codex 0.153 confirmed."}`) + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if !tracker.completed() {
		t.Fatal("this turn's own task_complete was not detected")
	}
	if text, _ := readCodexRolloutFinalAssistantText(path, turnStart); text != "codex 0.153 confirmed." {
		t.Fatalf("final text = %q, want this turn's answer", text)
	}
}

func TestCodexTurnAbortedEndsTheTurnAsAFailure(t *testing.T) {
	turnStart := time.Now().Add(-time.Second)
	path := writeCodexTurnRollout(t,
		`{"type":"session_meta","payload":{"id":"thread","cwd":"/tmp/w"}}`,
		rolloutRow(turnStart.Add(100*time.Millisecond), `{"type":"task_started","turn_id":"this-turn"}`),
		rolloutRow(turnStart.Add(200*time.Millisecond), `{"type":"turn_aborted","turn_id":"other-turn","reason":"interrupted"}`),
	)
	tracker := newCodexTurnCompletionTracker(turnStart, "/tmp/w", func(time.Time) string { return path })
	if tracker.completed() {
		t.Fatal("another turn's turn_aborted ended this turn")
	}
	if got := readCodexRolloutTurnError(path, turnStart); got != "" {
		t.Fatalf("another turn's abort produced an error: %q", got)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(rolloutRow(turnStart.Add(3*time.Second), `{"type":"turn_aborted","turn_id":"this-turn","reason":"interrupted"}`) + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if !tracker.completed() {
		t.Fatal("turn_aborted for this turn must end the wait")
	}
	aborted, reason := tracker.aborted()
	if !aborted || reason != "interrupted" {
		t.Fatalf("aborted = %v %q, want true interrupted", aborted, reason)
	}
	if got, want := readCodexRolloutTurnError(path, turnStart), codexTurnAbortedMessage("interrupted"); got != want {
		t.Fatalf("turn error = %q, want %q", got, want)
	}
	if text, _ := readCodexRolloutFinalAssistantText(path, turnStart); text != "" {
		t.Fatalf("an aborted turn produced final text %q", text)
	}
	if got := codexTurnAbortedMessage(""); !strings.Contains(got, "interrupted") {
		t.Fatalf("empty reason message = %q", got)
	}
	if got := codexTurnAbortedMessage("replaced"); !strings.Contains(got, "replaced") {
		t.Fatalf("custom reason dropped: %q", got)
	}
}

func TestCodexTaskCompleteBelongsToTurnKeepsLegacyRollouts(t *testing.T) {
	if !codexTaskCompleteBelongsToTurn("", "any") {
		t.Fatal("rollouts without task_started must keep the timestamp-only behavior")
	}
	if !codexTaskCompleteBelongsToTurn("this", "") {
		t.Fatal("a task_complete without turn_id must still count")
	}
	if codexTaskCompleteBelongsToTurn("this", "other") {
		t.Fatal("a different turn's completion must be ignored")
	}
}

// Codex 0.160 retains its startup screen above the newly rendered header.
// A short conversation leaves that old banner within the last twelve lines.
func TestCodex0160HistoricalResumeBannerDoesNotBlockReadyPane(t *testing.T) {
	pane := `  >_ OpenAI Codex (v0.160.0)
  /project
  Resuming session…
› Ask Codex to do anything
  >_ OpenAI Codex (v0.160.0)
  /project
› Remember a code. Reply exactly FIRST_OK
• FIRST_OK
  Worked for 4s • 17:58
› Ask Codex to do anything
  GPT-6.1-Sol low · /project
`
	if codexPaneIsResuming(pane) {
		t.Fatal("historical banner blocks a newly rendered ready pane")
	}
	if !hasCodexReadyPrompt(pane) || !codexPaneHasEmptyComposer(pane) {
		t.Fatal("resumed pane must accept input")
	}
}
