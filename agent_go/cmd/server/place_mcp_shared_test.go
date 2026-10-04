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

// A connection lives where it was added and nowhere else: from inside a workflow, Relay, Crew or Code a person's
// own connection (kept in their store, or added to another place) never resolves; a chat outside any place
// keeps resolving the person's own connections until those chats have a place of their own.
func TestPlaceSessionsNeverReachAPersonsOtherConnections(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	t.Setenv("CAPLAYER_SERVICE_URL", "")
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")
	if _, err := addPlaceMCPServer("alice", placeMCPServer{Name: "linear", Catalog: "Linear", URL: "https://example.com/mcp", Transport: "http"}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	outside := personContext("alice")
	if own, err := api.resolveGovernedMCP(outside, "alice", "Linear"); err != nil || own == nil {
		t.Fatalf("a chat outside any place lost the person's own connection: %+v %v", own, err)
	}
	inside := context.WithValue(outside, placeScopedKey{}, true)
	resolved, err := api.resolveGovernedMCP(inside, "alice", "Linear")
	if err == nil || resolved != nil {
		t.Fatalf("a place session reached the person's own connection: %+v", resolved)
	}
	internal := placeMCPInternalName("alice", "linear")
	if _, err := api.resolveGovernedMCP(inside, "alice", internal); err == nil || !strings.Contains(err.Error(), "not attached to this") {
		t.Fatalf("a place session reached another place's internal name: %v", err)
	}
}

// placeAttachmentNamed finds a place's connection by its plain or internal name, whoever added it.
func TestPlaceAttachmentNamedFindsByPlainOrInternalName(t *testing.T) {
	withMCPConnectionsRoot(t)
	t.Setenv("AUTH_SECRET", "test-auth-secret-with-enough-entropy")
	root := "Workflow/named"
	internal := connectPlaceForTest(t, "alice", root, "upwork")
	for _, name := range []string{"upwork", "Upwork", internal} {
		if a, ok := placeAttachmentNamed(root, name); !ok || a.Owner != "alice" || a.Server != "upwork" {
			t.Errorf("%q not found: %+v %v", name, a, ok)
		}
	}
	if _, ok := placeAttachmentNamed(root, "linear"); ok {
		t.Error("a connection the place does not have was found")
	}
}
