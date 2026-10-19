package server

import (
	"sort"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
)

// Token-backed Run conversations cannot use the shell to reach registered
// tools. Project their existing Run surface onto the direct model/CLI bridge.
// The resolved workflow mode and registration gate remain authoritative;
// tool execution still uses the authenticated session's normal permission checks.
func externalWorkflowTransport(claims *UserClaims, workflowRun bool, gate *productToolGate) (codeExecution bool, directBridge []string) {
	if claims == nil || claims.ExternalBuilderOperationID != "" || claims.AccessToken == nil || !workflowRun {
		return externalBuilderTransport(claims)
	}
	for _, name := range agentworksproduct.RunTools() {
		if !accessTokenRunToolDenied(claims, name) && gate.Allows(name) {
			directBridge = append(directBridge, name)
		}
	}
	return false, directBridge
}

// A token-backed Crew chat has the same unavailable shell as a workflow Run
// chat. Project only its existing product allowlist onto the native bridge;
// registration, Run restrictions and execution-time checks remain in force.
func externalProjectTransport(claims *UserClaims, gate *productToolGate) (codeExecution bool, directBridge []string) {
	if claims == nil || claims.AccessToken == nil || !gate.enforcing() {
		return true, nil
	}
	gate.mu.Lock()
	names := make([]string, 0, len(gate.allowed))
	for name := range gate.allowed {
		names = append(names, name)
	}
	gate.mu.Unlock()
	for _, name := range names {
		if gate.Allows(name) && !accessTokenRunToolDenied(claims, name) {
			directBridge = append(directBridge, name)
		}
	}
	sort.Strings(directBridge)
	return false, directBridge
}
