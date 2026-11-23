package tmuxcontrol

import (
	"errors"
	"testing"
)

func TestForceCompleteSessionIsConsumedOnce(t *testing.T) {
	const sessionName = "mlp-codex-cli-int-test"
	if !RequestForceComplete(sessionName) {
		t.Fatalf("RequestForceComplete returned false")
	}
	if !ConsumeForceComplete(sessionName) {
		t.Fatalf("first consume returned false")
	}
	if ConsumeForceComplete(sessionName) {
		t.Fatalf("second consume returned true; request should be one-shot")
	}
}

func TestForceCompleteRejectsBlankSession(t *testing.T) {
	if RequestForceComplete(" \t\n") {
		t.Fatalf("blank session should not be accepted")
	}
	if ConsumeForceComplete(" \t\n") {
		t.Fatalf("blank session should not consume")
	}
}

func TestErrForceCompleteIdentity(t *testing.T) {
	if !errors.Is(ErrForceComplete, ErrForceComplete) {
		t.Fatalf("ErrForceComplete should be comparable via errors.Is")
	}
}
