package server

import (
	"testing"
)

// Sharing opens a product only when its owner says so: RunEveryone gives every
// account that may use the product reader access, never owner or editor, and a
// disabled account gets nothing. Without it a stranger has none (no change).
func TestWorkflowRunEveryoneGivesReaderOnlyWhenTheOwnerSharedIt(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true},{"id":"stranger","username":"stranger","products":["agentworks"]},{"id":"nolicense","username":"nolicense"},{"id":"gone","username":"gone","disabled":true}]}`)
	m := NewWorkflowManifest("Shared run")
	m.CreatedBy = "owner"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}}
	if got := workflowAccessForManifest(&UserClaims{UserID: "stranger", Username: "stranger"}, m); got != WorkflowAccessNone {
		t.Fatalf("an unshared workflow must stay closed to a stranger, got %v", got)
	}
	m.Access.RunEveryone = true
	if got := workflowAccessForManifest(&UserClaims{UserID: "stranger", Username: "stranger"}, m); got != WorkflowAccessRead {
		t.Fatalf("a workflow shared with everyone is a reader for any account, got %v", got)
	}
	if got := workflowAccessForManifest(&UserClaims{UserID: "owner", Username: "owner"}, m); got != WorkflowAccessOwner {
		t.Fatalf("sharing must not demote the owner, got %v", got)
	}
	if got := workflowAccessForManifest(&UserClaims{UserID: "nolicense", Username: "nolicense"}, m); got != WorkflowAccessNone {
		t.Fatalf("an account without the product gets nothing, got %v", got)
	}
	if got := workflowAccessForManifest(&UserClaims{UserID: "gone", Username: "gone"}, m); got != WorkflowAccessNone {
		t.Fatalf("a disabled account gets nothing, got %v", got)
	}
}

// 'people' mode reports an email that matches no active account instead of dropping it.
func TestResolveShareEmailsReportsUnknownEmails(t *testing.T) {
	withMemoryUserDirectory(t, `{"users":[{"id":"u1","username":"ann","email":"ann@example.com"},{"id":"u2","username":"bob","email":"bob@example.com","disabled":true}]}`)
	dir, err := readUserDirectoryFile()
	if err != nil {
		t.Fatal(err)
	}
	people, unknown := resolveShareEmails(dir, []string{" Ann@Example.com ", "ann@example.com", "bob@example.com", "new@example.com"})
	if len(people) != 1 || people[0].ID != "u1" {
		t.Fatalf("only the active account matches, once: %+v", people)
	}
	if len(unknown) != 2 || unknown[0] != "bob@example.com" || unknown[1] != "new@example.com" {
		t.Fatalf("a disabled or missing account must be reported back: %v", unknown)
	}
}

// A private Crew shared with named people opens for them and for nobody else.
func TestPrivateCrewSharedWithNamedPeopleOpensOnlyForThem(t *testing.T) {
	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })
	root := "Crew/demo-12345678"
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "demo-12345678", OwnerID: "owner", Shared: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPrivate("work", "demo-12345678", true); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetRunPeople("work", "demo-12345678", []string{"ann"}); err != nil {
		t.Fatal(err)
	}
	if !crewSharedWith(root, "ann") {
		t.Fatal("a named person must open a private Crew shared with them")
	}
	if crewSharedWith(root, "bob") {
		t.Fatal("anyone not named must stay out of a private Crew")
	}
}
