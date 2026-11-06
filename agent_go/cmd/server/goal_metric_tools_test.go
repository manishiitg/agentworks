package server

import (
	"bytes"
	"context"
	"encoding/json"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// PLAT-822: real metric tools and run recording preserve DB/run evidence without
// interpreting scope labels, or even an equal route name, as goal contribution.

// Exercise the real tool -> existing SQLite schema -> Goal measurements response.

func TestGoalMetricToolsRoundTripMultipleOutcomes(t *testing.T) {
	env := newCrewFunctionEnv(t)
	env.mock.mu.Lock()
	env.mock.files["Workflow/metrics/workflow.json"] = `{"id":"metrics","access":{"owners":["owner"]}}`
	env.mock.mu.Unlock()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := "Workflow/metrics"
	if err := os.MkdirAll(filepath.Join(root, ws, "db"), 0755); err != nil {
		t.Fatal(err)
	}
	_, executors, _ := createGoalMetricTools()
	call := func(name string, args map[string]interface{}) (string, error) {
		args["workspace_path"] = ws
		return executors[name].(func(context.Context, map[string]interface{}) (string, error))(context.Background(), args)
	}
	metric := func(id, goal string) map[string]interface{} {
		return map[string]interface{}{"id": id, "criterion_id": id, "name": id, "role": "primary", "goal_id": goal, "goal_name": goal, "unit": "ms", "direction": "decrease", "definition": "p50", "source": "samples", "window": "daily", "collection_frequency": "daily", "freshness_hours": 48}
	}
	latency := metric("voice", "performance")
	cost := metric("cost", "spend")
	english := metric("English", "performance")
	english["role"] = "supporting"
	delete(english, "goal_id")
	delete(english, "goal_name")
	english["supports"] = []string{"voice"}
	english["support_kind"] = "breakdown"
	english["dimensions"] = map[string]string{"language": "English"}
	if _, err := call("configure_goal_metrics", map[string]interface{}{"metrics": []interface{}{latency, cost, english}}); err != nil {
		t.Fatal(err)
	}
	if _, err := call("record_goal_observations", map[string]interface{}{"observations": []interface{}{map[string]interface{}{"metric": "voice", "criterion_id": "voice", "unit": "ms", "run_id": "run-1", "observed_at": "2026-09-12T00:00:00Z", "value": 120, "evidence": []string{"source"}}}}); err != nil {
		t.Fatal(err)
	}
	raw, err := call("get_goal_metrics", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Metrics []struct {
			ID       string   `json:"id"`
			Supports []string `json:"supports"`
		}
		Observations []json.RawMessage
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Metrics) != 3 || len(result.Observations) != 1 {
		t.Fatal("tool dropped outcomes", raw)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/workflow/goal-metrics?workspace_path="+ws, nil)
	(&StreamingAPI{}).handleGetGoalMetrics(response, request)
	var measurements step.GoalMeasurementLedger
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &measurements) != nil || len(measurements.Metrics) != 3 || len(measurements.Observations) != 1 {
		t.Fatalf("Goal measurements response lost saved values: %d %s", response.Code, response.Body.String())
	}
}

// PLAT-746-265: parent-owned goals share evidence across linked workflows,
// never auto-achieve, and retain live Builder/Run and cross-Crew boundaries.
func TestCrewGoalsShareEvidenceWithoutSharingWriteAuthority(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnv(t)
	docs := t.TempDir()
	docs, _ = filepath.EvalSymlinks(docs)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Goals lab", Purpose: "Track outcomes", StepInstruction: "Track real results", IdempotencyKey: "goal-test"})
	if err != nil {
		t.Fatal(err)
	}
	roots, err := crewroot.PrivateRoot(docs, crew.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(docs, crew.WorkspacePath), roots} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"runner","username":"runner","role":"creator"},{"id":"stranger","username":"stranger","role":"creator"}]}`)
	for _, id := range []string{"first", "second", "unlinked"} {
		b := crewExecutableBinding{ID: id, CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: "Workflow/" + id + "-folder", Kind: "workflow"}
		if err := registerCrewExecutable(b); err != nil {
			t.Fatal(err)
		}
		files.files[b.Root+"/workflow.json"] = `{"id":"` + id + `","label":"Example"}`
	}
	api := &StreamingAPI{projectActions: svc}
	endpoint := "/api/crew-goals?crew_id=" + crew.CrewID
	call := func(user, method string, request any, want int) map[string]json.RawMessage {
		t.Helper()
		body, _ := json.Marshal(request)
		r := httptest.NewRequest(method, endpoint, bytes.NewReader(body)).WithContext(internalBotRequestContext(t.Context(), user))
		w := httptest.NewRecorder()
		api.handleCrewGoals(w, r)
		if w.Code != want {
			t.Fatalf("%s %s got %d want %d: %s", user, method, w.Code, want, w.Body.String())
		}
		result := map[string]json.RawMessage{}
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	target := 20.0
	metric := step.GoalMetric{ID: "leads", CriterionID: "qualified", Name: "Qualified leads", Role: "primary", Unit: "leads", Direction: "increase", Definition: "Qualified leads in CRM", Source: "CRM report", Window: "instant", CollectionFrequency: "daily", FreshnessHours: 48, Target: &target}
	goal := step.CrewGoal{ID: "sales", Name: "Grow sales", Outcome: "Reach qualified leads", Status: "active", WorkflowIDs: []string{"first", "second"}}
	save := crewGoalRequest{Action: "save", Goal: goal, Metrics: []step.GoalMetric{metric}}
	call("owner", "POST", save, 200)
	call("owner", "POST", crewGoalRequest{Action: "save", Goal: step.CrewGoal{ID: "launch", Name: "Launch website", Status: "active"}}, 200)
	call("runner", "POST", save, 403)
	call("stranger", "GET", nil, 403)
	invalid := save
	invalid.Goal.WorkflowIDs = []string{"not-this-crew"}
	call("owner", "POST", invalid, 400)
	value := 21.0
	observation := step.GoalObservation{Metric: "leads", CriterionID: "qualified", Unit: "leads", RunID: "collection-1", Value: &value, ObservedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), Evidence: []string{"CRM report row 1"}}
	record := crewGoalRequest{Action: "record", SourceWorkspace: "Workflow/first-folder", Observations: []step.GoalObservation{observation}}
	missing := record
	missing.Observations = []step.GoalObservation{observation}
	missing.Observations[0].Evidence = nil
	call("owner", "POST", missing, 400)
	call("owner", "POST", record, 200)
	call("owner", "POST", record, 200) // duplicate delivery is idempotent
	record.SourceWorkspace = "Workflow/second-folder"
	response := call("owner", "POST", record, 200)
	var ledger step.GoalMeasurementLedger
	json.Unmarshal(response["measurements"], &ledger)
	if len(ledger.Observations) != 2 {
		t.Fatal("workflow source IDs collided", len(ledger.Observations))
	}
	var goals []step.CrewGoal
	json.Unmarshal(response["goals"], &goals)
	var progress map[string]step.CrewGoalProgress
	json.Unmarshal(response["progress"], &progress)
	if len(goals) != 2 || !progress["sales"].TargetsMet || progress["launch"].TargetsMet {
		t.Fatal("goal tracking state", goals, progress)
	}
	for _, g := range goals {
		if g.Status != "active" {
			t.Fatal("evidence auto-achieved goal")
		}
	}
	record.SourceWorkspace = "Workflow/unlinked-folder"
	call("owner", "POST", record, 400)
	record.SourceWorkspace = "Workflow/first-folder"
	different := 22.0
	record.Observations[0].Value = &different
	call("owner", "POST", record, 400)
	save.Goal.Name = "Sales outcome"
	save.Goal.Status = "achieved"
	call("owner", "POST", save, 200)
	loaded, err := step.LoadGoalMeasurements(ctx, "Workflow/second-folder", 100)
	if err != nil || len(loaded.Observations) != 2 || len(loaded.Metrics) != 1 || loaded.Metrics[0].GoalName != "Sales outcome" {
		t.Fatal("parent history/definitions lost", loaded, err)
	}
	if _, err := os.Stat(filepath.Join(docs, "Workflow/second/db/db.sqlite")); !os.IsNotExist(err) {
		t.Fatal("child got a second measurement DB")
	}
	// Atomic semantic refusal must not partially rename the goal or erase history.
	save.Goal.Name = "Wrong rename"
	save.Metrics[0].Unit = "percent"
	call("owner", "POST", save, 400)
	saved, err := step.LoadCrewGoals(ctx, crew.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range saved {
		if g.ID == "sales" && g.Name != "Sales outcome" {
			t.Fatal("partial goal save")
		}
	}
	// Explicitly shared Run access sees evidence, but still cannot change targets.
	identity, _ := projectIdentityOf(crew.WorkspacePath)
	if err := defaultProjectOwners().modify(func(records map[string]projectOwnerRecord) (bool, error) {
		r := records[identity.Product+"/"+identity.Folder]
		r.Private = true
		r.AudienceConfigured = true
		r.RunPeople = []string{"runner"}
		records[identity.Product+"/"+identity.Folder] = r
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	resetCrewOwnerCache()
	call("runner", "GET", nil, 200)
	call("runner", "POST", record, 403)
	call("stranger", "GET", nil, 403)
	_, executors, _ := createGoalMetricTools()
	read := executors["get_goal_metrics"].(func(context.Context, map[string]interface{}) (string, error))
	if _, err := read(internalBotRequestContext(t.Context(), "runner"), map[string]interface{}{"workspace_path": "Workflow/first-folder"}); err != nil {
		t.Fatal("shared measurement tool refused", err)
	}
	if _, err := read(internalBotRequestContext(t.Context(), "stranger"), map[string]interface{}{"workspace_path": "Workflow/first-folder"}); err == nil {
		t.Fatal("tool exposed another Crew")
	}
	observe := executors["record_goal_observations"].(func(context.Context, map[string]interface{}) (string, error))
	// The workflow's native tool hits the same ledger and idempotent source ID.
	args := map[string]interface{}{"workspace_path": "Workflow/first-folder", "observations": []step.GoalObservation{observation}}
	if _, err := observe(internalBotRequestContext(ctx, "owner"), args); err != nil {
		t.Fatal("workflow record tool refused", err)
	}
	if _, err := observe(internalBotRequestContext(t.Context(), "runner"), args); err == nil {
		t.Fatal("Run person changed measurements")
	}
	// PLAT-746-336: scheduled native steps arrive through their registered
	// bridge with no HTTP claims. Keep the authenticated parent's identity.
	_, childExecutors, categories := createGoalMetricTools()
	parent, child := "metrics-parent", "metrics-child"
	mcpclient.GetSessionRegistry().RegisterHTTPSession(parent, child)
	t.Cleanup(func() { mcpclient.GetSessionRegistry().CloseHTTPSession(parent) })
	bindWorkflowStepIdentity(internalBotRequestContext(ctx, "owner"), parent, childExecutors, categories)
	childRead := childExecutors["get_goal_metrics"].(func(context.Context, map[string]interface{}) (string, error))
	childObserve := childExecutors["record_goal_observations"].(func(context.Context, map[string]interface{}) (string, error))
	childCtx := executor.WithSessionID(t.Context(), child)
	if _, err := childRead(childCtx, args); err != nil {
		t.Fatal("registered child cannot read tracking", err)
	}
	if _, err := childObserve(childCtx, args); err != nil {
		t.Fatal("registered child cannot record tracking", err)
	}
	if _, err := childRead(executor.WithSessionID(t.Context(), "unrelated-child"), args); err == nil {
		t.Fatal("unrelated session inherited tracking authority")
	}
	if _, err := childObserve(internalBotRequestContext(childCtx, "runner"), args); err == nil {
		t.Fatal("Run caller inherited owner measurement authority")
	}
	reg := &chatPolicyTestDefinition{}
	if err := api.registerCrewGoalTools(reg, "owner", crew.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.tools["crew_goals"].exec(ctx, map[string]interface{}{"action": "list"}); err != nil {
		t.Fatal("Crew goals tool unavailable", err)
	}
	// Team membership grants Crew authoring only after joining and while on;
	// the goal HTTP path must use that live authority, not a saved owner list.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	save.Goal.Name = "Sales outcome"
	save.Metrics[0].Unit = "leads"
	for _, state := range []struct {
		on, invited bool
		status      int
	}{{true, true, 403}, {true, false, 200}, {false, false, 403}} {
		if err := writeCrewTeam(crewTeam{ProjectID: crew.CrewID, OwnerID: "owner", On: state.on, People: []crewTeamPerson{{UserID: "stranger", Invited: state.invited}}}); err != nil {
			t.Fatal(err)
		}
		call("stranger", "POST", save, state.status)
	}

	// Freshness and unavailable readings cannot be converted into success.
	active := step.CrewGoal{MetricIDs: []string{"leads"}}
	old := ledger.Observations[0]
	old.ObservedAt = time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	stale := step.GoalMeasurementLedger{Metrics: ledger.Metrics, Observations: []step.GoalObservation{old}}
	if p := step.SummarizeCrewGoal(active, &stale, time.Now()); p.TargetsMet || p.State != "Needs fresh measurements" {
		t.Fatal("stale success", p)
	}
	old.Value = nil
	old.Status = "unavailable"
	stale.Observations = []step.GoalObservation{old}
	if p := step.SummarizeCrewGoal(active, &stale, time.Now()); p.TargetsMet || p.State != "Measurement needed" {
		t.Fatal("unknown treated as zero/success", p)
	}
}
