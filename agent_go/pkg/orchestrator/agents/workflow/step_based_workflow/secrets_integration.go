package step_based_workflow

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
)

// GetEffectiveSecrets returns the secrets stored on the orchestrator.
// Unlike skills, secrets have no step-level override — they are always orchestrator-level.
func GetEffectiveSecrets(bo *orchestrator.BaseOrchestrator) []orchestrator.SecretEntry {
	return bo.GetSecrets()
}

// BuildWorkflowSecretPrompt builds the system prompt section with secret names only.
// Secret values must never be rendered into prompts or logs. Agents should read
// them from the injected environment at execution time.
func BuildWorkflowSecretPrompt(secrets []orchestrator.SecretEntry) string {
	if len(secrets) == 0 {
		return ""
	}

	names := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		names = append(names, "`"+secret.Name+"`")
	}
	return "\n## Secrets\n\nAvailable secret names: " + strings.Join(names, ", ") + ". Read values only from the authorized injected environment; never ask for, print, echo, log, save or hardcode them.\n"
}
