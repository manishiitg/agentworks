package server

import (
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// From inside a Crew an agent lists and reaches only its own Crew, its Team and the Crews linked to it, with the agents
// inside them; old workflow projects are not listed (calls: TestCrewSeesWorkflowFunctions). Callers outside a Crew
// (MCP) see every owned Crew (owner, 19 Nov).
func TestAgentDirectoryOtherOwnedCrewsNeedALink(t *testing.T) {
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
	listed := func(here string) map[string]bool {
		t.Helper()
		ctx := ctxA
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
		t.Fatalf("an unlinked Crew must list only itself and the person's own agents in no Crew: %v", got)
	}
	// Not just hidden: a Crew tab sending through AgentBridge is refused.
	tab := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: fixtureUserA}, BridgeHosted: &hostedBridgeBinding{Profile: crewProfileID, Project: "beta-crew", Chat: "beta-main"}, NamedAgentID: "beta-tab", Path: beta}
	alphaAgent := triggerTarget{Kind: triggerCallerUser, NamedAgentID: "alpha-agent"}
	alphaCrew := triggerTarget{Kind: triggerCallerCrew, Path: alpha, CrewID: fixtureCrewID}
	for _, target := range []triggerTarget{alphaAgent, alphaCrew} {
		if err := requireLinkedOwnCrew(ctxA, fixtureUserA, tab, target); err == nil {
			t.Fatalf("an unlinked Crew reached %+v", target)
		}
	}
	// A new message to the person's own attached agent in no Crew is never blocked by a link.
	if err := requireLinkedOwnCrew(ctxA, fixtureUserA, tab, triggerTarget{Kind: triggerCallerUser, NamedAgentID: "own-native"}); err != nil {
		t.Fatalf("a Crew agent could not start a message to the person's own attached agent: %v", err)
	}
	// Another Team's agents stay hidden until that Team's Crew is linked; the Crew's own Team shows.
	otherTeam := map[string]any{"kind": "team_agent", "team_project_id": fixtureCrewID}
	ownTeam := map[string]any{"kind": "team_agent", "team_project_id": "beta-crew"}
	if reach, _ := crewReachFrom(ctxA, beta); reach.keepsRow(otherTeam, nil, nil, nil) || !reach.keepsRow(ownTeam, nil, nil, nil) {
		t.Fatal("an unlinked Crew saw another Team's agent, or not its own Team's")
	}
	// #3663: a link to a project on the other machine shows the person's own agents tagged with it, never another Team's.
	f.put(beta+"/workflow.json", `{"schema_version":1,"id":"beta-crew","label":"Beta","capabilities":{},"linked_remote_projects":[{"project_id":"mac-crew","name":"Mac"}]}`)
	ownOnMac := map[string]any{"kind": "connected_agent", "yours": true, "crew_id": "mac-crew"}
	ownOnMacCopy := map[string]any{"kind": "connected_agent", "yours": true, "crew_id": "mac-crew", "team_project_id": "mac-crew-copy"}
	if reach, _ := crewReachFrom(ctxA, beta); !reach.keepsRow(ownOnMac, nil, nil, nil) || !reach.keepsRow(ownOnMacCopy, nil, nil, nil) || reach.keepsRow(otherTeam, nil, nil, nil) {
		t.Fatal("a linked Mac project's own agents were hidden, or another Team's agent showed")
	}
	// Linking Alpha to Beta opens Alpha, its agents and its Team.
	f.put(beta+"/workflow.json", `{"schema_version":1,"id":"beta-crew","label":"Beta","capabilities":{},"workflow_context_paths":["`+alpha+`"]}`)
	if got := listed(beta); !got[fixtureCrewID] || !got["alpha-agent"] {
		t.Fatalf("a linked Crew and its agents must be listed: %v", got)
	}
	for _, target := range []triggerTarget{alphaAgent, alphaCrew} {
		if err := requireLinkedOwnCrew(ctxA, fixtureUserA, tab, target); err != nil {
			t.Fatalf("a linked Crew was refused: %+v %v", target, err)
		}
	}
	if reach, _ := crewReachFrom(ctxA, beta); !reach.keepsRow(otherTeam, nil, nil, nil) {
		t.Fatal("a linked Crew's Team agent was hidden")
	}
}

// #3663 review: a blank project_id must not skip the connection-bounds check on an ordinary workspace_path reference.
func TestRemoteProjectLinkBlankIDKeepsReferenceCheck(t *testing.T) {
	if id, err := remoteProjectLinkArg(map[string]any{"project_id": "  ", "workspace_path": "Chats/Work/projects/a"}); id != "" || err != nil {
		t.Fatalf("a blank project_id was taken as a link: %q %v", id, err)
	}
	if _, err := remoteProjectLinkArg(map[string]any{"project_id": "mac-crew", "workspace_path": "Chats/Work/projects/a"}); err == nil {
		t.Fatal("a link with a workspace_path was accepted")
	}
	if id, err := remoteProjectLinkArg(map[string]any{"project_id": " mac-crew "}); id != "mac-crew" || err != nil {
		t.Fatalf("a link was refused: %q %v", id, err)
	}
}
