package claudecode

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/internal/shelllaunch"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxcontrol"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxsize"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/launchscrub"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/paneview"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/sessionlease"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/sessionregistry"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/tmuxlaunch"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingready"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxstartup"
)

const (
	// Default to no provider-level turn timeout. Workflow/background callers own
	// their execution deadline; the adapter should not cancel a still-running tmux
	// coding agent before the outer workflow timeout.
	defaultTmuxTimeout           = 0
	defaultPersistentIdleTimeout = codingtimeout.DefaultPersistentSessionIdle
	defaultBoundedRetention      = 30 * time.Minute
	defaultTmuxPollInterval      = 750 * time.Millisecond
	defaultTmuxCaptureLines      = "10000"
	defaultTmuxHistoryLimit      = "20000"
	minTmuxMajorVersion          = 3
	claudeIdleStableWindow       = 1200 * time.Millisecond
	claudeTailFallbackMaxLines   = 120

	EnvClaudeTmuxSessionPrefix     = "CLAUDE_CODE_TMUX_SESSION_PREFIX"
	EnvClaudeTmuxTimeoutSeconds    = "CLAUDE_CODE_TMUX_TIMEOUT_SECONDS"
	EnvClaudeTmuxPromptWaitSeconds = "CLAUDE_CODE_TMUX_PROMPT_WAIT_SECONDS"
	// EnvClaudeTmuxPromptMaxWaitSeconds caps the total time waitForTmuxPrompt
	// will wait for a ready input prompt while Claude is actively working
	// (e.g. compacting a long conversation on resume). The prompt-wait above
	// is a sliding inactivity window; this is the absolute backstop.
	EnvClaudeTmuxPromptMaxWaitSeconds = "CLAUDE_CODE_TMUX_PROMPT_MAX_WAIT_SECONDS"
	EnvClaudeTmuxIdleTimeoutSeconds   = "CLAUDE_CODE_TMUX_IDLE_TIMEOUT_SECONDS"
	// EnvClaudeTmuxDiagnosticsDir, when set, receives a full tmux-pane text
	// snapshot whenever the adapter times out waiting for the initial prompt.
	// This is intentionally opt-in because panes can contain user content.
	EnvClaudeTmuxDiagnosticsDir = "CLAUDE_CODE_TMUX_DIAGNOSTICS_DIR"
	// EnvClaudeInteractiveStalePaneBackstopSeconds bounds how long the
	// assistant-response loop will keep waiting on a pane that produced activity
	// and then went byte-identical without ever reaching a ready prompt. Set to
	// 0 to disable the backstop.
	EnvClaudeInteractiveStalePaneBackstopSeconds = "CLAUDE_CODE_INTERACTIVE_STALE_PANE_BACKSTOP_SECONDS"
	// EnvClaudeMCPToolIdleTimeout controls Claude Code's client-side watchdog
	// for an MCP tool call that has not emitted progress. Keep it aligned with
	// the bridge's 90-minute timeout for long-running delegation tools.
	EnvClaudeMCPToolIdleTimeout = "CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT"

	// EnvClaudeMCPReadyWaitSeconds caps how long a freshly created session holds
	// its first prompt waiting for the MCP readiness file (see
	// MetadataKeyMCPReadyFile). On timeout the adapter proceeds anyway. Default
	// 30s; only cold sessions with a readiness file configured ever wait.
	EnvClaudeMCPReadyWaitSeconds = "CLAUDE_CODE_MCP_READY_WAIT_SECONDS"

	EnvClaudePromptSuggestion       = "CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION"
	EnvClaudeDisableAlternateScreen = "CLAUDE_CODE_DISABLE_ALTERNATE_SCREEN"
)

var claudeInteractiveSessionRegistry = struct {
	sync.Mutex
	sessions       map[string]struct{}
	sandboxCleanup map[string]func()
}{
	sessions:       map[string]struct{}{},
	sandboxCleanup: map[string]func(){},
}

var claudeInteractiveOwnerRegistry = sessionregistry.NewOwnerRegistry[string]()

type claudeInteractivePersistentSession struct {
	accountHome     string
	ownerSessionID  string
	tmuxSessionName string
	nativeSessionID string
	authFingerprint string
	// scopeFingerprint identifies the credential scope this live process was
	// LAUNCHED with. A retained CLI applies its environment once, so without
	// this a later turn's narrower scope never reaches the running process.
	scopeFingerprint string
	// Native tools and hooks are also fixed at launch. Attended turns must
	// replace an older process before enabling or removing question handling.
	toolConfigFingerprint string
	workingDir            string
	tempFiles             []string
	readyMarker           codingready.ColdLaunchMarker
	idleLease             sessionlease.Lease
	initErr               error
	createdAt             time.Time
	mu                    sync.Mutex
	retainedProgress      claudeRetainedProgress
}

var claudeInteractivePersistentRegistry = sessionregistry.NewOwnerRegistry[*claudeInteractivePersistentSession]()

func newClaudeCallContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	var callCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		callCtx, cancel = context.WithCancel(context.Background())
	}
	done := make(chan struct{})
	var once sync.Once

	go func() {
		select {
		case <-parent.Done():
			if parent.Err() == context.Canceled {
				cancel()
			}
		case <-done:
		}
	}()

	return callCtx, func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
}

// ClaudeCodeInteractiveAdapter runs Claude Code through its interactive tmux transport.
// It intentionally does not invoke `claude -p`.
type ClaudeCodeInteractiveAdapter struct {
	sdkTransport    bool
	modelID         string
	oauthToken      string
	authFingerprint string
	logger          interfaces.Logger
}

// SetOAuthToken configures a token for this adapter instance only. It must be
// called before GenerateContent; retained sessions inherit the token at launch.
func (c *ClaudeCodeInteractiveAdapter) SetOAuthToken(token string) {
	c.oauthToken = strings.TrimSpace(token)
	c.authFingerprint = claudeCredentialFingerprint(c.oauthToken)
}

func NewClaudeCodeInteractiveAdapter(modelID string, logger interfaces.Logger) *ClaudeCodeInteractiveAdapter {
	return newClaudeCodeInteractiveAdapter(modelID, logger)
}

// NewClaudeCodeInteractiveAdapterWithOAuthToken creates a Claude Code adapter
// with an explicitly scoped subscription token. The token is never discovered
// from the process environment and is applied only to the spawned Claude
// process after login-shell initialization.
func NewClaudeCodeInteractiveAdapterWithOAuthToken(modelID, oauthToken string, logger interfaces.Logger) *ClaudeCodeInteractiveAdapter {
	return newClaudeCodeInteractiveAdapterWithOAuthToken(modelID, oauthToken, logger)
}

func newClaudeCodeInteractiveAdapter(modelID string, logger interfaces.Logger) *ClaudeCodeInteractiveAdapter {
	return newClaudeCodeInteractiveAdapterWithOAuthToken(modelID, "", logger)
}

func newClaudeCodeInteractiveAdapterWithOAuthToken(modelID, oauthToken string, logger interfaces.Logger) *ClaudeCodeInteractiveAdapter {
	if modelID == "" {
		modelID = "claude-code"
	}
	modelID = CurrentClaudeCodeModel(modelID)
	oauthToken = strings.TrimSpace(oauthToken)
	return &ClaudeCodeInteractiveAdapter{
		modelID:         modelID,
		oauthToken:      oauthToken,
		authFingerprint: claudeCredentialFingerprint(oauthToken),
		logger:          logger,
	}
}

func claudeCredentialFingerprint(oauthToken string) string {
	if strings.TrimSpace(oauthToken) == "" {
		return "saved-login"
	}
	sum := sha256.Sum256([]byte(oauthToken))
	return hex.EncodeToString(sum[:])
}

// claudeInteractiveFinalEnv returns the credential environment applied after
// login-shell initialization for the interactive TUI transport.
//
// A token produced by `claude setup-token` is a Claude Code OAuth credential,
// not a generic Anthropic gateway bearer token. It must therefore remain in
// CLAUDE_CODE_OAUTH_TOKEN all the way to the spawned CLI. Mapping it to
// ANTHROPIC_AUTH_TOKEN lets the interactive client consult its saved ~/.claude
// login for subscription/account state; on a machine with multiple Claude
// accounts that can charge or rate-limit the wrong account even though the
// workflow token reached this adapter.
// claudeSkillScopeEnv keeps a managed Claude to the skills the product
// attaches. Claude also offers its own built-in skills (schedule, loop, run,
// init, ...) and account-synced ones (artifact-*, dataviz, ...) which compete
// with AgentWorks's own (schedules, triggers, dashboards) and are unrelated to
// the product. Project skills in <cwd>/.claude/skills are unaffected (server A,
// 2026-09-29).
func claudeSkillScopeEnv() []string {
	return []string{"CLAUDE_CODE_DISABLE_BUNDLED_SKILLS=1", "CLAUDE_CODE_DISABLE_POLICY_SKILLS=1"}
}

// claudeAddSkillScopeSettings adds to a launch's --settings what the env
// switches above do not cover (owner, 2026-10-29: an AgentWorks-launched Claude
// uses only AgentWorks's skills): the built-in "mods" plugin, the built-in
// design and doctor skills, and the skills synced from the claude.ai account
// (anthropic-skills:*). Given through --settings these apply to this launch
// only; nothing is changed or moved in the person's own Claude setup. Other
// plugins (cc-plugin-agents-md, the person's installed plugins) and project
// skills stay. Caller keys are kept.
func claudeAddSkillScopeSettings(settings map[string]any) {
	plugins, _ := settings["enabledPlugins"].(map[string]any)
	if plugins == nil {
		plugins = map[string]any{}
	}
	plugins["cc-plugin-plugin-authoring@builtin"] = false
	settings["enabledPlugins"] = plugins

	overrides, _ := settings["skillOverrides"].(map[string]any)
	if overrides == nil {
		overrides = map[string]any{}
	}
	for name, value := range claudeForcedSkillOverrides() {
		overrides[name] = value
	}
	settings["skillOverrides"] = overrides

	settings["syncClaudeAiSkills"] = false
}

func claudeForcedSkillOverrides() map[string]any {
	return map[string]any{"design": "off", "doctor": "off"}
}

// NativeSkillInvocable shares the launch's forced policy with host discovery.
// It does not enable a skill or override the native CLI's own settings.
func NativeSkillInvocable(name string) bool {
	return claudeForcedSkillOverrides()[name] != "off" && !strings.HasPrefix(name, "cc-plugin-plugin-authoring:")
}

// writeClaudeSkillScopedSettings adds the skill scope to the launch settings
// and writes them to a per-launch --settings file, mode 0600, in the session's
// private runtime dir (the system temp dir when the caller gave none). The
// caller removes it with the launch's other files when the CLI exits.
func writeClaudeSkillScopedSettings(opts *llmtypes.CallOptions, settings map[string]any) (string, error) {
	claudeAddSkillScopeSettings(settings)
	raw, err := json.Marshal(settings)
	if err != nil {
		return "", err
	}
	dir := ""
	if llmtypes.CLIHasPrivateRuntime(opts) {
		dir = strings.TrimSpace(opts.CLIRuntimeDir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("claude settings dir: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, "claude-code-settings-*.json")
	if err != nil {
		return "", fmt.Errorf("claude settings file: %w", err)
	}
	path := tmp.Name()
	_, werr := tmp.Write(raw)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr, os.Chmod(path, 0o600)); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("claude settings file: %w", err)
	}
	return path, nil
}

func claudeInteractiveFinalEnv(oauthToken string) []string {
	oauthToken = strings.TrimSpace(oauthToken)
	if oauthToken == "" {
		return nil
	}
	return []string{"CLAUDE_CODE_OAUTH_TOKEN=" + oauthToken}
}

// claudeAmbientAuthEnvKeys route Claude Code's own auth. They must never leak
// from the server process into a per-user/per-project Claude Code invocation:
// an ambient ANTHROPIC_API_KEY (or a wrong-scope OAuth token) would silently
// override the deliberately-scoped credential resolution above.
var claudeAmbientAuthEnvKeys = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"CLAUDE_CODE_OAUTH_TOKEN",
}

// stripClaudeAmbientAuthEnv removes claudeAmbientAuthEnvKeys from base. Use
// before handing a raw os.Environ() to a claude invocation that doesn't
// already go through startSession's shell-level unset.
func stripClaudeAmbientAuthEnv(base []string) []string {
	out := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		stripped := false
		for _, ambientKey := range claudeAmbientAuthEnvKeys {
			if key == ambientKey {
				stripped = true
				break
			}
		}
		if !stripped {
			out = append(out, entry)
		}
	}
	return out
}

func isClaudeInteractiveAuthenticationFailure(content string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(content), " "))
	if strings.HasPrefix(normalized, "please run /login") &&
		strings.Contains(normalized, "api error: 401") {
		return true
	}
	return strings.HasPrefix(normalized, "failed to authenticate") &&
		strings.Contains(normalized, "api error: 401")
}

func (c *ClaudeCodeInteractiveAdapter) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	opts := &llmtypes.CallOptions{}
	for _, opt := range options {
		opt(opts)
	}
	if err := llmtypes.ValidateCLISecurityLaunch(opts, llmtypes.ConfinedModes(opts)...); err != nil {
		return nil, err
	}

	sdk := c.sdkTransport
	switch opts.CodingProviderTransport {
	case "":
	case "sdk":
		sdk = true
	case "tmux", "structured":
		sdk = false
	default:
		return nil, fmt.Errorf("unsupported Claude native transport %q", opts.CodingProviderTransport)
	}
	selected := *c // Adapter configuration has no locks or mutable session state.
	selected.sdkTransport = sdk
	c = &selected
	if sdk || opts.CodingProviderTransport == "structured" {
		return c.generateContentStructured(ctx, messages, opts)
	}

	// Opt-in structured/JSON transport: `claude -p --output-format stream-json`,
	// per-turn and one-shot (no tmux).
	if opts.Metadata != nil && opts.Metadata.Custom != nil {
		if structured, ok := opts.Metadata.Custom[MetadataKeyStructuredTransport].(bool); ok && structured {
			return c.generateContentStructured(ctx, messages, opts)
		}
	}
	if llmtypes.NativePlanModeForSession(ctx, claudeInteractiveSessionIDFromOptions(opts)) {
		return nil, llmtypes.NativePlanUnavailable("Claude Code in terminal transport")
	}
	if _, ok := llmtypes.NativeGoalCommandForSession(ctx, claudeInteractiveSessionIDFromOptions(opts)); ok {
		return nil, llmtypes.NativeGoalUnavailable("Claude Code in terminal transport")
	}
	// Defensive backstop for direct adapter callers that bypass the
	// orchestrator's image-content pre-processing. In practice, callers
	// running through mcp-agent-builder-go funnel CLI image input as a
	// TEXT message containing the absolute workspace path (see
	// workspace_advanced_tools.go:1509-1517 `pathBasedImageAnalysisProvider`
	// branch); the model uses its filesystem-read capability to view the
	// file. So image input across all CLI providers IS uniform — text +
	// path — and this rejection rarely fires.
	if containsClaudeImageContent(messages) {
		if opts.StreamChan != nil {
			close(opts.StreamChan)
		}
		return nil, fmt.Errorf("claude-code tmux transport does not support llmtypes.ImageContent yet")
	}

	return llmtypes.WithObservability(ctx, llmtypes.ObservabilityConfig{
		Provider:     "claudecode",
		Model:        c.modelID,
		Opts:         opts,
		MessageCount: len(messages),
		Messages:     messages,
		HeaderLine:   fmt.Sprintf("claude (tmux) model=%s msgs=%d", c.modelID, len(messages)),
		RequestMetaExtra: map[string]interface{}{
			"transport": "tmux",
		},
	}, func(sink *llmtypes.StreamSink) (*llmtypes.ContentResponse, error) {
		_ = sink
		return c.generateContentTmuxBody(ctx, opts, messages)
	})
}

func (c *ClaudeCodeInteractiveAdapter) generateContentTmuxBody(ctx context.Context, opts *llmtypes.CallOptions, messages []llmtypes.MessageContent) (resp *llmtypes.ContentResponse, err error) {
	var sessionName string
	defer func() {
		if isClaudeTmuxSessionLostError(err) {
			err = llmtypes.WrapCodingAgentTmuxSessionLostError(err, "claude-code", sessionName, "tmux session lost")
		}
	}()

	if err := ensureTmuxAvailable(ctx); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, fmt.Errorf("claude cli not found in PATH; install and authenticate Claude Code first")
	}

	resumeID := claudeResumeIDFromOptions(opts)
	interactiveSessionID := claudeInteractiveSessionIDFromOptions(opts)
	persistentInteractive := claudePersistentInteractiveFromOptions(opts) && interactiveSessionID != ""
	idleCompact := llmtypes.CodingProviderIdleCompactFromOptions(opts)
	if idleCompact && resumeID == "" {
		return llmtypes.IdleCompactSkipResponse("claude-code", llmtypes.IdleCompactReasonNoSession), nil
	}

	// On user-initiated cancellation, tear down the persistent tmux
	// session so the live pane closes alongside the workflow step.
	defer func() {
		if interactiveSessionID == "" || ctx.Err() != context.Canceled {
			return
		}
		closeClaudePersistentInteractiveSession(interactiveSessionID, "workflow context canceled", c.logger)
	}()
	nativeSessionID := resumeID
	if nativeSessionID == "" {
		nativeSessionID = newClaudeNativeSessionID()
	}
	workingDir := claudeWorkingDirFromOptions(opts)

	// Capture turn-start before launching the CLI so we only aggregate
	// usage from JSONL events emitted during this turn (the transcript
	// persists prior turns when --resume is used).
	turnStart := time.Now().UTC()

	callCtx, cancel := newClaudeCallContext(ctx, tmuxTimeout())
	defer cancel()

	runID := "mlp-claude-run-" + randomHex(8)

	systemPrompt, conversationMessages := splitSystemPrompt(messages)

	var persistentSession *claudeInteractivePersistentSession
	createdSession := true
	releasePersistentSession := false
	discardPersistentSession := func(err error) {
		if persistentInteractive && persistentSession != nil {
			markClaudePersistentInteractiveSessionFailedLocked(persistentSession, err, c.logger)
			releasePersistentSession = false
			failedSession := persistentSession
			persistentSession = nil
			failedSession.mu.Unlock()
			cleanupFailedClaudePersistentInteractiveSession(failedSession)
		}
	}
	defer func() {
		if releasePersistentSession && persistentSession != nil {
			releaseClaudePersistentInteractiveSession(persistentSession, c.logger)
		}
	}()

	var readyMarker codingready.ColdLaunchMarker
	if persistentInteractive {
		var err error
		persistentSession, createdSession, err = c.acquirePersistentInteractiveSession(callCtx, interactiveSessionID, nativeSessionID, opts, systemPrompt, workingDir)
		if err != nil {
			return nil, err
		}
		releasePersistentSession = true
		sessionName = persistentSession.tmuxSessionName
		readyMarker = persistentSession.readyMarker
		adoptClaudeModSession(persistentSession, c.logger)
		nativeSessionID = persistentSession.nativeSessionID
	} else {
		sessionName = newTmuxSessionName()
		args, tempFiles, err := c.buildClaudeArgs(opts, sessionName, nativeSessionID, systemPrompt)
		if err != nil {
			removeClaudeModSessionDir(sessionName)
			return nil, err
		}
		defer removeFiles(tempFiles)

		if opts != nil && opts.Metadata != nil {
			readyMarker = codingready.PrepareColdLaunch(codingready.MCPReadyFileFromMetadata(opts.Metadata.Custom))
		}
		if err := c.startSession(callCtx, sessionName, args, workingDir, opts); err != nil {
			removeClaudeModSessionDir(sessionName)
			return nil, err
		}
		registerClaudeInteractiveSession(sessionName)
		cleanupSession := cleanupClaudeInteractiveSessionAfter(sessionName, llmtypes.TmuxKillDelay)
		defer cleanupSession()
		if interactiveSessionID != "" {
			registerClaudeInteractiveOwner(interactiveSessionID, sessionName)
			defer unregisterClaudeInteractiveOwner(interactiveSessionID, sessionName)
		}
	}
	tmuxstartup.Publish(callCtx, opts.StreamChan, "claude-code", c.modelID, sessionName, workingDir, map[string]interface{}{
		"claude_code_interactive_session": sessionName,
	})

	if createdSession {
		if err := waitForTmuxPrompt(callCtx, sessionName, opts.StreamChan, claudeInteractiveStreamTmuxScreenEnabled(opts)); err != nil {
			discardPersistentSession(err)
			return nil, err
		}
		// The input box being ready does NOT mean the MCP servers have finished
		// connecting — those load asynchronously, so a cold session can fire its
		// first prompt before tools/list completes and the model runs its opening
		// turn with no tools. When the caller wired a readiness file (the MCP
		// server creates it on tools/list), hold the first prompt until it
		// appears. Bounded + best-effort: on timeout we proceed anyway rather
		// than block the turn. Only cold (createdSession) sessions pay this;
		// reused persistent sessions are already connected.
		if readyMarker.Path != "" {
			waitForMCPReadyMarker(callCtx, readyMarker, sessionName, c.logger)
		} else {
			waitForMCPReadyFile(callCtx, opts, sessionName, c.logger)
		}
		if err := waitForClaudeModReady(callCtx, sessionName); err != nil {
			discardPersistentSession(err)
			return nil, err
		}
	}

	if idleCompact {
		// Idle compaction (PLAT-872): /compact in the resumed pane, no person message.
		emit := func(chunk llmtypes.StreamChunk) {
			if opts.StreamChan != nil {
				select {
				case opts.StreamChan <- chunk:
				case <-callCtx.Done():
				}
			}
		}
		result, compactErr := runClaudeTmuxIdleCompact(callCtx, sessionName, emit)
		if compactErr != nil {
			discardPersistentSession(compactErr)
			return nil, compactErr
		}
		tmuxinput.MarkReady(sessionName)
		gi := &llmtypes.GenerationInfo{Additional: map[string]interface{}{"provider": "claude-code", "claude_code_mode": "tmux", "claude_code_session": sessionName, "claude_code_session_id": nativeSessionID}}
		llmtypes.AttachIdleCompactResult(gi, result)
		llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{Provider: "claude-code", Transport: llmtypes.CodingProviderTransportTmux, NativeSessionID: nativeSessionID, TmuxSession: sessionName, WorkingDir: workingDir, Model: c.modelID, Status: llmtypes.CodingProviderSessionStatusIdle})
		return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{GenerationInfo: gi}}}, nil
	}

	if llmtypes.CodingProviderLaunchOnlyFromOptions(opts) {
		tmuxinput.MarkReady(sessionName)
		var lastSnapshot string
		streamClaudeTerminalSnapshot(callCtx, sessionName, opts.StreamChan, &lastSnapshot)
		additional := map[string]interface{}{
			"provider":                           "claude-code",
			"claude_code_mode":                   "tmux",
			"claude_code_run_id":                 runID,
			"claude_code_session":                sessionName,
			"claude_code_session_id":             nativeSessionID,
			"claude_code_native_session_id":      nativeSessionID,
			"claude_code_resumed_session_id":     resumeID,
			"claude_code_uses_print_flag":        false,
			"claude_code_structured_streaming":   false,
			"claude_code_persistent_interactive": persistentInteractive,
		}
		gi := &llmtypes.GenerationInfo{Additional: additional}
		llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
			Provider:        "claude-code",
			Transport:       llmtypes.CodingProviderTransportTmux,
			NativeSessionID: nativeSessionID,
			TmuxSession:     sessionName,
			WorkingDir:      workingDir,
			Model:           c.modelID,
			Status:          llmtypes.CodingProviderSessionStatusIdle,
		})
		return &llmtypes.ContentResponse{
			Choices: []*llmtypes.ContentChoice{{
				Content:        "",
				GenerationInfo: gi,
			}},
		}, nil
	}

	prompt, err := buildTmuxPrompt(conversationMessages, opts, resumeID, persistentInteractive)
	if err != nil {
		return nil, err
	}

	var rememberInstructions func()
	prompt, rememberInstructions = claudeCurrentResumeInstructions(opts, workingDir, nativeSessionID, systemPrompt, prompt, resumeID != "")
	defer func() {
		if err == nil {
			rememberInstructions()
		}
	}()
	c.logger.Infof("Executing Claude Code tmux session: %s", sessionName)
	if _, err := deliverClaudeModInput(callCtx, sessionName, prompt); err != nil {
		discardPersistentSession(err)
		return nil, err
	}
	tmuxinput.MarkReady(sessionName)
	llmtypes.SignalPromptAccepted(opts.StreamChan)

	// Opt-in: stream structured content (assistant text + tool-call starts) by
	// tailing the CLI's JSONL transcript mid-turn, so a design-first UI can
	// render real content without the terminal pane. Additive — the goroutine
	// stops when this function returns. The completed JSONL end_turn below is the
	// authoritative final response; the pane remains the completion/display rail.
	if opts.StreamChan != nil && claudeInteractiveStreamTranscriptEnabled(opts) {
		stopTranscriptStream := startClaudeModStream(callCtx, sessionName, turnStart, opts.StreamChan)
		// streamClaudeTranscript performs one final transcript flush after its
		// context is cancelled. It must finish that flush before this body
		// returns: WithObservability owns (and immediately closes) StreamChan
		// after GenerateContent returns. Without the join, the detached tailer
		// can send into that closed channel and crash the whole server.
		defer stopTranscriptStream()
	}

	// Claude Code says when the turn ends, through the mod in this terminal.
	var content string
	end, ok := waitForClaudeModTurnComplete(callCtx, sessionName, turnStart)
	if ok && end.Reason == "error" {
		// The turn died on an API error; Claude Code's answer is that error.
		ok, err = false, claudeModTurnError(sessionName, end.Answer)
	} else if !ok && end.Type == "force_complete" {
		err = tmuxcontrol.ErrForceComplete
	} else if !ok && end.Type == "terminal_lost" {
		err = claudeModTerminalLost(context.Background(), sessionName)
	}
	if ok {
		content = end.Answer
		if persistentInteractive && persistentSession != nil {
			// A /clear typed before this turn has a transcript of its own by now.
			adoptClaudeModSession(persistentSession, c.logger)
			nativeSessionID = persistentSession.nativeSessionID
		}
		c.logger.Infof("[CLAUDE_MOD] turn complete tmux=%s session=%s reason=%s duration_ms=%d context=%d/%d", sessionName, end.SessionID, end.Reason, end.DurationMS, end.ContextTokens, end.ContextWindow)
	} else if err == nil {
		err = callCtx.Err()
	}

	if err != nil {
		if isClaudeTmuxSessionLostError(err) {
			discardPersistentSession(err)
		}
		if isContextCanceledError(err) {
			if interruptErr := interruptClaudeInteractiveSession(sessionName, c.logger); interruptErr != nil {
				if persistentInteractive && persistentSession != nil {
					discardPersistentSession(fmt.Errorf("Claude Code tmux session did not return to prompt after context cancellation: %w", interruptErr))
				}
			}
		}
		// opts.StreamChan close is owned by WithObservability.
		return nil, err
	}
	// Claude renders authentication failures as ordinary assistant-shaped text.
	// Treating that pane content as a successful completion poisons workflow
	// state and leaves a retained session permanently unusable.
	if isClaudeInteractiveAuthenticationFailure(content) {
		authErr := errors.New("claude-code interactive authentication failed: the CLI rejected the configured credential with HTTP 401")
		discardPersistentSession(authErr)
		return nil, authErr
	}
	// Cancellation wins over a response detected in the same polling cycle.
	// Without this check, a tool can finish just after the parent cancels and
	// waitForMarkedResponse can return content while GenerateContent reports a
	// false success. Discard the retained session as well: its prompt readiness
	// is no longer trustworthy after an interrupted tool turn.
	if ctxErr := ctx.Err(); ctxErr != nil {
		discardPersistentSession(ctxErr)
		return nil, ctxErr
	}
	// What the mod recorded for this turn: its history rows, the prompts it
	// answered and its model requests' token counts.
	record := claudeModRecordTurn(sessionName, turnStart)
	turnText := record.turnText
	// Trailing-capture grace window — see llmtypes.RunTrailingPaneCapture.
	// Skip for persistent interactive sessions: they live past the call
	// and are scraped by other paths.
	if !persistentInteractive {
		llmtypes.RunTrailingPaneCapture(callCtx, opts.StreamChan,
			func(ctx context.Context) (string, error) {
				snap, err := captureTmuxPane(ctx, sessionName)
				if err != nil {
					return "", err
				}
				return strings.TrimRight(snap, "\n"), nil
			},
			map[string]interface{}{
				"tmux_session":                    sessionName,
				"claude_code_interactive_session": sessionName,
			},
		)
	}
	if persistentInteractive && persistentSession != nil {
		checkCtx, checkCancel := context.WithTimeout(context.Background(), time.Second)
		exists, checkErr := claudeTmuxSessionExists(checkCtx, sessionName)
		checkCancel()
		if checkErr == nil && !exists {
			discardPersistentSession(fmt.Errorf("Claude Code tmux session %s ended after response capture", sessionName))
		}
	}
	closeResumeRef := ""
	responseSessionID := nativeSessionID
	if !persistentInteractive {
		// Stop accepting new owner-routed input before the close transaction enters
		// the tmux broker. The deferred unregister remains as an idempotent cleanup.
		if interactiveSessionID != "" {
			unregisterClaudeInteractiveOwner(interactiveSessionID, sessionName)
		}
		closeResumeRef = closeClaudeSessionForResume(sessionName, nativeSessionID, c.logger)
		if isUUIDLike(strings.TrimSpace(closeResumeRef)) {
			responseSessionID = closeResumeRef
		}
	}

	// opts.StreamChan close is owned by WithObservability.

	additional := map[string]interface{}{
		"provider":                           "claude-code",
		"claude_code_mode":                   "tmux",
		"claude_code_run_id":                 runID,
		"claude_code_session":                sessionName,
		"claude_code_session_id":             responseSessionID,
		"claude_code_native_session_id":      nativeSessionID,
		"claude_code_resumed_session_id":     resumeID,
		"claude_code_close_resume_ref":       closeResumeRef,
		"claude_code_uses_print_flag":        false,
		"claude_code_structured_streaming":   false,
		"claude_code_persistent_interactive": persistentInteractive,
		"context_window_usage_known":         false,
	}
	if !persistentInteractive {
		// terminal_retention_seconds intentionally not set: the rail
		// snapshot stays until the user dismisses it via the X button.
		additional["claude_code_interactive_retention_seconds"] = int(boundedRetentionTimeout().Seconds())
	}

	// Usage is the sum of the turn's model requests, as the mod reported them.
	gi := &llmtypes.GenerationInfo{Additional: additional}
	effectiveModel := c.modelID
	if record.requests > 0 {
		usage := claudeGenerationInfo(record.usage.InputTokens, record.usage.OutputTokens, record.usage.CacheCreationTokens, record.usage.CacheReadTokens)
		gi.PromptTokens = usage.PromptTokens
		gi.CompletionTokens = usage.CompletionTokens
		gi.TotalTokens = usage.TotalTokens
		gi.CachedContentTokens = usage.CachedContentTokens
		// The cost ledger reads the raw cache token keys.
		for k, v := range usage.Additional {
			additional[k] = v
		}
	}
	if model := record.usage.Model; model != "" {
		effectiveModel = model
		additional["claude_code_model"] = model
		if meta, _ := c.GetModelMetadata(model); meta != nil {
			if cost := llmtypes.ComputeUSDCostFromMetadata(meta, gi); cost > 0 {
				additional["cost_usd_estimated"] = cost
				additional["cost_model_id"] = model
			}
		}
	}
	llmtypes.AttachCodingProviderSessionHandle(gi, llmtypes.CodingProviderSessionHandle{
		Provider:        "claude-code",
		Transport:       llmtypes.CodingProviderTransportTmux,
		NativeSessionID: responseSessionID,
		TmuxSession:     sessionName,
		WorkingDir:      workingDir,
		Model:           effectiveModel,
		Status:          llmtypes.CodingProviderSessionStatusIdle,
	})

	// The CLI's own tool-use loop, from the rows the mod recorded, so workflow
	// conversation logs keep the in-CLI text/tool_use/tool_result trail.
	if sidecarMsgs := record.messages; len(sidecarMsgs) > 0 {
		llmtypes.AttachCodingProviderIntermediateMessages(gi, llmtypes.CodingProviderIntermediateMessages{
			Provider:  "claude-code",
			Transport: llmtypes.CodingProviderTransportTmux,
			Messages:  sidecarMsgs,
		})
	}

	// Answer, history and usage all come from the mod, never the pane.
	additional["claude_code_final_extraction_source"] = "claude_mod"
	additional["claude_code_completion_source"] = "claude_mod"
	additional["native_continuation_roots"] = record.roots
	// Chat surfaces want the whole turn (narration before tool calls too), not
	// only the committed end_turn answer that final_result keeps for workflows.
	if turnText != "" && turnText != content {
		additional["assistant_turn_text"] = turnText
	}

	return &llmtypes.ContentResponse{
		Choices: []*llmtypes.ContentChoice{
			{
				Content:        content,
				StopReason:     "stop",
				GenerationInfo: gi,
			},
		},
	}, nil
}

func containsClaudeImageContent(messages []llmtypes.MessageContent) bool {
	for _, msg := range messages {
		for _, part := range msg.Parts {
			switch part.(type) {
			case llmtypes.ImageContent, *llmtypes.ImageContent:
				return true
			}
		}
	}
	return false
}

func (c *ClaudeCodeInteractiveAdapter) GetModelID() string {
	return c.modelID
}

func (c *ClaudeCodeInteractiveAdapter) GetModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	modelID = CurrentClaudeCodeModel(modelID)
	if modelID == "" {
		modelID = c.modelID
	}
	return claudeCodeModelMetadata(modelID, "claude-code"), nil
}

func (c *ClaudeCodeInteractiveAdapter) buildClaudeArgs(opts *llmtypes.CallOptions, sessionName, nativeSessionID, systemPrompt string) ([]string, []string, error) {
	systemPrompt += llmtypes.CLIPrivateSkillInstructions(opts, ".claude/skills")
	extraArgs := []string{}
	var tempFiles []string
	toolsArg := ""
	resumeID := claudeResumeIDFromOptions(opts)

	if opts.Metadata != nil && opts.Metadata.Custom != nil {
		if mcpConfig, ok := opts.Metadata.Custom[MetadataKeyMCPConfig].(string); ok && strings.TrimSpace(mcpConfig) != "" {
			configPath, err := writeTempJSONConfig("claude-code-mcp-*.json", mcpConfig)
			if err != nil {
				return nil, nil, err
			}
			tempFiles = append(tempFiles, configPath)
			extraArgs = append(extraArgs, "--mcp-config", configPath, "--strict-mcp-config")
		}

		if sessionName != "" {
			settingsPath, sFiles, err := c.prepareStatusLineSettings(opts, sessionName)
			if err != nil {
				return nil, nil, err
			}
			tempFiles = append(tempFiles, sFiles...)
			extraArgs = append(extraArgs, "--settings", settingsPath)
		} else {
			mcpCfg, _ := opts.Metadata.Custom[MetadataKeyMCPConfig].(string)
			settings, _ := opts.Metadata.Custom[MetadataKeySettings].(string)
			// Merged with the caller's own settings.
			settingsMap := claudeLoadSettingsMap(settings)
			if claudeBridgeShellToolName(mcpCfg) != "" {
				// Platform calls in the native Bash cannot work (no credentials);
				// say so up front.
				if _, err := claudeAddShellRedirectHook(settingsMap, mcpCfg); err != nil {
					return nil, nil, err
				}
			}
			settingsPath, err := writeClaudeSkillScopedSettings(opts, settingsMap)
			if err != nil {
				return nil, nil, err
			}
			tempFiles = append(tempFiles, settingsPath)
			extraArgs = append(extraArgs, "--settings", settingsPath)
		}

		if tools, ok := opts.Metadata.Custom[MetadataKeyTools].(string); ok {
			toolsArg = tools
		}
		if allowedTools, ok := opts.Metadata.Custom[MetadataKeyAllowedTools].(string); ok && strings.TrimSpace(allowedTools) != "" {
			extraArgs = append(extraArgs, "--allowed-tools", allowedTools)
		}
		if effort, ok := opts.Metadata.Custom[MetadataKeyEffort].(string); ok && strings.TrimSpace(effort) != "" {
			extraArgs = append(extraArgs, "--effort", effort)
		}
	} else {
		if sessionName != "" {
			settingsPath, sFiles, err := c.prepareStatusLineSettings(opts, sessionName)
			if err != nil {
				return nil, nil, err
			}
			tempFiles = append(tempFiles, sFiles...)
			extraArgs = append(extraArgs, "--settings", settingsPath)
		} else {
			settingsPath, err := writeClaudeSkillScopedSettings(opts, map[string]any{})
			if err != nil {
				return nil, nil, err
			}
			tempFiles = append(tempFiles, settingsPath)
			extraArgs = append(extraArgs, "--settings", settingsPath)
		}
	}

	// --no-chrome prevents the "Claude in Chrome extension detected" startup modal
	// (offered in dontAsk mode when the Chrome extension is present), which would
	// otherwise block the input prompt and time the session out after 5m.
	permissionMode := "dontAsk"
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if mode, ok := opts.Metadata.Custom[MetadataKeyPermissionMode].(string); ok && strings.TrimSpace(mode) != "" {
			permissionMode = strings.TrimSpace(mode)
		}
	}
	args := []string{"claude", "--permission-mode", permissionMode, "--no-chrome"}
	if sessionName != "" {
		base, err := claudeModBaseDir(opts)
		if err != nil {
			return nil, nil, fmt.Errorf("prepare the AgentWorks Claude mod: %w", err)
		}
		claudeModBases.Store(sessionName, base)
		pluginDir, err := claudeModPluginDir(base.plugin)
		if err != nil {
			return nil, nil, fmt.Errorf("prepare the AgentWorks Claude mod: %w", err)
		}
		args = append(args, "--plugin-dir", pluginDir)
	}
	args = append(args, claudeSeatbeltAddDirs(opts, claudeWorkingDirFromOptions(opts))...)
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	} else if nativeSessionID != "" {
		args = append(args, "--session-id", nativeSessionID, "--name", defaultClaudeDisplayName())
	}
	if c.shouldPassModelFlag() {
		args = append(args, "--model", c.modelID)
	}

	// projectedToClaudeMd records whether the system prompt was successfully
	// written to <workingDir>/CLAUDE.md below. In project-instruction-only
	// mode it gates whether we still pass --system-prompt-file: when the
	// CLAUDE.md projection succeeded we skip the flag (the prompt is applied
	// once, via project instructions); otherwise we fall back to the flag.
	projectedToClaudeMd := false

	// Project the system prompt into <workingDir>/AGENTS.md (a per-session
	// marked block). ON by default; operators that need to protect their own
	// CLAUDE.md can opt out with WithClaudeCodeWriteProjectInstructionFile(false).
	// By default the prompt is ALSO injected via --system-prompt-file below;
	// CLAUDE.md makes it visible inside the workspace for debugging and
	// downstream tooling that reads project instructions. With
	// WithProjectInstructionOnly(true) the --system-prompt-file injection is
	// skipped and CLAUDE.md becomes the sole carrier (no doubled prompt).
	//
	// When the same flag is on AND an MCP config was provided via
	// WithMCPConfig, we also project the MCP servers into
	// <workingDir>/.mcp.json (Claude Code's project-scoped MCP
	// convention), removed on cleanup.
	// In project-instruction-only mode the AGENTS.md block is the only carrier, so it
	// is used only when this claude reads AGENTS.md; an older (or unreadable-version)
	// binary gets the prompt through --system-prompt-file below instead.
	carrierOK := !projectInstructionOnlyFromOptions(opts) || claudeReadsAgentsMD()
	if carrierOK && writeProjectInstructionFromOptions(opts) && opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		workingDir, _ := opts.Metadata.Custom[MetadataKeyWorkingDir].(string)
		if strings.TrimSpace(systemPrompt) != "" {
			if rulePath, err := writeClaudeCodeProjectInstructionFile(workingDir, systemPrompt); err != nil {
				// Best-effort: a write failure here must not block the
				// session. projectedToClaudeMd stays false so the
				// --system-prompt-file fallback below still fires.
				_ = err
			} else if rulePath != "" {
				tempFiles = append(tempFiles, rulePath)
				projectedToClaudeMd = true
			}
		}
		// .mcp.json projection is intentionally gated behind an env
		// var: dropping it triggers Claude Code's "New MCP server found
		// in .mcp.json — approve?" discovery prompt at startup, which
		// the tmux adapter cannot dismiss. Pre-recording approval in
		// ~/.claude.json (enabledMcpjsonServers + mcpServers) was
		// verified NOT to suppress the prompt on Claude Code v2.1.150.
		// The --mcp-config <temp> + --strict-mcp-config flags above
		// already load the MCP servers without triggering the prompt,
		// so disabling this projection by default loses only the
		// workspace-visibility belt-and-suspenders, not the actual MCP
		// loading. Set MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS=1 to
		// turn it back on if you have a way to handle the prompt
		// (e.g. a tmux send-keys post-launch dismissal).
		if os.Getenv("MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS") != "" {
			if mcpConfig, ok := opts.Metadata.Custom[MetadataKeyMCPConfig].(string); ok && strings.TrimSpace(mcpConfig) != "" && strings.TrimSpace(workingDir) != "" {
				if mcpPath, err := writeClaudeCodeProjectMCPFile(workingDir, mcpConfig, llmtypes.CLIHomeEnvironment(opts)["HOME"]); err != nil {
					_ = err
				} else if mcpPath != "" {
					tempFiles = append(tempFiles, mcpPath)
				}
			}
		}
	}

	// Inject the system prompt via --system-prompt-file UNLESS the caller
	// opted into project-instruction-only mode AND the CLAUDE.md projection
	// actually succeeded. In that case the prompt lives solely in CLAUDE.md
	// (auto-loaded as project instructions), so passing it here too would
	// double the system prompt. If CLAUDE.md was skipped or failed to write,
	// projectedToClaudeMd is false and we fall back to the flag so the prompt
	// is never silently dropped.
	if strings.TrimSpace(systemPrompt) != "" && !(projectInstructionOnlyFromOptions(opts) && projectedToClaudeMd) {
		systemPromptPath, err := writeTempFile("claude-code-system-prompt-*.md", systemPrompt)
		if err != nil {
			return nil, nil, err
		}
		tempFiles = append(tempFiles, systemPromptPath)
		args = append(args, "--system-prompt-file", systemPromptPath)
	}

	// Project attached skills into .claude/skills/ so Claude Code's
	// skill loader picks them up at startup. Independent of the
	// writeProjectInstructionFromOptions gate (which controls CLAUDE.md);
	// skills are useful even when the instruction-file projection is off.
	// Best-effort; matches the codex/gemini/cursor/pi pattern.
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if workingDir, _ := opts.Metadata.Custom[MetadataKeyWorkingDir].(string); strings.TrimSpace(workingDir) != "" {
			if skills := llmtypes.AttachedSkillsFromOptions(opts); len(skills) > 0 {
				_ = c.ProjectSkills(llmtypes.CLIArtifactDirectory(opts, workingDir), skills)
			}
		}
	}

	if denied := claudeNativeShellDisallowed(toolsArg); denied != "" {
		extraArgs = append(extraArgs, "--disallowedTools", denied)
	}
	args = append(args, "--tools", claudeEffectiveTools(toolsArg))
	args = append(args, extraArgs...)

	return args, tempFiles, nil
}

// writeProjectInstructionFromOptions reads the feature flag for writing
// the per-session system prompt to <workingDir>/CLAUDE.md. Defaults to
// true when the key is unset; callers can opt out by passing
// WithClaudeCodeWriteProjectInstructionFile(false).
func writeProjectInstructionFromOptions(opts *llmtypes.CallOptions) bool {
	if llmtypes.CLIHasPrivateRuntime(opts) {
		return false
	}
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return true
	}
	v, ok := opts.Metadata.Custom[MetadataKeyWriteProjectInstructionFile]
	if !ok {
		return true
	}
	enabled, _ := v.(bool)
	return enabled
}

// projectInstructionOnlyFromOptions reads the OFF-by-default feature flag for
// injecting the system prompt only via CLAUDE.md (skipping --system-prompt-file).
// Returns false when the key is unset. Callers opt in with
// WithProjectInstructionOnly(true).
func projectInstructionOnlyFromOptions(opts *llmtypes.CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	enabled, _ := opts.Metadata.Custom[MetadataKeyProjectInstructionOnly].(bool)
	return enabled
}

// writeClaudeCodeProjectInstructionFile adds the per-session system prompt
// to <workingDir>/AGENTS.md as a marked, per-session block and returns the
// lease token the caller appends to tempFiles; removeFiles releases it.
//
// Returns "" with nil error when no work needs doing (empty workingDir). The
// caller treats any error as best-effort and continues.
func writeClaudeCodeProjectInstructionFile(workingDir, systemPrompt string) (string, error) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return "", nil
	}
	// AGENTS.md is the one project-instructions file for every CLI (Claude
	// Code reads it natively). The prompt is added as a marked block, counted
	// per session, so the user's own file and other sessions sharing this
	// folder are never overwritten or deleted.
	return projectfile.AcquireLease(filepath.Join(workingDir, "AGENTS.md"), systemPrompt)
}

func (c *ClaudeCodeInteractiveAdapter) shouldPassModelFlag() bool {
	modelID := strings.TrimSpace(c.modelID)
	return modelID != "" && modelID != "claude-code"
}

func (c *ClaudeCodeInteractiveAdapter) startSession(ctx context.Context, sessionName string, args []string, workingDir string, opts *llmtypes.CallOptions) error {
	if workingDir != "" {
		// Pre-trust the working directory so Claude Code does not show its
		// interactive "Do you trust the files in this folder?" dialog, which
		// the adapter cannot dismiss in tmux mode and would cause a timeout.
		prepareClaudeUserConfig(workingDir, c.oauthToken, llmtypes.CLIHomeEnvironment(opts)["HOME"])
	}
	finalEnv := append(claudeInteractiveFinalEnv(c.oauthToken), claudeSkillScopeEnv()...)
	if claudeArgsHavePluginDir(args) {
		modDir, err := prepareClaudeModSessionDir(sessionName)
		if err != nil {
			return err
		}
		finalEnv = append(finalEnv, claudeModDirEnv+"="+modDir, claudeModInboxEnv+"="+claudeModInboxDir(sessionName),
			claudeModSlashEnv+"="+claudeModSlashCommands(), claudeModMCPServersEnv+"="+strings.Join(claudeMCPServerNames(opts), ","))
	}
	// This already scrubs Claude's own ambient auth keys. The caller's scoped
	// environment is a separate contract that the structured path applies when
	// it builds cmd.Env -- a tmux pane has no such slice, it inherits the tmux
	// SERVER's environment, so it has to be executed here as export/unset too.
	scopedEnv, scopedUnset := llmtypes.ScopedCodingAgentEnvironmentPlan(os.Environ(), finalEnv, opts)
	scopedScrub := launchscrub.Scoped(finalEnv, scopedEnv, opts)
	finalEnv = append(finalEnv, scopedEnv...)
	unsetKeys := append(append([]string(nil), claudeAmbientAuthEnvKeys...), scopedUnset...)
	args, cleanupSandbox, err := claudeLandlockArgs(opts, args, workingDir, sessionName)
	if err != nil {
		return fmt.Errorf("confine Claude Code: %w", err)
	}
	shellCommand, cleanupLaunchScript, err := shelllaunch.CommandWithScopedEnv(args, workingDir, finalEnv, unsetKeys, scopedScrub)
	if err != nil {
		cleanupSandbox()
		return fmt.Errorf("prepare Claude Code launch environment: %w", err)
	}
	stagedPolicy := ""
	if opts != nil && opts.CLISecurity.LandlockEnforced() && len(args) > 2 && args[1] == "--config" {
		stagedPolicy = args[2]
	}
	tmuxArgs := []string{"new-session", "-d", "-s", sessionName}
	tmuxArgs = append(tmuxArgs, claudePromptSuggestionEnvArgs()...)
	tmuxArgs = append(tmuxArgs, tmuxsize.Args()...)
	tmuxArgs = append(tmuxArgs, shellCommand)
	tmuxArgs = tmuxlaunch.WithHistoryLimit(tmuxArgs, defaultTmuxHistoryLimit)
	if err := runCommand(ctx, nil, "tmux", tmuxArgs...); err != nil {
		cleanupLaunchScript()
		cleanupSandbox()
		return fmt.Errorf("failed to start Claude Code tmux session %q: %w", sessionName, err)
	}
	retainClaudeSessionSandboxCleanup(sessionName, stagedPolicy, cleanupSandbox, 30*time.Second)
	// The wrapper deletes itself before starting the login shell. This delayed
	// cleanup is a backstop for an abnormal tmux child launch that never opened
	// the wrapper after new-session returned successfully.
	time.AfterFunc(30*time.Second, cleanupLaunchScript)
	if err := runCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "remain-on-exit", "on"); err != nil {
		return fmt.Errorf("failed to configure Claude Code tmux session %q: %w", sessionName, err)
	}
	if err := runCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "history-limit", defaultTmuxHistoryLimit); err != nil {
		return fmt.Errorf("failed to configure Claude Code tmux history for session %q: %w", sessionName, err)
	}
	// Pin the window size to manual. The session is detached (no attached
	// client), so tmux's default window-size "latest" recomputes the size
	// from the most-recent client — and with zero clients it collapses to
	// default-size (80x24). When that happens the Claude Code TUI reflows
	// into half its width: box borders squish, columns compress, lines
	// double-wrap, and the captured pane becomes unreadable. "manual" freezes
	// the size we launched at (tmuxsize.Args) until the frontend explicitly
	// resize-windows it.
	_ = runCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "window-size", "manual")
	// Enable focus-events so the Claude Code TUI receives focus in/out
	// sequences and stops parking its "tmux focus-events off · add
	// 'set -g focus-events on'" nag line in the footer.
	_ = runCommand(ctx, nil, "tmux", "set-option", "-t", sessionName, "focus-events", "on")
	return nil
}

var preTrustClaudeMu sync.Mutex

// preTrustClaudeWorkingDir marks workingDir as trusted in ~/.claude.json so
// Claude Code skips its interactive "Do you trust the files in this folder?"
// dialog. Trust is recorded under projects.<path>.hasTrustDialogAccepted.
// On macOS /var is a symlink to /private/var, so we record both the raw and
// resolved paths. Errors are silently ignored — the session will still launch
// and the adapter will time out on the trust prompt rather than failing here.
func preTrustClaudeWorkingDir(workingDir string) {
	prepareClaudeUserConfig(workingDir, "")
}

// prepareClaudeUserConfig applies the non-sensitive first-run configuration
// needed for a non-interactive tmux session. Claude Code currently ignores a
// valid CLAUDE_CODE_OAUTH_TOKEN during its OAuth onboarding screen (upstream
// issue #46259), even though the same token works for a normal -p invocation.
// Mark onboarding complete only when this adapter already holds an explicit
// token; sessions that rely on an interactive saved login retain Claude's
// normal onboarding flow.
func prepareClaudeUserConfig(workingDir, oauthToken string, accountHome ...string) {
	paths := pathidentity.Candidates(workingDir)

	configPath, err := claudeUserConfigPath(accountHome...)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return
	}

	preTrustClaudeMu.Lock()
	defer preTrustClaudeMu.Unlock()

	raw, readErr := os.ReadFile(configPath)
	var config map[string]interface{}
	if readErr == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &config)
	}
	if config == nil {
		config = map[string]interface{}{}
	}
	if strings.TrimSpace(oauthToken) != "" {
		config["hasCompletedOnboarding"] = true
	}
	// A chosen theme keeps the first-run appearance picker from blocking the
	// prompt; an operator's own choice is kept.
	if theme, _ := config["theme"].(string); strings.TrimSpace(theme) == "" {
		config["theme"] = "dark"
	}
	// "Set up auto mode for your environment?" takes over the input box in
	// auto-mode sessions once enough startups have accumulated, so an Enter
	// meant for our message selects "Set it up" (the default) instead. Its own
	// "Don't show again" choice writes autoModeEnvSetup.dismissed=true (Claude
	// Code 2.1.233); record the same so the dialog never appears unattended.
	autoModeSetup, _ := config["autoModeEnvSetup"].(map[string]interface{})
	if autoModeSetup == nil {
		autoModeSetup = map[string]interface{}{}
	}
	autoModeSetup["dismissed"] = true
	config["autoModeEnvSetup"] = autoModeSetup

	projects, _ := config["projects"].(map[string]interface{})
	if projects == nil {
		projects = map[string]interface{}{}
	}
	for _, p := range paths {
		entry, _ := projects[p].(map[string]interface{})
		if entry == nil {
			entry = map[string]interface{}{}
		}
		entry["hasTrustDialogAccepted"] = true
		entry["hasCompletedProjectOnboarding"] = true
		projects[p] = entry
	}
	config["projects"] = projects

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(configPath, out, 0o600)
}

func claudePromptSuggestionEnvArgs() []string {
	mcpToolIdleTimeoutMS := codingtimeout.LongRunningMCPToolTimeout().Milliseconds()
	args := []string{
		"-e", EnvClaudePromptSuggestion + "=false",
		// The session-quality survey ("How is Claude doing this session?")
		// takes over the input box; its documented off switch keeps it from
		// ever appearing in an unattended session.
		"-e", "CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1",
		"-e", EnvClaudeMCPToolIdleTimeout + "=" + strconv.FormatInt(mcpToolIdleTimeoutMS, 10),
		// Keep the classic renderer. Claude Code's fullscreen renderer (the
		// default on recent builds) draws inside the alternate screen, which
		// has no scrollback: the tmux pane keeps history_size=0, so AgentWorks'
		// terminal viewer has nothing to scroll back through (observed on server A,
		// Claude 2.1.233). The classic renderer writes the conversation into the
		// normal screen, where tmux history and the viewer's scrollback grow.
		"-e", EnvClaudeDisableAlternateScreen + "=1",
		// The tmux adapter relies on the user's Claude Code login. Inherited
		// Anthropic API env vars make recent Claude Code builds stop at an
		// interactive auth-choice prompt that this transport cannot answer.
		"-e", "ANTHROPIC_API_KEY=",
		"-e", "ANTHROPIC_AUTH_TOKEN=",
		"-e", "ANTHROPIC_BASE_URL=",
		// Never inherit a machine-wide OAuth token. A workflow token is applied
		// privately by the final launch wrapper; otherwise Claude uses /login.
		"-e", "CLAUDE_CODE_OAUTH_TOKEN=",
	}
	for _, name := range claudeParentSessionEnvNames {
		args = append(args, "-e", name+"=")
	}
	return args
}

// claudeParentSessionEnvNames are set by a running Claude Code session for
// its own children. A server (or test) started from inside Claude Code passes
// them on, and the launched Claude then treats itself as a child: it stops
// saving its transcript ("Transcript saving is off -- inherited
// CLAUDE_CODE_CHILD_SESSION marker", seen live 2026-09-24), which blinds
// every transcript-based completion, answer and durable-ack proof, and it can
// message the parent session over the inherited socket. Blank them on every
// launch.
var claudeParentSessionEnvNames = []string{
	"CLAUDECODE",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDE_CODE_MESSAGING_SOCKET",
	"CLAUDE_CODE_MESSAGING_TOKEN",
	"CLAUDE_CODE_SESSION_ATTENDED",
}

// withoutClaudeParentSessionEnv drops those variables from a process
// environment (structured `claude -p` launches build cmd.Env directly).
func withoutClaudeParentSessionEnv(environment []string) []string {
	out := environment[:0:0]
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, parent := range claudeParentSessionEnvNames {
			if name == parent {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, entry)
		}
	}
	return out
}

func claudeInteractiveShellCommand(args []string, workingDir string) string {
	return shelllaunch.Command(args, workingDir)
}

func buildTmuxPrompt(messages []llmtypes.MessageContent, opts *llmtypes.CallOptions, resumeID string, persistentInteractive bool) (string, error) {
	var b strings.Builder

	latestIndex := latestHumanMessageIndex(messages)
	if persistentInteractive || resumeID != "" || len(messages) <= 1 {
		latest := latestMessageForPrompt(messages, latestIndex)
		if latest != nil {
			b.WriteString(tmuxMessagePartsToText(latest.Parts))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("Previous conversation context:\n")
		for i, msg := range messages {
			if i == latestIndex {
				continue
			}
			b.WriteString("\n")
			b.WriteString(tmuxPromptRoleLabel(msg.Role))
			b.WriteString(":\n")
			b.WriteString(tmuxMessagePartsToText(msg.Parts))
			b.WriteString("\n")
		}
		if latestIndex >= 0 {
			b.WriteString("\nCurrent user message:\n")
			b.WriteString(tmuxMessagePartsToText(messages[latestIndex].Parts))
			b.WriteString("\n")
		}
	}

	return b.String(), nil
}

func latestMessageForPrompt(messages []llmtypes.MessageContent, latestHumanIndex int) *llmtypes.MessageContent {
	if latestHumanIndex >= 0 {
		return &messages[latestHumanIndex]
	}
	if len(messages) == 0 {
		return nil
	}
	return &messages[len(messages)-1]
}

func tmuxPromptRoleLabel(role llmtypes.ChatMessageType) string {
	switch role {
	case llmtypes.ChatMessageTypeHuman:
		return "User"
	case llmtypes.ChatMessageTypeAI:
		return "Assistant"
	case llmtypes.ChatMessageTypeSystem:
		return "System"
	case llmtypes.ChatMessageTypeTool:
		return "Tool"
	default:
		return "Message"
	}
}

func splitSystemPrompt(messages []llmtypes.MessageContent) (string, []llmtypes.MessageContent) {
	systemPrompts := make([]string, 0, 1)
	conversationMessages := make([]llmtypes.MessageContent, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == llmtypes.ChatMessageTypeSystem {
			text := strings.TrimSpace(tmuxMessagePartsToText(msg.Parts))
			if text != "" {
				systemPrompts = append(systemPrompts, text)
			}
			continue
		}
		conversationMessages = append(conversationMessages, msg)
	}
	return strings.Join(systemPrompts, "\n\n"), conversationMessages
}

func tmuxMessagePartsToText(parts []llmtypes.ContentPart) string {
	if len(parts) == 0 {
		return ""
	}

	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		switch p := part.(type) {
		case llmtypes.TextContent:
			texts = append(texts, p.Text)
		case *llmtypes.TextContent:
			if p != nil {
				texts = append(texts, p.Text)
			}
		case map[string]interface{}:
			texts = append(texts, tmuxMapContentPartToText(p))
		case map[string]string:
			texts = append(texts, tmuxStringMapContentPartToText(p))
		case json.RawMessage:
			texts = append(texts, tmuxRawMessageContentPartToText(p))
		case []byte:
			texts = append(texts, tmuxRawMessageContentPartToText(json.RawMessage(p)))
		case llmtypes.ImageContent:
			texts = append(texts, fmt.Sprintf("[image content: source_type=%s media_type=%s]", p.SourceType, p.MediaType))
		case *llmtypes.ImageContent:
			if p != nil {
				texts = append(texts, fmt.Sprintf("[image content: source_type=%s media_type=%s]", p.SourceType, p.MediaType))
			}
		case llmtypes.ToolCallResponse:
			texts = append(texts, fmt.Sprintf("[tool result id=%s name=%s is_error=%t]\n%s", p.ToolCallID, p.Name, p.IsError, p.Content))
		case *llmtypes.ToolCallResponse:
			if p != nil {
				texts = append(texts, fmt.Sprintf("[tool result id=%s name=%s is_error=%t]\n%s", p.ToolCallID, p.Name, p.IsError, p.Content))
			}
		default:
			texts = append(texts, fmt.Sprintf("[unsupported content part %T]", part))
		}
	}

	return strings.Join(texts, "\n")
}

func tmuxMapContentPartToText(part map[string]interface{}) string {
	for _, key := range []string{"Text", "text", "Content", "content"} {
		if text, ok := part[key].(string); ok {
			return text
		}
	}
	if typeValue, _ := part["type"].(string); typeValue != "" {
		return fmt.Sprintf("[unsupported %s content part]", typeValue)
	}
	return fmt.Sprintf("[unsupported content part %T]", part)
}

func tmuxStringMapContentPartToText(part map[string]string) string {
	for _, key := range []string{"Text", "text", "Content", "content"} {
		if text, ok := part[key]; ok {
			return text
		}
	}
	if typeValue := part["type"]; typeValue != "" {
		return fmt.Sprintf("[unsupported %s content part]", typeValue)
	}
	return fmt.Sprintf("[unsupported content part %T]", part)
}

func tmuxRawMessageContentPartToText(part json.RawMessage) string {
	var decoded map[string]interface{}
	if err := json.Unmarshal(part, &decoded); err != nil {
		return fmt.Sprintf("[unsupported content part %T]", part)
	}
	return tmuxMapContentPartToText(decoded)
}

func latestHumanMessageIndex(messages []llmtypes.MessageContent) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llmtypes.ChatMessageTypeHuman {
			return i
		}
	}
	return -1
}

func claudeResumeIDFromOptions(opts *llmtypes.CallOptions) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	if resumeID, ok := opts.Metadata.Custom[MetadataKeyResumeSessionID].(string); ok {
		return strings.TrimSpace(resumeID)
	}
	return ""
}

func newClaudeNativeSessionID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func defaultClaudeDisplayName() string {
	return "mcp-agent-" + time.Now().Format("20060102-150405")
}

// claudeInteractiveStreamTmuxScreenEnabled reports whether to push raw
// terminal-pane snapshots to the caller's StreamChan mid-turn. Default ON, set
// per call via WithStreamTmuxScreen — there is no environment-variable
// fallback.
func claudeInteractiveStreamTmuxScreenEnabled(opts *llmtypes.CallOptions) bool {
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if v, ok := opts.Metadata.Custom[MetadataKeyStreamTmuxScreen].(bool); ok {
			return v
		}
	}
	return true
}

// waitForMCPReadyFile holds a freshly created session's first prompt until the
// MCP readiness file (MetadataKeyMCPReadyFile) exists — the marker the session's
// MCP server writes once it has answered the CLI's tools/list handshake. This
// closes the cold-turn race in which the model runs its opening turn before its
// tools have connected (Claude then fabricates tool calls as text; Codex reports
// the tools unavailable). It is strictly best-effort: a no-op when no readiness
// file is configured or the file already exists, bounded by
// EnvClaudeMCPReadyWaitSeconds (default 30s), and it returns (never errors) on
// timeout or context cancellation so it can neither hang nor fail a turn — on
// timeout the session simply proceeds with today's behavior, minus the
// readiness guarantee.
func waitForMCPReadyFile(ctx context.Context, opts *llmtypes.CallOptions, sessionName string, logger interfaces.Logger) {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return
	}
	readyFile, _ := opts.Metadata.Custom[MetadataKeyMCPReadyFile].(string)
	waitForMCPReadyMarker(ctx, codingready.ColdLaunchMarker{Path: readyFile}, sessionName, logger)
}

func waitForMCPReadyMarker(ctx context.Context, marker codingready.ColdLaunchMarker, sessionName string, logger interfaces.Logger) {
	readyFile := strings.TrimSpace(marker.Path)
	if readyFile == "" {
		return
	}
	maxWait := 30 * time.Second
	if parsed, ok := claudePositiveDurationFromEnv(EnvClaudeMCPReadyWaitSeconds); ok {
		maxWait = parsed
	}
	start := time.Now()
	if codingready.WaitForMCPReadyFileAfter(ctx, marker, maxWait) {
		if logger != nil && time.Since(start) >= 50*time.Millisecond {
			logger.Debugf("Claude Code MCP tools connected after %s (session %s)", time.Since(start).Round(time.Millisecond), sessionName)
		}
		return
	}
	if ctx.Err() == nil && logger != nil {
		logger.Errorf("Claude Code MCP readiness file %q not present after %s; sending first prompt without the tools-connected guarantee (session %s)", readyFile, maxWait, sessionName)
	}
}

func waitForTmuxPrompt(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, streamTerminalScreen bool) error {
	// promptWait is a sliding INACTIVITY window, not a hard cap on the whole
	// wait. On resume Claude Code frequently compacts the conversation, and a
	// large compaction routinely runs longer than promptWait. A fixed deadline
	// fired mid-compaction, so waitForTmuxPrompt returned a timeout and the
	// caller never sent the user's message. Instead we reset the inactivity
	// timer whenever Claude is actively working (the compaction spinner / "esc
	// to interrupt" / running progress lines), so a genuinely hung pane still
	// aborts after promptWait of silence while a busy-but-progressing pane is
	// allowed to finish. maxWait is the absolute backstop for a pane that
	// reports activity forever.
	promptWait := promptReadyTimeout()
	maxWait := promptReadyMaxWait(promptWait)
	deadline, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	ticker := time.NewTicker(defaultTmuxPollInterval)
	defer ticker.Stop()
	resumePromptHandled := false
	trustPromptHandled := false
	themePromptHandled := false
	featurePromptsDismissed := 0
	var lastTerminalSnapshot string
	var lastTerminalStreamedAt time.Time

	lastActivityAt := time.Now()
	// Activity only counts when the screen changed since the last poll: a
	// real spinner or progress line animates, while a frozen redraw that
	// happens to contain such text does not. A resumed session froze mid-
	// redraw and was counted as busy for the whole 40-minute ceiling
	// (local 2026-10-08, salesoutreach Builder chat).
	lastActivityScreen := ""
	for {
		select {
		case <-deadline.Done():
			captured, _ := captureTmuxPane(context.Background(), sessionName)
			if strings.TrimSpace(captured) != "" {
				return claudePromptTimeoutError(fmt.Sprintf("timed out after %s waiting for Claude Code prompt", maxWait), sessionName, captured)
			}
			return fmt.Errorf("timed out after %s waiting for Claude Code prompt", maxWait)
		case <-ticker.C:
			captured, err := captureTmuxPane(deadline, sessionName)
			if err != nil {
				if isClaudeTmuxSessionLostError(err) {
					return err
				}
				continue
			}
			// Claude Code exited while starting: fail now, so the caller can relaunch,
			// instead of waiting out the inactivity window on a dead pane.
			if status, dead := claudeTmuxPaneExited(deadline, sessionName); dead {
				// Re-capture: the snapshot above can predate the exit, and the CLI's last words (e.g. "No conversation
				// found with session ID") stay on the dead pane because remain-on-exit keeps it.
				if final, err := captureTmuxPane(context.Background(), sessionName); err == nil && strings.TrimSpace(final) != "" {
					captured = final
				}
				return &llmtypes.CodingAgentTmuxSessionLostError{Provider: "claude-code", SessionName: sessionName, Reason: fmt.Sprintf("Claude Code exited while starting (status %s): %s", status, llmtypes.CompactTerminalPaneForError(sessionName, captured))}
			}
			if streamChan != nil && streamTerminalScreen {
				if time.Since(lastTerminalStreamedAt) >= time.Second && streamClaudeTerminalSnapshot(ctx, sessionName, streamChan, &lastTerminalSnapshot) {
					lastTerminalStreamedAt = time.Now()
				}
			}
			if !resumePromptHandled {
				resumePromptKeys := claudeResumeCompressionPromptSubmitKeys(captured)
				if len(resumePromptKeys) > 0 {
					resumePromptHandled = true
					args := append([]string{"send-keys", "-t", sessionName}, resumePromptKeys...)
					if err := runCommand(deadline, nil, "tmux", args...); err != nil {
						return fmt.Errorf("failed to continue Claude Code resumed session without compaction: %w", err)
					}
					lastActivityAt = time.Now()
					continue
				}
			}
			// Claude Code shows a one-time "Is this a project you trust?" folder
			// safety prompt on first launch in a fresh workspace. Its UI has changed
			// from a numbered menu to an arrow-selected menu whose initial selection
			// is "No, exit". Select the affirmative option by its visible text; never
			// assume that a key such as "1" maps to the desired row.
			if !trustPromptHandled {
				if keys := claudeTrustFolderPromptAcceptKeys(captured); len(keys) > 0 {
					args := append([]string{"send-keys", "-t", sessionName}, keys...)
					if err := runCommand(deadline, nil, "tmux", args...); err != nil {
						return fmt.Errorf("failed to select Claude Code trust-folder acceptance: %w", err)
					}
					// Only Enter accepts the choice. Directional navigation must be
					// observed on the next pane poll before confirmation.
					trustPromptHandled = len(keys) == 1 && keys[0] == "Enter"
					lastActivityAt = time.Now()
					continue
				}
			}
			// Claude Code v2.1.233 opens a one-time terminal-theme picker before
			// it creates the ordinary input prompt. It is not authentication, and
			// it has no "Enter to confirm" footer, so the generic numbered-menu
			// handler below intentionally does not match it. Keep its highlighted
			// default (Dark mode) by submitting Enter once; without this a freshly
			// provisioned server accepts no prompts at all.
			if !themePromptHandled && isClaudeThemeSelectionPrompt(captured) {
				themePromptHandled = true
				if err := runCommand(deadline, nil, "tmux", "send-keys", "-t", sessionName, "Enter"); err != nil {
					return fmt.Errorf("failed to accept Claude Code theme selection: %w", err)
				}
				lastActivityAt = time.Now()
				continue
			}
			// Catch-all for any OTHER numbered choice menu blocking the prompt — the
			// "Resume from summary / Resume full session" prompt, or a future startup
			// choice claude adds. Trust ("1. Yes") and feature-onboarding (Esc) are
			// handled above and excluded inside the predicate; here we accept claude's
			// highlighted recommended default with Enter (its convention for the safe
			// "proceed" option) so the session is never stuck on an unrecognized menu.
			// A ready input prompt means no modal is in the way. Resumed sessions
			// redraw earlier conversation text, which can look like a numbered
			// menu with "no thanks" in it; pressing keys at a ready prompt then
			// is harmful (two Escapes open Claude's Rewind list, and the next live
			// input could not be delivered -- local 2026-09-25 07:49).
			// Ready is the mod's to say: it reports once Claude Code's session has
			// started. The screen is read only for the startup dialogs above and
			// below, which come before that.
			if claudeModCurrentSessionID(sessionName) != "" && !isClaudeRewindSelector(captured) {
				return nil
			}
			if isClaudeRewindSelector(captured) {
				if err := runCommand(deadline, nil, "tmux", "send-keys", "-t", sessionName, "Escape"); err != nil {
					return fmt.Errorf("failed to close Claude Code rewind list: %w", err)
				}
				lastActivityAt = time.Now()
				continue
			}
			if featurePromptsDismissed < 5 && isClaudeBlockingChoiceMenu(captured) {
				featurePromptsDismissed++
				if err := runCommand(deadline, nil, "tmux", "send-keys", "-t", sessionName, "Enter"); err != nil {
					return fmt.Errorf("failed to accept Claude Code choice menu: %w", err)
				}
				lastActivityAt = time.Now()
				continue
			}
			// Optional feature/onboarding popups are prevented at launch, never
			// dismissed by pressing keys: --no-chrome, the classic renderer, and
			// the pre-seeded trust/onboarding/theme settings in ~/.claude.json.
			// A popup that still appears (a new one from a CLI update) is left
			// alone and reported with the screen when the startup wait ends, so
			// it can be prevented too. Guessing at it with Escape once misread
			// redrawn conversation as a popup and opened Claude's Rewind list
			// (local 2026-09-25).
			if claudeModCurrentSessionID(sessionName) != "" {
				return nil
			}
			// Reset the inactivity window while Claude is busy (compacting,
			// thinking, running tools) so a slow-but-progressing resume isn't
			// aborted before the input prompt appears.
			if (hasClaudeActivity(captured) || isClaudeCompactionInProgress(captured)) && captured != lastActivityScreen {
				lastActivityScreen = captured
				lastActivityAt = time.Now()
				continue
			}
			if time.Since(lastActivityAt) >= promptWait {
				captured, _ := captureTmuxPane(context.Background(), sessionName)
				if strings.TrimSpace(captured) != "" {
					return claudePromptTimeoutError(fmt.Sprintf("timed out after %s of inactivity waiting for Claude Code prompt", promptWait), sessionName, captured)
				}
				return fmt.Errorf("timed out after %s of inactivity waiting for Claude Code prompt", promptWait)
			}
		}
	}
}

func claudePromptTimeoutError(message, sessionName, captured string) error {
	diagnosticNote := saveClaudeTmuxPromptDiagnostic(sessionName, captured)
	return fmt.Errorf("%s; %s%s", message, llmtypes.CompactTerminalPaneForError(sessionName, captured), diagnosticNote)
}

func saveClaudeTmuxPromptDiagnostic(sessionName, captured string) string {
	dir := strings.TrimSpace(os.Getenv(EnvClaudeTmuxDiagnosticsDir))
	if dir == "" || strings.TrimSpace(captured) == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Sprintf("; could not save tmux diagnostic: %v", err)
	}
	name := fmt.Sprintf("claude-tmux-timeout-%s-%d.txt", sanitizeTmuxSessionName(sessionName), time.Now().UnixNano())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(captured), 0o600); err != nil {
		return fmt.Sprintf("; could not save tmux diagnostic: %v", err)
	}
	return fmt.Sprintf("; full tmux pane saved to %s", path)
}

// isClaudeTrustFolderPrompt detects Claude Code's first-launch "Is this a
// project you created or one you trust?" folder-trust dialog.
func isClaudeTrustFolderPrompt(captured string) bool {
	for _, rawLine := range strings.Split(captured, "\n") {
		if isClaudeTrustAcceptanceOption(rawLine) {
			return true
		}
	}
	return false
}

func isClaudeTrustAcceptanceOption(rawLine string) bool {
	line := strings.ToLower(strings.TrimSpace(rawLine))
	return strings.Contains(line, "yes") && strings.Contains(line, "trust") &&
		(strings.Contains(line, "folder") || strings.Contains(line, "project"))
}

// claudeTrustFolderPromptAcceptKeys selects the affirmative trust choice using
// the visible option text. Claude Code has used both numbered and arrow-menu
// variants; in the latter, the initial selection is commonly "No, exit".
//
// It deliberately navigates first and presses Enter only after a later pane
// capture shows that the affirmative row is selected. That makes a changed menu
// layout fail closed instead of accidentally declining the trust dialog.
func claudeTrustFolderPromptAcceptKeys(captured string) []string {
	if !isClaudeTrustFolderPrompt(captured) {
		return nil
	}

	type option struct {
		target   bool
		selected bool
	}
	var options []option
	for _, rawLine := range strings.Split(captured, "\n") {
		line := strings.ToLower(strings.TrimSpace(rawLine))
		target := isClaudeTrustAcceptanceOption(rawLine)
		decline := strings.Contains(line, "no, exit")
		if !target && !decline {
			continue
		}
		options = append(options, option{
			target:   target,
			selected: strings.Contains(rawLine, "❯"),
		})
	}

	targetIndex, selectedIndex := -1, -1
	for i, candidate := range options {
		if candidate.target {
			targetIndex = i
		}
		if candidate.selected {
			selectedIndex = i
		}
	}
	if targetIndex < 0 {
		return nil
	}
	if selectedIndex == targetIndex {
		return []string{"Enter"}
	}
	if selectedIndex < 0 {
		// No visible selected marker: reset to the first option, then navigate to
		// the row identified by the affirmative text. Do not press Enter yet.
		keys := []string{"Home"}
		for i := 0; i < targetIndex; i++ {
			keys = append(keys, "Down")
		}
		return keys
	}

	keys := make([]string, 0, absInt(targetIndex-selectedIndex))
	key := "Down"
	if targetIndex < selectedIndex {
		key = "Up"
	}
	for i := 0; i < absInt(targetIndex-selectedIndex); i++ {
		keys = append(keys, key)
	}
	return keys
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// isClaudeDismissableFeaturePrompt detects claude's optional startup onboarding /
// feature modals — a numbered menu offering to turn on a new feature, with a
// decline option ("Not now", "keep browser tools off", "Maybe later", ...). These
// block the input prompt; Esc declines them safely. Examples: "Claude in Chrome
// extension detected", "Try the new fullscreen renderer?". The trust-folder
// security prompt is explicitly excluded (it needs an affirmative "1. Yes").
// isClaudeRewindSelector reports Claude Code's Rewind list (opened by two
// Escapes at an idle prompt): "Restore the code and/or conversation to the
// point before…" with the latest entry highlighted as "❯ (current)". It is a
// selection list, not a draft: delete keys cannot clear it, one Escape closes
// it.
func isClaudeRewindSelector(captured string) bool {
	c := strings.ToLower(captured)
	return strings.Contains(c, "restore the code and/or conversation") &&
		strings.Contains(c, "(current)") && strings.Contains(c, "esc to cancel")
}

func isClaudeDismissableFeaturePrompt(captured string) bool {
	if isClaudeTrustFolderPrompt(captured) {
		return false
	}
	c := strings.ToLower(captured)
	hasMenu := strings.Contains(c, "❯ 1.") || (strings.Contains(c, "1.") && strings.Contains(c, "2."))
	hasDecline := strings.Contains(c, "not now") || strings.Contains(c, "maybe later") ||
		strings.Contains(c, "keep browser tools off") || strings.Contains(c, "no thanks") ||
		strings.Contains(c, "no, keep")
	return hasMenu && hasDecline
}

// isClaudeThemeSelectionPrompt identifies the initial appearance chooser
// introduced by Claude Code 2.1.233. Match its distinctive wording instead of
// treating every numbered menu as safe to accept: security and resume prompts
// have different required choices and are handled separately above.
func isClaudeThemeSelectionPrompt(captured string) bool {
	c := strings.ToLower(captured)
	return strings.Contains(c, "choose the text style that looks best with your terminal") &&
		strings.Contains(c, "to change this later, run /theme") &&
		strings.Contains(c, "dark mode")
}

// isClaudeBlockingChoiceMenu is the generic catch-all for a numbered selection
// menu that blocks the input prompt and is explicitly awaiting a choice (e.g.
// "Resume from summary / Resume full session", or a future startup choice). It
// EXCLUDES the two cases with a known non-default action — the trust prompt
// (needs "1. Yes") and feature-onboarding prompts (need Esc to decline) — so it
// only fires on an otherwise-unrecognized choice, where pressing Enter accepts
// claude's highlighted recommended default (its convention for the safe
// "proceed" option). The "awaiting a choice" gate keeps it off normal output
// that merely contains "1." / "2.".
func isClaudeBlockingChoiceMenu(captured string) bool {
	if isClaudeTrustFolderPrompt(captured) || isClaudeDismissableFeaturePrompt(captured) {
		return false
	}
	c := strings.ToLower(captured)
	hasMenu := strings.Contains(c, "❯ 1.") || (strings.Contains(c, "1.") && strings.Contains(c, "2."))
	awaitingChoice := strings.Contains(c, "enter to confirm") || strings.Contains(c, "press enter")
	return hasMenu && awaitingChoice
}

func claudeResumeCompressionPromptSubmitKeys(captured string) []string {
	if isClaudeResumeSummaryMenu(captured) {
		return claudeResumeSummaryMenuKeys(captured)
	}
	// Check TUI selection menu before the older text-based format \u2014 both may
	// contain "compact"+"continue", but the TUI menu needs arrow keys, not typing.
	if isClaudeResumeSelectMenu(captured) {
		return claudeResumeSelectMenuKeys(captured)
	}
	if isClaudeResumeCompressionPrompt(captured) {
		return []string{"continue", "C-m"}
	}
	return nil
}

func isClaudeResumeSummaryMenu(captured string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(captured, "\u00a0", " "))
	return strings.Contains(normalized, "resume from summary") &&
		strings.Contains(normalized, "enter to confirm") &&
		(strings.Contains(normalized, "resume full session") ||
			strings.Contains(normalized, "usage limits") ||
			strings.Contains(normalized, "substantial portion"))
}

// claudeResumeSummaryMenuKeys returns the tmux key sequence to choose "Resume
// full session as-is" from the resume-from-summary TUI menu (options:
// "Resume from summary (recommended)" / "Resume full session as-is" /
// "Don't ask me again"), regardless of which option the \u276f cursor currently
// sits on.
//
// Previously this always sent a bare Enter, accepting whatever was
// pre-selected -- which per Claude Code's own default is "Resume from
// summary (recommended)", the option that discards full conversation detail
// in favor of a compressed summary. That silently chose compression on every
// resume of a long conversation, the opposite of what a caller asking to
// preserve full context wants. Navigate to "Resume full session as-is"
// explicitly instead, the same way claudeResumeSelectMenuKeys already does
// for the separate compact-vs-continue menu.
func claudeResumeSummaryMenuKeys(captured string) []string {
	lines := strings.Split(strings.ReplaceAll(captured, "\u00a0", " "), "\n")
	cursorIdx, targetIdx := -1, -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if cursorIdx == -1 && strings.Contains(trimmed, "\u276f") {
			cursorIdx = i
		}
		if targetIdx == -1 {
			// "as-is"/"as is" is specific to the menu OPTION text ("Resume
			// full session as-is"). A bare "full session" check also matches
			// the descriptive paragraph above the menu ("Resuming the full
			// session will consume a substantial portion..."), which sits on
			// an earlier line than the real option and pointed navigation at
			// the wrong target entirely.
			lower := strings.ToLower(trimmed)
			if strings.Contains(lower, "as-is") || strings.Contains(lower, "as is") {
				targetIdx = i
			}
		}
	}
	if cursorIdx == -1 || targetIdx == -1 || cursorIdx == targetIdx {
		// Cursor already on the target, or either line couldn't be found --
		// accept whatever is selected rather than risk navigating blind.
		return []string{"C-m"}
	}
	key := "Down"
	steps := targetIdx - cursorIdx
	if steps < 0 {
		key = "Up"
		steps = -steps
	}
	keys := make([]string, 0, steps+1)
	for i := 0; i < steps; i++ {
		keys = append(keys, key)
	}
	return append(keys, "C-m")
}

// isClaudeResumeSelectMenu detects the interactive TUI selection menu Claude Code
// shows on resume when the conversation is long: a \u276f-cursor menu with a compact
// option and a "run/continue as is" option. The \u276f distinguishes it from the older
// text-based prompt where the user types "continue".
func isClaudeResumeSelectMenu(captured string) bool {
	normalized := strings.ReplaceAll(captured, "\u00a0", " ")
	lower := strings.ToLower(normalized)
	if !strings.Contains(normalized, "\u276f") {
		return false
	}
	hasCompact := strings.Contains(lower, "compact") || strings.Contains(lower, "compress")
	hasRunAsIs := strings.Contains(lower, "as is") ||
		strings.Contains(lower, "without compact") ||
		strings.Contains(lower, "without compress") ||
		strings.Contains(lower, "run as") ||
		strings.Contains(lower, "continue as") ||
		strings.Contains(lower, "continue without")
	return hasCompact && hasRunAsIs
}

// claudeResumeSelectMenuKeys returns the tmux key sequence to choose the
// "continue without compacting / run as is" option from the TUI selection menu.
// It checks which option the \u276f cursor is on: if it's already on the
// continue/as-is option, Enter is enough; if it's on the compact option,
// navigate down first.
func claudeResumeSelectMenuKeys(captured string) []string {
	lines := strings.Split(strings.ReplaceAll(captured, "\u00a0", " "), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, "\u276f") {
			continue
		}
		lower := strings.ToLower(trimmed)
		// A line is the "continue/run-as-is" option when it has qualifier words
		// like "without", "as is", "run as", or "continue as". These take
		// priority over "compact" appearing in the same line (e.g. "without compacting").
		isRunAsIs := strings.Contains(lower, "without") ||
			strings.Contains(lower, "as is") ||
			strings.Contains(lower, "run as") ||
			strings.Contains(lower, "continue as") ||
			strings.Contains(lower, "continue without")
		isCompact := (strings.Contains(lower, "compact") || strings.Contains(lower, "compress")) && !isRunAsIs
		if isCompact {
			// Cursor is on the compact option \u2014 move to the continue/as-is option below.
			return []string{"Down", "C-m"}
		}
		// Cursor is already on continue/run-as-is.
		return []string{"C-m"}
	}
	// Fallback: accept whatever is selected.
	return []string{"C-m"}
}

func isClaudeResumeCompressionPrompt(captured string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(captured, "\u00a0", " "))
	// Match the DISTINCTIVE wording of Claude Code's legacy text resume prompt
	// ("Would you like to compact the conversation or continue without
	// compacting?"), not loose keyword co-occurrence. The old check matched any
	// pane containing compact/compress + continue + resume/conversation/context
	// \u2014 which normal scrollback frequently does \u2014 so we'd type a stray "continue"
	// into whatever input was focused, garbling real drafts (e.g. turning a
	// "1. do it" prompt into "1. do itcontinue"). These phrases only appear in
	// the actual prompt.
	return strings.Contains(normalized, "without compacting") ||
		strings.Contains(normalized, "without compaction") ||
		strings.Contains(normalized, "continue without compact") ||
		(strings.Contains(normalized, "would you like to compact") && strings.Contains(normalized, "continue"))
}

func latestClaudePromptDraftRaw(captured string) (draft string, placeholder bool, ok bool) {
	lines := strings.Split(captured, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "❯" {
			return "", false, true
		}
		if strings.HasPrefix(trimmed, "❯") {
			draft := strings.TrimSpace(strings.TrimPrefix(trimmed, "❯"))
			return draft, isClaudePromptPlaceholder(draft), true
		}
	}
	return "", false, false
}

func isClaudePromptPlaceholder(draft string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(draft), " "))
	if normalized == "" {
		return false
	}
	return strings.HasPrefix(normalized, "type your message") ||
		strings.HasPrefix(normalized, "press up to edit queued messages") ||
		(strings.HasPrefix(normalized, "try ") && strings.Contains(normalized, "\"")) ||
		normalized == "show me what it found"
}

func closeClaudeSessionForResume(sessionName, knownSessionID string, logger interfaces.Logger) string {
	// The mod reports the session the terminal is on; the caller kills it.
	if id := claudeModCurrentSessionID(sessionName); id != "" {
		return id
	}
	return strings.TrimSpace(knownSessionID)
}

func interruptClaudeInteractiveSession(sessionName string, logger interfaces.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	since := time.Now()
	event, err := claudeModRequest(ctx, sessionName, "abort", "")
	if err == nil && event.Delivery == "aborted" {
		_, err = waitForClaudeModEvent(ctx, sessionName, "turn_complete", since)
	}
	if err != nil && logger != nil {
		logger.Debugf("Failed to interrupt Claude Code tmux session %s: %v", sessionName, err)
	}
	return err
}

func isContextCanceledError(err error) bool {
	return err != nil && errors.Is(err, context.Canceled)
}

func hasReadyEmptyInputPrompt(captured string) bool {
	if !hasReadyInputPrompt(captured) {
		return false
	}
	draft, placeholder, ok := latestClaudePromptDraftRaw(captured)
	if !ok {
		return false
	}
	return strings.TrimSpace(draft) == "" || placeholder
}

// PaneReadyForInput reports whether Claude Code's current tmux screen is at
// its empty, idle composer. Host runtimes use this after a stream-driven quiet
// boundary to settle a retained logical turn without terminating the reusable
// Claude process.
func PaneReadyForInput(captured string) bool {
	return hasReadyEmptyInputPrompt(captured)
}

func hasReadyInputPrompt(captured string) bool {
	normalized := strings.ReplaceAll(captured, "\u00a0", " ")
	lines := strings.Split(normalized, "\n")
	start := len(lines) - 80
	if start < 0 {
		start = 0
	}
	// Claude's bottom status area is intentionally extensible (updates,
	// Remote Control, context pills, and future indicators). Do not require a
	// whitelist of every line it can render. Find the most recent composer in
	// the recent pane, then determine whether it is busy from the activity
	// adjacent to that composer.
	promptIndex := -1
	for i := len(lines) - 1; i >= start; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "❯" || strings.HasPrefix(trimmed, "❯ ") {
			promptIndex = i
			break
		}
	}
	if promptIndex < 0 {
		return false
	}
	for i := promptIndex - 1; i >= start; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || isClaudeTUIBoundaryLine(trimmed) {
			continue
		}
		cleaned := cleanClaudeTerminalProgressLine(trimmed)
		if isClaudeRunningProgressLine(trimmed) ||
			isClaudeRunningProgressLine(cleaned) ||
			isClaudeToolInProgressLine(trimmed) {
			return false
		}
		break
	}
	return true
}

// isClaudeToolInProgressLine reports whether a terminal line is a LIVE,
// currently-running tool call -- unlike isClaudeToolProgressLine, it does not
// match past-tense "Called X" lines, including the collapsed
// "Called X (ctrl+o to expand)" summary Claude Code leaves as idle chrome
// above the prompt once a tool call has already finished. Past tense never
// means "still running"; the "(ctrl+o to expand)" hint additionally proves
// it, since a call only becomes expandable once it has already finished and
// its summary is collapsed -- a still-running call has nothing to expand
// yet. Using isClaudeToolProgressLine directly for readiness gating treated
// finished-call chrome as permanently in-progress, since it never leaves the
// pane once printed: the prompt was never judged ready and the session
// self-discarded after 5 minutes of what looked like inactivity but was a
// normal idle prompt sitting below a completed tool-call summary.
func isClaudeToolInProgressLine(trimmed string) bool {
	if strings.Contains(trimmed, "(ctrl+o to expand)") {
		return false
	}
	return strings.Contains(trimmed, "Calling ") ||
		(strings.Contains(trimmed, " - ") && strings.Contains(trimmed, "(MCP)"))
}

func hasClaudeActivity(captured string) bool {
	normalized := strings.ReplaceAll(captured, "\u00a0", " ")
	lines := strings.Split(normalized, "\n")
	if promptIndex := lastClaudePromptLineIndex(lines); promptIndex >= 0 {
		for i := promptIndex + 1; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if strings.Contains(trimmed, "esc to interrupt") ||
				isClaudeRunningProgressLine(trimmed) {
				return true
			}
		}
		start := promptIndex - 8
		if start < 0 {
			start = 0
		}
		for i := promptIndex - 1; i >= start; i-- {
			trimmed := strings.TrimSpace(lines[i])
			if isClaudeRunningProgressLine(trimmed) {
				return true
			}
		}
		return false
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "esc to interrupt") ||
			isClaudeRunningProgressLine(trimmed) ||
			isClaudeToolProgressLine(trimmed) {
			return true
		}
	}
	return false
}

func lastClaudePromptLineIndex(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "❯" || strings.HasPrefix(trimmed, "❯ ") {
			return i
		}
	}
	return -1
}

func isUUIDLike(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, ch := range value {
		switch i {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
				return false
			}
		}
	}
	return true
}

func streamClaudeTerminalSnapshot(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk, lastTerminalSnapshot *string) bool {
	streamClaudeStatusLine(ctx, sessionName, streamChan)
	snapshot, err := captureTmuxPaneForDisplay(ctx, sessionName)
	if err != nil {
		return false
	}
	snapshot = strings.TrimRight(stripClaudeANSIPreserveColors(snapshot), "\n")
	if strings.TrimSpace(snapshot) == "" || snapshot == *lastTerminalSnapshot {
		return false
	}
	select {
	case streamChan <- llmtypes.StreamChunk{
		Type:    llmtypes.StreamChunkTypeTerminal,
		Content: snapshot,
		Metadata: map[string]interface{}{
			"tmux_session":                    sessionName,
			"claude_code_interactive_session": sessionName,
		},
	}:
		*lastTerminalSnapshot = snapshot
		return true
	default:
		return false
	}
}

func isClaudeRunningProgressLine(trimmed string) bool {
	if isClaudeCompletedWorkStatusLine(trimmed) {
		return false
	}
	return strings.Contains(trimmed, "esc to interrupt") ||
		strings.HasPrefix(trimmed, "· ") ||
		strings.HasPrefix(trimmed, "✶ ") ||
		strings.HasPrefix(trimmed, "✳ ") ||
		strings.HasPrefix(trimmed, "✢ ") ||
		strings.HasPrefix(trimmed, "✽ ") ||
		strings.HasPrefix(trimmed, "✻ ")
}

func isClaudeCompletedWorkStatusLine(trimmed string) bool {
	trimmed = strings.TrimSpace(trimmed)
	if !(strings.HasPrefix(trimmed, "· ") ||
		strings.HasPrefix(trimmed, "✶ ") ||
		strings.HasPrefix(trimmed, "✳ ") ||
		strings.HasPrefix(trimmed, "✢ ") ||
		strings.HasPrefix(trimmed, "✽ ") ||
		strings.HasPrefix(trimmed, "✻ ")) {
		return false
	}
	return strings.Contains(trimmed, " for ") &&
		!strings.Contains(trimmed, "…") &&
		!strings.Contains(strings.ToLower(trimmed), "esc to interrupt")
}

func isClaudeToolProgressLine(trimmed string) bool {
	return strings.Contains(trimmed, "Calling ") ||
		strings.Contains(trimmed, "Called ") ||
		(strings.Contains(trimmed, " - ") && strings.Contains(trimmed, " (MCP)"))
}

func cleanClaudeTerminalProgressLine(trimmed string) string {
	trimmed = strings.TrimPrefix(trimmed, "⏺ ")
	trimmed = strings.TrimPrefix(trimmed, "● ")
	trimmed = strings.TrimSpace(trimmed)
	if isClaudeCompletedWorkStatusLine(trimmed) ||
		strings.HasPrefix(trimmed, "· ") ||
		strings.HasPrefix(trimmed, "⎿ Tip:") ||
		isClaudeTUIStatusLine(trimmed) ||
		strings.HasPrefix(trimmed, "⏵") ||
		strings.Contains(trimmed, "/effort") ||
		strings.Contains(trimmed, "don't ask on") {
		return ""
	}
	if isClaudeToolProgressLine(trimmed) {
		return normalizeClaudeToolProgressLine(trimmed)
	}
	return trimmed
}

func normalizeClaudeToolProgressLine(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.Index(line, " (ctrl+o"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	line = strings.TrimRight(line, ".… ")

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return line
	}

	switch fields[0] {
	case "Calling":
		return "Calling " + fields[1] + "..."
	case "Called":
		if len(fields) >= 4 && fields[2] != "1" && strings.HasPrefix(fields[3], "time") {
			return "Called " + fields[1] + " " + fields[2] + " times"
		}
		return "Called " + fields[1]
	default:
		return line
	}
}

func captureTmuxPane(ctx context.Context, sessionName string) (string, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-t", sessionName, "-p", "-J", "-S", "-"+defaultTmuxCaptureLines)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to capture Claude Code tmux session: %w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// stripClaudeANSIPreserveColors strips ANSI cursor positioning / clear-screen
// sequences but preserves SGR (Select Graphic Rendition: color, bold, dim,
// underline, etc., terminated with `m`). The frontend feeds this output
// through ansi_up to colorize the rendered pane snapshot. Cursor positioning
// is dropped because ansi_up does not emulate VT100 movement.
func stripClaudeANSIPreserveColors(s string) string {
	return paneview.StripANSIPreserveColors(s)
}

func captureTmuxPaneForDisplay(ctx context.Context, sessionName string) (string, error) {
	var out bytes.Buffer
	// -e preserves ANSI SGR (color, bold, dim, etc.) so the frontend can
	// colorize the snapshot via ansi_up. Cursor positioning sequences are
	// stripped by stripClaudeANSIPreserveColors in streamClaudeTerminalSnapshot
	// before the snapshot leaves the adapter so they don't garble rendering.
	// -J joins wrapped lines so the frontend can handle wrapping natively without
	// hard splitting words mid-line. Display snapshots intentionally capture only
	// the visible pane: deep scrollback flattens prior spinner/redraw frames into
	// duplicate rows before the app can render the current screen.
	cmd := exec.CommandContext(ctx, "tmux", "capture-pane", "-t", sessionName, "-p", "-e", "-J")
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to capture Claude Code display session: %w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func isClaudeTUIStatusLine(trimmed string) bool {
	trimmed = strings.TrimSpace(trimmed)
	trimmed = strings.TrimPrefix(trimmed, "⎿")
	trimmed = strings.TrimSpace(trimmed)
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "compacted") &&
		(strings.Contains(lower, "ctrl+o") || strings.Contains(lower, "full summary") || strings.Contains(lower, "history"))
}

// isClaudeCompactionInProgress reports whether the pane is actively compacting
// (or otherwise summarizing) the conversation — the long-running resume step
// that must NOT count against waitForTmuxPrompt's inactivity window. Matches
// the in-progress wording ("Compacting…" / "Summarizing…") but not the
// finished "Compacted" status line handled by isClaudeTUIStatusLine.
func isClaudeCompactionInProgress(captured string) bool {
	lower := strings.ToLower(strings.ReplaceAll(captured, " ", " "))
	return strings.Contains(lower, "compacting") || strings.Contains(lower, "summarizing")
}

func isClaudeTUIBoundaryLine(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(trimmed, "✻ ") ||
		strings.HasPrefix(trimmed, "✽ ") ||
		strings.HasPrefix(trimmed, "✶ ") ||
		strings.HasPrefix(trimmed, "✳ ") ||
		strings.HasPrefix(trimmed, "✢ ") ||
		strings.HasPrefix(trimmed, "╭") ||
		strings.HasPrefix(trimmed, "╰") ||
		strings.HasPrefix(trimmed, "────────────────") ||
		isClaudeTUIStatusLine(trimmed) ||
		strings.HasPrefix(trimmed, "❯") ||
		strings.HasPrefix(trimmed, "⏵")
}

func tmuxTimeout() time.Duration {
	seconds, ok := claudeDurationSecondsFromEnv(EnvClaudeTmuxTimeoutSeconds)
	if !ok || seconds < 0 {
		return defaultTmuxTimeout
	}
	return time.Duration(seconds) * time.Second
}

func promptReadyTimeout() time.Duration {
	return tmuxlaunch.PromptWait(EnvClaudeTmuxPromptWaitSeconds)
}

// promptReadyMaxWait is the absolute ceiling waitForTmuxPrompt will wait for a
// ready input prompt while Claude keeps reporting activity (e.g. a long
// compaction on resume). The inactivity window (promptReadyTimeout) handles a
// hung pane; this only bounds a pane that looks busy forever. Defaults to a
// generous multiple of the inactivity window so real compactions complete.
func promptReadyMaxWait(idleWait time.Duration) time.Duration {
	if parsed, ok := claudePositiveDurationFromEnv(EnvClaudeTmuxPromptMaxWaitSeconds); ok {
		return parsed
	}
	// A real compaction finishes well inside this; a pane that looks busy
	// longer is treated as hung (it was 8x, 40 minutes by default, which held
	// a chat for 40 minutes on 2026-10-08).
	maxWait := idleWait * 3
	if maxWait < 15*time.Minute {
		maxWait = 15 * time.Minute
	}
	return maxWait
}

func persistentInteractiveIdleTimeout() time.Duration {
	seconds, ok := claudeDurationSecondsFromEnv(EnvClaudeTmuxIdleTimeoutSeconds)
	if !ok || seconds <= 0 {
		return defaultPersistentIdleTimeout
	}
	return time.Duration(seconds) * time.Second
}

func claudePositiveDurationFromEnv(keys ...string) (time.Duration, bool) {
	seconds, ok := claudeDurationSecondsFromEnv(keys...)
	if !ok || seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func claudeDurationSecondsFromEnv(keys ...string) (int, bool) {
	for _, key := range keys {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		seconds, err := strconv.Atoi(raw)
		if err != nil {
			return 0, false
		}
		return seconds, true
	}
	return 0, false
}

func boundedRetentionTimeout() time.Duration {
	return tmuxlaunch.Retention(defaultBoundedRetention)
}

func ensureTmuxAvailable(ctx context.Context) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found in PATH; claude-code requires tmux %d.x or newer for interactive mode", minTmuxMajorVersion)
	}

	versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(versionCtx, "tmux", "-V").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to check tmux version: %w: %s", err, strings.TrimSpace(string(out)))
	}

	major, ok := parseTmuxMajorVersion(string(out))
	if !ok {
		return fmt.Errorf("failed to parse tmux version from %q; claude-code requires tmux %d.x or newer", strings.TrimSpace(string(out)), minTmuxMajorVersion)
	}
	if major < minTmuxMajorVersion {
		return fmt.Errorf("tmux %s is too old; claude-code requires tmux %d.x or newer", strings.TrimSpace(string(out)), minTmuxMajorVersion)
	}
	return nil
}

func parseTmuxMajorVersion(version string) (int, bool) {
	fields := strings.Fields(strings.TrimSpace(version))
	if len(fields) < 2 || fields[0] != "tmux" {
		return 0, false
	}

	raw := fields[1]
	digits := strings.Builder{}
	for _, r := range raw {
		if r < '0' || r > '9' {
			break
		}
		digits.WriteRune(r)
	}
	if digits.Len() == 0 {
		return 0, false
	}

	major, err := strconv.Atoi(digits.String())
	if err != nil {
		return 0, false
	}
	return major, true
}

// CleanupClaudeCodeTmuxSessions cleans up registered Claude Code tmux sessions.
func CleanupClaudeCodeTmuxSessions(ctx context.Context) error {
	closeAllKeptClaudeSDKSessions()
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}

	sessions := activeClaudeInteractiveSessions()
	persistentSessions := drainClaudePersistentInteractiveSessions()
	for _, session := range persistentSessions {
		sessions = appendUniqueStrings(sessions, session.tmuxSessionName)
		unregisterClaudeInteractiveOwner(session.ownerSessionID, session.tmuxSessionName)
		removeFiles(session.tempFiles)
	}

	var failures []string
	for _, sessionName := range sessions {
		if err := killClaudeInteractiveSession(ctx, sessionName); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("failed to clean up Claude Code tmux sessions: %s", strings.Join(failures, "; "))
	}
	return nil
}

func activeClaudeInteractiveSessions() []string {
	claudeInteractiveSessionRegistry.Lock()
	defer claudeInteractiveSessionRegistry.Unlock()

	sessions := make([]string, 0, len(claudeInteractiveSessionRegistry.sessions))
	for sessionName := range claudeInteractiveSessionRegistry.sessions {
		sessions = append(sessions, sessionName)
	}
	return sessions
}

func appendUniqueStrings(base []string, extra ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, value := range append(base, extra...) {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func claudeInteractiveSessionPrefix() string {
	prefix := strings.TrimSpace(os.Getenv(EnvClaudeTmuxSessionPrefix))
	if prefix == "" {
		prefix = "mlp-claude-code"
	}
	return sanitizeTmuxSessionName(prefix)
}

func newTmuxSessionName() string {
	prefix := claudeInteractiveSessionPrefix()
	return sanitizeTmuxSessionName(fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano(), randomHex(4)))
}

func registerClaudeInteractiveSession(sessionName string) {
	claudeInteractiveSessionRegistry.Lock()
	defer claudeInteractiveSessionRegistry.Unlock()
	claudeInteractiveSessionRegistry.sessions[sessionName] = struct{}{}
}

func unregisterClaudeInteractiveSession(sessionName string) {
	claudeInteractiveSessionRegistry.Lock()
	cleanup := claudeInteractiveSessionRegistry.sandboxCleanup[sessionName]
	delete(claudeInteractiveSessionRegistry.sandboxCleanup, sessionName)
	delete(claudeInteractiveSessionRegistry.sessions, sessionName)
	claudeInteractiveSessionRegistry.Unlock()
	if cleanup != nil {
		cleanup()
	}
}

func registerClaudeInteractiveOwner(ownerSessionID, tmuxSessionName string) {
	tmuxSessionName = strings.TrimSpace(tmuxSessionName)
	if tmuxSessionName == "" {
		return
	}
	tmuxinput.MarkStartingForOwner(tmuxSessionName, ownerSessionID)
	claudeInteractiveOwnerRegistry.Set(ownerSessionID, tmuxSessionName)
}

func unregisterClaudeInteractiveOwner(ownerSessionID, tmuxSessionName string) {
	claudeInteractiveOwnerRegistry.DeleteIf(ownerSessionID, tmuxSessionName)
	tmuxinput.RemoveReadiness(tmuxSessionName)
}

func activeClaudeInteractiveOwner(ownerSessionID string) (string, bool) {
	sessionName, ok := claudeInteractiveOwnerRegistry.Get(ownerSessionID)
	return sessionName, ok && strings.TrimSpace(sessionName) != ""
}

func claudeInteractiveSessionIDFromOptions(opts *llmtypes.CallOptions) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	if sessionID, ok := opts.Metadata.Custom[MetadataKeyInteractiveSessionID].(string); ok {
		return strings.TrimSpace(sessionID)
	}
	return ""
}

func claudePersistentInteractiveFromOptions(opts *llmtypes.CallOptions) bool {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return false
	}
	enabled, ok := opts.Metadata.Custom[MetadataKeyPersistentInteractive].(bool)
	return ok && enabled
}

func claudeWorkingDirFromOptions(opts *llmtypes.CallOptions) string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return ""
	}
	if dir, ok := opts.Metadata.Custom[MetadataKeyWorkingDir].(string); ok {
		return strings.TrimSpace(dir)
	}
	return ""
}

func claudeToolConfigFingerprint(opts *llmtypes.CallOptions) string {
	config := map[string]string{}
	if opts != nil && opts.Metadata != nil {
		for _, key := range []string{MetadataKeyTools, MetadataKeyAllowedTools, MetadataKeySettings} {
			if value, ok := opts.Metadata.Custom[key].(string); ok {
				value = strings.TrimSpace(value)
				if key == MetadataKeyTools {
					value = claudeEffectiveTools(value)
				}
				if key == MetadataKeySettings && value != "" {
					if !strings.HasPrefix(value, "{") {
						if data, err := os.ReadFile(value); err == nil {
							value = string(data)
						}
					}
					var settings map[string]interface{}
					if json.Unmarshal([]byte(value), &settings) == nil {
						data, _ := json.Marshal(settings)
						value = string(data)
					}
				}
				config[key] = value
			}
		}
	}
	data, _ := json.Marshal(config)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func claudeOptionsResumingNativeSession(opts *llmtypes.CallOptions, sessionID string) *llmtypes.CallOptions {
	copy := *opts
	metadata := llmtypes.Metadata{}
	if opts.Metadata != nil {
		metadata = *opts.Metadata
		metadata.Custom = maps.Clone(opts.Metadata.Custom)
	}
	copy.Metadata = &metadata
	WithResumeSessionID(sessionID)(&copy)
	return &copy
}

// acquirePersistentInteractiveSession returns with session.mu held. The caller
// must releaseClaudePersistentInteractiveSession on normal completion, or mark,
// unlock, and clean up the session on a ready-prompt failure.
func (c *ClaudeCodeInteractiveAdapter) acquirePersistentInteractiveSession(ctx context.Context, ownerSessionID, nativeSessionID string, opts *llmtypes.CallOptions, systemPrompt string, workingDir string) (*claudeInteractivePersistentSession, bool, error) {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return nil, false, fmt.Errorf("persistent Claude Code tmux session requires an owner session ID")
	}

	now := time.Now()
	session, created, ok := claudeInteractivePersistentRegistry.GetOrCreate(ownerSessionID, func() *claudeInteractivePersistentSession {
		sessionName := newTmuxSessionName()
		session := &claudeInteractivePersistentSession{
			ownerSessionID:        ownerSessionID,
			tmuxSessionName:       sessionName,
			nativeSessionID:       nativeSessionID,
			authFingerprint:       c.authFingerprint,
			scopeFingerprint:      llmtypes.CodingAgentScopeFingerprint(opts),
			toolConfigFingerprint: claudeToolConfigFingerprint(opts),
			workingDir:            strings.TrimSpace(workingDir),
			accountHome:           llmtypes.CLIHomeEnvironment(opts)["HOME"],
			createdAt:             now,
		}
		session.mu.Lock()
		return session
	})
	if !ok {
		return nil, false, fmt.Errorf("persistent Claude Code tmux session requires an owner session ID")
	}
	if !created {
		// Release the registry (map) lock BEFORE taking the per-session lock.
		// session.mu is held for a whole turn; holding the global map lock
		// across it stalls every other acquire behind a busy session
		// (lock-held-across-blocking-call deadlock).
		session.mu.Lock()
		if session.authFingerprint != c.authFingerprint {
			session.mu.Unlock()
			closeClaudePersistentInteractiveSession(ownerSessionID, "Claude Code credential changed", c.logger)
			return c.acquirePersistentInteractiveSession(ctx, ownerSessionID, nativeSessionID, opts, systemPrompt, workingDir)
		}
		// Same rule, applied to the caller's scope: the live process still
		// holds the environment it launched with, so a changed (or removed)
		// scope cannot take effect without replacing it. Reusing here is what
		// made credential revocation impossible without killing the session.
		if session.scopeFingerprint != llmtypes.CodingAgentScopeFingerprint(opts) {
			session.mu.Unlock()
			closeClaudePersistentInteractiveSession(ownerSessionID, "Claude Code credential scope changed", c.logger)
			return c.acquirePersistentInteractiveSession(ctx, ownerSessionID, nativeSessionID, opts, systemPrompt, workingDir)
		}
		if session.toolConfigFingerprint != claudeToolConfigFingerprint(opts) {
			// Resume the same transcript after changing launch settings. Copy the
			// options because callers may reuse them for another turn.
			resumeSessionID := session.nativeSessionID
			session.mu.Unlock()
			closeClaudePersistentInteractiveSession(ownerSessionID, "Claude Code native tools or hooks changed", c.logger)
			if resumeSessionID != "" {
				opts = claudeOptionsResumingNativeSession(opts, resumeSessionID)
				nativeSessionID = resumeSessionID
			}
			return c.acquirePersistentInteractiveSession(ctx, ownerSessionID, nativeSessionID, opts, systemPrompt, workingDir)
		}
		if session.initErr != nil {
			err := session.initErr
			session.mu.Unlock()
			return nil, false, err
		}
		session.idleLease.Pause()
		return session, false, nil
	}

	// The registry disappears on backend restart; tmux and its CLI do not.
	// Reclaim only this chat before launching a replacement (PLAT-746-859).
	closePreviousClaudeTmuxCopies(ctx, ownerSessionID)

	args, tempFiles, err := c.buildClaudeArgs(opts, session.tmuxSessionName, nativeSessionID, systemPrompt)
	if err != nil {
		removeClaudeModSessionDir(session.tmuxSessionName)
		session.initErr = err
		session.mu.Unlock()
		removeClaudePersistentInteractiveSession(ownerSessionID, session)
		return nil, false, err
	}
	session.tempFiles = tempFiles
	if opts != nil && opts.Metadata != nil {
		session.readyMarker = codingready.PrepareColdLaunch(codingready.MCPReadyFileFromMetadata(opts.Metadata.Custom))
	}

	if err := c.startSession(ctx, session.tmuxSessionName, args, workingDir, opts); err != nil {
		removeClaudeModSessionDir(session.tmuxSessionName)
		session.initErr = err
		session.mu.Unlock()
		removeClaudePersistentInteractiveSession(ownerSessionID, session)
		removeFiles(tempFiles)
		return nil, false, err
	}
	registerClaudeInteractiveSession(session.tmuxSessionName)
	registerClaudeInteractiveOwner(ownerSessionID, session.tmuxSessionName)
	return session, true, nil
}

func releaseClaudePersistentInteractiveSession(session *claudeInteractivePersistentSession, logger interfaces.Logger) {
	if session == nil {
		return
	}
	armClaudePersistentIdleLease(session, logger)
	session.mu.Unlock()
}

// armClaudePersistentIdleLease records both ordinary-turn release and retained
// direct-input activity through the same shared lease implementation.
func armClaudePersistentIdleLease(session *claudeInteractivePersistentSession, logger interfaces.Logger) bool {
	if session == nil {
		return false
	}
	return session.idleLease.Arm(persistentInteractiveIdleTimeout(), func() {
		closeClaudePersistentInteractiveSession(session.ownerSessionID, "idle timeout", logger)
	})
}

// CloseClaudeCodeInteractiveSessionForOwner closes the persistent
// Claude Code interactive session for the given owner. See cursorcli's
// equivalent CloseCursorCLIInteractiveSessionForOwner for the
// mid-chat-prompt-change motivation.
func CloseClaudeCodeInteractiveSessionForOwner(ownerSessionID, reason string) {
	if process, ok := claudeSDKProcesses.Get(ownerSessionID); ok {
		process.cancel()
	}
	closeKeptClaudeSDKSession(ownerSessionID, "chat_closed: "+reason)

	closeClaudePersistentInteractiveSession(ownerSessionID, reason, nil)
}

// CloseClaudeCodeInteractiveSessionByTmux closes the persistent Claude Code
// interactive session whose backing tmux session matches tmuxSessionName,
// regardless of the owner key it was registered under. Teardown backstop for
// when the owning session ID is unknown or has drifted. Delegates to the
// owner-keyed close so the same graceful exit + cleanup runs. No-op when no
// live session matches.
func CloseClaudeCodeInteractiveSessionByTmux(tmuxSessionName, reason string) {
	name := strings.TrimSpace(tmuxSessionName)
	if name == "" {
		return
	}
	owner, _, ok := claudeInteractivePersistentRegistry.Find(func(s *claudeInteractivePersistentSession) bool {
		return s != nil && s.tmuxSessionName == name
	})
	if !ok || owner == "" {
		return
	}
	closeClaudePersistentInteractiveSession(owner, reason, nil)
}

func closeClaudePersistentInteractiveSession(ownerSessionID, reason string, logger interfaces.Logger) {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return
	}

	session, ok := claudeInteractivePersistentRegistry.Delete(ownerSessionID)
	if !ok || session == nil {
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	session.idleLease.Stop()
	if logger != nil {
		logger.Debugf("Closing persistent Claude Code tmux session %s for owner %s: %s", session.tmuxSessionName, ownerSessionID, reason)
	}
	unregisterClaudeInteractiveOwner(ownerSessionID, session.tmuxSessionName)
	_ = closeClaudeSessionForResume(session.tmuxSessionName, session.nativeSessionID, logger)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = killClaudeInteractiveSession(cleanupCtx, session.tmuxSessionName)
	unregisterClaudeInteractiveSession(session.tmuxSessionName)
	removeFiles(session.tempFiles)
}

func markClaudePersistentInteractiveSessionFailedLocked(session *claudeInteractivePersistentSession, err error, logger interfaces.Logger) {
	if session == nil {
		return
	}
	if err != nil {
		session.initErr = err
	}
	session.idleLease.Stop()
	if logger != nil {
		logger.Debugf("Discarding persistent Claude Code tmux session %s for owner %s: %v", session.tmuxSessionName, session.ownerSessionID, err)
	}
}

func cleanupFailedClaudePersistentInteractiveSession(session *claudeInteractivePersistentSession) {
	if session == nil {
		return
	}
	removeClaudePersistentInteractiveSession(session.ownerSessionID, session)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = killClaudeInteractiveSession(cleanupCtx, session.tmuxSessionName)
	unregisterClaudeInteractiveOwner(session.ownerSessionID, session.tmuxSessionName)
	unregisterClaudeInteractiveSession(session.tmuxSessionName)
	removeFiles(session.tempFiles)
}

func removeClaudePersistentInteractiveSession(ownerSessionID string, session *claudeInteractivePersistentSession) {
	claudeInteractivePersistentRegistry.DeleteIf(ownerSessionID, session)
}

func drainClaudePersistentInteractiveSessions() []*claudeInteractivePersistentSession {
	sessions := claudeInteractivePersistentRegistry.Drain()

	for _, session := range sessions {
		stopClaudeIdleTimerIfAvailable(session)
	}
	return sessions
}

func stopClaudeIdleTimerIfAvailable(session *claudeInteractivePersistentSession) {
	if session == nil || !session.mu.TryLock() {
		return
	}
	defer session.mu.Unlock()
	session.idleLease.Stop()
}

// SendClaudeCodeInput routes live input into a registered Claude Code tmux session.
func SendClaudeCodeInput(ctx context.Context, ownerSessionID, message string) error {
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	if ownerSessionID == "" {
		return fmt.Errorf("Claude Code owner session ID is required")
	}
	if session, ok := claudeInteractivePersistentRegistry.Get(ownerSessionID); ok && session != nil {
		if !armClaudePersistentIdleLease(session, nil) {
			return fmt.Errorf("Claude Code session for owner %s is expiring; retry to resume it in a fresh tmux session", ownerSessionID)
		}
	}
	sessionName, ok := activeClaudeInteractiveOwner(ownerSessionID)
	if !ok {
		return fmt.Errorf("no active Claude Code tmux session registered for owner session %s", ownerSessionID)
	}
	started := time.Now()
	delivery, err := deliverClaudeModInput(ctx, sessionName, message)
	if err != nil {
		return err
	}
	recordClaudeModInputTaken(ownerSessionID, message, time.Since(started))
	log.Printf("[CLAUDE_MOD] input taken owner=%s tmux=%s delivery=%s", ownerSessionID, sessionName, delivery)
	return nil
}

func cleanupClaudeInteractiveSessionAfter(sessionName string, retention time.Duration) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if retention <= 0 {
				closeClaudeInteractiveSessionNow(sessionName)
				return
			}
			time.AfterFunc(retention, func() {
				closeClaudeInteractiveSessionNow(sessionName)
			})
		})
	}
}

func closeClaudeInteractiveSessionNow(sessionName string) {
	defer unregisterClaudeInteractiveSession(sessionName)
	killCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = killClaudeInteractiveSession(killCtx, sessionName)
}

func killClaudeInteractiveSession(ctx context.Context, sessionName string) error {
	if strings.TrimSpace(sessionName) == "" {
		return nil
	}
	defer removeClaudeModSessionDir(sessionName)
	defer unregisterClaudeInteractiveSession(sessionName)
	// Reap the pane process trees (CLI + spawned MCP node subprocesses) before
	// killing the session — kill-session only SIGHUPs the pane process, so the
	// children would otherwise orphan and leak.
	tmuxcontrol.ReapSessionProcessTree(ctx, sessionName)
	if err := runCommand(ctx, nil, "tmux", "kill-session", "-t", sessionName); err != nil {
		if isTmuxMissingSessionError(err) || isTmuxNoServerError(err) {
			return nil
		}
		return err
	}
	return nil
}

func claudeTmuxSessionExists(ctx context.Context, sessionName string) (bool, error) {
	if strings.TrimSpace(sessionName) == "" {
		return false, nil
	}
	if err := runCommand(ctx, nil, "tmux", "has-session", "-t", sessionName); err != nil {
		if isTmuxMissingSessionError(err) || isTmuxNoServerError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func claudeInteractiveSessionsFromTmuxList(out, prefix string) []string {
	var sessions []string
	for _, line := range strings.Split(out, "\n") {
		sessionName := strings.TrimSpace(line)
		if sessionName == "" {
			continue
		}
		if sessionName == prefix || strings.HasPrefix(sessionName, prefix+"-") {
			sessions = append(sessions, sessionName)
		}
	}
	return sessions
}

func isTmuxNoServerError(err error) bool {
	if err == nil {
		return false
	}
	if llmtypes.IsCodingAgentTmuxSessionLostError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no server running") ||
		strings.Contains(msg, "failed to connect to server")
}

func isTmuxMissingSessionError(err error) bool {
	if err == nil {
		return false
	}
	if llmtypes.IsCodingAgentTmuxSessionLostError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "can't find session") ||
		strings.Contains(msg, "can't find pane") ||
		strings.Contains(msg, "no current target")
}

func isClaudeTmuxSessionLostError(err error) bool {
	return isTmuxNoServerError(err) || isTmuxMissingSessionError(err)
}

func writeTempJSONConfig(pattern, value string) (string, error) {
	return writeTempFile(pattern, value)
}

func writeTempFile(pattern, value string) (string, error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	path := tmp.Name()
	if _, err := tmp.WriteString(value); err != nil {
		_ = tmp.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("failed to write temp file %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("failed to close temp file %s: %w", path, err)
	}
	return path, nil
}

func removeFiles(paths []string) {
	for _, path := range paths {
		if projectfile.IsLease(path) {
			projectfile.ReleaseToken(path)
			continue
		}
		_ = os.Remove(path)
	}
}

func runCommand(ctx context.Context, stdin io.Reader, name string, args ...string) error {
	_, err := runCommandOutput(ctx, stdin, name, args...)
	return err
}

func runCommandOutput(ctx context.Context, stdin io.Reader, name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func sanitizeTmuxSessionName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "mlp-claude-code"
	}

	var b strings.Builder
	for _, r := range name {
		if r == ':' || r == '.' || r == '/' || r == '\\' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			b.WriteByte('-')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

var (
	claudeStatusLineStreamMu sync.Mutex
	claudeStatusLineStreamed = make(map[string]string) // sessionName -> raw JSON content of last streamed statusline
)

// readClaudeStatuslineWithWait reads the statusline temp file, polling until it
// exists and is non-empty. Claude writes it asynchronously when its TUI renders
// the statusLine command, which can lag a freshly-returned turn — so a one-shot
// read races the render. Bounded by the smaller of ~15s and the context
// deadline; returns the last read error if the file never materializes.
func readClaudeStatuslineWithWait(ctx context.Context, outputPath string) ([]byte, error) {
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	var lastErr error
	for {
		raw, err := os.ReadFile(outputPath)
		if err == nil && len(bytes.TrimSpace(raw)) > 0 {
			return raw, nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("statusline payload at %s is empty", outputPath)
		}
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func claudeStatuslinePath(sessionName string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("claude_statusline_%s.json", sessionName))
}

func (c *ClaudeCodeInteractiveAdapter) prepareStatusLineSettings(opts *llmtypes.CallOptions, sessionName string) (string, []string, error) {
	// The statusline this configures is where the account's remaining quota
	// arrives. Pair the session with its credential now, while the credential is
	// in scope, so later readers can attribute what they see to an account
	// (PLAT-101).
	rememberClaudeSessionAccount(sessionName, c.oauthToken)
	outputPath := claudeStatuslinePath(sessionName)
	// Create helper script that cat's stdin to outputPath
	scriptContent := fmt.Sprintf("#!/bin/sh\ncat > %s\n", outputPath)
	scriptPath, err := writeTempFile("claude-statusline-helper-*.sh", scriptContent)
	if err != nil {
		return "", nil, err
	}
	_ = os.Chmod(scriptPath, 0o755)

	var tempFiles []string
	tempFiles = append(tempFiles, scriptPath)

	settingsMap := make(map[string]interface{})
	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		if settings, ok := opts.Metadata.Custom[MetadataKeySettings].(string); ok && strings.TrimSpace(settings) != "" {
			settings = strings.TrimSpace(settings)
			if strings.HasPrefix(settings, "{") {
				_ = json.Unmarshal([]byte(settings), &settingsMap)
			} else {
				if raw, err := os.ReadFile(settings); err == nil {
					_ = json.Unmarshal(raw, &settingsMap)
				}
			}
		}
	}

	if opts != nil && opts.Metadata != nil && opts.Metadata.Custom != nil {
		mcpCfg, _ := opts.Metadata.Custom[MetadataKeyMCPConfig].(string)
		if _, err := claudeAddShellRedirectHook(settingsMap, mcpCfg); err != nil {
			removeFiles(tempFiles)
			return "", nil, err
		}
	}

	settingsMap["statusLine"] = map[string]interface{}{
		"type":    "command",
		"command": "sh " + scriptPath,
		"padding": 0,
	}

	settingsPath, err := writeClaudeSkillScopedSettings(opts, settingsMap)
	if err != nil {
		removeFiles(tempFiles)
		return "", nil, err
	}

	tempFiles = append(tempFiles, settingsPath)
	return settingsPath, tempFiles, nil
}

func streamClaudeStatusLine(ctx context.Context, sessionName string, streamChan chan<- llmtypes.StreamChunk) bool {
	if streamChan == nil {
		return false
	}
	outputPath := claudeStatuslinePath(sessionName)
	raw, err := os.ReadFile(outputPath)
	if err != nil {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return false
	}

	// Deduplicate streams
	claudeStatusLineStreamMu.Lock()
	last := claudeStatusLineStreamed[sessionName]
	if last == trimmed {
		claudeStatusLineStreamMu.Unlock()
		return false
	}
	claudeStatusLineStreamed[sessionName] = trimmed
	claudeStatusLineStreamMu.Unlock()

	// Pass no default model: the real model comes from the statusLine JSON
	// (model.display_name). Fabricating "claude-3-5-sonnet" here would show a
	// wrong model whenever a different Claude model is in use.
	status, err := parseClaudeStatusLineJSON(raw, "")
	if err != nil {
		return false
	}
	// Tag the owning tmux session so downstream consumers can attribute this
	// telemetry to the exact coding-agent pane (a session may host several).
	if status.Metadata == nil {
		status.Metadata = map[string]interface{}{}
	}
	status.Metadata["tmux_session"] = sessionName

	// Publish the quota reading to the account this session runs under. Every
	// session on that subscription draws from the same pool, so one session's
	// observation is every session's best available answer (PLAT-101).
	recordClaudeSessionRateLimitWindows(sessionName, status.RateLimitWindows(), time.Now().UTC())

	select {
	case streamChan <- llmtypes.StreamChunk{
		Type:       llmtypes.StreamChunkTypeStatusLine,
		StatusLine: status,
	}:
		return true
	default:
		return false
	}
}

func parseClaudeStatusLineJSON(raw []byte, defaultModel string) (*llmtypes.StatusLine, error) {
	// Parse with standard mapping, supporting both camelCase and snake_case or customized Claude Code output
	var payload struct {
		InputTokens              int `json:"input_tokens"`
		InputTokensCamel         int `json:"inputTokens"`
		OutputTokens             int `json:"output_tokens"`
		OutputTokensCamel        int `json:"outputTokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheCreationInputCamel  int `json:"cacheCreationInputTokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheReadInputCamel      int `json:"cacheReadInputTokens"`
		TotalInputTokens         int `json:"total_input_tokens"`
		TotalInputCamel          int `json:"totalInputTokens"`
		TotalOutputTokens        int `json:"total_output_tokens"`
		TotalOutputCamel         int `json:"totalOutputTokens"`
	}

	if err := json.Unmarshal(bytes.TrimSpace(raw), &payload); err != nil {
		return nil, err
	}

	status := &llmtypes.StatusLine{
		Provider: "claudecode",
		Model:    defaultModel,
	}

	// Resolve the token counts
	if payload.InputTokens > 0 {
		status.InputTokens = payload.InputTokens
	} else {
		status.InputTokens = payload.InputTokensCamel
	}

	if payload.OutputTokens > 0 {
		status.OutputTokens = payload.OutputTokens
	} else {
		status.OutputTokens = payload.OutputTokensCamel
	}

	if payload.CacheCreationInputTokens > 0 {
		status.CacheCreationInputTokens = payload.CacheCreationInputTokens
	} else {
		status.CacheCreationInputTokens = payload.CacheCreationInputCamel
	}

	if payload.CacheReadInputTokens > 0 {
		status.CacheReadInputTokens = payload.CacheReadInputTokens
	} else {
		status.CacheReadInputTokens = payload.CacheReadInputCamel
	}

	if payload.TotalInputTokens > 0 {
		status.TotalInputTokens = payload.TotalInputTokens
	} else {
		status.TotalInputTokens = payload.TotalInputCamel
	}

	if payload.TotalOutputTokens > 0 {
		status.TotalOutputTokens = payload.TotalOutputTokens
	} else {
		status.TotalOutputTokens = payload.TotalOutputCamel
	}

	// Map raw payload to metadata
	var rawMap map[string]interface{}
	if err := json.Unmarshal(raw, &rawMap); err == nil {
		status.Metadata = rawMap

		// Claude Code's native statusLine input carries the real model and cost,
		// which the token-focused struct above ignores. Extract them defensively
		// from the raw map so we surface the actual model (not a hardcoded
		// default) and the cost — without breaking the custom token format that
		// has neither. model may be an object ({id, display_name}) or a string.
		switch m := rawMap["model"].(type) {
		case map[string]interface{}:
			if dn, _ := m["display_name"].(string); strings.TrimSpace(dn) != "" {
				status.Model = dn
			} else if id, _ := m["id"].(string); strings.TrimSpace(id) != "" {
				status.Model = id
			}
		case string:
			if strings.TrimSpace(m) != "" {
				status.Model = m
			}
		}
		// Native Claude Code statusLine payloads keep usage under
		// context_window rather than the legacy top-level token fields.
		if contextWindow, ok := rawMap["context_window"].(map[string]interface{}); ok {
			if n := claudeIntFromAny(contextWindow["total_input_tokens"]); n > 0 {
				status.TotalInputTokens = n
			}
			if n := claudeIntFromAny(contextWindow["total_output_tokens"]); n > 0 {
				status.TotalOutputTokens = n
			}
			if current, ok := contextWindow["current_usage"].(map[string]interface{}); ok {
				if n := claudeIntFromAny(current["input_tokens"]); n > 0 {
					status.InputTokens = n
				}
				if n := claudeIntFromAny(current["output_tokens"]); n > 0 {
					status.OutputTokens = n
				}
				if n := claudeIntFromAny(current["cache_creation_input_tokens"]); n > 0 {
					status.CacheCreationInputTokens = n
				}
				if n := claudeIntFromAny(current["cache_read_input_tokens"]); n > 0 {
					status.CacheReadInputTokens = n
				}
				// The prompt of the latest API call is what occupies the window.
				used := claudeIntFromAny(current["input_tokens"]) + claudeIntFromAny(current["cache_creation_input_tokens"]) + claudeIntFromAny(current["cache_read_input_tokens"])
				status.SetContextUsage(used, claudeIntFromAny(contextWindow["context_window_size"]))
			}
		}
		switch cost := rawMap["cost"].(type) {
		case map[string]interface{}:
			if c := claudeFloatFromAny(cost["total_cost_usd"]); c > 0 {
				status.CostUSD = c
			}
		default:
			if c := claudeFloatFromAny(rawMap["total_cost_usd"]); c > 0 {
				status.CostUSD = c
			}
		}

		// Expose plan rate-limit usage, context fill, and effort as generic
		// statusline extras so UIs can render them next to cost without knowing
		// Claude Code's schema.
		status.SetStatusExtras(claudeStatusExtras(rawMap))
		// The same windows, unformatted. Extras are display text; a runtime
		// deciding whether to suspend a workflow and when to resume it needs
		// the actual reset instant, which formatting destroys (PLAT-101).
		status.SetRateLimitWindows(claudeRateLimitWindows(rawMap))
	}

	// "claude-code"/"claudecode" are placeholder ids equal to the provider name;
	// emitting them as Model renders a duplicate "claudecode · claude-code".
	if status.Model == "claude-code" || status.Model == "claudecode" {
		status.Model = ""
	}

	return status, nil
}

// claudeFloatFromAny coerces a JSON-decoded number (float64) — or a numeric
// string — into a float64, returning 0 when the value isn't numeric.
func claudeFloatFromAny(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func claudeIntFromAny(v interface{}) int {
	return int(claudeFloatFromAny(v))
}

// claudeStatusExtras turns Claude Code's native statusLine fields into
// display-ready segments, in footer order: plan rate-limit usage ("5h N%",
// "7d N%"), context-window fill ("ctx N%"), and reasoning effort ("xhigh").
// Every field is optional — rate_limits only appears for Pro/Max after the first
// API response, each window may be independently absent, and effort/context may
// be missing — so a missing field simply yields no segment. Schema:
//
//	"rate_limits":    {"five_hour": {"used_percentage": 24.0}, "seven_day": {"used_percentage": 41.0}}
//	"context_window": {"used_percentage": 4}
//	"effort":         {"level": "xhigh"}
//
// claudeRateLimitWindows extracts the same rate-limit windows claudeStatusExtras
// renders, but as structured values with the reset instant intact.
//
// Both readers exist on purpose: one produces text for a UI, this one produces
// state for the runtime. Keeping them separate means a change to how a window
// is displayed cannot alter what the runtime decides, and vice versa.
//
// A window with no usable reset time is still returned (with a zero ResetsAt)
// when it is exhausted, because "exhausted, reset time unknown" is a real and
// materially different state from "not exhausted" — PLAT-101 requires the
// caller to distinguish them rather than invent a timestamp.
func claudeRateLimitWindows(rawMap map[string]interface{}) []llmtypes.RateLimitWindow {
	rl, ok := rawMap["rate_limits"].(map[string]interface{})
	if !ok {
		return nil
	}
	var windows []llmtypes.RateLimitWindow
	for _, name := range []string{"five_hour", "seven_day"} {
		win, ok := rl[name].(map[string]interface{})
		if !ok {
			continue
		}
		if _, present := win["used_percentage"]; !present {
			continue
		}
		window := llmtypes.RateLimitWindow{
			Name:        name,
			UsedPercent: claudeFloatFromAny(win["used_percentage"]),
		}
		if epoch := int64(claudeFloatFromAny(win["resets_at"])); epoch > 0 {
			window.ResetsAt = time.Unix(epoch, 0).UTC()
		}
		windows = append(windows, window)
	}
	return windows
}

func claudeStatusExtras(rawMap map[string]interface{}) []string {
	var extras []string
	if rl, ok := rawMap["rate_limits"].(map[string]interface{}); ok {
		for _, w := range []struct{ key, label string }{
			{"five_hour", "5h"},
			{"seven_day", "7d"},
		} {
			win, ok := rl[w.key].(map[string]interface{})
			if !ok {
				continue
			}
			if _, present := win["used_percentage"]; !present {
				continue
			}
			extras = append(extras, llmtypes.FormatUsageExtraWithReset(
				w.label,
				claudeFloatFromAny(win["used_percentage"]),
				int64(claudeFloatFromAny(win["resets_at"])),
				time.Now(),
			))
		}
	}
	if cw, ok := rawMap["context_window"].(map[string]interface{}); ok {
		if _, present := cw["used_percentage"]; present {
			extras = append(extras, llmtypes.FormatUsageExtra("ctx", claudeFloatFromAny(cw["used_percentage"])))
		}
	}
	if eff, ok := rawMap["effort"].(map[string]interface{}); ok {
		if level, _ := eff["level"].(string); strings.TrimSpace(level) != "" {
			extras = append(extras, level)
		}
	}
	return extras
}

// GetStatusLine retrieves a snapshot of the current statusline for the active session.
// Satisfies the llmtypes.StatusLineProvider interface.
func (c *ClaudeCodeInteractiveAdapter) GetStatusLine(ctx context.Context, sessionID string) (*llmtypes.StatusLine, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}

	// Find the session in the registry or use fallback
	var tmuxSessionName string
	if sess, ok := claudeInteractivePersistentRegistry.Get(sessionID); ok && sess != nil {
		tmuxSessionName = sess.tmuxSessionName
	} else {
		_, sess, _ := claudeInteractivePersistentRegistry.Find(func(sess *claudeInteractivePersistentSession) bool {
			return sess != nil && sess.tmuxSessionName == sessionID
		})
		if sess != nil {
			tmuxSessionName = sess.tmuxSessionName
		}
	}

	if tmuxSessionName == "" {
		tmuxSessionName = sessionID
	}

	outputPath := claudeStatuslinePath(tmuxSessionName)
	raw, err := readClaudeStatuslineWithWait(ctx, outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read statusline payload: %w", err)
	}

	// Use the adapter's configured model as the fallback; the statusLine JSON's
	// model (when present) overrides it inside parseClaudeStatusLineJSON, and the
	// "claude-code" placeholder is stripped there.
	status, err := parseClaudeStatusLineJSON(raw, c.modelID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse statusline payload: %w", err)
	}
	recordClaudeSessionRateLimitWindows(tmuxSessionName, status.RateLimitWindows(), time.Now().UTC())

	return status, nil
}

// claudeTmuxPaneExited reports whether the session's pane process has exited,
// with its exit status, as tmux records it (remain-on-exit keeps the pane).
func claudeTmuxPaneExited(ctx context.Context, sessionName string) (string, bool) {
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "-t", sessionName, "#{pane_dead} #{pane_dead_status}").Output()
	if err != nil {
		return "", false
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 || fields[0] != "1" {
		return "", false
	}
	if len(fields) > 1 {
		return fields[1], true
	}
	return "unknown", true
}
