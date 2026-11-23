package claudecode

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestCleanupClaudeCodeTmuxSessionsDoesNotBlockOnBusyPersistentSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	session := &claudeInteractivePersistentSession{
		ownerSessionID:  "busy-owner",
		tmuxSessionName: "mlp-claude-code-exp-cleanup-busy-test",
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	oldPersistent := claudeInteractivePersistentRegistry.Replace(map[string]*claudeInteractivePersistentSession{
		session.ownerSessionID: session,
	})
	t.Cleanup(func() {
		claudeInteractivePersistentRegistry.Replace(oldPersistent)
	})

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- CleanupClaudeCodeTmuxSessions(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cleanup error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cleanup blocked on busy persistent session mutex")
	}
}
