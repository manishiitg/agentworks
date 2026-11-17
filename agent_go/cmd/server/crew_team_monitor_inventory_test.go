package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

// #2938: a joined Team is not an owned project. Sidebar inventory must not be
// the monitor's membership source, including a quiet Team without any agents.
func TestCrewTeamMonitorIncludesJoinedTeamsOutsideOwnNavigation(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"editor","products":["work"]},{"id":"member","username":"member","role":"editor","products":["work"]},{"id":"invited","username":"invited","role":"editor","products":["work"]}]}`)
	for _, id := range []string{"joined-a", "joined-b", "joined-c", "joined-d"} {
		if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: id, ProjectID: id, OwnerID: "owner", Shared: true}); err != nil {
			t.Fatal(err)
		}
		team := crewTeam{ProjectID: id, Name: id, OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}, {UserID: "invited", Invited: true}}}
		if !crewTeamReachOpen(team, "member") {
			t.Fatal("fixture must admit the joined member through central reach")
		}
		if err := writeCrewTeamAgentFixture(team); err != nil {
			t.Fatal(err)
		}
	}
	router := mux.NewRouter()
	f.api.registerCrewTeamRoutes(router)
	read := func(user string) crewTeamMonitorReply {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, adminRequest(http.MethodGet, "/crew-team-monitor", "", &UserClaims{UserID: user, Username: user}, nil))
		var out crewTeamMonitorReply
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("monitor: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	out := read("member")
	if len(out.Connections) != 1 || len(out.Connections[0].Teams) != 4 {
		t.Fatalf("joined quiet Teams missing: %+v", out)
	}
	for _, team := range out.Connections[0].Teams {
		if team.ProjectID == "" || team.Name == "" {
			t.Fatal("metadata-less Team returned")
		}
	}
	if out := read("invited"); len(out.Connections[0].Teams) != 0 {
		t.Fatal("invitation granted monitor access")
	}
	// The route must not become a new token capability.
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, adminRequest(http.MethodGet, "/crew-team-monitor", "", &UserClaims{UserID: "owner", Scope: "report-preview"}, nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("scoped token admitted: %d", denied.Code)
	}
}
