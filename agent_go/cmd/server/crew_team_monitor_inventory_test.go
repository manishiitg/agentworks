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

// #3005: one unavailable saved server must not turn successful Team listings
// into a page error. Names and the partial-failure notice are server supplied.
func TestCrewTeamMonitorPartialConnectionsKeepListedTeams(t *testing.T) {
	desktopTestEnvironment(t)
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "false")
	online := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/crew-team-monitor" || r.Header.Get("Authorization") != "Bearer saved-sign-in" {
			http.Error(w, "unauthorized", 401)
			return
		}
		externalJSON(w, crewTeamMonitorReply{Connections: []crewTeamMonitorConnection{{Teams: []crewTeamLiveView{{ProjectID: "joined", Name: "Example", Work: &crewTeamMonitorWork{Lines: []string{}}}}}}})
	}))
	defer online.Close()
	offline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unavailable", 503) }))
	defer offline.Close()
	if err := writeDesktopConnections("owner", []desktopConnection{{ID: "online", Name: "Example company", Server: online.URL, Token: "saved-sign-in"}, {ID: "offline", Name: "Server", Server: offline.URL, Token: "other-sign-in"}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.api.handleCrewTeamMonitor(w, desktopTestRequest(http.MethodGet, "/api/crew-team-monitor", "", "owner"))
	var reply crewTeamMonitorReply
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
		t.Fatalf("partial listing failed: %d %s", w.Code, w.Body.String())
	}
	// The notice names the server and its reason, not only a count.
	if reply.Notice != "A saved server: Teams didn't load" {
		t.Fatalf("missing named partial result: %+v", reply)
	}
	listed := 0
	for _, c := range reply.Connections {
		listed += len(c.Teams)
		if c.Connection == "online" && c.Network != "Example company" {
			t.Fatal("lost saved server name")
		}
		if c.Connection == "offline" && c.Network != "" {
			t.Fatal("generic Server tag retained")
		}
	}
	if listed != 1 {
		t.Fatalf("successful Team disappeared: %+v", reply)
	}
}
