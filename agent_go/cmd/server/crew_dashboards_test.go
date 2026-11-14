package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
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
		{ID: "report", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: "Workflow/report", Kind: "script"},
		{ID: "foreign", CrewID: "another", CrewRoot: "Crew/another", Root: "Workflow/foreign", Kind: "script"},
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
	binding := crewExecutableBinding{ID: "report", CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: root, Kind: "script"}
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

// PLAT-746-1023: namespaced asset rewriting must not corrupt longer paths.
func TestTeamOnlineAssetRewriteMatchesWholePathTokens(t *testing.T) {
	input := `<img src="db/assets/x.png"><img src="db/assets/x.png?v=1#preview"> url(db/assets/x.png) db/assets/x.png.bak other/db/assets/x.png`
	got := rewriteTeamOnlineAssetToken(input, "db/assets/x.png", "db/assets/item/x.png")
	want := `<img src="db/assets/item/x.png"><img src="db/assets/item/x.png?v=1#preview"> url(db/assets/item/x.png) db/assets/x.png.bak other/db/assets/x.png`
	if got != want {
		t.Fatalf("asset rewrite changed nonmatching paths: %s", got)
	}
}

// PLAT-746-1023: a validated Mac source needs no server-local source root,
// but an admission for another person, item, submission or root grants nothing.
func TestTeamOnlineReceiveRequiresBoundSourceAdmissionNotServerSourceRoot(t *testing.T) {
	folder := t.TempDir()
	root, err := os.OpenRoot(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	otherRoot, err := os.OpenRoot(folder)
	if err != nil {
		t.Fatal(err)
	}
	defer otherRoot.Close()
	source := triggerTarget{CrewOwner: "owner", CrewID: "source", Path: "Crew/not-on-this-server"}
	ref := teamOnlineDashboardRef{ItemID: "report", Kind: "artifact", Revision: strings.Repeat("a", 64), SubmissionID: "submission", Package: root}
	item := teamonline.Item{ID: ref.ItemID, Revision: ref.Revision, Snapshot: dashboards.Snapshot{Title: "Report", Artifact: true, Files: map[string]string{"index.html": "<h1>Report</h1>"}}}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(folder, teamonline.Descriptor), data, 0600); err != nil {
		t.Fatal(err)
	}
	ref.SourceAdmission = &teamOnlineDashboardSourceAdmission{person: "owner", owner: "owner", sourceProjectID: source.CrewID, kind: ref.Kind, itemID: ref.ItemID, revision: ref.Revision, submissionID: ref.SubmissionID, packageRoot: root}
	api := &StreamingAPI{}
	for _, tc := range []struct {
		name   string
		change func(*teamOnlineDashboardRef)
	}{
		{"absent", func(r *teamOnlineDashboardRef) { r.SourceAdmission = nil }},
		{"person", func(r *teamOnlineDashboardRef) { r.SourceAdmission.person = "other" }},
		{"owner", func(r *teamOnlineDashboardRef) { r.SourceAdmission.owner = "other" }},
		{"source", func(r *teamOnlineDashboardRef) { r.SourceAdmission.sourceProjectID = "other" }},
		{"kind", func(r *teamOnlineDashboardRef) { r.SourceAdmission.kind = "dashboard" }},
		{"item", func(r *teamOnlineDashboardRef) { r.SourceAdmission.itemID = "other" }},
		{"revision", func(r *teamOnlineDashboardRef) { r.SourceAdmission.revision = strings.Repeat("b", 64) }},
		{"submission", func(r *teamOnlineDashboardRef) { r.SourceAdmission.submissionID = "other" }},
		{"package", func(r *teamOnlineDashboardRef) { r.Package = otherRoot }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt, admission := ref, *ref.SourceAdmission
			attempt.SourceAdmission = &admission
			tc.change(&attempt)
			if _, err := api.receiveTeamOnlineDashboard(internalBotRequestContext(t.Context(), "owner"), source, triggerTarget{}, attempt, true); err == nil || !strings.Contains(err.Error(), "source item is unavailable") {
				t.Fatalf("mismatched source capability was not refused: %v", err)
			}
		})
	}
	out, err := api.receiveTeamOnlineDashboard(internalBotRequestContext(t.Context(), "owner"), source, triggerTarget{}, ref, true)
	if err != nil || out.DestinationItemID != teamOnlineDashboardItemID(source.CrewID, ref.Kind, ref.ItemID) {
		t.Fatalf("validated Mac source required a server source path: %+v %v", out, err)
	}
}
