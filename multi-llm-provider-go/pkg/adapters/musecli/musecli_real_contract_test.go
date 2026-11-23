package musecli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireRealMuseCLIE2E(t *testing.T) {
	t.Helper()
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner: go test ./pkg/adapters/musecli/ -run TestMuseCLIReal -args -coding-cli-p0-live")
	}
	for _, bin := range []string{"muse", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("real Muse CLI tests require %s in PATH: %v", bin, err)
		}
	}
}

func museRandomHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b)
}

func TestMuseCLIRealInteractiveTmuxFullContract(t *testing.T) {
	requireRealMuseCLIE2E(t)

	workdir := t.TempDir()

	// runtime_context (projection half): plant a canary skill before boot.
	// The model-reads-it half needs a live turn and belongs to the
	// runtime_context cert, not fresh_launch.
	canarySkill := "muse-fresh-canary-" + museRandomHex(t, 3)
	skillDir := filepath.Join(workdir, ".agents", "skills", canarySkill)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillDoc := "---\nname: " + canarySkill + "\ndescription: fresh_launch canary\n---\n# Canary\nReply pineapple when asked.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillDoc), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	session := "mlp-muse-cli-int-t-" + museRandomHex(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "tmux", "kill-session", "-t", session).Run()
	})

	// --trust-workspace keeps this cert about launch, not gate UX: trust
	// gates are CertTrustAuthPrompts territory. The gate assertion below
	// fails loudly if the flag ever stops working.
	launch := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", session,
		"-x", "200", "-y", "50", "-c", workdir, "muse", "--trust-workspace")
	if out, err := launch.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v\n%s", err, out)
	}

	deadline := time.Now().Add(90 * time.Second)
	var pane string
	for {
		p, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			t.Fatalf("capture pane: %v", err)
		}
		pane = p
		if museTUISufficientlySettled(pane) {
			break
		}
		if musePaneShowsBlockingGate(pane) {
			t.Fatalf("muse TUI blocked on trust/auth gate (trust_auth_prompts cert territory); pane:\n%s", pane)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for muse TUI to settle; latest pane:\n%s", pane)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("settled muse pane:\n%s", pane)

	// Projection half of runtime_context: the canary skill must be
	// discoverable in this trusted workspace.
	skillsOut, err := exec.CommandContext(ctx, "muse", "skills", "list", "--source", "project", "--workspace", workdir, "--trust-workspace").CombinedOutput()
	if err != nil {
		t.Fatalf("muse skills list: %v\n%s", err, skillsOut)
	}
	if !strings.Contains(string(skillsOut), canarySkill) {
		t.Fatalf("canary skill %q not discovered; skills list:\n%s", canarySkill, skillsOut)
	}

	// Still alive 5s later: a booted TUI, not a crash-looping process.
	time.Sleep(5 * time.Second)
	if err := exec.CommandContext(ctx, "tmux", "has-session", "-t", session).Run(); err != nil {
		t.Fatalf("muse tmux session died after settling: %v", err)
	}

	if err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", session).Run(); err != nil {
		t.Fatalf("kill test session: %v", err)
	}
	if err := exec.CommandContext(ctx, "tmux", "has-session", "-t", session).Run(); err == nil {
		t.Fatal("test tmux session survived kill-session")
	}
}

func TestMusePaneGateDetection(t *testing.T) {
	blocked := []string{
		"Do you trust the files in this folder? [y/n]",
		"workspace is untrusted",
		"Run `muse login` to continue",
		"Please log in to Meta to proceed",
	}
	for _, pane := range blocked {
		if !musePaneShowsBlockingGate(pane) {
			t.Errorf("gate not detected in %q", pane)
		}
	}
	for _, pane := range []string{"", "thinking...", "Welcome to Muse\n\n> "} {
		if musePaneShowsBlockingGate(pane) {
			t.Errorf("false gate positive in %q", pane)
		}
	}
	if museTUISufficientlySettled("alpha\nbeta") {
		t.Error("2-line pane must not count as settled")
	}
}

// TestMuseTUIAtPromptSurvivesScroll: a completed long answer scrolls the
// boot banner off, but the prompt glyph + status line still mark the idle
// shape the turn-completion waiter needs. A gate is never "at prompt".
func TestMuseTUIAtPromptSurvivesScroll(t *testing.T) {
	scrolled := "◆ long answer line one\n  wrapped continuation\n\n── Voice input ──\n❯\n───\n  muse-spark-1.3-contributor · xhigh · /tmp/work\n"
	if !museTUIAtPrompt(scrolled) {
		t.Error("scrolled idle pane (no banner) must count as at-prompt")
	}
	if museTUISufficientlySettled(scrolled) {
		t.Error("scrolled pane must NOT count as boot-settled (banner gone)")
	}
	if museTUIAtPrompt("Do you trust this workspace?\n> 1  Trust and continue") {
		t.Error("trust gate must not count as at-prompt")
	}
	if museTUIAtPrompt("thinking...") {
		t.Error("bare busy pane must not count as at-prompt")
	}
}
