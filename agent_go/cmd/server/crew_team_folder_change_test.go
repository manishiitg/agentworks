package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// Showcase, 6 Nov (PLAT-856-509): adding and removing a shared folder gave the Team a new, empty store each time, so
// every copy uploaded all 7,606 files again and 7,571 false clashes were notified. A folder change now keeps the store:
// only the new folder's files go up, nothing clashes, a removed folder's files leave what people can read, and sharing
// it again brings them back without an upload. A folder inside one already shared is refused, not saved as no change.
func TestCrewTeamFolderChangeKeepsStoreWithoutReupload(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	crewTeamTestDocs(t, "fold-test", "fold-test")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"member","username":"member","can_create":true,"products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "fold-test", OwnerID: "owner", ProjectID: "fold-test"}); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "fold-test", Name: "Example", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}, Folder: crewTeamFolder{ID: "f1", Folders: []string{"team"}}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	router := mux.NewRouter()
	api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	put := func(user, folders string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("PUT", "/api/crew-teams/fold-test", strings.NewReader(`{"name":"Example","on":true,"people":[{"user_id":"member"}],"folder":{"folders":[`+strings.TrimSuffix(strings.TrimPrefix(teamFolderTestEntries(folders), "["), "]")+`]}}`))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	type copyOf struct {
		c   desktopConnection
		dir string
		b   *crewTeamLocal
		ctx context.Context
	}
	newCopy := func(who string, files map[string]string) copyOf {
		dir := t.TempDir()
		root := filepath.Join(dir, "files")
		for rel, content := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		claims := &UserClaims{UserID: who, Username: who}
		return copyOf{c: desktopConnection{ID: who, Server: crewTeamServerHost, User: UserInfo{ID: who}}, dir: dir,
			b:   &crewTeamLocal{FolderID: "f1", Connection: who, Project: "fold-test", RemoteUser: who, Client: who + "-copy", Root: root},
			ctx: context.WithValue(t.Context(), crewTeamTransportKey{}, crewTeamInProcess{handler: api.crewTeamServerHandler(), claims: claims})}
	}
	sync := func(cs ...copyOf) {
		t.Helper()
		for _, c := range cs {
			if err := syncCrewTeamLocal(c.ctx, c.c.User.ID, c.c, c.dir, c.b); err != nil {
				t.Fatal("sync", c.c.User.ID, err)
			}
		}
	}
	state := func() crewTeamFileState {
		s, _ := readCrewTeamFileState("fold-test")
		return s
	}
	owner := newCopy("owner", map[string]string{"team/a.md": "a", "team/b.md": "b", "code/c.md": "c"})
	member := newCopy("member", nil)
	sync(owner, owner, member) // the owner's first round receives, the second sends team/
	start := state()
	if len(start.held(crewTeamFolder{Folders: []string{"team"}})) != 2 {
		t.Fatalf("team/ did not go up: %+v", start.Versions)
	}
	// Adding code/ keeps the store; only code/c.md goes up.
	if w := put("owner", `"code","team"`); w.Code != 200 {
		t.Fatal("add folder", w.Code, w.Body.String())
	}
	if team, _ := readCrewTeam("fold-test"); team.Folder.ID != "f1" {
		t.Fatalf("a folder change started a new store: %s", team.Folder.ID)
	}
	sync(owner, member)
	added := state()
	if paths, _ := crewTeamChangedSince(added, start.Revision, 100); len(paths) != 1 || paths[0] != "code/c.md" {
		t.Fatalf("adding a folder sent %v, want only code/c.md", paths)
	}
	if got, _ := os.ReadFile(filepath.Join(member.b.Root, "code", "c.md")); string(got) != "c" {
		t.Fatalf("member did not receive the added folder: %q", got)
	}
	// A folder inside one already shared is refused in plain words, with no save.
	if w := put("member", `"code","team","team/sub"`); w.Code != 400 || !strings.Contains(w.Body.String(), "team/ already shares that folder.") {
		t.Fatalf("nested folder: %d %s", w.Code, w.Body.String())
	}
	// Removing code/ keeps team/ in step and sends nothing; code/ leaves what people can read.
	if w := put("owner", `"team"`); w.Code != 200 {
		t.Fatal("remove folder", w.Code, w.Body.String())
	}
	sync(owner, member)
	if s := state(); s.Revision != added.Revision {
		t.Fatalf("removing a folder made copies send files: revision %d -> %d", added.Revision, s.Revision)
	}
	if _, err := os.Stat(filepath.Join(owner.b.Root, "code", "c.md")); err != nil {
		t.Fatal("the owner's own files of a folder no longer shared were touched", err)
	}
	// Shared again: the store's files are announced once and adopted, with no upload.
	if w := put("owner", `"code","team"`); w.Code != 200 {
		t.Fatal("share again", w.Code, w.Body.String())
	}
	announced := state()
	sync(owner, member, owner)
	if s := state(); s.Revision != announced.Revision || announced.Revision != added.Revision+1 {
		t.Fatalf("sharing a folder again uploaded files: revisions %d, %d, %d", added.Revision, announced.Revision, s.Revision)
	}
	if s := state(); len(s.Clashes) != 0 {
		t.Fatalf("folder changes made clashes: %+v", s.Clashes)
	}
	// PLAT-746-1020: screen, chat and MCP all set the complete selection.
	// Members can add, but omitting an existing folder never removes it.
	for _, adapter := range []string{"chat", "mcp"} {
		if w := put("owner", `"team"`); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		change := func(folders []any) bool {
			claims := &UserClaims{UserID: "member", Username: "member"}
			ctx := context.WithValue(t.Context(), UserContextKey, claims)
			if adapter == "chat" {
				_, err := api.crewTeamConfigureChat(ctx, claims, nil, "fold-test", "fold-test", "configure", map[string]any{"folders": folders})
				return err == nil
			}
			w := httptest.NewRecorder()
			api.externalCrewTeamSyncCall(w, httptest.NewRequest("POST", "/api/external/call", nil).WithContext(ctx), map[string]any{"crew_id": "fold-test", "action": "settings", "folders": folders})
			return w.Code == 200
		}
		if !change([]any{"team", "code"}) {
			t.Fatalf("%s could not add a folder", adapter)
		}
		if change([]any{"code"}) {
			t.Fatalf("%s let a member remove team/", adapter)
		}
		if change([]any{}) {
			t.Fatalf("%s accepted no shared folders", adapter)
		}
		current := mustReadCrewTeam(t, "fold-test")
		if current.Folder.ID != "f1" || !slices.Equal(current.Folder.Folders, []string{"code", "team"}) {
			t.Fatalf("%s changed the store or saved a refused selection: %+v", adapter, current.Folder)
		}
	}

}

// A long selected folder remains paged in chat.
func TestCrewTeamFilesPageRetainsContinuation(t *testing.T) {
	var many []map[string]any
	for i := range 450 {
		many = append(many, map[string]any{"path": fmt.Sprintf("d/f%03d.md", i)})
	}
	raw, _ := json.Marshal(map[string]any{"files": many})
	if page, err := crewTeamFilesPage(raw, "", 400); err != nil || !strings.Contains(string(page), "f449.md") || strings.Contains(string(page), "next_offset") {
		t.Fatalf("last page: %v %s", err, page)
	}
	if page, _ := crewTeamFilesPage(raw, "d", 0); !strings.Contains(string(page), `"next_offset":200`) || strings.Contains(string(page), "f200.md") {
		t.Fatalf("first page: %s", page)
	}
}
