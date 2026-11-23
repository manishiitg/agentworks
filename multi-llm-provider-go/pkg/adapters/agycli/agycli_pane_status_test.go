package agycli

import (
	"strings"
	"testing"
	"time"
)

func TestAgyPaneStatusIgnoresOldAnswerText(t *testing.T) {
	quoted := "The answer quotes Requesting permission for: and esc to cancel."
	pane := quoted + strings.Repeat("\nold output", 25) + "\n? for shortcuts\n> "
	if !PaneReadyForInput(pane) {
		t.Fatal("old busy text blocked a ready pane")
	}
	if marker := agyApprovalMarkerShown(pane); marker != "" {
		t.Fatalf("old approval quote blocked a ready pane: %q", marker)
	}
	if agyPaneShowsTrustGate(pane) {
		t.Fatal("old trust text blocked a ready pane")
	}
	blocked := quoted + strings.Repeat("\nold output", 25) + "\nRequesting permission for:\nAllow calling this tool?"
	if marker := agyApprovalMarkerShown(blocked); marker == "" {
		t.Fatal("active approval was missed")
	}
	if PaneReadyForInput(blocked) {
		t.Fatal("approval pane appeared ready")
	}
}

// PLAT-861-116: the folded prompt was pasted while AGY still worked on a live
// steer; AGY queued it and recorded it 24 minutes later. A busy pane keeps the
// intake wait going; an idle pane (or the answer ceiling) still fails it.
func TestAgyIntakeKeepsWaitingWhileAgyWorksOnEarlierInput(t *testing.T) {
	started := time.Now().Add(-5 * time.Minute)
	busy := strings.Repeat("old output\n", 30) + "esc to cancel\n> "
	idle := strings.Repeat("old output\n", 30) + "? for shortcuts\n> "
	if !agyIntakeShouldKeepWaiting(busy, started, time.Now()) {
		t.Fatal("busy pane must keep waiting for natively queued input")
	}
	if agyIntakeShouldKeepWaiting(idle, started, time.Now()) {
		t.Fatal("idle pane without the user step must fail the intake")
	}
	if agyIntakeShouldKeepWaiting(busy, time.Now().Add(-agySidecarTurnTimeout-time.Second), time.Now()) {
		t.Fatal("wait must stay bounded by the answer ceiling")
	}
}
