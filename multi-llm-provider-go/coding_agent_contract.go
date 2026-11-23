package llmproviders

import (
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/agycli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
	musecli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

// CodingAgentTransport describes the native process interface. App-server
// provides structured events and live steering on a persistent connection.
type CodingAgentTransport string

const (
	CodingAgentTransportTmux       CodingAgentTransport = "tmux"
	CodingAgentTransportStructured CodingAgentTransport = "structured"
	CodingAgentTransportAppServer  CodingAgentTransport = "app-server"
	CodingAgentTransportRPC        CodingAgentTransport = "rpc"
	CodingAgentTransportACP        CodingAgentTransport = "acp"
	CodingAgentTransportSDK        CodingAgentTransport = "sdk"
	CodingAgentTransportMSP        CodingAgentTransport = "msp"
)

// CodingAgentProviderContract is the provider-level contract that host apps can
// rely on when treating a provider as a coding agent. Adapter-specific code
// still owns exact CLI flags, but this struct is the shared capability gate used
// by tests and orchestration code.
type CodingAgentProviderContract struct {
	Provider Provider
	// ModelID is set only when a provider becomes a coding agent for a specific
	// model/transport. Prefer provider-level CLI entries for new coding agents.
	ModelID string

	DisplayName string
	CLIName     string
	Transport   CodingAgentTransport

	// RuntimeBinary is the executable the terminal probes on PATH to detect
	// the CLI (e.g. "claude"). The providers page, the manifest, and the P0
	// runner derive detection from this field — never a second hardcoded list.
	RuntimeBinary string
	// InstallCommand is the allowlisted command shown on the providers page
	// and executed by guided terminal setup to install the CLI.
	InstallCommand string
	// VersionProbeArgs asks the CLI for its version (usually ["--version"]).
	// CodingAgentCLIVersion runs RuntimeBinary with these args.
	VersionProbeArgs []string
	// MinCLIVersion is the oldest CLI version P0 has certified, in the
	// CLI's own --version form. Older versions fail the floor check with an
	// explicit update message instead of undefined adapter behavior.
	MinCLIVersion string

	RequiresWorkingDir     bool
	RequiresOwnerSessionID bool
	UsesPersistentSession  bool
	SupportsLiveInput      bool
	SupportsInterrupt      bool
	SupportsTerminalStream bool
	// SupportsStatusLine reports whether the adapter implements the
	// llmtypes.StatusLineProvider interface and emits a StreamChunkTypeStatusLine
	// chunk (canonical provider name, owning tmux_session in Metadata, no
	// placeholder model). Consumers surface its real-time token/cost telemetry in
	// the terminal pane. Certified by CertStatusLine.
	SupportsStatusLine      bool
	SupportsFinalExtraction bool
	SupportsNativeResume    bool
	UsesMCPBridge           bool
	// RequiresMCPBridgeConfig means host apps must fail before launch if they
	// cannot build and pass the provider's MCP bridge config. Coding agents
	// should never silently fall back to tool-less or unrestricted built-in tool
	// mode when the bridge is unavailable.
	RequiresMCPBridgeConfig bool
	SupportsBridgeOnlyTools bool
	// ToolRestrictionGaps records accepted limits of a best-effort policy.
	// Non-empty notes never constitute a strict bridge-only guarantee.
	ToolRestrictionGaps    []string
	UsesNativeSystemPrompt bool
	LaunchesViaLoginShell  bool
	ProcessScopedCleanup   bool
	HandlesTmuxSessionLoss bool
	StructuredFallback     bool
	ImageInputInteractive  bool

	// SurfacesTokenUsage reports whether the adapter consumes input/output/cache
	// token counts from the CLI's output (stream-json events, transcript files,
	// etc.) and reports them through GenerationInfo. False means the orchestrator
	// gets no usage data and the cost ledger writes a bare row.
	SurfacesTokenUsage bool

	// TokenUsageSource describes WHERE the adapter obtains usage data so cost
	// auditing knows the fidelity floor. One of:
	//   "stream-json"     — exact, parsed from the CLI's structured output.
	//   "transcript-file" — exact, parsed from a CLI-written transcript on disk.
	//   "estimated"       — approximate; the adapter heuristically guesses.
	// "estimated" callers (e.g. cursor's tmux mode at ~4 chars/token) MUST mark
	// the source so cost reports can be flagged as approximate.
	TokenUsageSource string

	// SurfacesPlanUsage reports whether the adapter exposes the account's plan
	// usage windows (e.g. 5-hour / 7-day percentages and reset times) as
	// structured llmtypes.RateLimitWindow data on its StatusLine
	// (RateLimitWindowsMetaKey). The chat UI shows them and the coding
	// watchdog trusts them over screen text, so a provider that has them must
	// keep proving it (CertPlanUsage).
	SurfacesPlanUsage bool

	// PlanUsageSource describes where plan usage comes from when
	// SurfacesPlanUsage is true: "statusline-sidecar" (the CLI's own
	// statusline JSON) or "transcript-file" (a CLI-written rollout/transcript).
	PlanUsageSource string

	// PlanUsageUnavailableReason is REQUIRED when SurfacesPlanUsage is false:
	// why this CLI cannot report plan usage (so onboarding a provider forces
	// the decision instead of silently skipping it).
	PlanUsageUnavailableReason string
	// InteractiveTokenUsageSource optionally overrides TokenUsageSource for
	// turns executed in the persistent interactive (tmux) lane. Empty
	// means the interactive lane meters identically to the default lane
	// (or has no interactive turns at all). When set it must name the
	// same closed label set: a dual-lane provider may bill exec turns
	// from JSON envelopes and sidecar turns from transcript files, but
	// each lane's source must be declared.
	InteractiveTokenUsageSource string

	// AdapterReadsTranscript reports whether the adapter has code that reads
	// the CLI's on-disk conversation transcript directly — used for sidecar
	// features (token extraction for tmux mode, replay, forensic audit). This
	// is independent of SupportsNativeResume: a CLI can fully support --resume
	// without us ever reading its files. Only flip true if a transcript
	// reader function is registered in transcriptReaderRegistry.
	AdapterReadsTranscript bool

	// TranscriptPathTemplate is a human-readable hint of where the CLI writes
	// its transcript (e.g. "~/.cursor/chats/<md5(cwd)>/<id>/store.db"). Empty
	// if the adapter doesn't read transcripts. Documentation only — adapters
	// should resolve the actual path via package-local helpers, never by
	// string-formatting this value.
	TranscriptPathTemplate string

	// SupportsStructuredStreaming reports whether the adapter emits STRUCTURED
	// assistant-text (Content) and tool-call (ToolCallStart) stream chunks live
	// during a turn — the design-first, no-terminal streaming path a UI can render
	// without the raw tmux pane. The SOURCE is provider-specific and does not
	// matter to this flag: claude/codex/cursor tail the CLI's on-disk transcript
	// (tagged "<provider>_stream_source":"transcript"), while pi consumes an
	// injected marker stream. It is STRONGER than AdapterReadsTranscript: reading
	// a transcript once for a final-answer/token summary is not streaming. When
	// true, the provider MUST register a CertStructuredStreaming certification and
	// is held to it as a P0 requirement (see RequiredP0CodingAgentCertificationIDs).
	// Flip true only once the live structured stream + its real E2E exist.
	SupportsStructuredStreaming bool

	// RequiresWorkspaceTrust reports whether the CLI can block startup on a
	// trust/auth TUI prompt before any user prompt can be submitted. Some CLIs
	// show a "do you trust the files in this folder?" dialog, or a startup
	// login picker, that the adapter must detect and surface instead of
	// parsing as ready/output. Structured-only CLIs do not render these TUI
	// prompts.
	//
	// Pairs with CertTrustAuthPrompts in codingAgentCapabilityCertifications:
	// when RequiresWorkspaceTrust=true, the provider must have a registered
	// cert (or known-gap allowance) proving the trust handler works in a
	// fresh workspace.
	RequiresWorkspaceTrust bool

	// RestoreAsksInteractivePrompts reports whether the CLI shows an
	// interactive menu (e.g. "Resume from summary? Compact? Discard?")
	// when launched with --resume, which the adapter must navigate before
	// the new prompt can flow. Today only Claude Code does this (its
	// resume-summary menu, dismissed via isClaudeResumeSummaryMenu); other
	// coding CLIs resume silently.
	//
	// Pairs with CertResumeCompactionStartup so adapters that genuinely
	// face this dialog must prove they navigate it. The pairing also lets
	// CLIs that DON'T show the dialog skip an irrelevant cert instead of
	// dragging it into knownCertificationGaps.
	RestoreAsksInteractivePrompts bool

	// APIKeyEnvVars lists the environment variables the CLI accepts to
	// authenticate. Empty means the CLI relies on its own native auth
	// flow (stored credentials, OAuth, etc.) and no env-based shortcut
	// works — e.g. cursor-agent uses cursor.sh's logged-in identity.
	//
	// For multi-provider CLIs, list the canonical primary vars only;
	// provider routing lives in adapter-specific configuration.
	APIKeyEnvVars []string

	// HandlesCtrlCCleanExit reports whether sending Ctrl+C (0x03 keystroke
	// in tmux mode, SIGINT in structured mode) leaves the CLI's persisted
	// chat state intact and resumable. Distinct from SupportsInterrupt,
	// which only proves the interrupt is RECEIVED — this stronger claim
	// is that --resume <id> on the next launch still loads the prior
	// conversation without corruption.
	//
	// Pairs with CertCtrlCStatePreserved. Default is false on every
	// provider today: we haven't yet written the e2e tests that prove
	// state survives a cancel for any CLI. Flip true together with
	// landing the test entry — the drift test in
	// TestAllCodingAgentCapabilityClaimsHaveRegisteredCertification will
	// fail otherwise.
	HandlesCtrlCCleanExit bool

	// WorkingDirInstructionFile names the file the CLI auto-loads as
	// project-level guidance when present in the working directory (e.g.
	// "CLAUDE.md", "AGENTS.md", "GEMINI.md", or for cursor the directory
	// "<workingDir>/.cursor/rules"). Workflow authors can drop this file
	// in their step folders and the CLI follows it without going through
	// the orchestrator's per-session system-prompt injection.
	//
	// DOCUMENTATION ONLY — the orchestrator does NOT write to this path.
	// Per-session system prompts go through each CLI's runtime mechanism
	// (Claude --system-prompt-file, Codex -c model_instructions_file,
	// Cursor .cursor/rules/mlp-system-*.mdc). Confusing the two paths
	// would either leak ephemeral session content into persistent files
	// or block authors from contributing their own static guidance.
	WorkingDirInstructionFile string

	// NativeProjectInstructionFiles comes from the provider adapter. Empty means
	// unknown, not proof that the CLI ignores all files. Hosts may omit their
	// own injection only when these files are in the actual CLI working root.
	// This declaration is not evidence that a running CLI loaded a file.
	NativeProjectInstructionFiles []string

	// UserInstructionFile names the home-directory file the CLI auto-loads
	// for the operator's persistent style preferences (e.g.
	// "~/.claude/CLAUDE.md", "~/.codex/AGENTS.md"). DOCUMENTATION ONLY —
	// the orchestrator must never write here. The operator owns this
	// file because it applies to every invocation across every workspace.
	UserInstructionFile string

	// WorkingDirMCPConfigFile names the file/directory the CLI auto-loads
	// for project-scoped MCP server configuration (e.g. ".cursor/mcp.json",
	// ".gemini/settings.json"). The orchestrator writes to these paths today
	// as part of bridge injection — adapters resolve
	// them via package-local helpers (WithCursorMCPConfig, etc.), this
	// field is purely documentation so workflow authors know which files
	// in their step folder are conventionally owned by the orchestrator.
	WorkingDirMCPConfigFile string

	// UserMCPConfigFile names the home-directory file where the operator
	// can register MCP servers that should be visible to every invocation
	// of this CLI (e.g. "~/.codex/config.toml", "~/.gemini/settings.json").
	// DOCUMENTATION ONLY — the orchestrator does not write here.
	UserMCPConfigFile string

	// SupportsStalledTurnDiagnosis reports whether the adapter exposes a
	// DiagnoseTurnCompletion(..., since) function that can be asked,
	// independently of any live turn, whether the provider's own completion
	// signal shows a turn already finished after a given time (PLAT-116: the
	// platform's own bridge from that live signal back to its caller can
	// silently stall — a real incident reproduced live on Codex — and
	// without this, nothing distinguishes "still working" from "finished,
	// but the platform never found out" once that happens). Only Codex and
	// Claude Code have this today; Pi CLI's marker file lives in a
	// per-session temp directory with no OS-standard path an external
	// caller can read without a registry export first, and Cursor's default
	// (structured) transport never reaches the interactive completion path
	// this diagnoses.
	//
	// Pairs with CertStalledTurnDiagnosis, promoted to P0 wherever true
	// (RequiredP0CodingAgentCertificationIDs): a false "made no progress"
	// timeout on a turn that actually succeeded is a release-blocking
	// defect, not a nice-to-have.
	SupportsStalledTurnDiagnosis bool

	// SupportsDurableAck reports whether a live-input send is confirmed
	// against the CLI's own durable record (rollout/transcript/marker),
	// not just the pane. The pane stays the fast resubmit signal, but a
	// pane misread must not fail a send the CLI actually accepted, and a
	// queued-then-flushed steer must be provable after the fact.
	//
	// Pairs with CertDurableAck, promoted to P0 wherever true.
	SupportsDurableAck bool
}

var codingAgentProviderContracts = map[Provider]CodingAgentProviderContract{
	ProviderClaudeCode: {
		NativeProjectInstructionFiles: claudecode.NativeProjectInstructionFiles(),
		Provider:                      ProviderClaudeCode,
		DisplayName:                   "Claude Code",
		CLIName:                       "claude",
		Transport:                     CodingAgentTransportTmux,
		RuntimeBinary:                 "claude",
		InstallCommand:                "npm install -g @anthropic-ai/claude-code@latest",
		VersionProbeArgs:              []string{"--version"},
		MinCLIVersion:                 "2.1.278",
		RequiresWorkingDir:            true,
		RequiresOwnerSessionID:        true,
		UsesPersistentSession:         true,
		SupportsLiveInput:             true,
		SupportsInterrupt:             true,
		SupportsTerminalStream:        true,
		SupportsStatusLine:            true,
		SupportsFinalExtraction:       true,
		SupportsNativeResume:          true,
		UsesMCPBridge:                 true,
		RequiresMCPBridgeConfig:       true,
		SupportsBridgeOnlyTools:       true,
		UsesNativeSystemPrompt:        true,
		LaunchesViaLoginShell:         true,
		ProcessScopedCleanup:          true,
		HandlesTmuxSessionLoss:        true,
		StructuredFallback:            false,
		ImageInputInteractive:         true,
		SurfacesTokenUsage:            true,
		TokenUsageSource:              "transcript-file",
		SurfacesPlanUsage:             true,
		PlanUsageSource:               "statusline-sidecar",
		AdapterReadsTranscript:        true,
		TranscriptPathTemplate:        "~/.claude/projects/*/<session-id>.jsonl",
		SupportsStructuredStreaming:   true,
		RequiresWorkspaceTrust:        true,
		RestoreAsksInteractivePrompts: true,
		// Claude Code authentication is its saved login or a process-scoped
		// workflow OAuth token. Ambient API-key variables are explicitly ignored.
		APIKeyEnvVars:                []string{},
		WorkingDirInstructionFile:    "CLAUDE.md",
		UserInstructionFile:          "~/.claude/CLAUDE.md",
		WorkingDirMCPConfigFile:      ".mcp.json",
		UserMCPConfigFile:            "~/.claude/settings.json",
		SupportsStalledTurnDiagnosis: true,
		SupportsDurableAck:           true,
	},
	ProviderCodexCLI: {
		NativeProjectInstructionFiles: codexcli.NativeProjectInstructionFiles(),
		Provider:                ProviderCodexCLI,
		DisplayName:             "Codex CLI",
		CLIName:                 "codex",
		Transport:               CodingAgentTransportAppServer,
		RuntimeBinary:           "codex",
		InstallCommand:          "npm install -g @openai/codex@latest",
		VersionProbeArgs:        []string{"--version"},
		MinCLIVersion:           "0.164.0",
		RequiresWorkingDir:      true,
		RequiresOwnerSessionID:  true,
		UsesPersistentSession:   true,
		SupportsLiveInput:       true,
		SupportsInterrupt:       true,
		SupportsTerminalStream:  false,
		SupportsStatusLine:      true,
		SupportsFinalExtraction: true,
		// Wired end-to-end: mcpagent.Agent.CodexSessionID is populated by the
		// adapter, session_handle persists it, llmproviders.WithCodexResumeSessionID
		// re-exports the resume option, and server.go's restore switch reads
		// it back via `case "codex-cli":` (server.go:6270). Contract used to
		// say false; the drift test in coding_agent_contract_test.go now
		// enforces this matches the actual wiring.
		SupportsNativeResume:         true,
		UsesMCPBridge:                true,
		RequiresMCPBridgeConfig:      true,
		SupportsBridgeOnlyTools:      true,
		UsesNativeSystemPrompt:       true,
		LaunchesViaLoginShell:        false,
		ProcessScopedCleanup:         true,
		HandlesTmuxSessionLoss:       false,
		StructuredFallback:           false,
		ImageInputInteractive:        true,
		SurfacesTokenUsage:           true,
		TokenUsageSource:             "stream-json",
		SurfacesPlanUsage:            true,
		PlanUsageSource:              "app-server",
		AdapterReadsTranscript:       true,
		TranscriptPathTemplate:       "~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<session-uuid>.jsonl",
		SupportsStructuredStreaming:  true,
		RequiresWorkspaceTrust:       false,
		APIKeyEnvVars:                []string{"CODEX_API_KEY"},
		WorkingDirInstructionFile:    "AGENTS.md",
		UserInstructionFile:          "~/.codex/AGENTS.md",
		WorkingDirMCPConfigFile:      "", // Codex receives session MCP config over app-server stdio.
		UserMCPConfigFile:            "~/.codex/config.toml",
		SupportsStalledTurnDiagnosis: false,
		SupportsDurableAck:           true,
	},
	ProviderCursorCLI: {
		NativeProjectInstructionFiles: cursorcli.NativeProjectInstructionFiles(),
		Provider:                    ProviderCursorCLI,
		DisplayName:                 "Cursor CLI",
		CLIName:                     "cursor-agent",
		Transport:                   CodingAgentTransportSDK,
		RuntimeBinary:               "cursor-agent",
		InstallCommand:              "curl --proto '=https' --proto-redir '=https' --tlsv1.2 https://cursor.com/install | bash",
		VersionProbeArgs:            []string{"--version"},
		MinCLIVersion:               "2026.10.22",
		RequiresWorkingDir:          true,
		RequiresOwnerSessionID:      true,
		UsesPersistentSession:       true,
		SupportsLiveInput:           true, // run.steer adds the message at the next safe step.
		SupportsInterrupt:           true,
		SupportsTerminalStream:      false,
		SupportsFinalExtraction:     true,
		SupportsNativeResume:        true,
		UsesMCPBridge:               true,
		RequiresMCPBridgeConfig:     true,
		SupportsBridgeOnlyTools:     true,
		UsesNativeSystemPrompt:      true,
		LaunchesViaLoginShell:       false,
		ProcessScopedCleanup:        true,
		HandlesTmuxSessionLoss:      false,
		StructuredFallback:          false,
		ImageInputInteractive:       true,
		SurfacesTokenUsage:          true,
		TokenUsageSource:            "sdk-events",
		PlanUsageUnavailableReason:  "Cursor CLI exposes no local or structured plan usage; only its private account API",
		AdapterReadsTranscript:      false,
		TranscriptPathTemplate:      "",
		SupportsStructuredStreaming: true,
		RequiresWorkspaceTrust:      false,
		APIKeyEnvVars:               []string{"CURSOR_API_KEY"},
		WorkingDirInstructionFile:   ".cursor/rules",    // directory of .mdc rule files; Cursor honors every file in here when alwaysApply:true is set in frontmatter.
		UserInstructionFile:         "~/.cursor/rules",  // same directory layout at the user level
		WorkingDirMCPConfigFile:     ".cursor/mcp.json", // adapter writes this when WithCursorMCPConfig is provided
		UserMCPConfigFile:           "~/.cursor/mcp.json",
		SupportsDurableAck:          false,
	},
	ProviderPiCLI: {
		NativeProjectInstructionFiles: picli.NativeProjectInstructionFiles(),
		Provider:                    ProviderPiCLI,
		DisplayName:                 "Pi CLI",
		CLIName:                     "pi",
		Transport:                   CodingAgentTransportRPC,
		RuntimeBinary:               "pi",
		InstallCommand:              "npm install -g @earendil-works/pi-coding-agent@latest",
		VersionProbeArgs:            []string{"--version"},
		MinCLIVersion:               "1.4.0",
		RequiresWorkingDir:          true,
		RequiresOwnerSessionID:      true,
		UsesPersistentSession:       true,
		SupportsLiveInput:           true,
		SupportsInterrupt:           true,
		SupportsTerminalStream:      false,
		SupportsStatusLine:          true,
		SupportsFinalExtraction:     true,
		SupportsNativeResume:        true,
		UsesMCPBridge:               true,
		RequiresMCPBridgeConfig:     true,
		SupportsBridgeOnlyTools:     true,
		UsesNativeSystemPrompt:      true,
		LaunchesViaLoginShell:       false,
		ProcessScopedCleanup:        true,
		HandlesTmuxSessionLoss:      false,
		StructuredFallback:          false,
		ImageInputInteractive:       false,
		SurfacesTokenUsage:          true,
		TokenUsageSource:            "stream-json",
		PlanUsageUnavailableReason:  "Pi runs on API keys / bring-your-own providers; its output carries tokens and cost, no plan windows",
		AdapterReadsTranscript:      true,
		TranscriptPathTemplate:      "$PI_CODING_AGENT_SESSION_DIR/**/*_<session-id>.jsonl or ~/.pi/agent/sessions/**/*_<session-id>.jsonl",
		SupportsStructuredStreaming: true,
		APIKeyEnvVars:               []string{"PI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY"},
		// Pi follows AGENTS.md-style project instructions, but this adapter
		// injects per-session system guidance through --append-system-prompt
		// rather than writing durable project files. MCP is provided through
		// native builtin:mcp extension and a private session-scoped
		// mcp.json; --no-builtin-tools provides the bridge-only gate.
		WorkingDirInstructionFile: "AGENTS.md",
		UserInstructionFile:       "~/.pi/agent/AGENTS.md",
		WorkingDirMCPConfigFile:   ".pi/mcp.json",
		UserMCPConfigFile:         "~/.pi/agent/mcp.json",
		SupportsDurableAck:        true,
	},
	ProviderMuseCLI: {
		NativeProjectInstructionFiles: musecli.NativeProjectInstructionFiles(),
		Provider:                ProviderMuseCLI,
		DisplayName:             "Muse",
		CLIName:                 "muse",
		Transport:               CodingAgentTransportTmux,
		RuntimeBinary:           "muse",
		InstallCommand:          "curl --proto '=https' --proto-redir '=https' --tlsv1.2 https://dev.meta.ai/install.sh | bash",
		VersionProbeArgs:        []string{"--version"},
		MinCLIVersion:           "1.3.0",
		RequiresWorkingDir:      true,
		RequiresOwnerSessionID:  true,
		UsesPersistentSession:   true,
		SupportsLiveInput:       true,
		SupportsInterrupt:       true,
		SupportsTerminalStream:  true,
		SupportsFinalExtraction: true,
		SupportsNativeResume:    true,
		UsesMCPBridge:           true,
		RequiresMCPBridgeConfig: true,
		SupportsBridgeOnlyTools: false,
		ToolRestrictionGaps: []string{
			"Native tools remain model-visible; the PreToolUse hook gates execution only.",
			"Muse 1.1.1 write_todos can bypass PreToolUse; internal session controls are not fully contained.",
			"Native questions may still appear; recommended-answer handling remains the recovery path.",
		},
		UsesNativeSystemPrompt:      false,
		LaunchesViaLoginShell:       true,
		ProcessScopedCleanup:        true,
		HandlesTmuxSessionLoss:      false,
		StructuredFallback:          false,
		ImageInputInteractive:       false,
		SurfacesTokenUsage:          true,
		TokenUsageSource:            "transcript-file",
		SurfacesPlanUsage:           true,
		PlanUsageSource:             "msp",
		AdapterReadsTranscript:      true,
		TranscriptPathTemplate:      "$XDG_DATA_HOME/muse/sessions/YYYY/MM/DD/<session-id>/session.jsonl",
		SupportsStructuredStreaming: false,
		RequiresWorkspaceTrust:      true,
		APIKeyEnvVars:               []string{"META_API_KEY"},
		WorkingDirInstructionFile:   "AGENTS.md",
		SupportsDurableAck:          true,
	},
	ProviderAgyCLI: {
		NativeProjectInstructionFiles: agycli.NativeProjectInstructionFiles(),
		Provider:                ProviderAgyCLI,
		DisplayName:             "Antigravity",
		CLIName:                 "agy",
		Transport:               CodingAgentTransportTmux,
		RuntimeBinary:           "agy",
		InstallCommand:          "curl -fsSL https://antigravity.google/cli/install.sh | bash",
		VersionProbeArgs:        []string{"--version"},
		MinCLIVersion:           "1.2.12",
		RequiresWorkingDir:      true,
		RequiresOwnerSessionID:  true,
		UsesPersistentSession:   true,
		SupportsLiveInput:       true,
		SupportsInterrupt:       true,
		SupportsTerminalStream:  false,
		SupportsStatusLine:      false,
		SupportsFinalExtraction: true,
		SupportsNativeResume:    true,
		UsesMCPBridge:           true,
		RequiresMCPBridgeConfig: true,
		SupportsBridgeOnlyTools: true,
		ToolRestrictionGaps: []string{
			"Native tool mode uses a workspace PreToolUse hook because agy has no --tools flag. mcp_only denies every native tool; full (confined) admits the native toolset alongside the bridge.",
			"MCP servers mount into global user config only (agy mcp add); no per-run or per-project scope, so parallel runs cannot hold different mounts.",
		},
		UsesNativeSystemPrompt:      false,
		LaunchesViaLoginShell:       false,
		ProcessScopedCleanup:        true,
		HandlesTmuxSessionLoss:      false,
		StructuredFallback:          false,
		ImageInputInteractive:       false,
		SurfacesTokenUsage:          true,
		TokenUsageSource:            "exec-json",
		InteractiveTokenUsageSource: "transcript-file",
		PlanUsageUnavailableReason:  "agy exposes per-turn tokens in JSON and conversation files, but no account plan usage windows or reset times through the CLI",
		AdapterReadsTranscript:      true,
		TranscriptPathTemplate:      "~/.gemini/antigravity-cli/conversations/<conversation-id>.db",
		SupportsStructuredStreaming: false,
		RequiresWorkspaceTrust:      true,
		// Key mode is real (AUTH_MODE_GEMINI_API_KEY, changelog 1.1.13) but
		// needs BOTH the env key and modelProvider:"gemini" in settings.json;
		// without the provider switch the OAuth login wins and the key is
		// ignored. GOOGLE_API_KEY takes precedence when both are set.
		APIKeyEnvVars:      []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		SupportsDurableAck: true,
	},
}

// GetCodingAgentProviderContract returns the shared coding-agent contract for a
// provider/model pair.
func GetCodingAgentProviderContract(provider Provider, modelID string) (CodingAgentProviderContract, bool) {
	return GetCodingAgentProviderContractForTransport(provider, modelID, "")
}

// GetCodingAgentProviderContractForTransport is the contract of a chat whose
// Claude Code runs on the transport it chose ("sdk" or "tmux"); empty uses
// the server's setting. Other providers ignore it.
func GetCodingAgentProviderContractForTransport(provider Provider, modelID, claudeTransport string) (CodingAgentProviderContract, bool) {
	normalizedProvider := Provider(strings.ToLower(strings.TrimSpace(string(provider))))
	contract, ok := codingAgentProviderContracts[normalizedProvider]
	if !ok {
		return CodingAgentProviderContract{}, false
	}
	contract.NativeProjectInstructionFiles = append([]string(nil), contract.NativeProjectInstructionFiles...)

	if strings.TrimSpace(contract.ModelID) != "" && !strings.EqualFold(strings.TrimSpace(modelID), contract.ModelID) {
		return CodingAgentProviderContract{}, false
	}
	if normalizedProvider == ProviderCursorCLI {
		if transport, err := resolveCursorCLITransport(); err == nil && transport == CursorCLITransportTmux {
			contract.Transport = CodingAgentTransportTmux
			contract.SupportsLiveInput = true
			contract.SupportsTerminalStream = true
			contract.SupportsDurableAck = true
			contract.LaunchesViaLoginShell = true
			contract.HandlesTmuxSessionLoss = true
			contract.SurfacesTokenUsage = true
			contract.TokenUsageSource = "estimated"
			contract.AdapterReadsTranscript = true
			contract.TranscriptPathTemplate = "~/.cursor/chats/<cwd-hash>/<sessionId>/store.db"
		}
	}
	if normalizedProvider == ProviderClaudeCode {
		if transport, err := resolveClaudeCodeTransport(claudeTransport); err == nil && transport == ClaudeCodeTransportSDK {
			contract.Transport = CodingAgentTransportSDK
			contract.SupportsTerminalStream = false
			contract.SupportsLiveInput = true // Native SDK priority-now input, confirmed by the CLI message UUID.
			contract.SupportsDurableAck = false
			// Keep the base persistent-session capability: SDK chats retain their process
			// while Claude-owned background tasks run after the answer.
			contract.LaunchesViaLoginShell = false
			contract.HandlesTmuxSessionLoss = false
			contract.SupportsStalledTurnDiagnosis = false
			contract.RestoreAsksInteractivePrompts = false
			contract.TokenUsageSource = "sdk-events"
			contract.SurfacesPlanUsage = false
			contract.PlanUsageSource = ""
			contract.PlanUsageUnavailableReason = "The Claude Agent SDK reports per-turn token usage; this adapter does not read account plan windows"
		}
	}
	if normalizedProvider == ProviderAgyCLI && agycli.AgyACPSelected() {
		// agy acp: protocol events, no screen; ACP has no mid-turn input, so a
		// message sent during a turn waits for the next one.
		contract.Transport = CodingAgentTransportACP
		contract.SupportsTerminalStream = false
		contract.SupportsLiveInput = false
		contract.LaunchesViaLoginShell = false
	}
	if normalizedProvider == ProviderMuseCLI && musecli.MuseMSPSelected() {
		// muse serve: protocol events, no screen.
		contract.Transport = CodingAgentTransportMSP
		contract.SupportsTerminalStream = false
		contract.SupportsStructuredStreaming = true
		contract.LaunchesViaLoginShell = false
		contract.TokenUsageSource = "msp"
	} else if normalizedProvider == ProviderMuseCLI {
		contract.SurfacesPlanUsage = false
		contract.PlanUsageSource = ""
		contract.PlanUsageUnavailableReason = "Muse reports plan usage only over muse serve (MUSE_CLI_TRANSPORT=msp)"
	}
	if normalizedProvider == ProviderCodexCLI {
		if transport, err := resolveCodexCLITransport(); err == nil && transport == CodexCLITransportTmux {
			contract.Transport = CodingAgentTransportTmux
			contract.SupportsTerminalStream = true
			contract.LaunchesViaLoginShell = true
			contract.HandlesTmuxSessionLoss = true
			contract.TokenUsageSource = "transcript-file"
			contract.PlanUsageSource = "transcript-file"
			contract.SupportsStalledTurnDiagnosis = true
		}
	}

	return contract, true
}

// IsCodingAgentProvider reports whether provider/model should be treated as a
// coding agent by host applications.
func IsCodingAgentProvider(provider Provider, modelID string) bool {
	_, ok := GetCodingAgentProviderContract(provider, modelID)
	return ok
}

// IsTmuxCodingAgentProvider reports whether provider/model uses the persistent
// tmux contract.
func IsTmuxCodingAgentProvider(provider Provider, modelID string) bool {
	contract, ok := GetCodingAgentProviderContract(provider, modelID)
	return ok && contract.Transport == CodingAgentTransportTmux
}

// CodingAgentProviderContracts returns all currently declared coding-agent
// contracts.
func CodingAgentProviderContracts() []CodingAgentProviderContract {
	contracts := make([]CodingAgentProviderContract, 0, len(codingAgentProviderContracts))
	for provider, registered := range codingAgentProviderContracts {
		contract, _ := GetCodingAgentProviderContract(provider, registered.ModelID)
		contracts = append(contracts, contract)
	}
	sort.Slice(contracts, func(i, j int) bool {
		if contracts[i].Provider == contracts[j].Provider {
			return contracts[i].ModelID < contracts[j].ModelID
		}
		return contracts[i].Provider < contracts[j].Provider
	})
	return contracts
}
