package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

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
	// A server that never answers held every poll for its whole wait (17-20 s) and slowed the whole app.
	stop := make(chan struct{})
	var silentReads atomic.Int32
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { silentReads.Add(1); <-stop }))
	defer silent.Close()
	defer close(stop)
	first, refresh := crewTeamMonitorFirstWait, crewTeamMonitorRefreshWait
	crewTeamMonitorFirstWait, crewTeamMonitorRefreshWait = 100*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() {
		crewTeamMonitorFirstWait, crewTeamMonitorRefreshWait = first, refresh
		crewTeamMonitorGoodReads.Clear()
		crewTeamMonitorFailures.Clear()
	})
	if err := writeDesktopConnections("owner", []desktopConnection{{ID: "online", Name: "Example company", Server: online.URL, Token: "saved-sign-in"}, {ID: "offline", Name: "Server", Server: offline.URL, Token: "other-sign-in"}, {ID: "silent", Name: "Quiet company", Server: silent.URL, Token: "third-sign-in"}}); err != nil {
		t.Fatal(err)
	}
	poll := func() crewTeamMonitorReply {
		started := time.Now()
		w := httptest.NewRecorder()
		f.api.handleCrewTeamMonitor(w, desktopTestRequest(http.MethodGet, "/api/crew-team-monitor", "", "owner"))
		var reply crewTeamMonitorReply
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
			t.Fatalf("partial listing failed: %d %s", w.Code, w.Body.String())
		}
		if took := time.Since(started); took > time.Second {
			t.Fatalf("a silent server held the poll for %s", took)
		}
		return reply
	}
	reply := poll()
	// The notice names each server and its reason, not only a count.
	if reply.Notice != "A saved server: Teams didn't load · Quiet company: didn't answer" {
		t.Fatalf("missing named partial result: %+v", reply)
	}
	// The next poll reports the silent server at once instead of waiting on it again, and keeps the listed Team.
	if again := poll(); again.Notice != reply.Notice {
		t.Fatalf("second poll changed: %+v", again)
	}
	if reads := silentReads.Load(); reads > 2 {
		t.Fatalf("every poll waited on the silent server: %d reads", reads)
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
