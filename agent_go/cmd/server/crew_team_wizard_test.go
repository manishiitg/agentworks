package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// PLAT-746, PR1730 review: checked agents must override saved Only me choices, including Main.
func TestCrewTeamSetupExplicitAgentChoices(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	ctx := context.Background()
	main := crewOwnerDefaultChatKey("team", "owner")
	selected, hidden := "team"+codeChatSideMarker+"selected", "team"+codeChatSideMarker+"hidden"
	entries := map[string]ProductConversationRecord{}
	for _, key := range []string{main, selected, hidden} {
		entries[productConversationRegistryEntryKey(crewProfileID, key)] = ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: key, SessionID: key + "-session"}
	}
	if err := defaultProductConversationRegistryStore().writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: entries}); err != nil {
		t.Fatal(err)
	}
	previous := crewTeam{ProjectID: "team", OwnerID: "owner", AgentChoices: map[string]map[string]bool{"owner": {"main": false, selected: false, hidden: true}}}
	if err := writeCrewTeam(previous); err != nil {
		t.Fatal(err)
	}
	if agents := crewWizardAgents(ctx, previous); len(agents) != 3 {
		t.Fatalf("fixture must expose Main and both side agents: %+v", agents)
	}
	w := httptest.NewRecorder()
	f.api.handleCrewTeamSetup(w, adminRequest(http.MethodPost, "/api/crew-teams/team/setup", `{"agents":["`+selected+`"]}`, &UserClaims{UserID: "owner", Username: "owner"}, map[string]string{"project": "team"}))
	if w.Code != http.StatusOK {
		t.Fatalf("Finish: %d %s", w.Code, w.Body.String())
	}
	saved, err := readCrewTeam("team")
	if err != nil || !saved.On || !saved.agentInTeam("owner", main) || !saved.agentInTeam("owner", selected) || saved.agentInTeam("owner", hidden) {
		t.Fatalf("Finish did not apply checked and unchecked choices: %+v (%v)", saved.AgentChoices, err)
	}
}

// PLAT-746, PR1730 review: missing-dashboard consent must leave the Team off, then Finish may create and share it.
func TestCrewTeamSetupDashboardConsentBeforeSaving(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","email":"owner@example.com","role":"editor","products":["work"]},{"id":"other","username":"other","email":"other@example.com","role":"editor","products":["work"]}]}`)
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	call := func(user string, create bool) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"people": []map[string]string{{"user_id": "other", "level": crewPeopleDashboards}}, "create_dashboard": create})
		w := httptest.NewRecorder()
		f.api.handleCrewTeamSetup(w, adminRequest(http.MethodPost, "/api/crew-teams/team/setup", string(body), &UserClaims{UserID: user, Username: user}, map[string]string{"project": "team"}))
		return w
	}
	if w := call("other", true); w.Code != http.StatusForbidden {
		t.Fatalf("another person completed owner's setup: %d %s", w.Code, w.Body.String())
	}
	w := call("owner", false)
	if w.Code != http.StatusConflict {
		t.Fatalf("missing dashboard: %d %s", w.Code, w.Body.String())
	}
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil || failure.Error.Code != "dashboard_needed" {
		t.Fatalf("missing creation choice: %s (%v)", w.Body.String(), err)
	}
	before, err := readCrewTeamOrNew("team", "owner")
	if err != nil || !crewTeamIsNew(before, "owner") || crewSharedWithOthers(externalCommandCrewRoot) {
		t.Fatalf("setup changed access before consent: %+v (%v)", before, err)
	}
	listed, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: externalCommandCrewRoot, Action: "list", IncludeDraft: true})
	if err != nil || len(listed.Dashboards) != 0 {
		t.Fatalf("dashboard created before consent: %+v (%v)", listed, err)
	}
	if w = call("owner", true); w.Code != http.StatusOK {
		t.Fatalf("confirmed Finish: %d %s", w.Code, w.Body.String())
	}
	saved, err := readCrewTeam("team")
	if err != nil || !saved.On || !hasPublishedDashboard(context.Background(), externalCommandCrewRoot) {
		t.Fatalf("confirmed Team/dashboard not ready: %+v (%v)", saved, err)
	}
	record, _ := defaultProjectOwners().Lookup("work", "test-team")
	if !slices.Contains(record.RunPeople, "other") || slices.ContainsFunc(saved.People, func(p crewTeamPerson) bool { return p.UserID == "other" }) {
		t.Fatalf("Dashboards only did not retain its distinct access: run people=%v, team people=%v", record.RunPeople, saved.People)
	}
}
