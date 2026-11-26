package claudecode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #2911/Codex: the staging backstop used to stop credential observation after
// thirty seconds although the interactive process was still alive.
func TestClaudeTmuxPolicyCleanupKeepsCredentialObserver(t *testing.T) {
	policy := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policy, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	name := "refresh-lifecycle-fixture"
	closed := make(chan struct{})
	retainClaudeSessionSandboxCleanup(name, policy, func() { close(closed) }, time.Millisecond)
	t.Cleanup(func() { unregisterClaudeInteractiveSession(name) })
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(policy); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("policy staging file not removed")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-closed:
		t.Fatal("staging cleanup stopped a live observer")
	default:
	}
	unregisterClaudeInteractiveSession(name)
	select {
	case <-closed:
	default:
		t.Fatal("session close did not stop its observer")
	}
	unregisterClaudeInteractiveSession(name) // no duplicate callback
}
