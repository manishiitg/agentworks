package services

import (
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref/reftest"
)

// PLAT-435: both spellings, plus another user's physical path.
func TestWorkspaceRefSites(t *testing.T) {
	const logical = "Chats/Work/projects/c1"
	phys := workspaceref.MustParse(logical).Physical("alice")
	other := reftest.OtherUser("alice", logical)

	reftest.BothSpellings(t, "alice", logical, func(t *testing.T, spelling string) {
		if !SameSlackScopePath(spelling, logical) || !SameSlackScopePath(spelling, phys) {
			t.Fatalf("%q must name the same crew as both spellings", spelling)
		}
		if SameSlackScopePath(spelling, logical+"/x") {
			t.Fatalf("%q equals a deeper path", spelling)
		}
	})
	if SameSlackScopePath(phys, other) || !SameSlackScopePath(other, logical) {
		t.Fatal("different owners never equal; logical equals any owner's physical (the owner's own)")
	}
	if SameSlackScopePath("_users/alice", "") || !SameSlackScopePath("", "") {
		t.Fatal("empty scope handling changed")
	}

	if got := physicalBotScopeOwner(phys); got != "alice" {
		t.Fatalf("owner = %q", got)
	}
	if physicalBotScopeOwner(logical) != "" || physicalBotScopeOwner("_users/alice") != "" {
		t.Fatal("logical or bare owner has no scope owner")
	}
	if got := routeWorkspaceUserID(ChannelRoute{WorkspacePath: other}, "fallback"); got != "other-alice" {
		t.Fatalf("route owner = %q", got)
	}
	if got := routeWorkspaceUserID(ChannelRoute{WorkspacePath: logical}, "fallback"); got != "fallback" {
		t.Fatalf("logical route owner = %q", got)
	}
}
