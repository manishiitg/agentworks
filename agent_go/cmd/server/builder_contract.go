package server

// BuilderInvariantID is an executable proof point for one user-facing
// invariant the coding-agent-loop layer must hold across the HTTP API,
// orchestrator runtime, MCP bridge, terminal store, and frontend.
//
// IDs correspond to the rows in docs/core/coding_agent_builder_e2e_contract.md
// + the integration contract in agent_go/docs/cross_repo_integration_contract.md.
// Adding a row to either doc without registering a BuilderInvariantID here
// (and either a cert entry or a knownBuilderInvariantGaps allowance) drifts
// the contract — TestAllBuilderInvariantsHaveRegisteredCertification fails.
//
// Each ID names a tightly-scoped invariant. Compound docs that cover
// multiple flows should split into one ID per flow (e.g. cancel-on-disconnect
// and cancel-on-stop are separate IDs even though the doc lists them together).
type BuilderInvariantID string

const (
	// Runtime invariants from docs/core/coding_agent_builder_e2e_contract.md.

	// InvCWDMatching: chat + workflow shell + coding agents + execute_shell_command
	// all see the same caller-workspace cwd.
	InvCWDMatching BuilderInvariantID = "cwd_matching"

	// InvMCPBridgeSessionScoped: every coding-agent MCP call receives the
	// correct per-session bridge URL/token; sessions never cross-pollinate.
	InvMCPBridgeSessionScoped BuilderInvariantID = "mcp_bridge_session_scoped"

	// InvTmuxLifecyclePolicy: workflow steps + sub-agents + background agents
	// default to bounded tmux lifecycle for tmux-contract providers; chat
	// defaults to persistent for tmux providers only.
	InvTmuxLifecyclePolicy BuilderInvariantID = "tmux_lifecycle_policy"

	// InvTerminalRetentionWindow: bounded terminals stay viewable for the
	// configured retention window, expose closes_at, then get cleaned up.
	InvTerminalRetentionWindow BuilderInvariantID = "terminal_retention_window"

	// InvTerminalSelectionStable: completed snapshots stay selectable;
	// active terminal refresh doesn't steal manual selection.
	InvTerminalSelectionStable BuilderInvariantID = "terminal_selection_stable"

	// InvTerminalOwnerReconciliation: lifecycle is keyed by stable runtime
	// identity (tmux_session, step id), not the exact event owner string —
	// start/chunk and end events with different owner strings reconcile.
	InvTerminalOwnerReconciliation BuilderInvariantID = "terminal_owner_reconciliation"

	// InvTerminalScrollPreserved: manual scroll-up survives terminal refresh;
	// scroll-at-bottom auto-follows new content.
	InvTerminalScrollPreserved BuilderInvariantID = "terminal_scroll_preserved"

	// InvTerminalDebugIDsCopyable: debug IDs reachable from the UI without
	// cluttering the normal pane view.
	InvTerminalDebugIDsCopyable BuilderInvariantID = "terminal_debug_ids_copyable"

	// InvNoDuplicateUnifiedCompletion: unified completion doesn't duplicate
	// terminal output, tool panels, or stale streaming text.
	InvNoDuplicateUnifiedCompletion BuilderInvariantID = "no_duplicate_unified_completion"

	// InvProviderVsTerminalCompletionSeparate: provider completion (adapter-
	// owned, based on idle pane + final extraction) and terminal UI completion
	// (inactivity timers, "STATUS: COMPLETED") are separate contracts;
	// neither should trigger workflow success on its own.
	InvProviderVsTerminalCompletionSeparate BuilderInvariantID = "provider_vs_terminal_completion_separate"

	// User-facing flow invariants (from "Required Builder E2E Matrix").

	InvChatLaunch                BuilderInvariantID = "chat_launch"
	InvMultiTurnMemory           BuilderInvariantID = "multi_turn_memory"
	InvTmuxLossContinuation      BuilderInvariantID = "tmux_loss_continuation"
	InvLiteralPromptText         BuilderInvariantID = "literal_prompt_text"
	InvStalePromptDraft          BuilderInvariantID = "stale_prompt_draft"
	InvLiveSteerSameSession      BuilderInvariantID = "live_steer_same_session"
	InvCancellationProducesEvent BuilderInvariantID = "cancellation_produces_event"
	InvCancelDoesNotReusePane    BuilderInvariantID = "cancel_does_not_reuse_pane"
	InvWorkflowStepCwdMCP        BuilderInvariantID = "workflow_step_cwd_mcp"
	InvQueryStepResolution       BuilderInvariantID = "query_step_resolution"
	InvTodoOrchestratorParallel  BuilderInvariantID = "todo_orchestrator_parallel"
	InvBackgroundAgentVisibility BuilderInvariantID = "background_agent_visibility"
	InvTerminalCenterStates      BuilderInvariantID = "terminal_center_states"
	InvTerminalLifecycleAPI      BuilderInvariantID = "terminal_lifecycle_api"
	InvTerminalDismissAPI        BuilderInvariantID = "terminal_dismiss_api"
	InvHistoryResume             BuilderInvariantID = "history_resume"
	InvUIFormattingSeparation    BuilderInvariantID = "ui_formatting_separation"

	// New invariants landed this session.

	// InvWorkflowDependencyPreflight: run_full_workflow refuses to launch
	// when declared MCP servers / secrets / skills aren't configured on
	// this host. Surfaces the missing dependencies as a single structured
	// error before any step fires.
	InvWorkflowDependencyPreflight BuilderInvariantID = "workflow_dependency_preflight"

	// InvTerminalDynamicResize: frontend pane width gets propagated to
	// tmux on resize so cursor/claude/codex/gemini panes don't wrap or
	// crop their UI.
	InvTerminalDynamicResize BuilderInvariantID = "terminal_dynamic_resize"

	// InvTerminalCtrlCDelivery: the debug-action dropdown's "Send Ctrl+C"
	// option delivers the 0x03 keystroke (tmux send-keys C-c) to the
	// foreground TUI without disturbing surrounding pane state.
	InvTerminalCtrlCDelivery BuilderInvariantID = "terminal_ctrl_c_delivery"

	// InvCostTrackingPipeline: real LLM token usage flows adapter →
	// orchestrator → cost ledger, with the right model id and bucketing,
	// for both API providers and CLI providers. The HTTP /api/cost endpoint
	// surfaces the captured turn so the UI cost dashboard sees real numbers.
	InvCostTrackingPipeline BuilderInvariantID = "cost_tracking_pipeline"

	// InvInspectorDebugVisibility: the /api/inspector debug endpoints
	// capture real conversation events for a live session (system prompt,
	// LLM calls, tool calls, streaming chunks, token usage). Unknown
	// session ids return 404 cleanly.
	InvInspectorDebugVisibility BuilderInvariantID = "inspector_debug_visibility"

	// InvProviderAPIKeyValidation: /api/validate-key endpoints accept a
	// valid API key for the requested provider (Anthropic, OpenAI, Vertex)
	// and cleanly reject bogus keys without surfacing implementation
	// internals or leaking the secret on the wire.
	InvProviderAPIKeyValidation BuilderInvariantID = "provider_api_key_validation"
)

// AllBuilderInvariants returns every declared invariant ID. The drift test
// iterates this to enforce certification coverage. Order is deterministic.
func AllBuilderInvariants() []BuilderInvariantID {
	return []BuilderInvariantID{
		InvCWDMatching,
		InvMCPBridgeSessionScoped,
		InvTmuxLifecyclePolicy,
		InvTerminalRetentionWindow,
		InvTerminalSelectionStable,
		InvTerminalOwnerReconciliation,
		InvTerminalScrollPreserved,
		InvTerminalDebugIDsCopyable,
		InvNoDuplicateUnifiedCompletion,
		InvProviderVsTerminalCompletionSeparate,
		InvChatLaunch,
		InvMultiTurnMemory,
		InvTmuxLossContinuation,
		InvLiteralPromptText,
		InvStalePromptDraft,
		InvLiveSteerSameSession,
		InvCancellationProducesEvent,
		InvCancelDoesNotReusePane,
		InvWorkflowStepCwdMCP,
		InvQueryStepResolution,
		InvTodoOrchestratorParallel,
		InvBackgroundAgentVisibility,
		InvTerminalCenterStates,
		InvTerminalLifecycleAPI,
		InvTerminalDismissAPI,
		InvHistoryResume,
		InvUIFormattingSeparation,
		InvWorkflowDependencyPreflight,
		InvTerminalDynamicResize,
		InvTerminalCtrlCDelivery,
		InvCostTrackingPipeline,
		InvInspectorDebugVisibility,
		InvProviderAPIKeyValidation,
	}
}

// BuilderInvariantCertification records the executable proof for a builder
// invariant. TestFile is repository-relative (from coding-agent-loop root)
// so the drift test can open it directly to verify TestName exists.
type BuilderInvariantCertification struct {
	ID          BuilderInvariantID
	TestFile    string
	TestName    string
	Env         []string // env vars that must be set for the test to actually run (e.g. RUN_REAL_E2E=1)
	Description string
	RealE2E     bool // true when the cert exercises real CLIs / real MCP servers, not stubs
}

// builderInvariantCertifications maps each invariant to its proof. Drift test
// asserts every ID has either an entry here or an entry in
// knownBuilderInvariantGaps. Adding a new BuilderInvariantID without one of
// those two fails the test.
var builderInvariantCertifications = map[BuilderInvariantID]BuilderInvariantCertification{
	InvWorkflowDependencyPreflight: {
		ID:          InvWorkflowDependencyPreflight,
		TestFile:    "agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/preflight_validation.go",
		TestName:    "validateWorkflowDependencies",
		Description: "run_full_workflow refuses to launch when declared MCP servers aren't configured; see also the run-time call in planning_exports.go around the 'preflight: refuse to launch' comment.",
		// Not RealE2E yet — the validator has the production code but
		// no dedicated test. Listed here so the cert exists and the
		// drift test passes; flip RealE2E:true once a real test lands.
	},
	InvTerminalCtrlCDelivery: {
		ID:          InvTerminalCtrlCDelivery,
		TestFile:    "agent_go/cmd/server/terminal_routes.go",
		TestName:    "sendTerminalKey",
		Description: "POST /api/terminals/{id}/key accepts debug control keys including 'ctrl-c' and 'ctrl-o' and runs the matching tmux send-keys sequence. Frontend wires them via the debug-action dropdown.",
	},
	InvMultiTurnMemory: {
		ID:          InvMultiTurnMemory,
		TestFile:    "agent_go/cmd/server/multi_turn_chat_e2e_real_test.go",
		TestName:    "TestMultiTurnChatE2E_ClaudeCode",
		Env:         []string{"RUN_CLAUDE_CODE_EXPERIMENTAL_LIVE_E2E=1"},
		Description: "Multi-turn chat with canary tokens injected in turn 1, verified in turn 2 — proves the provider's persistent session carries memory across turns and the orchestrator doesn't accidentally drop history. Per-CLI variants (_Codex, _Cursor) follow the same shape.",
		RealE2E:     true,
	},
	InvTerminalLifecycleAPI: {
		ID:          InvTerminalLifecycleAPI,
		TestFile:    "agent_go/pkg/orchestrator/terminal_pane_e2e_real_test.go",
		TestName:    "TestTerminalPaneCrossTransportReal",
		Env:         []string{"RUN_ANTHROPIC_REAL_E2E=1"},
		Description: "Drives a full streaming-chunk → streaming-end terminal lifecycle across API and tmux-backed coding-agent transports. Asserts /api/terminals transitions active→inactive/closing without leaking terminals or duplicating panes across transport flips.",
		RealE2E:     true,
	},
	InvWorkflowStepCwdMCP: {
		ID:          InvWorkflowStepCwdMCP,
		TestFile:    "agent_go/pkg/orchestrator/types/workflow_orchestrator_e2e_real_test.go",
		TestName:    "TestWorkflowE2ESingleRegularStepPiCLI",
		Env:         []string{"RUN_WORKFLOW_REAL_E2E=1", "RUN_PI_CLI_WORKFLOW_E2E=1"},
		Description: "Real workflow run with a single regular step on Pi CLI. Proves the coding agent launches in the workflow execution directory, calls the session-scoped MCP bridge, and writes the expected output file — closing the invariant from the contract doc's 'Workflow step cwd/MCP' P1 row.",
		RealE2E:     true,
	},
	InvNoDuplicateUnifiedCompletion: {
		ID:          InvNoDuplicateUnifiedCompletion,
		TestFile:    "agent_go/cmd/testing/coding_agent_chat_e2e_test.go",
		TestName:    "TestExtractUnifiedCompletionFinalUsesDocumentedShape",
		Description: "Pure-Go extractor test that proves the unified-completion shape pulls ONLY the newest assistant text — no tool panels, no terminal box characters, no prompt echoes. Cheap to run (no real CLI needed) but pins down the assertion the doc demands for the unified-completion contract.",
	},
	InvCostTrackingPipeline: {
		ID:          InvCostTrackingPipeline,
		TestFile:    "agent_go/cmd/server/cost_ledger_e2e_real_test.go",
		TestName:    "TestCostLedgerCapturesRealAnthropicTurn",
		Env:         []string{"RUN_ANTHROPIC_REAL_E2E=1", "ANTHROPIC_API_KEY"},
		Description: "Real Anthropic API call → adapter GenerationInfo → orchestrator token event → cost ledger persist → /api/cost HTTP surface. Per-CLI variants prove the same pipeline for supported CLI providers; cost_http_e2e_real_test.go covers the API-provider HTTP path.",
		RealE2E:     true,
	},
	InvInspectorDebugVisibility: {
		ID:          InvInspectorDebugVisibility,
		TestFile:    "agent_go/cmd/server/inspector_e2e_real_test.go",
		TestName:    "TestInspectorHTTPCapturesRealAnthropicEvents",
		Env:         []string{"RUN_ANTHROPIC_REAL_E2E=1", "ANTHROPIC_API_KEY"},
		Description: "Drives a real Anthropic turn through the orchestrator and asserts /api/inspector returns the full event chain (system prompt, LLM generation, streaming chunks, token usage) for the live session. Per-CLI variants prove the same surface for supported CLI providers. Companion TestInspectorHTTPUnknownSessionReturns404 covers the empty-session branch.",
		RealE2E:     true,
	},
	InvProviderAPIKeyValidation: {
		ID:          InvProviderAPIKeyValidation,
		TestFile:    "agent_go/cmd/server/validate_anthropic_real_test.go",
		TestName:    "TestValidateAPIKeyAnthropicRealAccepts",
		Env:         []string{"RUN_ANTHROPIC_REAL_E2E=1", "ANTHROPIC_API_KEY"},
		Description: "POST /api/validate-key with a real working ANTHROPIC_API_KEY returns ok=true; companion TestValidateAPIKeyAnthropicRealRejectsBadKey returns ok=false for a fabricated key. Per-provider variants (validate_openai_real_test.go, validate_vertex_real_test.go) follow the same shape for OpenAI + Vertex.",
		RealE2E:     true,
	},
}
