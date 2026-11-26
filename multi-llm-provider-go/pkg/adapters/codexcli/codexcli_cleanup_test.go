package codexcli

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestCleanupCodexCLIInteractiveSessionsDoesNotBlockOnBusySession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	session := &codexInteractiveSession{
		ownerSessionID:  "busy-owner",
		tmuxSessionName: "mlp-codex-cli-cleanup-busy-test",
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	oldPersistent := codexPersistentRegistry.Replace(map[string]*codexInteractiveSession{
		session.ownerSessionID: session,
	})
	t.Cleanup(func() {
		codexPersistentRegistry.Replace(oldPersistent)
	})

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- CleanupCodexCLIInteractiveSessions(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cleanup error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cleanup blocked on busy session mutex")
	}
}
