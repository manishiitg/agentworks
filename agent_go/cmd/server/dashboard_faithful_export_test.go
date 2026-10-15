package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

func TestDashboardExportPublicationAndRetryIdentity(t *testing.T) {
	newExternalToolsFixture(t)
	ctx := context.Background()
	created, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: "Workflow/invoices", Action: "create", ID: "scores", Title: "Scores", Files: map[string]string{"index.html": "<!doctype html><p>96</p>"}, IncludeDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	input, identity, err := normalizeDashboardExport(dashboardExportOptions{Dashboard: "scores", Theme: "light"}, "")
	if err != nil || input.SourceMode != "dashboard" {
		t.Fatalf("default source: %+v %v", input, err)
	}
	if _, err := resolveDashboardExportSource(ctx, dashboardTarget{Root: "Workflow/invoices"}, input); err == nil {
		t.Fatal("export admitted an unpublished draft")
	}
	_, err = dashboardWorkspaceRequest(ctx, dashboards.Request{Root: "Workflow/invoices", Action: "publish", ID: "scores", ExpectedRevision: created.Dashboard.Revision, IncludeDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveDashboardExportSource(ctx, dashboardTarget{Root: "Workflow/invoices"}, input)
	if err != nil || resolved.Revision != created.Dashboard.Revision || !strings.Contains(resolved.Document, created.Dashboard.Revision) {
		t.Fatalf("lost exact publication: %+v %v", resolved, err)
	}
	_, repeat, _ := normalizeDashboardExport(input, "")
	if identity != repeat {
		t.Fatal("retry identity depends on mutable publication state")
	}
	_, legacy, err := normalizeDashboardExport(dashboardExportOptions{}, "code/motion/explainer")
	if err != nil || legacy != "code/motion/explainer" {
		t.Fatal("legacy explicit composition compatibility changed")
	}
}

// Run only from the browser verification script. It starts the real workspace
// file store, JWT middleware, publication reader and media routes, then lets
// Chromium and the pinned renderer exercise them through the actual preview.
func TestDashboardFaithfulExportChromium(t *testing.T) {
	if os.Getenv("DASHBOARD_EXPORT_BROWSER_CHECK") != "1" {
		t.Skip("run frontend/scripts/test-dashboard-faithful-export-browser.mjs")
	}
	f := newExternalToolsFixture(t)
	t.Setenv("AUTH_SECRET", "dashboard-faithful-browser-fixture-secret")
	page := `<!doctype html><html><head><style>
html,body{margin:0;min-height:180px;background:rgb(240,245,250)}html.dark,html.dark body{background:rgb(10,20,30)}
#bar{height:80px;background:rgb(20,160,220);width:0}
</style></head><body><div id="bar"></div><span id="value"></span><script>
window.report.ready(async()=>{await new Promise(resolve=>setTimeout(resolve,50));const snapshot=await window.report.get('{{dashboard_assets}}/data.json');
window.report.motion.create({id:'scores',duration:1,render:s=>{document.querySelector('#bar').style.width=(snapshot.total*s.progress)+'%';document.querySelector('#value').textContent=Math.round(snapshot.total*s.progress)}})});
</script></body></html>`
	created, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: "Workflow/invoices", Action: "create", ID: "faithful", Title: "Scores", IncludeDraft: true, Files: map[string]string{"index.html": page, "data.json": `{"total":96}`}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: "Workflow/invoices", Action: "publish", ID: "faithful", ExpectedRevision: created.Dashboard.Revision, IncludeDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	document := dashboards.Document("faithful", created.Dashboard.Revision)
	still, err := dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: "Workflow/invoices", Action: "create", ID: "still", Title: "Still", IncludeDraft: true, Files: map[string]string{"index.html": "<!doctype html><html><body>Final total: 96</body></html>"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = dashboardWorkspaceRequest(context.Background(), dashboards.Request{Root: "Workflow/invoices", Action: "publish", ID: "still", ExpectedRevision: still.Dashboard.Revision, IncludeDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	stillToken, err := mintReportPreviewToken(&UserClaims{UserID: "owner", Username: "owner"}, "Workflow/invoices", dashboards.Document("still", still.Dashboard.Revision))
	if err != nil {
		t.Fatal(err)
	}
	token, err := mintReportPreviewToken(&UserClaims{UserID: "owner", Username: "owner"}, "Workflow/invoices", document)
	if err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	router.HandleFunc(reportPreviewAPIPrefix+"file", f.api.handleReportPreviewFile).Methods("GET")
	router.HandleFunc(reportPreviewAPIPrefix+"media-url", f.api.handleReportMediaURL).Methods("POST")
	router.HandleFunc(reportMediaStreamPath, f.api.handleReportMediaStream).Methods("GET", "HEAD")
	service := httptest.NewServer(AuthMiddleware(router))
	defer service.Close()
	frontend, err := filepath.Abs("../../../frontend")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("node", "scripts/test-dashboard-faithful-export-browser.mjs")
	command.Dir = frontend
	command.Env = append(os.Environ(), "DASHBOARD_FIXTURE_URL="+service.URL, "DASHBOARD_FIXTURE_ROOT="+f.docs, "DASHBOARD_FIXTURE_REVISION="+created.Dashboard.Revision, "SECRET_DASHBOARD_EXPORT_TOKEN="+token, "DASHBOARD_FIXTURE_STATIC_REVISION="+still.Dashboard.Revision, "SECRET_DASHBOARD_EXPORT_STATIC_TOKEN="+stillToken)
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	result, err := command.Output()
	if err != nil {
		t.Fatalf("faithful capture: %v\n%s\n%s", err, result, diagnostics.String())
	}
	var proof map[string]any
	if err := json.Unmarshal(result, &proof); err != nil || proof["passed"] != true {
		t.Fatalf("browser proof missing: %v %s", err, result)
	}
	t.Log("Real published dashboard capture, frozen data, theme, terminal frame, media metadata/play/seek and scoped source refusal passed.")
}
