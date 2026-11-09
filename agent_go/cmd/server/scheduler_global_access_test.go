package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// PLAT-746-747: the one company-wide pause uses native admin authority and
// connection consent across HTTP, MCP and Crew chat. Project consent alone
// cannot change it. This exercises saved schedules and the real clock refusal,
// not an AI model run or a deployed-server acceptance claim.
func TestGlobalSchedulerPauseKeepsAdminAuthorityAcrossChannels(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","admin":true,"can_create":true,"products":["work"]},{"id":"other","username":"other","role":"editor","products":["work"]}]}`)
	state, err := schedulerstate.Open(filepath.Join(t.TempDir(), "scheduler.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	f.api.scheduler = &SchedulerService{api: f.api, stateStore: state, runtimeStates: map[string]*ScheduleRuntimeState{}}
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(t.Context(), UserContextKey, owner)
	router := mux.NewRouter()
	SchedulerRoutes(router, f.api.scheduler)
	httpConfig := func(claims *UserClaims, method, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, adminRequest(method, "/api/scheduler/config", body, claims, nil))
		return w
	}
	mcp := func(claims *UserClaims, action string, extra map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		args := map[string]any{"scope": "schedule", "action": action}
		for k, v := range extra {
			args[k] = v
		}
		body, _ := json.Marshal(map[string]any{"name": "automations", "arguments": args})
		w := httptest.NewRecorder()
		f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil))
		return w
	}
	mustOK := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != 200 {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
	}
	// Create a real saved Crew timer through the existing MCP adapter.
	mustOK(mcp(owner, "create", map[string]any{"project": "team", "schedule": map[string]any{"name": "Crew timer", "schedule_type": "cron", "cron_expression": "* * * * *", "timezone": "UTC", "enabled": true, "messages": []any{"Return the saved result."}}}))
	child := crewExecutableBinding{ID: "global-pause-workflow", CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/global-pause-workflow", Kind: "script", RuntimeRoot: "workflows/global-pause-workflow"}
	if err := registerCrewExecutable(child); err != nil {
		t.Fatal(err)
	}
	if err := initializeCrewScriptSource(ctx, child, "Example Python workflow"); err != nil {
		t.Fatal(err)
	}
	manifest := NewWorkflowManifest("Example Python workflow")
	manifest.ID, manifest.CreatedBy = child.ID, "owner"
	manifest.Schedules = []WorkflowSchedule{{ID: "workflow-timer", Name: "Workflow timer", Enabled: true, ScheduleType: "cron", CronExpression: "* * * * *", Timezone: "UTC"}}
	if err := WriteWorkflowManifest(ctx, child.Root, manifest); err != nil {
		t.Fatal(err)
	}
	// Retired product settings have no authority and are not remapped into the
	// global switch. The next native save writes only the current schema.
	if err := writeFileToWorkspace(ctx, schedulerConfigFilePath, `{"globally_paused":false,"paused_products":["agentworks"]}`); err != nil {
		t.Fatal(err)
	}
	checkBoth := func(paused bool) {
		t.Helper()
		w := mcp(owner, "list", map[string]any{"project": "team"})
		mustOK(w)
		var crew struct {
			Schedules []ScheduledJobResponse `json:"schedules"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &crew); err != nil || len(crew.Schedules) != 1 {
			t.Fatalf("saved Crew timer missing: %s (%v)", w.Body.String(), err)
		}
		if (crew.Schedules[0].StatusWord == "Paused") != paused || !crew.Schedules[0].Enabled {
			t.Fatalf("Crew timer disagrees with global pause=%v: %+v", paused, crew.Schedules[0])
		}
		w = mcp(owner, "list", map[string]any{"project": child.ID})
		mustOK(w)
		var workflow struct {
			State WorkflowSchedulerState `json:"scheduler_state"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &workflow); err != nil || len(workflow.State.Schedules) != 1 || workflow.State.GloballyPaused == nil {
			t.Fatalf("saved workflow timer missing: %s (%v)", w.Body.String(), err)
		}
		if *workflow.State.GloballyPaused != paused || (workflow.State.Schedules[0].State == "paused") != paused {
			t.Fatalf("workflow timer disagrees with global pause=%v: %+v", paused, workflow.State)
		}
	}
	checkBoth(false)
	mustOK(httpConfig(owner, http.MethodPut, `{"globally_paused":true,"paused_by":"website"}`))
	mustOK(mcp(owner, "scheduler_status", nil)) // no project selector
	checkBoth(true)
	// Exercise the actual due-workflow clock guard and its durable decision.
	sctx := buildScheduleContext(child.Root, manifest, manifest.Schedules[0])
	f.api.scheduler.triggerSchedule(sctx, time.Now().UTC())
	decisions, err := state.ListFireDecisions(ctx, "workflow", child.Root, "workflow-timer", 10)
	if err != nil || len(decisions) != 1 || decisions[0].Decision != "skipped_paused" {
		t.Fatalf("paused clock did not persist its refusal: %+v %v", decisions, err)
	}
	for name, claims := range map[string]*UserClaims{
		"non-admin":       {UserID: "other", Username: "other"},
		"project-consent": {UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read", "crews:write"}, AllCrews: true}},
		"project-bound":   {UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"users:manage", "crews:write"}, ProjectKind: "work", AllCrews: true}},
	} {
		w := mcp(claims, "resume_all", nil)
		if w.Code != 403 || !strings.Contains(w.Body.String(), "administrator") {
			t.Fatalf("%s changed the global pause or got no next step: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := httpConfig(&UserClaims{UserID: "other", Username: "other"}, http.MethodPut, `{"globally_paused":false}`); w.Code != 403 {
		t.Fatalf("non-admin changed native config: %d %s", w.Code, w.Body.String())
	}
	// A proper administrator connection can resume without project consent.
	admin := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"users:manage"}}}
	mustOK(mcp(admin, "resume_all", nil))
	checkBoth(false)
	profile, err := f.api.agentProfiles.Resolve(crewProfileID, 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	reg := &recordingRegistrar{}
	if err := f.api.registerCrewSettingsChatTools(reg, nil, profile, owner, false, "global-pause-chat", externalCommandCrewRoot, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.tools["manage_schedules"]
	if !ok {
		t.Fatal("administrator Crew chat lacks the same schedule controls")
	}
	if _, err := tool.exec(ctx, map[string]any{"action": "pause_all"}); err != nil {
		t.Fatal(err)
	}
	checkBoth(true)
	if _, err := tool.exec(ctx, map[string]any{"action": "resume_all"}); err != nil {
		t.Fatal(err)
	}
	checkBoth(false)
	w := mcp(owner, "list", nil)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "Choose the Crew or workflow") {
		t.Fatalf("project action no longer requires a project: %d %s", w.Code, w.Body.String())
	}
	raw, _, err := readFileFromWorkspace(ctx, schedulerConfigFilePath)
	if err != nil || strings.Contains(raw, "paused_products") {
		t.Fatalf("retired pause configuration survived native save: %s %v", raw, err)
	}
	events := readSchedulerPauseEvents(ctx)
	if len(events) != 4 || events[0].UserID != "owner" || events[0].Action != "paused" || events[1].Action != "resumed" || events[2].Action != "paused" || events[3].Action != "resumed" {
		t.Fatalf("native pause audit did not preserve actor and changes: %+v", events)
	}
}
