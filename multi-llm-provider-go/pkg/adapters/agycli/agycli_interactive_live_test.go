package agycli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/testcontracts"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Live interactive-lane P0 proofs for agy: live_input (follow-up into the
// TUI sidecar is answered), busy_live_input (mid-turn input queues behind
// the running turn, plus Escape interrupt), reply_formatting_fidelity (the
// shared GFM bar: exec-extracted markdown keeps structure the wrapped pane
// breaks).
//
// Trust posture (muse parity): the lane FAILS LOUDLY on a workspace trust
// gate instead of auto-answering, so sidecar boots run in scratch dirs
// under an already-trusted workspace; the gate-detection test pins the
// loud failure (and proves settings.json is untouched).

func requireAgyTmux(t *testing.T) {
	t.Helper()
	requireRealAgyCLIE2E(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("real agy interactive tests require tmux in PATH: %v", err)
	}
}

// agyTrustWorkdirForTest pre-trusts dir as the test's explicit caller duty:
// agy trust is exact-path (subdirs of a trusted dir are NOT trusted) and
// the lane fails loudly on gates, so the test grants trust and restores
// settings.json byte-identical afterwards.
func agyTrustWorkdirForTest(t *testing.T, dir string) {
	t.Helper()
	restore, err := TrustAgyWorkspaceDir(dir)
	if err != nil {
		t.Fatalf("trust test workdir: %v", err)
	}
	t.Cleanup(restore)
}

// agy sidecar boots need a pre-trusted tmpdir (see above).
func agySidecarWorkdirForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	agyTrustWorkdirForTest(t, dir)
	return dir
}

func TestAgyCLIRealInteractiveLiveInputContract(t *testing.T) {
	requireAgyTmux(t)
	t.Cleanup(func() { _ = CleanupAgyCLIInteractiveSessions(context.Background()) })

	owner := "agy-live-" + agyRandomHex(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	session, err := ensureAgyInteractiveSession(ctx, owner, agySidecarWorkdirForTest(t))
	if err != nil {
		t.Fatalf("boot sidecar: %v", err)
	}
	token := "AGY_LIVE_" + agyRandomHex(t, 4)
	if err := SendAgyInteractiveInput(ctx, owner, "Do not use tools. Reply with exactly this token and nothing else: "+token); err != nil {
		t.Fatalf("live input: %v", err)
	}
	if err := waitAgyPromptEcho(ctx, session.tmuxSessionName, token, 60*time.Second); err != nil {
		t.Fatalf("prompt echo: %v", err)
	}
	pane, err := waitAgyPaneReady(ctx, session.tmuxSessionName, 150*time.Second)
	if err != nil {
		t.Fatalf("wait done: %v", err)
	}
	if !strings.Contains(pane, token) {
		t.Fatalf("pane lacks live token %s:\n%s", token, pane)
	}
}

func TestAgyCLIRealBusyLiveInputContract(t *testing.T) {
	requireAgyTmux(t)
	t.Cleanup(func() { _ = CleanupAgyCLIInteractiveSessions(context.Background()) })

	owner := "agy-busy-" + agyRandomHex(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	session, err := ensureAgyInteractiveSession(ctx, owner, agySidecarWorkdirForTest(t))
	if err != nil {
		t.Fatalf("boot sidecar: %v", err)
	}
	if err := SendAgyInteractiveInput(ctx, owner, "Write a 1200-word essay on the history of glass. Be slow and thorough. Do not use any tools; answer from knowledge."); err != nil {
		t.Fatalf("slow turn: %v", err)
	}
	time.Sleep(8 * time.Second)
	token := "AGY_BUSY_" + agyRandomHex(t, 4)
	if err := SendAgyInteractiveInput(ctx, owner, "Reply with exactly this token and nothing else: "+token); err != nil {
		t.Fatalf("busy follow-up: %v", err)
	}
	if err := waitAgyPromptEcho(ctx, session.tmuxSessionName, token, 120*time.Second); err != nil {
		t.Fatalf("follow-up echo: %v", err)
	}
	pane, err := waitAgyPaneReady(ctx, session.tmuxSessionName, 240*time.Second)
	if err != nil {
		t.Fatalf("wait done: %v", err)
	}
	if !strings.Contains(pane, token) {
		t.Fatalf("pane lacks queued token %s:\n%s", token, pane)
	}

	// Interrupt phase: a fresh slow turn is Escape-cancelled mid-run.
	if err := SendAgyInteractiveInput(ctx, owner, "Write a 2000-word essay on copper mining. Be slow. Do not use any tools; answer from knowledge."); err != nil {
		t.Fatalf("second slow turn: %v", err)
	}
	time.Sleep(6 * time.Second)
	if err := SendAgyInteractiveControlKey(ctx, owner, "Escape"); err != nil {
		t.Fatalf("Escape: %v", err)
	}
	pane, err = waitAgyPaneReady(ctx, session.tmuxSessionName, 90*time.Second)
	if err != nil {
		t.Fatalf("wait after interrupt: %v", err)
	}
	if !strings.Contains(pane, agyPaneInterruptedMarker) {
		t.Fatalf("pane lacks interrupt marker:\n%s", pane)
	}
}

func TestAgyCLIRealReplyFormattingFidelityContract(t *testing.T) {
	requireAgyTmux(t)
	t.Cleanup(func() { _ = CleanupAgyCLIInteractiveSessions(context.Background()) })

	// Agy is pi-shaped for this bar: the adapter returns exec-envelope text
	// that never passes through the terminal, while the sidecar pane wraps
	// at 200 columns. Extracted comes from the exec turn, TmuxScreen from a
	// long-line sidecar turn; the bar fails the run if the pane never
	// wrapped (a green test that proved nothing).
	token := "FMT_" + agyRandomHex(t, 5)
	trusted := agySidecarWorkdirForTest(t)
	adapter := NewAgyCLIAdapter("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	execPrompt := fmt.Sprintf("Do not use tools. Reply with markdown only about %s: two paragraphs, each ONE unbroken line of at least 250 characters, separated by exactly one blank line; then three bullet lines starting with '- ', each at least 120 characters on its own line. Preserve blank lines between paragraphs and keep each list item on its own line.", token)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, execPrompt),
	}, WithWorkingDir(trusted))
	if err != nil {
		t.Fatalf("exec turn: %v", err)
	}
	extracted := resp.Choices[0].Content
	handle, ok := llmtypes.ExtractCodingProviderSessionHandle(resp.Choices[0].GenerationInfo)
	if !ok || handle.NativeSessionID == "" {
		t.Fatal("exec turn has no native conversation handle")
	}
	trail, ok, err := ReadNativeTranscript(handle.NativeSessionID)
	if err != nil || !ok {
		t.Fatalf("exec native transcript: found=%t err=%v", ok, err)
	}
	nativeFinal := ""
	for _, message := range trail.Messages {
		if message.Role == llmtypes.ChatMessageTypeAI {
			nativeFinal = ""
			for _, part := range message.Parts {
				if text, ok := part.(llmtypes.TextContent); ok {
					nativeFinal += text.Text
				}
			}
		}
	}
	if nativeFinal == "" {
		t.Fatal("exec native transcript has no final assistant text")
	}

	owner := "agy-fmt-" + agyRandomHex(t, 3)
	session, err := ensureAgyInteractiveSession(ctx, owner, trusted)
	if err != nil {
		t.Fatalf("boot sidecar: %v", err)
	}
	wrapPrompt := fmt.Sprintf("Write 3 long paragraphs about %s, each at least 300 characters on ONE line with no line breaks. Do not use tools.", token)
	if err := SendAgyInteractiveInput(ctx, owner, wrapPrompt); err != nil {
		t.Fatalf("wrap prompt: %v", err)
	}
	if err := waitAgyPromptEcho(ctx, session.tmuxSessionName, token, 60*time.Second); err != nil {
		t.Fatalf("prompt echo: %v", err)
	}
	pane, err := waitAgyPaneReady(ctx, session.tmuxSessionName, 180*time.Second)
	if err != nil {
		t.Fatalf("wait done: %v", err)
	}

	testcontracts.AssertFinalAnswerPreservesStructure(t, testcontracts.ReplyFormattingCase{
		Provider:       "agy-cli",
		TmuxScreen:     pane,
		Extracted:      extracted,
		NativeFinal:    nativeFinal,
		WantParagraphs: 2,
		WantBullets:    3,
		UserGoal:       "markdown with paragraphs + bullets survives to the caller",
		ExpectedNote:   "exec-envelope text matches the same turn's saved native final byte-for-byte; the separate 200-col sidecar turn proves terminal wrapping",
	})
}

func TestAgyCLIRealInteractiveTrustGateContract(t *testing.T) {
	requireAgyTmux(t)
	t.Cleanup(func() { _ = CleanupAgyCLIInteractiveSessions(context.Background()) })

	// Untrusted tmpdir boot must fail LOUDLY on the trust gate (never
	// auto-answer) and leave settings.json byte-identical.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	settingsPath := filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
	before, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, bootErr := ensureAgyInteractiveSession(ctx, "agy-gate-"+agyRandomHex(t, 3), t.TempDir())
	if bootErr == nil || !strings.Contains(bootErr.Error(), "trust gate") {
		t.Fatalf("error = %v, want loud trust-gate failure", bootErr)
	}
	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("re-read settings: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("settings.json changed by a refused boot:\nbefore %q\nafter %q", before, after)
	}
	t.Logf("gate held (settings untouched): %v", strings.Split(bootErr.Error(), "\n")[0])
}

func TestAgyCLIRealPersistentSidecarWiringContract(t *testing.T) {
	requireAgyTmux(t)
	t.Cleanup(func() { _ = CleanupAgyCLIInteractiveSessions(context.Background()) })

	adapter := NewAgyCLIAdapter("", "", nil)
	trusted := agySidecarWorkdirForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	// Persistent without an owner fails before touching any CLI.
	_, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "hi"),
	}, WithWorkingDir(trusted), WithPersistentInteractiveSession(true))
	if err == nil || !strings.Contains(err.Error(), "owner session id") {
		t.Fatalf("error = %v, want owner-required failure", err)
	}

	// Persistent with an owner runs the exec turn AND leaves a live sidecar
	// that follow-up input reaches through the public entry point.
	owner := "agy-persist-" + agyRandomHex(t, 3)
	token := "AGY_PERSIST_" + agyRandomHex(t, 4)
	resp, err := adapter.GenerateContent(ctx, []llmtypes.MessageContent{
		llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, "Reply with exactly this token and nothing else: "+token),
	}, WithWorkingDir(trusted), WithInteractiveSessionID(owner), WithPersistentInteractiveSession(true))
	if err != nil {
		t.Fatalf("persistent turn: %v", err)
	}
	if content := strings.TrimSpace(resp.Choices[0].Content); !strings.Contains(content, token) {
		t.Fatalf("content = %q, want %s", content, token)
	}
	session, ok := activeAgyInteractiveSession(owner)
	if !ok {
		t.Fatal("persistent turn left no registered sidecar")
	}
	followToken := "AGY_PFOLLOW_" + agyRandomHex(t, 4)
	if err := SendAgyInteractiveInput(ctx, owner, "Reply with exactly this token and nothing else: "+followToken); err != nil {
		t.Fatalf("sidecar follow-up: %v", err)
	}
	if err := waitAgyPromptEcho(ctx, session.tmuxSessionName, followToken, 60*time.Second); err != nil {
		t.Fatalf("prompt echo: %v", err)
	}
	pane, err := waitAgyPaneReady(ctx, session.tmuxSessionName, 150*time.Second)
	if err != nil {
		t.Fatalf("wait done: %v", err)
	}
	if !strings.Contains(pane, followToken) {
		t.Fatalf("pane lacks follow-up token %s:\n%s", followToken, pane)
	}
}
