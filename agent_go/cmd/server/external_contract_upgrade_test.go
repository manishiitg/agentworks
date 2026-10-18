package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
)

// Real MCP, native migration tool, Builder stamp checks and disk. An owner
// moves a workflow from 1.0.46 to 1.0.47 without a model turn; the Builder's
// ordering and role rules still hold.
func TestExternalContractUpgradeOverMCP(t *testing.T) {
	f := newBuilderOperationFixture(t)
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","created_by":"owner","version":"1.0.46","code_layout_version":1,"access":{"owners":["owner"],"editors":["outsider"],"readers":["reader"]}}`)
	f.write(t, "Workflow/invoices/planning/plan.json", `{"steps":[{"id":"chat","type":"message_sequence","title":"Chat","description":"Talk to the person","items":[{"id":"hello","type":"user_message","message":"Hello"}]}]}`)
	_, owner := f.issue(t, "owner")
	_, reader := f.issue(t, "reader")

	status := f.call(t, owner, "workflow_upgrade", map[string]any{"action": "status"}, 200).Body.String()
	if !strings.Contains(status, `"version":"1.0.46"`) || !strings.Contains(status, `"version":"1.0.47"`) || !strings.Contains(status, "upgrade-agent-step") {
		t.Fatalf("status did not list the owed migration: %s", status)
	}
	// The stamp is refused until the migration's work is done.
	stamp := map[string]any{"action": "stamp", "version": "1.0.47", "confirm": "invoices"}
	f.call(t, owner, "workflow_upgrade", stamp, 409)
	if !strings.Contains(f.read(t, "Workflow/invoices/workflow.json"), `"1.0.46"`) {
		t.Fatal("a refused stamp changed the workflow")
	}

	revision := externalTestBody(t, f.call(t, owner, "get_plan", map[string]any{}, 200), 200)["revision"].(string)
	f.call(t, owner, "plan", map[string]any{"action": "migrate_agent_steps", "arguments": map[string]any{}, "expected_revision": revision, "request_id": "migrate-agent-steps"}, 200)
	if !strings.Contains(f.read(t, "Workflow/invoices/planning/plan.json"), `"type": "agent"`) && !strings.Contains(f.read(t, "Workflow/invoices/planning/plan.json"), `"type":"agent"`) {
		t.Fatal("the saved step type was not migrated")
	}

	f.call(t, owner, "workflow_upgrade", map[string]any{"action": "stamp", "version": "1.0.47", "confirm": "someone-else"}, 400)
	f.call(t, owner, "workflow_upgrade", map[string]any{"action": "stamp", "version": "1.0.48", "confirm": "invoices"}, 409)
	f.call(t, reader, "workflow_upgrade", stamp, 403)

	done := f.call(t, owner, "workflow_upgrade", stamp, 200).Body.String()
	if !strings.Contains(done, `"applied":true`) {
		t.Fatalf("stamp not applied: %s", done)
	}
	manifest := f.read(t, "Workflow/invoices/workflow.json")
	if !strings.Contains(manifest, `"1.0.47"`) || !strings.Contains(manifest, "contract_upgrade_history") {
		t.Fatalf("manifest not stamped with history: %s", manifest)
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
	revision = externalTestBody(t, f.call(t, owner, "get_plan", map[string]any{}, 200), 200)["revision"].(string)
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
	revision = externalTestBody(t, f.call(t, owner, "get_plan", map[string]any{}, 200), 200)["revision"].(string)
	// Saying "none" for a resource the description itself names is a contradiction, not a review.
	f.call(t, owner, "plan", map[string]any{"action": "update_step", "expected_revision": revision, "request_id": "resources-contradiction", "arguments": map[string]any{"step_id": "chat", "changes": map[string]any{"description": "## Goal\nGreet the person once.\n\n## Resources\n- Knowledge base: none\n- Database: none\n- Brain: none\n- Learnings: none\n\n## Guides\nknowledgebase/tone.md", "reason": "Resource intent review."}}}, 200)
	f.call(t, owner, "workflow_upgrade", intentStamp, 409)
	// Store settings do not forbid reading named workflow guides. Keep them unchanged through the stamp.
	config := `{"steps":[{"id":"chat","agent_configs":{"knowledgebase_access":"none","learnings_access":"none"}}]}`
	f.write(t, "Workflow/invoices/planning/step_config.json", config)
	f.write(t, "Workflow/invoices/knowledgebase/tone.md", "Use a clear greeting.")
	f.write(t, "Workflow/invoices/learnings/_global/references/greeting.md", "Greet once.")
	revision = externalTestBody(t, f.call(t, owner, "get_plan", map[string]any{}, 200), 200)["revision"].(string)
	f.call(t, owner, "plan", map[string]any{"action": "update_step", "expected_revision": revision, "request_id": "resources-reviewed", "arguments": map[string]any{"step_id": "chat", "changes": map[string]any{"description": "## Goal\nGreet the person once.\n\n## Resources\n- Knowledge base: reads the tone guide; no writes\n- Database: none\n- Brain: none\n- Learnings: reads the named greeting guide; no contributions\n\n## Guides\nknowledgebase/tone.md\nlearnings/_global/references/greeting.md", "reason": "Resource intent review."}}}, 200)
	configBeforeStamp := f.read(t, "Workflow/invoices/planning/step_config.json")
	f.call(t, owner, "workflow_upgrade", intentStamp, 200)
	if f.read(t, "Workflow/invoices/planning/step_config.json") != configBeforeStamp {
		t.Fatal("resource-intent stamp changed configured access")
	}
	after = f.call(t, owner, "workflow_upgrade", map[string]any{"action": "status"}, 200).Body.String()
	if !strings.Contains(after, "upgrade-goal-agent") {
		t.Fatalf("Goal agent review is not due: %s", after)
	}
	f.write(t, "Workflow/invoices/planning/step_config.json", `{"steps":[{"id":"chat","agent_configs":{"learning_objective":"Keep reusable greeting technique","learnings_access":"read-write","knowledgebase_access":"none"}}]}`)
	goalStamp := map[string]any{"action": "stamp", "version": "1.0.50", "confirm": "invoices"}
	f.call(t, owner, "workflow_upgrade", goalStamp, 409)
	revision = externalTestBody(t, f.call(t, owner, "get_plan", map[string]any{}, 200), 200)["revision"].(string)
	f.call(t, owner, "plan", map[string]any{"action": "migrate_goal_agents", "arguments": map[string]any{}, "expected_revision": revision, "request_id": "migrate-goal-agents"}, 200)
	migratedPlan := f.read(t, "Workflow/invoices/planning/plan.json")
	migratedConfig := f.read(t, "Workflow/invoices/planning/step_config.json")
	if !strings.Contains(migratedPlan, "Keep reusable greeting technique") || !strings.Contains(migratedPlan, "learnings/_global") || strings.Contains(migratedConfig, "learning_objective") || !strings.Contains(migratedConfig, `"learnings_access": "read-write"`) {
		t.Fatal("native MCP migration lost audit text or changed configured access")
	}
	f.call(t, reader, "workflow_upgrade", goalStamp, 403)
	f.call(t, owner, "workflow_upgrade", goalStamp, 200)
	after = f.call(t, owner, "workflow_upgrade", map[string]any{"action": "status"}, 200).Body.String()
	if !strings.Contains(after, `"pending":[]`) {
		t.Fatalf("reviewed migration remains due: %s", after)
	}

	// A valid Goal need not have per-step overrides. Missing config stays absent,
	// while malformed config is not mistaken for defaults by the stamp.
	manifest = strings.Replace(f.read(t, "Workflow/invoices/workflow.json"), `"version": "1.0.50"`, `"version": "1.0.49"`, 1)
	f.write(t, "Workflow/invoices/workflow.json", manifest)
	f.write(t, "Workflow/invoices/planning/step_config.json", `{broken`)
	f.call(t, owner, "workflow_upgrade", goalStamp, 409)
	configPath := filepath.Join(f.docs, "Workflow/invoices/planning/step_config.json")
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	revision = externalTestBody(t, f.call(t, owner, "get_plan", map[string]any{}, 200), 200)["revision"].(string)
	f.call(t, owner, "plan", map[string]any{"action": "migrate_goal_agents", "arguments": map[string]any{}, "expected_revision": revision, "request_id": "migrate-goal-agents-no-config"}, 200)
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("migration created config or could not check absence: %v", err)
	}
	f.call(t, owner, "workflow_upgrade", goalStamp, 200)
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("stamp created config or could not check absence: %v", err)
	}

}
