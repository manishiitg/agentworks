package services

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// PLAT-442 step 4: a Crew at the shared root is a valid bot destination that names no owner, and a moved Crew's old
// spelling is the same destination.
func TestBotScopeAcceptsASharedCrewAndFoldsOldSpellings(t *testing.T) {
	const (
		shared      = "Crew/sde-1a2b3c4d"
		oldPhysical = "_users/alice/Chats/Work/projects/sde-1a2b3c4d"
		oldLogical  = "Chats/Work/projects/sde-1a2b3c4d"
	)
	t.Cleanup(func() { SetCrewScopeHooks(nil, nil, nil) })
	// No hooks installed: a shared root is a valid destination as it stands; the bare root and a hidden entry are not.
	for _, p := range []string{shared, shared + "/code"} {
		if err := ValidateBotScope(p, "work"); err != nil {
			t.Errorf("ValidateBotScope(%q) = %v", p, err)
		}
	}
	for _, p := range []string{"Crew", "Crew/", "Crew/.migrating/x"} {
		if err := ValidateBotScope(p, "work"); !errors.Is(err, ErrLogicalCrewScope) {
			t.Errorf("ValidateBotScope(%q) = %v, want a refusal", p, err)
		}
	}
	if err := ValidateBotScope(oldLogical, "work"); !errors.Is(err, ErrLogicalCrewScope) {
		t.Errorf("a logical crew path is still refused: %v", err)
	}
	if err := ValidateBotScope(oldPhysical, "work"); err != nil {
		t.Errorf("an old physical path is still valid: %v", err)
	}
	// Without the server's hooks nothing is folded: two spellings are two destinations, as before the move.
	if SameSlackScopePath(oldPhysical, shared) {
		t.Error("the old and new spelling matched with no alias installed")
	}
	// With them, the moved Crew's spellings are one destination, owned by the registered owner.
	SetCrewScopeHooks(
		func(p string) string {
			if strings.Contains(p, "sde-1a2b3c4d") {
				return "alice"
			}
			return ""
		},
		func(p string) string {
			const marker = "Chats/Work/projects/sde-1a2b3c4d"
			if i := strings.Index(p, marker); i >= 0 {
				return shared + p[i+len(marker):]
			}
			return p
		},
		func(context.Context, string) []SharedCrewListing { return nil },
	)
	for _, spelling := range []string{oldPhysical, oldLogical, shared} {
		if !SameSlackScopePath(spelling, shared) || !SameSlackScopePath(shared, spelling) {
			t.Errorf("%q is not the same destination as %q", spelling, shared)
		}
	}
	if SameSlackScopePath(shared, "Crew/other-99999999") || SameSlackScopePath(oldPhysical, "_users/bob/Chats/Work/projects/other-11112222") {
		t.Error("different Crews matched")
	}
	// The route's workspace user: the registry's, for the shared path and for an old spelling alike.
	for _, p := range []string{shared, oldPhysical, oldLogical} {
		if got := routeWorkspaceUserID(ChannelRoute{WorkspacePath: p}, "fallback"); got != "alice" && !(p == oldLogical && got == "fallback") {
			t.Errorf("routeWorkspaceUserID(%q) = %q", p, got)
		}
	}
	if got := routeWorkspaceUserID(ChannelRoute{WorkspacePath: shared, WorkspaceUserID: "explicit"}, "fallback"); got != "explicit" {
		t.Errorf("an explicit user was overridden: %q", got)
	}
	// An unknown shared Crew has no owner: the fallback, never "" or the path.
	if got := routeWorkspaceUserID(ChannelRoute{WorkspacePath: "Crew/unknown-00000000"}, "fallback"); got != "fallback" {
		t.Errorf("routeWorkspaceUserID of an unregistered shared Crew = %q", got)
	}
}
