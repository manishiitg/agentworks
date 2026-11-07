package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

func newAgentWorksFixture(t *testing.T) *multiUserFixture {
	t.Helper()
	f := newMultiUserFixture(t, legacyIdentityLayout())
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
	return f
}

func (f *multiUserFixture) agentWorksCrews() int {
	f.Mock.mu.Lock()
	defer f.Mock.mu.Unlock()
	count := 0
	for path, content := range f.Mock.files {
		if strings.HasSuffix(path, "/product.json") && strings.Contains(content, `"role": "agentworks"`) {
			count++
		}
	}
	return count
}

// One AgentWorks per person however often ensure runs, and it starts with no routine of its own: no schedules, no
// linked Goals.
func TestAgentWorksEnsureKeepsOneAgentWithNoRoutine(t *testing.T) {
	f := newAgentWorksFixture(t)
	first, err := f.API.ensureAgentWorks(context.Background(), fixtureUserA)
	if err != nil || !first.Created || first.CrewID != agentWorksCrewID(fixtureUserA) || first.ConversationKey == "" {
		t.Fatalf("first ensure = %+v, %v", first, err)
	}
	second, err := f.API.ensureAgentWorks(context.Background(), fixtureUserA)
	if err != nil || second.Created || second.CrewID != first.CrewID || second.ConversationKey != first.ConversationKey {
		t.Fatalf("second ensure = %+v, %v", second, err)
	}
	if got := f.agentWorksCrews(); got != 1 {
		t.Fatalf("AgentWorks Crews = %d, want 1", got)
	}
	ctx := f.Ctx(fixtureUserA)
	if schedules, err := readProjectSchedules(ctx, first.WorkspacePath); err != nil || len(schedules) != 0 {
		t.Fatalf("AgentWorks made itself a routine: %v %v", schedules, err)
	}
	if refs, err := readWorkWorkflowReferences(ctx, first.WorkspacePath); err != nil || len(refs) != 0 {
		t.Fatalf("AgentWorks linked Goals on its own: %v %v", refs, err)
	}
}

// The one-off retirement removes only what AgentWorks made for itself, once: its three built-in schedules and the
// earlier per-Goal tab checks, the Goals it linked and the wake_tab it set. A schedule the person made stays, and
// so does a wake_tab pointing anywhere else.
func TestAgentWorksRetirementRemovesOnlyItsOwnRoutineOnce(t *testing.T) {
	f := newAgentWorksFixture(t)
	crew, err := f.API.ensureAgentWorks(context.Background(), fixtureUserA)
	if err != nil {
		t.Fatal(err)
	}
	ctx := f.Ctx(fixtureUserA)
	for _, id := range []string{"aw-morning", "aw-evening", "aw-daily", "aw-tab-goal-1", "mine"} {
		schedule := productschedule.Schedule{ID: id, Name: id, Enabled: true, CronExpression: "45 9 * * *", Timezone: "UTC", Messages: []string{"hello"}}
		if _, err := f.API.projectActions.CreateProjectSchedule(ctx, fixtureUserA, crewProfileID, crew.CrewID, schedule); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	if err := updateGoalWakeTab(ctx, fixtureGoal, &WorkflowWakeTab{CrewID: crew.CrewID, ChatID: agentWorksMainChatID}); err != nil {
		t.Fatal(err)
	}
	if err := writeAgentWorksState(ctx, crew.WorkspacePath, agentWorksState{Goals: map[string]*agentWorksGoalEntry{"goal-1": {Path: fixtureGoal}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.API.retireAgentWorksGoalWatchingFor(context.Background(), fixtureUserA); err != nil {
		t.Fatal(err)
	}
	remaining := []string{}
	schedules, err := readProjectSchedules(ctx, crew.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, schedule := range schedules {
		remaining = append(remaining, schedule.ID)
	}
	if len(remaining) != 1 || remaining[0] != "mine" {
		t.Fatalf("schedules after retirement = %v, want only the person's own", remaining)
	}
	if manifest, _, err := ReadWorkflowManifest(ctx, fixtureGoal); err != nil || manifest.Capabilities.Notifications != nil && manifest.Capabilities.Notifications.WakeTab != nil {
		t.Fatalf("the wake_tab AgentWorks set was not cleared: %v", err)
	}
	state := readAgentWorksState(ctx, crew.WorkspacePath)
	if state.GoalWatchRetired == "" || len(state.Goals) != 0 {
		t.Fatalf("state after retirement = %+v", state)
	}
	if _, found, err := readFileFromWorkspace(ctx, crew.WorkspacePath+"/"+agentWorksRetirementBackup); err != nil || !found {
		t.Fatalf("no copy of the old state and schedules was kept: %v %v", found, err)
	}
	// Done once: a schedule with a retired id made later is left alone.
	if _, err := f.API.projectActions.CreateProjectSchedule(ctx, fixtureUserA, crewProfileID, crew.CrewID, productschedule.Schedule{ID: "aw-morning", Name: "mine again", Enabled: true, CronExpression: "45 9 * * *", Timezone: "UTC", Messages: []string{"hello"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.API.retireAgentWorksGoalWatchingFor(context.Background(), fixtureUserA); err != nil {
		t.Fatal(err)
	}
	if schedules, _ := readProjectSchedules(ctx, crew.WorkspacePath); len(schedules) != 2 {
		t.Fatalf("a second run removed a schedule: %d", len(schedules))
	}
}

// AgentWorks cannot be deleted; the refusal says why there is nothing to pause.
func TestAgentWorksDeleteIsRefused(t *testing.T) {
	f := newAgentWorksFixture(t)
	crew, err := f.API.ensureAgentWorks(context.Background(), fixtureUserA)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/agent-profiles/work/projects/"+crew.CrewID, nil).WithContext(f.Ctx(fixtureUserA))
	req = mux.SetURLVars(req, map[string]string{"id": crewProfileID, "project_id": crew.CrewID})
	rec := httptest.NewRecorder()
	f.API.handleDeleteAgentProfileProject(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "can't be deleted") {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if f.agentWorksCrews() != 1 {
		t.Fatal("AgentWorks was removed")
	}
}

// From inside a Crew, list_agents shows the person's other Crews only when linked, except for their AgentWorks main
// agent, which reaches all of them (owner, 7 Nov). The server decides by the Crew's role.
func TestAgentDirectoryOtherOwnedCrewsNeedALinkExceptForAgentWorks(t *testing.T) {
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
	aw, err := f.API.ensureAgentWorks(context.Background(), fixtureUserA)
	if err != nil {
		t.Fatal(err)
	}
	beta := f.Layout.CrewPhysical(fixtureUserA, "beta-folder")
	f.put(beta+"/product.json", `{"schema_version":1,"product":"work","id":"beta-crew","title":"Beta","session_id":"work:project:beta-crew","owner_id":"`+fixtureUserA+`"}`)
	f.put(beta+"/workflow.json", `{"schema_version":1,"id":"beta-crew","label":"Beta","capabilities":{}}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "beta-folder", OwnerID: fixtureUserA, ProjectID: "beta-crew", Shared: true}); err != nil {
		t.Fatal(err)
	}
	crewIDs := func(here string) map[string]bool {
		ctx := f.Ctx(fixtureUserA)
		if here != "" {
			ctx = withDirectoryThisCrew(ctx, here)
		}
		d, err := listAgentDirectory(ctx, GetUserFromContext(ctx))
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, row := range d["agents"].([]map[string]interface{}) {
			if row["kind"] == "crew" {
				id, _ := row["crew_id"].(string)
				ids[id] = true
			}
		}
		return ids
	}
	if got := crewIDs(aw.WorkspacePath); !got[fixtureCrewID] || !got["beta-crew"] {
		t.Fatalf("AgentWorks must list the person's Crews: %v", got)
	}
	if got := crewIDs(beta); got[fixtureCrewID] || !got["beta-crew"] {
		t.Fatalf("an unlinked Crew must list only itself: %v", got)
	}
	// Not just hidden: a message or function call from an unlinked Crew to another Crew of the same person is refused,
	// while the AgentWorks main agent may.
	ctxA := f.Ctx(fixtureUserA)
	alphaTarget := triggerTarget{Kind: triggerCallerCrew, Path: f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder), CrewID: fixtureCrewID}
	callerFrom := func(path, id string) triggerLinkCaller {
		return triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: id}, Path: path}
	}
	if err := requireLinkedOwnCrew(ctxA, fixtureUserA, callerFrom(beta, "beta-crew"), alphaTarget); err == nil {
		t.Fatal("an unlinked Crew reached another Crew of the same person")
	}
	if err := requireLinkedOwnCrew(ctxA, fixtureUserA, callerFrom(aw.WorkspacePath, aw.CrewID), alphaTarget); err != nil {
		t.Fatalf("AgentWorks must reach the person's Crews: %v", err)
	}
	if got := crewIDs(""); !got[fixtureCrewID] || !got["beta-crew"] {
		t.Fatalf("outside a Crew every owned Crew shows: %v", got)
	}
}
