package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/spf13/viper"
)

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
	router.POST("/api/query", workspacehandlers.QueryWorkflowDB)
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
