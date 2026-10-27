package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
)

// Real MCP, Builder stamp checks and disk. Deployment has prepared the plan;
// remaining semantic upgrades retain their ordering, access and grant checks.
func TestExternalContractUpgradeOverMCP(t *testing.T) {
	f := newBuilderOperationFixture(t)
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","created_by":"owner","version":"1.0.44","code_layout_version":1,"access":{"owners":["owner","outsider"],"readers":["reader"]}}`)
	f.write(t, "Workflow/invoices/planning/plan.json", `{"steps":[{"id":"chat","type":"agent","title":"Chat","description":"Talk to the person","items":[{"id":"hello","type":"user_message","message":"Hello"}]}]}`)
	_, owner := f.issue(t, "owner")
	_, reader := f.issue(t, "reader")

	status := f.call(t, owner, "workflow_upgrade", map[string]any{"action": "status"}, 200).Body.String()
	if !strings.Contains(status, `"version":"1.0.46"`) || !strings.Contains(status, `"version":"1.0.47"`) || !strings.Contains(status, "upgrade-agent-step") {
		t.Fatalf("status did not list the owed migration: %s", status)
	}
	// Reading status leaves the already prepared plan unchanged.
	if !strings.Contains(f.read(t, "Workflow/invoices/planning/plan.json"), `"type":"agent"`) {
		t.Fatal("status mutated the saved plan")
	}
	f.call(t, owner, "workflow_upgrade", map[string]any{"action": "prepare"}, 400)
	f.write(t, "Workflow/invoices/code/check/main.py", "import sqlite3\nconn = sqlite3.connect('db/db.sqlite')\n")
	managedStamp := map[string]any{"action": "stamp", "version": "1.0.45", "confirm": "invoices"}
	f.call(t, owner, "workflow_upgrade", managedStamp, 409)
	f.write(t, "Workflow/invoices/code/check/main.py", "from agentworks_db import query\nrows = query('SELECT 1')\n")
	f.call(t, owner, "workflow_upgrade", managedStamp, 200)
	layoutStamp := map[string]any{"action": "stamp", "version": "1.0.46", "confirm": "invoices"}
	f.call(t, owner, "workflow_upgrade", layoutStamp, 409) // heading checks still apply
	revision := externalTestBody(t, f.call(t, owner, "workflow", map[string]any{"action": "plan"}, 200), 200)["revision"].(string)
	f.call(t, owner, "plan", map[string]any{"action": "update_step", "expected_revision": revision, "request_id": "layout-before-agent-stamp", "arguments": map[string]any{"step_id": "chat", "changes": map[string]any{"description": "## Goal\nTalk to the person.\n## Inputs\nTheir message.\n## Output\nA greeting.\n## Done when\nThe person is greeted.", "reason": "Organize the existing greeting task."}}}, 200)
	f.call(t, owner, "workflow_upgrade", layoutStamp, 200)

	// Stamps still follow the ladder after preparation.
	stamp := map[string]any{"action": "stamp", "version": "1.0.47", "confirm": "invoices"}
	if !strings.Contains(f.read(t, "Workflow/invoices/workflow.json"), `"1.0.46"`) {
		t.Fatal("a refused stamp changed the workflow")
	}

	f.call(t, owner, "workflow_upgrade", map[string]any{"action": "stamp", "version": "1.0.47", "confirm": "someone-else"}, 400)
	f.call(t, owner, "workflow_upgrade", map[string]any{"action": "stamp", "version": "1.0.48", "confirm": "invoices"}, 409)
	f.call(t, reader, "workflow_upgrade", stamp, 403)

	done := f.call(t, owner, "workflow_upgrade", stamp, 200).Body.String()
	if !strings.Contains(done, `"applied":true`) {
		t.Fatalf("stamp not applied: %s", done)
	}
	manifest := f.read(t, "Workflow/invoices/workflow.json")
	if !strings.Contains(manifest, `"1.0.47"`) || strings.Contains(manifest, "contract_upgrade_history") {
		t.Fatalf("manifest not stamped without obsolete history: %s", manifest)
	}
	again := f.call(t, owner, "workflow_upgrade", stamp, 200).Body.String()
	if !strings.Contains(again, `"applied":false`) {
		t.Fatalf("a repeated stamp was not a no-op: %s", again)
	}
	after := f.call(t, owner, "workflow_upgrade", map[string]any{"action": "status"}, 200).Body.String()
	if !strings.Contains(after, `upgrade-description-first`) {
		t.Fatalf("description-first migration should remain: %s", after)
	}
	nextStamp := map[string]any{"action": "stamp", "version": "1.0.48", "confirm": "invoices"}
	f.call(t, owner, "workflow_upgrade", nextStamp, 409)
	revision = externalTestBody(t, f.call(t, owner, "workflow", map[string]any{"action": "plan"}, 200), 200)["revision"].(string)
	f.call(t, owner, "plan", map[string]any{"action": "update_step", "expected_revision": revision, "request_id": "review-description-first", "arguments": map[string]any{"step_id": "chat", "changes": map[string]any{"prompt_contract": "description-first-v1", "description": "Greet the person once.", "system_rules": "Never repeat the greeting to validate it.", "items": []any{}, "reason": "Reviewed description-first task; remove redundant execution item."}}}, 200)
	f.call(t, owner, "workflow_upgrade", nextStamp, 200)
	// A workflow already on 1.0.48 owes the resource-intent review (1.0.49).
	after = f.call(t, owner, "workflow_upgrade", map[string]any{"action": "status"}, 200).Body.String()
	if !strings.Contains(after, `upgrade-resource-intent`) {
		t.Fatalf("resource-intent migration should be due after 1.0.48: %s", after)
	}
	intentStamp := map[string]any{"action": "stamp", "version": "1.0.49", "confirm": "invoices"}
	// Refused while the description says nothing about the four resources.
	f.call(t, owner, "workflow_upgrade", intentStamp, 409)
	revision = externalTestBody(t, f.call(t, owner, "workflow", map[string]any{"action": "plan"}, 200), 200)["revision"].(string)
	// Saying "none" for a resource the description itself names is a contradiction, not a review.
	f.call(t, owner, "plan", map[string]any{"action": "update_step", "expected_revision": revision, "request_id": "resources-contradiction", "arguments": map[string]any{"step_id": "chat", "changes": map[string]any{"description": "## Goal\nGreet the person once.\n\n## Resources\n- Knowledge base: none\n- Database: none\n- Brain: none\n- Learnings: none\n\n## Guides\nknowledgebase/tone.md", "reason": "Resource intent review."}}}, 200)
	f.call(t, owner, "workflow_upgrade", intentStamp, 409)
	// Store settings do not forbid reading named workflow guides. Keep them unchanged through the stamp.
	config := `{"steps":[{"id":"chat","agent_configs":{"knowledgebase_access":"none","learnings_access":"none"}}]}`
	f.write(t, "Workflow/invoices/planning/step_config.json", config)
	f.write(t, "Workflow/invoices/knowledgebase/tone.md", "Use a clear greeting.")
	f.write(t, "Workflow/invoices/learnings/_global/references/greeting.md", "Greet once.")
	revision = externalTestBody(t, f.call(t, owner, "workflow", map[string]any{"action": "plan"}, 200), 200)["revision"].(string)
	f.call(t, owner, "plan", map[string]any{"action": "update_step", "expected_revision": revision, "request_id": "resources-reviewed", "arguments": map[string]any{"step_id": "chat", "changes": map[string]any{"description": "## Goal\nGreet the person once.\n\n## Resources\n- Knowledge base: reads the tone guide; no writes\n- Database: none\n- Brain: none\n- Learnings: reads the named greeting guide; no contributions\n\n## Guides\nknowledgebase/tone.md\nlearnings/_global/references/greeting.md", "reason": "Resource intent review."}}}, 200)
	configBeforeStamp := f.read(t, "Workflow/invoices/planning/step_config.json")
	f.call(t, owner, "workflow_upgrade", intentStamp, 200)
	if f.read(t, "Workflow/invoices/planning/step_config.json") != configBeforeStamp {
		t.Fatal("resource-intent stamp changed configured access")
	}
	after = f.call(t, owner, "workflow_upgrade", map[string]any{"action": "status"}, 200).Body.String()
	if !strings.Contains(after, "upgrade-workflow-skills") || strings.Contains(after, "upgrade-goal-agent") {
		t.Fatalf("only the remaining skills upgrade should follow resource review: %s", after)
	}
	skillsStamp := map[string]any{"action": "stamp", "version": "1.0.51", "confirm": "invoices"}
	f.call(t, reader, "workflow_upgrade", skillsStamp, 403)
	f.call(t, owner, "workflow_upgrade", skillsStamp, 409) // copies and review are still required
	// PLAT-825-111: the retired no-op 1.0.50 rung must not strand a Goal
	// reviewed through 1.0.47 -> 1.0.48 -> 1.0.49. The next stamp is 1.0.51.
	if strings.Contains(after, `"version":"1.0.50"`) {
		t.Fatal("status still requires the retired conversion", after)
	}
	revision = externalTestBody(t, f.call(t, owner, "workflow", map[string]any{"action": "plan"}, 200), 200)["revision"].(string)
	f.call(t, owner, "plan", map[string]any{"action": "migrate_workflow_skills", "arguments": map[string]any{}, "expected_revision": revision, "request_id": "skills-after-reviewed-resource-intent"}, 200)
	f.call(t, owner, "workflow_upgrade", skillsStamp, 200)
	after = f.call(t, owner, "workflow_upgrade", map[string]any{"action": "status"}, 200).Body.String()
	if !strings.Contains(after, `"version":"1.0.51"`) || !strings.Contains(after, `"pending":[]`) {
		t.Fatal("reviewed Goal did not reach the current contract directly", after)
	}
}
