package server

import (
	"net/http"
	"testing"
)

// Crew co-owners (owner decision 2026-10-15): a co-owner has the main owner's PERMISSIONS on the Crew (build, files, the
// Builder), while IDENTITY stays the main owner's, and the creator can never be removed.
func TestCrewCoOwnerHasPermissionsButNotIdentity(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","email":"owner@example.com","products":["work"]},{"id":"co","username":"co","email":"co@example.com","products":["work"]},{"id":"ann","username":"ann","email":"ann@example.com","products":["work"]},{"id":"nowork","username":"nowork","email":"nowork@example.com","products":["agentworks"]}]}`)
	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })
	const folder = "demo-12345678"
	root := "Crew/" + folder
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: "owner", Shared: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPrivate("work", folder, true); err != nil {
		t.Fatal(err)
	}
	if changed, err := registry.AddCoOwner("work", folder, "co"); err != nil || !changed {
		t.Fatalf("add co-owner: changed=%v err=%v", changed, err)
	}
	if changed, _ := registry.AddCoOwner("work", folder, "co"); changed {
		t.Fatal("adding the same co-owner twice changed the list")
	}
	if changed, _ := registry.AddCoOwner("work", folder, "owner"); changed {
		t.Fatal("the main owner became a co-owner")
	}

	// Roles, on a PRIVATE Crew: the co-owner still sees and builds it; a stranger does not.
	for user, want := range map[string]string{"owner": crewRoleOwner, "co": crewRoleCoOwner, "ann": crewRoleNone} {
		if got := crewRoleOf(root, user); got != want {
			t.Fatalf("role of %s = %q, want %q", user, got, want)
		}
	}
	// Permission: owner and co-owner; identity (the owner's own browser, mailbox): the main owner only.
	if !crewProjectOwnedByCaller("co", root) || crewProjectOwnedByCallerStrict("co", root) || !crewProjectOwnedByCallerStrict("owner", root) || crewProjectOwnedByCaller("ann", root) {
		t.Fatal("permission and identity predicates disagree with the co-owner rules")
	}
	if owner, ok := crewProjectOwnerID(root); !ok || owner != "owner" {
		t.Fatalf("a co-owner changed whose Crew it is: %q", owner)
	}
	ref := crewPathRef{Root: root, OwnerID: "owner", Shared: true}
	if got := crewAccessFor(&UserClaims{UserID: "co", Username: "co"}, ref); got != crewAccessOwner {
		t.Fatalf("co-owner access = %v, want owner", got)
	}
	if got := crewAccessFor(&UserClaims{UserID: "ann", Username: "ann"}, ref); got != crewAccessNone {
		t.Fatalf("a stranger on a private Crew got access %v", got)
	}
	// The Crew's files through the raw workspace proxy: the co-owner reaches them, a stranger does not.
	if workspaceProxyPathIsOtherUser(root+"/instructions.md", "co") || !workspaceProxyPathIsOtherUser(root+"/instructions.md", "ann") {
		t.Fatal("the workspace proxy gate ignores co-owners")
	}

	// The main owner's own Builder conversations on disk stay theirs: hidden from a co-owner, shown to the owner.
	if !crewBuilderDiskHiddenFromUser("co", root) || crewBuilderDiskHiddenFromUser("owner", root) {
		t.Fatal("a co-owner can read the main owner's Builder conversation transcripts")
	}

	// The creator can never be removed, a co-owner can; both by email, by an owner.
	crew := crewProjectBinding{OwnerID: "owner", OwnedByCaller: true}
	crew.Binding.WorkspacePath = root
	api := &StreamingAPI{}
	claims := &UserClaims{UserID: "co", Username: "co"}
	if _, status, code, _ := api.changeCrewOwners(claims, crew, "remove", "owner@example.com"); status != http.StatusConflict || code != "creator" {
		t.Fatalf("removing the creator: status=%d code=%q", status, code)
	}
	if state, status, _, _ := api.changeCrewOwners(claims, crew, "remove", "co@example.com"); status != 0 || len(state.CoOwners) != 0 {
		t.Fatalf("removing a co-owner: status=%d state=%+v", status, state)
	}
	if crewIsCoOwner(root, "co") {
		t.Fatal("a removed co-owner still builds")
	}
}
