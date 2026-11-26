package llm

import (
	"context"
	"fmt"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/observability"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/interfaces"
)

// Re-export Provider type and constants from llm-providers
type Provider = llmproviders.Provider

const (
	ProviderBedrock           = llmproviders.ProviderBedrock
	ProviderOpenAI            = llmproviders.ProviderOpenAI
	ProviderAnthropic         = llmproviders.ProviderAnthropic
	ProviderOpenRouter        = llmproviders.ProviderOpenRouter
	ProviderVertex            = llmproviders.ProviderVertex
	ProviderAzure             = llmproviders.ProviderAzure
	ProviderZAI               = llmproviders.ProviderZAI
	ProviderKimi              = llmproviders.ProviderKimi
	ProviderClaudeCode        = llmproviders.ProviderClaudeCode
	ProviderCodexCLI          = llmproviders.ProviderCodexCLI
	ProviderCursorCLI         = llmproviders.ProviderCursorCLI
	ProviderPiCLI             = llmproviders.ProviderPiCLI
	ProviderMuseCLI           = llmproviders.ProviderMuseCLI
	ProviderAgyCLI            = llmproviders.ProviderAgyCLI
	ProviderMiniMax           = llmproviders.ProviderMiniMax
	ProviderMiniMaxCodingPlan = llmproviders.ProviderMiniMaxCodingPlan
	ProviderElevenLabs        = llmproviders.ProviderElevenLabs
	ProviderDeepgram          = llmproviders.ProviderDeepgram
)

const (
	ClaudeCodeTransportTmux = llmproviders.ClaudeCodeTransportTmux
)

type CodingAgentTransport = llmproviders.CodingAgentTransport
type CodingAgentProviderContract = llmproviders.CodingAgentProviderContract
type CodingAgentSecurityProfile = llmproviders.CodingAgentSecurityProfile
type CLISecurityMode = llmtypes.CLISecurityMode
type CLISecurityPolicy = llmtypes.CLISecurityPolicy

const (
	CodingAgentTransportTmux       = llmproviders.CodingAgentTransportTmux
	CodingAgentTransportStructured = llmproviders.CodingAgentTransportStructured
	CodingAgentTransportAppServer  = llmproviders.CodingAgentTransportAppServer
	CodingAgentTransportRPC        = llmproviders.CodingAgentTransportRPC
	CodingAgentTransportSDK        = llmproviders.CodingAgentTransportSDK
	CodingAgentTransportACP        = llmproviders.CodingAgentTransportACP
)

func GetCodingAgentProviderContract(provider Provider, modelID string) (CodingAgentProviderContract, bool) {
	return llmproviders.GetCodingAgentProviderContract(provider, modelID)
}

// GetCodingAgentProviderContractForTransport is the contract of a chat whose
// Claude Code runs on the transport it chose; empty uses the server's setting.
func GetCodingAgentProviderContractForTransport(provider Provider, modelID, claudeTransport string) (CodingAgentProviderContract, bool) {
	return llmproviders.GetCodingAgentProviderContractForTransport(provider, modelID, claudeTransport)
}

func IsCodingAgentProvider(provider Provider, modelID string) bool {
	return llmproviders.IsCodingAgentProvider(provider, modelID)
}

func IsTmuxCodingAgentProvider(provider Provider, modelID string) bool {
	return llmproviders.IsTmuxCodingAgentProvider(provider, modelID)
}

func CodingAgentProviderContracts() []CodingAgentProviderContract {
	return llmproviders.CodingAgentProviderContracts()
}

func CodingAgentSecurityProfiles() []CodingAgentSecurityProfile {
	return llmproviders.CodingAgentSecurityProfiles()
}

func GetCodingAgentSecurityProfile(provider Provider) (CodingAgentSecurityProfile, bool) {
	return llmproviders.GetCodingAgentSecurityProfile(provider)
}

func WithCLISecurityPolicy(policy CLISecurityPolicy) llmtypes.CallOption {
	return llmproviders.WithCLISecurityPolicy(policy)
}

func CodingAgentInteractiveSessionOption(provider Provider, ownerSessionID string) llmtypes.CallOption {
	return llmproviders.CodingAgentInteractiveSessionOption(provider, ownerSessionID)
}

func CodingAgentPersistentInteractiveOption(provider Provider, enabled bool) llmtypes.CallOption {
	return llmproviders.CodingAgentPersistentInteractiveOption(provider, enabled)
}

func CodingAgentWorkingDirOption(provider Provider, workingDir string) llmtypes.CallOption {
	return llmproviders.CodingAgentWorkingDirOption(provider, workingDir)
}

func CodingAgentProjectDirIDOption(provider Provider, projectDirID string) llmtypes.CallOption {
	return llmproviders.CodingAgentProjectDirIDOption(provider, projectDirID)
}

func CodingAgentProjectInstructionOnlyOption(provider Provider, enabled bool) llmtypes.CallOption {
	return llmproviders.CodingAgentProjectInstructionOnlyOption(provider, enabled)
}

func NativeResumeOption(provider Provider, sessionID string) llmtypes.CallOption {
	return llmproviders.NativeResumeOption(provider, sessionID)
}

type CodingAgentContinuationError = llmproviders.CodingAgentContinuationError
type CodingAgentContinuationErrorKind = llmproviders.CodingAgentContinuationErrorKind

const (
	CodingAgentContinuationErrorNonApplicable  = llmproviders.CodingAgentContinuationErrorNonApplicable
	CodingAgentContinuationErrorNonContinuable = llmproviders.CodingAgentContinuationErrorNonContinuable
	CodingAgentContinuationErrorStaleHandle    = llmproviders.CodingAgentContinuationErrorStaleHandle
)

func ContinueCodingAgentSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, message string, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return llmproviders.ContinueCodingAgentSession(ctx, model, handle, message, options...)
}

func StartCodingAgentTransportSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return llmproviders.StartCodingAgentTransportSession(ctx, model, handle, options...)
}

// CompactCodingAgentSession runs only the CLI's own compaction on the native
// session (idle compaction, PLAT-872).
func CompactCodingAgentSession(ctx context.Context, model llmtypes.Model, handle llmtypes.CodingProviderSessionHandle, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	return llmproviders.CompactCodingAgentSession(ctx, model, handle, options...)
}

func SendCodingAgentLiveInput(ctx context.Context, provider Provider, modelID, ownerSessionID, message string) error {
	return llmproviders.SendCodingAgentLiveInput(ctx, provider, modelID, ownerSessionID, message)
}

func SendCodingAgentRetainedInput(ctx context.Context, provider Provider, modelID, ownerSessionID, message string) error {
	return llmproviders.SendCodingAgentRetainedInput(ctx, provider, modelID, ownerSessionID, message)
}

// SendCodingAgentControlKey injects a tmux control key (e.g. "Escape", "C-c")
// into a currently running tmux-based coding-agent session. Mirrors
// SendCodingAgentLiveInput but sends a raw key instead of text.
func SendCodingAgentControlKey(ctx context.Context, provider Provider, modelID, ownerSessionID, key string) error {
	return llmproviders.SendCodingAgentControlKey(ctx, provider, modelID, ownerSessionID, key)
}

// IsAllowedCodingAgentControlKey reports whether the named tmux key is on the
// whitelist accepted by SendCodingAgentControlKey.
func IsAllowedCodingAgentControlKey(key string) bool {
	return llmproviders.IsAllowedCodingAgentControlKey(key)
}

func IsCodingAgentContinuationError(err error, kind CodingAgentContinuationErrorKind) bool {
	return llmproviders.IsCodingAgentContinuationError(err, kind)
}

const (
	MetadataKeyMCPConfig                  = "mcp_config"
	MetadataKeyDangerouslySkipPermissions = "dangerously_skip_permissions"
	MetadataKeyTools                      = "claude_code_tools"
)

// AwaitClaudeInputDurable waits for the transcript proof that a previous
// live input reached the Claude Code CLI. Host applications call it after
// the fast pane ack; a zero timeout selects the adapter's env-tuned budget.
func AwaitClaudeInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	return llmproviders.AwaitClaudeInputDurable(ctx, sessionID, message, timeout)
}

// AwaitCodexInputDurable waits for the rollout proof that a previous live
// input reached the Codex CLI. Host applications call it after the fast
// pane ack; a zero timeout selects the adapter's env-tuned budget.
func AwaitCodexInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	return llmproviders.AwaitCodexInputDurable(ctx, sessionID, message, timeout)
}

// AwaitPiInputDurable waits for the marker proof that a previous live
// input reached the Pi CLI. Host applications call it after the fast
// pane ack; a zero timeout selects the adapter's env-tuned budget.
func AwaitPiInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	return llmproviders.AwaitPiInputDurable(ctx, sessionID, message, timeout)
}

// AwaitMuseInputDurable waits for the transcript proof that a previous
// live input reached the Muse CLI. Host applications call it after the
// fast pane ack; a zero timeout selects the adapter's env-tuned budget.
func AwaitMuseInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	return llmproviders.AwaitMuseInputDurable(ctx, sessionID, message, timeout)
}

// AwaitAgyInputDurable confirms a live input against AGY's conversation SQLite.
func AwaitAgyInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	return llmproviders.AwaitAgyInputDurable(ctx, sessionID, message, timeout)
}

// CleanupPiCLIInteractiveSessions closes all tracked Pi CLI interactive
// sessions.
func CleanupPiCLIInteractiveSessions(ctx context.Context) error {
	return llmproviders.CleanupPiCLIInteractiveSessions(ctx)
}

// ClosePiCLIInteractiveSessionForOwner closes a tracked Pi CLI session by
// owning application session id.
func ClosePiCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	llmproviders.ClosePiCLIInteractiveSessionForOwner(ownerSessionID, reason)
}

// WithClaudeCodeWorkingDir sets the process working directory for Claude Code.
func WithClaudeCodeWorkingDir(dir string) llmtypes.CallOption {
	return llmproviders.WithClaudeCodeWorkingDir(dir)
}

// WithMuseStreamTranscript opts muse tmux turns into transcript streaming
// (assistant text, tool starts/ends, reasoning from session.jsonl).
func WithMuseStreamTranscript(enabled bool) llmtypes.CallOption {
	return llmproviders.WithMuseStreamTranscript(enabled)
}

// WithMuseStreamTmuxScreen opts muse tmux turns into raw pane snapshots
// (Terminal chunks, the mode1 view).
func WithMuseStreamTmuxScreen(enabled bool) llmtypes.CallOption {
	return llmproviders.WithMuseStreamTmuxScreen(enabled)
}

// WithCursorWorkingDir sets the Cursor Agent CLI workspace/cwd.
func WithCursorWorkingDir(dir string) llmtypes.CallOption {
	return llmproviders.WithCursorWorkingDir(dir)
}

// WithCursorMCPConfig writes a temporary/restored .cursor/mcp.json for Cursor.
func WithCursorMCPConfig(config string) llmtypes.CallOption {
	return llmproviders.WithCursorMCPConfig(config)
}

// WithMuseMCPConfig mounts the document's mcpServers in a per-launch private
// configuration directory, leaving shared Muse settings untouched.
func WithMuseMCPConfig(config string) llmtypes.CallOption {
	return llmproviders.WithMuseMCPConfig(config)
}

// WithAgyMCPConfig mounts the document's stdio mcpServers for one agy turn
// via `agy mcp add` (global user config: agy offers no scoped mount) and
// removes them afterwards. A mounted turn is tool-capable via
// --dangerously-skip-permissions, natives included.
func WithAgyMCPConfig(config string) llmtypes.CallOption {
	return llmproviders.WithAgyMCPConfig(config)
}

// WithAgyNativeToolsMode selects AGY's MCP-only or native read/search mode.
func WithAgyNativeToolsMode(mode string) llmtypes.CallOption {
	return llmproviders.WithAgyNativeToolsMode(mode)
}

// WithMuseToolAllowlist installs best-effort restrictions on unlisted native
// calls reaching PreToolUse. Internal session controls may bypass that hook.
// Separately mounted MCP tools remain available.
func WithMuseToolAllowlist(toolNames []string) llmtypes.CallOption {
	return llmproviders.WithMuseToolAllowlist(toolNames)
}

// WithCursorApproveMCPs enables Cursor Agent CLI's --approve-mcps flag, which
// auto-accepts the "approve this MCP server?" TUI consent dialog so bridge
// tool calls do not stall waiting for human approval. Only useful alongside
// WithCursorMCPConfig — without an MCP config there is nothing to approve.
func WithCursorApproveMCPs() llmtypes.CallOption {
	return llmproviders.WithCursorApproveMCPs()
}

// WithCursorAutoApproveWebSearch auto-accepts Cursor Agent CLI's TUI prompts
// for web search and opening URLs during an already-user-initiated agent turn.
// It does not enable --force.
func WithCursorAutoApproveWebSearch() llmtypes.CallOption {
	return llmproviders.WithCursorAutoApproveWebSearch()
}

// WithCursorDenyBuiltinTools installs a per-session .cursor/hooks.json that
// denies Cursor's built-in Shell/Read/Edit/Write/etc. tools at the hook
// layer, forcing the agent to route every tool call through the MCP bridge.
// This is the modern equivalent of --mode ask but without the conversational
// hard-refuse behavior — natural-language requests still work; only built-in
// tool calls get vetoed. Pairs cleanly with WithCursorMCPConfig +
// WithCursorApproveMCPs.
func WithCursorDenyBuiltinTools(enabled bool) llmtypes.CallOption {
	return llmproviders.WithCursorDenyBuiltinTools(enabled)
}

// WithCursorFullNativeTools lets Cursor's own shell, reads, edits and deletes
// run (Full CLI) and keeps subagents, cloud/background agents and computer use
// denied.
func WithCursorFullNativeTools() llmtypes.CallOption {
	return llmproviders.WithCursorFullNativeTools()
}

// WithPiResumeSessionID resumes a Pi native session created with --session-id.
func WithPiResumeSessionID(sessionID string) llmtypes.CallOption {
	return llmproviders.WithPiResumeSessionID(sessionID)
}

// WithPiWorkingDir sets the Pi CLI workspace/cwd.
func WithPiWorkingDir(dir string) llmtypes.CallOption {
	return llmproviders.WithPiWorkingDir(dir)
}

// WithPiProvider overrides the Pi upstream provider inferred from the model id.
func WithPiProvider(provider string) llmtypes.CallOption {
	return llmproviders.WithPiProvider(provider)
}

// WithPiMCPConfig records a Pi MCP config candidate.
func WithPiMCPConfig(config string) llmtypes.CallOption {
	return llmproviders.WithPiMCPConfig(config)
}

// WithPiBridgeOnlyTools disables Pi built-in tools while leaving extension and
// custom tools, including the MCP adapter, enabled.
func WithPiBridgeOnlyTools(enabled bool) llmtypes.CallOption {
	return llmproviders.WithPiBridgeOnlyTools(enabled)
}

// Config holds configuration for LLM initialization (agent_go version)
// This is kept for backward compatibility and converted to llm-providers Config internally
type Config struct {
	ConnectionID string
	Provider     Provider
	ModelID      string
	Temperature  float64
	Tracers      []observability.Tracer
	TraceID      observability.TraceID

	MaxRetries int
	// Logger for structured logging
	Logger loggerv2.Logger
	// Context for LLM initialization (optional, uses background with timeout if not provided)
	Context context.Context
	// API keys for providers (optional, falls back to environment variables if not provided)
	APIKeys *ProviderAPIKeys
	// ClaudeCodeTransport optionally overrides CLAUDE_CODE_TRANSPORT for this
	// initialized Claude Code model.
	ClaudeCodeTransport string
}

// ProviderAPIKeys is the canonical API key holder — aliased from multi-llm-provider-go.
// Add new provider fields to llmproviders.ProviderAPIKeys, not here.
type ProviderAPIKeys = llmproviders.ProviderAPIKeys

// Music generation types are re-exported for agent packages that depend on mcpagent/llm.
type MusicGenerationModel = llmtypes.MusicGenerationModel
type MusicGenerationResponse = llmtypes.MusicGenerationResponse
type GeneratedMusic = llmtypes.GeneratedMusic
type MusicGenerationOptions = llmtypes.MusicGenerationOptions
type MusicGenerationOption = llmtypes.MusicGenerationOption

// AzureAPIConfig is aliased from multi-llm-provider-go.
type AzureAPIConfig = llmproviders.AzureAPIConfig

// BedrockConfig is aliased from multi-llm-provider-go.
type BedrockConfig = llmproviders.BedrockConfig

// LoggerAdapter adapts v2.Logger to interfaces.Logger
type LoggerAdapter struct {
	logger loggerv2.Logger
}

// NewLoggerAdapter creates a new logger adapter
func NewLoggerAdapter(logger loggerv2.Logger) *LoggerAdapter {
	return &LoggerAdapter{logger: logger}
}

// Infof implements interfaces.Logger
func (l *LoggerAdapter) Infof(format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Info(fmt.Sprintf(format, v...))
}

// Errorf implements interfaces.Logger
func (l *LoggerAdapter) Errorf(format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Error(fmt.Sprintf(format, v...), nil)
}

// Debugf implements interfaces.Logger
func (l *LoggerAdapter) Debugf(format string, args ...interface{}) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Debug(fmt.Sprintf(format, args...))
}

// convertConfig converts agent_go Config to llm-providers Config
func convertConfig(config Config) llmproviders.Config {
	// Create EventEmitterAdapter from tracers
	var eventEmitter interfaces.EventEmitter
	if len(config.Tracers) > 0 {
		eventEmitter = NewEventEmitterAdapter(config.Tracers)
	} else {
		// Create a no-op event emitter if no tracers
		eventEmitter = NewEventEmitterAdapter(nil)
	}

	// Adapt the v2 logger to the provider logger interface.
	var logger interfaces.Logger
	if config.Logger != nil {
		logger = NewLoggerAdapter(config.Logger)
	} else {
		// Create a no-op logger if none provided
		logger = NewLoggerAdapter(nil)
	}

	// API keys — same underlying type (alias), so clone directly.
	providerAPIKeys := config.APIKeys.Clone()

	return llmproviders.Config{
		ConnectionID:        config.ConnectionID,
		Provider:            llmproviders.Provider(config.Provider),
		ModelID:             config.ModelID,
		Temperature:         config.Temperature,
		EventEmitter:        eventEmitter,
		TraceID:             interfaces.TraceID(config.TraceID),
		MaxRetries:          config.MaxRetries,
		Logger:              logger,
		Context:             config.Context,
		APIKeys:             providerAPIKeys,
		ClaudeCodeTransport: config.ClaudeCodeTransport,
	}
}

// ConfigHook, when the host sets it (once, at start), adjusts every Config
// before InitializeLLM uses it, including the agent's own re-initializations
// (continuation relaunch, model switch). AgentWorks uses it to route models
// that name no account through its server-account admission.
var ConfigHook func(Config) Config

// InitializeLLM creates and initializes an LLM based on the provider configuration
// This function maintains backward compatibility by accepting agent_go Config
// and converting it to llm-providers Config internally
func InitializeLLM(config Config) (llmtypes.Model, error) {
	if ConfigHook != nil {
		config = ConfigHook(config)
	}
	// Convert agent_go Config to llm-providers Config
	externalConfig := convertConfig(config)

	// Call llm-providers InitializeLLM (already returns llmtypes.Model)
	llm, err := llmproviders.InitializeLLM(externalConfig)
	if err != nil {
		return nil, err
	}

	// Wrap the returned LLM to maintain backward compatibility with agent_go-specific fields
	return wrapProviderAwareLLM(llm, config.Provider, config.ModelID, config.Logger, config.APIKeys), nil
}

// wrapProviderAwareLLM preserves the configuration metadata used by Agent.
func wrapProviderAwareLLM(llm llmtypes.Model, provider Provider, modelID string, logger loggerv2.Logger, apiKeys *ProviderAPIKeys) *ProviderAwareLLM {
	return &ProviderAwareLLM{
		Model:    llm,
		provider: provider,
		modelID:  modelID,
		logger:   logger,
		apiKeys:  apiKeys,
	}
}

// ProviderAwareLLM preserves provider metadata and applies provider-specific call options.
type ProviderAwareLLM struct {
	llmtypes.Model
	provider Provider
	modelID  string
	logger   loggerv2.Logger
	apiKeys  *ProviderAPIKeys
}

// GetProvider returns the provider of this LLM
func (p *ProviderAwareLLM) GetProvider() Provider {
	return p.provider
}

// GetModelID returns the model ID of this LLM
func (p *ProviderAwareLLM) GetModelID() string {
	return p.modelID
}

// GetAPIKeys returns the API keys used by this LLM
// This allows the agent to automatically extract and reuse keys from the LLM
func (p *ProviderAwareLLM) GetAPIKeys() *ProviderAPIKeys {
	return p.apiKeys
}

// GenerateContent wraps the underlying model and adds OpenRouter usage metadata.
func (p *ProviderAwareLLM) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	// Automatically add usage parameter for OpenRouter requests to get cache token information
	if p.provider == ProviderOpenRouter {
		if p.logger != nil {
			p.logger.Info("Adding OpenRouter usage parameter for cache token information")
		}
		options = append(options, WithOpenRouterUsage())
	}

	// Call the underlying LLM (which is already a ProviderAwareLLM from llm-providers)
	return p.Model.GenerateContent(ctx, messages, options...)
}

// WithOpenRouterUsage enables usage parameter for OpenRouter requests to get cache token information
func WithOpenRouterUsage() CallOption {
	return func(opts *CallOptions) {
		// Set the usage parameter in the request metadata
		if opts.Metadata == nil {
			opts.Metadata = &llmtypes.Metadata{
				Usage: &llmtypes.UsageMetadata{Include: true},
			}
		} else {
			if opts.Metadata.Usage == nil {
				opts.Metadata.Usage = &llmtypes.UsageMetadata{Include: true}
			} else {
				opts.Metadata.Usage.Include = true
			}
		}
	}
}

// WithMCPConfig sets the MCP configuration JSON string for the Claude Code adapter session.
func WithMCPConfig(config string) CallOption {
	return llmproviders.WithMCPConfig(config)
}

// WithMCPReadyFile sets the path the session's MCP bridge creates once the CLI
// completes its tools/list handshake, so a cold interactive session holds its
// first prompt until the tools are connected. See llmproviders.WithMCPReadyFile.
func WithMCPReadyFile(path string) CallOption {
	return llmproviders.WithMCPReadyFile(path)
}

// WithDangerouslySkipPermissions enables the --dangerously-skip-permissions flag for the Claude Code CLI.
// CAUTION: This allows the agent to execute any tool without user confirmation.
func WithDangerouslySkipPermissions() CallOption {
	return llmproviders.WithDangerouslySkipPermissions()
}

// WithClaudeCodeSettings sets the --settings flag for the Claude Code CLI.
// It accepts either a JSON string or a file path.
func WithClaudeCodeSettings(settings string) CallOption {
	return llmproviders.WithClaudeCodeSettings(settings)
}

// WithClaudeCodeTools sets the --tools flag for the Claude Code CLI.
// Use "" to disable all built-in tools.
func WithClaudeCodeTools(tools string) CallOption {
	return llmproviders.WithClaudeCodeTools(tools)
}

// WithAllowedTools sets the --allowed-tools flag for the Claude Code CLI.
// Example: "mcp__api-bridge__*" to allow all tools from the bridge.
func WithAllowedTools(tools string) CallOption {
	return llmproviders.WithAllowedTools(tools)
}

// WithMaxTurns sets the --max-turns flag for the Claude Code CLI.
// Limits the number of agentic turns. Claude Code exits with an error when the limit is reached.
func WithMaxTurns(maxTurns int) CallOption {
	return llmproviders.WithMaxTurns(maxTurns)
}

// WithResumeSessionID sets the --resume flag so the Claude Code CLI resumes
// an existing session instead of starting a new one.
func WithResumeSessionID(id string) CallOption {
	return llmproviders.WithResumeSessionID(id)
}

// WithClaudeCodeEffort sets the --effort flag for the Claude Code CLI.
// Values: "low", "medium", "high", "max"
func WithClaudeCodeEffort(level string) CallOption {
	return llmproviders.WithClaudeCodeEffort(level)
}

// --- Codex CLI Wrapper Functions ---

// WithCodexResumeSessionID resumes a Codex CLI session by thread ID.
func WithCodexResumeSessionID(id string) CallOption {
	return llmproviders.WithCodexResumeSessionID(id)
}

// WithCodexApprovalPolicy sets the approval_policy for the Codex CLI.
func WithCodexApprovalPolicy(policy string) CallOption {
	return llmproviders.WithCodexApprovalPolicy(policy)
}

// WithCodexStructuredTransport selects `codex exec --json` (per-turn,
// one-shot, no tmux dependency) instead of the tmux interactive transport.
// OFF by default — tmux is the normal product path (persistent chat, live
// steering, terminal streaming); structured is for callers with neither need
// (e.g. unattended workflow steps).
func WithCodexStructuredTransport(enabled bool) CallOption {
	return llmproviders.WithCodexStructuredTransport(enabled)
}

// WithClaudeStructuredTransport selects `claude -p --output-format stream-json`
// instead of the tmux interactive transport. OFF by default — see
// WithCodexStructuredTransport doc comment for the rationale.
func WithClaudeStructuredTransport(enabled bool) CallOption {
	return llmproviders.WithClaudeStructuredTransport(enabled)
}

// WithMuseStructuredTransport selects `muse exec --json` instead of the tmux
// interactive transport. OFF by default — the adapter runs the tmux lane
// unless this option explicitly requests the one-shot JSON lane.
func WithMuseStructuredTransport(enabled bool) CallOption {
	return llmproviders.WithMuseStructuredTransport(enabled)
}

// WithCodexStreamTranscript opts into streaming structured content (assistant
// text + tool-call starts) mid-turn by tailing Codex's own rollout JSONL. OFF
// by default — a caller with no StreamingCallback registered has nothing to
// do with the extra chunks, so mcpagent only turns this on automatically when
// one is set (see coding_agent_integrations.go).
func WithCodexStreamTranscript(enabled bool) CallOption {
	return llmproviders.WithCodexStreamTranscript(enabled)
}

// WithClaudeStreamTranscript is WithCodexStreamTranscript for Claude Code.
func WithClaudeStreamTranscript(enabled bool) CallOption {
	return llmproviders.WithClaudeStreamTranscript(enabled)
}

// WithClaudeStreamTmuxScreen controls whether Claude Code's raw tmux-pane
// snapshots are included in the general stream. Product chat should normally
// disable this and consume WithClaudeStreamTranscript instead; an explicit
// terminal surface can obtain its pane independently.
func WithClaudeStreamTmuxScreen(enabled bool) CallOption {
	return llmproviders.WithClaudeStreamTmuxScreen(enabled)
}

// WithCodexStreamTmuxScreen is WithClaudeStreamTmuxScreen for Codex CLI.
func WithCodexStreamTmuxScreen(enabled bool) CallOption {
	return llmproviders.WithCodexStreamTmuxScreen(enabled)
}

// WithCodexReasoningEffort sets the model_reasoning_effort for the Codex CLI.
func WithCodexReasoningEffort(effort string) CallOption {
	return llmproviders.WithCodexReasoningEffort(effort)
}

// WithCodexNativeTools keeps Codex's shell and subagents and disables its
// other native features (Full CLI).
func WithCodexNativeTools() CallOption {
	return llmproviders.WithCodexNativeTools()
}

// WithCodexDisableShellTool disables the built-in shell tool in Codex CLI.
func WithCodexDisableShellTool() CallOption {
	return llmproviders.WithCodexDisableShellTool()
}

// WithCodexSandbox sets the --sandbox flag for the Codex CLI.
func WithCodexSandbox(sandbox string) CallOption {
	return llmproviders.WithCodexSandbox(sandbox)
}

// WithCodexProjectDirID sets the working directory for the Codex CLI.
func WithCodexProjectDirID(dir string) CallOption {
	return llmproviders.WithCodexProjectDirID(dir)
}

// WithCodexConfigOverrides passes arbitrary -c key=value overrides to the Codex CLI.
func WithCodexConfigOverrides(overrides []string) CallOption {
	return llmproviders.WithCodexConfigOverrides(overrides)
}

// WithCodexMCPServers passes per-session MCP server configuration to Codex via
// a temporary profile TOML. It must not be flattened into -c arguments because
// bridge tool schemas and credentials can exceed tmux command limits.
func WithCodexMCPServers(mcpJSON string) CallOption {
	return llmproviders.WithCodexMCPServers(mcpJSON)
}

// Re-export helper functions from llm-providers

// GetDefaultModel returns the default model for each provider from environment variables
func GetDefaultModel(provider Provider) string {
	return llmproviders.GetDefaultModel(llmproviders.Provider(provider))
}

// ValidateProvider checks if the provider is supported
func ValidateProvider(provider string) (Provider, error) {
	p, err := llmproviders.ValidateProvider(provider)
	return Provider(p), err
}

// Re-export response types from llm-providers
type LLMDefaultsResponse = llmproviders.LLMDefaultsResponse
type APIKeyValidationRequest = llmproviders.APIKeyValidationRequest
type APIKeyValidationResponse = llmproviders.APIKeyValidationResponse

// GetLLMDefaults returns default LLM configurations from environment variables
func GetLLMDefaults() LLMDefaultsResponse {
	return llmproviders.GetLLMDefaults()
}

// ValidateAPIKey validates API keys for OpenRouter, OpenAI, Bedrock, and Vertex
func ValidateAPIKey(req APIKeyValidationRequest) APIKeyValidationResponse {
	return llmproviders.ValidateAPIKey(req)
}

// IsO3O4Model detects o3/o4 models (OpenAI) for conditional logic in agent
func IsO3O4Model(modelID string) bool {
	return llmproviders.IsO3O4Model(modelID)
}

func AwaitCursorInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	return llmproviders.AwaitCursorInputDurable(ctx, sessionID, message, timeout)
}
