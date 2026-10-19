package step_based_workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestSavedScriptUsesStepToolsWithoutBorrowingRunChatTools(t *testing.T) {
	t.Setenv("MCP_API_URL", "http://example.test")
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/example")
	base.ToolCategories = map[string]string{"query_workflow_db": "workflow_db", "mutate_workflow_db": "workflow_db"}
	base.WorkspaceToolExecutors = map[string]interface{}{}
	for _, name := range []string{"query_workflow_db", "mutate_workflow_db"} {
		base.WorkspaceTools = append(base.WorkspaceTools, llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{Name: name}})
		base.WorkspaceToolExecutors[name] = func(context.Context, map[string]interface{}) (string, error) { return "own workflow", nil }
	}
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, httpSessionID: "pinned-run-without-db-tools"}
	step := &ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "measure"}}
	guard := &workspace.FolderGuardConfig{ReadPaths: []string{"Workflow/example"}, WritePaths: []string{"Workflow/example/runs/current"}}
	for _, access := range []string{DBAccessRead, DBAccessReadWrite, DBAccessNone} {
		t.Run(access, func(t *testing.T) {
			env := map[string]string{"STEP_OUTPUT_DIR": "/workspace/output", "STEP_EXECUTION_DIR": "/workspace", "RUN_FOLDER": "iteration-1/default"}
			release := hcpo.configureScriptBridgeSession(step, guard, access, env)
			defer release()
			session := env["MCP_SESSION_ID"]
			if session == hcpo.httpSessionID || !strings.HasSuffix(env["MCP_API_URL"], "/s/"+session) {
				t.Fatal("script must use its own registered bridge session")
			}
			for _, name := range []string{"query_workflow_db", "mutate_workflow_db"} {
				got, callErr := codeexec.CallCustomToolWithSession(context.Background(), session, name, nil)
				allowed := access != DBAccessNone && (name == "query_workflow_db" || access == DBAccessReadWrite)
				if allowed && (callErr != nil || got != "own workflow") || !allowed && callErr == nil {
					t.Fatalf("access=%s tool=%s got=%q error=%v", access, name, got, callErr)
				}
			}
			if got := common.GetSessionShellConfig(session).Env[workflowDBAccessEnv]; got != access {
				t.Fatalf("DB capability changed: got %q want %q", got, access)
			}
		})
	}
}
