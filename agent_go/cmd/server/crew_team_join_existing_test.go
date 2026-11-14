package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-976: real HTTP/chunk/disk sync must never upload a private sibling
// merely because it shares the Team folder or skill's name.
func TestTeamExistingProjectSyncKeepsPrivateFoldersAndSkills(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	crewTeamTestDocs(t, "existing-join", "existing-join")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"member","username":"member","can_create":true,"products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: crewProfileID, Folder: "existing-join", OwnerID: "owner", ProjectID: "existing-join"}); err != nil {
		t.Fatal(err)
	}
	team := crewTeam{ProjectID: "existing-join", OwnerID: "owner", Name: "Example", On: true, People: []crewTeamPerson{{UserID: "member"}}, Folder: crewTeamFolder{ID: "folder-store", Folders: []string{"docs"}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	router := mux.NewRouter()
	api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		router.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user})))
	}))
	defer server.Close()
	write := func(root, rel, value string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(root, rel, want string) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || string(raw) != want {
			t.Fatalf("%s: %q %v, want %q", rel, raw, err, want)
		}
	}
	newCopy := func(user string, mounted bool) (desktopConnection, string, *crewTeamLocal) {
		root := t.TempDir()
		return desktopConnection{ID: user, Server: server.URL, Token: user, User: UserInfo{ID: user}}, t.TempDir(), &crewTeamLocal{FolderID: "folder-store", Project: "existing-join", Connection: user, RemoteUser: user, Client: user + "-copy", Root: root, Mounted: mounted}
	}
	c1, d1, b1 := newCopy("owner", false)
	c2, d2, b2 := newCopy("member", true)
	b2.ProtectPrivate = true
	b1.SkillRoot = filepath.Join(t.TempDir(), "skills")
	b2.SkillRoot = filepath.Join(t.TempDir(), "skills")
	round := func(c desktopConnection, dir string, b *crewTeamLocal) {
		t.Helper()
		if err := syncCrewTeamLocal(t.Context(), c.User.ID, c, dir, b); err != nil {
			t.Fatal(err)
		}
	}
	round(c1, d1, b1)
	write(b1.Root, "docs/note.txt", "Team note")
	write(filepath.Dir(b1.SkillRoot), "skills/report/SKILL.md", "Team skill")
	write(b1.Root, crewTeamSetupFile, `{"selected_skills":["report"]}`)
	round(c1, d1, b1)
	write(b2.Root, "docs/private.txt", "private document")
	write(filepath.Dir(b2.SkillRoot), "skills/report/SKILL.md", "private skill")
	write(b2.Root, "MEMORY.md", "private memory")
	write(b2.Root, "PROJECT_INSTRUCTIONS.md", "private guidance")
	if err := backupCrewTeamJoinSetup(b2.Root, "my private chat rules"); err != nil {
		t.Fatal(err)
	}
	round(c2, d2, b2)
	read(b2.Root, "docs (team)/note.txt", "Team note")
	read(b2.Root, "docs/private.txt", "private document")
	read(filepath.Dir(b2.SkillRoot), "skills/report (team)/SKILL.md", "Team skill")
	read(filepath.Dir(b2.SkillRoot), "skills/report/SKILL.md", "private skill")
	read(b2.Root, "PROJECT_INSTRUCTIONS (before team).md", "private guidance")
	write(b2.Root, "docs (team)/note.txt", "member edit")
	write(b2.Root, "docs/private.txt", "private changed")
	round(c2, d2, b2)
	round(c1, d1, b1)
	read(b1.Root, "docs/note.txt", "member edit")
	state, err := readCrewTeamCurrentState("existing-join")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"docs/private.txt", "MEMORY.md", "PROJECT_INSTRUCTIONS (before team).md"} {
		if state.Versions[rel].File != nil {
			t.Fatalf("private file uploaded: %s", rel)
		}
	}
	// The physical directory's identity restores a member-local rename without
	// treating it as deletion of everyone else's Team files.
	if err := os.Rename(filepath.Join(b2.Root, "docs (team)"), filepath.Join(b2.Root, "renamed locally")); err != nil {
		t.Fatal(err)
	}
	round(c2, d2, b2)
	read(b2.Root, "docs (team)/note.txt", "member edit")
	// Clearing a private collision removes the suffix; the mount retains its ID.
	if err := os.Rename(filepath.Join(b2.Root, "docs"), filepath.Join(b2.Root, "my docs")); err != nil {
		t.Fatal(err)
	}
	round(c2, d2, b2)
	read(b2.Root, "docs/note.txt", "member edit")
	read(b2.Root, "my docs/private.txt", "private changed")
	before := mustReadCrewTeam(t, "existing-join")
	ids := map[string]string{}
	for _, p := range before.Folder.Paths {
		ids[p.Path] = p.ID
	}
	if err := os.Rename(filepath.Join(b1.Root, "docs"), filepath.Join(b1.Root, "reports")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(filepath.Dir(b1.SkillRoot), "skills", "report"), filepath.Join(filepath.Dir(b1.SkillRoot), "skills", "analysis")); err != nil {
		t.Fatal(err)
	}
	round(c1, d1, b1)
	round(c2, d2, b2)
	read(b2.Root, "reports/note.txt", "member edit")
	read(filepath.Dir(b2.SkillRoot), "skills/analysis/SKILL.md", "Team skill")
	var selected crewTeamSetup
	raw, err := os.ReadFile(filepath.Join(b2.Root, crewTeamSetupFile))
	if err != nil || json.Unmarshal(raw, &selected) != nil || len(selected.SelectedSkills) != 1 || selected.SelectedSkills[0] != "analysis" {
		t.Fatalf("renamed skill selection did not follow its identity: %s %v", raw, err)
	}

	read(filepath.Dir(b2.SkillRoot), "skills/report/SKILL.md", "private skill")
	renamed := mustReadCrewTeam(t, "existing-join")
	for _, p := range renamed.Folder.Paths {
		if p.Path == "reports" && p.ID != ids["docs"] {
			t.Fatal("folder rename replaced its identity")
		}
		if p.Path == "skills/analysis" && p.ID != ids["skills/report"] {
			t.Fatal("skill rename replaced its identity")
		}
	}

	// Even a fresh New-project member copy must not publish a separately
	// installed local skill merely because Team skills use the same resource root.
	c3, d3, b3 := newCopy("member", true)
	b3.Client = "fresh-member-copy"
	b3.SkillRoot = filepath.Join(t.TempDir(), "skills")
	write(filepath.Dir(b3.SkillRoot), "skills/local-only/SKILL.md", "private installed skill")
	round(c3, d3, b3)
	round(c3, d3, b3)
	latest, err := readCrewTeamCurrentState("existing-join")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Versions["skills/local-only/SKILL.md"].File != nil {
		t.Fatal("New-project member published an unchosen installed skill")
	}
	selectedLocal := b2.importSelectedSkills([]string{"analysis"}, []string{"report", "company-skill"})
	if !slices.Contains(selectedLocal, "company-skill") {
		t.Fatal("uninstalled company skill selection was dropped")
	}
	if slices.Contains(b2.exportSelectedSkills(selectedLocal, []string{"analysis"}), "company-skill") {
		t.Fatal("private company skill selection was uploaded")
	}
	if got := b2.exportSelectedSkills([]string{"report", "analysis"}); len(got) != 1 || got[0] != "analysis" {
		t.Fatalf("private skill selection leaked into Team setup: %v", got)
	}

}

// PLAT-746-976: Team-side rename changes a name, never which local private
// folder or acknowledged file versions the shared folder belongs to.
func TestTeamMountRenameRetainsIdentityAndRejectsLinks(t *testing.T) {
	root := t.TempDir()
	b := crewTeamLocal{Root: root, Mounted: true, Files: map[string]crewTeamLocalFile{}}
	share := crewTeamFolder{Folders: []string{"docs"}, Paths: []crewTeamSharedPath{{ID: "stable-id", Path: "docs"}}}
	if err := b.reconcileMounts(share); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "note.txt"), []byte("shared"), 0600); err != nil {
		t.Fatal(err)
	}
	b.Files["docs/note.txt"] = crewTeamLocalFile{}
	share.Folders = []string{"reports"}
	share.Paths[0].Path = "reports"
	if err := b.reconcileMounts(share); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "reports", "note.txt")); string(got) != "shared" {
		t.Fatal("rename lost file")
	}
	if _, ok := b.Files["reports/note.txt"]; !ok {
		t.Fatal("rename lost acknowledged baseline")
	}
	if b.Mounts["stable-id"].Local != "reports" {
		t.Fatal("mount identity changed")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	share = crewTeamFolder{Folders: []string{"linked/docs"}, Paths: []crewTeamSharedPath{{ID: "other-id", Path: "linked/docs"}}}
	if err := b.reconcileMounts(share); err == nil {
		t.Fatal("mount followed a private link outside project")
	}
	var old crewTeamFolder
	if err := json.Unmarshal([]byte(`{"folders":["docs"]}`), &old); err == nil {
		t.Fatal("legacy folder names accepted without migration")
	}
}

func teamFolderTestEntries(list string) string {
	var names []string
	if err := json.Unmarshal([]byte("["+list+"]"), &names); err != nil {
		panic(err)
	}
	entries := []crewTeamSharedPath{}
	for _, name := range names {
		entries = append(entries, crewTeamSharedPath{Path: name})
	}
	raw, _ := json.Marshal(entries)
	return string(raw)
}

// PLAT-746-976: the real accept handler must reject foreign/Team-on/online
// targets before accepting the invite, and join with zero agents shared.
func TestTeamExistingProjectAcceptRechecksAdmissionAndInitialPrivacy(t *testing.T) {
	desktopTestEnvironment(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	f, _ := newExternalCrewCommandFixture(t)
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(t.Context(), UserContextKey, claims)
	roots, err := f.api.crewSyncRoots(ctx, claims, "team")
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","title":"Test Team","session_id":"sess-team","chat_instructions":"my private chat settings"}`)
	if err = os.WriteFile(filepath.Join(roots.Files, "PROJECT_INSTRUCTIONS.md"), []byte("my original rules"), 0600); err != nil {
		t.Fatal(err)
	}
	main := crewOwnerDefaultChatKey("team", "owner")
	own := productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{
		productConversationRegistryEntryKey(crewProfileID, main):                                 {ProfileID: crewProfileID, ConversationKey: main, SessionID: "own-main", WorkspacePath: externalCommandCrewRoot},
		productConversationRegistryEntryKey(crewProfileID, "team"+codeChatSideMarker+"research"): {ProfileID: crewProfileID, ConversationKey: "team" + codeChatSideMarker + "research", SessionID: "own-research", WorkspacePath: externalCommandCrewRoot},
	}}
	if err = defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), own); err != nil {
		t.Fatal(err)
	}
	if err = defaultProjectOwners().Register(projectOwnerRecord{Product: crewProfileID, Folder: "invited", OwnerID: "other", ProjectID: "invited"}); err != nil {
		t.Fatal(err)
	}
	remoteTeam := crewTeam{ProjectID: "invited", OwnerID: "other", On: true, Name: "Team example", People: []crewTeamPerson{{UserID: "owner", Invited: true}}, Folder: crewTeamFolder{ID: "invited-files", Folders: []string{"docs"}}}
	if err = writeCrewTeam(remoteTeam); err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	f.api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	accepts := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/invite") {
			accepts++
		}
		router.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, claims)))
	}))
	defer remote.Close()
	if err = writeDesktopConnections("owner", []desktopConnection{{ID: "saved", Server: remote.URL, Token: "remote-test-session", User: UserInfo{ID: "owner"}}}); err != nil {
		t.Fatal(err)
	}
	request := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := desktopTestRequest(http.MethodPost, "/api/desktop-connections/saved/crew-teams/invited/join", body, "owner")
		r = mux.SetURLVars(r, map[string]string{"connection": "saved", "project": "invited"})
		f.api.handleDesktopCrewTeamJoin(w, r)
		return w
	}
	if w := request(`{"local_project":"invited","confirm":true}`); w.Code < 400 || accepts != 0 {
		t.Fatalf("foreign target accepted: %d %s", w.Code, w.Body.String())
	}
	if err = writeCrewTeamState("team", func(s *crewTeamLocalState) { s.TeamOn = true }); err != nil {
		t.Fatal(err)
	}
	if w := request(`{"local_project":"team","confirm":true}`); w.Code < 400 || accepts != 0 {
		t.Fatalf("Team-on target accepted: %d %s", w.Code, w.Body.String())
	}
	if err = writeCrewTeamState("team", func(s *crewTeamLocalState) { s.TeamOn = false }); err != nil {
		t.Fatal(err)
	}
	if err = crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", Side: "local", PairID: "independent", Editing: "local", ConnectionID: "saved", PeerProjectID: "independent-online"}); err != nil {
		t.Fatal(err)
	}
	if w := request(`{"local_project":"team","confirm":true}`); w.Code != 409 || !strings.Contains(w.Body.String(), "Already online") || accepts != 0 {
		t.Fatalf("independent online link replaced: %d %s", w.Code, w.Body.String())
	}
	stateRoot, err := crewroot.StateRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(stateRoot, "crew-sync", "team.json")); err != nil {
		t.Fatal(err)
	}
	if w := request(`{"local_project":"team"}`); w.Code != 409 || accepts != 0 {
		t.Fatalf("instructions notice skipped: %d %s", w.Code, w.Body.String())
	}
	reg := &singleToolCaptureRegistrar{}
	if err = f.api.registerCrewTeamChatTool(reg, newProductToolGate(nil), claims, false, "own-main", externalCommandCrewRoot, "", ""); err != nil {
		t.Fatal(err)
	}
	preview, err := reg.exec(ctx, map[string]any{"action": "accept", "project": "invited"})
	if err != nil || !strings.Contains(preview, "instructions_notice") || !strings.Contains(preview, `"project_id":"team"`) || accepts != 0 {
		t.Fatalf("chat preview failed: %s %v", preview, err)
	}
	result, err := reg.exec(ctx, map[string]any{"action": "accept", "project": "invited", "accept_into": "team", "confirm": true, "agents": []any{}})
	if err != nil || !strings.Contains(result, `"project_id":"team"`) || accepts != 1 {
		t.Fatalf("chat join failed: %s %v accepts=%d", result, err, accepts)
	}
	saved := mustReadCrewTeam(t, "invited")
	count := 0
	for _, agent := range saved.Agents {
		if agent.OwnerID == "owner" {
			count++
			if agent.InTeam {
				t.Fatal("unselected existing agent became shared")
			}
		}
	}

	if saved.agentInTeam("owner", "never-chatted") {
		t.Fatal("undiscovered existing-project agent defaulted to shared")
	}
	if err := assignCrewTeamAgentIDs(saved, []crewTeamAgentsPerson{{UserID: "owner", Agents: []crewTeamAgent{{Tab: "late-tab", ChatKey: "invited:chat:late-tab", Name: "Later"}}}}); err != nil {
		t.Fatal(err)
	}
	later := mustReadCrewTeam(t, "invited")
	late, ok := later.registeredAgent("owner", "invited:chat:late-tab")
	if !ok || late.InTeam {
		t.Fatal("later discovered agent defaulted to shared")
	}
	if err := setCrewTeamAgentChoice(ctx, "invited", "owner", late.ID, true); err != nil {
		t.Fatal("explicit sharing refused:", err)
	}

	joinTime, err := time.Parse(time.RFC3339Nano, readCrewTeamState("team").JoinedAt)
	if err != nil {
		t.Fatal("join time missing:", err)
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath("owner"))
	if err != nil {
		t.Fatal(err)
	}
	postKey := "team" + codeChatSideMarker + "post-join"
	doc.Entries[productConversationRegistryEntryKey(crewProfileID, postKey)] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: postKey, ConversationID: "post-conversation", SessionID: "post-session", WorkspacePath: externalCommandCrewRoot, CreatedAt: joinTime.Add(time.Second).Format(time.RFC3339Nano)}
	if err = defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), doc); err != nil {
		t.Fatal(err)
	}
	tabs, _, err := f.api.macCrewAgents(ctx, claims, "team")
	if err != nil {
		t.Fatal(err)
	}
	var post *bool
	for _, tab := range tabs {
		if tab.ConversationKey == postKey {
			post = tab.TeamNewInTeam
		}
	}
	if post == nil || !*post {
		t.Fatal("post-join agent lost the normal Team default")
	}
	if err = assignCrewTeamAgentIDs(mustReadCrewTeam(t, "invited"), []crewTeamAgentsPerson{{UserID: "owner", Agents: []crewTeamAgent{{Tab: "post-join", ChatKey: "invited:chat:post-join", Name: "Post join", initialInTeam: post}}}}); err != nil {
		t.Fatal(err)
	}
	if !mustReadCrewTeam(t, "invited").agentInTeam("owner", "invited:chat:post-join") {
		t.Fatal("post-join agent was not registered with normal sharing")
	}
	if !mustReadCrewTeam(t, "invited").agentInTeam("owner", late.ID) {
		t.Fatal("explicit sharing was not saved")
	}
	if raw, err := os.ReadFile(filepath.Join(roots.Files, "CHAT_INSTRUCTIONS (before team).md")); err != nil || string(raw) != "my private chat settings" {
		t.Fatalf("chat settings backup missing: %s %v", raw, err)
	}
	if count != 2 {
		t.Fatalf("expected two private existing agents, got %d", count)
	}
	if raw, err := os.ReadFile(filepath.Join(roots.Files, "PROJECT_INSTRUCTIONS (before team).md")); err != nil || string(raw) != "my original rules" {
		t.Fatal("instructions backup missing")
	}
	if b, err := crewroot.ReadSync("team"); err != nil || b == nil || b.PeerProjectID != "invited" {
		t.Fatal("existing project was not paired")
	}
	if state := readCrewTeamState("team"); state.MemberOf != "invited" {
		t.Fatal("existing project identity was replaced")
	}
	after, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath("owner"))
	if err != nil {
		t.Fatal(err)
	}
	for key, entry := range own.Entries {
		if after.Entries[key].SessionID != entry.SessionID {
			t.Fatal("existing chat identity changed")
		}
	}
}

// PLAT-746-976: unknown/pre-join dates are private, including a later-created
// registry row whose first history proves the physical agent existed earlier.
func TestTeamJoinAgentDefaultUsesFirstKnownCreationTime(t *testing.T) {
	join := "2026-11-14T06:00:00Z"
	for _, tc := range []struct {
		created, history string
		want             bool
	}{
		{"2026-11-14T05:00:00Z", "", false},
		{join, "", false},
		{"2026-11-14T06:00:01Z", "", true},
		{"", "", false},
		{"invalid", "", false},
		{"2026-11-14T06:00:01Z", "2026-11-14T05:00:00Z", false},
		{"", "2026-11-14T06:00:01Z", true},
	} {
		if got := crewTeamAgentJoinedAfter(join, tc.created, tc.history); got != tc.want {
			t.Fatalf("created=%q history=%q: %v want %v", tc.created, tc.history, got, tc.want)
		}
	}
}
