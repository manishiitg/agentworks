package step_based_workflow

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pythontools"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestPythonToolsBindToStepIdentityGuardAndEnvironment(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/demo")
	base.ToolCategories = map[string]string{"execute_shell_command": "workspace_advanced"}
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	pythonTools, err := pythontools.Load(context.Background(), []string{"python_tools:lookup_customer"}, func(_ context.Context, file string) (string, error) {
		if strings.HasSuffix(file, "tool.json") {
			return `{"description":"Customer lookup","parameters":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}}`, nil
		}
		return "def run(input): return input", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	config := &agents.OrchestratorAgentConfig{
		MCPSessionID:          "python-tool-step",
		FolderGuardReadPaths:  []string{"Workflow/demo/code/tools/lookup_customer"},
		FolderGuardWritePaths: []string{"Workflow/demo/runs/iteration-0/default/execution/answer"},
	}
	executors := map[string]interface{}{"execute_shell_command": func(ctx context.Context, args map[string]interface{}) (string, error) {
		if !reflect.DeepEqual(ctx.Value(virtualtools.FolderGuardReadPathsKey), config.FolderGuardReadPaths) || !reflect.DeepEqual(ctx.Value(virtualtools.FolderGuardWritePathsKey), config.FolderGuardWritePaths) {
			t.Fatalf("Python executor bypassed its step guard")
		}
		if ctx.Value(common.ChatSessionIDKey) != config.MCPSessionID {
			t.Fatal("wrong step tool session")
		}
		env := args["extra_env"].(map[string]interface{})
		if env["SECRET_DB_URL"] != "test-secret" || env["STEP_OUTPUT_DIR"] != "/docs/output" {
			t.Fatal("step environment was lost")
		}
		if !strings.Contains(args["command"].(string), "/code/tools/lookup_customer/main.py") {
			t.Fatal("wrong tool source")
		}
		return `{"stdout":"{\"customer\":\"Ada\"}","exit_code":0}`, nil
	}}
	injectStepEnvIntoShellExecutor(executors, "/docs/output", "/docs/execution", "", "iteration-0/default", config.MCPSessionID, map[string]string{"SECRET_DB_URL": "test-secret"})
	tools := []llmtypes.Tool{{Type: "function", Function: &llmtypes.FunctionDefinition{Name: "execute_shell_command"}}}
	if err := hcpo.bindPythonTools(config, pythonTools, &tools, executors); err != nil {
		t.Fatal(err)
	}
	if len(config.DirectTools) != 1 || config.DirectTools[0].Name != "lookup_customer" || config.DirectTools[0].DisplayGroup != pythontools.Category {
		t.Fatalf("named tool not registered: %+v", config.DirectTools)
	}
	value, err := config.DirectTools[0].Execute(context.Background(), map[string]interface{}{"id": "123"})
	if err != nil || value != `{"customer":"Ada"}` {
		t.Fatalf("result=%q err=%v", value, err)
	}
	if len(base.ToolCategories) != 1 || len(base.WorkspaceTools) != 0 {
		t.Fatal("step tools mutated shared orchestrator state")
	}
	pythonTools[0].Name = "execute_shell_command"
	if err := hcpo.bindPythonTools(&agents.OrchestratorAgentConfig{}, pythonTools, &tools, executors); err == nil {
		t.Fatal("platform name collision accepted")
	}
}

func TestPythonToolSourceGrantSurvivesEverySequenceItem(t *testing.T) {
	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath("Workflow/demo")
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}
	config := &AgentConfigs{EnabledCustomTools: []string{"python_tools:lookup_customer"}, KnowledgebaseAccess: KBAccessNone, LearningsAccess: LearningsAccessNone}
	for _, access := range []MessageSequenceWriteAccess{{}, {Learnings: true}, {Knowledgebase: true}} {
		read, write := hcpo.setupMessageSequenceFolderGuard("step-1", "answer", config, access)
		if !slices.Contains(read, "Workflow/demo/code/tools/lookup_customer") {
			t.Fatal("refreshed item guard lost named tool source")
		}
		for _, granted := range write {
			if strings.Contains(granted, "/code") {
				t.Fatalf("sequence granted source writes: %s", granted)
			}
		}
		if slices.Contains(read, "Workflow/demo/code/tools/unselected") {
			t.Fatal("unselected tool was granted")
		}
	}
}
