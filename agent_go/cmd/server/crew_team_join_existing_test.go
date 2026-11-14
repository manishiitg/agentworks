package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
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
	round := func(c desktopConnection, dir string, b *crewTeamLocal) {
		t.Helper()
		if err := syncCrewTeamLocal(t.Context(), c.User.ID, c, dir, b); err != nil {
			t.Fatal(err)
		}
	}
	round(c1, d1, b1)
	write(b1.Root, "docs/note.txt", "Team note")
	write(b1.Root, "skills/report/SKILL.md", "Team skill")
	round(c1, d1, b1)
	write(b2.Root, "docs/private.txt", "private document")
	write(b2.Root, "skills/report/SKILL.md", "private skill")
	write(b2.Root, "MEMORY.md", "private memory")
	write(b2.Root, "PROJECT_INSTRUCTIONS.md", "private guidance")
	if err := backupCrewTeamJoinSetup(b2.Root); err != nil {
		t.Fatal(err)
	}
	round(c2, d2, b2)
	read(b2.Root, "docs (team)/note.txt", "Team note")
	read(b2.Root, "docs/private.txt", "private document")
	read(b2.Root, "skills/report (team)/SKILL.md", "Team skill")
	read(b2.Root, "skills/report/SKILL.md", "private skill")
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
