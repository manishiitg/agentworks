package browser

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref/reftest"
)

// PLAT-435: capture binds the caller's own project under both spellings and
// refuses another user's.
func TestCaptureWorkspaceBothSpellings(t *testing.T) {
	const logical = "Chats/Work/projects/c1"
	reftest.BothSpellings(t, "alice", logical+"/code", func(t *testing.T, spelling string) {
		if got := captureWorkspace("alice", &common.SessionShellConfig{WorkflowPath: spelling}); got != logical {
			t.Fatalf("%q -> %q", spelling, got)
		}
	})
	other := reftest.OtherUser("alice", logical+"/code")
	if got := captureWorkspace("alice", &common.SessionShellConfig{WorkflowPath: other, WorkingDir: other}); got != "" {
		t.Fatalf("another user's project bound: %q", got)
	}
}
