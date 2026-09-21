package common

import "strings"

// cliProviders is the canonical set of LLM provider IDs that run as CLI
// agents. These providers expose tools through the local CLI's MCP server
// rather than the standard tool-calling API, and they ship CLI-specific
// concerns the rest of the codebase needs to detect:
//   - prompt template variants (the CLI's own tool-calling docs)
//   - HTTP api-bridge tool mapping (mcp__api-bridge__*)
//   - native conversation/context management (the CLI maintains its own history)
//
// Add new CLI runtime providers here only when prompt/tool mapping needs to
// treat the entire provider as CLI-native. The stricter coding-agent capability
// contract lives in multi-llm-provider-go's coding_agent_contract.go and is
// exposed through llm.IsCodingAgentProvider / llm.IsTmuxCodingAgentProvider.
// Use that contract for transport, live input, tmux, cleanup, and resume
// behavior.
//
// NOTE: This is *only* the "is this a CLI runtime?" question. It is **not**
// the "should this agent run in code-execution mode?" question — code-exec
// mode is now always on regardless of provider, so call sites that decide
// `UseCodeExecutionMode` should just set it to true unconditionally.
var cliProviders = map[string]struct{}{
	"claude-code": {},
	"codex-cli":   {},
	"cursor-cli":  {},
	"pi-cli":      {},
	"muse-cli":    {},
	"agy-cli":     {},
}

// IsCLIProvider reports whether the given provider ID is a CLI agent
// runtime (claude-code, codex-cli, cursor-cli, pi-cli, muse-cli, agy-cli).
// The lookup is case-insensitive and whitespace-trimmed for resilience against
// config drift.
func IsCLIProvider(provider string) bool {
	_, ok := cliProviders[strings.ToLower(strings.TrimSpace(provider))]
	return ok
}
