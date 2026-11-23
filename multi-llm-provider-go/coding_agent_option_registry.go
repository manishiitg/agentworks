package llmproviders

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func normalizeCodingAgentProvider(provider Provider) Provider {
	return Provider(strings.ToLower(strings.TrimSpace(string(provider))))
}

// codingAgentInteractiveSessionRegistry maps a tmux coding-agent provider to
// the public "own this interactive session by app session id" CallOption.
var codingAgentInteractiveSessionRegistry = map[Provider]func(string) llmtypes.CallOption{
	ProviderClaudeCode: WithClaudeCodeInteractiveSessionID,
	ProviderCodexCLI:   WithCodexInteractiveSessionID,
	ProviderCursorCLI:  WithCursorInteractiveSessionID,
	ProviderPiCLI:      WithPiInteractiveSessionID,
	ProviderMuseCLI:    WithMuseInteractiveSessionID,
	ProviderAgyCLI:     WithAgyInteractiveSessionID,
}

// CodingAgentInteractiveSessionOption returns the CallOption that associates a
// provider's tmux session with ownerSessionID, or nil when unsupported.
func CodingAgentInteractiveSessionOption(provider Provider, ownerSessionID string) llmtypes.CallOption {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return nil
	}
	if fn, ok := codingAgentInteractiveSessionRegistry[normalizeCodingAgentProvider(provider)]; ok {
		return fn(ownerSessionID)
	}
	return nil
}

var codingAgentPersistentInteractiveRegistry = map[Provider]func(bool) llmtypes.CallOption{
	ProviderClaudeCode: WithClaudeCodePersistentInteractiveSession,
	ProviderCodexCLI:   WithCodexPersistentInteractiveSession,
	ProviderCursorCLI:  WithCursorPersistentInteractiveSession,
	ProviderPiCLI:      WithPiPersistentInteractiveSession,
	ProviderMuseCLI:    WithMusePersistentInteractiveSession,
	ProviderAgyCLI:     WithAgyPersistentInteractiveSession,
}

// CodingAgentPersistentInteractiveOption returns the provider's "keep tmux
// alive after turn completion" CallOption, or nil when unsupported.
func CodingAgentPersistentInteractiveOption(provider Provider, enabled bool) llmtypes.CallOption {
	if fn, ok := codingAgentPersistentInteractiveRegistry[normalizeCodingAgentProvider(provider)]; ok {
		return fn(enabled)
	}
	return nil
}

var codingAgentWorkingDirRegistry = map[Provider]func(string) llmtypes.CallOption{
	ProviderClaudeCode: WithClaudeCodeWorkingDir,
	ProviderCodexCLI:   WithCodexProjectDirID,
	ProviderCursorCLI:  WithCursorWorkingDir,
	ProviderPiCLI:      WithPiWorkingDir,
	ProviderMuseCLI:    WithMuseWorkingDir,
	ProviderAgyCLI:     WithAgyWorkingDir,
}

// CodingAgentWorkingDirOption returns the provider's working-directory
// CallOption. Codex uses its historical ProjectDirID option as the cwd flag.
func CodingAgentWorkingDirOption(provider Provider, workingDir string) llmtypes.CallOption {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return nil
	}
	if fn, ok := codingAgentWorkingDirRegistry[normalizeCodingAgentProvider(provider)]; ok {
		return fn(workingDir)
	}
	return nil
}

var codingAgentProjectDirIDRegistry = map[Provider]func(string) llmtypes.CallOption{
	ProviderCodexCLI: WithCodexProjectDirID,
}

// CodingAgentProjectDirIDOption returns an option for provider-native project
// directory/session identifiers. This is distinct from the working directory:
// Codex's historical option name doubles as cwd.
func CodingAgentProjectDirIDOption(provider Provider, projectDirID string) llmtypes.CallOption {
	projectDirID = strings.TrimSpace(projectDirID)
	if projectDirID == "" {
		return nil
	}
	if fn, ok := codingAgentProjectDirIDRegistry[normalizeCodingAgentProvider(provider)]; ok {
		return fn(projectDirID)
	}
	return nil
}

var codingAgentProjectInstructionOnlyRegistry = map[Provider]func(bool) llmtypes.CallOption{
	ProviderClaudeCode: WithClaudeCodeProjectInstructionOnly,
	ProviderCodexCLI:   WithCodexProjectInstructionOnly,
	ProviderMuseCLI:    WithMuseProjectInstructionOnly,
}

// CodingAgentProjectInstructionOnlyOption returns the provider option that
// prevents duplicate prompt injection when the adapter projects the prompt into
// a CLI-native project instruction file. Not every coding-agent provider needs
// this: Cursor already uses its rules file as the single prompt channel,
// and Pi uses an explicit append-system-prompt path. Muse needs it because it
// has no system-prompt flag at all — without the file the whole preamble is
// typed into the TUI on every turn.
func CodingAgentProjectInstructionOnlyOption(provider Provider, enabled bool) llmtypes.CallOption {
	if fn, ok := codingAgentProjectInstructionOnlyRegistry[normalizeCodingAgentProvider(provider)]; ok {
		return fn(enabled)
	}
	return nil
}
