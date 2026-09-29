package server

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

// currentCodingAgentModel maps a retired model a project or workflow saved to
// the model that replaced it, so a retirement never breaks saved settings.
func currentCodingAgentModel(provider, modelID string) string {
	if strings.EqualFold(strings.TrimSpace(provider), "claude-code") {
		return claudecode.CurrentClaudeCodeModel(modelID)
	}
	return modelID
}
