package musecli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const museQuestionFixture = "Historical 429 quota exhausted\n◆ Request user input Color — running (1s)\nChoose a color\n› 1. Red\n  2. Blue (Recommended)\n  3. Green\n1 of 3\nEnter to select · Esc to interrupt\n❯"

func TestMuseRecommendedQuestion(t *testing.T) {
	a, ok := museRecommendedQuestion(museQuestionFixture)
	if !ok || a.current != 0 || a.target != 0 || a.label != "Red" {
		t.Fatalf("wrong first option: %+v %v", a, ok)
	}
	moved, ok := museRecommendedQuestion(strings.Replace(strings.Replace(museQuestionFixture, "› 1.", "  1.", 1), "  2.", "› 2.", 1))
	if !ok || moved.key != a.key || moved.current != 1 || moved.target != 0 {
		t.Fatal("cursor movement changed question identity")
	}
	timer, ok := museRecommendedQuestion(strings.Replace(museQuestionFixture, "(1s)", "(20s)", 1))
	if !ok || timer.key != a.key {
		t.Fatal("running timer changed question identity")
	}
	next, ok := museRecommendedQuestion(strings.Replace(museQuestionFixture, "1 of 3", "2 of 3", 1))
	if !ok || next.key == a.key {
		t.Fatal("next question must have a new identity")
	}
	for _, pane := range []string{
		strings.ReplaceAll(museQuestionFixture, " (Recommended)", ""),
		strings.Replace(museQuestionFixture, "Red", "Red (Recommended)", 1),
	} {
		if answer, ok := museRecommendedQuestion(pane); !ok || answer.target != 0 {
			t.Fatalf("must select first option regardless of labels: %+v %v", answer, ok)
		}
	}
	for _, pane := range []string{
		strings.ReplaceAll(museQuestionFixture, "›", " "),
		strings.Replace(museQuestionFixture, "Choose a color", "Do you trust this workspace?", 1),
		"Do you trust this folder?\n› 1. Yes (Recommended)\nEnter to select",
		"Earlier we said Blue (Recommended).\n❯",
	} {
		if _, ok := museRecommendedQuestion(pane); ok {
			t.Fatalf("must not auto-answer:\n%s", pane)
		}
	}
}

func TestMuseAutoAnswerDisabledCanceledAndDuplicate(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	WithAutoSelectRecommended(false)(opts)
	ctx := museWithAutoAnswer(context.Background(), opts)
	if _, err := museHandlePendingQuestion(ctx, "must-not-access-tmux", museQuestionFixture); llmerrors.KindOf(err) != llmerrors.KindUserInputRequired {
		t.Fatalf("disabled: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := museHandlePendingQuestion(museWithAutoAnswer(canceled, nil), "must-not-access-tmux", museQuestionFixture); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	answer, _ := museRecommendedQuestion(museQuestionFixture)
	duplicate := context.WithValue(context.Background(), museAutoAnswerKey{}, &museAutoAnswerState{lastQuestion: answer.key})
	if pending, err := museHandlePendingQuestion(duplicate, "must-not-access-tmux", museQuestionFixture); !pending || err != nil {
		t.Fatalf("duplicate: %v %v", pending, err)
	}
}

func TestMuseRecommendedReview(t *testing.T) {
	pane := "◆ Request user input Color, Drink, Layout — running (5s)\nReview answers before submit · Enter to edit or submit · ↑/↓ to move · Esc to go back\n  Color: Blue (Recommended)\n  Drink: Tea (Recommended)\n  Layout: Compact (Recommended)\n> Submit answers\n  Interrupt turn\n──────────────────\n❯"
	answer, ok := museRecommendedQuestion(pane)
	if !ok || !answer.review || answer.current != 3 || answer.target != 3 {
		t.Fatalf("review = %+v, %v", answer, ok)
	}
	if musePendingUserInputError(pane) == nil {
		t.Fatal("review must not be treated as idle")
	}
	changed := strings.Replace(pane, "Tea (Recommended)", "Coffee", 1)
	if answer, ok := museRecommendedQuestion(changed); !ok || !answer.review || answer.target != 3 {
		t.Fatal("must submit completed answers without recommendation labels")
	}
	if _, ok := museRecommendedQuestion(strings.Replace(pane, "Tea (Recommended)", "", 1)); ok {
		t.Fatal("must not submit an empty answer")
	}
}

func TestMuseFirstOptionBotChannel(t *testing.T) {
	pane := "◆ Request user input Channel, Chat, Behavior — running (7s)\nWhich bot channel do you want to connect to this workflow - Slack or WhatsApp?\n  1. Slack\n› 2. WhatsApp\n  3. None of the above\n1 of 3\nEnter to select · Esc to interrupt"
	answer, ok := museRecommendedQuestion(pane)
	if !ok || answer.current != 1 || answer.target != 0 || answer.label != "Slack" {
		t.Fatalf("must navigate to first channel: %+v %v", answer, ok)
	}
}

// PLAT-872-70: Muse 1.4.x marks the selected row with U+27E9, which must read
// as the cursor in both the live widget and the recommended-option parser.
func TestMuseQuestionAngleBracketCursor(t *testing.T) {
	pane := "◈ Request user input Color — running (3s)\n\n  Which color do you prefer?\n\n  \u27e9 1. Red                 A bold choice.\n    2. Blue (Recommended)  A balanced choice.\n    3. Green               A fresh choice.\n    4. None of the above   Add details in notes.\n\n  Enter to select · ↑/↓ to move · Esc to interrupt\n"
	if w, ok := museReadWidget(pane); !ok || w.cursor != 0 || len(w.rows) != 4 {
		t.Fatalf("1.4.x widget not read: %+v %v", w, ok)
	}
	if answer, ok := museRecommendedQuestion(pane); !ok || answer.current != 0 || answer.target != 0 {
		t.Fatalf("1.4.x recommended parse failed: %+v %v", answer, ok)
	}
}

func TestMuseFirstOptionLivePaneShape(t *testing.T) {
	pane := "Agent pid 3118\nIdentity added: /Users/test/.ssh/id_ed25519\n\n  Muse Code 1.3.0\n\n❯ Integration test of your native request_user_input widget. Ask all THREE questions in a SINGLE request_user_input call.\n\n◈ Request user input Color, Drink, Layout — running (14s)\n\n  Which color do you prefer?\n\n  › 1. Red                 A bold and warm choice.\n    2. Blue (Recommended)  A balanced and popular choice.\n    3. Green               A fresh and natural choice.\n    4. None of the above   Optionally, add details in notes (tab).\n\n  1 of 3\n  Enter to select · ↑/↓ to move · ←/→ to switch question · Tab for an optional note · Esc to interrupt\n\n────────────────────────────────────────────────────────────────────────\n❯\n────────────────────────────────────────────────────────────────────────\n  muse-spark-1.3-contributor · max · /tmp/work · Auto-review"
	answer, ok := museRecommendedQuestion(pane)
	if !ok || answer.current != 0 || answer.target != 0 {
		t.Fatalf("live pane not actionable: %+v %v", answer, ok)
	}
	if llmerrors.KindOf(musePendingUserInputError(pane)) != llmerrors.KindUserInputRequired || !musePaneHasRunningTool(pane) {
		t.Fatal("live Muse tool marker must register as a pending question and running tool")
	}
	for _, marker := range []string{"◆", "◇"} {
		frame, frameOK := museRecommendedQuestion(strings.Replace(pane, "◈ Request", marker+" Request", 1))
		if !frameOK || frame.key != answer.key {
			t.Fatalf("animated marker changed question identity: %q, %v", frame.key, frameOK)
		}
	}
}

func TestMuseStoppedAutoAnswerCannotSendKeys(t *testing.T) {
	state := &museAutoAnswerState{}
	state.stopped.Store(true)
	ctx := context.WithValue(context.Background(), museAutoAnswerKey{}, state)
	if _, err := museHandlePendingQuestion(ctx, "must-not-access-tmux", museQuestionFixture); !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped session: %v", err)
	}
}

// The multi-select form from Excellence 2026-09-29 (a Code chat stuck on it):
// the hint wraps ("Esc\ninterrupt") and reads "Enter to toggle".
const museCheckboxFixture = "! Message not sent — another run is still starting\n" +
	"◆ Request user input Features — running (44s)\n" +
	"Which features should I add to startup-navigator? You can pick more than one.\n" +
	"› 1. [ ] Security pack (Recommended)   Brute-force protection on login/register\n" +
	"  2. [ ] Password reset                 Forgot-password flow with email reset links\n" +
	"  3. [ ] Article reactions             Let readers react to articles\n" +
	"  4. [ ] None of the above             Optionally, add details in notes (tab).\n" +
	"  5. Submit answer (0 checked)\n" +
	"Enter to toggle · Submit row to continue · ↑/↓ to move · Tab for an optional note · Esc\n" +
	"interrupt\n" +
	"❯"

func TestMuseCheckboxQuestionIsDetectedAndAnswered(t *testing.T) {
	var pending *llmerrors.Error
	if err := musePendingUserInputError(museCheckboxFixture); !errors.As(err, &pending) || pending.Kind != llmerrors.KindUserInputRequired {
		t.Fatalf("checkbox question not detected as waiting for input: %v", err)
	}
	// Nothing checked: toggle the first (recommended) option.
	toggle, ok := museRecommendedQuestion(museCheckboxFixture)
	if !ok || toggle.current != 0 || toggle.target != 0 || !strings.HasPrefix(toggle.label, "Security pack") {
		t.Fatalf("first step = %+v %v", toggle, ok)
	}
	// Once checked: move to Submit and submit; a new identity, so it is a new action.
	checked := strings.Replace(strings.Replace(museCheckboxFixture, "1. [ ] Security", "1. [x] Security", 1), "(0 checked)", "(1 checked)", 1)
	submit, ok := museRecommendedQuestion(checked)
	if !ok || submit.target != 4 || submit.key == toggle.key {
		t.Fatalf("second step = %+v %v", submit, ok)
	}
	// The single-choice form still works and a widget without its hint is not answered.
	if _, ok := museRecommendedQuestion(museQuestionFixture); !ok {
		t.Fatal("single-choice question regressed")
	}
	if _, ok := museRecommendedQuestion(strings.Replace(museQuestionFixture, "Enter to select", "", 1)); ok {
		t.Fatal("a question without its hint line must not be answered")
	}
}
