package agentworksproduct

import "github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"

// ExecutionFunctionTools returns the product-owned admission and display group
// for workflow run agents, independently of Builder/Run chat tools.
func ExecutionFunctionTools() agentprofiles.ExecutionFunctionTools {
	if def := mustAgentWorksManifest().Execution.FunctionTools; def != nil {
		return agentprofiles.ExecutionFunctionTools{Category: def.Category, Tools: append([]string(nil), def.Tools...)}
	}
	return agentprofiles.ExecutionFunctionTools{}
}
