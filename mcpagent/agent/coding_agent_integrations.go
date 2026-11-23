package mcpagent

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingtimeout"
)

type codingAgentIntegrationAppender func(*Agent, []llmtypes.CallOption, LLMModel) ([]llmtypes.CallOption, error)

// applyCodingAgentIntegrationOptions runs the provider's MCP bridge
// integration binding. Non-coding providers and providers without bridge
// config pass through untouched. A coding provider whose contract
// requires bridge config but has no registered binding fails here —
// before launch — instead of silently starting without its
// orchestration context.
func applyCodingAgentIntegrationOptions(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	provider := llmproviders.Provider(model.Provider)
	appender, ok := codingAgentIntegrationAppenders[provider]
	if !ok {
		if contract, cok := llm.GetCodingAgentProviderContract(provider, model.ModelID); cok && contract.RequiresMCPBridgeConfig {
			return opts, fmt.Errorf("coding agent provider %s requires an MCP bridge integration binding: none registered", provider)
		}
		return opts, nil
	}
	if err := a.prepareIsolatedOutputLink(); err != nil {
		return opts, err
	}
	return appender(a, opts, model)
}

var codingAgentIntegrationAppenders = map[llmproviders.Provider]codingAgentIntegrationAppender{
	llmproviders.ProviderClaudeCode: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendClaudeCodeIntegrationOptions(opts, model)
	},
	llmproviders.ProviderCodexCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendCodexCLIIntegrationOptions(opts, model)
	},
	llmproviders.ProviderCursorCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		if effort, ok := model.Options["reasoning_effort"].(string); ok && strings.TrimSpace(effort) != "" {
			opts = append(opts, llmtypes.WithReasoningEffort(effort))
		}
		return a.appendCursorCLIIntegrationOptions(opts)
	},
	llmproviders.ProviderMuseCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendMuseCLIIntegrationOptions(opts)
	},
	llmproviders.ProviderAgyCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendAgyCLIIntegrationOptions(opts)
	},
	llmproviders.ProviderPiCLI: func(a *Agent, opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
		return a.appendPiCLIIntegrationOptionsForModel(opts, model)
	},
}

const (
	codingAgentToolsMCPOnly = "mcp_only"
	// codingAgentToolsFull is the CLI's own toolset (reads, shell, edits,
	// subagents) alongside the bridge (PLAT-364 Full CLI). It takes effect only
	// when the CLI is confined or explicitly admitted as personal Windows.
	// Other unconfined requests run as mcp_only.
	codingAgentToolsFull     = "full"
	codingAgentApprovalsAuto = "provider_auto"
	codingAgentApprovalsAll  = "approve_all"
)

// claudeFullCLINativeTools are the Claude Code built-ins in Full CLI mode:
// reads, search, skills, subagents, todos, web, and its own shell and file
// edits. Agent subagents inherit this --tools list. TaskCreate/Get/Update/List
// are the current todo tools; TodoWrite is the legacy name, ignored where
// unknown.
const claudeFullCLINativeTools = "WebSearch,WebFetch,Read,Grep,Glob,Skill,Agent,TaskCreate,TaskGet,TaskUpdate,TaskList,TodoWrite,Bash,Write,Edit,MultiEdit,NotebookEdit"

// fullCLIEnabled reports Full CLI: the mode asks for it AND the CLI starts
// confined, or trusted app code explicitly admits personal Windows execution.
// A missing policy never authorizes native tools.
func (a *Agent) fullCLIEnabled() bool {
	if !strings.EqualFold(strings.TrimSpace(a.codingAgentToolsMode), codingAgentToolsFull) {
		return false
	}
	policy := a.cliSecurityPolicy
	// PLAT-856-508: trusted app code admits personal, local Windows execution.
	// This capability never claims confinement and cannot enable a Unix host.
	personalWindows := runtime.GOOS == "windows" && os.Getenv("LOCAL_MODE") == "true" && os.Getenv("MULTI_USER_MODE") != "true" && policy != nil &&
		llmtypes.NormalizeCLISecurityMode(policy.Mode) == llmtypes.CLISecurityModeCompatibility &&
		slices.Contains(policy.ApprovedCapabilities, "personal_windows")
	return policy.Confined() || personalWindows
}

func (a *Agent) agyNativeToolsMode() string {
	if a.fullCLIEnabled() {
		return codingAgentToolsFull
	}
	return codingAgentToolsMCPOnly
}

// nativeCodingToolsEnabled reports whether this CLI runs with its own tools:
// only in Full CLI that actually applies. There is no
// reads-only middle state: anything else is
// mcp_only. Pi stays bridge-only until it has a confined full mode.
func (a *Agent) nativeCodingToolsEnabled() bool {
	return a.provider != llmproviders.ProviderPiCLI && a.fullCLIEnabled()
}

func (a *Agent) appendClaudeCodeIntegrationOptions(opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	claudeHTTPHooksEnabled := claudeHTTPRoutingHooksEnabled()
	contract, _ := llm.GetCodingAgentProviderContractForTransport(llm.ProviderClaudeCode, model.ModelID, a.claudeCodeTransport)
	sdk := string(contract.Transport) == "sdk"

	// Use restricted permissions instead of skipping them entirely. Allow our
	// bridge tools and WebSearch to run without prompts. In enforced mode the
	// wildcard is replaced with an explicit allowlist DERIVED from the actual
	// registered bridge tool set (core + withAdditionalBridgeTools) — not a
	// hardcoded 4-tool literal, which silently rejected any additional tool a
	// caller had registered.
	nativeTools := "WebSearch"
	if a.nativeCodingToolsEnabled() {
		nativeTools = claudeFullCLINativeTools
	}
	bridgeConfig, err := a.buildBridgeMCPConfig()
	if err != nil {
		return nil, fmt.Errorf("Claude Code requires the MCP bridge: %w", err)
	}
	// Native questions need the answer hook (python3, a session-scoped bridge). When it cannot be
	// prepared the turn runs without AskUserQuestion; failing the turn would be worse for the person
	// than the agent asking in plain text.
	nativeQuestions := a.claudeNativeQuestionsEnabled()
	a.logger.Info("Claude native questions", loggerv2.Any("enabled", nativeQuestions), loggerv2.String("conditions", a.claudeNativeQuestionsReason()))
	var questionHook claudeNativeQuestionHookFile
	if nativeQuestions && !sdk {
		var hookErr error
		questionHook, hookErr = prepareClaudeNativeQuestionHook(bridgeConfig)
		if hookErr != nil {
			a.logger.Warn("Claude native questions unavailable for this turn", loggerv2.Error(hookErr))
			nativeQuestions = false
		}
	}
	if nativeQuestions {
		nativeTools += ",AskUserQuestion"
	}
	allowedTools := "mcp__api-bridge__*," + nativeTools
	if claudeHTTPHooksEnabled {
		allowedTools = strings.Join(claudeBridgeAllowedToolIdentifiers(a.additionalBridgeTools, a.admitsBridgeTool), ",") + "," + nativeTools
	}
	opts = append(opts, llm.WithAllowedTools(allowedTools))

	if a.nativeCodingToolsEnabled() {
		// Full CLI: Claude's own reads, shell, edits, skills, todos, subagents
		// and web tools run natively. The sandbox is the boundary, so Claude
		// must not stop the turn to ask about a command or a read (live on RTS
		// 2026-09-29, a Bash read outside the folder stalled the turn on a
		// prompt).
		opts = append(opts, llm.WithClaudeCodeTools(nativeTools))
		opts = append(opts, llmproviders.WithDangerouslySkipPermissions())
	} else {
		// Force Claude to use our custom tools by disabling its own internal ones.
		opts = append(opts, llm.WithClaudeCodeTools(nativeTools))
	}

	settingsJSON := ""
	if claudeHTTPHooksEnabled {
		hookPath, hookErr := writeClaudeHTTPRoutingHook(a.additionalBridgeTools, a.admitsBridgeTool, strings.Split(nativeTools, ","))
		if hookErr != nil {
			a.logger.Warn("Failed to write Claude Code HTTP routing hook", loggerv2.Error(hookErr))
		} else {
			var settingsErr error
			settingsJSON, settingsErr = buildClaudeHTTPRoutingSettings(hookPath)
			if settingsErr != nil {
				a.logger.Warn("Failed to build Claude Code hook settings", loggerv2.Error(settingsErr))
			} else {
				a.logger.Info("🪝 Claude Code HTTP tool routing enforcement enabled",
					loggerv2.String("env", "MCPAGENT_CLAUDE_ENFORCE_HTTP_TOOL_ROUTING"),
					loggerv2.String("hook_path", hookPath))
			}
		}
	}

	if nativeQuestions && !sdk {
		settingsJSON, err = addClaudeNativeQuestionHook(settingsJSON, questionHook)
		if err != nil {
			return nil, fmt.Errorf("connect Claude native questions: %w", err)
		}
	}
	if settingsJSON != "" {
		opts = append(opts, llm.WithClaudeCodeSettings(settingsJSON))
	}
	opts = append(opts, llm.WithMCPConfig(bridgeConfig))
	if a.bridgeReadyFile != "" {
		// Hold the cold session's first prompt until the bridge reports the tools
		// are connected (tools/list answered), so the model never opens with no
		// tools. BuildBridgeMCPConfig set this path just above.
		opts = append(opts, llm.WithMCPReadyFile(a.bridgeReadyFile))
	}
	a.logger.Info("🌉 Using MCP bridge for Claude Code tool access via HTTP API")

	if a.maxTurns > 0 {
		opts = append(opts, llm.WithMaxTurns(a.maxTurns))
	}
	if a.claudeCodeSessionID != "" {
		opts = append(opts, llm.WithResumeSessionID(a.claudeCodeSessionID))
	}
	if a.wantsStructuredTransport() {
		opts = append(opts, llm.WithClaudeStructuredTransport(true))
	} else if a.enableStreaming && !sdk {
		opts = append(opts, llmproviders.WithClaudeStreamTranscript(true))
		// Transcript events power formatted chat, while terminal snapshots power
		// raw/terminal consumers. They are independent projections of the same
		// retained session and must remain available together.
		opts = append(opts, llm.WithClaudeStreamTmuxScreen(true))
	}
	if model.Options != nil {
		if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" {
			opts = append(opts, llm.WithClaudeCodeEffort(effort))
			a.logger.Info(fmt.Sprintf("🧠 [CLAUDE_CODE] Effort level set to: %s", effort))
		}
	}
	// Mid-turn assistant TEXT only streams when the adapter is explicitly told
	// to tail its transcript for content (separate from EnableStreaming, which
	// is auto-enabled above purely for tool-call observability and does NOT
	// imply this). Only worth the tailing cost when a caller actually
	// registered a StreamingCallback to consume the content.
	if a.streamingCallback != nil && !sdk {
		opts = append(opts, llm.WithClaudeStreamTranscript(true))
		opts = append(opts, llm.WithClaudeStreamTmuxScreen(true))
	}
	return opts, nil
}

func (a *Agent) appendCodexCLIIntegrationOptions(opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	// Full CLI ("Native agent tools"): Codex's shell and subagents run in its
	// workspace-write sandbox, inside AgentWorks' own confinement
	// (TestCodexCLIRealNativeToolsP0). mcp_only keeps the shell disabled and
	// the sandbox read-only.
	native := a.nativeCodingToolsEnabled()
	if native {
		// Shell and subagents only; browser/computer use, apps, plugins,
		// hooks, image generation etc. stay disabled.
		opts = append(opts, llm.WithCodexNativeTools())
	} else {
		opts = append(opts, llm.WithCodexDisableShellTool())
	}
	// App-server chooses on-request only when this turn has an attending user;
	// unattended work stays never. Explicit caller policies remain authoritative.
	// Shell/exec containment: WithCodexDisableShellTool above turns OFF codex's
	// built-in shell_tool + the other native code-exec features (unified_exec,
	// tool_search, browser/computer use, …) via codex's first-class `--disable`
	// flags. Verified live against codex v0.145.0: with those disabled and only
	// the MCP bridge in the session, codex has NO native way to run a command and
	// is forced through the bridge (it reports it has no shell rather than
	// shelling out; TestStructuredTransportToolFailure{Recovery,GiveUp}/Codex now
	// route through the bridge with calls>=2). This CORRECTS a long-standing
	// belief — previously baked into this comment and the transport docs — that
	// codex's native `functions.exec` was "unremovable by any flag". That was
	// true of an older codex; it is not true of the current one. For SHELL, codex
	// is now containable to the bridge exactly like claude/cursor/pi.
	//
	// IMPORTANT caveat: containment only holds while the session exposes no OTHER
	// code-exec tool. Codex will use any available one (e.g. a node_repl-style MCP
	// that can child_process out) as an escape hatch, so the bridge-only guarantee
	// is "shell_tool disabled AND the MCP set is the bridge alone".
	//
	// Native WRITES are a separate axis: disabling shell_tool does not necessarily
	// disable every mutating native tool codex may expose (e.g. apply_patch), so
	// the sandbox mode below still governs whether codex can mutate the host
	// directly vs. having to route writes through the bridge.
	//
	// SANDBOX is always READ-ONLY (2026-09-24): native exec can read but can
	// never write or mutate the host, so every state change goes through the
	// MCP bridge (execute_shell_command runs in the executor process, not in
	// Codex's sandbox, so bridge writes still work). Read-only has no network;
	// product APIs are reached through the bridge too.
	// Native writes are never allowed (user decision 2026-09-24), and
	// disabling shell_tool does not remove every mutating Codex tool (e.g.
	// apply_patch), so Codex always runs in its read-only sandbox. Writes go
	// through the bridge, whose execute_shell_command runs outside it.
	sandboxMode := "read-only"
	// Full CLI: Codex may write inside its working directory; AgentWorks'
	// confinement (Landlock, Seatbelt) bounds everything else.
	if native {
		sandboxMode = "workspace-write"
		if !a.cliSecurityPolicy.Confined() {
			// fullCLIEnabled admits only the explicit personal Windows exception.
			sandboxMode = "danger-full-access"
		}
	}
	opts = append(opts, llm.WithCodexSandbox(sandboxMode))
	configOverrides := make([]string, 0, 2)
	if sandboxMode == "workspace-write" && a.codexNetworkAccess {
		configOverrides = append(configOverrides, "sandbox_workspace_write.network_access=true")
	}
	if len(configOverrides) > 0 {
		opts = append(opts, llm.WithCodexConfigOverrides(configOverrides))
	}
	if a.codexSessionID != "" {
		opts = append(opts, llm.WithCodexResumeSessionID(a.codexSessionID))
	}

	bridgeConfig, bridgeErr := a.buildBridgeMCPConfig()
	if bridgeErr != nil {
		return nil, fmt.Errorf("Codex CLI requires the MCP bridge: %w", bridgeErr)
	}
	var bridgeParsed map[string]interface{}
	if err := json.Unmarshal([]byte(bridgeConfig), &bridgeParsed); err != nil {
		return nil, fmt.Errorf("Codex CLI requires valid MCP bridge config: %w", err)
	}
	mcpServers, ok := bridgeParsed["mcpServers"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Codex CLI requires MCP bridge config with mcpServers")
	}
	apiBridge, ok := mcpServers["api-bridge"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Codex CLI requires MCP bridge config with api-bridge server")
	}
	mcpToolTimeout := codingtimeout.LongRunningMCPToolTimeout()
	apiBridge["tool_timeout_sec"] = int64(mcpToolTimeout / time.Second)
	apiBridge["default_tools_approval_mode"] = "approve"
	mcpServersJSON, err := json.Marshal(mcpServers)
	if err != nil {
		return nil, fmt.Errorf("Codex CLI requires serializable MCP bridge servers: %w", err)
	}
	opts = append(opts, llm.WithCodexMCPServers(string(mcpServersJSON)))
	if a.bridgeReadyFile != "" {
		// Hold a cold codex session's first prompt until the bridge reports the
		// tools connected (tools/list answered) — see BuildBridgeMCPConfig.
		opts = append(opts, llm.WithMCPReadyFile(a.bridgeReadyFile))
	}
	a.logger.Info(fmt.Sprintf("🌉 [CODEX_CLI] Configured MCP bridge through app-server thread config (MCP tool timeout=%s, layer=codex_mcp_client)", mcpToolTimeout))

	if model.Options != nil {
		if effort, ok := model.Options["reasoning_effort"].(string); ok && effort != "" {
			opts = append(opts, llm.WithCodexReasoningEffort(effort))
			a.logger.Info(fmt.Sprintf("🧠 [CODEX_CLI] Reasoning effort set to: %s", effort))
		}
	}
	a.logger.Info("🌉 Using Codex app-server with the configured native tools, MCP bridge, and native permission handling")
	return opts, nil
}

func (a *Agent) appendPiCLIIntegrationOptionsForModel(opts []llmtypes.CallOption, model LLMModel) ([]llmtypes.CallOption, error) {
	var err error
	opts, err = a.appendPiCLIIntegrationOptions(opts)
	if err != nil {
		return nil, err
	}
	if model.Options != nil {
		if provider, ok := model.Options["pi_provider"].(string); ok && provider != "" {
			opts = append(opts, llm.WithPiProvider(provider))
		}
	}
	return opts, nil
}
