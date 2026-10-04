package step_based_workflow

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pythontools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func (hcpo *StepBasedWorkflowOrchestrator) bindPythonTools(config *agents.OrchestratorAgentConfig, pythonTools []pythontools.Tool, tools *[]llmtypes.Tool, executors map[string]interface{}) error {
	// Capture the step's env/identity injection AND its explicit folder guard.
	// Unknown custom tools otherwise pass straight through the base wrapper;
	// calling a raw shell inside one would bypass its context guard injection.
	guarded := hcpo.WrapWorkspaceToolsWithExplicitPaths(config.FolderGuardReadPaths, config.FolderGuardWritePaths, executors)
	shell, ok := guarded["execute_shell_command"].(pythontools.Executor)
	if !ok || shell == nil {
		return fmt.Errorf("Python tools require the shared execute_shell_command executor")
	}
	reserved := map[string]bool{}
	for _, tool := range hcpo.WorkspaceTools {
		if tool.Function != nil {
			reserved[tool.Function.Name] = true
		}
	}
	for _, tool := range *tools {
		if tool.Function != nil {
			reserved[tool.Function.Name] = true
		}
	}
	for _, tool := range pythonTools {
		if reserved[tool.Name] {
			return fmt.Errorf("Python tool %q collides with a platform tool; choose another name", tool.Name)
		}
		reserved[tool.Name] = true
		source := filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), tool.Directory(), "main.py")
		config.DirectTools = append(config.DirectTools, mcpagent.ToolDefinition{
			Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters,
			Execute:      tool.Bind(shell, source, filepath.Dir(source)),
			Timeout:      time.Duration(tool.TimeoutSeconds) * time.Second,
			DisplayGroup: pythontools.Category,
		})
	}
	return nil
}

// Both sequence item guards and construction guards need the selected source
// paths. Session guards are refreshed on every item and take precedence over
// the immutable tool wrapper's context, so a construction-only grant is lost.
func appendPythonToolReadPaths(readPaths []string, workspace string, config *AgentConfigs) []string {
	if config == nil {
		return readPaths
	}
	names, err := pythontools.SelectedNames(config.EnabledCustomTools)
	if err != nil {
		return readPaths // Invalid selections fail Load; never grant unsafe paths.
	}
	for _, name := range names {
		readPaths = append(readPaths, filepath.Join(workspace, "code", "tools", name))
	}
	return readPaths
}
