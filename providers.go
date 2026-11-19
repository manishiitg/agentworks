package llmproviders

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxcontrol"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxsize"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	agycli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	claudecodeadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	codexcli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	cursorcli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	musecli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	picli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

// ForceCompleteCodingAgentTmuxSession asks a tmux-backed coding adapter to
// finish its current wait loop on the next poll and return the pane's current
// response through the normal generation path.
func ForceCompleteCodingAgentTmuxSession(sessionName string) bool {
	return tmuxcontrol.RequestForceComplete(sessionName)
}

// Provider represents the available LLM providers
type Provider string

const (
	ProviderBedrock           Provider = "bedrock"
	ProviderOpenAI            Provider = "openai"
	ProviderAnthropic         Provider = "anthropic"
	ProviderOpenRouter        Provider = "openrouter"
	ProviderVertex            Provider = "vertex"
	ProviderAzure             Provider = "azure"
	ProviderZAI               Provider = "z-ai"
	ProviderKimi              Provider = "kimi"
	ProviderClaudeCode        Provider = "claude-code"
	ProviderCodexCLI          Provider = "codex-cli"
	ProviderCursorCLI         Provider = "cursor-cli"
	ProviderPiCLI             Provider = "pi-cli"
	ProviderMuseCLI           Provider = "muse-cli"
	ProviderAgyCLI            Provider = "agy-cli"
	ProviderACPCLI            Provider = "acp-cli" // any registered ACP agent; model "<agentID>[/<model>]"
	ProviderMiniMax           Provider = "minimax"
	ProviderMiniMaxCodingPlan Provider = "minimax-coding-plan"
	ProviderElevenLabs        Provider = "elevenlabs"
	ProviderDeepgram          Provider = "deepgram"

	DefaultCodexCLIModel  = "high"
	DefaultCursorCLIModel = "composer-2.5"
	DefaultPiCLIModel     = picli.DefaultModelID
	DefaultMuseCLIModel   = "muse-spark-1.3"
	DefaultAgyCLIModel    = "gemini-3.8-flash-high"
	DefaultACPCLIModel    = "opencode"

	// EnvClaudeCodeTransport selects the Claude Code provider transport.
	// Supported values: "sdk" (default, official Agent SDK) or "tmux".
	EnvClaudeCodeTransport     = "CLAUDE_CODE_TRANSPORT"
	EnvCodexCLITransport       = "CODEX_CLI_TRANSPORT"
	CodexCLITransportAppServer = "app-server"
	CodexCLITransportTmux      = "tmux"

	// EnvCursorCLITransport selects the Cursor transport: "sdk" (default, official Cursor SDK
	// local runtime, which steers a running turn) or "tmux".
	EnvCursorCLITransport  = "CURSOR_CLI_TRANSPORT"
	CursorCLITransportTmux = "tmux"
	CursorCLITransportSDK  = "sdk"

	ClaudeCodeTransportTmux = "tmux"
	ClaudeCodeTransportSDK  = "sdk"
)

// SetCodingAgentTmuxSize records the operator's last-known terminal viewport
// (cols × rows) for any newly-launched coding-agent tmux session (Claude
// Code, Codex CLI, Cursor CLI, Pi CLI). Pass <=0 for either
// axis to clear that axis's override and fall back to the env/default value.
// Sizes outside the package's safe band are clamped, not rejected.
func SetCodingAgentTmuxSize(columns, rows int) {
	tmuxsize.SetPreferredSize(columns, rows)
}

// CleanupClaudeCodeTmuxSessions removes Claude Code tmux sessions
// registered by this process. It intentionally does not kill every tmux session
// with the provider prefix, because other backend processes/tests may own live
// coding-agent sessions in the same user tmux server.
func CleanupClaudeCodeTmuxSessions(ctx context.Context) error {
	return claudecodeadapter.CleanupClaudeCodeTmuxSessions(ctx)
}

// CleanupCodexCLIInteractiveSessions removes Codex CLI tmux sessions registered
// by this process.
func CleanupCodexCLIInteractiveSessions(ctx context.Context) error {
	return codexcli.CleanupCodexCLIInteractiveSessions(ctx)
}

// CleanupCursorCLIInteractiveSessions removes Cursor CLI tmux sessions
// registered by this process.
func CleanupCursorCLIInteractiveSessions(ctx context.Context) error {
	return cursorcli.CleanupCursorCLIInteractiveSessions(ctx)
}

// CleanupPiCLIInteractiveSessions removes Pi CLI tmux sessions registered by
// this process.
func CleanupPiCLIInteractiveSessions(ctx context.Context) error {
	return picli.CleanupPiCLIInteractiveSessions(ctx)
}

// CleanupMuseCLIInteractiveSessions removes Muse CLI tmux sessions registered
// by this process.
func CleanupMuseCLIInteractiveSessions(ctx context.Context) error {
	return musecli.CleanupMuseCLIInteractiveSessions(ctx)
}

// CleanupAgyCLIInteractiveSessions removes agy tmux sessions registered by
// this process.
func CleanupAgyCLIInteractiveSessions(ctx context.Context) error {
	return agycli.CleanupAgyCLIInteractiveSessions(ctx)
}

// interactiveSessionPrefixes are the tmux session-name prefixes used by every
// coding-agent CLI transport. Kept in sync with each adapter's
// <provider>InteractiveSessionPrefix() default.
var interactiveSessionPrefixes = []string{
	"mlp-pi-cli-int",
	"mlp-codex-cli-int",
	"mlp-cursor-cli-int",
	"mlp-claude-code",
	"mlp-muse-",
	"agy-int-",
}

// SweepOrphanedInteractiveTmuxSessions reaps and kills every coding-agent tmux
// session (and its orphaned process tree) left over from a PREVIOUS backend run.
// It finds sessions by name prefix rather than the in-process registries, which
// are empty on a fresh boot — so this is the recovery path for sessions stranded
// by an ungraceful exit (crash, SIGKILL, sleep). Returns the count swept.
//
// CAUTION: prefix matching also catches sessions owned by a concurrent backend
// or test sharing the same tmux server. Call this ONLY at single-instance
// startup (e.g. the desktop app), never from a context where another owner may
// hold live sessions.
func SweepOrphanedInteractiveTmuxSessions(ctx context.Context) int {
	return tmuxcontrol.SweepInteractiveSessions(ctx, interactiveSessionPrefixes)
}

// Close*InteractiveSessionForOwner force-closes the persistent CLI
// session for the given owner. Callers reach for these when something
// about the session must change in a way the running CLI process can't
// adopt mid-flight — most concretely a workshop-mode switch that
// rewrites the agent's system prompt. The CLI process loaded its
// prompt at launch time and won't re-read the rule file, so the
// orchestrator tears down the persistent session here and the next
// turn relaunches with the new content.
//
// Calls are no-ops when no session is registered for the owner.

func ClosePiCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	picli.ClosePiCLIInteractiveSessionForOwner(ownerSessionID, reason)
}

func CloseCursorCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	cursorcli.CloseCursorCLIInteractiveSessionForOwner(ownerSessionID, reason)
}

func CloseCodexCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	codexcli.CloseCodexCLIInteractiveSessionForOwner(ownerSessionID, reason)
}

func CloseClaudeCodeInteractiveSessionForOwner(ownerSessionID, reason string) {
	claudecodeadapter.CloseClaudeCodeInteractiveSessionForOwner(ownerSessionID, reason)
}

func CloseMuseCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	musecli.CloseMuseCLIInteractiveSessionForOwner(ownerSessionID, reason)
}

func CloseAgyCLIInteractiveSessionForOwner(ownerSessionID, reason string) {
	agycli.CloseAgyCLIInteractiveSessionForOwner(ownerSessionID, reason)
}

// CloseXxxCLIInteractiveSessionByTmux variants tear down a tmux-backed coding
// CLI session by its tmux session name rather than by owner key. They run the
// same provider-specific graceful exit + cleanup as the owner-keyed closes,
// and are used as a teardown backstop when the owning session ID is unknown or
// has drifted (e.g. workflow sub-agents registered under a step-execution
// owner). All are no-ops when no live session matches the tmux name.

func CloseCodexCLIInteractiveSessionByTmux(tmuxSessionName, reason string) {
	codexcli.CloseCodexCLIInteractiveSessionByTmux(tmuxSessionName, reason)
}

func CloseClaudeCodeInteractiveSessionByTmux(tmuxSessionName, reason string) {
	claudecodeadapter.CloseClaudeCodeInteractiveSessionByTmux(tmuxSessionName, reason)
}

func CloseMuseCLIInteractiveSessionByTmux(tmuxSessionName, reason string) {
	musecli.CloseMuseCLIInteractiveSessionByTmux(tmuxSessionName, reason)
}

func CloseAgyCLIInteractiveSessionByTmux(tmuxSessionName, reason string) {
	agycli.CloseAgyCLIInteractiveSessionByTmux(tmuxSessionName, reason)
}

// SendClaudeCodeInput sends user input to a live Claude Code tmux session
// registered for the owning application session.
func SendClaudeCodeInput(ctx context.Context, sessionID, message string) error {
	return claudecodeadapter.SendClaudeCodeInput(ctx, sessionID, message)
}

// AwaitClaudeInputDurable waits for the transcript proof that a previous
// SendClaudeCodeInput reached the CLI. It is the durability half of the
// two-stage delivery receipt; host applications call it after the fast
// pane ack and promote their delivery tick on confirmation. A zero
// timeout selects the adapter's env-tuned budget.
func AwaitClaudeInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	ack, err := claudecodeadapter.AwaitClaudeInputDurable(ctx, sessionID, message, timeout)
	if err != nil {
		return llmtypes.DurableAck{Outcome: llmtypes.DurableAckFailed}, err
	}
	outcome := llmtypes.DurableAckConfirmed
	if ack.Outcome == claudecodeadapter.ClaudeDurableAckUnflushed {
		outcome = llmtypes.DurableAckUnflushed
	}
	return llmtypes.DurableAck{
		Outcome:      outcome,
		Latency:      ack.Latency,
		ProofSource:  ack.ProofPath,
		RowTimestamp: ack.RowTimestamp,
	}, nil
}

// SendCodexCLIInteractiveInput sends user input to a live Codex CLI interactive
// tmux session registered for the owning application session.
func SendCodexCLIInteractiveInput(ctx context.Context, sessionID, message string) error {
	return codexcli.SendCodexInteractiveInput(ctx, sessionID, message)
}

// SendPiCLIInteractiveInput steers the active native Pi RPC turn for its owner.
func SendPiCLIInteractiveInput(ctx context.Context, sessionID, message string) error {
	return picli.SendPiInteractiveInput(ctx, sessionID, message)
}

// AwaitPiInputDurable waits for the persisted native user record of a previous
// SendPiCLIInteractiveInput. Command acceptance remains unflushed until that
// record exists; exhausting the proof budget does not mean delivery failed. A
// zero timeout selects the adapter's env-tuned budget.
func AwaitPiInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	ack, err := picli.AwaitPiInputDurable(ctx, sessionID, message, timeout)
	if err != nil {
		return llmtypes.DurableAck{Outcome: llmtypes.DurableAckFailed}, err
	}
	outcome := llmtypes.DurableAckConfirmed
	if ack.Outcome == picli.PiDurableAckUnflushed {
		outcome = llmtypes.DurableAckUnflushed
	}
	return llmtypes.DurableAck{
		Outcome:      outcome,
		Latency:      ack.Latency,
		ProofSource:  ack.ProofPath,
		RowTimestamp: ack.RowTimestamp,
	}, nil
}

// SendMuseCLIInteractiveInput sends user input to a live Muse CLI
// interactive tmux session registered for the owning application session.
func SendMuseCLIInteractiveInput(ctx context.Context, sessionID, message string) error {
	return musecli.SendMuseInteractiveInput(ctx, sessionID, message)
}

// AwaitMuseInputDurable waits for the transcript proof that a previous
// SendMuseCLIInteractiveInput reached the CLI. It is the durability
// half of the two-stage delivery receipt; host applications call it
// after the fast pane ack and promote their delivery tick on
// confirmation. A zero timeout selects the adapter's env-tuned budget.
func AwaitMuseInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	ack, err := musecli.AwaitMuseInputDurable(ctx, sessionID, message, timeout)
	if err != nil {
		return llmtypes.DurableAck{Outcome: llmtypes.DurableAckFailed}, err
	}
	outcome := llmtypes.DurableAckConfirmed
	if ack.Outcome == musecli.MuseDurableAckUnflushed {
		outcome = llmtypes.DurableAckUnflushed
	}
	return llmtypes.DurableAck{
		Outcome:      outcome,
		Latency:      ack.Latency,
		ProofSource:  ack.ProofPath,
		RowTimestamp: ack.RowTimestamp,
	}, nil
}

// SendMuseCLIInteractiveControlKey injects a tmux control key into a registered
// Muse CLI interactive session.
func SendMuseCLIInteractiveControlKey(ctx context.Context, sessionID, key string) error {
	return musecli.SendMuseInteractiveControlKey(ctx, sessionID, key)
}

// SendAgyCLIInteractiveInput sends user input to a live agy interactive
// tmux session registered for the owning application session.
func SendAgyCLIInteractiveInput(ctx context.Context, sessionID, message string) error {
	return agycli.SendAgyInteractiveInput(ctx, sessionID, message)
}

// AwaitAgyInputDurable confirms a prior tmux live-input send from AGY's
// conversation SQLite user step rather than its pane echo.
func AwaitAgyInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	ack, err := agycli.AwaitAgyInputDurable(ctx, sessionID, message, timeout)
	if err != nil {
		return llmtypes.DurableAck{Outcome: llmtypes.DurableAckFailed}, err
	}
	return llmtypes.DurableAck{
		Outcome:      llmtypes.DurableAckConfirmed,
		Latency:      ack.Latency,
		ProofSource:  ack.ProofPath,
		RowTimestamp: ack.RowTimestamp,
	}, nil
}

// SendAgyCLIInteractiveControlKey injects a tmux control key into a
// registered agy interactive session.
func SendAgyCLIInteractiveControlKey(ctx context.Context, sessionID, key string) error {
	return agycli.SendAgyInteractiveControlKey(ctx, sessionID, key)
}

// AwaitCodexInputDurable waits for the rollout proof that a previous
// SendCodexCLIInteractiveInput reached the CLI. It is the durability
// half of the two-stage delivery receipt; host applications call it
// after the fast pane ack and promote their delivery tick on
// confirmation. A zero timeout selects the adapter's env-tuned budget.
func AwaitCodexInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	ack, err := codexcli.AwaitCodexInputDurable(ctx, sessionID, message, timeout)
	if err != nil {
		return llmtypes.DurableAck{Outcome: llmtypes.DurableAckFailed}, err
	}
	outcome := llmtypes.DurableAckConfirmed
	if ack.Outcome == codexcli.CodexDurableAckUnflushed {
		outcome = llmtypes.DurableAckUnflushed
	}
	return llmtypes.DurableAck{
		Outcome:      outcome,
		Latency:      ack.Latency,
		ProofSource:  ack.ProofPath,
		RowTimestamp: ack.RowTimestamp,
	}, nil
}

// ReadCodingAgentRetainedTurnMessages reconstructs the structured messages for
// a turn that was injected directly into an already-running coding CLI. It is a
// read-only sidecar operation: it never starts or resumes an agent session.
func ReadCodingAgentRetainedTurnMessages(provider Provider, ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	switch Provider(strings.ToLower(strings.TrimSpace(string(provider)))) {
	case ProviderClaudeCode:
		return claudecodeadapter.ReadRetainedTurnMessages(ownerSessionID, turnStart)
	case ProviderCodexCLI:
		return codexcli.ReadRetainedTurnMessages(ownerSessionID, turnStart)
	case ProviderCursorCLI:
		return cursorcli.ReadRetainedTurnMessages(ownerSessionID, turnStart)
	case ProviderMuseCLI:
		return musecli.ReadRetainedTurnMessages(ownerSessionID, turnStart)
	case ProviderAgyCLI:
		return agycli.ReadRetainedTurnMessages(ownerSessionID, turnStart)
	default:
		return nil
	}
}

func ReadCodingAgentRetainedTurnResponse(provider Provider, ownerSessionID string, turnStart time.Time) llmtypes.RetainedTurnResponse {
	if provider == ProviderClaudeCode {
		return claudecodeadapter.ReadRetainedTurnResponse(ownerSessionID, turnStart)
	}
	return llmtypes.RetainedTurnResponse{Messages: ReadCodingAgentRetainedTurnMessages(provider, ownerSessionID, turnStart)}
}

// ReadCodingAgentRetainedTurnEndedWithoutReply reports that the CLI's own record shows the retained turn that began at
// turnStart ended with no reply (the person interrupted it, or the model call failed), with the CLI's stop reason. Pi
// and Muse are wired; the other CLIs report false, so their turns settle as before.
func ReadCodingAgentRetainedTurnEndedWithoutReply(provider Provider, ownerSessionID string, turnStart time.Time) (string, bool) {
	switch Provider(strings.ToLower(strings.TrimSpace(string(provider)))) {
	case ProviderMuseCLI:
		return musecli.RetainedTurnEndedWithoutReply(ownerSessionID, turnStart)
	}
	return "", false
}

// ReadCodingAgentRetainedTurnUsage returns the usage a coding CLI reported in its own transcript for model runs nobody
// has reported yet, for turns typed into an already-running terminal (they never pass through GenerateContent). Each
// run, message or step is returned once, with the CLI's own id, so a caller can record it once. turnStart bounds the
// read for the CLIs that timestamp their records; modelID prices Muse. Cursor's transcript carries no usage, so it
// returns nothing (callers must not guess a number). Read-only apart from remembering what it returned.
func ReadCodingAgentRetainedTurnUsage(provider Provider, ownerSessionID, modelID string, turnStart time.Time) []llmtypes.RetainedTurnUsage {
	switch Provider(strings.ToLower(strings.TrimSpace(string(provider)))) {
	case ProviderClaudeCode:
		return claudecodeadapter.ReadRetainedTurnUsage(ownerSessionID, turnStart)
	case ProviderCodexCLI:
		return codexcli.ReadRetainedTurnUsage(ownerSessionID, turnStart)
	case ProviderMuseCLI:
		return musecli.ReadRetainedTurnUsage(ownerSessionID, modelID)
	case ProviderAgyCLI:
		return agycli.ReadRetainedTurnUsage(ownerSessionID, turnStart)
	default:
		return nil
	}
}

// ReadCodingAgentRetainedTurnStructuredProgressMessages includes native tools
// and thinking as well as narration, using each adapter's retained cursor.
func ReadCodingAgentRetainedTurnStructuredProgressMessages(provider Provider, ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	switch provider {
	case ProviderClaudeCode:
		return claudecodeadapter.ReadRetainedTurnStructuredProgressMessages(ownerSessionID, turnStart)
	case ProviderCodexCLI:
		return codexcli.ReadRetainedTurnStructuredProgressMessages(ownerSessionID, turnStart)
	case ProviderMuseCLI:
		return musecli.ReadRetainedTurnStructuredProgressMessages(ownerSessionID, turnStart)
	case ProviderAgyCLI:
		return agycli.ReadRetainedTurnStructuredProgressMessages(ownerSessionID, turnStart)
	default:
		return ReadCodingAgentRetainedTurnProgressMessages(provider, ownerSessionID, turnStart)
	}
}

// ReadCodingAgentRetainedTurnProgressMessages reads in-flight commentary without
// asserting completion. This consumes provider progress cursors: callers must
// serialize reads with delivery/publication and must not discard returned text.
// Cursor shares its normal stream cursor; the other coding CLIs require the
// turn start to bound their progress cursors. The optional argument preserves
// existing Cursor callers. None of these readers asserts turn completion.
func ReadCodingAgentRetainedTurnProgressMessages(provider Provider, ownerSessionID string, turnStart ...time.Time) []llmtypes.MessageContent {
	switch Provider(strings.ToLower(strings.TrimSpace(string(provider)))) {
	case ProviderClaudeCode:
		if len(turnStart) > 0 {
			return claudecodeadapter.ReadRetainedTurnProgressMessages(ownerSessionID, turnStart[0])
		}
	case ProviderCodexCLI:
		if len(turnStart) > 0 {
			return codexcli.ReadRetainedTurnProgressMessages(ownerSessionID, turnStart[0])
		}
	case ProviderCursorCLI:
		return cursorcli.ReadRetainedTurnProgressMessages(ownerSessionID)
	case ProviderMuseCLI:
		if len(turnStart) > 0 {
			return musecli.ReadRetainedTurnProgressMessages(ownerSessionID, turnStart[0])
		}
	case ProviderAgyCLI:
		if len(turnStart) > 0 {
			return agycli.ReadRetainedTurnProgressMessages(ownerSessionID, turnStart[0])
		}
	}
	return nil
}

// SendClaudeCodeControlKey injects a tmux control key (e.g. "Escape", "C-c")
// into a registered Claude Code tmux session.
func SendClaudeCodeControlKey(ctx context.Context, sessionID, key string) error {
	return claudecodeadapter.SendClaudeCodeControlKey(ctx, sessionID, key)
}

// SendCodexCLIInteractiveControlKey injects a tmux control key into a
// registered Codex CLI interactive session.
func SendCodexCLIInteractiveControlKey(ctx context.Context, sessionID, key string) error {
	return codexcli.SendCodexInteractiveControlKey(ctx, sessionID, key)
}

// SendCursorCLIInteractiveControlKey injects a tmux control key into a
// registered Cursor CLI interactive session.
func SendCursorCLIInteractiveControlKey(ctx context.Context, sessionID, key string) error {
	return cursorcli.SendCursorInteractiveControlKey(ctx, sessionID, key)
}

// SendPiCLIInteractiveControlKey injects a tmux control key into a registered
// Pi CLI interactive session.
func SendPiCLIInteractiveControlKey(ctx context.Context, sessionID, key string) error {
	return picli.SendPiInteractiveControlKey(ctx, sessionID, key)
}

// Config holds configuration for LLM initialization
type Config struct {
	ConnectionID string
	Provider     Provider
	ModelID      string
	Temperature  float64
	// EventEmitter for emitting LLM events (replaces Tracers)
	EventEmitter interfaces.EventEmitter
	TraceID      interfaces.TraceID
	MaxRetries   int
	// Logger for structured logging
	Logger interfaces.Logger
	// Context for LLM initialization (optional, uses background with timeout if not provided)
	Context context.Context
	// API keys for providers (optional, falls back to environment variables if not provided)
	APIKeys *ProviderAPIKeys
	// ClaudeCodeTransport optionally overrides CLAUDE_CODE_TRANSPORT for this
	// initialized Claude Code model. Default is "sdk" (official streaming
	// Agent SDK); "tmux" selects the interactive terminal transport.
	ClaudeCodeTransport string
}

// ProviderAPIKeys holds API keys for different providers
type ProviderAPIKeys struct {
	RuntimeEnvironment map[string]string `json:"-"`
	// ResolveConnection is a server-owned authorization and credential resolver.
	// It is never accepted from JSON and must survive credential clones.
	ResolveConnection func(context.Context, Provider, string) (*ProviderAPIKeys, error) `json:"-"`
	OpenRouter        *string
	OpenAI            *string
	Anthropic         *string
	// ClaudeCodeOAuthToken is a workflow-scoped Claude Code subscription token.
	// It is deliberately separate from Anthropic: the claude-code provider must
	// never fall back to API-key billing. Callers should populate this only from
	// an explicitly scoped credential; provider initialization never reads it
	// from the ambient environment.
	ClaudeCodeOAuthToken *string `json:"-"`
	Vertex               *string
	CodexCLI             *string
	CursorCLI            *string
	PiCLI                *string
	// MuseCLI is an explicit Meta API key for muse-cli. Empty (the common
	// case) means stored `muse login` or META_API_KEY env, mirroring how
	// the adapter resolves auth. Never a third-party key.
	MuseCLI *string
	// AgyCLI is an explicit Gemini API key for agy-cli. Empty (the common
	// case) means stored `agy` Google login or GEMINI_API_KEY env.
	AgyCLI            *string
	MiniMax           *string
	MiniMaxCodingPlan *string
	ElevenLabs        *string
	Deepgram          *string
	ZAI               *string
	Kimi              *string
	// PiProviderKeys stores Pi sub-provider API keys keyed by Pi provider id
	// (for example "google", "zai", "zai-coding-cn", "deepseek").
	PiProviderKeys map[string]string
	// PiCustomProvider is the account's own OpenAI-compatible endpoint for
	// Pi (base URL and model ids, never the key: that is in PiProviderKeys
	// under the same provider name).
	PiCustomProvider *picli.PiCustomProvider
	Bedrock          *BedrockConfig
	Azure            *AzureAPIConfig
}

// AzureAPIConfig holds Azure-specific configuration
type AzureAPIConfig struct {
	Endpoint   string // Azure AI endpoint URL
	APIKey     string // Azure API key
	APIVersion string // API version (optional, defaults to 2024-10-21)
	Region     string // Azure region (optional, for logging)
}

// BedrockConfig holds Bedrock-specific configuration
type BedrockConfig struct {
	Region string
}

// SetKeyForProvider sets the API key for a given provider.
// Use this instead of switch statements to avoid missing new providers.
func (k *ProviderAPIKeys) SetKeyForProvider(provider Provider, key *string) {
	switch provider {
	case ProviderOpenRouter:
		k.OpenRouter = key
	case ProviderOpenAI:
		k.OpenAI = key
	case ProviderAnthropic:
		k.Anthropic = key
	case ProviderVertex:
		k.Vertex = key
	case ProviderCodexCLI:
		k.CodexCLI = key
	case ProviderCursorCLI:
		k.CursorCLI = key
	case ProviderPiCLI:
		k.PiCLI = key
	case ProviderAgyCLI:
		k.AgyCLI = key
	case ProviderMiniMax:
		k.MiniMax = key
	case ProviderMiniMaxCodingPlan:
		k.MiniMaxCodingPlan = key
	case ProviderElevenLabs:
		k.ElevenLabs = key
	case ProviderDeepgram:
		k.Deepgram = key
	case ProviderZAI:
		k.ZAI = key
	case ProviderKimi:
		k.Kimi = key
	}
}

// Clone returns a deep copy of ProviderAPIKeys.
// Use this instead of field-by-field copies to avoid missing new fields.
func (k *ProviderAPIKeys) Clone() *ProviderAPIKeys {
	if k == nil {
		return nil
	}
	out := *k // shallow copy — all *string fields are immutable, so sharing is safe
	if k.RuntimeEnvironment != nil {
		out.RuntimeEnvironment = map[string]string{}
		for key, value := range k.RuntimeEnvironment {
			out.RuntimeEnvironment[key] = value
		}
	}
	if k.Bedrock != nil {
		b := *k.Bedrock
		out.Bedrock = &b
	}
	if k.Azure != nil {
		a := *k.Azure
		out.Azure = &a
	}
	if k.PiCustomProvider != nil {
		custom := *k.PiCustomProvider
		custom.Models = append([]string(nil), k.PiCustomProvider.Models...)
		out.PiCustomProvider = &custom
	}
	if k.PiProviderKeys != nil {
		out.PiProviderKeys = make(map[string]string, len(k.PiProviderKeys))
		for provider, key := range k.PiProviderKeys {
			out.PiProviderKeys[provider] = key
		}
	}
	return &out
}

// InitializeLLM creates and initializes an LLM based on the provider configuration
func InitializeLLM(config Config) (llmtypes.Model, error) {
	if config.ConnectionID != "" {
		if config.APIKeys == nil || config.APIKeys.ResolveConnection == nil {
			return nil, fmt.Errorf("provider connection cannot be resolved")
		}
		ctx := config.Context
		if ctx == nil {
			ctx = context.Background()
		}
		keys, err := config.APIKeys.ResolveConnection(ctx, config.Provider, config.ConnectionID)
		if err != nil {
			return nil, err
		}
		if keys == nil {
			return nil, fmt.Errorf("provider connection has no credentials")
		}
		config.APIKeys = keys
	}
	var llm llmtypes.Model
	var err error

	switch config.Provider {
	case ProviderBedrock:
		llm, err = initializeBedrock(config)
	case ProviderOpenAI:
		llm, err = initializeOpenAI(config)
	case ProviderAnthropic:
		llm, err = initializeAnthropic(config)
	case ProviderOpenRouter:
		llm, err = initializeOpenRouter(config)
	case ProviderVertex:
		llm, err = initializeVertex(config)
	case ProviderAzure:
		llm, err = initializeAzure(config)
	case ProviderZAI:
		llm, err = initializeZAI(config)
	case ProviderKimi:
		llm, err = initializeKimi(config)
	case ProviderClaudeCode:
		llm, err = initializeClaudeCode(config)
	case ProviderCodexCLI:
		llm, err = initializeCodexCLI(config)
	case ProviderCursorCLI:
		llm, err = initializeCursorCLI(config)
	case ProviderPiCLI:
		llm, err = initializePiCLI(config)
	case ProviderMuseCLI:
		llm, err = initializeMuseCLI(config)
	case ProviderAgyCLI:
		llm, err = initializeAgyCLI(config)
	case ProviderACPCLI:
		llm, err = initializeACPCLI(config)
	case ProviderMiniMax:
		llm, err = initializeMiniMax(config)
	case ProviderMiniMaxCodingPlan:
		llm, err = initializeMiniMaxCodingPlan(config)
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", config.Provider)
	}

	if err != nil {
		return nil, err
	}

	// Wrap the LLM with provider information and tracing
	if config.APIKeys != nil && len(config.APIKeys.RuntimeEnvironment) > 0 {
		keys := config.APIKeys.Clone()
		llm = &accountBoundModel{Model: llm, environment: keys.RuntimeEnvironment, credentials: accountCredentials(config)}
	}
	return NewProviderAwareLLM(llm, config.Provider, config.ModelID, config.EventEmitter, config.TraceID, config.Logger), nil
}

// InitializeEmbeddingModel creates and initializes an embedding model based on the provider configuration
// Supported providers: OpenAI, OpenRouter, Vertex AI, Bedrock

func CloseCursorCLIInteractiveSessionByTmux(tmuxSessionName, reason string) {
	cursorcli.CloseCursorCLIInteractiveSessionByTmux(tmuxSessionName, reason)
}

func AwaitCursorInputDurable(ctx context.Context, sessionID, message string, timeout time.Duration) (llmtypes.DurableAck, error) {
	ack, err := cursorcli.AwaitCursorInputDurable(ctx, sessionID, message, timeout)
	if err != nil {
		return llmtypes.DurableAck{Outcome: llmtypes.DurableAckFailed}, err
	}
	outcome := llmtypes.DurableAckConfirmed
	if ack.Outcome == cursorcli.CursorDurableAckUnflushed {
		outcome = llmtypes.DurableAckUnflushed
	}
	return llmtypes.DurableAck{
		Outcome:      outcome,
		Latency:      ack.Latency,
		ProofSource:  ack.ProofPath,
		RowTimestamp: ack.RowTimestamp,
	}, nil
}
