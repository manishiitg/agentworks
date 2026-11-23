package llmproviders

import (
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

// CodingAgentPaneReady reports whether a coding-agent TUI screen is at its
// idle input composer. The provider adapters remain the canonical owners of
// prompt, approval, queued-input, and active-status recognition; host runtimes
// should not duplicate those provider-specific rules.
func CodingAgentPaneReady(provider Provider, captured string) bool {
	provider = Provider(strings.ToLower(strings.TrimSpace(string(provider))))
	switch provider {
	case ProviderCursorCLI:
		return cursorcli.PaneReadyForInput(captured)
	case ProviderClaudeCode:
		return claudecodeadapter.PaneReadyForInput(captured)
	case ProviderCodexCLI:
		return codexcli.PaneReadyForInput(captured)
	case ProviderAgyCLI:
		return agycli.PaneReadyForInput(captured)
	default:
		return false
	}
}
