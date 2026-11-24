package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	for _, p := range []struct{ id, owner string }{{"mac", "owner"}, {"srv", "bob"}, {"own", "owner"}, {"other", "bob"}} {
		if err := registry.Register(projectOwnerRecord{Product: "work", Folder: p.id + "-12345678", OwnerID: p.owner, Shared: true, ProjectID: p.id}); err != nil {
			t.Fatal(err)
		}
	}
	macCrewOnlineCopy(t, "mac")
	joinCrewTeam(t, "srv-12345678", "owner") // the Team's owner co-owns srv
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
