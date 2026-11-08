package server

import (
	"sort"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
)

// Token-backed chats (MCP `ask`, smoke tests, external Builder operations)
// take the website route when the model runs as a coding CLI over the MCP
// bridge: the bridge already offers search_tools, get_api_spec and call_tool,
// and call_tool reaches only tools the gate admitted at registration, through
// the same per-session HTTP route and execution checks as a direct bridge
// tool. Listing every allowed tool as a direct tool only bloated the catalog.
//
// A model without the bridge (an API model) has no call_tool: in code
// execution mode it reaches tools only through execute_shell_command, which a
// Run token is denied. Those chats keep the gate's tools as direct tools.
func externalWorkflowTransport(claims *UserClaims, workflowRun bool, gate *productToolGate, bridgeProvider bool) (codeExecution bool, directBridge []string) {
	if claims == nil || claims.AccessToken == nil || !workflowRun || bridgeProvider {
		return true, nil
	}
	for _, name := range agentworksproduct.RunTools() {
		if !accessTokenRunToolDenied(claims, name) && gate.Allows(name) {
			directBridge = append(directBridge, name)
		}
	}
	return false, directBridge
}

// externalProjectTransport: see externalWorkflowTransport. Without the bridge,
// project the gate's tools onto direct tools; the gate already carries the Run
// restrictions when the caller's role is Run, and execution-time checks remain.
func externalProjectTransport(claims *UserClaims, gate *productToolGate, bridgeProvider bool) (codeExecution bool, directBridge []string) {
	if claims == nil || claims.AccessToken == nil || !gate.enforcing() || bridgeProvider {
		return true, nil
	}
	gate.mu.Lock()
	names := make([]string, 0, len(gate.allowed))
	for name := range gate.allowed {
		names = append(names, name)
	}
	gate.mu.Unlock()
	for _, name := range names {
		if gate.Allows(name) {
			directBridge = append(directBridge, name)
		}
	}
	sort.Strings(directBridge)
	return false, directBridge
}

// usesMCPBridge mirrors mcpagent's rule for a coding CLI that reaches tools
// through the MCP bridge (and so has call_tool).
func usesMCPBridge(provider, modelID string) bool {
	contract, ok := codingAgentProviderContract(provider, modelID)
	return ok && contract.UsesMCPBridge
}
