package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// joinCrewTeam makes exactly these people the co-owners of the server Crew at folder: the only builders besides its main
// owner (owner, 21 Nov: a Crew on the server has co-owners and no Team). No people removes them all.
func joinCrewTeam(t *testing.T, folder string, people ...string) {
	t.Helper()
	ensureProjectStateRoot(t)
	rec, found := defaultProjectOwners().Lookup(crewProfileID, folder)
	if !found || rec.ProjectID == "" {
		t.Fatalf("Crew %s needs a registered project id to have co-owners", folder)
	}
	for _, id := range rec.CoOwners {
		if _, err := defaultProjectOwners().RemoveCoOwner(crewProfileID, folder, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range people {
		if _, err := defaultProjectOwners().AddCoOwner(crewProfileID, folder, id); err != nil {
			t.Fatal(err)
		}
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

// Who builds besides the main owner depends on where the Crew lives (owner, 21 Nov). A Crew on the server: its
// co-owners, never its Team. A Mac Crew's online copy: its Team's joined members while the Team is on (5 Nov), never a
// co_owners entry; an invite, Team off or removal takes it away at once.
func TestCrewTeamMemberBuildsLikeCoOwnerOnlyWhileJoined(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"ann","username":"ann","email":"ann@example.com","products":["work"]},{"id":"bob","username":"bob","email":"bob@example.com","products":["work"]},{"id":"cara","username":"cara","email":"cara@example.com","products":["work"]}]}`)
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
	ref := crewPathRef{Root: root, OwnerID: "owner", Shared: true}
	// On the server: ann is a co-owner, bob a joined member of a Team left over from before; only ann builds it.
	if !crewLivesOnServer("team-proj") {
		t.Fatal("a Crew made on the server is not treated as one")
	}
	if _, err := registry.AddCoOwner("work", folder, "ann"); err != nil {
		t.Fatal(err)
	}
	save(true, false, "bob")
	if !crewProjectOwnedByCaller("ann", root) || crewRoleOf(root, "ann") != crewRoleCoOwner || crewAccessFor(&UserClaims{UserID: "ann", Username: "ann"}, ref) != crewAccessOwner {
		t.Fatal("a co-owner of a server Crew cannot build it")
	}
	if crewProjectOwnedByCaller("bob", root) || crewProjectOwnedByCallerStrict("ann", root) {
		t.Fatal("a Team member built a server Crew, or a co-owner became its owner")
	}
	// A User (owner, 25 Nov: a role with limited permission; co-owners always change everything): cara works in the
	// project but her turns cannot write its instructions, skills, settings or the extra paths the owner locked; other
	// files stay writable. ann, a co-owner, is never locked.
	if _, err := registry.AddUser("work", folder, "cara"); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserLockedPaths("work", folder, "cara", cleanCrewLockedPaths([]string{"briefs/", "../escape"})); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetUserLockedPaths("work", folder, "ann", nil); err == nil {
		t.Fatal("set locked paths for someone who is not a user")
	}
	blocked := crewSetupLockedWritePaths(root, "cara")
	if !crewProjectOwnedByCaller("cara", root) || crewRoleOf(root, "cara") != crewRoleUser || !slices.Contains(blocked, root+"/PROJECT_INSTRUCTIONS.md") || !slices.Contains(blocked, root+"/skills") || !slices.Contains(blocked, root+"/briefs") {
		t.Fatalf("a user's blocked writes = %v", blocked)
	}
	for _, p := range blocked {
		// "../escape" is cleaned to a path inside the project; nothing may point outside it.
		if p == root+"/notes.md" || strings.Contains(p, "..") || !strings.HasPrefix(p, root+"/") {
			t.Fatalf("a user is blocked from %q", p)
		}
	}
	if crewSetupLocked(root, "owner") || crewSetupLocked(root, "ann") || len(crewSetupLockedWritePaths(root, "ann")) > 0 {
		t.Fatal("the owner's or a co-owner's setup is locked")
	}
	if slices.Contains(crewCoOwnersOnly(root), "cara") || !slices.Contains(crewCoOwnersOnly(root), "ann") {
		t.Fatalf("co-owners list = %v", crewCoOwnersOnly(root))
	}
	// A User's turn gets the project's keys (#4008): cara is not a vault owner, so her turn reads the Crew vault as the
	// main owner, and only rows from that Crew vault, never the owner's own same-named key. A co-owner and a stranger
	// read as themselves.
	crewVaultContentsCache.Store("team-proj", crewVaultContentsCached{at: time.Now()})
	t.Cleanup(func() { forgetCrewVaultContents("team-proj") })
	vaultCtx := withCrewVaultScope(context.Background(), crewVaultID("team-proj"))
	if crewVaultReader(vaultCtx, "cara") != "owner" || crewVaultReader(vaultCtx, "ann") != "ann" || crewVaultReader(vaultCtx, "zed") != "zed" || crewVaultReader(context.Background(), "cara") != "cara" {
		t.Fatal("a User does not read the project vault as its main owner, or someone else does")
	}
	crewKey, err := vaultSecretName(crewVaultID("team-proj"), "FAL_AI_KEY")
	if err != nil {
		t.Fatal(err)
	}
	ownKey, _ := vaultSecretName("v-owner-personal", "FAL_AI_KEY")
	if rows := crewVaultRowsOnly(vaultCtx, []globalSecretEntry{{Name: ownKey}, {Name: crewKey}}); len(rows) != 1 || rows[0].Name != crewKey {
		t.Fatalf("a User's rows from the owner's Vault = %v", rows)
	}
	// The same for apps: only the Crew vault's picked apps, never one of the owner's own.
	if apps := crewVaultAppRowsOnly([]vaultRuntimeServer{{ID: "owner-mail"}, {ID: "crew-drive"}}, []string{vaultServerName("crew-drive"), "none"}); len(apps) != 1 || apps[0].ID != "crew-drive" {
		t.Fatalf("a User's apps from the owner's Vault = %v", apps)
	}
	// A key given in this project's chat goes to its project vault, the only vault it reads (#4144: it went to the
	// person's own Vault and never arrived); a User may not add one there.
	if defaultSecretPlace(root) != secretPlaceProject {
		t.Fatal("an online project's new keys do not go to its project vault")
	}
	if _, _, err := (&StreamingAPI{}).saveProjectSecret(context.Background(), "cara", root, "NEW_KEY", "value", ""); err == nil || !strings.Contains(err.Error(), "owner and co-owners") {
		t.Fatalf("a User added a key to the project vault: %v", err)
	}
	// The same lock outside chat (#3796): a direct MCP setup tool is refused for cara while its read action, a co-owner
	// and the owner stay open, and the website's file proxy refuses her write to the instructions but not a work file.
	if crewSetupLockExternalRefusal("cara", "install_skill", "install", map[string]any{"crew_id": "team-proj"}) == "" {
		t.Fatal("a user installed a skill over MCP")
	}
	if crewSetupLockExternalRefusal("cara", "crew_owners", "list", map[string]any{"crew_id": "team-proj"}) != "" || crewSetupLockExternalRefusal("ann", "install_skill", "install", map[string]any{"crew_id": "team-proj"}) != "" || crewSetupLockExternalRefusal("owner", "install_skill", "install", map[string]any{"crew_id": "team-proj"}) != "" {
		t.Fatal("the lock refused a read, a co-owner or the owner")
	}
	proxyPut := func(user, rel string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/wp/api/documents/"+root+"/"+rel, strings.NewReader(`{"content":"x"}`))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		workspaceProxyHandler().ServeHTTP(w, r)
		return w
	}
	if w := proxyPut("cara", "PROJECT_INSTRUCTIONS.md"); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "controls this project's setup") {
		t.Fatalf("a user's instructions write through the website = %d %q", w.Code, w.Body.String())
	}
	if w := proxyPut("cara", "notes.md"); strings.Contains(w.Body.String(), "controls this project's setup") {
		t.Fatal("a user was refused an ordinary file")
	}
	if w := proxyPut("ann", "PROJECT_INSTRUCTIONS.md"); strings.Contains(w.Body.String(), "controls this project's setup") {
		t.Fatal("a co-owner was refused the instructions")
	}
	// Making cara a co-owner lifts the lock; removing ann as co-owner ends her access.
	if _, err := registry.AddCoOwner("work", folder, "cara"); err != nil {
		t.Fatal(err)
	}
	if crewSetupLocked(root, "cara") {
		t.Fatal("a user made co-owner is still locked")
	}
	if _, err := registry.RemoveCoOwner("work", folder, "ann"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RemoveCoOwner("work", folder, "cara"); err != nil {
		t.Fatal(err)
	}
	if crewProjectOwnedByCaller("ann", root) {
		t.Fatal("a removed co-owner still builds the Crew")
	}
	// The online copy of a Mac Crew keeps its Team.
	if err := crewroot.WriteSync("team-proj", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "owner-pair", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatal(err)
	}
	if crewLivesOnServer("team-proj") {
		t.Fatal("a Mac Crew's online copy is treated as a server Crew")
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
	if _, err := registry.AddCoOwner("work", folder, "bob"); err != nil {
		t.Fatal(err)
	}
	if crewProjectOwnedByCaller("bob", root) || crewRoleOf(root, "bob") == crewRoleCoOwner {
		t.Fatal("a co_owners entry gave builder access to a Mac Crew's online copy")
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "team-proj", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "ann"}}, Folder: crewTeamFolder{ID: "f1", Folders: []string{"team"}}}); err != nil {
		t.Fatal(err)
	}
	// A Mac Crew: a member on the web works in their own copy in the Crew's folder, team-copies/<member>/.
	if got, want := crewTeamMemberFilesPath("ann", root), root+"/team-copies/ann"; got != want {
		t.Fatalf("member Files root on a Mac Crew = %q, want %q", got, want)
	}
	// team-copies/ is never shared or synced, even if explicitly selected, and Files lists only a member's own copy.
	selected := crewTeamFolder{ID: "f2", Folders: []string{"team", "team-copies"}}
	if selected.holds("team-copies/ann/notes.md") || selected.walks("team-copies") || crewTeamAllowed("team-copies/ann/notes.md") || !selected.holds("team/notes.md") {
		t.Fatal("team-copies/ is part of a Team share")
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

// testMacPair is the owner's Mac for a Crew marked with macCrewOnlineCopy; adminRequest sends it.
const testMacPair = "test-owner-mac"

// macCrewOnlineCopy marks project as the server's copy of a Mac Crew: only such a Crew has a Team (owner, 21 Nov; a Crew
// made on the server has co-owners instead). The owner's Team changes then come from their Mac (testMacPair).
func macCrewOnlineCopy(t *testing.T, project string) {
	t.Helper()
	if err := crewroot.WriteSync(project, crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: testMacPair, PeerProjectID: "mac-" + project}); err != nil {
		t.Fatal(err)
	}
}

// One-off migration (server Crews have co-owners and no Team): a server Crew's joined members become co-owners and its
// Team record goes into the backup; invited people gain nothing and a Mac Crew's online copy keeps its Team.
func TestServerCrewTeamsMigrationMovesMembersToCoOwners(t *testing.T) {
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"ann","username":"ann","products":["work"]},{"id":"bob","username":"bob","products":["work"]}]}`)
	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })
	for _, p := range []string{"srv", "mac"} {
		if err := registry.Register(projectOwnerRecord{Product: "work", Folder: p + "-12345678", OwnerID: "owner", Shared: true, ProjectID: p}); err != nil {
			t.Fatal(err)
		}
		if err := writeCrewTeam(crewTeam{ProjectID: p, OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "ann"}, {UserID: "bob", Invited: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	macCrewOnlineCopy(t, "mac")
	api := &StreamingAPI{}
	backup := filepath.Join(state, "backup")
	rows, err := api.migrateServerCrewTeams(context.Background(), false, backup)
	if err != nil || len(rows) != 1 || rows[0].Project != "srv" || crewProjectOwnedByCaller("ann", "Crew/srv-12345678") {
		t.Fatalf("dry run: rows=%+v err=%v, or it changed access", rows, err)
	}
	if rows, err = api.migrateServerCrewTeams(context.Background(), true, backup); err != nil || len(rows) != 1 || rows[0].Result != "migrated" {
		t.Fatalf("apply: rows=%+v err=%v", rows, err)
	}
	if !crewProjectOwnedByCaller("ann", "Crew/srv-12345678") || crewProjectOwnedByCaller("bob", "Crew/srv-12345678") {
		t.Fatal("a joined member did not become a co-owner, or an invited person did")
	}
	if _, err := readCrewTeam("srv"); !os.IsNotExist(err) {
		t.Fatalf("the server Crew's Team record is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backup, "team-folders", "srv", "team.json")); err != nil {
		t.Fatalf("the Team record was not backed up: %v", err)
	}
	if team, err := readCrewTeam("mac"); err != nil || len(team.People) != 2 {
		t.Fatalf("a Mac Crew's Team was changed: %+v %v", team, err)
	}
}
