package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// joinCrewTeam saves the Crew's Team as on with exactly these joined members: the only builders besides its main owner.
// No people is the Team with everyone removed.
func joinCrewTeam(t *testing.T, folder string, people ...string) {
	t.Helper()
	ensureProjectStateRoot(t)
	rec, found := defaultProjectOwners().Lookup(crewProfileID, folder)
	if !found || rec.ProjectID == "" {
		t.Fatalf("Crew %s needs a registered project id to have a Team", folder)
	}
	team := crewTeam{ProjectID: rec.ProjectID, OwnerID: rec.OwnerID, On: true, People: []crewTeamPerson{}}
	for _, id := range people {
		team.People = append(team.People, crewTeamPerson{UserID: id})
	}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
}

// A joined Team member has the main owner's PERMISSIONS on the Crew (build, files, the Builder), while IDENTITY stays the
// main owner's.
func TestCrewTeamMemberHasPermissionsButNotIdentity(t *testing.T) {
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
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: folder, ProjectID: "demo-proj", OwnerID: "owner", Shared: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPrivate("work", folder, true); err != nil {
		t.Fatal(err)
	}
	joinCrewTeam(t, folder, "co")
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
	// Joining one Crew gives nothing in the same owner's other Crews.
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "sibling-12345678", ProjectID: "sibling-proj", OwnerID: "owner", Shared: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := cleanAgentProfileWorkspace("Crew/sibling-12345678", "co"); err == nil {
		t.Fatal("a Team member opened the owner's sibling Crew")
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

	// Owner, 5 Nov: a Crew co-owner's turn runs on their own login, never the main owner's; nobody's turn borrows it.
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
	for _, who := range []string{"co", "owner", "ann"} {
		if got := coOwnerTurnOwner(context.Background(), who, root+"/instructions.md"); got != "" {
			t.Fatalf("%s's Crew turn runs as %q, want their own login", who, got)
		}
	}

	// Every owner reads every Builder conversation on disk (owner, 2026-10-16); readers read none.
	if crewBuilderDiskHiddenFromUser("co", root) || crewBuilderDiskHiddenFromUser("owner", root) || !crewBuilderDiskHiddenFromUser("ann", root) {
		t.Fatal("Builder conversation transcripts must be readable by every owner and by no reader")
	}
}

// A Team is a shared room (owner, 5 Nov): a joined member builds the Crew like a co-owner while the Team is on; an
// invite, Team off or removal takes it away at once. A co_owners entry left in the registry gives no access (owner, 6 Nov).
func TestCrewTeamMemberBuildsLikeCoOwnerOnlyWhileJoined(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"ann","username":"ann","email":"ann@example.com","products":["work"]}]}`)
	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })
	const folder = "team-12345678"
	root := "Crew/" + folder
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: "owner", Shared: true, ProjectID: "team-proj"}); err != nil {
		t.Fatal(err)
	}
	save := func(on, invited bool, people ...string) {
		t.Helper()
		team := crewTeam{ProjectID: "team-proj", OwnerID: "owner", On: on, People: []crewTeamPerson{}}
		for _, id := range people {
			team.People = append(team.People, crewTeamPerson{UserID: id, Invited: invited})
		}
		if err := writeCrewTeam(team); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name          string
		on, invited   bool
		people        []string
		wantCoOwnerOf bool
	}{{"invited", true, true, []string{"ann"}, false}, {"joined", true, false, []string{"ann"}, true}, {"team off", false, false, []string{"ann"}, false}, {"removed", true, false, nil, false}} {
		save(tc.on, tc.invited, tc.people...)
		if got := crewProjectOwnedByCaller("ann", root); got != tc.wantCoOwnerOf {
			t.Fatalf("%s: ann builds the Crew = %v, want %v", tc.name, got, tc.wantCoOwnerOf)
		}
		if crewProjectOwnedByCallerStrict("ann", root) {
			t.Fatalf("%s: a team member became the owner", tc.name)
		}
	}
	// The old list on disk, for a person who is also a joined member, then with the Team off: only the Team counts.
	path := registry.path()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	stored["projects"].(map[string]any)[projectOwnerKey("work", folder)].(map[string]any)["co_owners"] = []string{"ann"}
	if raw, err = json.Marshal(stored); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ref := crewPathRef{Root: root, OwnerID: "owner", Shared: true}
	save(true, false, "ann")
	if !crewProjectOwnedByCaller("ann", root) {
		t.Fatal("joined member lost the Crew")
	}
	save(false, false, "ann")
	if crewProjectOwnedByCaller("ann", root) || crewRoleOf(root, "ann") == crewRoleCoOwner || crewAccessFor(&UserClaims{UserID: "ann", Username: "ann"}, ref) == crewAccessOwner {
		t.Fatal("a co_owners entry in the registry gave builder access with the Team off")
	}
	// One Files view (owner, 5 Nov): the server opens a joined member's Files on the folder the Team shares; the owner
	// keeps the Crew's root.
	if err := writeCrewTeam(crewTeam{ProjectID: "team-proj", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "ann"}}, Folder: crewTeamFolder{ID: "f1", Folders: []string{"team"}}}); err != nil {
		t.Fatal(err)
	}
	if got := crewTeamMemberFilesPath("ann", root); got != root+"/team" {
		t.Fatalf("member Files root = %q, want %q", got, root+"/team")
	}
	if got := crewTeamMemberFilesPath("owner", root); got != root {
		t.Fatalf("owner Files root = %q, want %q", got, root)
	}
	// A Mac Crew: a member on the web works in their own copy in the Crew's folder, team-copies/<member>/.
	if err := crewroot.WriteSync("team-proj", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "owner-pair", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatal(err)
	}
	if got, want := crewTeamMemberFilesPath("ann", root), root+"/team-copies/ann"; got != want {
		t.Fatalf("member Files root on a Mac Crew = %q, want %q", got, want)
	}
	// team-copies/ is never shared or synced, Whole Crew included, and Files lists only a member's own copy.
	whole := crewTeamFolder{ID: "f2", Whole: true}
	if whole.holds("team-copies/ann/notes.md") || whole.walks("team-copies") || crewTeamAllowed("team-copies/ann/notes.md") || !whole.holds("notes.md") {
		t.Fatal("team-copies/ is part of the Whole Crew share")
	}
	for _, tc := range []struct {
		entry, listing, user string
		hidden               bool
	}{
		{root + "/team-copies", root, "owner", true},
		{root + "/team-copies/ann", root, "ann", true},
		{root + "/team-copies/ann/notes.md", root + "/team-copies/ann", "ann", false},
		{root + "/team-copies/ann/notes.md", root + "/team-copies/ann", "bob", true},
		{root + "/notes.md", root, "owner", false},
	} {
		if got := crewroot.TeamCopyHidden(tc.entry, tc.listing, tc.user); got != tc.hidden {
			t.Fatalf("Files listing of %q in %q for %s: hidden=%v, want %v", tc.entry, tc.listing, tc.user, got, tc.hidden)
		}
	}
}
