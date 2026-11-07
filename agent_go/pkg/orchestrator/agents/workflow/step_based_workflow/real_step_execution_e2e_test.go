package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/spf13/viper"
)

// The Builder's real single-step pipeline runs saved Python through the
// workspace HTTP server, writes the workflow database and saves durable assets.
// Obsolete fields in saved JSON cannot select another execution mode.
func TestBuilderStepExecutionUsesLiveDatabaseAndAssets(t *testing.T) {
	docs, server := workflowHTTPTestWorkspace(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")

	const workflow = "Workflow/real-execution"
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(docs, workflow, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("workflow.json", `{"code_layout_version":1,"test_mode":true}`)
	write("variables/variables.json", `{"variables":[],"groups":[{"name":"default","values":{}}]}`)
	write("soul/soul.md", "# Workflow\nWrite the requested result.\n")
	write("db/README.md", "# Database\nresults stores the current evidence path.\n")
	write("planning/plan.json", `{"steps":[{"type":"scripted","id":"save-evidence","title":"Save evidence","description":"Write a result and durable evidence.","script_only":true,"test_mode":true,"context_output":"result.json"}]}`)
	write("planning/step_config.json", `{"steps":[{"id":"save-evidence","agent_configs":{"lock_code":true,"test_mode":true,"learnings_access":"none","knowledgebase_access":"none"}}]}`)
	write("code/save-evidence/main.py", `import json, os
from pathlib import Path
from agentworks_db import execute
assert 'AGENTWORKS_TEST_MODE' not in os.environ
assert os.environ['RUN_FOLDER'] == 'run-evidence'
db_path = Path(os.environ['DB_PATH'])
assert str(db_path).endswith('/Workflow/real-execution/db/db.sqlite')
asset = db_path.parent / 'assets' / 'evidence.md'
asset.parent.mkdir(parents=True, exist_ok=True)
asset.write_text('Current evidence')
execute("INSERT INTO results VALUES (?, ?)", ["current", "db/assets/evidence.md"])
(Path(os.environ['STEP_OUTPUT_DIR']) / 'result.json').write_text(json.dumps({'saved': True}))
print('STATUS: COMPLETED')
`)
	dbPath := filepath.Join(docs, workflow, "db/db.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE results (id TEXT PRIMARY KEY, evidence_path TEXT)"); err != nil {
		t.Fatal(err)
	}
	// Old receipts remain readable; starting ordinary work must not delete them.
	write("runs/test-historical/test_mode.json", `{"succeeded":false}`)

	c, err := NewStepBasedWorkflowOrchestrator(context.Background(), "", "", 0, "", nil, nil, false, "", &orchestrator.LLMConfig{}, 1, loggerv2.NewNoop(), nil, nil, nil, nil, nil, nil, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetWorkspacePath(workflow)
	c.WorkspaceClient = workspace.NewClient(server.URL)
	// Only the HTTP transport is adapted here. Python uses the shipped DB helper,
	// the real per-step bridge registry and the workspace's real SQL handler.
	c.ToolCategories = map[string]string{"mutate_workflow_db": "workflow_db"}
	c.WorkspaceTools = []llmtypes.Tool{{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "mutate_workflow_db"}}}
	c.WorkspaceToolExecutors = map[string]interface{}{"mutate_workflow_db": func(ctx context.Context, args map[string]interface{}) (string, error) {
		params, _ := args["params"].([]interface{})
		result, err := c.WorkspaceClient.MutateAuthorizedWorkflowDB(ctx, workspace.MutateWorkflowDBParams{DBPath: workflow + "/db/db.sqlite", Statements: []workspace.WorkflowDBMutationStatement{{SQL: args["sql"].(string), Params: params}}})
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(result)
		return string(raw), err
	}}
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 5 || parts[0] != "s" {
			http.NotFound(w, r)
			return
		}
		var args map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		result, err := codeexec.CallCustomToolWithSession(r.Context(), parts[1], parts[4], args)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "result": result})
	}))
	defer bridge.Close()
	t.Setenv("MCP_API_URL", bridge.URL)
	c.SetWorkspaceEnvRef(map[string]string{"MCP_API_TOKEN": "test-bridge"})
	c.variableValues = map[string]string{}
	defer common.ClearSessionShellConfig(c.GetMCPSessionID())
	var options WorkshopExecuteOptions
	if err := json.Unmarshal([]byte(`{"RunFolder":"run-evidence","SavedScriptOnly":true,"test_mode":true,"TestRunID":"test-obsolete","RealRunFolder":"iteration-99/default"}`), &options); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	if result, err := c.ExecuteStepForWorkshop(ctx, "save-evidence", &options); err != nil {
		t.Fatalf("normal Builder execution failed: %s (%v)", result, err)
	}
	var evidence string
	if err := db.QueryRow("SELECT evidence_path FROM results WHERE id='current'").Scan(&evidence); err != nil || evidence != "db/assets/evidence.md" {
		t.Fatalf("live database evidence=%q, err=%v", evidence, err)
	}
	if raw, err := os.ReadFile(filepath.Join(docs, workflow, evidence)); err != nil || string(raw) != "Current evidence" {
		t.Fatalf("live asset was not saved: %s (%v)", raw, err)
	}
	if _, err := os.Stat(filepath.Join(docs, workflow, "runs/run-evidence/execution/save-evidence/result.json")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(docs, workflow, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "test-") && entry.Name() != "test-historical" {
			t.Fatalf("a new simulated run was created: %s", entry.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(docs, workflow, "runs/test-historical/test_mode.json")); err != nil {
		t.Fatalf("historical receipt was removed: %v", err)
	}
}

func workflowHTTPTestWorkspace(t *testing.T) (string, *httptest.Server) {
	t.Helper()
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("NATIVE_WORKSPACE", "true")
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Any("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.POST("/api/folders", workspacehandlers.CreateFolder)
	router.DELETE("/api/folders/*folderpath", workspacehandlers.DeleteFolder)
	router.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	router.POST("/api/mutate", workspacehandlers.MutateWorkflowDB)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)

	return docs, server
}

// PLAT-746-388: a migrated workflow's next step must read its predecessor's
// physical runtime output through the same logical workspace HTTP interface.
func TestBoundWorkflowStepOutputFeedsNextStep(t *testing.T) {
	docs, server := workflowHTTPTestWorkspace(t)
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(state, "ownership/projects.json"), `{"projects":{"work/example":{"product":"work","folder":"example","project_id":"crew","owner_id":"owner"}}}`)
	key := fmt.Sprintf("%x.json", sha256.Sum256([]byte("Workflow/demo")))
	write(filepath.Join(state, "ownership/crew-executables", key), `{"id":"demo","crew_id":"crew","crew_root":"Crew/example","workspace_path":"Workflow/demo","kind":"workflow"}`)
	if err := os.MkdirAll(filepath.Join(docs, "Crew/example"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docs, "_system/crew-data/crew"), 0700); err != nil {
		t.Fatal(err)
	}
	const execution = "Workflow/demo/runs/current/execution"
	physical := workflowAbsPath(execution, "producer/result.json")
	write(physical, `{"ready":true}`)
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/demo")
	base.WorkspaceClient = workspace.NewClient(server.URL)
	c := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	steps := []PlanStepInterface{
		&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "producer", ContextOutput: FlexibleContextOutput("result.json")}},
		&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "consumer", ContextDependencies: []string{"result.json"}}},
	}
	paths := c.resolveDependencyPathsWithWorkspace(context.Background(), []string{"result.json"}, 1, "step-2", steps, execution, docs, nil)
	if len(paths) != 1 || paths[0] != physical {
		t.Fatalf("dependency paths: %v, want %s", paths, physical)
	}
	content, err := c.formatContextDependenciesWithContent(context.Background(), paths, docs)
	if err != nil || !strings.Contains(content, `{"ready":true}`) {
		t.Fatalf("next step lost producer output: %q, %v", content, err)
	}
	// Existence selection must skip a missing candidate instead of always taking
	// the first declared producer, which used to hide this same mapping error.
	steps = append([]PlanStepInterface{&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "missing", ContextOutput: FlexibleContextOutput("result.json")}}}, steps...)
	paths = c.resolveDependencyPathsWithWorkspace(context.Background(), []string{"result.json"}, 2, "step-3", steps, execution, docs, nil)
	if len(paths) != 1 || paths[0] != physical {
		t.Fatalf("existing producer not selected: %v", paths)
	}
	validation, err := validateFilePath(execution+"/consumer", physical)
	if err != nil || validation != execution+"/producer/result.json" {
		t.Fatalf("validation lost runtime identity: %q, %v", validation, err)
	}
	if _, err := os.Stat(filepath.Join(docs, "Workflow/demo")); !os.IsNotExist(err) {
		t.Fatal("retired physical workflow was recreated")
	}
}
