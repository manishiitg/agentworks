package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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

	// Taking it out removes the Team's folders from the project and nothing else.
	files := t.TempDir()
	for _, rel := range []string{"team/notes.md", "TEAM_INSTRUCTIONS.md", "reports/own.md"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(files, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(files, rel), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir, err := crewTeamProjectCopyDir("srv", "mac", "f1")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	copyRecord := crewTeamLocal{FolderID: "f1", Project: "mac", LocalProject: "srv", RemoteUser: crewTeamProjectMemberID("srv"), Root: files, Mounted: true, ProjectMember: true,
		Mounts: map[string]crewTeamMount{"p1": {Remote: "team", Local: "team"}}, Files: map[string]crewTeamLocalFile{"TEAM_INSTRUCTIONS.md": {}}}
	if err = saveCrewTeamLocal(dir, copyRecord); err != nil {
		t.Fatal(err)
	}
	(&StreamingAPI{}).removeStaleCrewTeamProjectCopies(context.Background())
	if _, err := os.Stat(filepath.Join(files, "team")); err != nil {
		t.Fatal("a member project's copy was removed while it is still in the Team")
	}
	team.Projects = nil
	if err = writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	(&StreamingAPI{}).removeStaleCrewTeamProjectCopies(context.Background())
	for rel, want := range map[string]bool{"team": false, "TEAM_INSTRUCTIONS.md": false, "reports/own.md": true} {
		if _, err := os.Stat(filepath.Join(files, rel)); (err == nil) != want {
			t.Errorf("%s present=%v after removal, want %v", rel, err == nil, want)
		}
	}
}
