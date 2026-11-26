package musecli

import (
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"testing"
)

func TestMusePendingQuestionIsNotAuthOrThrottle(t *testing.T) {
	pane := "Old response: 429 quota exhausted\n◆ Request user input Goals, Target, Support — running (37m 29s)\nApprove this grouping?\n1. Approve split\nEnter to select · Esc to interrupt"
	err := musePendingUserInputError(pane)
	if llmerrors.KindOf(err) != llmerrors.KindUserInputRequired {
		t.Fatalf("wrong error: %v", err)
	}
	if musePaneShowsBlockingGate(pane) {
		t.Fatal("question misidentified as auth gate")
	}
	if musePendingUserInputError("Previously used Request user input; approve this grouping") != nil {
		t.Fatal("ordinary output is not an active question")
	}
	if !musePaneShowsBlockingGate("Do you trust this workspace?") {
		t.Fatal("trust gate no longer detected")
	}
}
