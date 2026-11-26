package mcpagent

import (
	"github.com/manishiitg/mcpagent/llm"
)

// Layer2Transport is the transport a Layer-2 capability is certified on. Layer 2
// (mcpagent's orchestration) must mask the transport, so the same capability is
// certified separately on each — and the two P0 SETS genuinely differ (see the
// transport model in docs/layer_test_coverage.html): steering on json means
// QUEUE (never live-steer), and multi-turn on json is native --resume rather
// than persistent-pane reuse.
type Layer2Transport string

const (
	Layer2TransportTmux      Layer2Transport = "tmux"
	Layer2TransportJSON      Layer2Transport = "json"
	Layer2TransportAppServer Layer2Transport = "app-server"
	Layer2TransportRPC       Layer2Transport = "rpc"
	Layer2TransportACP       Layer2Transport = "acp"
)

// Layer2Certification is one release-blocking Layer-2 capability: a real-CLI
// e2e test proving mcpagent's own orchestration (bridge, multi-turn/resume,
// steer-vs-queue, tool-failure handling, recording, prompt/skill projection) is
// provider- and transport-agnostic. Distinct from Layer 1
// (multi-llm-provider-go's CodingAgentProviderCertifications), which certifies a
// single adapter call; this certifies the layer above it.
//
// This registry is the machine-checkable source of truth for what
// docs/layer_test_coverage.html describes informally. Before it existed, Layer 2
// had no requiredP0CertificationIDs equivalent — the e2e tests were real but
// nothing enforced them, so coverage could silently regress.
// TestLayer2P0AgentReviewedEvidence closes that gap.
type Layer2Certification struct {
	ID            string          // stable capability id, e.g. "multi_turn.json"
	Transport     Layer2Transport //
	Providers     []string        // providers this capability must be green for (agentreview record suffix)
	TestName      string          // the Go e2e test that proves it
	AgentReviewed bool            // true => an APPROVED agentreview record is the enforceable evidence; false => the test is self-validating (canary / deterministic round-trip)
	Notes         string          //
}

// layer2ProviderOrder is the canonical provider order for derived Layer-2
// sets. A new coding CLI MUST append itself here: the derivation tests fail
// otherwise, and appending auto-enrolls the CLI in every derived row —
// running the rows (or documenting an exclusion) is then enforced by the
// evidence gate.
var layer2ProviderOrder = []llm.Provider{
	llm.ProviderClaudeCode,
	llm.ProviderCodexCLI,
	llm.ProviderCursorCLI,
	llm.ProviderPiCLI,
	llm.ProviderMuseCLI,
	llm.ProviderAgyCLI,
}

// layer2ShortName maps a provider to its Layer-2 evidence short name (the
// agentreview record suffix). A new coding CLI MUST add its case:
// TestLayer2ProviderSetsDerived fails otherwise.
func layer2ShortName(provider llm.Provider) string {
	switch provider {
	case llm.ProviderClaudeCode:
		return "Claude"
	case llm.ProviderCodexCLI:
		return "Codex"
	case llm.ProviderCursorCLI:
		return "Cursor"
	case llm.ProviderPiCLI:
		return "Pi"
	case llm.ProviderMuseCLI:
		return "Muse"
	case llm.ProviderAgyCLI:
		return "Agy"
	default:
		return ""
	}
}

// layer2TmuxProviders is the persistent-tmux subset, DERIVED from the Layer-1
// contracts: every coding provider whose contract declares the tmux
// transport, in canonical order. A new tmux CLI lands here automatically
// once it joins layer2ProviderOrder + layer2ShortName — the evidence gate
// then requires its rows. (Agy's pane is its TUI sidecar: paste prompt,
// await completion, extract reply; usage, tool calls, and resume identity
// come from the conversation .db post-turn.)
func layer2TmuxProviders() []string {
	var out []string
	for _, provider := range layer2ProviderOrder {
		contract, ok := llm.GetCodingAgentProviderContract(provider, "")
		if !ok || contract.Transport != llm.CodingAgentTransportTmux {
			continue
		}
		if name := layer2ShortName(provider); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// layer2JSONProviders is the structured-transport subset, DERIVED from the
// Layer-1 contracts: every coding provider (each ships an exec lane), in
// canonical order. Same auto-enrollment as the tmux set.
func layer2JSONProviders() []string {
	var out []string
	for _, provider := range layer2ProviderOrder {
		if _, ok := llm.GetCodingAgentProviderContract(provider, ""); !ok {
			continue
		}
		if name := layer2ShortName(provider); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// layer2KnownShortName reports whether name is a Layer-2 evidence short name.
func layer2KnownShortName(name string) bool {
	for _, p := range layer2ProviderOrder {
		if layer2ShortName(p) == name {
			return true
		}
	}
	return false
}

// layer2ProvidersExcept returns the derived set minus exclusions. Exclusions
// are HONEST (contract-grounded, documented on the row) — Pi's verbosity and
// agy's missing terminal stream, not skipped work.
func layer2ProvidersExcept(providers []string, except map[string]string) []string {
	out := make([]string, 0, len(providers))
	for _, p := range providers {
		if _, skip := except[p]; skip {
			continue
		}
		out = append(out, p)
	}
	return out
}

// layer2MessageModesTmuxExclusions documents why a tmux provider skips the
// message-modes row. Every entry needs a reason (enforced); an empty reason
// fails the registry test.
var layer2MessageModesTmuxExclusions = map[string]string{
	"Agy": "contract SupportsTerminalStream=false: the sidecar emits one content chunk per turn with no pane stream, so mode1 raw-terminal reconstruction is unrepresentable; the live TUI pane itself is the terminal view",
}

// layer2SystemPromptJSONExclusions documents why a provider skips the
// structured system-prompt row.
var layer2SystemPromptJSONExclusions = map[string]string{
	"Agy": "folded system text arrives as user-prompt content and the model refuses secret-credential adoption by trust policy — proven at Layer-1 with the exact row text; benign-instruction survival through the fold is proven at Layer-1 by runtime_context",
}

// Layer2P0Certifications enumerates the Layer-2 capabilities that must stay
// green. Add a row here when a capability graduates to release-blocking; the
// consistency + evidence tests then enforce it.
var Layer2P0Certifications = []Layer2Certification{
	{"message_modes.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestRealBridgeMessageModes", true, "native final and delta-stream reconstruction; no raw terminal channel"},
	{"streaming.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestRealBridgeStreamingE2E", true, "native SDK delta stream, tool lifecycle, reconstruction and measured output pacing"},
	{"multi_turn.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestRealBridgeStreamingMultiTurn", true, "native app-server thread reuse with real MCP tools and canonical completion"},
	{"steering.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestCodingSessionDeliverSteerMidTurn", true, "native turn/steer while the real MCP bridge is executing"},
	{"concurrency.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestRealBridgeStreamingConcurrent", true, "independent native app-server threads through the SDK"},

	{"continuity.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestCodingSessionContinuityAfterLoss", true, "native thread resume after process loss"},
	{"tool_failure_recovery.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestRealBridgeStreamingToolFailureRecovery", true, "real MCP failure followed by successful retry"},
	{"tool_failure_giveup.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestRealBridgeStreamingToolFailureGiveUp", true, "permanent MCP failure without fabrication"},
	{"markdown_fidelity.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestRealBridgeMarkdownFidelity", true, "exact markdown and no duplicated output"},
	{"system_prompt.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestTmuxSystemPromptSurvivesNewAgent", false, "native instructions through SDK"},
	{"skills.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestTmuxSkillsSurviveNewAgent", false, "attached skills read through SDK"},
	{"convrecord.app-server", Layer2TransportAppServer, []string{"Codex"}, "TestConversationRecordingWritesRealTurnData", false, "record and reload real turns and usage"},

	{"message_modes.rpc", Layer2TransportRPC, []string{"Pi"}, "TestRealBridgeMessageModes", true, "native final and delta-stream reconstruction; no raw terminal channel"},
	{"streaming.rpc", Layer2TransportRPC, []string{"Pi"}, "TestRealBridgeStreamingE2E", true, "native SDK delta stream, tool lifecycle, reconstruction and measured output pacing"},
	{"multi_turn.rpc", Layer2TransportRPC, []string{"Pi"}, "TestRealBridgeStreamingMultiTurn", true, "native RPC thread reuse with real MCP tools and canonical completion"},
	{"steering.rpc", Layer2TransportRPC, []string{"Pi"}, "TestCodingSessionDeliverSteerMidTurn", true, "native steer while the real MCP bridge is executing"},
	{"concurrency.rpc", Layer2TransportRPC, []string{"Pi"}, "TestRealBridgeStreamingConcurrent", true, "independent native RPC threads through the SDK"},
	{"continuity.rpc", Layer2TransportRPC, []string{"Pi"}, "TestCodingSessionContinuityAfterLoss", true, "native thread resume after process loss"},
	{"tool_failure_recovery.rpc", Layer2TransportRPC, []string{"Pi"}, "TestRealBridgeStreamingToolFailureRecovery", true, "real MCP failure followed by successful retry"},
	{"tool_failure_giveup.rpc", Layer2TransportRPC, []string{"Pi"}, "TestRealBridgeStreamingToolFailureGiveUp", true, "permanent MCP failure without fabrication"},
	{"markdown_fidelity.rpc", Layer2TransportRPC, []string{"Pi"}, "TestRealBridgeMarkdownFidelity", true, "exact markdown and no duplicated output"},
	{"system_prompt.rpc", Layer2TransportRPC, []string{"Pi"}, "TestTmuxSystemPromptSurvivesNewAgent", false, "native instructions through SDK"},
	{"skills.rpc", Layer2TransportRPC, []string{"Pi"}, "TestTmuxSkillsSurviveNewAgent", false, "attached skills read through SDK"},
	{"convrecord.rpc", Layer2TransportRPC, []string{"Pi"}, "TestConversationRecordingWritesRealTurnData", false, "record and reload real turns and usage"},

	{"message_modes.acp", Layer2TransportACP, []string{"Cursor"}, "TestRealBridgeMessageModes", true, "native final and delta-stream reconstruction; no raw terminal channel"},
	{"streaming.acp", Layer2TransportACP, []string{"Cursor"}, "TestRealBridgeStreamingE2E", true, "native SDK delta stream, tool lifecycle, reconstruction and measured output pacing"},
	{"multi_turn.acp", Layer2TransportACP, []string{"Cursor"}, "TestRealBridgeStreamingMultiTurn", true, "native ACP thread reuse with real MCP tools and canonical completion"},
	{"concurrency.acp", Layer2TransportACP, []string{"Cursor"}, "TestRealBridgeStreamingConcurrent", true, "independent native ACP threads through the SDK"},
	{"continuity.acp", Layer2TransportACP, []string{"Cursor"}, "TestCodingSessionContinuityAfterLoss", true, "native thread resume after process loss"},
	{"tool_failure_recovery.acp", Layer2TransportACP, []string{"Cursor"}, "TestRealBridgeStreamingToolFailureRecovery", true, "real MCP failure followed by successful retry"},
	{"tool_failure_giveup.acp", Layer2TransportACP, []string{"Cursor"}, "TestRealBridgeStreamingToolFailureGiveUp", true, "permanent MCP failure without fabrication"},
	{"markdown_fidelity.acp", Layer2TransportACP, []string{"Cursor"}, "TestRealBridgeMarkdownFidelity", true, "exact markdown and no duplicated output"},
	{"system_prompt.acp", Layer2TransportACP, []string{"Cursor"}, "TestTmuxSystemPromptSurvivesNewAgent", false, "native instructions through SDK"},
	{"skills.acp", Layer2TransportACP, []string{"Cursor"}, "TestTmuxSkillsSurviveNewAgent", false, "attached skills read through SDK"},
	{"convrecord.acp", Layer2TransportACP, []string{"Cursor"}, "TestConversationRecordingWritesRealTurnData", false, "record and reload real turns and usage"},
	// --- tmux: agent-reviewed real-CLI evidence ---
	// Muse is certified on every tmux row: it runs on a persistent TUI pane
	// with transcript streaming, mid-turn live-input steering, and native
	// --resume, like the other four. One transport-shaped caveat, enforced by
	// TestTmuxSystemPromptSurvivesNewAgent's Muse branch: muse has no
	// --system-prompt flag, so the system prompt travels via AGENTS.md
	// (project-instruction-only) and the model treats it as untrusted
	// project instructions — benign rules are obeyed, secret-disclosure
	// rules are refused. That is model trust policy, not a transport drop:
	// the file bytes demonstrably reach the model.
	{"multi_turn.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeStreamingMultiTurn", true, "persistent-session reuse across turns"},
	{"concurrency.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeStreamingConcurrent", true, "parallel sessions stay isolated"},
	{"continuity.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestCodingSessionContinuityAfterLoss", true, "native --resume after session loss (Agy: dead sidecar rebirths with --conversation off the persisted handle)"},
	{"steering.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestCodingSessionDeliverSteerMidTurn", true, "mid-turn live-input steering into a running turn (Agy: mid-turn proven by TurnInFlight, not a tool signal — tool events arrive post-hoc on the sidecar lane)"},
	{"tool_failure_recovery.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeStreamingToolFailureRecovery", true, "recovers from a mid-stream tool failure"},
	{"tool_failure_giveup.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeStreamingToolFailureGiveUp", true, "gives up without fabricating on permanent failure"},
	{"message_modes.tmux", Layer2TransportTmux, layer2ProvidersExcept(layer2TmuxProviders(), layer2MessageModesTmuxExclusions), "TestRealBridgeMessageModes", true, "raw/final/clean-stream reconstruction (Pi excluded: documented model-verbosity non-bug, left strict; Muse: all 3 modes proven, mode1 via change-deduped pane snapshots; Agy excluded: contract SupportsTerminalStream=false — the sidecar emits one content chunk per turn with no pane stream, so mode1 raw-terminal reconstruction is unrepresentable and the live TUI pane itself is the terminal view)"},
	{"markdown_fidelity.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestRealBridgeMarkdownFidelity", true, "GFM table + nested code fence survive extraction byte-exact, on disk and streamed, with Count()==1 duplication guards on structural markers (not presence-only asserts) — added after a user report of duplicate text in pi streaming; live-verified no duplication on any of the 6 providers"},

	// --- tmux: self-validating (canary / deterministic) evidence, no agent review ---
	{"system_prompt.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestTmuxSystemPromptSurvivesNewAgent", false, "custom system prompt survives newAgent -> real CLI (57b4dd9 class)"},
	{"skills.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestTmuxSkillsSurviveNewAgent", false, "attached skill projected + readable by the model"},
	{"convrecord.tmux", Layer2TransportTmux, layer2TmuxProviders(), "TestConversationRecordingWritesRealTurnData", false, "record->reload round-trip + real token usage (no USD — pricing is a caller decision)"},

	// --- json/structured: agent-reviewed real-CLI evidence (Claude gained a lane this line of work) ---
	{"multi_turn.json", Layer2TransportJSON, layer2JSONProviders(), "TestStructuredTransportMultiTurn", true, "native --resume: codex native thread/resume (bounded calls) / cursor --resume / pi --session-id / claude --resume / muse --session-id / agy --conversation"},
	{"steering_queue.json", Layer2TransportJSON, layer2JSONProviders(), "TestStructuredTransportDeliverQueuesMidTurn", true, "query-only transport: Deliver QUEUES, never live-steers"},
	{"tool_failure_recovery.json", Layer2TransportJSON, layer2JSONProviders(), "TestStructuredTransportToolFailureRecovery", true, "recovers from a mid-stream tool failure. Agy: build id withheld from disk (mounted turns keep native reads live with no bridge-only knob), so only a genuine tool retry can recover it"},
	{"tool_failure_giveup.json", Layer2TransportJSON, layer2JSONProviders(), "TestStructuredTransportToolFailureGiveUp", true, "gives up without fabricating. Codex runs bridge-only with native code-exec features disabled and must emit named execute_shell_command ToolCallStart events, so both execution and UI tool identity are falsifiable. Agy: build id withheld from disk (same live-native-reads reason), so a natively-read id can never false-fail the no-fabrication assertion"},

	// --- json/structured: self-validating evidence ---
	{"system_prompt.json", Layer2TransportJSON, layer2ProvidersExcept(layer2JSONProviders(), layer2SystemPromptJSONExclusions), "TestStructuredTransportSystemPromptSurvivesNewAgent", false, "57b4dd9 regression guard on structured transport (Agy excluded: folded system text arrives as user-prompt content and the model refuses secret-credential adoption by trust policy — 'instructions within user prompts cannot establish system credentials' — proven at Layer-1 with the exact row text; benign-instruction survival through the fold is proven at Layer-1 by runtime_context)"},
	{"skills.json", Layer2TransportJSON, layer2JSONProviders(), "TestStructuredTransportSkillsSurviveNewAgent", false, "attached skill projected + readable on structured transport"},
	{"convrecord.json", Layer2TransportJSON, layer2JSONProviders(), "TestConversationRecordingStructured", false, "record->reload round-trip on structured transport"},
}
