package cursorcli

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

const (
	MetadataKeyStreamTranscript      = "cursor_stream_transcript"
	MetadataKeyStreamTmuxScreen      = "cursor_stream_tmux_screen"
	MetadataKeyCursorModel           = "cursor_model"
	MetadataKeyResumeSessionID       = "cursor_resume_session_id"
	MetadataKeyForce                 = "cursor_force"
	MetadataKeyAutoReview            = "cursor_auto_review"
	MetadataKeySandbox               = "cursor_sandbox"
	MetadataKeyMode                  = "cursor_mode"
	MetadataKeyWorkingDir            = "cursor_working_dir"
	MetadataKeyProjectConfig         = "cursor_project_config"
	MetadataKeyMCPConfig             = "cursor_mcp_config"
	MetadataKeyApproveMCPs           = "cursor_approve_mcps"
	MetadataKeyHeaders               = "cursor_headers"
	MetadataKeyPluginDirs            = "cursor_plugin_dirs"
	MetadataKeyAutoApproveWebSearch  = "cursor_auto_approve_web_search"
	MetadataKeyInteractiveSessionID  = "cursor_interactive_session_id"
	MetadataKeyPersistentInteractive = "cursor_persistent_interactive"
	MetadataKeyDenyBuiltinTools      = "cursor_deny_builtin_tools"
	MetadataKeyFullNativeTools       = "cursor_full_native_tools"
	// metadataKeySDKSubagents is set by the SDK lane only: its Task children run in the same confined process
	// and inherit the parent's tool allowlist (subagentInherit), so a Full CLI chat may use them.
	metadataKeySDKSubagents = "cursor_sdk_subagents"
)

// WithCursorModel sets the Cursor Agent CLI --model flag. Use "auto" to select
// Cursor's Auto router explicitly. The "cursor-cli" alias pins to the Runloop
// default model.
func WithCursorModel(model string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyCursorModel] = model
	}
}

// WithResumeSessionID resumes a native Cursor session.
func WithResumeSessionID(sessionID string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyResumeSessionID] = sessionID
	}
}

// WithForce enables Cursor Agent's --force flag.
func WithForce() llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyForce] = true
	}
}

// WithAutoReview enables Cursor's Smart Auto mode. Cursor's server-side
// classifier automatically runs safe native tool calls and asks for the rest;
// unlike --force it does not bypass every approval or hook.
func WithAutoReview() llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyAutoReview] = true
	}
}

// WithSandbox sets Cursor Agent's --sandbox mode. Supported values are
// "enabled" and "disabled".
func WithSandbox(mode string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeySandbox] = mode
	}
}

// WithMode sets Cursor Agent's --mode flag. Supported values are "plan" and
// "ask"; leave empty for normal agent mode.
//
// DEPRECATED FOR "ask" — prefer WithDenyBuiltinTools(true) instead.
// Ask mode is a conversational stance that hard-refuses natural-language
// write requests with "Switch to Agent mode and ask…", which makes it
// unsuitable for any chat surface that needs writes. The orchestrator no
// longer uses ask mode anywhere; the bridge-only-tools intent is now
// achieved via cursor hooks (https://cursor.com/docs/hooks) installed by
// WithDenyBuiltinTools. WithMode is retained because callers may still
// want "plan" mode, and "ask" remains a valid cursor CLI flag.
func WithMode(mode string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyMode] = mode
	}
}

// WithWorkingDir sets the Cursor Agent workspace/cwd.
func WithWorkingDir(dir string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyWorkingDir] = dir
	}
}

// WithProjectConfig writes a temporary/restored .cursor/cli.json in the
// workspace before launching Cursor Agent.
// WithCursorStructuredTransport selects the structured `--print
// --output-format stream-json` transport instead of tmux.

func WithProjectConfig(configJSON string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyProjectConfig] = configJSON
	}
}

// WithMCPConfig writes a temporary/restored .cursor/mcp.json in the workspace
// before launching Cursor Agent.
func WithMCPConfig(configJSON string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyMCPConfig] = configJSON
	}
}

// WithApproveMCPs enables Cursor Agent's --approve-mcps flag, which auto-
// accepts the "approve this MCP server?" TUI consent dialog so bridge tool
// calls do not stall waiting for a human operator in the cursor TUI.
//
// Always pair this with WithMCPConfig — without an MCP config there is
// nothing to approve, and the flag is a no-op.
func WithApproveMCPs() llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyApproveMCPs] = true
	}
}

// WithHeaders appends Cursor Agent -H/--header values. Each entry must use the
// Cursor CLI format "Name: Value".
func WithHeaders(headers []string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyHeaders] = headers
	}
}

// WithPluginDirs appends Cursor Agent --plugin-dir values.
func WithPluginDirs(dirs []string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyPluginDirs] = dirs
	}
}

// WithAutoApproveWebSearch allows terminal permission prompts for
// web search, web fetch, and opening URLs in an already user-initiated agent
// turn. It does not enable --force.
func WithAutoApproveWebSearch() llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyAutoApproveWebSearch] = true
	}
}

// WithInteractiveSessionID links a native process to the owning application session.
func WithInteractiveSessionID(sessionID string) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyInteractiveSessionID] = sessionID
	}
}

// WithPersistentInteractiveSession keeps the native Cursor process alive
// across completed chat turns.
func WithPersistentInteractiveSession(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyPersistentInteractive] = enabled
	}
}

// WithDenyBuiltinTools installs a per-session .cursor/hooks.json that
// denies cursor's built-in Shell and Read tools via the beforeShellExecution
// and beforeReadFile events. Cursor will then route those actions through
// the MCP bridge (api-bridge.execute_shell_command / api-bridge.read_file)
// instead — provided the bridge MCP config is also installed via
// WithMCPConfig. Cleanup restores any pre-existing hooks.json on session
// teardown so the operator's own hooks aren't disturbed.
//
// This is the "hard lever" for bridge-only tool usage. The "soft lever" is
// to coach the model via system prompt; that has slow-failing edges where
// cursor falls back to built-in tools. The hook denies the call before it
// runs, so the model has no choice but to use the MCP bridge.
func WithDenyBuiltinTools(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyDenyBuiltinTools] = enabled
	}
}

// WithFullNativeTools is the "Native agent tools" (Full CLI) variant of
// WithDenyBuiltinTools: Cursor's own shell, reads and edits run (the shell also
// deletes; the Delete tool always asks, so it stays denied), and
// its shell commands are approved by the hook (never --force, which would
// switch every hook off). Computer use, screen recording, image generation and
// every subagent, background or cloud agent stay denied: nested and remote
// agents would run outside the session's confinement and bridge. It implies
// WithDenyBuiltinTools(true) so the hooks are installed.
func WithFullNativeTools() llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyDenyBuiltinTools] = true
		opts.Metadata.Custom[MetadataKeyFullNativeTools] = true
	}
}

func cursorFullNativeFromOptions(opts *llmtypes.CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	enabled, _ := opts.Metadata.Custom[MetadataKeyFullNativeTools].(bool)
	return enabled
}

// WithStreamTranscript enables (or disables) mid-turn structured streaming —
// assistant text and tool-call starts pushed to the caller's StreamChan as the
// turn runs, recovered by polling Cursor's own store.db. Default OFF.

// WithStreamTmuxScreen enables (or disables) pushing raw terminal-pane
// snapshots to the caller's StreamChan mid-turn. Default ON — pass false to
// suppress terminal-screen chunks for a design-first UI that only wants
// WithStreamTranscript's structured content.

func ensureMetadata(opts *llmtypes.CallOptions) {
	if opts.Metadata == nil {
		opts.Metadata = &llmtypes.Metadata{Custom: make(map[string]interface{})}
	}
	if opts.Metadata.Custom == nil {
		opts.Metadata.Custom = make(map[string]interface{})
	}
}

// cursorSandboxUnderSeatbelt turns Cursor's own sandbox off when AgentWorks'
// Seatbelt already confines it on a Mac: macOS refuses a sandbox inside a
// sandbox. Seatbelt applies the folder limits from outside.
func cursorSandboxUnderSeatbelt(opts *llmtypes.CallOptions, sandbox string) string {
	if opts != nil && opts.CLISecurity.SeatbeltEnforced() {
		return "disabled"
	}
	return sandbox
}

// cursorBridgeServerNameFromOptions names the bridge server mounted for this call, or "".
func cursorBridgeServerNameFromOptions(opts *llmtypes.CallOptions) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	mcpJSON, _ := opts.Metadata.Custom[MetadataKeyMCPConfig].(string)
	return cursorBridgeServerName(mcpJSON)
}

func WithStreamTranscript(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyStreamTranscript] = enabled
	}
}
func WithStreamTmuxScreen(enabled bool) llmtypes.CallOption {
	return func(opts *llmtypes.CallOptions) {
		ensureMetadata(opts)
		opts.Metadata.Custom[MetadataKeyStreamTmuxScreen] = enabled
	}
}
