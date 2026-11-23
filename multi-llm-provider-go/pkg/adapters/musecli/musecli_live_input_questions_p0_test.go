package musecli

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Covers the app's SendCodingAgentLiveInput -> retained polling path, which
// does not keep GenerateContent running while native question widgets appear.
func TestMuseCLIRealLiveInputQuestionsP0(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	owner := "live-question-p0-" + museRandomHex(t, 3)
	t.Cleanup(func() { KillMusePersistentSession(owner) })
	stream := make(chan llmtypes.StreamChunk, 1000)
	opts := []llmtypes.CallOption{WithPersistentInteractiveSession(true), WithInteractiveSessionID(owner), WithWorkingDir(t.TempDir()), llmtypes.WithStreamingChan(stream)}
	first, err := museLiveAdapter().GenerateContent(ctx, []llmtypes.MessageContent{{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Reply READY and nothing else."}}}}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	// Retained controls must not retain this now-closed call's stream callback.
	close(stream)
	session := first.Choices[0].GenerationInfo.CodingProviderSessionHandle.TmuxSession
	t.Logf("test session: %s", session)
	waitFinal := func(t *testing.T) string {
		t.Helper()
		for {
			messages := ReadRetainedTurnMessages(owner, time.Now())
			if len(messages) > 0 {
				return strings.TrimSpace(messageText(messages[len(messages)-1]))
			}
			select {
			case <-ctx.Done():
				pane, _ := museTmuxCapturePane(context.Background(), session)
				t.Fatalf("retained response timeout: %v\n%s", ctx.Err(), pane)
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	t.Run("question_created_after_live_delivery", func(t *testing.T) {
		prompt := "Use native request_user_input to ask THREE questions in one call, then wait for actual answers: Color with Blue (Recommended), Red; Drink with Tea (Recommended), Coffee; Layout with Compact (Recommended), Spacious. Do not use other tools. After receiving answers, reply only with the three selected names separated by |."
		if err := SendMuseInteractiveInput(ctx, owner, prompt); err != nil {
			t.Fatal(err)
		}
		final := waitFinal(t)
		selected := strings.Trim(strings.ReplaceAll(strings.ReplaceAll(final, "(Recommended)", ""), " ", ""), "`\n")
		if selected != "Blue|Tea|Compact" {
			t.Fatalf("retained selected answers = %q", final)
		}
		t.Logf("retained observer answered all pages and submitted review: %s", final)
	})
	if t.Failed() {
		return
	}
	t.Run("existing_question_with_cursor_on_wrong_option", func(t *testing.T) {
		prompt := "Use native request_user_input to ask ONE question: Which color? Options in order Blue (Recommended), Red, Green. Wait for the actual answer, then reply COLOR=<selected color>. Do not use any other tools."
		if err := SendMuseInteractiveInput(ctx, owner, prompt); err != nil {
			t.Fatal(err)
		}
		var answer museRecommendedAnswer
		for {
			pane, err := museTmuxCapturePane(ctx, session)
			if err != nil {
				t.Fatal(err)
			}
			if parsed, ok := museRecommendedQuestion(pane); ok && !parsed.review {
				answer = parsed
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(500 * time.Millisecond):
			}
		}
		// Reproduce the attached production screenshot's cursor on option three.
		for i := answer.current; i < 2; i++ {
			if err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "Down").Run(); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(300 * time.Millisecond)
		pane, _ := museTmuxCapturePane(ctx, session)
		moved, ok := museRecommendedQuestion(pane)
		if !ok || moved.current != 2 || moved.target == 2 {
			t.Fatalf("failed to stage wrong cursor:\n%s", pane)
		}
		inputCtx, stopInput := context.WithTimeout(ctx, 15*time.Second)
		err := SendMuseInteractiveInput(inputCtx, owner, "What color was selected in the question immediately before this message? Reply only <color>|LIVE-FOLLOWUP. Do not use tools.")
		stopInput()
		if err != nil {
			t.Fatalf("live input into existing question: %v", err)
		}
		final := waitFinal(t)
		if !strings.Contains(strings.ToLower(final), "blue") || !strings.Contains(final, "LIVE-FOLLOWUP") {
			t.Fatalf("wrong recommendation or lost follow-up: %q", final)
		}
		t.Logf("live input corrected cursor and preserved follow-up: %s", final)
	})
	// Regression for the 409 "Live input unavailable: ... [user_input_required]"
	// bug: entry.autoAnswer is only populated by the turn that first launched
	// the persistent session. Force it back to nil here -- as it would be for
	// a live steer delivered before that first turn ever completed, or against
	// a session whose launch predates museBindPersistentAutoAnswer -- and
	// confirm SendMuseInteractiveInput still resolves a native question
	// instead of surfacing the raw pendingErr.
	t.Run("live_delivery_with_nil_persistent_auto_answer", func(t *testing.T) {
		key, err := musePersistentKey(owner)
		if err != nil {
			t.Fatal(err)
		}
		musePersistentPool.Lock()
		entry := musePersistentPool.m[key]
		if entry == nil {
			musePersistentPool.Unlock()
			t.Fatal("no pooled session for owner")
		}
		entry.autoAnswer = nil
		musePersistentPool.Unlock()
		prompt := "Use native request_user_input to ask ONE question: Which fruit? Options in order Apple (Recommended), Pear. Wait for the actual answer, then reply FRUIT=<selected fruit>. Do not use any other tools."
		if err := SendMuseInteractiveInput(ctx, owner, prompt); err != nil {
			t.Fatalf("live input with nil persistent auto-answer: %v", err)
		}
		final := waitFinal(t)
		if !strings.Contains(strings.ToLower(final), "apple") {
			t.Fatalf("question was not auto-answered: %q", final)
		}
		t.Logf("nil persistent auto-answer still resolved the native question: %s", final)
	})
}
