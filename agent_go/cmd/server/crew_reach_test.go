package server

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// From inside a Crew an agent lists and reaches only its own Crew, its Team, and any Crew tagged with # in the
// current turn (with the agents inside them); old workflow projects are not listed (calls: TestCrewSeesWorkflowFunctions).
// Callers outside a Crew (MCP) see every owned Crew.
func TestAgentDirectoryOtherOwnedCrewsNeedHashReference(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	f.WithSharing(true)
	registry := agentprofiles.NewRegistry()
	crew := f.Crew
	crew.UIPanels.Schedules = true
	for _, profile := range []agentprofiles.Profile{f.Code, crew} {
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	f.API.agentProfiles = registry
	f.API.projectActions = NewProjectActionDispatcher(f.API, registry)
	alpha := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	beta := f.Layout.CrewPhysical(fixtureUserA, "beta-folder")
	f.put(beta+"/product.json", `{"schema_version":1,"product":"work","id":"beta-crew","title":"Beta","session_id":"work:project:beta-crew","owner_id":"`+fixtureUserA+`"}`)
	f.put(beta+"/workflow.json", `{"schema_version":1,"id":"beta-crew","label":"Beta","capabilities":{}}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "beta-folder", OwnerID: fixtureUserA, ProjectID: "beta-crew", Shared: true}); err != nil {
		t.Fatal(err)
	}
	// An agent inside Alpha: an app chat registered from the person's Mac.
	agentMessageMu.Lock()
	store, err := readAgentMessageStore(t.Context())
	if err == nil {
		store.Agents = append(store.Agents, namedMessageAgent{ID: "alpha-agent", UserID: fixtureUserA, Name: "alpha-helper", CreatedAt: time.Now().UTC(),
			Attachment: &namedAgentAttachment{ID: "attach-alpha", Provider: "agentworks", RuntimeInfo: &agentRuntimeInfo{CrewID: fixtureCrewID}}})
		// The person's own attached terminal chat in no Crew (a reviewer they message from every Crew).
		store.Agents = append(store.Agents, namedMessageAgent{ID: "own-native", UserID: fixtureUserA, Name: "reviewer", CreatedAt: time.Now().UTC(),
			Attachment: &namedAgentAttachment{ID: "attach-native", Provider: "claude-code", RuntimeInfo: &agentRuntimeInfo{}}})
		err = saveAgentMessageStore(t.Context(), store)
	}
	agentMessageMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ctxA := f.Ctx(fixtureUserA)
	listed := func(here string, turnRefs ...string) map[string]bool {
		t.Helper()
		ctx := withTurnReachReferences(ctxA, turnRefs)
		if here != "" {
			ctx = withDirectoryThisCrew(ctx, here)
		}
		d, err := listAgentDirectory(ctx, GetUserFromContext(ctx))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, row := range d["agents"].([]map[string]interface{}) {
			got["kind:"+stringField(row["kind"])] = true
			if row["kind"] == "crew" {
				got[stringField(row["crew_id"])] = true
			}
			got[stringField(row["agent_id"])] = true
		}
		return got
	}
	if got := listed(""); !got[fixtureCrewID] || !got["alpha-agent"] || got["kind:goal"] {
		t.Fatalf("outside a Crew every owned Crew shows, and no Goal rows: %v", got)
	}
	if got := listed(beta); got[fixtureCrewID] || got["alpha-agent"] || !got["beta-crew"] || got["kind:goal"] || !got["own-native"] {
		t.Fatalf("without a # reference a Crew must list only itself and the person's own agents in no Crew: %v", got)
	}
	// Not just hidden: a Crew tab sending through AgentBridge is refused without a # tag.
	tab := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: fixtureUserA}, BridgeHosted: &hostedBridgeBinding{Profile: crewProfileID, Project: "beta-crew", Chat: "beta-main"}, NamedAgentID: "beta-tab", Path: beta}
	alphaAgent := triggerTarget{Kind: triggerCallerUser, NamedAgentID: "alpha-agent"}
	alphaCrew := triggerTarget{Kind: triggerCallerCrew, Path: alpha, CrewID: fixtureCrewID}
	for _, target := range []triggerTarget{alphaAgent, alphaCrew} {
		if err := requireLinkedOwnCrew(ctxA, fixtureUserA, tab, target); err == nil {
			t.Fatalf("an untagged Crew reached %+v", target)
		}
	}
	// A new message to the person's own attached agent in no Crew is never blocked.
	if err := requireLinkedOwnCrew(ctxA, fixtureUserA, tab, triggerTarget{Kind: triggerCallerUser, NamedAgentID: "own-native"}); err != nil {
		t.Fatalf("a Crew agent could not start a message to the person's own attached agent: %v", err)
	}
	// Another Team's agents stay hidden unless that Team's Crew is tagged with #; the Crew's own Team shows.
	otherTeam := map[string]any{"kind": "team_agent", "team_project_id": fixtureCrewID}
	ownTeam := map[string]any{"kind": "team_agent", "team_project_id": "beta-crew"}
	if reach, _ := crewReachFrom(ctxA, beta); reach.keepsRow(otherTeam, nil, nil, nil) || !reach.keepsRow(ownTeam, nil, nil, nil) {
		t.Fatal("an untagged Crew saw another Team's agent, or not its own Team's")
	}
	// Stale workflow_context_paths or linked_remote_projects in workflow.json no longer grant durable reach.
	f.put(beta+"/workflow.json", `{"schema_version":1,"id":"beta-crew","label":"Beta","capabilities":{},"workflow_context_paths":["`+alpha+`"],"linked_remote_projects":[{"project_id":"mac-crew","name":"Mac"}]}`)
	if got := listed(beta); got[fixtureCrewID] || got["alpha-agent"] {
		t.Fatalf("stale durable links in workflow.json must not grant reach: %v", got)
	}
	// Tagging Alpha with # in the current turn opens Alpha, its agents, and its Team for this turn only.
	ctxTagged := withTurnReachReferences(ctxA, []string{alpha})
	if got := listed(beta, alpha); !got[fixtureCrewID] || !got["alpha-agent"] {
		t.Fatalf("a #-referenced Crew and its agents must be listed in that turn: %v", got)
	}
	for _, target := range []triggerTarget{alphaAgent, alphaCrew} {
		if err := requireLinkedOwnCrew(ctxTagged, fixtureUserA, tab, target); err != nil {
			t.Fatalf("a #-referenced Crew was refused: %+v %v", target, err)
		}
	}
	if reach, _ := crewReachFrom(ctxTagged, beta); !reach.keepsRow(otherTeam, nil, nil, nil) {
		t.Fatal("a #-referenced Crew's Team agent was hidden")
	}
	// Verify bindToolExecutionContext tracks the live turn's # references across turns on a reused session.
	const sid = "beta-reach-turn-test"
	f.API.lastQueryRequests = map[string]QueryRequest{}
	bind := f.API.bindToolExecutionContext(ctxA, sid, QueryRequest{AgentProfileID: "work", AgentProfileConversationKey: "beta-crew", SelectedFolder: beta}, false)
	f.API.rememberLastQueryRequest(sid, QueryRequest{AgentProfileID: "work", AgentProfileConversationKey: "beta-crew", SelectedFolder: beta, authorizedWorkflowContextReadPaths: []string{alpha}})
	turn1Ctx, err := bind(t.Context(), "send_message")
	if err != nil {
		t.Fatalf("turn 1 bind: %v", err)
	}
	if err := requireLinkedOwnCrew(turn1Ctx, fixtureUserA, tab, alphaAgent); err != nil {
		t.Fatalf("turn 1 with # reference refused: %v", err)
	}
	f.API.rememberLastQueryRequest(sid, QueryRequest{AgentProfileID: "work", AgentProfileConversationKey: "beta-crew", SelectedFolder: beta})
	turn2Ctx, err := bind(t.Context(), "send_message")
	if err != nil {
		t.Fatalf("turn 2 bind: %v", err)
	}
	if err := requireLinkedOwnCrew(turn2Ctx, fixtureUserA, tab, alphaAgent); err == nil {
		t.Fatal("turn 2 without # reference retained prior turn reach")
	}
}
