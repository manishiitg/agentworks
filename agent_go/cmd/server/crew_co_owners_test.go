package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Crew co-owners (owner decision 2026-10-15): a co-owner has the main owner's PERMISSIONS on the Crew (build, files, the
// Builder), while IDENTITY stays the main owner's, and the creator can never be removed.
func TestCrewCoOwnerHasPermissionsButNotIdentity(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","email":"owner@example.com","products":["work"]},{"id":"co","username":"co","email":"co@example.com","products":["work"]},{"id":"ann","username":"ann","email":"ann@example.com","products":["work"]},{"id":"nowork","username":"nowork","email":"nowork@example.com","products":["agentworks"]}]}`)
	if name := crewOwnerDisplayName("owner"); name != "owner@example.com" {
		t.Fatalf("tab controller is not identified by email: %q", name)
	}
	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() {
		defaultProjectOwnersOverride = prev
		crewOwners.mu.Lock()
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.mu.Unlock()
	})
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
	// A co-owner's Builder chat opens the Crew's folder (tool registration needs it); a stranger's does not.
	if _, err := cleanAgentProfileWorkspace(root, "co"); err != nil {
		t.Fatalf("co-owner refused the Crew folder: %v", err)
	}
	if _, err := cleanAgentProfileWorkspace(root, "ann"); err == nil {
		t.Fatal("a stranger opened the Crew folder")
	}
	ref := crewPathRef{Root: root, OwnerID: "owner", Shared: true}
	if got := crewAccessFor(&UserClaims{UserID: "co", Username: "co"}, ref); got != crewAccessOwner {
		t.Fatalf("co-owner access = %v, want owner", got)
	}
	if got := (&StreamingAPI{}).crewBrowserAccess(&UserClaims{UserID: "co"}, root); got != WorkflowAccessOwner {
		t.Fatalf("co-owner browser access: %v", got)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "co"}))
	if scope, err := personalConnectionItemScope(request, root, false, "Google", "Google apps"); err != nil || scope.Workspace != root {
		t.Fatalf("co-owner project accounts: %+v %v", scope, err)
	}
	if got := crewAccessFor(&UserClaims{UserID: "ann", Username: "ann"}, ref); got != crewAccessNone {
		t.Fatalf("a stranger on a private Crew got access %v", got)
	}
	// The Crew's files through the raw workspace proxy: the co-owner reaches them, a stranger does not.
	if workspaceProxyPathIsOtherUser(root+"/instructions.md", "co") || !workspaceProxyPathIsOtherUser(root+"/instructions.md", "ann") {
		t.Fatal("the workspace proxy gate ignores co-owners")
	}

	// A co-owner's turn runs as the main owner for accounts and limits; the owner's own and a stranger's turn do not change.
	crewOwners.mu.Lock()
	crewOwners.entries = map[string]crewOwnerEntry{}
	crewOwners.read = func(_ context.Context, r string) string {
		if r == root {
			return "owner"
		}
		return ""
	}
	crewOwners.mu.Unlock()
	t.Cleanup(func() { crewOwners.mu.Lock(); crewOwners.read = nil; crewOwners.mu.Unlock() })
	if got := coOwnerTurnOwner(context.Background(), "co", root+"/instructions.md"); got != "owner" {
		t.Fatalf("co-owner turn owner = %q, want owner", got)
	}
	if coOwnerTurnOwner(context.Background(), "owner", root) != "" || coOwnerTurnOwner(context.Background(), "ann", root) != "" {
		t.Fatal("only a co-owner's turn runs as the main owner")
	}

	// Every owner reads every Builder conversation on disk (owner, 2026-10-16); readers read none.
	if crewBuilderDiskHiddenFromUser("co", root) || crewBuilderDiskHiddenFromUser("owner", root) || !crewBuilderDiskHiddenFromUser("ann", root) {
		t.Fatal("Builder conversation transcripts must be readable by every owner and by no reader")
	}

	// The creator can never be removed, a co-owner can; both by email, by an owner.
	crew := crewProjectBinding{OwnerID: "owner", OwnedByCaller: true}
	crew.Binding.WorkspacePath = root
	api := &StreamingAPI{}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	if _, status, _, _ := api.changeCrewOwners(&UserClaims{UserID: "co", Username: "co"}, crew, "remove", "co@example.com"); status != 0 {
		t.Fatalf("a co-owner could not change the owners: status=%d", status)
	}
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
