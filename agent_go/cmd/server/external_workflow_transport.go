package server

import "github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"

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
