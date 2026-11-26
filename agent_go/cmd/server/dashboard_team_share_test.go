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

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// Sharing an Artifact with the Team (#3987): the copy is one self-contained page in the Team's shared folder, only
// Artifacts go, someone outside the Team can't reach it, and a teammate's copy is listed and opened under From the Team.
func TestDashboardTeamShareCopiesArtifactIntoTeamFolder(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"member","role":"editor","products":["work"]},{"id":"outsider","role":"editor","products":["work"]}]}`)
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}, Folder: crewTeamFolder{ID: "f1", Folders: []string{"team"}}}); err != nil {
		t.Fatal(err)
	}
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(context.Background(), UserContextKey, owner)
	artifact, plain := true, false
	for id, page := range map[string]struct {
		artifact *bool
		files    map[string]string
	}{
		"plan":  {&artifact, map[string]string{"index.html": `<html><head><link rel="stylesheet" href="style.css"></head><body><img src="./logo.svg"></body></html>`, "style.css": "body{color:red}", "logo.svg": "<svg/>"}},
		"sales": {&plain, map[string]string{"index.html": "<p>Sales</p>"}},
	} {
		created, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: externalCommandCrewRoot, Action: "create", ID: id, Title: "Page " + id, Artifact: page.artifact, IncludeDraft: true, Files: page.files})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: externalCommandCrewRoot, Action: "publish", ID: id, IncludeDraft: true, ExpectedRevision: created.Dashboard.Revision}); err != nil {
			t.Fatal(err)
		}
	}
	roots, found, err := crewStorageRoots(externalCommandCrewRoot)
	if err != nil || !found {
		t.Fatalf("project roots: %v %v", found, err)
	}
	copyPath := filepath.Join(roots.Files, "team", "artifacts", "plan.html")

	action := func(user, id, name, mode string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"dashboard_id": id, "action": name, "mode": mode})
		w := httptest.NewRecorder()
		f.api.handleCrewTeamDashboardAction(w, adminRequest(http.MethodPost, "/api/crew-teams/team/dashboards/action", string(body), &UserClaims{UserID: user, Username: user}, map[string]string{"project": "team"}))
		return w
	}
	if w := action("outsider", "plan", dashboardTeamAction, "on"); w.Code == http.StatusOK {
		t.Fatalf("someone outside the Team shared a page: %s", w.Body.String())
	}
	if w := action("owner", "sales", dashboardTeamAction, "on"); w.Code != http.StatusConflict {
		t.Fatalf("a live dashboard went to the Team: %d %s", w.Code, w.Body.String())
	}
	if w := action("owner", "plan", dashboardTeamAction, "on"); w.Code != http.StatusOK {
		t.Fatalf("share: %d %s", w.Code, w.Body.String())
	}
	copied, err := os.ReadFile(copyPath)
	page := string(copied)
	if err != nil || !strings.Contains(page, "<style>\nbody{color:red}") || !strings.Contains(page, `src="data:image/svg+xml;base64,`) || strings.Contains(page, "style.css") || !strings.Contains(page, `content="owner"`) {
		t.Fatalf("Team copy is not self-contained: %v %s", err, page)
	}

	// What a teammate's sync brings in: another project's page in the same shared folder.
	if err := os.WriteFile(filepath.Join(roots.Files, "team", "artifacts", "roadmap.html"), []byte(dashboardTeamStamp("<html><head><title>Roadmap</title></head><body>Q1</body></html>", "roadmap", "Roadmap", "teammate")), 0o644); err != nil {
		t.Fatal(err)
	}
	tabW := httptest.NewRecorder()
	f.api.handleCrewTeamDashboardsTab(tabW, adminRequest(http.MethodGet, "/api/crew-teams/team/dashboards-tab", "", &UserClaims{UserID: "member", Username: "member"}, map[string]string{"project": "team"}))
	var tab crewDashboardsTab
	if tabW.Code != http.StatusOK || json.Unmarshal(tabW.Body.Bytes(), &tab) != nil {
		t.Fatalf("tab: %d %s", tabW.Code, tabW.Body.String())
	}
	var own, theirs *crewDashboardsTabItem
	for i := range tab.Items {
		switch tab.Items[i].ID {
		case "plan":
			own = &tab.Items[i]
		case "roadmap":
			theirs = &tab.Items[i]
		}
	}
	if own == nil || !own.TeamShared || own.FromTeam || theirs == nil || !theirs.FromTeam || theirs.Title != "Roadmap" || !strings.Contains(theirs.Line, "teammate") {
		t.Fatalf("From the Team: %s", tabW.Body.String())
	}
	if w := action("member", "roadmap", "team_open", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Q1") {
		t.Fatalf("open Team copy: %d %s", w.Code, w.Body.String())
	}

	if w := action("owner", "plan", dashboardTeamAction, "off"); w.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("Team copy still there: %v", err)
	}
}
