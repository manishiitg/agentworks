package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746: discovering a shared dashboard must not expose another Crew or
// initialize a tracking database. Existing metric calls must never write it.
func TestCrewDashboardDiscoveryKeepsProjectAccessWithoutTrackingStore(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnv(t)
	docs, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Progress", IdempotencyKey: "dashboard-access"})
	if err != nil {
		t.Fatal(err)
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"runner","username":"runner","role":"creator"},{"id":"stranger","username":"stranger","role":"creator"}]}`)
	for _, binding := range []crewExecutableBinding{
		{ID: "report", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: "Workflow/report", Kind: "workflow"},
		{ID: "foreign", CrewID: "another", CrewRoot: "Crew/another", Root: "Workflow/foreign", Kind: "workflow"},
	} {
		if err := registerCrewExecutable(binding); err != nil {
			t.Fatal(err)
		}
		files.files[binding.Root+"/workflow.json"] = `{"id":"` + binding.ID + `","label":"Progress report"}`
		files.files[binding.Root+"/db/reports/index.html"] = "<h1>Progress</h1>"
	}
	api := &StreamingAPI{projectActions: svc}
	call := func(user string, want int) string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/crew-dashboards?crew_id="+crew.CrewID, nil).WithContext(internalBotRequestContext(t.Context(), user))
		response := httptest.NewRecorder()
		api.handleCrewDashboards(response, request)
		if response.Code != want {
			t.Fatalf("%s: %d %s", user, response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	body := call("owner", 200)
	if !strings.Contains(body, "Workflow/report") || strings.Contains(body, "foreign") {
		t.Fatal("incorrect admitted catalog", body)
	}
	call("stranger", 403)
	identity, _ := projectIdentityOf(crew.WorkspacePath)
	if err := defaultProjectOwners().modify(func(records map[string]projectOwnerRecord) (bool, error) {
		record := records[identity.Product+"/"+identity.Folder]
		record.Private = true
		record.AudienceConfigured = true
		record.RunPeople = []string{"runner"}
		records[identity.Product+"/"+identity.Folder] = record
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	resetCrewOwnerCache()
	call("runner", 200)
	call("stranger", 403)
	private, err := crewroot.PrivateRoot(docs, crew.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(private, "db/db.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("discovery created a tracking store: %v", err)
	}
}

func TestWorkflowTrackingStaysInWorkflowDBAndRefusesCrewRoots(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnv(t)
	docs, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Data", IdempotencyKey: "tracking-data"})
	if err != nil {
		t.Fatal(err)
	}
	root := "Workflow/report"
	binding := crewExecutableBinding{ID: "report", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: root, Kind: "workflow"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	files.files[root+"/workflow.json"] = `{"id":"report","label":"Progress"}`
	metric := step.GoalMetric{ID: "readings", CriterionID: "readings", Name: "Readings", Role: "primary", Unit: "count", Direction: "increase", Definition: "Observed count", Source: "Report", Window: "instant", CollectionFrequency: "daily", FreshnessHours: 48}
	if err := step.ConfigureGoalMetrics(ctx, root, []step.GoalMetric{metric}); err != nil {
		t.Fatal(err)
	}
	_, executors, _ := createGoalMetricTools()
	record := executors["record_goal_observations"].(func(context.Context, map[string]interface{}) (string, error))
	observation := map[string]interface{}{"metric": "readings", "criterion_id": "readings", "run_id": "capture", "unit": "count", "value": 7, "observed_at": "2026-11-07T00:00:00Z", "evidence": []string{"report"}}
	args := map[string]interface{}{"workspace_path": root, "observations": []interface{}{observation}}
	if _, err := record(ctx, args); err != nil {
		t.Fatal(err)
	}
	ledger, err := step.LoadGoalMeasurements(ctx, root, 10)
	if err != nil || len(ledger.Observations) != 1 {
		t.Fatalf("workflow data: %+v %v", ledger, err)
	}
	if ledger.Observations[0].RunID != "capture" {
		t.Fatal("rewrote source run ID")
	}
	args["workspace_path"] = crew.WorkspacePath
	if _, err := record(ctx, args); err == nil {
		t.Fatal("Crew-root write was accepted")
	}
	private, err := crewroot.PrivateRoot(docs, crew.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(private, "db/db.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("writer created Crew DB: %v", err)
	}
	// Dashboard readers use SQL on this same workflow data, not Goal APIs.
	layout, found, err := crewroot.LookupExecutable(docs, root)
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(layout.Runtime, "db/db.sqlite")); err != nil {
		t.Fatal(err)
	}

}
