package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-509: actual HTTP/file paths, with an allowed edit before each refusal.
// Folder membership never grants the source owner's private Crew authority.
func mustReadCrewTeam(t *testing.T, project string) crewTeam {
	t.Helper()
	team, err := readCrewTeam(project)
	if err != nil {
		t.Fatal(err)
	}
	return team
}

func TestCrewTeamFolderHTTPConflictRetryAndRevocation(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	crewTeamTestDocs(t, "team-test", "team-test") // the member's server copy lives in the Crew's folder
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"member","username":"member","can_create":true,"products":["work"]},{"id":"teammate","username":"teammate","can_create":true,"products":["work"]},{"id":"outsider","username":"outsider","can_create":true,"products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "team-test", OwnerID: "owner", ProjectID: "team-test"}); err != nil {
		t.Fatal(err)
	}
	// The online copy of a Crew that lives on its owner's Mac (a Crew on the server has no Mac agents list).
	if err := crewroot.WriteSync("team-test", crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	router := mux.NewRouter()
	api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	call := func(user, method, route, body, transfer string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, route, strings.NewReader(body))
		if !r.URL.Query().Has("folder_id") {
			if team, err := readCrewTeam("team-test"); err == nil {
				q := r.URL.Query()
				q.Set("folder_id", team.Folder.ID)
				r.URL.RawQuery = q.Encode()
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		r.Header.Set("X-Team-Transfer", transfer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	base := "/api/crew-teams/team-test"
	// Team off (the Team tab's switch): the saved roster grants nothing, folder or agents.
	w := call("owner", "PUT", base, `{"name":"Example","people":[{"user_id":"member"}],"folder":{"whole":true}}`, "")
	if w.Code != 200 || crewTeamMember("team-test", "member") || call("member", "GET", base+"/files", "", "").Code != 403 {
		t.Fatalf("a roster saved with Team off must not let its people in: %d %s", w.Code, w.Body.String())
	}
	// A plain refusal must not reveal the owner or whether this private Crew exists.
	former := call("member", "GET", base, "", "")
	unknown := call("outsider", "GET", "/api/crew-teams/missing", "", "")
	if former.Code != 403 || unknown.Code != 403 || former.Body.String() != unknown.Body.String() {
		t.Fatal("lost-access refusal discloses a private Team's state")
	}
	config := `{"name":"Example","on":true,"people":[{"user_id":"member"},{"user_id":"teammate"}],"folder":{"whole":true}}`
	w = call("owner", "PUT", base, config, "")
	if w.Code != 200 {
		t.Fatalf("owner config: %d %s", w.Code, w.Body.String())
	}
	// Added people are invited, not members, until they accept (the decisions log, 5 Nov).
	if crewTeamMember("team-test", "member") || call("member", "GET", base+"/files", "", "").Code != 403 {
		t.Fatal("an invited person must have no access before accepting")
	}
	if w = call("outsider", "POST", base+"/invite", `{"accept":true}`, ""); w.Code != 404 {
		t.Fatalf("someone not invited accepted: %d %s", w.Code, w.Body.String())
	}
	for _, who := range []string{"member", "teammate"} {
		if w = call(who, "POST", base+"/invite", `{"accept":true}`, ""); w.Code != 200 {
			t.Fatalf("%s accept: %d %s", who, w.Code, w.Body.String())
		}
	}
	// The team's agents (a shared room, 5 Nov): members see the owner's In team agents, grouped by person; Only me
	// agents and non-members see nothing.
	if err := writeMacSharedAgents("team-test", []macSharedAgent{{Tab: "team01", Name: "Reviewer", Purpose: "Checks invoices", Audience: "team"}, {Tab: "solo01", Name: "Solo", Purpose: "Private", AgentBridgePublic: true}}); err != nil {
		t.Fatal(err)
	}
	if w = call("member", "GET", base+"/agents", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"Reviewer"`) || strings.Contains(w.Body.String(), `"Solo"`) {
		t.Fatalf("member team agents: %d %s", w.Code, w.Body.String())
	}
	if w = call("outsider", "GET", base+"/agents", "", ""); w.Code != 403 {
		t.Fatalf("outsider saw the team's agents: %d %s", w.Code, w.Body.String())
	}
	// One folder picker on the Mac app and the website (owner, 6 Nov): anyone in the Team lists through the server;
	// with the owner's Mac off it says so instead of listing. Someone outside the Team is refused.
	if w = call("member", "GET", base+"/folders?path=", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"offline"`) {
		t.Fatalf("member folder listing with the Mac off: %d %s", w.Code, w.Body.String())
	}
	if w = call("outsider", "GET", base+"/folders?path=", "", ""); w.Code != 403 {
		t.Fatalf("outsider listed the Crew's folders: %d %s", w.Code, w.Body.String())
	}
	agents := []macSharedAgent{{Tab: "team01", Audience: "team"}, {Tab: "solo01", AgentBridgePublic: true}}
	for _, tc := range []struct{ tab, who, reason string }{{"team01", "member", ""}, {"team01", "outsider", "not_in_audience"}, {"solo01", "member", "not_in_audience"}} {
		if _, reason := macSharedAudience(agents, tc.tab, tc.who, "owner", crewTeamPeople("team-test"), false); reason != tc.reason {
			t.Fatalf("audience %s/%s: %s", tc.tab, tc.who, reason)
		}
	}
	if !crewTeamMember("team-test", "member") {
		t.Fatal("active member missing")
	}
	// The creator is not a removable roster entry, and owner_id from a PUT
	// never transfers the Crew, even when a joined member edits Team settings.
	if w = call("member", "PUT", base, `{"on":true,"people":[{"user_id":"owner"}]}`, ""); w.Code != 400 {
		t.Fatalf("creator accepted as removable person: %d %s", w.Code, w.Body.String())
	}
	forgedOwner := strings.Replace(config, `"name":"Example"`, `"name":"Example","owner_id":"member"`, 1)
	if w = call("member", "PUT", base, forgedOwner, ""); w.Code != 200 {
		t.Fatalf("member update: %d %s", w.Code, w.Body.String())
	}
	if saved, err := readCrewTeam("team-test"); err != nil || saved.OwnerID != "owner" {
		t.Fatalf("creator changed: %+v %v", saved, err)
	}
	// Joined members manage the same Team settings; outsiders still cannot.
	if w = call("member", "PUT", base, config, ""); w.Code != 200 {
		t.Fatalf("member cannot manage Team: %d %s", w.Code, w.Body.String())
	}
	for _, who := range []string{"outsider"} {
		if w = call(who, "PUT", base, config, ""); w.Code != 403 {
			t.Fatalf("%s changed team: %d %s", who, w.Code, w.Body.String())
		}
	}
	// Owner, 5 Nov: only the Crew's main owner turns the Team on/off, switches Folders / Whole Crew and stops sharing a
	// folder; members add people and folders. The one save path refuses the rest, for every caller.
	if w = call("member", "PUT", base, strings.Replace(config, `"on":true`, `"on":false`, 1), ""); w.Code != 403 || !strings.Contains(w.Body.String(), "Only owner can turn the Team on or off.") {
		t.Fatalf("member turned the Team off: %d %s", w.Code, w.Body.String())
	}
	folders := func(list string) string {
		return strings.Replace(config, `"folder":{"whole":true}`, `"folder":{"folders":[`+list+`]}`, 1)
	}
	if w = call("member", "PUT", base, folders(`"team"`), ""); w.Code != 403 {
		t.Fatalf("member switched to Folders: %d %s", w.Code, w.Body.String())
	}
	if w = call("owner", "PUT", base, folders(`"team"`), ""); w.Code != 200 {
		t.Fatalf("owner switched to Folders: %d %s", w.Code, w.Body.String())
	}
	if w = call("member", "PUT", base, folders(`"team","notes"`), ""); w.Code != 200 {
		t.Fatalf("member added a folder: %d %s", w.Code, w.Body.String())
	}
	if w = call("member", "PUT", base, folders(`"notes"`), ""); w.Code != 403 {
		t.Fatalf("member stopped sharing a folder: %d %s", w.Code, w.Body.String())
	}
	if saved, _ := readCrewTeam("team-test"); !slices.Equal(saved.Folder.Folders, []string{"notes", "team"}) || !saved.On {
		t.Fatalf("folders after member changes: %+v", saved.Folder)
	}
	memberTab, ownerTab := crewTeamTabFor(t.Context(), mustReadCrewTeam(t, "team-test"), "member"), crewTeamTabFor(t.Context(), mustReadCrewTeam(t, "team-test"), "owner")
	if memberTab.CanSwitch || memberTab.CanChoose || !memberTab.CanAddFolder || !memberTab.CanAllMine || memberTab.CanRemoveFolder || !ownerTab.CanSwitch || !ownerTab.CanChoose || !ownerTab.CanRemoveFolder {
		t.Fatalf("tab flags: member %+v owner %+v", memberTab, ownerTab)
	}
	if w = call("owner", "PUT", base, config, ""); w.Code != 200 {
		t.Fatalf("owner back to the Whole Crew: %d %s", w.Code, w.Body.String())
	}
	// All my agents in team is each person's own (owner, 5 Nov): a member's off hides only their agents, never the
	// owner's; a Main is always In team.
	if w = call("member", "POST", base+"/agent-visibility", `{"agent_key":"*","in_team":false}`, ""); w.Code != 200 {
		t.Fatalf("member's own All my agents: %d %s", w.Code, w.Body.String())
	}
	if w = call("member", "POST", base+"/agent-visibility", `{"agent_key":"main","in_team":false}`, ""); w.Code != 400 {
		t.Fatalf("member's Main went Only me: %d %s", w.Code, w.Body.String())
	}
	if saved := mustReadCrewTeam(t, "team-test"); !saved.agentInTeam("owner", "team01") || saved.agentInTeam("member", "team-test"+codeChatSideMarker+"notes") || !saved.agentInTeam("member", macMainTab) || saved.allMine("member") {
		t.Fatalf("member's All my agents changed the wrong agents: %+v", saved.AgentChoices)
	}
	if w = call("owner", "POST", base+"/leave", `{}`, ""); w.Code != 403 || !crewTeamMember("team-test", "owner") {
		t.Fatalf("the owner left their own Team: %d", w.Code)
	}
	if w = call("member", "PUT", base, config, ""); w.Code != 200 {
		t.Fatalf("member update: %d %s", w.Code, w.Body.String())
	}
	file := base + "/file?path=brief.md"
	w = call("owner", "PUT", file, "first", "first")
	if w.Code != 200 {
		t.Fatalf("first write: %d %s", w.Code, w.Body.String())
	}
	state, e := readCrewTeamFileState("team-test")
	if e != nil {
		t.Fatal(e)
	}
	first := state.Versions["brief.md"].File.Hash
	w = call("owner", "PUT", file+"&base="+first, "owner changed", "owner-change")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "PUT", file+"&base="+first, "member changed", "member-change")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var receipt crewTeamReceipt
	if e = json.Unmarshal(w.Body.Bytes(), &receipt); e != nil {
		t.Fatal(e)
	}
	if len(receipt.Conflicts) != 1 {
		t.Fatalf("no recorded clash: %+v", receipt)
	}
	copyPath := receipt.Conflicts[0]
	w = call("member", "GET", base+"/history?revision=2&path="+url.QueryEscape(copyPath), "", "")
	if w.Code != 200 || w.Body.String() != "owner changed" {
		t.Fatalf("historical losing bytes: %d %s", w.Code, w.Body.String())
	}
	// Lost response replay must not change the folder revision or create another copy.
	w = call("member", "PUT", file+"&base="+first, "member changed", "member-change")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var retry crewTeamReceipt
	_ = json.Unmarshal(w.Body.Bytes(), &retry)
	if retry.Revision != receipt.Revision {
		t.Fatal("retry became another edit")
	}
	// A clash keeps the losing bytes in history, not another file in the folder.
	state, e = readCrewTeamFileState("team-test")
	if e != nil || len(state.manifest()) != 1 {
		t.Fatalf("visible copy was created: %+v %v", state, e)
	}
	w = call("member", "GET", base+"/clashes", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"losing_revision":2`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "POST", base+"/resolve", `{"path":"brief.md","keep":"revision:2","winning_revision":3,"transfer_id":"restore-old"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "GET", file, "", "")
	if w.Code != 200 || w.Body.String() != "owner changed" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "GET", base+"/clashes", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"clashes":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "PUT", file, "member changed", "member-again")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Chunks are served only for files the caller may read now: an old version's chunk is not fetched by its hash
	// (reviewer, 5 Nov); its history stays readable through the history route.
	w = call("member", "POST", base+"/chunks", `{"chunks":[{"id":"`+first+`","n":5}]}`, "")
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("an old version's chunk was served by hash: %d %d bytes", w.Code, w.Body.Len())
	}
	// Every joined member edits the shared folder; there is no per-person switch (owner, 5 Nov).
	if w = call("teammate", "PUT", base+"/file?path=teammate.md", "teammate wrote", "teammate-write"); w.Code != 200 {
		t.Fatalf("joined teammate could not write the shared folder: %d %s", w.Code, w.Body.String())
	}
	for _, who := range []string{"outsider"} {
		for _, method := range []string{"GET", "PUT"} {
			w = call(who, method, file, "should not be saved", "denied")
			if w.Code != 403 || !strings.Contains(w.Body.String(), "team_folder_forbidden") {
				t.Fatalf("%s %s: %d %s", who, method, w.Code, w.Body.String())
			}
		}
	}
	// Whole Crew still leaves out private state, credentials and each person's own memory (owner, 5 Nov).
	for _, p := range []string{"../outside.txt", ".env", "_system/crew-data/owner/chats.json", ".claude/projects/chat.jsonl", "MEMORY.md", "memory/notes.md", "product.json"} {
		w = call("member", "PUT", base+"/file?path="+url.QueryEscape(p), "private", "invalid")
		if w.Code != 403 || !strings.Contains(w.Body.String(), "team_path_forbidden") {
			t.Fatalf("path %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	if _, err := api.crewSyncRoots(t.Context(), &UserClaims{UserID: "member"}, "team-test"); err == nil {
		t.Fatal("folder member acquired whole Crew sync authority")
	}
	// A sync plan naming private state is refused before any chunk is taken.
	invalid := `{"client":"mac","changes":[{"path":".env","after":{"hash":"` + strings.Repeat("a", 64) + `","size":1,"chunks":[{"id":"` + strings.Repeat("a", 64) + `","n":1}]}}]}`
	w = call("member", "POST", base+"/sync/plan", invalid, "")
	if w.Code != 409 {
		t.Fatalf("private-state plan accepted: %d %s", w.Code, w.Body.String())
	}
	// Two actual clients over real HTTP with the chunked protocol, no mocked content transport. The interrupted
	// chunk upload is cut in half; the download edits a local file while its chunks arrive.
	var onDownload atomic.Bool
	var interruptUpload atomic.Bool
	var dirtyPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/chunks") && interruptUpload.CompareAndSwap(true, false) {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw[:len(raw)/2]))
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chunks") && who == "member" && onDownload.CompareAndSwap(true, false) {
			_ = os.WriteFile(dirtyPath, []byte("member edited while downloading"), 0600)
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who, Username: who}))
		router.ServeHTTP(w, r)
	}))
	defer server.Close()
	newClient := func(who string) (desktopConnection, string, *crewTeamLocal) {
		dir := t.TempDir()
		root := filepath.Join(dir, "files")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		c := desktopConnection{ID: who, Server: server.URL, Token: who, User: UserInfo{ID: who}}
		team, _ := readCrewTeam("team-test")
		b := &crewTeamLocal{FolderID: team.Folder.ID, Connection: who, Project: "team-test", RemoteUser: who, Client: who + "-mac", Root: root}
		return c, dir, b
	}
	c1, d1, b1 := newClient("owner")
	c2, d2, b2 := newClient("member")
	sync := func(c desktopConnection, dir string, b *crewTeamLocal) {
		t.Helper()
		if err := syncCrewTeamLocal(t.Context(), c.User.ID, c, dir, b); err != nil {
			t.Fatal("sync", c.User.ID, err)
		}
	}
	read := func(root, path, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(got) != want {
			t.Fatalf("file %s: %q %v; want %q", path, got, err, want)
		}
	}
	// Each copy's first round only receives the team folder (review 5 Nov #1).
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	if err := os.WriteFile(filepath.Join(b1.Root, "sync.md"), []byte("owner sends"), 0600); err != nil {
		t.Fatal(err)
	}
	interruptUpload.Store(true)
	if err := syncCrewTeamLocal(t.Context(), "owner", c1, d1, b1); err == nil {
		t.Fatal("interrupted upload unexpectedly completed")
	}
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	read(b2.Root, "sync.md", "owner sends")
	_ = os.WriteFile(filepath.Join(b2.Root, "sync.md"), []byte("member returns"), 0600)
	sync(c2, d2, b2)
	sync(c1, d1, b1)
	read(b1.Root, "sync.md", "member returns")
	_ = os.WriteFile(filepath.Join(b1.Root, "sync.md"), []byte("server arriving"), 0600)
	sync(c1, d1, b1)
	dirtyPath = filepath.Join(b2.Root, "sync.md")
	onDownload.Store(true)
	sync(c2, d2, b2)
	read(b2.Root, "sync.md", "member edited while downloading")
	sync(c2, d2, b2)
	sync(c1, d1, b1)
	read(b1.Root, "sync.md", "member edited while downloading")
	// Already accepted bytes must not be replayed as a new write.
	beforeRetry, _ := readCrewTeamFileState("team-test")
	sync(c2, d2, b2)
	afterRetry, _ := readCrewTeamFileState("team-test")
	if beforeRetry.Revision != afterRetry.Revision {
		t.Fatal("unchanged client created another revision")
	}
	teamHas := func(path, want string) {
		t.Helper()
		state, _ := readCrewTeamFileState("team-test")
		v, ok := state.Versions[path]
		if want == "" {
			if ok && v.File != nil {
				t.Fatalf("the team still has %s", path)
			}
			return
		}
		sum := sha256.Sum256([]byte(want))
		if !ok || v.File == nil || v.File.Hash != hex.EncodeToString(sum[:]) {
			t.Fatalf("the team's %s is not %q", path, want)
		}
	}
	// Review 5 Nov #4: a round started from a stale read of the copy's record (the turn and the 5 s loop overlap)
	// works from the record on disk, so it never sends an old base that would revert a newer edit.
	stale, err := readCrewTeamLocal(d2)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(b1.Root, "sync.md"), []byte("owner v2"), 0600)
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	_ = os.WriteFile(filepath.Join(b1.Root, "sync.md"), []byte("owner v3"), 0600)
	sync(c1, d1, b1)
	sync(c2, d2, &stale)
	teamHas("sync.md", "owner v3")
	read(b2.Root, "sync.md", "owner v3")
	b2 = &stale
	// Review 5 Nov #2: a file this copy leaves out only because of this machine's own ignore rules, or a file the
	// share's .gitignore newly leaves out while it is still here, is never sent as a removal.
	_ = os.WriteFile(filepath.Join(b1.Root, "kept.md"), []byte("keep me"), 0600)
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".config", "git"), 0700)
	_ = os.WriteFile(filepath.Join(home, ".config", "git", "ignore"), []byte("kept.md\n"), 0600)
	_ = os.WriteFile(filepath.Join(filepath.Dir(b2.Root), ".gitignore"), []byte("kept.md\n"), 0600)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	sync(c2, d2, b2)
	teamHas("kept.md", "keep me")
	_ = os.WriteFile(filepath.Join(b1.Root, ".gitignore"), []byte("kept.md\n"), 0600)
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	sync(c1, d1, b1)
	teamHas("kept.md", "keep me")
	read(b2.Root, "kept.md", "keep me")
	_ = os.Remove(filepath.Join(b1.Root, ".gitignore"))
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	fmt.Println("[CREW_TEAM_PROOF] two chunked HTTP clients: interrupted upload resumes, owner to member, member to owner, local edit during download retained, unchanged retry passed")
	// PR932: directory/file replacements must not strand the next sync after deleting children.
	if err := os.MkdirAll(filepath.Join(b1.Root, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b1.Root, "reports", "old.md"), []byte("old report"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	if err := os.RemoveAll(filepath.Join(b1.Root, "reports")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b1.Root, "reports"), []byte("now a file"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	read(b2.Root, "reports", "now a file")
	if err := os.Remove(filepath.Join(b2.Root, "reports")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(b2.Root, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b2.Root, "reports", "new.md"), []byte("directory again"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(c2, d2, b2)
	sync(c1, d1, b1)
	read(b1.Root, "reports/new.md", "directory again")
	// Idle rounds send nothing and make no revision.
	idle, _ := readCrewTeamFileState("team-test")
	for i := 0; i < 4; i++ {
		sync(c2, d2, b2)
	}
	if after, _ := readCrewTeamFileState("team-test"); after.Revision != idle.Revision {
		t.Fatal("idle rounds made a revision")
	}
	// A large file with a small edit sends only the changed chunks (the chunked design, 5 Nov).
	big := make([]byte, 3<<20)
	rand.New(rand.NewSource(7)).Read(big) // real content has cut points; a repeating pattern has none
	if err := os.WriteFile(filepath.Join(b1.Root, "big.bin"), big, 0600); err != nil {
		t.Fatal(err)
	}
	sync(c1, d1, b1)
	store, _ := crewTeamChunkStore("team-test")
	chunksBefore, _ := filepath.Glob(filepath.Join(store, "*", "*"))
	big = append(big[:len(big)/2], append([]byte("a small insert"), big[len(big)/2:]...)...)
	if err := os.WriteFile(filepath.Join(b1.Root, "big.bin"), big, 0600); err != nil {
		t.Fatal(err)
	}
	sync(c1, d1, b1)
	chunksAfter, _ := filepath.Glob(filepath.Join(store, "*", "*"))
	if added := len(chunksAfter) - len(chunksBefore); added < 1 || added > 3 {
		t.Fatalf("a small insert in a 3 MB file stored %d new chunks", added)
	}
	sync(c2, d2, b2)
	if got, _ := os.ReadFile(filepath.Join(b2.Root, "big.bin")); !bytes.Equal(got, big) {
		t.Fatal("the edited large file did not arrive whole")
	}
	t.Log("[CREW_TEAM_PROOF] idle rounds make no revision; a small insert in a large file moves only its chunks")
	// Canonical reads reject symlinks and hardlinks even when a host process plants one.
	dir, _ := crewTeamContentDir("team-test")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("not shared"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct {
		name string
		fn   func(string, string) error
	}{{"link.txt", os.Symlink}, {"hard.txt", os.Link}} {
		if err := link.fn(outside, filepath.Join(dir, "files", link.name)); err != nil {
			t.Fatal(err)
		}
		w = call("member", "GET", base+"/file?path="+link.name, "", "")
		if w.Code == 200 || strings.Contains(w.Body.String(), "not shared") {
			t.Fatalf("link read escaped: %d %s", w.Code, w.Body.String())
		}
	}
	// Revocation follows an allowed read, and also blocks replay of already accepted edits.
	w = call("member", "GET", file, "", "")
	if w.Code != 200 || w.Body.String() != "member changed" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("owner", "PUT", base, `{"people":[],"folder":{"whole":true}}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/sync/changes", "/chunks"} {
		if w = call("member", "POST", base+path, `{"client":"member-mac","chunks":[]}`, ""); w.Code != 403 {
			t.Fatal("revoked member still syncs", path, w.Code, w.Body.String())
		}
	}
	if crewTeamMember("team-test", "member") {
		t.Fatal("revoked member remained in the audience")
	}
	for _, method := range []string{"GET", "PUT"} {
		w = call("member", method, file, "member changed", "member-change")
		if w.Code != 403 {
			t.Fatal("revoked operation", method, w.Code, w.Body.String())
		}
	}
	fmt.Println("[CREW_TEAM_PROOF] allowed owner/member writes, history-only clash, retry receipt, outside-folder/private-state/link refusals, owner-only configuration and revocation passed")
	// Changing the selected folder keeps the Team's store (6 Nov: a new store per change re-uploaded every file): the
	// files no longer shared stay in the store unseen, nobody can read or write them, and a copy keeps syncing.
	oldTeam, _ := readCrewTeam("team-test")
	w = call("owner", "PUT", base, `{"name":"Example","on":true,"people":[{"user_id":"member"}],"folder":{"folders":["other"]}}`, "")
	if w.Code != 200 {
		t.Fatal("change folder", w.Code, w.Body.String())
	}
	// Removed earlier, so added back as an invite.
	if w = call("member", "POST", base+"/invite", `{"accept":true}`, ""); w.Code != 200 {
		t.Fatal("member accept again", w.Code, w.Body.String())
	}
	if newTeam, _ := readCrewTeam("team-test"); newTeam.Folder.ID != oldTeam.Folder.ID {
		t.Fatal("a folder change started a new store")
	}
	if w = call("owner", "PUT", file, "outside the share now", "outside-now"); w.Code != 403 {
		t.Fatal("a write to a folder no longer shared was admitted", w.Code)
	}
	beforeNarrowed, _ := readCrewTeamFileState("team-test")
	sync(c1, d1, b1)
	if after, _ := readCrewTeamFileState("team-test"); after.Revision != beforeNarrowed.Revision {
		t.Fatal("a copy sent files after the share was narrowed")
	}
	w = call("member", "GET", base+"/files", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"files":[]`) {
		t.Fatal("old files leaked into newly selected folder", w.Code, w.Body.String())
	}
	t.Log("[CREW_TEAM_PROOF] folder_change=isolated stale_write=403 old_history=retained")
	// Live Showcase first-upload failure: there is no canonical files directory yet.
	c3, d3, b3 := newClient("owner")
	// Folders share only what is inside them: a file beside them stays on the owner's side (owner, 5 Nov).
	if err := os.MkdirAll(filepath.Join(b3.Root, "other"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b3.Root, "other", "first-upload.md"), []byte("new folder starts here"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b3.Root, "not-shared.md"), []byte("stays here"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(c3, d3, b3) // a new copy first receives the folder, then sends its own files
	sync(c3, d3, b3)
	w = call("member", "GET", base+"/file?path=other/first-upload.md", "", "")
	if w.Code != 200 || w.Body.String() != "new folder starts here" {
		t.Fatal("first upload into empty generation", w.Code, w.Body.String())
	}
	if w = call("member", "GET", base+"/files", "", ""); strings.Contains(w.Body.String(), "not-shared.md") {
		t.Fatal("a file outside the chosen folders was shared", w.Body.String())
	}
	if w = call("member", "PUT", base+"/file?path=not-shared.md", "member writes outside", "outside-folders"); w.Code != 403 {
		t.Fatal("a write outside the chosen folders was accepted", w.Code, w.Body.String())
	}
	t.Log("[CREW_TEAM_PROOF] first_upload_into_empty_folder=pass")
	// A member's server copy (their agents on the server, 5 Nov): what their agent writes reaches the team as theirs,
	// and it is their attached folder; nobody else gets it.
	copyRoot, err := api.syncCrewTeamServerCopy(t.Context(), "member", "team-test", true)
	if err != nil || copyRoot == "" {
		t.Fatalf("member server copy: %q %v", copyRoot, err)
	}
	if err = os.WriteFile(filepath.Join(copyRoot, "other", "from-server-agent.md"), []byte("written by the member's agent"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = api.syncCrewTeamServerCopy(t.Context(), "member", "team-test", false); err != nil {
		t.Fatal(err)
	}
	if w = call("owner", "GET", base+"/files", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"other/from-server-agent.md"`) || !strings.Contains(w.Body.String(), `"author":"member"`) {
		t.Fatalf("server copy upload not recorded as the member's: %d %s", w.Code, w.Body.String())
	}
	if grants := crewTeamServerFolders(&UserClaims{UserID: "member"}); len(grants) != 1 || grants[0].Path != copyRoot || grants[0].Alias != "example" {
		t.Fatalf("member's attached team folder: %+v", grants)
	}
	if grants := crewTeamServerFolders(&UserClaims{UserID: "outsider"}); len(grants) != 0 {
		t.Fatalf("outsider got a team folder: %+v", grants)
	}
	// PLAT-856-509: Folders -> Whole Crew while the member's agent has an edit not yet sent. The old copy's record
	// can no longer send; its folder used to be wiped. The folder is kept and the edit reaches the team.
	if err = os.WriteFile(filepath.Join(copyRoot, "other", "unsent.md"), []byte("not sent yet"), 0600); err != nil {
		t.Fatal(err)
	}
	if w = call("owner", "PUT", base, `{"name":"Example","on":true,"people":[{"user_id":"member"}],"folder":{"whole":true}}`, ""); w.Code != 200 {
		t.Fatal("share switch with an unsent edit", w.Code, w.Body.String())
	}
	api.removeStaleCrewTeamServerCopies(t.Context())
	if _, err = api.syncCrewTeamServerCopy(t.Context(), "member", "team-test", true); err != nil {
		t.Fatal(err)
	}
	if w = call("owner", "GET", base+"/files", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"other/unsent.md"`) {
		t.Fatalf("the member's unsent edit was lost on the share switch: %d %s", w.Code, w.Body.String())
	}
	// Removed from the team: the copy holds files they have no right to, so it goes.
	if w = call("owner", "PUT", base, `{"name":"Example","on":true,"people":[],"folder":{"folders":["other"]}}`, ""); w.Code != 200 {
		t.Fatal("remove member", w.Code, w.Body.String())
	}
	api.removeStaleCrewTeamServerCopies(t.Context())
	if _, err = os.Stat(copyRoot); !os.IsNotExist(err) {
		t.Fatalf("a removed member's server copy stayed: %v", err)
	}
	// Owner, 5 Nov: Whole Crew, then back to Folders, brings back the folders shared before (the server decides).
	for _, body := range []string{`{"name":"Example","on":true,"people":[],"folder":{"whole":true}}`, `{"name":"Example","on":true,"people":[],"folder":{"folders":[]}}`} {
		if w = call("owner", "PUT", base, body, ""); w.Code != 200 {
			t.Fatal("share switch", w.Code, w.Body.String())
		}
	}
	if back, _ := readCrewTeam("team-test"); back.Folder.Whole || !slices.Equal(back.Folder.Folders, []string{"other"}) {
		t.Fatalf("back to Folders: %+v", back.Folder)
	}
	// Owner, 6 Nov: Folders always shares at least one, so the last one is not stopped.
	if w = call("owner", "PUT", base, `{"name":"Example","on":true,"people":[],"folder":{"folders":[]}}`, ""); w.Code != 400 {
		t.Fatalf("stopped sharing the last folder: %d %s", w.Code, w.Body.String())
	}
}

// Owner, 5 Nov: a Mac Crew's Team survives Mac online off and on. It moves to the new online copy only from a copy that
// is gone, only for its own owner, and never over a Team the new copy already has.
func TestCrewTeamMovesToNewOnlineCopyOnlyFromAGoneCopy(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	for _, id := range []string{"new-copy", "live-copy"} {
		if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: id, OwnerID: "owner", ProjectID: id}); err != nil {
			t.Fatal(err)
		}
	}
	team := func(project, owner string) crewTeam {
		return crewTeam{ProjectID: project, OwnerID: owner, On: true, People: []crewTeamPerson{{UserID: "member"}}, Folder: crewTeamFolder{ID: "f1", Folders: []string{"team"}}}
	}
	for _, t0 := range []crewTeam{team("gone-copy", "owner"), team("live-copy", "owner"), team("someone-else", "other")} {
		if err := writeCrewTeam(t0); err != nil {
			t.Fatal(err)
		}
	}
	moveCrewTeamToTwin("owner", "live-copy", "new-copy") // the old copy still exists: not moved
	moveCrewTeamToTwin("owner", "someone-else", "new-copy")
	if _, err := readCrewTeam("new-copy"); err == nil {
		t.Fatal("a Team moved from a live copy or from another owner's")
	}
	moveCrewTeamToTwin("owner", "gone-copy", "new-copy")
	moved, err := readCrewTeam("new-copy")
	if err != nil || moved.On || len(moved.People) != 1 || moved.People[0].Invited || moved.Folder.ID != "f1" {
		t.Fatalf("moved Team: %+v %v", moved, err)
	}
	if _, err := readCrewTeam("gone-copy"); err == nil {
		t.Fatal("the old Team record stayed behind")
	}
}

// Review 5 Nov #1: a new copy changes nothing on the team before it has received the folder once, and the Crew setup
// it writes afterwards is the team's, never its own (a new member's blank setup replaced the owner's).
func TestCrewTeamNewCopyReceivesTheTeamSetupBeforeWriting(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	api := f.api
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	setup := crewTeamSetup{ChatInstructions: "Team instructions written by the owner", SelectedSkills: []string{}}.encode()
	store, err := crewTeamChunkStore("team")
	if err != nil {
		t.Fatal(err)
	}
	version, err := crewTeamChunkReader(bytes.NewReader(setup), int64(len(setup)), false, func(id string, chunk []byte) error { return crewTeamPutChunk(store, id, chunk) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = commitCrewTeamChanges(t.Context(), "team", "owner", "setup", crewTransferDigest("setup"), []crewTeamChange{{Path: crewTeamSetupFile, After: &version}}); err != nil {
		t.Fatal(err)
	}
	if current, _, _, ok := api.crewSetupOf(t.Context(), "owner", "team"); !ok || current.ChatInstructions == "Team instructions written by the owner" {
		t.Fatalf("fixture Crew: %+v %t", current, ok)
	}
	dir := t.TempDir()
	b := &crewTeamLocal{FolderID: "f1", Connection: "server", Project: "team", LocalProject: "team", RemoteUser: "owner", Client: "new-copy", Root: filepath.Join(dir, "files")}
	if err = os.MkdirAll(b.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(t.Context(), crewTeamTransportKey{}, crewTeamInProcess{handler: api.crewTeamServerHandler(), claims: claims})
	c := desktopConnection{ID: "server", Server: crewTeamServerHost, User: UserInfo{ID: "owner"}}
	before, _ := readCrewTeamFileState("team")
	for i := 0; i < 2; i++ {
		if err = api.crewTeamMacRound(ctx, "owner", c, dir, b); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := readCrewTeamFileState("team")
	if after.Revision != before.Revision || after.Versions[crewTeamSetupFile].File.Hash != version.Hash {
		t.Fatalf("a new copy changed the team's setup: revision %d -> %d", before.Revision, after.Revision)
	}
	if got, _ := os.ReadFile(filepath.Join(b.Root, crewTeamSetupFile)); !bytes.Equal(got, setup) {
		t.Fatalf("the new copy's setup file: %q", got)
	}
	if applied, _, _, ok := api.crewSetupOf(t.Context(), "owner", "team"); !ok || applied.ChatInstructions != "Team instructions written by the owner" {
		t.Fatalf("the team's setup was not applied to the Crew: %+v %t", applied, ok)
	}
}

// Review 5 Nov #6: a new owner copy of a Crew on the server takes the store as its base: a file people on the website
// removed from the Crew folder is removed from the team (not written back), and their edit goes up.
func TestCrewTeamNewServerOwnerCopyKeepsTheCrewFolder(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	api := f.api
	team := crewTeam{ProjectID: "team", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true},
		People: []crewTeamPerson{{UserID: "other", Where: "mac", MacPair: "member-pair"}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if !serverCrewTeamStoreNeeded(team) {
		t.Fatal("fixture: the store is not needed")
	}
	claims := &UserClaims{UserID: "owner", Username: "owner"}
	roots, err := api.crewSyncRoots(context.WithValue(t.Context(), UserContextKey, claims), claims, "team")
	if err != nil {
		t.Fatal(err)
	}
	store, _ := crewTeamChunkStore("team")
	put := func(path, content string) {
		t.Helper()
		v, err := crewTeamChunkReader(strings.NewReader(content), int64(len(content)), false, func(id string, chunk []byte) error { return crewTeamPutChunk(store, id, chunk) })
		if err == nil {
			_, err = commitCrewTeamChanges(t.Context(), "team", "other", path, crewTransferDigest(path), []crewTeamChange{{Path: path, After: &v}})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	put("notes.md", "from the member's Mac")
	put("removed-on-web.md", "the web removed this")
	for path, content := range map[string]string{"notes.md": "edited on the website"} {
		if err = os.WriteFile(filepath.Join(roots.Files, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	api.syncServerCrewTeamStore(t.Context(), team)
	if _, err = os.Stat(filepath.Join(roots.Files, "removed-on-web.md")); !os.IsNotExist(err) {
		t.Fatal("a file removed on the website was written back into the Crew folder")
	}
	state, _ := readCrewTeamFileState("team")
	if v := state.Versions["removed-on-web.md"]; v.File != nil {
		t.Fatal("the team still has the file removed on the website")
	}
	sum := sha256.Sum256([]byte("edited on the website"))
	if v := state.Versions["notes.md"]; v.File == nil || v.File.Hash != hex.EncodeToString(sum[:]) || v.Author != "owner" {
		t.Fatalf("the website edit did not go up: %+v", v)
	}
}

// Owner, 5 Nov ("Sync it anyway"): a file that looks like it holds passwords or keys reaches the shared folder only
// when its uploader declared it on purpose in that transfer; machine state never does; removing it ends the choice.
func TestCrewTeamSyncsCredentialFileOnlyWhenChosenOnPurpose(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: "purpose-test", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	data := []byte("//registry.example.com/:_authToken=${TOKEN}\n")
	store, err := crewTeamChunkStore("purpose-test")
	if err != nil {
		t.Fatal(err)
	}
	version, err := crewTeamChunkReader(bytes.NewReader(data), int64(len(data)), false, func(id string, chunk []byte) error { return crewTeamPutChunk(store, id, chunk) })
	if err != nil {
		t.Fatal(err)
	}
	send := func(transfer string, declared ...string) error {
		_, err := commitCrewTeamChanges(t.Context(), "purpose-test", "member", transfer, crewTransferDigest(transfer), []crewTeamChange{{Path: "express/.npmrc", After: &version}}, declared...)
		return err
	}
	if err := send("undeclared"); err == nil {
		t.Fatal("a credential-looking file was shared without being chosen")
	}
	if crewTeamAllowed("node_modules/.npmrc", crewTeamOnPurpose{"node_modules/.npmrc": true}) {
		t.Fatal("machine state was allowed on purpose")
	}
	if err := send("chosen", "express/.npmrc"); err != nil {
		t.Fatalf("chosen file: %v", err)
	}
	state, _ := readCrewTeamFileState("purpose-test")
	if state.OnPurpose["express/.npmrc"] != "member" || state.Versions["express/.npmrc"].File == nil {
		t.Fatalf("not recorded on purpose: %+v", state.OnPurpose)
	}
	if _, err := commitCrewTeamChanges(t.Context(), "purpose-test", "member", "stop", crewTransferDigest("stop"), []crewTeamChange{{Path: "express/.npmrc", Before: version.Hash}}); err != nil {
		t.Fatalf("stop syncing: %v", err)
	}
	if state, _ = readCrewTeamFileState("purpose-test"); state.OnPurpose["express/.npmrc"] != "" {
		t.Fatal("the on-purpose record stayed after the file was removed")
	}
}

// One working copy per Team member (owner, 5 Nov): a member's Mac is admitted to relay this Crew only with the pair
// the server recorded for them once their agents work on their Mac, never with the owner's pair, and from then on
// the server refuses their turns here.
func TestCrewTeamMemberMacAdmittedOnlyWithTheirRecordedPair(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"member","username":"member","can_create":true,"products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "team-test", OwnerID: "owner", ProjectID: "team-test"}); err != nil {
		t.Fatal(err)
	}
	if err := crewroot.WriteSync("team-test", crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	member := &UserClaims{UserID: "member", Username: "member"}
	team := crewTeam{ProjectID: "team-test", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if api.crewRelayAdmit(t.Context(), member, crewRelayCrew{Project: "team-test", Pair: "owner-pair"}) == nil || crewLivesOnMac("work", "team-test:chat:mine", "member") {
		t.Fatal("a member on the web was admitted as the Crew's Mac, or lost their server turns")
	}
	team.People[0].Where, team.People[0].MacPair = "mac", "member-pair"
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if api.crewRelayAdmit(t.Context(), member, crewRelayCrew{Project: "team-test", Pair: "owner-pair"}) == nil {
		t.Fatal("a member's Mac was admitted with the owner's pair")
	}
	if err := api.crewRelayAdmit(t.Context(), member, crewRelayCrew{Project: "team-test", Pair: "member-pair"}); err != nil {
		t.Fatalf("a member on their Mac was refused their own pair: %v", err)
	}
	if !crewLivesOnMac("work", "team-test:chat:mine", "member") || crewOnMacRefusalFor("team-test:chat:mine", "member") != crewOnYourMacRefusal {
		t.Fatal("a member on their Mac still gets server turns here, or the wrong refusal")
	}
	// A second Mac of the same member takes the one working copy: it gets a new pair and the first Mac is refused.
	router := mux.NewRouter()
	api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	where := func(body string) string {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/crew-teams/team-test/where", strings.NewReader(body)).WithContext(context.WithValue(t.Context(), UserContextKey, member))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		var out struct {
			Pair string `json:"pair"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("where: %d %s", w.Code, w.Body.String())
		}
		return out.Pair
	}
	if got := where(`{"where":"mac","pair":"member-pair"}`); got != "member-pair" {
		t.Fatalf("the Mac that holds the copy lost its pair: %q", got)
	}
	second := where(`{"where":"mac"}`)
	if second == "" || second == "member-pair" || api.crewRelayAdmit(t.Context(), member, crewRelayCrew{Project: "team-test", Pair: "member-pair"}) == nil ||
		api.crewRelayAdmit(t.Context(), member, crewRelayCrew{Project: "team-test", Pair: second}) != nil {
		t.Fatal("a second Mac did not replace the first one's working copy")
	}
	// A member's Only me agent on their Mac is theirs alone: never listed for, or streamed to, anyone else.
	crewMacTeamTabsCache.Store("team-test\x00member", crewMacTeamTabsRead{at: time.Now(), tabs: []crewMacTeamTab{
		{Person: "member", Tab: "shared-tab", ConversationID: "c-shared", Name: "Shared"}, {Person: "member", Tab: "private-tab", ConversationID: "c-private", Name: "Private"}}})
	team, _ = readCrewTeam("team-test")
	team.AgentChoices = map[string]map[string]bool{"member": {"private-tab": false}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	if person, _ := crewMacTeamConversation(t.Context(), "team-test", "c-private"); person != "" || len(crewMacTabsFor(t.Context(), "member", "team-test", "owner")) != 1 {
		t.Fatal("a member's Only me agent on their Mac was listed or streamed to the owner")
	}
	if person, _ := crewMacTeamConversation(t.Context(), "team-test", "c-shared"); person != "member" || len(crewMacTabsFor(t.Context(), "member", "team-test", "member")) != 2 {
		t.Fatal("a member's In team agent was not watchable, or they lost their own Only me agent")
	}
	// A Crew that lives on the server (no owner's Mac): a member on their Mac is admitted with their own pair alone,
	// and their turns there go to their Mac; the owner never gets a Mac pair for it.
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "server-crew", OwnerID: "owner", ProjectID: "server-crew"}); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "server-crew", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member", Where: "mac", MacPair: "server-member-pair"}}}); err != nil {
		t.Fatal(err)
	}
	if err := api.crewRelayAdmit(t.Context(), member, crewRelayCrew{Project: "server-crew", Pair: "server-member-pair"}); err != nil || !crewLivesOnMac("work", "server-crew:chat:mine", "member") || crewLivesOnMac("work", "server-crew:chat:mine", "owner") {
		t.Fatalf("a member on their Mac in a server Crew: admitted=%v, or turns placed wrongly", err)
	}
	if _, ok := crewMacPairFor("owner", "server-crew"); ok {
		t.Fatal("the owner of a Crew on the server got a Mac pair")
	}
}
