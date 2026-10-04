package videoproduct

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref/reftest"
)

// PLAT-435: both spellings, another user's physical path, escaping path.
func TestProfileWorkspaceRootBothSpellings(t *testing.T) {
	const logical = "Chats/Video Studio/projects/v1"
	_, want := reftest.Spellings("alice", logical)
	reftest.BothSpellings(t, "alice", logical, func(t *testing.T, spelling string) {
		if got := profileWorkspaceRoot("alice", spelling); got != want {
			t.Fatalf("%q -> %q want %q", spelling, got, want)
		}
	})
	other := reftest.OtherUser("alice", logical)
	if got := profileWorkspaceRoot("alice", other); got != other {
		t.Fatalf("another user's path = %q", got)
	}
	if got := profileWorkspaceRoot("alice", "../bob/x"); got == "_users/alice" || got == "_users/bob/x" {
		t.Fatalf("escaping path = %q", got)
	}
}
