package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// PLAT-746-1030: registry-only Crews are not Teams; real own invites use
// the ordinary MCP handler and produce content-free join receipts on both outcomes.
func TestCrewTeamInvitesSkipUnconfiguredRecordsAndLogAnswers(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner-label","role":"editor","products":["work"]},{"id":"joiner","username":"member-label","role":"editor","products":["work"]}]}`)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	seed := func(team crewTeam) {
		t.Helper()
		if ownerOfCrewProjectID(team.ProjectID) == "" {
			if err := defaultProjectOwners().Register(projectOwnerRecord{Product: crewProfileID, Folder: team.ProjectID, ProjectID: team.ProjectID, OwnerID: team.OwnerID}); err != nil {
				t.Fatal(err)
			}
		}
		macCrewOnlineCopy(t, team.ProjectID)
		if err := writeCrewTeam(team); err != nil {
			t.Fatal(err)
		}
	}
	seed(crewTeam{ProjectID: "registry-only", OwnerID: "owner", Agents: map[string]crewTeamRegisteredAgent{"main": {ID: "main", OwnerID: "owner", Name: "Main"}}})
	seed(crewTeam{ProjectID: "paused", OwnerID: "owner", Name: "Saved Team"})
	seed(crewTeam{ProjectID: "selected-folder", OwnerID: "owner", Folder: crewTeamFolder{ID: "folder", Folders: []string{"docs"}}})
	seed(crewTeam{ProjectID: "team", OwnerID: "owner", Name: "PRIVATE_TITLE_SENTINEL", On: true, People: []crewTeamPerson{{UserID: "joiner", Invited: true}}})
	call := func(user string, args map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		logs.Reset()
		w := httptest.NewRecorder()
		f.api.externalCrewTeamSyncCall(w, adminRequest(http.MethodPost, "/api/external/v1/call", "", &UserClaims{UserID: user, Username: user}, nil), args)
		return w
	}
	w := call("owner", map[string]any{"action": "invites"})
	var listed struct {
		Teams   []crewTeam       `json:"teams"`
		Invites []crewTeamInvite `json:"invites"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	projects := []string{}
	for _, team := range listed.Teams {
		projects = append(projects, team.ProjectID)
	}
	if slices.Contains(projects, "registry-only") || !slices.Contains(projects, "paused") || !slices.Contains(projects, "selected-folder") || !slices.Contains(projects, "team") {
		t.Fatalf("listed Teams: %v", projects)
	}
	if !strings.Contains(logs.String(), fmt.Sprintf("[CREW_TEAM] event=invites_listed actor=owner-label teams=%d invites=0", len(projects))) {
		t.Fatal("missing listed counts", logs.String())
	}
	if _, err := readCrewTeam("registry-only"); err != nil {
		t.Fatal("listing must not delete the registry", err)
	}
	w = call("joiner", map[string]any{"action": "invites"})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Invites) != 1 || listed.Invites[0].ProjectID != "team" {
		t.Fatalf("own invite: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(listed.Invites[0].Notice.Line, "1 people · 0 agents") || listed.Invites[0].Notice.Join == "" || !strings.Contains(listed.Invites[0].Notice.Joined, "available online") {
		t.Fatal("invite must carry server-written counts and online confirmation, excluding pending members and private agents")
	}
	w = call("joiner", map[string]any{"action": "decline", "crew_id": "team"})
	if w.Code != 200 || mustReadCrewTeam(t, "team").invited("joiner") || !strings.Contains(logs.String(), "[CREW_TEAM] event=declined project=team actor=member-label") {
		t.Fatalf("decline: %d %s %s", w.Code, w.Body, logs.String())
	}
	team := mustReadCrewTeam(t, "team")
	team.People = []crewTeamPerson{{UserID: "joiner", Invited: true}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	w = call("joiner", map[string]any{"action": "accept", "crew_id": "team"})
	if w.Code != 200 || !mustReadCrewTeam(t, "team").member("joiner") {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	receipt := logs.String()
	if !strings.Contains(receipt, "event=started") || !strings.Contains(receipt, "event=finished outcome=done status=200 reason=none") || strings.Contains(receipt, "PRIVATE_TITLE_SENTINEL") {
		t.Fatal("accept receipt", receipt)
	}
	// The same caller cannot accept twice; failed receipts retain only the error code.
	w = call("joiner", map[string]any{"action": "accept", "crew_id": "team"})
	if w.Code != 404 || !strings.Contains(logs.String(), "event=finished outcome=failed status=404 reason=invite_not_found") {
		t.Fatalf("refused accept: %d %s %s", w.Code, w.Body, logs.String())
	}
	w = call("joiner", map[string]any{"action": "accept", "crew_id": "PRIVATE_ID_SENTINEL\nforged_log"})
	if w.Code < 400 || strings.Contains(logs.String(), "PRIVATE_ID_SENTINEL") {
		t.Fatal("caller input entered join logs", logs.String())
	}
}
