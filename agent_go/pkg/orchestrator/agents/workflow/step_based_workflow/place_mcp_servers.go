package step_based_workflow

import (
	"context"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// PlaceMCPServers returns a workflow's own MCP connections (added there by
// someone who can edit it, with their login) as server names plus complete
// configs. The server installs it; nil means none. Step agents get them next
// to the workflow's selected servers, like any other server.
var PlaceMCPServers func(ctx context.Context, workspacePath string) ([]string, mcpclient.RuntimeOverrides)

// addPlaceMCPServers adds the workflow's own connections to a step agent.
func (hcpo *StepBasedWorkflowOrchestrator) addPlaceMCPServers(config *agents.OrchestratorAgentConfig) {
	if PlaceMCPServers == nil || config == nil {
		return
	}
	names, overrides := PlaceMCPServers(context.Background(), hcpo.GetWorkspacePath())
	if len(names) == 0 {
		return
	}
	merged := make([]string, 0, len(config.ServerNames)+len(names))
	for _, name := range config.ServerNames {
		if name != mcpclient.NoServers {
			merged = append(merged, name)
		}
	}
	config.ServerNames = append(merged, names...)
	if config.RuntimeOverrides == nil {
		config.RuntimeOverrides = mcpclient.RuntimeOverrides{}
	}
	for name, override := range overrides {
		config.RuntimeOverrides[name] = override
	}
}
