package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
)

// Real MCP, native plan executors, disk and workspace readers, including team
// policy. No model turn and no mocked file writer stand in for the commit.
func TestExternalPlanEditsOverMCP(t *testing.T) {
	f := newBuilderOperationFixture(t)
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	token, raw := f.issue(t, "owner")
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := serveExternalMCP(t, f.api, claims)
	cli := dialExternalMCP(t, ctx, server.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	revision := func(credential string) string {
		t.Helper()
		body := externalTestBody(t, f.call(t, credential, "get_plan", map[string]any{}, 200), 200)
		return body["revision"].(string)
	}
	args := map[string]any{"workflow_id": "invoices", "action": "update_scripted_step", "expected_revision": revision(raw), "request_id": "rename", "arguments": map[string]any{"existing_step_id": "fetch-invoices", "title": "Fetch pending invoices", "reason": "Clarify pending invoice scope"}}
	result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "plan", "arguments": args})
	requireRemoteSuccess(t, result, "direct plan update")
	if !strings.Contains(f.read(t, "Workflow/invoices/planning/plan.json"), "Fetch pending invoices") {
		t.Fatal("native change was not saved")
	}
	first := f.call(t, raw, "plan", args, 200).Body.String()
	again := f.call(t, raw, "plan", args, 200).Body.String()
	if first != again || !strings.Contains(first, `"applied":true`) {
		t.Fatalf("retry receipt changed: %s %s", first, again)
	}
	changelogs, err := filepath.Glob(filepath.Join(f.docs, "Workflow/invoices/planning/changelog/*.json"))
	if err != nil || len(changelogs) == 0 {
		t.Fatal("no native plan changelog", err)
	}
	var logText strings.Builder
	for _, p := range changelogs {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		logText.Write(b)
	}
	if !strings.Contains(logText.String(), "Clarify pending invoice scope") || !strings.Contains(logText.String(), token.ID) || !strings.Contains(logText.String(), `"user_id": "owner"`) {
		t.Fatal("native changelog lacks reason or authenticated origin", logText.String())
	}
	args["request_id"] = "stale"
	f.call(t, raw, "plan", args, 409)
	_, reader := f.issue(t, "reader")
	f.call(t, reader, "plan", map[string]any{"action": "list"}, 403)
	_, readonlyOwner := f.issue(t, "readonly-owner")
	f.call(t, readonlyOwner, "plan", map[string]any{"action": "list"}, 404)
	args["expected_revision"] = revision(raw)
	args["arguments"].(map[string]any)["unexpected"] = true
	f.call(t, raw, "plan", args, 400)
	delete(args["arguments"].(map[string]any), "unexpected")
	list := f.call(t, raw, "plan", map[string]any{"action": "list"}, 200)
	if !strings.Contains(list.Body.String(), "update_step_config") || !strings.Contains(list.Body.String(), "input_schema") {
		t.Fatal(list.Body.String())
	}
	// Consolidated website names reuse the current plan type and native validation.
	f.call(t, raw, "plan", map[string]any{"action": "update_step", "expected_revision": revision(raw), "request_id": "current-type", "arguments": map[string]any{"step_id": "fetch-invoices", "changes": map[string]any{"title": "Fetch approved invoices", "reason": "Use the shared Builder operation"}}}, 200)
	if !strings.Contains(f.read(t, "Workflow/invoices/planning/plan.json"), "Fetch approved invoices") {
		t.Fatal("consolidated edit was not saved")
	}
	// Current native route ownership remains authoritative on the direct path.
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","access":{"owners":["owner"]},"team":{"leader_id":"owner","revision":1,"contributors":[{"user_id":"outsider","run":true}],"routes":[{"step_id":"parent","route_id":"first-route","contributor_id":"outsider"}]}}`)
	f.write(t, "Workflow/invoices/planning/plan.json", `{"steps":[{"type":"agent","id":"parent","title":"Coordinate","description":"Coordinate route contributions","items":[{"id":"coordinate","type":"user_message","message":"Coordinate contributions."}],"next_step_id":"end","predefined_routes":[{"route_id":"first-route","route_name":"First","sub_agent_step":{"type":"agent","id":"first-route","title":"First contribution","description":"Measure contribution","items":[{"id":"measure","type":"user_message","message":"Measure contribution."}],"next_step_id":"end"}}]}],"orphan_steps":[]}`)
	_, editor := f.issue(t, "outsider")
	edit := map[string]any{"action": "update_agent_step", "expected_revision": revision(editor), "request_id": "route-edit", "arguments": map[string]any{"existing_step_id": "first-route", "title": "Assigned contribution", "reason": "Clarify my assigned route"}}
	f.call(t, editor, "plan", edit, 200)
	before := f.read(t, "Workflow/invoices/planning/plan.json")
	edit["expected_revision"] = revision(editor)
	edit["request_id"] = "shared-edit"
	edit["arguments"].(map[string]any)["existing_step_id"] = "parent"
	f.call(t, editor, "plan", edit, 409)
	if f.read(t, "Workflow/invoices/planning/plan.json") != before {
		t.Fatal("rejected shared edit changed the plan")
	}
}
