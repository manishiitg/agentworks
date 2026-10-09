package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A message queued behind a running turn has no execution registered until it starts. The waiter must keep waiting while the chat is
// busy and fail only when the chat is idle and the execution never appears (eight quick asks to one Crew failed after 5 s).
func TestQueuedTurnWaitsWhileItsChatIsBusy(t *testing.T) {
	old := rootRegistrationGrace
	rootRegistrationGrace = 50 * time.Millisecond
	defer func() { rootRegistrationGrace = old }()

	busy := &StreamingAPI{}
	busy.setSessionBusy("crew-chat", true)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	if err := busy.waitForConversationTurnTree(ctx, "crew-chat", "exec-queued", 0); err == nil || strings.Contains(err.Error(), "was not registered") {
		t.Fatalf("a busy chat must keep the queued turn waiting until the caller gives up, got %v", err)
	}

	idle := &StreamingAPI{}
	err := idle.waitForConversationTurnTree(context.Background(), "idle-chat", "exec-missing", 0)
	if err == nil || !strings.Contains(err.Error(), "was not registered") {
		t.Fatalf("an idle chat whose turn never registers must fail as before, got %v", err)
	}
}
