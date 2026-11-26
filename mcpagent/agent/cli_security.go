package mcpagent

import (
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// withCLISecurityPolicy attaches an application-resolved launch policy to the
// Agent. The policy is copied immediately and again for each provider call so
// later configuration changes cannot widen an already-running session.
func withCLISecurityPolicy(policy llmtypes.CLISecurityPolicy) agentOption {
	resolved := policy.Clone()
	return func(a *Agent) {
		copyPolicy := resolved.Clone()
		a.cliSecurityPolicy = &copyPolicy
	}
}

func (a *Agent) appendCLISecurityPolicyOption(opts []llmtypes.CallOption, provider llm.Provider) []llmtypes.CallOption {
	if a == nil || a.cliSecurityPolicy == nil {
		return opts
	}
	policy := a.cliSecurityPolicy.Clone()
	// Linked-output steps carry a policy narrowed by the application to their
	// own guard. Keep the private home in this session's runtime too, rather
	// than reusing the parent chat's home and native history.
	if a.codingAgentOutputDir != "" && a.isolatedWorkspacePath != "" {
		policy.WorkspaceWritePaths = append(policy.WorkspaceWritePaths, a.isolatedWorkspacePath)
		if policy.PrivateHome != "" {
			policy.PrivateHome = filepath.Join(a.isolatedWorkspacePath, ".sandbox-cache", "cli-home", string(provider))
		}
	}
	// Provider identity comes from the trusted provider selected by AgentWorks,
	// never from a model-authored policy value.
	policy.Provider = strings.ToLower(strings.TrimSpace(string(provider)))
	return append(opts, llm.WithCLISecurityPolicy(policy))
}
