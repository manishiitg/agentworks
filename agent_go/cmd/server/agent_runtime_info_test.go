package server

import (
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"testing"
	"time"
)

// PLAT-856-572: a real pending question wins over a running turn; loss of the
// live connection wins over either. No question text reaches the directory.
func TestAgentActivityUsesTurnAndQuestionStateWithoutText(t *testing.T) {
	api := &StreamingAPI{}
	record := ProductConversationRecord{SessionID: t.Name()}
	if info := api.activityForRecord(record); info.Activity != "Idle" {
		t.Fatal(info)
	}
	api.setSessionBusy(record.SessionID, true)
	if info := api.activityForRecord(record); info.Activity != "Working" {
		t.Fatal(info)
	}
	feedback := virtualtools.GetHumanFeedbackStore()
	id := t.Name() + "-question"
	if err := feedback.CreatePendingRequest(id, "private question", "private context", record.SessionID, nil, true, time.Minute); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = feedback.SubmitResponse(id, "answered") })
	info := api.activityForRecord(record)
	if info.Activity != "Waiting for you" {
		t.Fatal(info)
	}
	row := map[string]interface{}{"online": true}
	projectAgentActivity(row, info.Activity, time.Now().Add(-3*time.Minute))
	if row["activity"] != "Waiting for you" || row["last_active_at"] == nil {
		t.Fatal(row)
	}
	row["online"] = false
	projectAgentActivity(row, info.Activity, info.LastActiveAt)
	if row["activity"] != "Offline" {
		t.Fatal(row)
	}
	if len(row) > 5 {
		t.Fatalf("activity projection contains unexpected data: %v", row)
	}
}

// The richer projection must use the real shared reach gate. A joined
// teammate can see internal organization; invite, Only me and Team off cannot.
func TestAgentRuntimeInfoUsesCanonicalTeamReachForInternalNames(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	_, _ = newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	team := crewTeam{ProjectID: "team", Name: "Internal Crew", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}}, Agents: crewTeamAgentFixtures("team", map[string]map[string]bool{"owner": {"worker": true}})}
	for id, a := range team.Agents {
		a.Place = "mac"
		team.Agents[id] = a
	}
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	info := &agentRuntimeInfo{TeamProject: "team", TeamTab: "worker", CLI: "Codex", ModelID: "example-model", CrewName: "Internal Crew", OwnerStatus: &agentOwnerStatus{LastMessages: []string{"private line"}}}
	if err := saveAgentMessageStore(t.Context(), agentMessageStore{Agents: []namedMessageAgent{{ID: "runtime-source", UserID: "owner", Name: "worker", Attachment: &namedAgentAttachment{ID: "attachment", Provider: "agentworks", ClientBuild: "known-helper-build", DeliveryRoute: appAgentDeliveryRoute, RuntimeInfo: info}}}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	admitted := api.reachableAgentRuntimeInfo(t.Context(), "other")
	if len(admitted) != 1 {
		t.Fatalf("joined teammate was not admitted: %+v", admitted)
	}
	// #2989: an already-resolved empty panel audience cannot fall back to an
	// all-Team discovery. A supplied subset gets the same labels, not more rows.
	if got := api.runtimeInfoForAdmittedReach(t.Context(), nil); len(got) != 0 {
		t.Fatalf("empty panel reach rediscovered agents: %+v", got)
	}
	for address, row := range admitted {
		limited := api.runtimeInfoForAdmittedReach(t.Context(), []crewTeamReachableAgent{row.Reach})
		if len(limited) != 1 || limited[address].Info == nil || limited[address].Info.ModelID != row.Info.ModelID || limited[address].Helper["client_build"] != row.Helper["client_build"] {
			t.Fatalf("panel subset changed runtime labels: %+v", limited)
		}
		if row.Helper["client_build"] != "known-helper-build" {
			t.Fatalf("admitted helper version missing: %#v", row.Helper)
		}
		projected := map[string]interface{}{}
		projectAgentRuntimeInfo(projected, row.Info, true)
		if projected["crew_name"] != "Internal Crew" {
			t.Fatalf("internal names unavailable to teammate: %v", projected)
		}
		if _, ok := projected["owner_status"]; ok {
			t.Fatal("private messages entered the directory")
		}
	}
	for _, scenario := range []string{"invited", "only_me", "off"} {
		team.On = true
		team.People[0].Invited = false
		for id, a := range team.Agents {
			a.InTeam = true
			team.Agents[id] = a
		}
		switch scenario {
		case "invited":
			team.People[0].Invited = true
		case "only_me":
			for id, a := range team.Agents {
				a.InTeam = false
				team.Agents[id] = a
			}
		case "off":
			team.On = false
		}
		if err := writeCrewTeamAgentFixture(team); err != nil {
			t.Fatal(err)
		}
		if leaked := api.reachableAgentRuntimeInfo(t.Context(), "other"); len(leaked) != 0 {
			t.Fatalf("%s exposed internal metadata: %+v", scenario, leaked)
		}
	}
}
