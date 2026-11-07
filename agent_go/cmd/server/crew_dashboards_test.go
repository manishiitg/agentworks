package server

import (
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
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Progress", Purpose: "Review progress", IdempotencyKey: "dashboard-access"})
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

func TestWorkflowMeasurementReadRefusesCrewRootsAndCreatesNothing(t *testing.T) {
	svc, files, ctx := newCrewCreationTestEnv(t)
	docs, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Data", Purpose: "Record progress", IdempotencyKey: "tracking-data"})
	if err != nil {
		t.Fatal(err)
	}
	root := "Workflow/report"
	binding := crewExecutableBinding{ID: "report", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: root, Kind: "workflow"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	files.files[root+"/workflow.json"] = `{"id":"report","label":"Progress"}`
	ledger, err := step.LoadGoalMeasurements(ctx, root, 10)
	if err != nil || len(ledger.Observations) != 0 {
		t.Fatalf("workflow data: %+v %v", ledger, err)
	}
	if _, err := step.LoadGoalMeasurements(ctx, crew.WorkspacePath, 10); err == nil {
		t.Fatal("Crew-root read was accepted")
	}
	private, err := crewroot.PrivateRoot(docs, crew.CrewID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(private, "db/db.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("reader created Crew DB: %v", err)
	}
}
