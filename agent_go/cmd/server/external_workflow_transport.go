package server

import (
	"sort"
)

// Without the bridge,
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
