package server

import "testing"

// Crew browser scope uses the registered shared-root project identity.
func TestPlaywrightCrewScopeAndVisibility(t *testing.T) {
	registry := withProjectOwnerRegistry(t)
	for _, rec := range []projectOwnerRecord{
		{Product: "work", Folder: "qa-a1", OwnerID: "alice", Shared: true},
		{Product: "work", Folder: "qa-b2", OwnerID: "bob", Shared: true},
	} {
		if err := registry.Register(rec); err != nil {
			t.Fatal(err)
		}
	}
	api := &StreamingAPI{}
	alice := &UserClaims{UserID: "alice"}
	bob := &UserClaims{UserID: "bob"}
	scope := playwrightScope("alice", "Crew/qa-a1/")
	if scope != "Crew/qa-a1" {
		t.Fatalf("crew scope = %q", scope)
	}
	if got := playwrightScope("bob", "Crew/qa-a1"); got != scope {
		t.Fatalf("shared crew scope = %q, want %q", got, scope)
	}
	if !api.playwrightVisible(alice, "Crew/qa-a1", "alice", scope) {
		t.Fatal("crew owner must see the crew's Playwright test")
	}
	if api.playwrightVisible(bob, "Crew/qa-b2", "alice", scope) {
		t.Fatal("another owner's same-named crew must not see it")
	}
	if api.playwrightVisible(alice, "Crew/qa-other", "alice", scope) {
		t.Fatal("a different crew must not see it")
	}
	if api.playwrightVisible(nil, "Crew/qa-a1", "alice", scope) {
		t.Fatal("an anonymous caller must not see it")
	}
	// Workflows are unchanged: private to the user who ran the test.
	if playwrightScope("alice", "Workflow/test/") != "Workflow/test" ||
		!api.playwrightVisible(alice, "Workflow/test", "alice", "Workflow/test") ||
		api.playwrightVisible(bob, "Workflow/test", "alice", "Workflow/test") {
		t.Fatal("workflow Playwright visibility changed")
	}
}
