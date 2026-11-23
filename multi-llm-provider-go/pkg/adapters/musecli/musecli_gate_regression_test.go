package musecli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Sanitized reproduction: preserve the offending wording and TUI structure,
// without retaining the customer's financial records or identities.
const museLoginAdvicePane = `◆ Practical next step: have your adviser log in to the Compliance Portal
  and submit feedback with supporting documents.
─────────────────────────────────────
❯
─────────────────────────────────────
  muse-spark-1.3-contributor · xhigh · /tmp/work
`

func TestMuseGateConversationMentionsP0(t *testing.T) {
	for _, text := range []string{
		museLoginAdvicePane,
		"◆ Treat untrusted data carefully. You may need to run muse login.",
		"◆ Please log in to another service to review your report.",
		"❯ How do you trust files from a colleague?",
		"◆ Run `muse login` to continue\n──────────────────\n❯\n──────────────\n muse-spark · xhigh · /tmp/work",
		"◆ Do you trust this workspace?\nThis was the old startup prompt.\n❯\n────\n muse-spark · xhigh · /tmp/work",
	} {
		if musePaneShowsBlockingGate(text) {
			t.Fatalf("conversation misclassified as auth gate: %q", text)
		}
	}
	if !museTUIAtPrompt(museLoginAdvicePane) {
		t.Fatal("login advice hid the ready composer")
	}
}

func TestMuseGateActiveDialogsRemainBlockedP0(t *testing.T) {
	for _, pane := range []string{
		"Do you trust this workspace?\nWorkspace: /tmp/work\n> 1  Trust and continue\n  2  Quit\nUse Up/Down or 1/2, then Enter. Esc quits.",
		"Do you trust this workspace?\n> 1  Trust and continue\nUse Up/Down or 1/2, then Enter. Esc quits.\n❯\n────\n muse-spark · xhigh · /tmp/work",
		"Run `muse login` to continue",
		"Please log in to Meta to proceed",
		"Run `muse login` to continue\n❯\n────\n muse-spark · xhigh · /tmp/work",
		strings.Replace(museQuestionFixture, "Choose a color", "Do you trust this workspace?", 1),
	} {
		if !musePaneShowsBlockingGate(pane) {
			t.Fatalf("active gate was missed: %q", pane)
		}
	}
}

// Exercise the live-input gate through real tmux capture without a model call
// or typing into an existing user session.
func TestMuseLiveInputLoginAdviceTmuxP0(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session := "mlp-muse-advice-test-" + museRandomSessionSuffix()
	dir := t.TempDir()
	panePath := filepath.Join(dir, "pane.txt")
	if err := os.WriteFile(panePath, []byte(museLoginAdvicePane), 0600); err != nil {
		t.Fatal(err)
	}
	// Only synthetic, non-sensitive content is displayed by this scratch pane.
	cmd := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", session,
		"-x", "160", "-y", "30", "-c", dir, "sh", "-c", "cat pane.txt; exec sleep 30")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launch: %v %s", err, out)
	}
	defer func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCleanup()
		_ = exec.CommandContext(cleanup, "tmux", "kill-session", "-t", session).Run()
	}()
	for {
		pane, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(pane, "muse-spark-1.3-contributor") {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := museWaitLiveInputComposer(ctx, session); err != nil {
		t.Fatalf("live input rejected an ordinary assistant answer: %v", err)
	}
}
