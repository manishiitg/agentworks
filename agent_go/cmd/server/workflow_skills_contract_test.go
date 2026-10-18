package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
)

// Real MCP -> native staged migration -> disk -> stamp, plus the website
// Builder's same executor. Large guides and binary assets never become prompt
// bodies or shared account skills, and team assignments remain authoritative.
func TestWorkflowSkillsMigrationOverMCPAndBuilder(t *testing.T) {
	f := newBuilderOperationFixture(t)
	router := gin.New()
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	workspace := httptest.NewServer(router)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","created_by":"owner","version":"1.0.50","code_layout_version":1,"access":{"owners":["owner"],"editors":["outsider"],"readers":["reader"]},"capabilities":{"selected_skills":["existing"]}}`)
	f.write(t, "Workflow/invoices/planning/plan.json", `{"steps":[{"id":"chat","type":"agent","title":"Review invoices","prompt_contract":"description-first-v1","description":"## Goal\nReview invoices.\n## Resources\n- Knowledge base: none\n- Database: none\n- Brain: none\n- Learnings: reads the invoice guide\n## Guides\nlearnings/chat/guide.md","items":[]},{"id":"excluded","type":"scripted","title":"No guides","description":"Return existing records"}]}`)
	f.write(t, "Workflow/invoices/planning/step_config.json", `{"steps":[{"id":"chat","agent_configs":{"learnings_access":"read-write","knowledgebase_access":"none","enabled_skills":["existing"]}},{"id":"excluded","agent_configs":{"learnings_access":"none","knowledgebase_access":"read-write"}}]}`)
	prose := "# Existing how-to\n" + strings.Repeat("Preserve this reusable technique verbatim.\n", 18000)
	asset := string([]byte{0, 255, 128, 42, 10})
	sources := map[string]string{"learnings/chat/guide.md": prose, "learnings/_global/references/auth.md": "Read the verified authentication receipt.\n", "learnings/chat/assets/example.bin": asset}
	for p, raw := range sources {
		f.write(t, "Workflow/invoices/"+p, raw)
	}
	f.write(t, "Workflow/invoices/learnings/chat/main.py", "print('legacy backup')")
	f.write(t, "Workflow/invoices/learnings/chat/script_metadata.json", `{"history":true}`)
	f.write(t, "Workflow/invoices/skills/workflow-how-to/SKILL.md", "Existing skill must stay unchanged")
	_, owner := f.issue(t, "owner")
	_, reader := f.issue(t, "reader")
	revision := func() string {
		return externalTestBody(t, f.call(t, owner, "get_plan", map[string]any{}, 200), 200)["revision"].(string)
	}
	stamp := map[string]any{"action": "stamp", "version": "1.0.51", "confirm": "invoices"}
	f.call(t, owner, "workflow_upgrade", stamp, 409)
	migrate := map[string]any{"action": "migrate_workflow_skills", "arguments": map[string]any{}, "expected_revision": revision(), "request_id": "migrate-skills"}
	f.call(t, reader, "plan", migrate, 403)
	if _, err := os.Stat(filepath.Join(f.docs, "Workflow/invoices/skills/workflow-how-to-2")); !os.IsNotExist(err) {
		t.Fatal("refused migration copied files", err)
	}
	token, _ := f.issue(t, "owner")
	claims, err := accessTokenClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	server := serveExternalMCP(t, f.api, claims)
	cli := dialExternalMCP(t, t.Context(), server.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), cli)
	migrate["workflow_id"] = "invoices"
	result := callRemoteTool(t, t.Context(), cli, "call_tool", map[string]any{"name": "plan", "arguments": migrate})
	requireRemoteSuccess(t, result, "migrate workflow skills")
	const folder = "skills/workflow-how-to-2/"
	var manifest struct {
		Version   string                               `json:"version"`
		Migration stepworkflow.WorkflowSkillsMigration `json:"workflow_skills_migration"`
	}
	if err := json.Unmarshal([]byte(f.read(t, "Workflow/invoices/workflow.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.0.50" || manifest.Migration.Folder != "workflow-how-to-2" || len(manifest.Migration.Files) != len(sources) {
		t.Fatalf("wrong migration ledger: %+v", manifest)
	}
	for _, file := range manifest.Migration.Files {
		source := sources[file.Source]
		target := "Workflow/invoices/" + folder + file.Target
		if f.read(t, target) != source || f.read(t, "Workflow/invoices/"+file.Source) != source {
			t.Fatal("guide or asset bytes lost", file.Source)
		}
		hash := sha256.Sum256([]byte(source))
		if file.SHA256 != hex.EncodeToString(hash[:]) {
			t.Fatal("copy hash wrong", file.Source)
		}
	}
	index := f.read(t, "Workflow/invoices/"+folder+"SKILL.md")
	if len(index) > 2000 || strings.Contains(index, "Preserve this reusable") || !strings.Contains(index, "Step chat (Review invoices)") || !strings.Contains(index, "Shared workflow guide") {
		t.Fatal("index must be short, lazy and show originating steps", index)
	}
	if f.read(t, "Workflow/invoices/skills/workflow-how-to/SKILL.md") != "Existing skill must stay unchanged" {
		t.Fatal("migration overwrote existing skill")
	}
	for _, p := range []string{"references/chat/main.py", "references/chat/script_metadata.json"} {
		if _, err := os.Stat(filepath.Join(f.docs, "Workflow/invoices/"+folder+p)); !os.IsNotExist(err) {
			t.Fatal("legacy code or metadata copied into skill", p)
		}
	}
	plan := f.read(t, "Workflow/invoices/planning/plan.json")
	if !strings.Contains(plan, "skills/workflow-how-to-2/references/chat/guide.md") || !strings.Contains(plan, "retired_learning_resource") {
		t.Fatal("live references or audit missing")
	}
	var config stepworkflow.StepConfigFile
	if err := json.Unmarshal([]byte(f.read(t, "Workflow/invoices/planning/step_config.json")), &config); err != nil {
		t.Fatal(err)
	}
	for _, sc := range config.Steps {
		ac := sc.AgentConfigs
		if ac.LearningsAccess != "" {
			t.Fatal("legacy access still active")
		}
		if sc.ID == "chat" {
			if ac.KnowledgebaseAccess != "none" || strings.Join(ac.EnabledSkills, ",") != "existing,workflow-how-to-2" {
				t.Fatal("reader skills or KB permissions changed", ac)
			}
		} else if ac.KnowledgebaseAccess != "read-write" || len(ac.EnabledSkills) > 0 {
			t.Fatal("opt-out or KB permissions changed", ac)
		}
	}
	// Stored copy tampering cannot be waved through by stamping or overwritten
	// by retry; an owner must reconcile the separately edited guide first.
	copyPath := "Workflow/invoices/" + folder + "references/chat/guide.md"
	f.write(t, copyPath, "Separately edited guide")
	f.call(t, owner, "workflow_upgrade", stamp, 409)
	migrate["request_id"] = "retry-conflicting-guide"
	migrate["expected_revision"] = revision()
	f.call(t, owner, "plan", migrate, 400)
	if f.read(t, copyPath) != "Separately edited guide" {
		t.Fatal("retry destroyed a separate edit")
	}
	f.write(t, copyPath, prose)
	f.call(t, owner, "workflow_upgrade", stamp, 200)
	status := f.call(t, owner, "workflow_upgrade", map[string]any{"action": "status"}, 200).Body.String()
	if !strings.Contains(status, `"pending":[]`) {
		t.Fatal(status)
	}
	read := f.call(t, reader, "read_workflow_knowledge", map[string]any{"path": folder + "references/_global/references/auth.md"}, 200).Body.String()
	if !strings.Contains(read, "authentication receipt") {
		t.Fatal("shared product cannot read local skill", read)
	}
	f.call(t, reader, "read_workflow_knowledge", map[string]any{"path": "skills/not-selected/SKILL.md"}, 403)

	// The same typed Builder writer uses the existing team commit checks. A
	// Leader must reassign a Contributor's route before changing its settings.
	teamManifest := `{"id":"invoices","label":"Invoices","version":"1.0.50","code_layout_version":1,"access":{"owners":["owner"]},"team":{"leader_id":"owner","revision":1,"contributors":[{"user_id":"outsider","run":true}],"routes":[{"step_id":"parent","route_id":"first","contributor_id":"outsider"}]}}`
	teamPlan := `{"steps":[{"id":"parent","type":"agent","title":"Coordinate","description":"Coordinate","predefined_routes":[{"route_id":"first","route_name":"First","sub_agent_step":{"id":"chat","type":"scripted","title":"Assigned","description":"Read invoices"}}]}]}`
	f.write(t, "Workflow/invoices/workflow.json", teamManifest)
	f.write(t, "Workflow/invoices/planning/plan.json", teamPlan)
	f.write(t, "Workflow/invoices/planning/step_config.json", `{"steps":[{"id":"chat","agent_configs":{"learnings_access":"read","knowledgebase_access":"none"}}]}`)
	execute := func(ctx context.Context, args map[string]interface{}) (string, error) {
		return stepworkflow.ExecuteExternalBuilderPlanTool(ctx, "migrate_workflow_skills", args, "Workflow/invoices", f.api.logger, nil, nil, nil)
	}
	builderArgs := map[string]interface{}{"expected_plan_revision": teamRevision(teamPlan)}
	if _, err := executeWorkflowSkillsBuilderTool(t.Context(), "Workflow/invoices", "owner", builderArgs, execute); err == nil {
		t.Fatal("Leader edited assigned route without reassignment")
	}
	if f.read(t, "Workflow/invoices/workflow.json") != teamManifest {
		t.Fatal("rejected team migration wrote its manifest")
	}
	if _, err := os.Stat(filepath.Join(f.docs, "Workflow/invoices/skills/workflow-how-to-3")); !os.IsNotExist(err) {
		t.Fatal("rejected team migration copied guides", err)
	}
	teamManifest = strings.Replace(teamManifest, `"routes":[{"step_id":"parent","route_id":"first","contributor_id":"outsider"}]`, `"routes":[]`, 1)
	f.write(t, "Workflow/invoices/workflow.json", teamManifest)
	if _, err := executeWorkflowSkillsBuilderTool(t.Context(), "Workflow/invoices", "owner", builderArgs, execute); err != nil {
		t.Fatal("Builder migration after reassignment", err)
	}
	if f.read(t, "Workflow/invoices/skills/workflow-how-to-3/references/chat/assets/example.bin") != asset {
		t.Fatal("native Builder lost binary asset")
	}
}
