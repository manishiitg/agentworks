package agycli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestAgyCLIRealDurableAckContract is the PLAT-352 P0 proof that a fast tmux
// send is promoted only by AGY's own new user row, including an identical
// retained follow-up in the same native conversation.
func TestAgyCLIRealDurableAckContract(t *testing.T) {
	if !*codingCLIP0Live {
		t.Skip("run through the live coding CLI P0 runner")
	}
	agyKeyModeForTest(t)
	workDir := agySidecarWorkdirForTest(t)
	owner := "agy-durable-" + agyRandomHex(t, 4)
	t.Cleanup(func() { CloseAgyCLIInteractiveSessionForOwner(owner, "test done") })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	session, err := ensureAgyInteractiveSession(ctx, owner, workDir)
	if err != nil {
		t.Fatal(err)
	}
	message := "Do not use tools or commands. Reply with the single word READY."
	for turn := 1; turn <= 2; turn++ {
		if err := SendAgyInteractiveInput(ctx, owner, message); err != nil {
			t.Fatalf("turn %d send: %v", turn, err)
		}
		ack, err := AwaitAgyInputDurable(ctx, owner, message, 90*time.Second)
		if err != nil || !strings.HasSuffix(ack.ProofPath, ".db") {
			t.Fatalf("turn %d durable ack = %+v, err %v", turn, ack, err)
		}
		if _, err := waitAgyPaneReady(ctx, session.tmuxSessionName, 150*time.Second); err != nil {
			t.Fatalf("turn %d completion: %v", turn, err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			messages := ReadRetainedTurnMessages(owner, time.Time{})
			if len(messages) == 1 {
				text := messages[0].Parts[0].(llmtypes.TextContent).Text
				if !strings.Contains(text, "READY") || strings.Contains(text, "Do not use tools") {
					t.Fatalf("turn %d retained answer = %q", turn, text)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("turn %d has no recorded retained answer", turn)
			}
			time.Sleep(500 * time.Millisecond)
		}
		if progress := ReadRetainedTurnProgressMessages(owner, time.Time{}); len(progress) != 1 {
			t.Fatalf("turn %d structured progress messages = %d, want 1", turn, len(progress))
		}
		if replay := ReadRetainedTurnProgressMessages(owner, time.Time{}); len(replay) != 0 {
			t.Fatalf("turn %d replayed %d prior progress messages", turn, len(replay))
		}
	}
}
