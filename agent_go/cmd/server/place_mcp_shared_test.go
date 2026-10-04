package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// connectPlaceForTest adds a connection to a place as owner's login (a workflow, Relay, Crew or Code root).
func connectPlaceForTest(t *testing.T, owner, root, name string) string {
	t.Helper()
	return connectCodeForTest(t, owner, root, name)
}

// A connection added to a workflow, Relay, Crew or Code belongs to that place: every session of it resolves
// it, a run or step that has no person included, whoever added it. Another place's session, a session with no
// place, and a name the place does not have are not claimed (they fall through to the other scopes).
func TestPlaceConnectionsResolveForEverySessionOfThatPlace(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	api := &StreamingAPI{}
	ctx := context.Background()
	root := "Workflow/wf1"
	internal := connectPlaceForTest(t, "alice", root, "upwork")

	// The step of a run: server-set working dir under the workflow, no person.
	common.SetSessionWorkingDir("msgseq-run-step", "Workflow/wf1/runs/iteration-0/job-search/execution")
	common.SetSessionWorkingDir("other-place-step", "Workflow/wf2/runs/iteration-0/x")
	t.Cleanup(func() {
		common.ClearSessionShellConfig("msgseq-run-step")
		common.ClearSessionShellConfig("other-place-step")
	})

	for _, name := range []string{"upwork", "UPWORK", internal} {
		resolved, handled, err := api.resolvePlaceAttachedMCP(ctx, "msgseq-run-step", name)
		if !handled || err != nil || resolved == nil || resolved.Name != internal {
			t.Fatalf("run step could not use the place's connection by %q: %+v %v %v", name, resolved, handled, err)
		}
	}
	if _, handled, _ := api.resolvePlaceAttachedMCP(ctx, "msgseq-run-step", "linear"); handled {
		t.Error("a name the place does not have was claimed")
	}
	if _, handled, _ := api.resolvePlaceAttachedMCP(ctx, "other-place-step", "upwork"); handled {
		t.Error("another place's session resolved this place's connection")
	}
	if _, handled, _ := api.resolvePlaceAttachedMCP(ctx, "session-with-no-place", "upwork"); handled {
		t.Error("a session with no place resolved a place connection")
	}

	// Removing it from the place stops it at once.
	if err := removePlaceMCP("alice", "upwork", root); err != nil {
		t.Fatal(err)
	}
	if _, handled, _ := api.resolvePlaceAttachedMCP(ctx, "msgseq-run-step", "upwork"); handled {
		t.Error("a removed connection still resolved")
	}
}

// Two connections of one place with the same plain name are never guessed between.
func TestPlaceConnectionWithTwoMatchesIsRefusedNotGuessed(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	api := &StreamingAPI{}
	root := "Workflow/wf3"
	a := connectPlaceForTest(t, "alice", root, "upwork")
	b := connectPlaceForTest(t, "bob", root, "upwork")
	if a == b {
		t.Fatal("two people's connections share an internal name")
	}
	common.SetSessionWorkingDir("wf3-step", "Workflow/wf3/runs/iteration-0/s")
	t.Cleanup(func() { common.ClearSessionShellConfig("wf3-step") })
	_, handled, err := api.resolvePlaceAttachedMCP(context.Background(), "wf3-step", "upwork")
	if !handled || err == nil || !strings.Contains(err.Error(), "exact name") {
		t.Fatalf("an ambiguous plain name was not refused: %v %v", handled, err)
	}
	if resolved, handled, err := api.resolvePlaceAttachedMCP(context.Background(), "wf3-step", a); !handled || err != nil || resolved.Name != a {
		t.Fatalf("the exact name was not honoured: %+v %v %v", resolved, handled, err)
	}
}

// The access rule: a person needs access to the place, a session with no person (a run) is the place's own, and
// a Code stays its owner's.
func TestPlaceConnectionsAreUsableByTheirPlacesPeopleOnly(t *testing.T) {
	ctx := context.Background()
	code := "_users/alice/Chats/Code/projects/x"
	if !placeMCPUsableBy(ctx, "", code) {
		t.Error("a session with no person was refused")
	}
	if !placeMCPUsableBy(ctx, "alice", code) {
		t.Error("the Code's owner was refused")
	}
	if placeMCPUsableBy(ctx, "bob", code) {
		t.Error("another person used a Code's connections")
	}
	if placeMCPUsableBy(ctx, "alice", "") || placeMCPUsableBy(ctx, "", "not/a/place/root") {
		t.Error("an invalid place root was accepted")
	}
}

// attachedMCPServersForRoot is the place's, not the adder's: a second person with access gets what the first
// added (a Code's owner here, since a Code has one person).
func TestAttachedServersAreTheirPlacesNotTheirAdders(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	code := "_users/alice/Chats/Code/projects/p"
	internal := connectPlaceForTest(t, "alice", code, "deepwiki")
	names, overrides := attachedMCPServersForRoot(personContext("alice"), code)
	if len(names) != 1 || names[0] != internal || overrides[internal].Server == nil {
		t.Fatalf("the owner did not get the place's connection: %v", names)
	}
	if names, _ := attachedMCPServersForRoot(personContext("bob"), code); len(names) != 0 {
		t.Errorf("another person got a Code's connections: %v", names)
	}
}
