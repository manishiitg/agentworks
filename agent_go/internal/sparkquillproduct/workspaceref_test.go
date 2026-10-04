package sparkquillproduct

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref/reftest"
)

// PLAT-435: both spellings resolve to the caller's physical root; another
// user's physical path is kept as theirs; an escaping path never becomes the
// caller's whole tree.
func TestRuntimeRootBothSpellings(t *testing.T) {
	const logical = "Chats/SparkQuill"
	_, want := reftest.Spellings("alice", logical)
	reftest.BothSpellings(t, "alice", logical, func(t *testing.T, spelling string) {
		if got := runtimeRoot("alice", spelling); got != want {
			t.Fatalf("%q -> %q want %q", spelling, got, want)
		}
	})
	other := reftest.OtherUser("alice", logical)
	if got := runtimeRoot("alice", other); got != other {
		t.Fatalf("another user's path = %q", got)
	}
	if got := runtimeRoot("alice", "../bob/x"); got == "_users/alice" || got == "_users/bob/x" {
		t.Fatalf("escaping path = %q", got)
	}
	if got := runtimeRoot("", logical); got != "_users/default/Chats/SparkQuill" {
		t.Fatalf("empty user = %q", got)
	}
}
