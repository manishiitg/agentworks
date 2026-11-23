package musecli

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// This uses a real authenticated Muse TUI and its native question widget.
// The test answers only its own harmless color question in an isolated workspace.
func TestMuseCLIRealPendingQuestionResumeP0(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	adapter := museLiveAdapter()
	owner := "question-p0-" + museRandomHex(t, 3)
	t.Cleanup(func() { KillMusePersistentSession(owner) })
	opts := []llmtypes.CallOption{WithAutoSelectRecommended(false), WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithWorkingDir(t.TempDir()), llmtypes.WithReasoningEffort("low")}
	launch, err := adapter.GenerateContent(ctx, nil, append(opts, llmtypes.WithCodingProviderLaunchOnly())...)
	if err != nil {
		t.Fatal(err)
	}
	session := launch.Choices[0].GenerationInfo.CodingProviderSessionHandle.TmuxSession
	t.Logf("isolated question session: %s", session)
	human := func(prompt string) []llmtypes.MessageContent {
		return []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: prompt}}}}
	}
	token := "QUESTION-RESUMED-" + museRandomHex(t, 3)
	prompt := "This is an integration test. First say exactly: Historical example only: 429 quota exhausted. Then use your native request_user_input tool to ask ONE multiple-choice question: Approve this color? Options: Blue (Recommended), Green. Do not answer it yourself. After I select Blue reply with exactly " + token + ". Do not use files, shell, or any other tools."
	_, err = adapter.GenerateContent(ctx, human(prompt), opts...)
	if llmerrors.KindOf(err) != llmerrors.KindUserInputRequired {
		t.Fatalf("expected user_input_required, got %v", err)
	}
	t.Logf("pending question detected: %v", err)
	pane, captureErr := museTmuxCapturePane(ctx, session)
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	if !strings.Contains(pane, "429 quota exhausted") {
		t.Fatalf("missing historical quota fixture in real pane:\n%s", pane)
	}
	if musePaneShowsBlockingGate(pane) {
		t.Fatal("native question identified as auth gate")
	}
	// A queued follow-up must fail promptly without submitting into the widget.
	start := time.Now()
	_, err = adapter.GenerateContent(ctx, human("QUEUED-PROMPT-MUST-NOT-BE-SUBMITTED"), opts...)
	if llmerrors.KindOf(err) != llmerrors.KindUserInputRequired {
		t.Fatalf("queued follow-up: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("pending question waited/retried instead of returning promptly")
	}
	if out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "Home", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("answer test question: %v %s", err, out)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		pane, err = museTmuxCapturePane(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(pane, token) && musePendingUserInputError(pane) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("answer did not resume:\n%s", pane)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	followToken := "AFTER-QUESTION-" + museRandomHex(t, 3)
	resp, err := adapter.GenerateContent(ctx, human("Reply with exactly "+followToken+" and nothing else."), opts...)
	if err != nil {
		t.Fatalf("follow-up after answering: %v", err)
	}
	if !strings.Contains(resp.Choices[0].Content, followToken) {
		t.Fatalf("follow-up response: %q", resp.Choices[0].Content)
	}
	if resp.Choices[0].GenerationInfo.CodingProviderSessionHandle.TmuxSession != session {
		t.Fatal("answer lost original session")
	}
	t.Log("PASS: question surfaced, queued prompt blocked, answer resumed, same-session follow-up succeeded")
}
