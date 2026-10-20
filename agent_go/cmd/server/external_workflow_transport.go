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
	if claims != nil && claims.ExternalBuilderOperationID != "" {
		// Same Builder tool surface as the website, projected onto the bridge
		// because a token-backed session has no usable shell transport.
		return externalProjectTransport(claims, gate)
	}
	if claims == nil || claims.AccessToken == nil || !workflowRun {
		return true, nil
	}
	for _, name := range agentworksproduct.RunTools() {
		if !accessTokenRunToolDenied(claims, name) && gate.Allows(name) {
			directBridge = append(directBridge, name)
		}
	}
	return false, directBridge
}

// A token-backed chat has no usable shell transport. Project the gate's tools
// onto the native bridge; the gate already carries the Run restrictions when
// the caller's role is Run, and execution-time checks remain in force.
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
		if gate.Allows(name) {
			directBridge = append(directBridge, name)
		}
	}
	sort.Strings(directBridge)
	return false, directBridge
}
