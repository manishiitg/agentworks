package step_based_workflow

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/spf13/viper"
)

// A real workspace HTTP server and Python processes exercise the Relay path:
// flat variables, independent invocations and resume without replaying step one.
func TestRelayGrouplessChainAndResumeRealWorkspace(t *testing.T) {
	if os.Getenv("RUN_RELAY_GROUPLESS_E2E") == "" {
		t.Skip("set RUN_RELAY_GROUPLESS_E2E=1 for real workspace/Python execution")
	}
	for _, kind := range []string{"relay", "workflow", "workflow-no-inputs"} {
		t.Run(kind, func(t *testing.T) { testGrouplessChainAndResume(t, kind) })
	}
}

func testGrouplessChainAndResume(t *testing.T, kind string) {
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
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	const workspacePath = "Workflow/relay-groupless-e2e"
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(docs, workspacePath, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("webhooks/deliveries/sample.json", `{}`)
	manifest := `{"code_layout_version":1}`
	if kind == "relay" {
		manifest = `{"kind":"relay","code_layout_version":1}`
	}
	write("workflow.json", manifest)
	if kind != "workflow-no-inputs" {
		write("variables/variables.json", `{"variables":[{"name":"INPUT","value":"{}"},{"name":"PREFIX","value":"Hello"}]}`)
	}
	write("planning/plan.json", `{"steps":[{"type":"scripted","id":"first","title":"First","description":"Create greeting","script_only":true,"next_step_id":"second","context_output":"result.json"},{"type":"scripted","id":"second","title":"Second","description":"Return greeting","script_only":true,"context_dependencies":["result.json"]}]}`)
	write("planning/step_config.json", `{"steps":[{"id":"first","agent_configs":{"lock_code":true}},{"id":"second","agent_configs":{"lock_code":true}}]}`)
	relayDBCheck := ""
	if kind == "relay" {
		relayDBCheck = "assert os.environ.get(\"WORKFLOW_DB_ACCESS\") == \"none\""
	}
	write("code/first/main.py", `import json, os
from pathlib import Path
assert not os.environ.get('VAR_GROUP_NAME')
`+relayDBCheck+`
out = Path(os.environ['STEP_OUTPUT_DIR'])
assert not (out / 'result.json').exists(), 'completed step was replayed'
value = json.loads(os.environ.get('VAR_INPUT', '{"name":"world"}'))
(out / 'result.json').write_text(json.dumps({'greeting': os.environ.get('VAR_PREFIX', 'Hello') + ' ' + value['name']}))
`)
	write("code/second/main.py", "raise RuntimeError('intentional failure before resume')\n")
	newController := func(folder, name, strategy string, resume int) *StepBasedWorkflowOrchestrator {
		t.Helper()
		c, err := NewStepBasedWorkflowOrchestrator(context.Background(), "", "", 0, "", nil, nil, false, "", &orchestrator.LLMConfig{}, 1, loggerv2.NewNoop(), nil, nil, nil, nil, nil, nil, nil, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		c.WorkspaceClient = workspace.NewClient(server.URL)
		c.SetWorkspaceEnvRef(map[string]string{})
		values := map[string]string{"INPUT": `{"name":"` + name + `"}`}
		if kind == "workflow-no-inputs" {
			values = nil
		}
		c.SetExecutionOptions(&ExecutionOptions{SelectedRunFolder: folder, ExecutionStrategy: strategy, ResumeFromStep: resume, WebhookInputFile: workspacePath + "/webhooks/deliveries/sample.json", WebhookVariables: values})
		return c
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := newController("iteration-1-hook", "Ada", ExecutionStrategyStartFromBeginningNoHuman, 0)
	if _, err := c.CreateTodoList(ctx, "", workspacePath); err == nil {
		t.Fatal("expected second step failure")
	} else {
		t.Logf("initial failure: %v", err)
	}
	firstPath := filepath.Join(docs, workspacePath, "runs/iteration-1-hook/execution/first/result.json")
	before, err := os.Stat(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	write("code/second/main.py", `import json, os, sys
from pathlib import Path
assert not os.environ.get('VAR_GROUP_NAME')
source = Path(sys.argv[1])
value = json.loads(source.read_text())
(Path(os.environ['STEP_OUTPUT_DIR']) / 'result.json').write_text(json.dumps(value))
`)
	c = newController("iteration-1-hook", "Ada", ExecutionStrategyResumeFromStepNoHuman, 2)
	if _, err := c.CreateTodoList(ctx, "", workspacePath); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(firstPath)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("resume changed completed output: %v", err)
	}
	if kind == "relay" {
		// Builder node tests also use flat config and a run ID without a group.
		c = newController("", "Lin", ExecutionStrategyStartFromBeginningNoHuman, 0)
		c.SetWorkspacePath(workspacePath)
		if _, err := c.ExecuteStepForWorkshop(ctx, "first", &WorkshopExecuteOptions{RunFolder: "iteration-3-hook", SavedScriptOnly: true}); err != nil {
			t.Fatal(err)
		}
		rawNode, err := os.ReadFile(filepath.Join(docs, workspacePath, "runs/iteration-3-hook/execution/first/result.json"))
		if err != nil || string(rawNode) != `{"greeting": "Hello Lin"}` {
			t.Fatalf("node output: %s (%v)", rawNode, err)
		}
	}
	for folder, name := range map[string]string{"iteration-1-hook": "Ada", "iteration-2-hook": "Grace"} {
		if folder == "iteration-2-hook" {
			c = newController(folder, name, ExecutionStrategyStartFromBeginningNoHuman, 0)
			if _, err := c.CreateTodoList(ctx, "", workspacePath); err != nil {
				t.Fatal(err)
			}
		}
		if kind == "workflow-no-inputs" {
			name = "world"
		}
		raw, err := os.ReadFile(filepath.Join(docs, workspacePath, "runs", folder, "execution/second/result.json"))
		if err != nil {
			t.Fatal(err)
		}
		var output map[string]string
		if err := json.Unmarshal(raw, &output); err != nil || output["greeting"] != "Hello "+name {
			t.Fatalf("output %s: %s (%v)", folder, raw, err)
		}
		raw, err = os.ReadFile(filepath.Join(docs, workspacePath, "runs", folder, "webhook_progress.json"))
		if err != nil {
			t.Fatal(err)
		}
		var progress map[string]WebhookProgressEntry
		if err := json.Unmarshal(raw, &progress); err != nil || len(progress) != 2 {
			t.Fatalf("progress: %s (%v)", raw, err)
		}
		for _, entry := range progress {
			if entry.Status != "completed" {
				t.Fatalf("unfinished progress: %+v", entry)
			}
		}
		if _, err := os.Stat(filepath.Join(docs, workspacePath, "runs", folder, "default")); !os.IsNotExist(err) {
			t.Fatal("synthetic group created")
		}
	}
}
