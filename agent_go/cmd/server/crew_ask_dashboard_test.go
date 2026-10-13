package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// The shared Ask read action reaches actual published storage, without a model,
// draft privileges, arbitrary project paths or a database query escape hatch.
func TestCrewAskDashboardPublishedSource(t *testing.T) {
	f := newExternalToolsFixture(t)
	owner, reader := UserClaims{UserID: "owner", Username: "owner"}, UserClaims{UserID: "reader", Username: "reader"}
	ctx := context.WithValue(context.Background(), UserContextKey, &owner)
	const root = "Workflow/invoices"
	created, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "create", ID: "ask-source", Title: "Totals", IncludeDraft: true, Files: map[string]string{"index.html": "<p>Total 42</p>", "scripts/total.py": "SELECT SUM(value) FROM metrics"}})
	if err != nil {
		t.Fatal(err)
	}
	revision := created.Dashboard.Revision
	if _, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: root, Action: "publish", ID: "ask-source", IncludeDraft: true, ExpectedRevision: revision}); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, UserContextKey, &reader)
	target, err := f.api.dashboardTarget(ctx, &reader, root)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := registerDashboardQuestionResources(ctx, []dashboardQuestionDiscovery{{Target: target, Document: *created.Dashboard}})
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]interface{}{"action": "dashboard", "dashboard_id": ids[0], "captured_revision": revision}
	result, err := f.api.crewAskFilesRun(ctx, root, root, args)
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]interface{}
	if json.Unmarshal([]byte(result), &source) != nil || source["captured_revision"] != revision || !strings.Contains(source["content"].(string), "42") {
		t.Fatal(result)
	}
	args["path"] = "scripts/total.py"
	if result, err := f.api.crewAskFilesRun(ctx, root, root, args); err != nil || !strings.Contains(result, "SUM(value)") {
		t.Fatal(result, err)
	}
	for _, file := range []string{"../../builder/conversation/private.json", "db/db.sqlite", "triggers/deliveries/another-caller.json"} {
		args["path"] = file
		if _, err := f.api.crewAskFilesRun(ctx, root, root, args); err == nil {
			t.Fatal("read private source", file)
		}
	}
	args["path"] = "index.html"
	if _, err := f.api.crewAskFilesRun(ctx, "Workflow/secret", "Workflow/secret", args); err == nil {
		t.Fatal("read another project's publication")
	}
	args["action"], args["sql"] = "query", "SELECT * FROM private_results"
	if _, err := f.api.crewAskFilesRun(ctx, root, root, args); err == nil {
		t.Fatal("accepted arbitrary database SQL")
	}
	args["action"] = "dashboard"
	if err := os.Remove(filepath.Join(f.docs, root, "db/reports/.dashboard-state/ask-source", revision+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.crewAskFilesRun(ctx, root, root, args); err == nil {
		t.Fatal("read revoked captured publication")
	}
}
