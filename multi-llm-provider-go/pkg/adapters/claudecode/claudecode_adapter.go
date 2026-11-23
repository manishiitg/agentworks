package claudecode

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/modelspecs"
)

// Constants for custom metadata keys
const (
	MetadataKeyMCPConfig                  = "mcp_config"
	MetadataKeyDangerouslySkipPermissions = "dangerously_skip_permissions"
	// MetadataKeyPermissionMode selects Claude Code's native permission policy
	// (for example "auto"). It is intentionally separate from the legacy
	// dangerously-skip flag so callers can opt into provider-reviewed native
	// tools without also bypassing every approval.
	MetadataKeyPermissionMode        = "claude_code_permission_mode"
	MetadataKeyTools                 = "claude_code_tools"
	MetadataKeyAllowedTools          = "claude_code_allowed_tools"
	MetadataKeySettings              = "claude_code_settings"
	MetadataKeyMaxTurns              = "claude_code_max_turns"
	MetadataKeyResumeSessionID       = "claude_code_resume_session_id"
	MetadataKeyEffort                = "claude_code_effort"
	MetadataKeyInteractiveSessionID  = "claude_code_interactive_session_id"
	MetadataKeyPersistentInteractive = "claude_code_persistent_interactive"
	MetadataKeyWorkingDir            = "claude_code_working_dir"
	// MetadataKeyStructuredTransport selects `claude -p --output-format
	// stream-json` (per-turn, one-shot, no tmux) instead of the tmux
	// interactive transport.
	MetadataKeyStructuredTransport = "claude_code_structured_transport"
	// MetadataKeyStreamTranscript opts into streaming structured content
	// (assistant text + tool-call starts) mid-turn by tailing Claude Code's
	// own JSONL transcript. Set via WithStreamTranscript (default OFF).
	MetadataKeyStreamTranscript = "claude_code_stream_transcript"
	// MetadataKeyStreamTmuxScreen controls whether raw terminal-pane snapshots
	// are also pushed to the caller's StreamChan mid-turn. Set via
	// WithStreamTmuxScreen (default ON).
	MetadataKeyStreamTmuxScreen = "claude_code_stream_tmux_screen"
	// MetadataKeyWriteProjectInstructionFile is the ON-by-default feature
	// flag for projecting the per-session system prompt into
	// <workingDir>/CLAUDE.md, which the CLI auto-loads as project
	// instructions. writeProjectInstructionFromOptions returns true when the
	// key is absent, so leaving it unset enables projection; pass
	// WithWriteProjectInstructionFile(false) to opt out.
	//
	// This applies to the interactive (tmux) adapter only. The structured
	// adapter has no projection at all and passes the prompt through
	// --append-system-prompt on every invocation, so no CLAUDE.md is written
	// for a structured session and none should be expected on disk.
	//
	// The comment here previously said OFF-by-default, which contradicted both
	// the reader and the exported wrapper.
	MetadataKeyWriteProjectInstructionFile = "claude_code_write_project_instruction_file"
	// MetadataKeyProjectInstructionOnly is the OFF-by-default feature flag
	// that makes the adapter inject the per-session system prompt SOLELY via
	// <workingDir>/CLAUDE.md and skip the --system-prompt-file flag. Default
	// off: the prompt goes through --system-prompt-file as usual (and is also
	// projected to CLAUDE.md when WithWriteProjectInstructionFile is on).
	// With it on, the prompt is written only to CLAUDE.md — which Claude Code
	// auto-loads as project instructions — eliminating the doubled system
	// prompt that otherwise results from passing the same bytes both ways.
	// Pass WithProjectInstructionOnly(true) to enable. If the CLAUDE.md
	// projection is disabled or fails, the adapter falls back to
	// --system-prompt-file so the prompt is never lost.
	MetadataKeyProjectInstructionOnly = "claude_code_project_instruction_only"
	// MetadataKeyMCPReadyFile names a filesystem path that the session's MCP
	// server (e.g. the mcpagent bridge) creates once it has answered the CLI's
	// tools/list handshake — i.e. the moment the MCP tools are actually
	// connected and callable. On a FRESHLY created interactive session the
	// adapter waits (bounded) for this file to appear before sending the first
	// prompt, closing the cold-turn race where the model's first turn runs
	// before its tools have connected (Claude then fabricates tool calls as
	// text; Codex reports "tools unavailable"). The wait is skipped on reused
	// persistent sessions (already connected) and when the path is empty, and it
	// degrades to "proceed anyway" on timeout so it can never hang a turn. The
	// path MUST be unique per session (the writer allocates a fresh one each
	// launch) so a stale file from a prior session in the same workspace cannot
	// falsely satisfy the gate.
	MetadataKeyMCPReadyFile = "mcp_ready_file"
)

// ClaudeCodeAdapter implements the LLM interface for the Claude Code CLI.
type ClaudeCodeAdapter struct {
	modelID      string
	logger       interfaces.Logger
	providerName string
}

// NewClaudeCodeAdapter creates a new instance of the ClaudeCodeAdapter.
func NewClaudeCodeAdapter(apiKey string, modelID string, logger interfaces.Logger) *ClaudeCodeAdapter {
	return NewProviderClaudeCodeAdapter(apiKey, modelID, "claude-code", nil, logger)
}

// NewProviderClaudeCodeAdapter creates a Claude Code adapter with provider-specific
// environment overrides. This lets Claude-compatible providers reuse the same CLI path.
func NewProviderClaudeCodeAdapter(apiKey string, modelID string, providerName string, envOverrides map[string]string, logger interfaces.Logger) *ClaudeCodeAdapter {
	modelID = CurrentClaudeCodeModel(modelID)
	_ = apiKey
	_ = envOverrides
	return &ClaudeCodeAdapter{
		modelID:      modelID,
		logger:       logger,
		providerName: providerName,
	}
}

// WithMCPConfig sets the MCP configuration JSON string for the session.
func WithMCPConfig(config string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyMCPConfig] = config
	}
}

// WithMCPReadyFile sets the path the session's MCP server creates once it has
// answered the CLI's tools/list handshake. On a freshly created interactive
// session the adapter holds the first prompt until this file appears (bounded,
// degrades to proceed-anyway on timeout), so the model never runs its opening
// turn before its MCP tools have connected. See MetadataKeyMCPReadyFile.
func WithMCPReadyFile(path string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyMCPReadyFile] = path
	}
}

// WithClaudeCodeSettings sets the --settings flag to a JSON string or file path.
func WithClaudeCodeSettings(settings string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeySettings] = settings
	}
}

// WithDangerouslySkipPermissions enables the --dangerously-skip-permissions flag.
// CAUTION: This allows the agent to execute any tool without user confirmation.
func WithDangerouslySkipPermissions() llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyDangerouslySkipPermissions] = true
	}
}

// WithPermissionMode sets Claude Code's --permission-mode value. Use "auto"
// when the caller intentionally enables native tools and wants Claude Code's
// own safety classifier to decide which actions run automatically.
func WithPermissionMode(mode string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyPermissionMode] = strings.TrimSpace(mode)
	}
}

// WithClaudeCodeTools sets the --tools flag to whitelist specific tools.
// Note: Core tools (Bash, Read, Write, etc.) may persist even if not listed.
// Use "" to disable optional tools (like WebSearch) if desired.
func WithClaudeCodeTools(tools string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyTools] = tools
	}
}

// WithAllowedTools sets the --allowed-tools flag to whitelist specific tools
// from requiring permission confirmation.
// Example: "mcp__mcpbridge__*" to allow all tools from the mcpbridge server.
func WithAllowedTools(tools string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyAllowedTools] = tools
	}
}

// WithMaxTurns sets the --max-turns flag to limit the number of agentic turns.
// Claude Code exits with an error when the limit is reached.
func WithMaxTurns(maxTurns int) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyMaxTurns] = maxTurns
	}
}

// WithEffort sets the --effort flag for the Claude Code CLI session.
// Values: "low", "medium", "high", "max"
func WithEffort(level string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyEffort] = level
	}
}

// WithResumeSessionID sets the --resume flag with a session ID so the Claude Code CLI
// resumes an existing session instead of starting a new one.
func WithResumeSessionID(sessionID string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyResumeSessionID] = sessionID
	}
}

// WithClaudeStructuredTransport selects `claude -p --output-format stream-json`
// (per-turn, one-shot, no tmux) instead of the tmux interactive transport. See
// MetadataKeyStructuredTransport for when to use this.
func WithClaudeStructuredTransport(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyStructuredTransport] = enabled
	}
}

// WithInteractiveSessionID links an interactive Claude Code run to the owning
// application session so follow-up user input can be sent directly to the TUI.
func WithInteractiveSessionID(sessionID string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyInteractiveSessionID] = sessionID
	}
}

// WithWorkingDir sets the directory used to launch the Claude Code CLI process.
func WithWorkingDir(dir string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyWorkingDir] = dir
	}
}

// WithPersistentInteractiveSession keeps the tmux-backed Claude Code TUI alive
// across completed turns. It should only be used for interactive chat sessions;
// deterministic workflow runs should keep the default per-turn lifecycle.
func WithPersistentInteractiveSession(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyPersistentInteractive] = enabled
	}
}

// WithWriteProjectInstructionFile controls whether the adapter ALSO writes
// the per-session system prompt to <workingDir>/CLAUDE.md (Claude Code's
// project-instructions convention), in addition to the --system-prompt-file
// flag injection. ON by default; pass false to opt out.
//
// Useful because (a) the prompt is visible inside the workspace for
// debugging and transparency, and (b) downstream tooling (other agents,
// IDE plugins) that reads CLAUDE.md sees the same instructions.
//
// When this flag is on AND WithMCPConfig was also set (and
// MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS is set), the adapter ALSO
// projects the MCP servers JSON into <workingDir>/.mcp.json (Claude Code's
// project-scoped MCP convention), removed on cleanup.
func WithWriteProjectInstructionFile(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyWriteProjectInstructionFile] = enabled
	}
}

// WithProjectInstructionOnly makes the adapter inject the per-session system
// prompt SOLELY via <workingDir>/CLAUDE.md and skip the --system-prompt-file
// flag. OFF by default. Claude Code auto-loads CLAUDE.md as project
// instructions, so the prompt is still applied — but only once, avoiding the
// doubled system prompt that results from passing the same bytes through both
// --system-prompt-file and CLAUDE.md.
//
// Requires the CLAUDE.md projection to be active (it is, by default; see
// WithWriteProjectInstructionFile) and a non-empty working dir. If the
// projection is disabled or the write fails, the adapter falls back to
// --system-prompt-file so the prompt is never silently dropped.
func WithProjectInstructionOnly(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyProjectInstructionOnly] = enabled
	}
}

func ensureMetadata(opts *llmtypes.CallOptions) {
	if opts.Metadata == nil {
		opts.Metadata = &llmtypes.Metadata{Custom: make(map[string]interface{})}
	}
	if opts.Metadata.Custom == nil {
		opts.Metadata.Custom = make(map[string]interface{})
	}
}

// GenerateContent generates content using Claude Code through the tmux
// transport. The old `claude -p` stream-json path is no longer selected by the
// public adapter; direct callers keep this type for API compatibility while
// execution is delegated to ClaudeCodeInteractiveAdapter.
func (c *ClaudeCodeAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return NewClaudeCodeInteractiveAdapter(c.modelID, c.logger).GenerateContent(ctx, messages, options...)
}

// SearchWeb uses Claude Code's native WebSearch tool and returns the final text response.
func (c *ClaudeCodeAdapter) SearchWeb(ctx context.Context, query string, options ...llmtypes.CallOption) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	searchPrompt := "Use WebSearch to answer the following query.\n\n" + query
	searchOptions := append([]llmtypes.CallOption{}, options...)
	searchOptions = append(searchOptions, WithClaudeCodeTools("WebSearch"))
	searchOptions = append(searchOptions, WithAllowedTools("WebSearch"))
	// Transcript streaming defaults OFF (claudeInteractiveStreamTranscriptEnabled)
	// unless a caller opts in -- SearchWeb never did, so a caller passing a
	// stream channel saw an empty stream (no ToolCallStart, no content) even on
	// a genuine, successful WebSearch: the final answer comes back through a
	// path that doesn't depend on this flag, while the stream itself never got
	// told the search happened. Same root cause as codexcli's SearchWeb fix.
	searchOptions = append(searchOptions, WithStreamTranscript(true))

	resp, err := c.GenerateContent(ctx, []llmtypes.MessageContent{
		{
			Role: llmtypes.ChatMessageTypeHuman,
			Parts: []llmtypes.ContentPart{
				llmtypes.TextContent{Text: searchPrompt},
			},
		},
	}, searchOptions...)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Choices) == 0 {
		return "", fmt.Errorf("claude code web search returned no response")
	}

	content := strings.TrimSpace(resp.Choices[0].Content)
	if content == "" {
		return "", fmt.Errorf("claude code web search returned empty response")
	}
	return content, nil
}

// GetModelID returns the model ID.
func (c *ClaudeCodeAdapter) GetModelID() string {
	return c.modelID
}

// GetModelMetadata returns metadata for the model.
func (c *ClaudeCodeAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	modelID = CurrentClaudeCodeModel(modelID)
	if modelID == "" {
		modelID = c.modelID
	}
	providerName := c.providerName
	if providerName == "" {
		providerName = "claude-code"
	}
	return claudeCodeModelMetadata(modelID, providerName), nil
}

// claudeCodeModelMetadata is a model's entry in the shared table (modelspecs). Claude Code also reports the
// real context window at the end of each reply, which the caller prefers.
func claudeCodeModelMetadata(modelID, provider string) *llmtypes.ModelMetadata {
	if m, ok := modelspecs.For(modelID, provider); ok {
		return m
	}
	return &llmtypes.ModelMetadata{ModelID: modelID, Provider: provider, ModelName: "Claude Code", ContextWindow: 200000}
}

// WithStreamTranscript enables (or disables) mid-turn structured streaming —
// assistant text and tool-call starts pushed to the caller's StreamChan as the
// turn runs, recovered by tailing Claude Code's own JSONL transcript. Default OFF.
func WithStreamTranscript(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyStreamTranscript] = enabled
	}
}

// WithStreamTmuxScreen enables (or disables) pushing raw terminal-pane
// snapshots to the caller's StreamChan mid-turn. Default ON — pass false to
// suppress terminal-screen chunks for a design-first UI that only wants
// WithStreamTranscript's structured content.
func WithStreamTmuxScreen(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyStreamTmuxScreen] = enabled
	}
}
