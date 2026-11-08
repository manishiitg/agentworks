package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
)

// The plan tool's own schema once carried every operation's schema (about 112k
// characters); the operations are served by action=list instead. PLAT-746-421.
func TestPlanToolSchemaStaysCompactAndNotInTheNativeBuilder(t *testing.T) {
	tools, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools {
		if tool.Name != "plan" {
			continue
		}
		found = true
		raw, _ := json.Marshal(tool.InputSchema)
		if len(raw) > 4000 {
			t.Fatalf("plan tool schema is %d characters, want a few KB", len(raw))
		}
		if strings.Contains(string(raw), "anyOf") {
			t.Fatal("plan tool schema must not embed the operation schemas")
		}
	}
	if !found {
		t.Fatal("plan tool is missing from the external catalog")
	}
	// The wrapper is external only: the Goal builder chat registers the native
	// plan tools directly, so it must never admit this tool by name.
	for _, name := range agentworksproduct.ChatTools("builder") {
		if name == "plan" {
			t.Fatal("the native Builder chat admits the external plan wrapper")
		}
	}
}

// Without the old anyOf, a call is checked by its operation's own closed
// schema. A bad call is refused with that operation's message and before the
// request is claimed or any file staged, so the same request_id still works.
func TestPlanArgumentsAreRefusedByTheOperationBeforeAnyMutation(t *testing.T) {
	f := newBuilderOperationFixture(t)
	t.Setenv("AGENTWORKS_MCP_BUILDER_ENABLED", "false")
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	_, raw := f.issue(t, "owner")
	revision := externalTestBody(t, f.call(t, raw, "get_plan", map[string]any{}, 200), 200)["revision"].(string)
	before := f.read(t, "Workflow/invoices/planning/plan.json")
	good := map[string]any{"existing_step_id": "fetch-invoices", "title": "Fetch pending invoices", "reason": "Clarify pending invoice scope"}
	with := func(change func(map[string]any)) map[string]any {
		arguments := map[string]any{}
		for key, value := range good {
			arguments[key] = value
		}
		change(arguments)
		return map[string]any{"workflow_id": "invoices", "action": "update_scripted_step", "expected_revision": revision, "request_id": "check", "arguments": arguments}
	}
	for name, call := range map[string]map[string]any{
		"unexpected property": with(func(a map[string]any) { a["unexpected"] = true }),
		"wrong type":          with(func(a map[string]any) { a["title"] = 5 }),
		"missing required":    with(func(a map[string]any) { delete(a, "existing_step_id") }),
	} {
		body := externalTestBody(t, f.call(t, raw, "plan", call, 400), 400)
		message, _ := json.Marshal(body)
		if !strings.Contains(string(message), "plan_edit_rejected") || !strings.Contains(string(message), "update_scripted_step") || !strings.Contains(string(message), "invalid") {
			t.Fatalf("%s: not refused by the operation's own validator: %s", name, message)
		}
		if f.read(t, "Workflow/invoices/planning/plan.json") != before {
			t.Fatalf("%s changed the plan", name)
		}
	}
	// The consolidated operations take the same path with their own message.
	modern := func(arguments map[string]any) map[string]any {
		return map[string]any{"workflow_id": "invoices", "action": "update_step", "expected_revision": revision, "request_id": "check-modern", "arguments": arguments}
	}
	for name, arguments := range map[string]map[string]any{
		"modern wrong type":        {"step_id": 5, "changes": map[string]any{"title": "x", "reason": "y"}},
		"modern missing required":  {"step_id": "fetch-invoices"},
		"modern unexpected fields": {"step_id": "fetch-invoices", "changes": map[string]any{"title": "x", "reason": "y"}, "unexpected": true},
	} {
		message, _ := json.Marshal(externalTestBody(t, f.call(t, raw, "plan", modern(arguments), 400), 400))
		if !strings.Contains(string(message), "plan_edit_rejected") || !strings.Contains(string(message), "invalid update_step arguments") {
			t.Fatalf("%s: not refused by the operation's own validator: %s", name, message)
		}
	}
	if f.read(t, "Workflow/invoices/planning/plan.json") != before {
		t.Fatal("a refused consolidated call changed the plan")
	}
	// The refusals did not claim the request_id, so the corrected call succeeds.
	f.call(t, raw, "plan", with(func(map[string]any) {}), 200)
	if !strings.Contains(f.read(t, "Workflow/invoices/planning/plan.json"), "Fetch pending invoices") {
		t.Fatal("the corrected call did not apply")
	}
	// list is unchanged without an operation and narrows to one with it.
	all := f.call(t, raw, "plan", map[string]any{"action": "list"}, 200).Body.String()
	one := f.call(t, raw, "plan", map[string]any{"action": "list", "operation": "update_step_config"}, 200).Body.String()
	if !strings.Contains(all, "update_step_config") || !strings.Contains(all, "add_step") || len(one) >= len(all)/2 || strings.Contains(one, `"name":"add_step"`) || !strings.Contains(one, "input_schema") {
		t.Fatalf("list operation filter wrong: all=%d one=%d", len(all), len(one))
	}
	f.call(t, raw, "plan", map[string]any{"action": "list", "operation": "no_such_operation"}, 400)
}
