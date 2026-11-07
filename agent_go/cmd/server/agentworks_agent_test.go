package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
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

// One AgentWorks per person however often ensure runs, and a schedule the person paused stays paused (PLAT-825-99).
func TestAgentWorksEnsureKeepsOneAgentAndThePersonsPause(t *testing.T) {
	f := newAgentWorksFixture(t)
	first, err := f.API.ensureAgentWorks(context.Background(), fixtureUserA)
	if err != nil || !first.Created || first.CrewID != agentWorksCrewID(fixtureUserA) || first.ConversationKey == "" {
		t.Fatalf("first ensure = %+v, %v", first, err)
	}
	jobID := projectScheduleJobID(crewProfileID, first.CrewID, "aw-morning")
	ctx := f.Ctx(fixtureUserA)
	if _, err := f.API.projectActions.SetEnabled(ctx, fixtureUserA, jobID, false); err != nil {
		t.Fatalf("the owned Goal should have added the morning roll-up: %v", err)
	}
	second, err := f.API.ensureAgentWorks(context.Background(), fixtureUserA)
	if err != nil || second.Created || second.CrewID != first.CrewID || second.ConversationKey != first.ConversationKey {
		t.Fatalf("second ensure = %+v, %v", second, err)
	}
	if got := f.agentWorksCrews(); got != 1 {
		t.Fatalf("AgentWorks Crews = %d, want 1", got)
	}
	job, err := f.API.projectActions.Job(ctx, fixtureUserA, jobID)
	if err != nil || job.Schedule.Enabled {
		t.Fatalf("paused roll-up must stay paused: %+v %v", job.Schedule, err)
	}
	refs, err := readWorkWorkflowReferences(ctx, first.WorkspacePath)
	if err != nil || len(refs) != 1 || refs[0] != fixtureGoal {
		t.Fatalf("the Goal the person runs is linked once: %v %v", refs, err)
	}
}

// AgentWorks is paused, never deleted; the refusal says how to pause it.
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
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "paused, not deleted") || !strings.Contains(rec.Body.String(), "schedules off") {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	if f.agentWorksCrews() != 1 {
		t.Fatal("AgentWorks was removed")
	}
}

// read_linked_goal rechecks the person's current owner/editor access on every call; being linked is not enough.
func TestReadLinkedGoalNeedsCurrentAccess(t *testing.T) {
	_, _, api := newCrewWorkflowRunTestEnv(t)
	run := func(userID string) (string, error) {
		reg := &recordingRegistrar{}
		if err := api.registerAgentWorksAgentTools(reg, nil, userID, crewWorkflowTestCrewPath); err != nil {
			t.Fatal(err)
		}
		return reg.tools["read_linked_goal"].exec(context.Background(), map[string]interface{}{"workspace_path": "Workflow/test", "action": "runs"})
	}
	if _, err := run("reader"); err == nil || !strings.Contains(err.Error(), "owner or editor") {
		t.Fatalf("a person without access read the Goal: %v", err)
	}
	if out, err := run("owner"); err != nil || !strings.Contains(out, `"schedules"`) {
		t.Fatalf("owner read = %s %v", out, err)

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
	if got := crewIDs(""); !got[fixtureCrewID] || !got["beta-crew"] {
		t.Fatalf("outside a Crew every owned Crew shows: %v", got)
	}
}
