package llmproviders

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

// nativeResumeRegistry maps a coding-agent provider to its public
// "resume this session by id" CallOption helper. A provider has a non-nil
// entry HERE if and only if its contract.SupportsNativeResume is true.
// The drift test in coding_agent_contract_test.go enforces this exact
// agreement so the contract can never silently misrepresent the wiring.
//
// To add native resume support for a new CLI:
//  1. Wire the four upstream layers:
//     - Add an XxxSessionID field on mcpagent.Agent.
//     - Have the adapter parse the CLI's session id (e.g. from stream-json)
//     and write it onto the agent after each turn.
//     - Add a public llmproviders.WithXxxResumeSessionID re-export here.
//     - Add a `case "xxx-cli":` arm to server.go's restore switch
//     (currently around line 6248) and to codingAgentHasNativeResume.
//  2. Add the provider here, pointing at the public option func.
//  3. Flip contract.SupportsNativeResume to true.
//
// Steps 2 and 3 must move together; the drift test fails otherwise.
var nativeResumeRegistry = map[Provider]func(sessionID string) llmtypes.CallOption{
	ProviderClaudeCode: WithResumeSessionID,
	ProviderCodexCLI:   WithCodexResumeSessionID,
	ProviderCursorCLI:  WithCursorResumeSessionID,
	ProviderPiCLI:      WithPiResumeSessionID,
	ProviderMuseCLI:    WithMuseResumeSessionID,
	ProviderAgyCLI:     WithAgyResumeSessionID,
}

// NativeResumeOption returns the CallOption that resumes sessionID for the
// given provider, or nil if the provider does not support native resume.
// Callers that need to add resume to a request should prefer this over
// hard-coding the per-provider option func, so the registry stays the
// single source of truth.
func NativeResumeOption(provider Provider, sessionID string) llmtypes.CallOption {
	if fn, ok := nativeResumeRegistry[normalizeCodingAgentProvider(provider)]; ok {
		return fn(sessionID)
	}
	return nil
}
