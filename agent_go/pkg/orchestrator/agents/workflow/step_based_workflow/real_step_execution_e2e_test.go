package step_based_workflow

import (
	"context"
	"database/sql"
	"encoding/json"
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
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/spf13/viper"
)

// The Builder's real single-step pipeline runs saved Python through the
// workspace HTTP server, writes the workflow database and saves durable assets.
// Obsolete fields in saved JSON cannot select another execution mode.
func TestBuilderStepExecutionUsesLiveDatabaseAndAssets(t *testing.T) {
	docs, server := workflowHTTPTestWorkspace(t)

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
	write("code/save-evidence/main.py", `import json, os, sqlite3
from pathlib import Path
assert 'AGENTWORKS_TEST_MODE' not in os.environ
assert os.environ['RUN_FOLDER'] == 'iteration-0/default'
db_path = Path(os.environ['DB_PATH'])
assert str(db_path).endswith('/Workflow/real-execution/db/db.sqlite')
asset = db_path.parent / 'assets' / 'evidence.md'
asset.parent.mkdir(parents=True, exist_ok=True)
asset.write_text('Current evidence')
with sqlite3.connect(db_path) as db:
    db.execute("INSERT INTO results VALUES ('current', 'db/assets/evidence.md')")
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
	c.SetWorkspaceEnvRef(map[string]string{})
	c.variableValues = map[string]string{}
	defer common.ClearSessionShellConfig(c.GetMCPSessionID())
	var options WorkshopExecuteOptions
	if err := json.Unmarshal([]byte(`{"RunFolder":"iteration-0/default","SavedScriptOnly":true,"test_mode":true,"TestRunID":"test-obsolete","RealRunFolder":"iteration-99/default"}`), &options); err != nil {
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
	if _, err := os.Stat(filepath.Join(docs, workflow, "runs/iteration-0/default/execution/save-evidence/result.json")); err != nil {
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
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)

	return docs, server
}
