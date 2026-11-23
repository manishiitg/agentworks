package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// PLAT-746-1026: any Team member adds their own secret to the Team (value copied inside the server, kept by them, never
// shown); a personal sign-in is only listed, never copied; a non-member is refused; the Team's vault follows the roster.
func TestTeamWayAddSharesKeysAndNeverSignIns(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	layout := legacyIdentityLayout()
	layout.CrewPhysical = func(_ string, folder string) string { return "Crew/" + folder }
	layout.CrewShort = func(folder string) string { return "Crew/" + folder }
	f := newMultiUserFixture(t, layout)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.API.chatStore = store
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
	// Members' agents pick the shared secret up in the Team's own Crew without it being saved there, and lose it when removed.
	source := crewProjectRootByID(fixtureCrewID)
	if picked, err := productSelectedGlobalSecrets(f.Ctx(fixtureUserB), crewProfileID, source); err != nil || !containsExactString(*picked, full) {
		t.Fatalf("a member's agent does not use the Team's secret: %v %v", picked, err)
	}
	if rec := post(fixtureUserB, `{"op":"remove","kind":"secret","id":"API_TOKEN"}`); rec.Code != http.StatusOK {
		t.Fatalf("remove secret: %d %s", rec.Code, rec.Body.String())
	}
	if picked, err := productSelectedGlobalSecrets(f.Ctx(fixtureUserB), crewProfileID, source); err != nil || containsExactString(*picked, full) || vault.has(full) {
		t.Fatalf("a removed Team secret is still used: %v %v", picked, err)
	}
	if rec := post(fixtureUserB, `{"op":"add","kind":"secret","id":"API_TOKEN"}`); rec.Code != http.StatusOK {
		t.Fatalf("re-add secret: %d %s", rec.Code, rec.Body.String())
	}
	vault.mu.Lock()
	members := vault.members[way.Vault]
	vault.mu.Unlock()
	if !containsExactString(members, fixtureUserA) || !containsExactString(members, fixtureUserB) {
		t.Fatalf("the Team's vault members are not the Team's people: %v", members)
	}

	vault.mu.Lock()
	personal := vault.personal[fixtureUserB]
	vault.connectors["c-aaaaaaaa"] = map[string]string{"ID": "c-aaaaaaaa", "OAuthServer": "github", "OAuthCredentialID": "c-aaaaaaaa", "Label": "Priya personal GitHub", "VaultID": personal}
	vault.mu.Unlock()
	rec = post(fixtureUserB, `{"op":"add","kind":"app","id":"c-aaaaaaaa"}`)
	var view teamWayView
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || !strings.Contains(view.Notice, "each person signs in themselves") {
		t.Fatalf("add sign-in app: %d %s", rec.Code, rec.Body.String())
	}
	// The sign-in need shows who added it and the asker's own connected flag (not the member's personal label).
	foundNeed := false
	for _, item := range view.Items {
		if item.Kind == "needs_sign_in" {
			foundNeed = item.AddedBy != "" && item.Connected != nil && !*item.Connected && !strings.Contains(item.Name, "Priya")
		}
	}
	if !foundNeed {
		t.Fatalf("sign-in need lacks who added it or the connected flag: %+v", view.Items)
	}
	vault.mu.Lock()
	copies := vault.copies
	vault.mu.Unlock()
	if copies != 0 {
		t.Fatal("a personal sign-in was copied to the Team")
	}

	// A key that lives only in a member's Mac Vault: the Mac sends its value in the request body and the Team's vault
	// stores it; the value is never returned, a web page cannot send one, and an existing Team key is not overwritten
	// without replace.
	send := func(body string, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body))).WithContext(f.Ctx(fixtureUserB))
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		rec := httptest.NewRecorder()
		f.API.handleCrewTeamWay(rec, mux.SetURLVars(req, map[string]string{"project": fixtureCrewID}))
		return rec
	}
	if rec := send(`{"op":"add","kind":"secret","id":"MAC_ONLY","value":"mac-value"}`, "Origin", "https://app.example.com"); rec.Code != http.StatusForbidden {
		t.Fatalf("a web page sent a key value: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(`{"op":"remove","kind":"secret","id":"MAC_ONLY","value":"mac-value"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a value was accepted on a remove: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(`{"op":"add","kind":"secret","id":"MAC_ONLY","value":"mac-value"}`); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "mac-value") {
		t.Fatalf("add a key sent from a Mac: %d %s", rec.Code, rec.Body.String())
	}
	macFull, _ := vaultSecretName(way.Vault, "MAC_ONLY")
	managedGlobalsMu.RLock()
	stored := managedGlobals[macFull]
	managedGlobalsMu.RUnlock()
	if stored != "mac-value" || !vault.has(macFull) {
		t.Fatal("the Team's vault did not store the key sent from the Mac")
	}
	if rec := send(`{"op":"add","kind":"secret","id":"MAC_ONLY","value":"other-value"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "already on the Team") {
		t.Fatalf("an existing Team key was changed without replace: %d %s", rec.Code, rec.Body.String())
	}
	if rec := send(`{"op":"add","kind":"secret","id":"MAC_ONLY","value":"other-value","replace":true}`); rec.Code != http.StatusOK {
		t.Fatalf("replace a Team key: %d %s", rec.Code, rec.Body.String())
	}

	// The Mac's side: the value is read from the Mac's own Vault and sent straight to the server; an agent-supplied value
	// is never used, and the reply carries the server's answer only.
	vault.seed(t, f.API, fixtureUserB, "", "MAC_KEY", "mac-local-value")
	var sent []byte
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sent, _ = io.ReadAll(r.Body)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"notice":"MAC_KEY is on the Team now."}`))}, nil
	})
	macCtx := context.WithValue(f.Ctx(fixtureUserB), crewTeamTransportKey{}, http.RoundTripper(rt))
	conn := desktopConnection{Server: "https://server.example.com", Token: "t"}
	reply, err := f.API.crewTeamWayRemote(macCtx, fixtureUserB, conn, fixtureCrewID, "way_add", map[string]any{"item": "secret", "id": "MAC_KEY", "value": "agent-supplied"})
	if err != nil || strings.Contains(reply, "mac-local-value") || strings.Contains(reply, "agent-supplied") {
		t.Fatalf("Mac add: %v %s", err, reply)
	}
	var body map[string]any
	if json.Unmarshal(sent, &body) != nil || body["value"] != "mac-local-value" {
		t.Fatalf("the Mac did not send its own Vault's value: %s", sent)
	}
	sent = nil
	if _, err := f.API.crewTeamWayRemote(macCtx, fixtureUserB, conn, fixtureCrewID, "way_add", map[string]any{"item": "secret", "id": "NOT_ON_THIS_MAC", "value": "agent-supplied"}); err != nil || strings.Contains(string(sent), "agent-supplied") || strings.Contains(string(sent), `"value"`) {
		t.Fatalf("a key this Mac does not have carried a value: %v %s", err, sent)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
