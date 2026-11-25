package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// #3664: only the Team's owner adds an online project, and only one they own or co-own that lives on the server; the
// Team's owner or the project's owners take it out. Only the in-process copy of a member project is admitted to the
// Team folder, and removing the project deletes only the folders it holds for the Team.
func TestCrewTeamOnlineProjectAccess(t *testing.T) {
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"ann","username":"ann","products":["work"]},{"id":"bob","username":"bob","products":["work"]}]}`)
	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })
	// Owner, 25 Nov: only a project whose AI teammate switch is on can be added ("off" has it off).
	for _, p := range []struct {
		id, owner string
		ai        bool
	}{{"mac", "owner", false}, {"srv", "bob", true}, {"own", "owner", true}, {"other", "bob", true}, {"off", "owner", false}} {
		if err := registry.Register(projectOwnerRecord{Product: "work", Folder: p.id + "-12345678", OwnerID: p.owner, Shared: true, ProjectID: p.id, AITeammate: p.ai}); err != nil {
			t.Fatal(err)
		}
	}
	macCrewOnlineCopy(t, "mac")
	joinCrewTeam(t, "srv-12345678", "owner") // the Team's owner co-owns srv
	if status, _, _, _ := setCrewProjectAITeammate("srv", "ann", false); status != http.StatusForbidden {
		t.Fatalf("someone who neither owns nor co-owns the project changed its AI teammate switch: %d", status)
	}
	team := crewTeam{ProjectID: "mac", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Folders: []string{"team"}}}
	root := func(p string) string { return crewProjectRootByID(p) }

	for _, c := range []struct {
		name, user, member, action string
		on, allowed                bool
	}{
		{"owner adds a co-owned server project", "owner", "srv", "add", true, true},
		{"owner adds an owned server project", "owner", "own", "add", true, true},
		{"a teammate cannot add", "ann", "srv", "add", true, false},
		{"owner cannot add someone else's project", "owner", "other", "add", true, false},
		{"owner cannot add a Mac project's online copy", "owner", "mac", "add", true, false},
		{"not while the Team is off", "owner", "srv", "add", false, false},
		{"not while the project's AI teammate switch is off", "owner", "off", "add", true, false},
		{"the project's owner takes it out", "bob", "srv", "remove", true, true},
		{"a stranger cannot take it out", "ann", "srv", "remove", true, false},
	} {
		tm := team
		tm.On = c.on
		if got := crewTeamProjectChangeRefusal(tm, c.user, c.member, root(c.member), c.action) == ""; got != c.allowed {
			t.Errorf("%s: allowed=%v, want %v", c.name, got, c.allowed)
		}
	}

	// Admission to the Team folder: the in-process copy of a member project only.
	team.Projects = []crewTeamProject{{ProjectID: "srv"}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: crewTeamProjectMemberID("srv")}
	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	marked := plain.WithContext(context.WithValue(plain.Context(), crewTeamProjectMemberKey{}, "srv"))
	if _, ok := crewTeamProjectAdmitted(marked, claims, "mac"); !ok {
		t.Fatal("a member project's copy was refused")
	}
	if _, ok := crewTeamProjectAdmitted(plain, claims, "mac"); ok {
		t.Fatal("a request without the in-process mark was admitted as a project")
	}
	if _, ok := crewTeamProjectAdmitted(marked.WithContext(context.WithValue(plain.Context(), crewTeamProjectMemberKey{}, "own")), &UserClaims{UserID: crewTeamProjectMemberID("own")}, "mac"); ok {
		t.Fatal("a project that is not in the Team was admitted")
	}

	// The online project's own page names its Team; only its owners or the Team's owner get Remove from Team.
	if in := crewProjectInTeams("srv", "bob"); len(in) != 1 || in[0].TeamID != "mac" || !in[0].CanRemove {
		t.Fatalf("project owner sees Teams %+v", in)
	}
	if in := crewProjectInTeams("srv", "ann"); len(in) != 1 || in[0].CanRemove {
		t.Fatalf("a stranger may not remove: %+v", in)
	}

	// Team keys: work lent by a member project's owner gets the Team's keys, the project's own key wins on a name
	// clash, and someone who owns no project in the Team gets none.
	if err := writeTeamWay("mac", teamWayRecord{Vault: "v-team1234", AddedBy: map[string]string{"secret:API_KEY": "owner", "secret:DB_URL": "owner"}}); err != nil {
		t.Fatal(err)
	}
	teamKey := func(name string) string { full, _ := vaultSecretName("v-team1234", name); return full }
	if keys := crewTeamProjectLentKeys(context.Background(), "bob"); keys[teamKey("API_KEY")] != "mac" || len(keys) != 2 {
		t.Fatalf("member project's owner got Team keys %v", keys)
	}
	if keys := crewTeamProjectLentKeys(context.Background(), "ann"); len(keys) != 0 {
		t.Fatalf("someone with no project in the Team got Team keys %v", keys)
	}
	ownKey, _ := vaultSecretName("v-srv12345", "API_KEY")
	if got := withCrewTeamProjectKeys("srv", []string{ownKey}); !slices.Equal(got, []string{ownKey, teamKey("DB_URL")}) {
		t.Fatalf("own key should win and the Team's other key be added: %v", got)
	}

	// Taking it out removes the Team's files that are still as last synced, and nothing else: an edit since the last
	// sync stays, a folder made later at the same name stays, and a link on the way is never followed.
	files, outside := t.TempDir(), t.TempDir()
	write := func(root, rel string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"team/notes.md", "team/edited.md", "TEAM_INSTRUCTIONS.md", "reports/own.md", "replaced/a.md"} {
		write(files, rel)
	}
	write(outside, "deep/secret.md")
	if err := os.Symlink(outside, filepath.Join(files, "linked")); err != nil {
		t.Fatal(err)
	}
	synced := map[string]crewTeamLocalFile{}
	for _, rel := range []string{"team/notes.md", "team/edited.md", "TEAM_INSTRUCTIONS.md", "replaced/a.md"} {
		info, err := os.Lstat(filepath.Join(files, rel))
		if err != nil {
			t.Fatal(err)
		}
		synced[rel] = crewTeamLocalFile{Stamp: crewsync.StampOf(info), ReadAt: time.Now().Add(2 * time.Second)} // read after the last change
	}
	if err := os.WriteFile(filepath.Join(files, "team/edited.md"), []byte("edited after the last sync"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := crewTeamProjectCopyDir("srv", "mac", "f1")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	copyRecord := crewTeamLocal{FolderID: "f1", Project: "mac", LocalProject: "srv", RemoteUser: crewTeamProjectMemberID("srv"), Root: files, Mounted: true, ProjectMember: true, Files: synced,
		Mounts: map[string]crewTeamMount{"p1": {Remote: "team", Local: "team"}, "p2": {Remote: "replaced", Local: "replaced", Inode: 1, Device: 1}, "p3": {Remote: "linked/deep", Local: "linked/deep"}}}
	if err = saveCrewTeamLocal(dir, copyRecord); err != nil {
		t.Fatal(err)
	}
	(&StreamingAPI{}).removeStaleCrewTeamProjectCopies(context.Background())
	if _, err := os.Stat(filepath.Join(files, "team/notes.md")); err != nil {
		t.Fatal("a member project's copy was removed while it is still in the Team")
	}
	team.Projects = nil
	if err = writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	(&StreamingAPI{}).removeStaleCrewTeamProjectCopies(context.Background())
	for path, want := range map[string]bool{filepath.Join(files, "team/notes.md"): false, filepath.Join(files, "TEAM_INSTRUCTIONS.md"): false, filepath.Join(files, "team/edited.md"): true,
		filepath.Join(files, "reports/own.md"): true, filepath.Join(files, "replaced/a.md"): true, filepath.Join(outside, "deep/secret.md"): true} {
		if _, err := os.Stat(path); (err == nil) != want {
			t.Errorf("%s present=%v after removal, want %v", path, err == nil, want)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the copy's record was kept: %v", err)
	}
}

// #3664: an online project's agents are Team agent rows owned by the project's own identity. A joined teammate reaches
// them, and they run as the project's main owner in that project; someone outside the Team, a project that left, or a
// Team that is off reaches nothing. The rows never belong to the main owner's account.
func TestCrewTeamOnlineProjectAgentReach(t *testing.T) {
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"ann","username":"ann","products":["work"]},{"id":"bob","username":"bob","products":["work"]},{"id":"carl","username":"carl","products":["work"]}]}`)
	registry := newTestRegistry(t)
	prev := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	t.Cleanup(func() { defaultProjectOwnersOverride = prev })
	for _, p := range []struct{ id, owner string }{{"mac", "owner"}, {"srv", "bob"}, {"other", "bob"}} {
		if err := registry.Register(projectOwnerRecord{Product: "work", Folder: p.id + "-12345678", OwnerID: p.owner, Shared: true, ProjectID: p.id}); err != nil {
			t.Fatal(err)
		}
	}
	macCrewOnlineCopy(t, "mac")
	id := crewTeamAgentIDPrefix + "00000000-0000-4000-8000-000000000001"
	member := crewTeamProjectMemberID("srv")
	row := crewTeamRegisteredAgent{ID: id, OwnerID: member, Tab: "t1", ChatKey: "srv" + codeChatSideMarker + "t1", Name: "Builder", Place: "server", InTeam: true, Rule: crewTeamDefaultRule()}
	team := crewTeam{ProjectID: "mac", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "ann"}}, Projects: []crewTeamProject{{ProjectID: "srv"}}, Agents: map[string]crewTeamRegisteredAgent{id: row}}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, viewer string
		change       func(*crewTeam)
		want         bool
	}{
		{"a joined teammate reaches the project's agent", "ann", nil, true},
		{"the Team's owner reaches it", "owner", nil, true},
		{"someone outside the Team does not", "carl", nil, false},
		{"the project's main owner is not in the Team by owning it", "bob", nil, false},
		{"not after the project left the Team", "ann", func(t *crewTeam) { t.Projects = nil }, false},
		{"not while the Team is off", "ann", func(t *crewTeam) { t.On = false }, false},
	} {
		tm := team
		tm.Projects = slices.Clone(team.Projects)
		if c.change != nil {
			c.change(&tm)
		}
		if got := crewTeamReachRow(tm, c.viewer, row); got != c.want {
			t.Errorf("%s: reach=%v, want %v", c.name, got, c.want)
		}
	}
	if got := crewTeamRowRunsAs(member); got != "bob" {
		t.Fatalf("the project's agent runs as %q, want its main owner", got)
	}
	if found, ok := crewTeamOfAgent("srv", id); !ok || found.ProjectID != "mac" {
		t.Fatal("the online project's chat did not find the Team that registered its agent")
	}
	if _, ok := crewTeamOfAgent("other", id); ok {
		t.Fatal("another project's chat claimed the online project's agent")
	}
	if address := crewTeamAgentAddress("srv", id); address != "agent:bob/"+id {
		t.Fatalf("address %q is not under the account the chat runs as", address)
	}
	// Owner, 24 Nov: an online project's agent is an AI teammate. A member's own ask works with the project's full
	// access unless the project set that agent to Answers only; outsiders and Run turns stay Run.
	dest := agentMessageEndpoint{ID: "srv", TeamAgentID: id}
	for _, c := range []struct {
		name   string
		source agentMessageEndpoint
		want   bool
	}{
		{"a teammate's own ask makes changes", agentMessageEndpoint{UserID: "ann"}, true},
		{"the Team owner's ask makes changes", agentMessageEndpoint{UserID: "owner"}, true},
		{"someone outside the Team does not", agentMessageEndpoint{UserID: "carl"}, false},
		{"a Run turn in a teammate's agent does not", agentMessageEndpoint{UserID: "ann", Mode: "run"}, false},
		{"a guest using a teammate's agent does not", agentMessageEndpoint{UserID: "ann", Guest: "carl"}, false},
	} {
		if got := crewTeamProjectAskChanges(dest, c.source); got != c.want {
			t.Errorf("%s: full access=%v, want %v", c.name, got, c.want)
		}
	}
	// The run decision deliverPublishedAgentMessage makes (review of #3744): the receiving chat runs as the project's
	// main owner, and a teammate's ask is another person's turn there.
	role := agentMessageEndpoint{UserID: "bob", ID: "srv", TeamAgentID: id}
	if publishedAgentRunMode(role, role, agentMessageEndpoint{UserID: "ann"}) {
		t.Fatal("a teammate's ask to the project's agent was delivered with Run rights, want full access")
	}
	team.Projects = []crewTeamProject{{ProjectID: "srv", AnswersOnly: []string{row.ChatKey}}}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if crewTeamProjectAskChanges(dest, agentMessageEndpoint{UserID: "ann"}) {
		t.Fatal("an agent set to Answers only still made changes")
	}
	if !publishedAgentRunMode(role, role, agentMessageEndpoint{UserID: "ann"}) {
		t.Fatal("an agent set to Answers only was delivered without Run rights")
	}
	// The other direction (Team Module Manager rule 2): only a Builder turn of the joined project asks the Team as the
	// project; a Run turn, a guest, another project or a project that left asks as its person and is refused.
	annID := crewTeamAgentIDPrefix + "00000000-0000-4000-8000-000000000002"
	annRow := crewTeamRegisteredAgent{ID: annID, OwnerID: "ann", Tab: "a1", ChatKey: "mac" + codeChatSideMarker + "a1", Name: "QA", Place: "mac", InTeam: true, Rule: crewTeamDefaultRule()}
	team.Agents[annID] = annRow
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	builder := agentMessageEndpoint{UserID: "bob", Kind: triggerCallerCrew, ID: "srv", Mode: "builder"}
	for _, c := range []struct {
		name   string
		source agentMessageEndpoint
		want   bool
	}{
		{"the joined project's Builder turn asks as the project", builder, true},
		{"a Run turn in the project does not", agentMessageEndpoint{UserID: "bob", Kind: triggerCallerCrew, ID: "srv", Mode: "run"}, false},
		{"another person's turn in the project does not", agentMessageEndpoint{UserID: "bob", Kind: triggerCallerCrew, ID: "srv", Mode: "run", Guest: "carl"}, false},
		{"a project not in the Team does not", agentMessageEndpoint{UserID: "bob", Kind: triggerCallerCrew, ID: "other", Mode: "builder"}, false},
	} {
		asker := crewTeamAskerOf(c.source)
		if got := asker == member && crewTeamReachRow(team, asker, annRow); got != c.want {
			t.Errorf("%s: asker=%q reach=%v, want %v", c.name, asker, got, c.want)
		}
	}
	team.Projects = nil
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if asker := crewTeamAskerOf(builder); asker == member || crewTeamReachRow(team, member, annRow) {
		t.Fatal("a project that left the Team still asked it as the project")
	}
}

// #3664 regression: an online project's copy stopped its whole sync on the first round with "the Team has not supplied a
// folder identity for team-setup.json". The Team's setup and skills are skipped there, never an error.
func TestCrewTeamProjectCopySkipsTheTeamsSetup(t *testing.T) {
	b := &crewTeamLocal{ProjectMember: true, Files: map[string]crewTeamLocalFile{}}
	files := []crewTeamRemoteFile{{Path: crewTeamSetupFile, Version: &chunkedFile{}}, {Path: "skills/review/SKILL.md", Version: &chunkedFile{}}}
	if _, err := crewTeamDownload(context.Background(), "owner", desktopConnection{}, "/api/crew-teams/team", "", t.TempDir(), b, files); err != nil {
		t.Fatalf("the Team's setup and skills should be skipped in a project's copy: %v", err)
	}
}
