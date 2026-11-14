package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

// PLAT-746-1026: any Team member adds their own secret to the Team (value copied inside the server, kept by them, never
// shown); a personal sign-in is only listed, never copied; a non-member is refused; the Team's vault follows the roster.
func TestTeamWayAddSharesKeysAndNeverSignIns(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	layout := legacyIdentityLayout()
	layout.CrewPhysical = func(_ string, folder string) string { return "Crew/" + folder }
	layout.CrewShort = func(folder string) string { return "Crew/" + folder }
	f := newMultiUserFixture(t, layout)
	vault := newOwnerVaultFake(t)
	registerTestCrew(t, fixtureCrewFolder, fixtureUserA, true, fixtureCrewID)
	profile := f.Crew
	profile.Version++
	profile.Runtime.Workspace.ProjectsRoot = "Crew"
	if err := f.API.agentProfiles.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: fixtureCrewID, OwnerID: fixtureUserA, Name: "Sales", On: true, People: []crewTeamPerson{{UserID: fixtureUserB}}}); err != nil {
		t.Fatal(err)
	}
	mine := vault.seed(t, f.API, fixtureUserB, "", "API_TOKEN", "s3cret-value")
	post := func(user, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body))).WithContext(f.Ctx(user))
		rec := httptest.NewRecorder()
		f.API.handleCrewTeamWay(rec, mux.SetURLVars(req, map[string]string{"project": fixtureCrewID}))
		return rec
	}
	if rec := post(fixtureUserC, `{"op":"add","kind":"secret","id":"API_TOKEN"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a person outside the Team added to it: %d %s", rec.Code, rec.Body.String())
	}
	rec := post(fixtureUserB, `{"op":"add","kind":"secret","id":"API_TOKEN"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "s3cret-value") {
		t.Fatalf("add secret: %d %s", rec.Code, rec.Body.String())
	}
	team, err := readCrewTeam(fixtureCrewID)
	if err != nil || team.OnlineHome == "" {
		t.Fatal("the first add must make the Team's online home", err)
	}
	way, err := readTeamWay(fixtureCrewID)
	if err != nil || way.Vault == "" {
		t.Fatal("the Team's vault was not recorded", err)
	}
	full, _ := vaultSecretName(way.Vault, "API_TOKEN")
	managedGlobalsMu.RLock()
	copied, kept := managedGlobals[full], managedGlobals[mine]
	managedGlobalsMu.RUnlock()
	if copied != "s3cret-value" || kept != "s3cret-value" || !vault.has(full) {
		t.Fatal("the Team must hold its own copy and the member keeps theirs")
	}
	selected, err := productSelectedGlobalSecrets(f.Ctx(fixtureUserA), crewProfileID, crewProjectRootByID(team.OnlineHome))
	if err != nil || selected == nil || !containsExactString(*selected, full) {
		t.Fatalf("the Team Crew does not use the shared secret: %v %v", selected, err)
	}
	vault.mu.Lock()
	members := vault.members[way.Vault]
	vault.mu.Unlock()
	if !containsExactString(members, fixtureUserA) || !containsExactString(members, fixtureUserB) {
		t.Fatalf("the Team's vault members are not the Team's people: %v", members)
	}

	vault.mu.Lock()
	personal := vault.personal[fixtureUserB]
	vault.connectors["c-aaaaaaaa"] = map[string]string{"ID": "c-aaaaaaaa", "OAuthServer": "github", "OAuthCredentialID": "c-aaaaaaaa", "Label": "GitHub", "VaultID": personal}
	vault.mu.Unlock()
	rec = post(fixtureUserB, `{"op":"add","kind":"app","id":"c-aaaaaaaa"}`)
	var view teamWayView
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || !strings.Contains(view.Notice, "each person signs in themselves") {
		t.Fatalf("add sign-in app: %d %s", rec.Code, rec.Body.String())
	}
	vault.mu.Lock()
	copies := vault.copies
	vault.mu.Unlock()
	if copies != 0 {
		t.Fatal("a personal sign-in was copied to the Team")
	}
}
