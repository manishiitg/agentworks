package musecli

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestMuseCLIRealDurableAckContract is the CertDurableAck P0 proof for
// Muse: a follow-up steered into a busy turn through the production
// live-input path must be confirmable against the native transcript
// (user_intent.accepted row), not just the pane, and the model must
// obey it. A second, idle follow-up must be durably acked fast.
func TestMuseCLIRealDurableAckContract(t *testing.T) {
	requireMetaMuseCLIE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	workdir := t.TempDir()
	session := museLiveBootTUI(t, ctx, workdir)
	owner := "muse-real-durable-ack-" + museRandomHex(t, 3)

	token := "QUEUED-" + museRandomHex(t, 3)
	essay := "Write a 400-word essay on the history of concrete with extensive detail. Token " + museRandomHex(t, 2) + "."
	essayStart := time.Now()
	if err := museSendPrompt(ctx, session, essay); err != nil {
		t.Fatalf("send long turn: %v", err)
	}
	nativeID, logPath, err := museWaitIntake(ctx, session, essayStart, promptSnippet(essay), "")
	if err != nil {
		t.Fatalf("long turn never taken in: %v", err)
	}
	// Bind the raw TUI into the pool exactly as a first turn would, so
	// the production Send/Await path (not just raw helpers) is exercised.
	musePersistentPool.Lock()
	musePersistentPool.m[owner] = &musePersistentSession{tmuxName: session, workdir: workdir, nativeSessionID: nativeID, logPath: logPath}
	musePersistentPool.Unlock()
	t.Cleanup(func() {
		musePersistentPool.Lock()
		delete(musePersistentPool.m, owner)
		musePersistentPool.Unlock()
	})
	time.Sleep(8 * time.Second)

	steer := "Reply with exactly " + token + " and nothing else."
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 60*time.Second)
	sendErr := SendMuseInteractiveInput(sendCtx, owner, steer)
	sendCancel()
	if sendErr != nil {
		t.Fatalf("SendMuseInteractiveInput error = %v", sendErr)
	}
	awaitCtx, awaitCancel := context.WithTimeout(context.Background(), 90*time.Second)
	ack, awaitErr := AwaitMuseInputDurable(awaitCtx, owner, steer, 60*time.Second)
	awaitCancel()
	if awaitErr != nil {
		t.Fatalf("AwaitMuseInputDurable error = %v", awaitErr)
	}
	if ack.Outcome != MuseDurableAckConfirmed {
		t.Fatalf("durable ack outcome = %q, want confirmed (latency %s)", ack.Outcome, ack.Latency)
	}
	t.Logf("busy steer durably acked in %s via %s", ack.Latency.Round(100*time.Millisecond), ack.ProofPath)

	deadline := time.Now().Add(6 * time.Minute)
	for {
		pane, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			t.Fatalf("capture pane: %v", err)
		}
		if strings.Contains(pane, token) && museTUIAtPrompt(pane) &&
			musePaneStable(ctx, session, pane) && museLogQuietSince(logPath, 5*time.Second) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued follow-up %q never answered", token)
		}
		time.Sleep(time.Second)
	}

	idleToken := "IDLE-" + museRandomHex(t, 3)
	idleMsg := "Do not use tools. Reply exactly " + idleToken + "."
	idleSendCtx, idleSendCancel := context.WithTimeout(context.Background(), 60*time.Second)
	idleSendErr := SendMuseInteractiveInput(idleSendCtx, owner, idleMsg)
	idleSendCancel()
	if idleSendErr != nil {
		t.Fatalf("idle SendMuseInteractiveInput error = %v", idleSendErr)
	}
	idleAwaitCtx, idleAwaitCancel := context.WithTimeout(context.Background(), 90*time.Second)
	idleAck, idleAwaitErr := AwaitMuseInputDurable(idleAwaitCtx, owner, idleMsg, 60*time.Second)
	idleAwaitCancel()
	if idleAwaitErr != nil {
		t.Fatalf("idle AwaitMuseInputDurable error = %v", idleAwaitErr)
	}
	if idleAck.Outcome != MuseDurableAckConfirmed {
		t.Fatalf("idle durable ack outcome = %q, want confirmed", idleAck.Outcome)
	}
	if idleAck.Latency > 45*time.Second {
		t.Fatalf("idle durable ack latency = %s, want well under the budget", idleAck.Latency)
	}
	t.Logf("idle follow-up durably acked in %s", idleAck.Latency.Round(100*time.Millisecond))
}
