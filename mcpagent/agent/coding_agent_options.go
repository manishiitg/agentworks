package mcpagent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

var codingAgentPersistentInteractiveEnabledByProvider = map[llm.Provider]func(*Agent) bool{
	llm.ProviderClaudeCode: func(a *Agent) bool { return a.claudeCodePersistentInteractiveSession },
	llm.ProviderCodexCLI:   func(a *Agent) bool { return a.codexPersistentInteractiveSession },
	llm.ProviderCursorCLI:  func(a *Agent) bool { return a.cursorPersistentInteractiveSession },
	llm.ProviderPiCLI:      func(a *Agent) bool { return a.piPersistentInteractiveSession },
	llm.ProviderMuseCLI:    func(a *Agent) bool { return a.musePersistentInteractiveSession },
	llm.ProviderAgyCLI:     func(a *Agent) bool { return a.agyPersistentInteractiveSession },
}

func (a *Agent) appendCodingAgentInteractiveOptions(opts []llmtypes.CallOption) []llmtypes.CallOption {
	return a.appendCodingAgentInteractiveOptionsForProvider(opts, a.provider, a.modelID)
}

func (a *Agent) appendCodingAgentInteractiveOptionsForProvider(opts []llmtypes.CallOption, provider llm.Provider, modelID string) []llmtypes.CallOption {
	opts = a.appendCodingAgentWorkingDirOptionForProvider(opts, provider, modelID)
	opts = a.appendCLISecurityPolicyOption(opts, provider)
	if contract, ok := llm.GetCodingAgentProviderContract(provider, modelID); ok && contract.SupportsTerminalStream && (a.enableStreaming || a.streamingCallback != nil) {
		switch provider {
		case llm.ProviderCodexCLI:
			opts = append(opts, llm.WithCodexStreamTranscript(true), llm.WithCodexStreamTmuxScreen(true))
		case llm.ProviderCursorCLI:
			opts = append(opts, cursorcli.WithStreamTranscript(true), cursorcli.WithStreamTmuxScreen(true))
		}
	}

	sessionID := strings.TrimSpace(a.sessionID)
	if sessionID == "" || !codingAgentInteractiveEnabledForProvider(provider, modelID, sessionID) {
		return opts
	}
	// Structured transport is a one-shot process, not a live pane: there is no
	// interactive session to attach to, so skip the interactive-session-id
	// option. (Skipping it does NOT by itself select structured — the adapters
	// dispatch on their own per-provider structured metadata key, which each
	// provider integration sets from wantsStructuredTransport. Both must agree,
	// which is why they read the same resolver.)
	if a.wantsStructuredTransport() {
		return opts
	}

	if option := llm.CodingAgentInteractiveSessionOption(provider, sessionID); option != nil {
		opts = append(opts, option)
	} else {
		return opts
	}

	if provider == llm.ProviderCodexCLI {
		if strings.TrimSpace(a.codingAgentWorkingDir) == "" {
			if legacyDir := strings.TrimSpace(a.codexProjectDirID); legacyDir != "" {
				opts = append(opts, llm.WithCodexProjectDirID(legacyDir))
			}
		}
	}

	if provider == llm.ProviderCursorCLI {
		opts = append(opts, llm.WithCursorAutoApproveWebSearch())
		// CursorBridgeToolsMode intentionally does NOT set --mode ask. Cursor's
		// ask mode is a conversational stance that hard-refuses natural-language
		// write requests with "Switch to Agent mode", which makes the chat
		// unusable for any task that involves writes. Cursor runs in default
		// agent mode; MCP bridge config is still provided via .cursor/mcp.json
		// for tools the agent chooses to invoke through the bridge.
	}

	if a.codingAgentPersistentInteractiveEnabled(provider) {
		if option := llm.CodingAgentPersistentInteractiveOption(provider, true); option != nil {
			opts = append(opts, option)
		}
	}

	return opts
}

func codingAgentInteractiveEnabledForProvider(provider llm.Provider, modelID, sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" {
		return false
	}
	contract, ok := llm.GetCodingAgentProviderContract(provider, modelID)
	return ok && contract.RequiresOwnerSessionID
}

func (a *Agent) appendCodingAgentWorkingDirOptionForProvider(opts []llmtypes.CallOption, provider llm.Provider, modelID string) []llmtypes.CallOption {
	workingDir := strings.TrimSpace(a.codingAgentWorkingDir)
	// Linked steps work directly in their admitted output folder; generated
	// provider files stay in the existing private runtime. Splitting Landlock
	// grants around managed records otherwise prevents new run-root entries.
	if a.isolatedSessionWorkspace {
		if tmpDir := a.ensureIsolatedWorkspaceDir(); tmpDir != "" {
			workingDir = tmpDir
		}
		if a.codingAgentOutputDir != "" {
			workingDir = a.codingAgentOutputDir
		}
	}
	if workingDir == "" {
		return opts
	}
	if a.codingAgentRuntimeDir != "" {
		opts = append(opts, llmtypes.WithCLIRuntimeDir(a.codingAgentRuntimeDir))
	} else {
		cleanupInactiveCodingAgentProjectArtifacts(workingDir, provider)
	}
	option := llm.CodingAgentWorkingDirOption(provider, workingDir)
	if option == nil {
		return opts
	}
	opts = append(opts, option)

	// CLI providers that ALSO project the per-session system prompt into a
	// workspace instruction file (claude→CLAUDE.md, codex→AGENTS.md) would
	// otherwise inject the prompt
	// twice — once via the CLI flag/env/in-band channel and once via the
	// projected file — doubling the (often large) builder prompt. Carry it
	// through the projected file only; each adapter falls back to its normal
	// channel if the projection is disabled or its write fails. Cursor carries
	// the prompt through its rules file alone, and Pi uses explicit append-
	// system-prompt, so they need no opt-in here.
	if option := llm.CodingAgentProjectInstructionOnlyOption(provider, true); option != nil {
		opts = append(opts, option)
	}

	return opts
}

func (a *Agent) codingAgentArtifactDir() string {
	if a.codingAgentRuntimeDir != "" {
		return a.codingAgentRuntimeDir
	}
	return a.codingAgentWorkingDir
}

// isolatedWorkspaceDirForSession derives the isolated workspace path from the
// session ID. Deterministic on purpose: coding CLIs key their resumable
// conversation by working directory (claude stores it under
// ~/.claude/projects/<slugified-cwd>/, and codex/cursor do the equivalent), so
// the SAME session must land in the SAME dir on every turn or native --resume
// can never find the previous turn's conversation.
//
// Hashed rather than using the raw ID: session IDs contain '/' and other path
// metacharacters (e.g. "msgseq-iteration-0-job-search/step-4"), and the full
// path also ends up embedded in provider metadata dir names.
//
// Returns "" when there is no session ID to key on — the caller then falls back
// to a random dir, which is correct because a session with no stable identity
// could never be resumed anyway.
func isolatedWorkspaceDirForSession(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(os.TempDir(), "mlp-cli-session-"+hex.EncodeToString(sum[:8]))
}

// ensureIsolatedWorkspaceDir returns this session's isolated workspace dir,
// resolving it once per Agent via sync.Once.
//
// The dir is derived from the session ID (see isolatedWorkspaceDirForSession)
// rather than freshly minted per Agent. A new Agent is constructed for EVERY
// turn, so a per-Agent random dir gave each turn a different cwd — which meant
// the coding CLI filed each turn's conversation under a different project key
// and native --resume failed with "No conversation found with session ID",
// leaking one orphaned conversation directory per turn. Isolation is unchanged:
// distinct sessions still get distinct dirs, so concurrent steps cannot collide
// and the user's real workspace is still never the CLI's cwd.
//
// Returns "" only if the directory cannot be created, in which case the caller
// falls back to CodingAgentWorkingDir to preserve session usability —
// isolation is belt-and-suspenders, not a hard contract.
func (a *Agent) ensureIsolatedWorkspaceDir() string {
	a.isolatedWorkspaceOnce.Do(func() {
		if dir := isolatedWorkspaceDirForSession(a.sessionID); dir != "" {
			// MkdirAll, not MkdirTemp: on the second and later turns of a
			// session this dir already exists and MUST be reused, not replaced.
			if err := os.MkdirAll(dir, 0o700); err == nil {
				a.isolatedWorkspacePath = dir
				a.isolatedWorkspaceStable = true
				if a.logger != nil {
					a.logger.Info("IsolatedSessionWorkspace: using session dir " + dir)
				}
				return
			} else if a.logger != nil {
				a.logger.Warn("IsolatedSessionWorkspace: MkdirAll failed for session dir; falling back to a random dir: " + err.Error())
			}
		}

		// No session ID (or the session dir could not be created): fall back to
		// the historical random dir. Not resumable, so Agent.Close still
		// rm -rf's this one — see the isolatedWorkspaceStable check there.
		dir, err := os.MkdirTemp("", "mlp-cli-session-*")
		if err != nil {
			if a.logger != nil {
				a.logger.Warn("IsolatedSessionWorkspace: os.MkdirTemp failed; falling back to CodingAgentWorkingDir")
			}
			return
		}
		a.isolatedWorkspacePath = dir
		if a.logger != nil {
			a.logger.Info("IsolatedSessionWorkspace: created tmp dir " + dir)
		}
	})
	return a.isolatedWorkspacePath
}

func extractCodingAgentSessionIDs(a *Agent, resp *llmtypes.ContentResponse) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].GenerationInfo == nil {
		return
	}
	a.updateCodingProviderSessionHandleFromResponse(resp)
	additional := resp.Choices[0].GenerationInfo.Additional
	if additional == nil {
		return
	}
	if session, ok := LookupSession(a.sessionID); ok {
		var roots []string
		switch values := additional["native_continuation_roots"].(type) {
		case []string:
			roots = values
		case []interface{}:
			for _, v := range values {
				if root, ok := v.(string); ok {
					roots = append(roots, root)
				}
			}
		}
		handle := a.currentAgentSessionHandle()
		if handle != nil {
			session.currentTurnLifecycle().setNativePublication(handle.Provider.NativeSessionID, roots)
		}
	}
	if sid, ok := additional["claude_code_session_id"].(string); ok && sid != "" {
		a.claudeCodeSessionID = sid
	}
	if sid, ok := additional["codex_thread_id"].(string); ok && sid != "" {
		if a.logger != nil && a.codexSessionID != sid {
			a.logger.Debug(fmt.Sprintf("CodexSessionID set from response: session=%q old=%q new=%q isolated=%v", a.sessionID, a.codexSessionID, sid, a.isolatedSessionWorkspace))
		}
		a.codexSessionID = sid
	}
	if sid, ok := additional["cursor_session_id"].(string); ok && sid != "" {
		a.cursorSessionID = sid
	}
	if sid, ok := additional["pi_session_id"].(string); ok && sid != "" {
		a.piSessionID = sid
	}
	if a.codingProviderSessionHandle.Empty() {
		a.codingProviderSessionHandle = a.legacyCodingProviderSessionHandle()
	}
}

func (a *Agent) buildStructuredResumeOptions() []llmtypes.CallOption {
	var opts []llmtypes.CallOption
	handle := a.legacyCodingProviderSessionHandle()
	if sessionID := strings.TrimSpace(handle.NativeSessionID); sessionID != "" {
		if option := llm.NativeResumeOption(a.provider, sessionID); option != nil {
			opts = append(opts, option)
		}
	}
	return opts
}

func (a *Agent) appendCursorCLIIntegrationOptions(opts []llmtypes.CallOption) ([]llmtypes.CallOption, error) {
	bridgeConfig, bridgeErr := a.buildBridgeMCPConfig()
	if bridgeErr != nil {
		return nil, fmt.Errorf("Cursor CLI requires the MCP bridge: %w", bridgeErr)
	}

	opts = append(opts, llm.WithCursorMCPConfig(bridgeConfig))
	// --approve-mcps auto-accepts cursor's "approve this MCP server?"
	// TUI dialog so the FIRST bridge tool call does not stall waiting
	// for a human to click through. Required whenever WithCursorMCPConfig
	// is set in a headless context.
	opts = append(opts, llm.WithCursorApproveMCPs())
	if a.bridgeReadyFile != "" {
		// Hold a cold cursor session's first prompt until the bridge reports the
		// tools connected (tools/list answered) — closes the cold-turn race where
		// cursor's first bridge call fails and it falls back to its Shell tool.
		opts = append(opts, llm.WithMCPReadyFile(a.bridgeReadyFile))
	}
	// WithCursorDenyBuiltinTools installs a per-session .cursor/hooks.json
	// that denies Cursor's built-in Shell/Read/Edit/Write/Delete/etc. tools at
	// the hook layer, forcing the agent to route tool calls through the bridge.
	// Full CLI ("Native agent tools") uses the same hooks but lets Cursor's own
	// shell, reads, edits and deletes run (shell approved by the hook, never
	// --force); subagents, cloud/background agents and computer use stay
	// denied (TestCursorCLIRealFullNativeP0).
	if a.nativeCodingToolsEnabled() {
		opts = append(opts, llm.WithCursorFullNativeTools())
	} else {
		opts = append(opts, llm.WithCursorDenyBuiltinTools(true))
	}
	if a.logger != nil {
		a.logger.Info("🌉 [CURSOR_CLI] Configured MCP bridge through .cursor/mcp.json with deny-builtin hooks")
		a.logger.Info("⏱️ [CURSOR_CLI] No supported MCP-client timeout control; request cancellation and the mcpbridge HTTP backstop remain authoritative")
		a.logger.Info("🌉 Using the selected Cursor transport with the scoped MCP bridge and CLI permissions")
	}

	return opts, nil
}

// museWaitsForUserChoice reports whether a Muse native question should wait
// for the user's answer instead of being auto-answered. That needs both a
// retained pane to answer in and a person attending the chat. Persistence alone
// is not enough: scheduled runs keep the native session alive too, and there a
// question must be auto-answered or the run waits forever (PLAT-354).
func (a *Agent) museWaitsForUserChoice() bool {
	return a.musePersistentInteractiveSession && a.userAnswersNativeQuestions && !a.wantsStructuredTransport()
}

func (a *Agent) appendMuseCLIIntegrationOptions(opts []llmtypes.CallOption) ([]llmtypes.CallOption, error) {
	bridgeConfig, bridgeErr := a.buildBridgeMCPConfig()
	if bridgeErr != nil {
		return nil, fmt.Errorf("Muse CLI requires the MCP bridge: %w", bridgeErr)
	}

	// Mount the bridge in a private config root; concurrent sessions must not
	// overwrite one another's settings or retain a stale bridge endpoint.
	opts = append(opts, llm.WithMuseMCPConfig(bridgeConfig))
	// mcp_only: Muse's PreToolUse policy allows native web search only, and
	// the provider adds --disable-shell/--disable-write. Full CLI ("Native
	// agent tools"): no allowlist at all, so Muse keeps its whole native
	// toolset (files, shell, subagents) inside AgentWorks' confinement. MCP
	// discovery and concrete MCP server tools mount separately in both modes.
	if !a.nativeCodingToolsEnabled() {
		opts = append(opts, llm.WithMuseToolAllowlist([]string{"web_search"}))
	}
	if a.museWaitsForUserChoice() {
		opts = append(opts, musecli.WithUserChoice(true))
	}
	if a.bridgeReadyFile != "" {
		// Hold a cold session's first prompt until the bridge reports the
		// tools connected — same cold-turn race as cursor (an unreachable
		// bridge fails the whole muse run at startup init).
		opts = append(opts, llm.WithMCPReadyFile(a.bridgeReadyFile))
	}
	if sessionID := strings.TrimSpace(a.museSessionID); sessionID != "" {
		if option := llm.NativeResumeOption(llm.ProviderMuseCLI, sessionID); option != nil {
			opts = append(opts, option)
		}
	}
	if a.logger != nil {
		a.logger.Info("🌉 [MUSE_CLI] Configured best-effort native-tool restrictions with web search and MCP; internal session-control gaps remain")
	}
	if a.wantsStructuredTransport() {
		opts = append(opts, llm.WithMuseStructuredTransport(true))
	} else if a.enableStreaming {
		opts = append(opts, llm.WithMuseStreamTranscript(true))
		opts = append(opts, llm.WithMuseStreamTmuxScreen(true))
	}
	// See appendClaudeCodeIntegrationOptions' matching comment (coding_agent_
	// integrations.go): content streaming needs this separate, explicit
	// opt-in beyond EnableStreaming.
	if a.streamingCallback != nil {
		opts = append(opts, llm.WithMuseStreamTranscript(true))
		opts = append(opts, llm.WithMuseStreamTmuxScreen(true))
	}
	return opts, nil
}

func (a *Agent) appendAgyCLIIntegrationOptions(opts []llmtypes.CallOption) ([]llmtypes.CallOption, error) {
	bridgeConfig, bridgeErr := a.buildBridgeMCPConfig()
	if bridgeErr != nil {
		return nil, fmt.Errorf("Agy CLI requires the MCP bridge: %w", bridgeErr)
	}

	// Each launch owns a private MCP catalogue. The workspace PreToolUse
	// hook applies the chosen native tool mode in both exec and retained lanes.
	opts = append(opts, llm.WithAgyMCPConfig(bridgeConfig))
	toolMode := a.agyNativeToolsMode()
	opts = append(opts, llm.WithAgyNativeToolsMode(toolMode))
	if a.bridgeReadyFile != "" {
		// Hold a cold session's first prompt until the bridge reports the
		// tools connected — same cold-turn race as cursor/muse.
		opts = append(opts, llm.WithMCPReadyFile(a.bridgeReadyFile))
	}
	if sessionID := strings.TrimSpace(a.agySessionID); sessionID != "" {
		if option := llm.NativeResumeOption(llm.ProviderAgyCLI, sessionID); option != nil {
			opts = append(opts, option)
		}
	}
	if a.logger != nil {
		a.logger.Info(fmt.Sprintf("🌉 [AGY_CLI] Configured private MCP bridge and native tool mode: %s", toolMode))
	}
	// No structured-transport option: the exec lane is agy's default, and the
	// persistent-interactive option (attached by the shared interactive path,
	// like every tmux provider) selects the TUI-sidecar lane instead. Both
	// lanes emit one content chunk per turn; the sidecar lane additionally
	// synthesizes post-hoc tool events from the conversation .db.
	return opts, nil
}

func codingAgentWorkingDirOptionForProvider(provider llm.Provider, modelID string) (func(string) llmtypes.CallOption, bool) {
	if !llm.IsCodingAgentProvider(provider, modelID) {
		return nil, false
	}
	return func(dir string) llmtypes.CallOption {
		return llm.CodingAgentWorkingDirOption(provider, dir)
	}, true
}

func (a *Agent) codingAgentPersistentInteractiveEnabled(provider llm.Provider) bool {
	if a == nil {
		return false
	}
	if fn, ok := codingAgentPersistentInteractiveEnabledByProvider[provider]; ok {
		return fn(a)
	}
	return false
}
