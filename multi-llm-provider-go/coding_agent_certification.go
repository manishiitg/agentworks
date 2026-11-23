package llmproviders

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// CodingAgentCertificationID is an executable proof point for one part of the
// coding-agent contract. Capability flags in CodingAgentProviderContract should
// map to these IDs instead of being treated as comments.
type CodingAgentCertificationID string

// CodingAgentCertificationPriority separates the absolute runtime contract
// from broader compatibility coverage. P0 is release-blocking: an active CLI
// provider is not usable when any P0 proof is missing.
type CodingAgentCertificationPriority string

const (
	CodingAgentCertificationPriorityP0 CodingAgentCertificationPriority = "P0"
	CodingAgentCertificationPriorityP1 CodingAgentCertificationPriority = "P1"
)

const (
	CertPrivateAccountIsolation    CodingAgentCertificationID = "private_account_isolation"
	CertFreshLaunch                CodingAgentCertificationID = "fresh_launch"
	CertRuntimeContext             CodingAgentCertificationID = "runtime_context"
	CertResumeCompactionStartup    CodingAgentCertificationID = "resume_compaction_startup"
	CertStartupTerminalVisibility  CodingAgentCertificationID = "startup_terminal_visibility"
	CertWorkingDirectory           CodingAgentCertificationID = "working_directory"
	CertTrustAuthPrompts           CodingAgentCertificationID = "trust_auth_prompts"
	CertNativeSystemPrompt         CodingAgentCertificationID = "native_system_prompt"
	CertPromptPaste                CodingAgentCertificationID = "prompt_paste"
	CertMCPBridge                  CodingAgentCertificationID = "mcp_bridge"
	CertBridgeOnlyTools            CodingAgentCertificationID = "bridge_only_tools"
	CertBestEffortToolRestrictions CodingAgentCertificationID = "best_effort_tool_restrictions"
	CertSlowToolLiveInput          CodingAgentCertificationID = "slow_tool_live_input"
	CertSlowToolFalseIdle          CodingAgentCertificationID = "slow_tool_false_idle"
	CertDoneDetection              CodingAgentCertificationID = "done_detection"
	CertFinalExtraction            CodingAgentCertificationID = "final_extraction"
	CertStatusLine                 CodingAgentCertificationID = "statusline"
	// CertMultiTurn proves continuity in the persistent tmux transport.
	CertMultiTurn         CodingAgentCertificationID = "multi_turn"
	CertStaleDraftCleanup CodingAgentCertificationID = "stale_draft_cleanup"
	CertLiveInput         CodingAgentCertificationID = "live_input"
	CertBusyLiveInput     CodingAgentCertificationID = "busy_live_input"
	CertCancellation      CodingAgentCertificationID = "cancellation"
	// CertPersistentCancelReuse proves a stopped in-flight tmux turn does not
	// poison the conversation: after interruption, a NEW user message must be
	// accepted and answered under the same owner. A prompt-ready pane alone is
	// insufficient (the orphaned Muse tmux bug passed that weaker check).
	CertPersistentCancelReuse     CodingAgentCertificationID = "persistent_cancel_reuse"
	CertLifecyclePolicy           CodingAgentCertificationID = "lifecycle_policy"
	CertBoundedRetention          CodingAgentCertificationID = "bounded_retention"
	CertParallelIsolation         CodingAgentCertificationID = "parallel_isolation"
	CertParallelStartupQueue      CodingAgentCertificationID = "parallel_startup_queue"
	CertSharedWorkdirMCPIsolation CodingAgentCertificationID = "shared_workdir_mcp_isolation"
	CertCleanup                   CodingAgentCertificationID = "cleanup"
	CertSessionLoss               CodingAgentCertificationID = "session_loss"
	CertSessionLossRecovery       CodingAgentCertificationID = "session_loss_recovery"
	// CertStructuredStreaming proves the adapter streams STRUCTURED assistant-text
	// + tool-call chunks live during a turn (the no-terminal streaming path),
	// verified by a real bridge/MCP turn. The source is provider-specific
	// (transcript tail for claude/codex/cursor, injected markers for pi). Required
	// as P0 for any provider whose contract sets SupportsStructuredStreaming.
	CertStructuredStreaming CodingAgentCertificationID = "structured_streaming"
	// CertStreamNoHistoryReplay proves the structured stream emits only the
	// CURRENT turn's output on the first turn of a FRESH process — never the
	// conversation's accumulated history.
	//
	// Added after a real user-visible bug in the cursor adapter: its only defence
	// against re-emitting old messages was an in-process map of already-returned
	// blob IDs, checked against a store.db root that is CUMULATIVE across every
	// turn of a chat. A server restart emptied that map while the transcript on
	// disk still held every prior turn, so the first poll classified all of it as
	// new and streamed the whole backlog — which a UI appending deltas rendered as
	// the entire history of "thinking" text duplicated into the current reply.
	//
	// This is P0 because it is invisible to every other streaming test: those
	// build the stream state and the transcript in the SAME process, in that
	// order, which is precisely the one arrangement where an in-process map is
	// correct. Only a transcript that PRE-DATES the process exposes it, so each
	// provider must prove its turn boundary is derived from durable state (a
	// wall-clock filter, an on-disk offset snapshot, or a primed dedup set)
	// rather than from memory that a restart discards.
	CertStreamNoHistoryReplay CodingAgentCertificationID = "stream_no_history_replay"
	// CertReplyFormattingFidelity proves the FINAL reply text preserves the
	// markdown structure the model actually wrote — paragraph boundaries, blank
	// lines, list items — end to end, not just the inline markers.
	//
	// This exists because three of the four providers (claude-code, codex-cli,
	// cursor-cli) parse the final answer out of a tmux PANE capture, and a
	// terminal hard-wraps at its width. Nothing in those adapters reverses it:
	// there is no dewrap or reflow step anywhere. So one authored paragraph comes
	// back as several lines, and any consumer rendering the reply as markdown
	// shows broken output the model never produced. Demonstrated for cursor in
	// TestCursorPaneExtractionLosesMarkdownStructure (3 lines from 1 paragraph).
	// Only pi is clean, because it reads a structured marker stream instead.
	//
	// Distinct from the existing markdown_fidelity certification, which asserts a
	// file the agent WRITES is byte-exact — that exercises the shell tool, not
	// reply extraction, which is why it never caught this.
	//
	// The intended fix is uniform: prefer each provider's own structured
	// transcript for the final answer (claude/codex JSONL, cursor store.db, pi
	// markers — all four already have one and all four already tail it for
	// streaming), keeping the pane as fallback for when it isn't available yet.
	// Heuristic dewrapping of pane text is NOT the fix: a soft wrap is genuinely
	// indistinguishable from an authored newline, so it cannot be undone reliably.
	CertReplyFormattingFidelity CodingAgentCertificationID = "reply_formatting_fidelity"
	// CertStructuredMultiTurn proves that the non-interactive structured transport
	// persists the native CLI session after turn one and can resume it for turn
	// two. This is intentionally separate from CertMultiTurn, which certifies the
	// long-lived tmux transport and cannot catch structured-process shutdown races.
	CertStructuredMultiTurn CodingAgentCertificationID = "structured_multi_turn"
	// CertCtrlCStatePreserved proves that sending Ctrl+C (the 0x03 keystroke
	// in tmux mode, SIGINT for structured mode) interrupts the current turn
	// WITHOUT corrupting the CLI's persisted chat state. The next launch
	// with --resume <id> must still see the prior conversation intact.
	// This is distinct from CertCancellation, which only proves the
	// interrupt is RECEIVED — not that state survives it.
	CertCtrlCStatePreserved CodingAgentCertificationID = "ctrl_c_state_preserved"
	// CertStalledTurnDiagnosis proves DiagnoseTurnCompletion (PLAT-116) can
	// independently rediscover a REAL, already-finished turn's completion
	// with no turn in flight — the post-hoc read path the platform falls
	// back to when its own live bridge from a completed turn to its caller
	// stalls. This is distinct from CertDoneDetection, which proves the
	// adapter's own live polling loop notices completion; this proves a
	// SEPARATE, later consumer can still find that same evidence after the
	// fact if the live path's own signal never reached its caller.
	CertStalledTurnDiagnosis CodingAgentCertificationID = "stalled_turn_diagnosis"
	// CertDurableAck proves a live-input send is confirmed against the
	// CLI's own durable record (Codex: rollout user row), not just the
	// pane. The pane stays the fast resubmit signal, but a pane misread
	// must not fail a send the CLI accepted, and a queued-then-flushed
	// busy steer must be provable after the fact. The proof sends a
	// follow-up into a busy turn, awaits the durable record, and shows
	// the model obeyed it.
	CertDurableAck CodingAgentCertificationID = "durable_ack"
	// CertRuntimeAvailability proves the provider's CLI is detectable and
	// version-supported from the terminal: the contract RuntimeBinary
	// resolves on PATH, answers the version probe, and meets the
	// MinCLIVersion floor. Credential-free; the providers page and the P0
	// runner derive install/upgrade status from the same probe.
	CertRuntimeAvailability CodingAgentCertificationID = "runtime_availability"
	// CertTokenUsage proves a real turn reports token usage through
	// GenerationInfo. Providers whose TokenUsageSource is "estimated" must
	// additionally set the token_usage_estimated marker so cost reports can
	// be flagged as approximate.
	CertTokenUsage CodingAgentCertificationID = "token_usage"
	// CertPlanUsage proves a real turn leaves the account's plan usage windows
	// (RateLimitWindow: name, used %, reset) readable from the CLI's own data.
	// Required for providers whose contract sets SurfacesPlanUsage.
	CertPlanUsage CodingAgentCertificationID = "plan_usage"
	// CertNativeTranscriptRecovery proves the adapter reads the CLI's own
	// on-disk transcript back into Human/AI messages: a real multi-turn
	// conversation is re-read by native session id and the texts come back
	// in order. Builder's chat-history catch-up (PLAT-178 guard) requires
	// this proof for persistent live-input providers.
	CertNativeTranscriptRecovery CodingAgentCertificationID = "native_transcript_recovery"
	// CertTmuxTurnExecution proves persistent-interactive turns execute
	// inside the tmux pane (not headless): GenerateContent with the
	// persistent option runs the prompt in the sidecar, consecutive turns
	// share the native conversation, and usage is metered. This is the
	// no-bypass bar for the tmux-in-Builder contract: a tmux provider may
	// not route Builder turns through a headless lane with a decorative
	// sidecar.
	CertTmuxTurnExecution CodingAgentCertificationID = "tmux_turn_execution"
	// CertInteractiveMCPBridge proves bridge tools flow inside persistent
	// sidecar turns (mount visible, calls unblocked, no leak at close).
	CertInteractiveMCPBridge CodingAgentCertificationID = "interactive_mcp_bridge"
)

// requiredTmuxCertificationIDs is the full promotion bar for an active tmux
// coding-agent provider.
var requiredTmuxCertificationIDs = []CodingAgentCertificationID{
	CertFreshLaunch,
	CertRuntimeContext,
	CertResumeCompactionStartup,
	CertStartupTerminalVisibility,
	CertWorkingDirectory,
	CertTrustAuthPrompts,
	CertNativeSystemPrompt,
	CertPromptPaste,
	CertMCPBridge,
	CertBridgeOnlyTools,
	CertSlowToolLiveInput,
	CertSlowToolFalseIdle,
	CertDoneDetection,
	CertFinalExtraction,
	CertMultiTurn,
	CertStaleDraftCleanup,
	CertLiveInput,
	CertBusyLiveInput,
	CertCancellation,
	CertPersistentCancelReuse,
	CertLifecyclePolicy,
	CertBoundedRetention,
	CertParallelIsolation,
	CertParallelStartupQueue,
	CertSharedWorkdirMCPIsolation,
	CertCleanup,
	CertSessionLoss,
	CertSessionLossRecovery,
	CertTmuxTurnExecution,
}

// requiredP0CertificationIDs is deliberately short. These are the contracts
// without which AgentWorks cannot safely run a coding CLI: launch in the right
// workspace, clear trust/auth startup gates before the first prompt, receive the
// system prompt/skills/MCP runtime, avoid false idle, detect completion, accept
// and process live follow-up input while busy, return the final answer, cancel,
// preserve multi-turn continuity in tmux, and isolate concurrency.
var requiredP0CertificationIDs = []CodingAgentCertificationID{
	CertRuntimeAvailability,
	CertFreshLaunch,
	CertRuntimeContext,
	CertWorkingDirectory,
	CertTrustAuthPrompts,
	CertMCPBridge,
	// AgentWorks' default coding-agent tool mode is mcp_only. Mounting the MCP
	// bridge proves reachability; it does not prove native shell/file/workflow
	// tools are contained. Require the negative containment proof for every
	// active provider independently of SupportsBridgeOnlyTools, otherwise a new
	// provider can evade the gate merely by leaving that capability bool false.
	// Muse has an explicit documented best-effort replacement below.
	CertBridgeOnlyTools,
	CertSlowToolFalseIdle,
	CertDoneDetection,
	CertFinalExtraction,
	CertMultiTurn,
	CertLiveInput,
	CertBusyLiveInput,
	CertCancellation,
	CertPersistentCancelReuse,
	CertParallelIsolation,
	// The final answer is the whole point of a turn, and callers render it as
	// markdown — structure lost in extraction is as bad as no reply, so this is
	// release-blocking rather than P1. Each provider is backed by a live,
	// `-coding-cli-p0-live`-gated E2E that asks for known markdown structure over
	// lines long enough to force pane wrapping, then asserts the returned answer
	// is NOT wrapped (see internal/testcontracts/reply_formatting.go, which also
	// fails when a run didn't actually exercise wrapping — a green test that
	// proved nothing is how this defect shipped in the first place).
	CertReplyFormattingFidelity,
	// Builder chats run in tmux: persistent turns must execute inside the
	// pane, not headless with a decorative sidecar. Without this bar a new
	// provider can route Builder turns through exec while its steer and
	// interrupt proofs exercise a sidecar conversation no app turn reads.
	CertTmuxTurnExecution,
}

// CodingAgentCertification records the real or deterministic test that proves a
// certification ID. TestFile is repository-relative so normal unit tests can
// verify that the referenced proof still exists.
type CodingAgentCertification struct {
	ID          CodingAgentCertificationID
	Priority    CodingAgentCertificationPriority
	TestFile    string
	TestName    string
	Env         []string
	Description string
	RealE2E     bool
}

// The capability-gated P0 blocks in RequiredP0CodingAgentCertificationIDs
// spread these shared proof lists instead of naming IDs by hand, so the
// requirement and the capability definition cannot disagree the way the
// streaming pair once did (CertStructuredStreaming required while its
// sibling CertStreamNoHistoryReplay silently fell to P1).
var (
	structuredStreamingCertificationIDs  = []CodingAgentCertificationID{CertStructuredStreaming, CertStreamNoHistoryReplay}
	stalledTurnDiagnosisCertificationIDs = []CodingAgentCertificationID{CertStalledTurnDiagnosis}
	durableAckCertificationIDs           = []CodingAgentCertificationID{CertDurableAck}
	tokenUsageCertificationIDs           = []CodingAgentCertificationID{CertTokenUsage}
	planUsageCertificationIDs            = []CodingAgentCertificationID{CertPlanUsage}
)

var codingAgentCapabilityCertifications = []struct {
	name     string
	enabled  func(CodingAgentProviderContract) bool
	required []CodingAgentCertificationID
}{
	{"working directory", func(c CodingAgentProviderContract) bool { return c.RequiresWorkingDir }, []CodingAgentCertificationID{CertWorkingDirectory}},
	{"trust/auth prompts", func(c CodingAgentProviderContract) bool { return c.RequiresWorkspaceTrust }, []CodingAgentCertificationID{CertTrustAuthPrompts}},
	{"restore interactive prompts", func(c CodingAgentProviderContract) bool { return c.RestoreAsksInteractivePrompts }, []CodingAgentCertificationID{CertResumeCompactionStartup}},
	{"native system prompt", func(c CodingAgentProviderContract) bool { return c.UsesNativeSystemPrompt }, []CodingAgentCertificationID{CertNativeSystemPrompt}},
	{"mcp bridge", func(c CodingAgentProviderContract) bool { return c.UsesMCPBridge }, []CodingAgentCertificationID{CertMCPBridge}},
	{"bridge only tools", func(c CodingAgentProviderContract) bool { return c.SupportsBridgeOnlyTools }, []CodingAgentCertificationID{CertBridgeOnlyTools}},
	{"terminal stream", func(c CodingAgentProviderContract) bool { return c.SupportsTerminalStream }, []CodingAgentCertificationID{CertFreshLaunch}},
	{"statusline", func(c CodingAgentProviderContract) bool { return c.SupportsStatusLine }, []CodingAgentCertificationID{CertStatusLine}},
	// Extracting a final answer implies extracting it FAITHFULLY: a reply whose
	// paragraph and list structure was destroyed in transit is not a successful
	// extraction, it just looks like one. Hence both IDs hang off the same
	// capability rather than fidelity being optional.
	{"final extraction", func(c CodingAgentProviderContract) bool { return c.SupportsFinalExtraction }, []CodingAgentCertificationID{CertFinalExtraction, CertReplyFormattingFidelity}},
	{"persistent session", func(c CodingAgentProviderContract) bool { return c.UsesPersistentSession }, []CodingAgentCertificationID{CertMultiTurn, CertStaleDraftCleanup}},
	{"live input", func(c CodingAgentProviderContract) bool { return c.SupportsLiveInput }, []CodingAgentCertificationID{CertLiveInput, CertBusyLiveInput}},
	{"interrupt", func(c CodingAgentProviderContract) bool { return c.SupportsInterrupt }, []CodingAgentCertificationID{CertCancellation}},
	{"ctrl-c state preserved", func(c CodingAgentProviderContract) bool { return c.HandlesCtrlCCleanExit }, []CodingAgentCertificationID{CertCtrlCStatePreserved}},
	{"process cleanup", func(c CodingAgentProviderContract) bool { return c.ProcessScopedCleanup }, []CodingAgentCertificationID{CertCleanup}},
	{"session loss", func(c CodingAgentProviderContract) bool { return c.HandlesTmuxSessionLoss }, []CodingAgentCertificationID{CertSessionLoss, CertSessionLossRecovery}},
	{"structured streaming", func(c CodingAgentProviderContract) bool { return c.SupportsStructuredStreaming }, structuredStreamingCertificationIDs},
	{"stalled turn diagnosis", func(c CodingAgentProviderContract) bool { return c.SupportsStalledTurnDiagnosis }, stalledTurnDiagnosisCertificationIDs},
	{"durable ack", func(c CodingAgentProviderContract) bool { return c.SupportsDurableAck }, durableAckCertificationIDs},
}

var codingAgentProviderCertifications = map[Provider][]CodingAgentCertification{
	ProviderAgyCLI: {
		{
			ID:          CertRuntimeAvailability,
			TestFile:    "pkg/adapters/agycli/agycli_runtime_availability_live_test.go",
			TestName:    "TestAgyRuntimeAvailabilityLive",
			Description: "contract agy binary resolves on PATH and answers the version probe (credential-free install/upgrade detection)",
			RealE2E:     true,
		},
		{
			ID:          CertFreshLaunch,
			TestFile:    "pkg/adapters/agycli/agycli_exec_live_test.go",
			TestName:    "TestAgyCLIRealExecFullContract",
			Description: "fresh print-mode turn starts and completes with a canary echo, wire usage, and a conversation handle",
			RealE2E:     true,
		},
		{
			ID:          CertRuntimeContext,
			TestFile:    "pkg/adapters/agycli/agycli_context_live_test.go",
			TestName:    "TestAgyCLIRealRuntimeContextContract",
			Description: "system-header fold steers the model (GEMINI.md project files are not auto-injected)",
			RealE2E:     true,
		},
		{
			ID:          CertWorkingDirectory,
			TestFile:    "pkg/adapters/agycli/agycli_context_live_test.go",
			TestName:    "TestAgyCLIRealWorkingDirectoryContract",
			Description: "pinned cwd reaches the model (canary file read) in an untrusted tmpdir print mode runs trust-exempt",
			RealE2E:     true,
		},
		{
			ID:          CertTrustAuthPrompts,
			TestFile:    "pkg/adapters/agycli/agycli_context_live_test.go",
			TestName:    "TestAgyCLIRealTrustAuthPromptsContract",
			Description: "logged-out agy (empty HOME) is detected via the auth marker and fails fast instead of hanging on OAuth",
			RealE2E:     true,
		},
		{
			ID:          CertMCPBridge,
			TestFile:    "pkg/adapters/agycli/agycli_bridge_live_test.go",
			TestName:    "TestAgyCLIRealMCPBridgeContract",
			Description: "canary MCP server mounts through WithMCPConfig and the model routes a call through it with no mount leak",
			RealE2E:     true,
		},
		{
			ID:          CertBridgeOnlyTools,
			TestFile:    "pkg/adapters/agycli/agycli_bridge_live_test.go",
			TestName:    "TestAgyCLIRealNativeToolModeBridgeAndDenial",
			Description: "workspace PreToolUse hook blocks native writes while the MCP canary remains callable in mcp_only mode",
			RealE2E:     true,
		},
		{
			ID:          CertSlowToolFalseIdle,
			TestFile:    "pkg/adapters/agycli/agycli_bridge_live_test.go",
			TestName:    "TestAgyCLIRealSlowToolFalseIdleContract",
			Description: "25s canary tool is awaited to completion with no premature done",
			RealE2E:     true,
		},
		{
			ID:          CertDoneDetection,
			TestFile:    "pkg/adapters/agycli/agycli_exec_live_test.go",
			TestName:    "TestAgyCLIRealExecFullContract",
			Description: "exit 0 + SUCCESS envelope marks done (same proof as fresh_launch)",
			RealE2E:     true,
		},
		{
			ID:          CertFinalExtraction,
			TestFile:    "pkg/adapters/agycli/agycli_exec_live_test.go",
			TestName:    "TestAgyCLIRealExecFullContract",
			Description: "envelope response text extracted with the canary intact (same proof as fresh_launch)",
			RealE2E:     true,
		},
		{
			ID:          CertMultiTurn,
			TestFile:    "pkg/adapters/agycli/agycli_resume_live_test.go",
			TestName:    "TestAgyCLIRealResumeMultiTurnContract",
			Description: "--conversation resume recalls turn 1 on the same native conversation id",
			RealE2E:     true,
		},
		{
			ID:          CertStructuredMultiTurn,
			TestFile:    "pkg/adapters/agycli/agycli_resume_live_test.go",
			TestName:    "TestAgyCLIRealResumeMultiTurnContract",
			Description: "resumed turn honors --json-schema with the schema payload extracted (same proof as multi_turn)",
			RealE2E:     true,
		},
		{
			ID:          CertTokenUsage,
			TestFile:    "pkg/adapters/agycli/agycli_exec_live_test.go",
			TestName:    "TestAgyCLIRealExecFullContract",
			Description: "wire input/output/thinking/cache usage lands on the response (same proof as fresh_launch)",
			RealE2E:     true,
		},
		{
			ID:          CertLiveInput,
			TestFile:    "pkg/adapters/agycli/agycli_interactive_live_test.go",
			TestName:    "TestAgyCLIRealInteractiveLiveInputContract",
			Description: "follow-up typed into the TUI sidecar is answered with the canary",
			RealE2E:     true,
		},
		{
			ID:          CertBusyLiveInput,
			TestFile:    "pkg/adapters/agycli/agycli_interactive_live_test.go",
			TestName:    "TestAgyCLIRealBusyLiveInputContract",
			Description: "mid-turn follow-up queues behind the running turn and is answered after, plus Escape interrupt",
			RealE2E:     true,
		},
		{
			ID:          CertCancellation,
			TestFile:    "pkg/adapters/agycli/agycli_cancel_isolation_live_test.go",
			TestName:    "TestAgyCLIRealCancellationContract",
			Description: "context cancel mid-turn kills the run and later turns are unaffected",
			RealE2E:     true,
		},
		{
			ID:          CertPersistentCancelReuse,
			TestFile:    "pkg/adapters/agycli/agycli_cancel_isolation_live_test.go",
			TestName:    "TestAgyCLIRealPersistentCancelReuseContract",
			Description: "a canceled sidecar turn is followed by a completed new message under the same owner",
			RealE2E:     true,
		},
		{
			ID:          CertParallelIsolation,
			TestFile:    "pkg/adapters/agycli/agycli_cancel_isolation_live_test.go",
			TestName:    "TestAgyCLIRealParallelIsolationContract",
			Description: "concurrent turns keep separate canaries and conversations",
			RealE2E:     true,
		},
		{
			ID:          CertReplyFormattingFidelity,
			TestFile:    "pkg/adapters/agycli/agycli_interactive_live_test.go",
			TestName:    "TestAgyCLIRealReplyFormattingFidelityContract",
			Description: "shared GFM-table + nested-fence bar survives sidecar pane capture byte-exact",
			RealE2E:     true,
		},
		{
			ID:          CertNativeTranscriptRecovery,
			TestFile:    "pkg/adapters/agycli/agycli_native_transcript_live_test.go",
			TestName:    "TestAgyCLIRealNativeTranscriptContract",
			Description: "real 2-turn conversation re-read from conversations/<id>.db by native session id with texts in order",
			RealE2E:     true,
		},
		{
			ID:          CertTmuxTurnExecution,
			TestFile:    "pkg/adapters/agycli/agycli_interactive_turn_live_test.go",
			TestName:    "TestAgyCLIRealInteractiveTurnContract",
			Description: "persistent turns execute in the sidecar pane with shared conversation and metered usage",
			RealE2E:     true,
		},
		{
			ID:          CertInteractiveMCPBridge,
			TestFile:    "pkg/adapters/agycli/agycli_interactive_turn_live_test.go",
			TestName:    "TestAgyCLIRealInteractiveMCPBridgeContract",
			Description: "sidecar turn routes a bridge tool call with no approval stall and no leak at close",
			RealE2E:     true,
		},
		{
			ID:          CertDurableAck,
			TestFile:    "pkg/adapters/agycli/agycli_durable_ack_live_test.go",
			TestName:    "TestAgyCLIRealDurableAckContract",
			Description: "live tmux input is confirmed by a matching new user step in AGY's conversation SQLite",
			RealE2E:     true,
		},
	},
	ProviderMuseCLI: {
		{
			ID:          CertStructuredStreaming,
			TestFile:    "pkg/adapters/musecli/musecli_msp_live_test.go",
			TestName:    "TestMuseMSPRealStreamSteerStop",
			Description: "muse serve streams text deltas and tool start/end events, takes a mid-turn message, stops at once and takes the next message",
			RealE2E:     true,
		},
		{
			ID:          CertPlanUsage,
			TestFile:    "pkg/adapters/musecli/musecli_msp_live_test.go",
			TestName:    "TestMuseMSPRealPlanUsage",
			Description: "a real muse serve turn carries the account's plan windows (used %, reset) from usage/read and usage/changed",
			RealE2E:     true,
		},
		{
			ID:          CertRuntimeAvailability,
			TestFile:    "pkg/adapters/musecli/musecli_runtime_availability_live_test.go",
			TestName:    "TestMuseRuntimeAvailabilityLive",
			Description: "contract muse binary resolves on PATH and answers the version probe (credential-free install/upgrade detection)",
			RealE2E:     true,
		},
		{
			ID:          CertTokenUsage,
			TestFile:    "pkg/adapters/musecli/musecli_token_usage_live_test.go",
			TestName:    "TestMuseTokenUsageLive",
			Description: "real turn reports input/output token counts through GenerationInfo for the cost ledger",
			RealE2E:     true,
		},
		{
			ID:          CertDurableAck,
			TestFile:    "pkg/adapters/musecli/musecli_durable_ack_live_test.go",
			TestName:    "TestMuseCLIRealDurableAckContract",
			Description: "steers a busy Muse turn via the production live-input path, awaits the transcript user-intent row as durable proof, and shows the model obeyed the steer; then proves an idle follow-up is durably acked fast",
			RealE2E:     true,
		},
		{
			ID:          CertBestEffortToolRestrictions,
			TestFile:    "pkg/adapters/musecli/musecli_tool_restrictions_live_test.go",
			TestName:    "TestMuseCLIRealBestEffortToolRestrictions",
			Description: "proves native reads are denied and MCP works in both transports, plus structured shell denial; reports internal-tool gaps without claiming strict containment",
			RealE2E:     true,
		},
		{
			ID:          CertFreshLaunch,
			TestFile:    "pkg/adapters/musecli/musecli_real_contract_test.go",
			TestName:    "TestMuseCLIRealInteractiveTmuxFullContract",
			Description: "boots the muse TUI in tmux in a scratch workspace, reaches a settled idle pane with no trust/auth gate, and cleans up the session",
			RealE2E:     true,
		},
		{
			ID:          CertStructuredMultiTurn,
			TestFile:    "pkg/adapters/musecli/musecli_exec_test.go",
			TestName:    "TestMuseExecLaneStructuredMultiTurn",
			Description: "runs two echo exec turns with --session-id and proves both prompts accumulated in one on-disk session",
			RealE2E:     true,
		},
		{
			ID:          CertRuntimeContext,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealExecRuntimeContext",
			Description: "plants a canary skill in a scratch workspace and runs the turn rooted there, so the answer proves the cwd was honored and project skills reached the model",
			RealE2E:     true,
		},
		{
			ID:          CertWorkingDirectory,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealExecRuntimeContext",
			Description: "the canary skill exists only in the requested workdir, so a correct answer proves the exec lane launched in that directory",
			RealE2E:     true,
		},
		{
			ID:          CertTrustAuthPrompts,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealTrustGate",
			Description: "boots a fresh workspace without --trust-workspace, proves the trust gate appears (never a silent hang), accepts it, and runs a first turn with no gate text in the answer",
			RealE2E:     true,
		},
		{
			ID:          CertMCPBridge,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealMCPBridge",
			Description: "mounts an in-process MCP stub through the settings merge, runs a Meta turn that calls its tool, and proves the stub saw the call, the wire carried the result, and settings restore byte-exact",
			RealE2E:     true,
		},
		{
			ID:          CertSlowToolFalseIdle,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealTmuxToolRunTerminal",
			Description: "interactive shell-tool turn waits for its current run terminal event after the tool result and returns the run-scoped final answer",
			RealE2E:     true,
		},
		{
			ID:          CertDoneDetection,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealTmuxMultiTurn",
			Description: "detects per-turn completion through accepted intent and matching run terminal events across two turns in one TUI session",
			RealE2E:     true,
		},
		{
			ID:          CertFinalExtraction,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealExecTurnContract",
			Description: "the adapter-returned text must match the sidecar transcript's last assistant text on a live Meta turn, with usage and shadow cost attached",
			RealE2E:     true,
		},
		{
			ID:          CertMultiTurn,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealTmuxMultiTurn",
			Description: "drives two turns through one TUI session and proves both answers share one native transcript",
			RealE2E:     true,
		},
		{
			ID:          CertLiveInput,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealTmuxLiveInput",
			Description: "queues a follow-up while a long turn is busy: intake-proven mid-turn and answered after it",
			RealE2E:     true,
		},
		{
			ID:          CertBusyLiveInput,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealTmuxLiveInput",
			Description: "the queued follow-up is accepted while the first turn streams and answered without losing either turn",
			RealE2E:     true,
		},
		{
			ID:          CertCancellation,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealTmuxCancellation",
			Description: "Ctrl-C mid-answer surfaces the interrupt marker, returns the TUI to idle, and the session takes the next turn",
			RealE2E:     true,
		},
		{
			ID:          CertPersistentCancelReuse,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealPersistentStopThenNewMessage",
			Description: "Escape stops an in-flight adapter turn, then the same owner accepts and answers a new message without a duplicate tmux session",
			RealE2E:     true,
		},
		{
			ID:          CertParallelIsolation,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealTmuxParallelIsolation",
			Description: "two TUI sessions in two workdirs run concurrently with distinct prompts; each native transcript holds only its own turn",
			RealE2E:     true,
		},
		{
			ID:          CertReplyFormattingFidelity,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealTmuxReplyFidelity",
			Description: "transcript extraction of a long-lines-and-list reply preserves markdown structure against the wrapped 80-col pane (agent-review sign-off)",
			RealE2E:     true,
		},
		{
			ID:          CertTmuxTurnExecution,
			TestFile:    "pkg/adapters/musecli/musecli_p0_live_test.go",
			TestName:    "TestMuseCLIRealPersistentSession",
			Description: "persistent turns execute in the tmux pane through GenerateContent dispatch",
			RealE2E:     true,
		},
	},
	ProviderClaudeCode: {
		{
			ID:          CertRuntimeAvailability,
			TestFile:    "pkg/adapters/claudecode/claudecode_runtime_availability_live_test.go",
			TestName:    "TestClaudeRuntimeAvailabilityLive",
			Description: "contract claude binary resolves on PATH and answers the version probe (credential-free install/upgrade detection)",
			RealE2E:     true,
		},
		{
			ID:          CertTokenUsage,
			TestFile:    "pkg/adapters/claudecode/claudecode_token_usage_live_test.go",
			TestName:    "TestClaudeTokenUsageLive",
			Description: "real turn reports input/output token counts through GenerationInfo for the cost ledger",
			RealE2E:     true,
		},
		{
			ID:          CertPlanUsage,
			TestFile:    "pkg/adapters/claudecode/claudecode_plan_usage_live_test.go",
			TestName:    "TestClaudePlanUsageLive",
			Description: "real turn leaves the account's plan usage windows (5h/7d used %, reset) in Claude's statusline sidecar",
			RealE2E:     true,
		},
		{
			ID:          CertDurableAck,
			TestFile:    "pkg/adapters/claudecode/claudecode_durable_ack_live_test.go",
			TestName:    "TestClaudeCodeRealDurableAckContract",
			Description: "steers a busy Claude turn via raw live input, awaits the transcript user row as durable proof, and shows the model obeyed the steer; then proves an idle follow-up is durably acked fast",
			RealE2E:     true,
		},
		{
			ID:          CertStalledTurnDiagnosis,
			TestFile:    "pkg/adapters/claudecode/claudecode_stalled_turn_diagnosis_live_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationStalledTurnDiagnosisContract",
			Description: "runs a real Claude Code turn to completion, then independently rediscovers it via DiagnoseTurnCompletion with no turn in flight",
			RealE2E:     true,
		},
		{
			ID:          CertStructuredMultiTurn,
			TestFile:    "pkg/adapters/claudecode/claudecode_structured_live_test.go",
			TestName:    "TestClaudeStructuredTwoTurnResume",
			Description: "runs two real Claude structured turns and proves the first turn exits naturally after persisting a resumable native session",
			RealE2E:     true,
		},
		{
			ID:          CertStructuredStreaming,
			TestFile:    "pkg/adapters/claudecode/claudecode_transcript_stream_realworld_test.go",
			TestName:    "TestClaudeCodeTranscriptStreamingRealWorldLive",
			Description: "tails the live JSONL transcript and streams structured assistant-text + MCP tool-call chunks across a real search→write→read bridge task",
			RealE2E:     true,
		},
		{
			ID:          CertStreamNoHistoryReplay,
			TestFile:    "pkg/adapters/claudecode/claudecode_transcript_stream_noreplay_live_test.go",
			TestName:    "TestClaudeTranscriptStreamNoHistoryReplayLive",
			Description: "commits a real marker turn to a real transcript, then proves a freshly constructed reader at a NOW boundary emits none of it",
			RealE2E:     true,
		},
		{
			ID:          CertReplyFormattingFidelity,
			TestFile:    "pkg/adapters/claudecode/claudecode_reply_formatting_live_test.go",
			TestName:    "TestClaudeCodeTmuxRealReplyFormattingFidelityE2E",
			Env:         []string{"-coding-cli-p0-live"},
			Description: "live turn proving markdown structure survives; the transcript preference used to be gated on length, which repaired truncation but was blind to equal-length re-wrapping",
			RealE2E:     true,
		},
		{
			ID:          CertRuntimeContext,
			TestFile:    "pkg/adapters/claudecode/claudecode_runtime_self_check_e2e_test.go",
			TestName:    "TestClaudeCodeTmuxRuntimeSelfCheckContract",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_INTEGRATION=1"},
			Description: "proves the real Claude Code session receives its system prompt, attached skill, and MCP bridge",
			RealE2E:     true,
		},
		{
			ID:          CertFreshLaunch,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationFreshPromptCarriesUserText",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_INTEGRATION=1"},
			Description: "starts Claude Code tmux transport and carries the first user prompt",
			RealE2E:     true,
		},
		{
			ID:          CertStatusLine,
			TestFile:    "pkg/adapters/claudecode/claudecode_statusline_stream_test.go",
			TestName:    "TestStreamClaudeStatusLineEmitsChunk",
			Description: "emits a status_line chunk carrying the real model (display_name), cost, tokens, and owning tmux session — no hardcoded default model",
		},
		{
			ID:          CertResumeCompactionStartup,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_adapter_test.go",
			TestName:    "TestClaudeResumeSummaryMenuNavigatesAwayFromCompressedDefault",
			Description: "resume/compaction startup prompts are detected and resolved through the global prompt-wait path, choosing the full session over the compressed summary",
		},
		{
			ID:          CertStartupTerminalVisibility,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_adapter_test.go",
			TestName:    "TestClaudeTerminalStreamCapturesRawScreenRows",
			Description: "Claude startup/working panes emit raw terminal rows before final completion",
		},
		{
			ID:          CertWorkingDirectory,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationHaikuWorkingDirectory",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "proves Claude Code and its MCP bridge run in the requested cwd",
			RealE2E:     true,
		},
		{
			ID:          CertTrustAuthPrompts,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationHaikuWorkingDirectory",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "fresh Claude workspace trust setup is handled before launching in a requested cwd",
			RealE2E:     true,
		},
		{
			ID:          CertNativeSystemPrompt,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationNativeSystemPrompt",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_INTEGRATION=1"},
			Description: "system prompt reaches Claude through native system-prompt transport",
			RealE2E:     true,
		},
		{
			ID:          CertPromptPaste,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationLargePastedPromptSubmits",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_INTEGRATION=1"},
			Description: "large pasted prompt submits correctly through tmux",
			RealE2E:     true,
		},
		{
			ID:          CertMCPBridge,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationHaikuMCPBridgeContract",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "Claude Code calls a real MCP bridge tool",
			RealE2E:     true,
		},
		{
			ID:          CertBridgeOnlyTools,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationNoInternalTools",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_INTEGRATION=1"},
			Description: "Claude Code bridge-only mode does not expose internal shell/file tools",
			RealE2E:     true,
		},
		{
			ID:          CertSlowToolLiveInput,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationHaikuLiveInputAndEscape",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_LIVE_E2E=1"},
			Description: "live validation input during a slow MCP tool does not complete the foreground turn",
			RealE2E:     true,
		},
		{
			ID:          CertSlowToolFalseIdle,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationHaikuLiveInputAndEscape",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_LIVE_E2E=1"},
			Description: "slow MCP activity keeps Claude classified active even if prompt-looking UI appears",
			RealE2E:     true,
		},
		{
			ID:          CertDoneDetection,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationHaikuLiveInputAndEscape",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_LIVE_E2E=1"},
			Description: "slow MCP plus live input does not falsely complete while active",
			RealE2E:     true,
		},
		{
			ID:          CertFinalExtraction,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxRealFinalExtractionFromTmuxAgentJudgeE2E",
			Env:         []string{"-coding-cli-p0-live"},
			Description: "real Claude Code tmux turn is captured and an agent judges final extraction quality, formatting, and TUI noise removal (no API key — agentreview sign-off, not an external LLM judge)",
			RealE2E:     true,
		},
		{
			ID:          CertMultiTurn,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationHaikuPersistentInteractiveMultiTurn",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "persistent Claude Code tmux session keeps native multi-turn memory",
			RealE2E:     true,
		},
		{
			ID:          CertStaleDraftCleanup,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationPersistentClearsStaleDraftBeforeNextTurn",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "stale prompt drafts are cleared before the next backend turn",
			RealE2E:     true,
		},
		{
			ID:          CertLiveInput,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationHaikuLiveInputAndEscape",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_LIVE_E2E=1"},
			Description: "live input routes into the active Claude Code tmux session",
			RealE2E:     true,
		},
		{
			ID:          CertBusyLiveInput,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationLiveInputProcessesQueuedFollowup",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_LIVE_E2E=1"},
			Description: "a follow-up submitted during a slow Claude Code tool call is processed by the same live session",
			RealE2E:     true,
		},
		{
			ID:          CertCancellation,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationPersistentCancelDoesNotLeaveBusySessionReusable",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "canceled Claude Code persistent sessions are not reused while busy",
			RealE2E:     true,
		},
		{
			ID:          CertPersistentCancelReuse,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationPersistentCancelDoesNotLeaveBusySessionReusable",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "a canceled persistent Claude pane is not reused unless it is prompt-ready",
			RealE2E:     true,
		},
		{
			ID:          CertLifecyclePolicy,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationHaikuPersistentInteractiveMultiTurn",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "persistent chat sessions remain registered after a completed turn",
			RealE2E:     true,
		},
		{
			ID:          CertBoundedRetention,
			TestFile:    "pkg/adapters/claudecode/claudecode_cleanup_test.go",
			TestName:    "TestCleanupClaudeCodeTmuxSessionsDoesNotBlockOnBusyPersistentSession",
			Description: "bounded Claude cleanup/retention path can drain without blocking active sessions",
		},
		{
			ID:          CertParallelIsolation,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationParallelIsolation",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "parallel Claude tmux sessions do not share state",
			RealE2E:     true,
		},
		{
			ID:          CertParallelStartupQueue,
			TestFile:    "pkg/adapters/internal/tmuxlaunch/tmuxlaunch_test.go",
			TestName:    "TestAcquireQueuesConcurrentStarts",
			Description: "tmux startup acquisition serializes concurrent provider launches when configured",
		},
		{
			ID:          CertSharedWorkdirMCPIsolation,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationSharedWorkingDirMCPIsolation",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_PERSISTENT_E2E=1"},
			Description: "parallel Claude sessions in one cwd keep MCP sessions isolated",
			RealE2E:     true,
		},
		{
			ID:          CertCleanup,
			TestFile:    "pkg/adapters/claudecode/claudecode_cleanup_test.go",
			TestName:    "TestCleanupClaudeCodeTmuxSessionsDoesNotBlockOnBusyPersistentSession",
			Description: "cleanup does not deadlock on busy persistent Claude Code sessions",
		},
		{
			ID:          CertSessionLoss,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_adapter_test.go",
			TestName:    "TestClaudeTmuxSessionLostErrorDetection",
			Description: "Claude Code classifies missing tmux server/session/pane errors",
		},
		{
			ID:          CertSessionLossRecovery,
			TestFile:    "coding_agent_continuation_real_test.go",
			TestName:    "TestCodingAgentContinuationRealE2EAfterTmuxLoss",
			Env:         []string{"RUN_CODING_AGENT_CONTINUATION_REAL_E2E=1"},
			Description: "Claude Code recovers a remembered-token conversation after its tmux session is killed",
			RealE2E:     true,
		},
		{
			ID:          CertTmuxTurnExecution,
			TestFile:    "pkg/adapters/claudecode/claudecode_interactive_integration_test.go",
			TestName:    "TestClaudeCodeTmuxIntegrationFreshPromptCarriesUserText",
			Env:         []string{"RUN_CLAUDE_CODE_TMUX_INTEGRATION=1"},
			Description: "interactive adapter executes the turn in the tmux pane and returns the reply",
			RealE2E:     true,
		},
	},
	ProviderCodexCLI: {
		{ID: CertPrivateAccountIsolation, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerSignedOutAccountCannotBorrowLogin", RealE2E: true, Description: "Native A/signed-out B/A account selection never borrows ambient login or thread"},
		{ID: CertSharedWorkdirMCPIsolation, TestFile: "pkg/adapters/codexcli/codexcli_real_contract_test.go", TestName: "TestCodexCLIRealInteractiveSharedWorkingDirMCPIsolation", RealE2E: true, Description: "Concurrent native threads in one workdir use their own MCP configuration and outputs"},
		{ID: CertFreshLaunch, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: FreshLaunch"},
		{ID: CertNativeSystemPrompt, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: NativeSystemPrompt"},
		{ID: CertPromptPaste, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: PromptPaste"},
		{ID: CertFinalExtraction, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: FinalExtraction"},
		{ID: CertReplyFormattingFidelity, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: ReplyFormattingFidelity"},
		{ID: CertMultiTurn, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: MultiTurn"},
		{ID: CertStructuredMultiTurn, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: StructuredMultiTurn"},
		{ID: CertStreamNoHistoryReplay, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: StreamNoHistoryReplay"},
		{ID: CertStaleDraftCleanup, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: StaleDraftCleanup"},
		{ID: CertLifecyclePolicy, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: LifecyclePolicy"},
		{ID: CertCleanup, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: Cleanup"},
		{ID: CertBoundedRetention, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: BoundedRetention"},
		{ID: CertSessionLossRecovery, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: SessionLossRecovery"},
		{ID: CertTokenUsage, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: TokenUsage"},
		{ID: CertStatusLine, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerLifecycleP0", RealE2E: true, Description: "Real Codex app-server: StatusLine"},
		{ID: CertRuntimeContext, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerRuntimeP0", RealE2E: true, Description: "Real Codex app-server: RuntimeContext"},
		{ID: CertWorkingDirectory, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerRuntimeP0", RealE2E: true, Description: "Real Codex app-server: WorkingDirectory"},
		{ID: CertMCPBridge, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerRuntimeP0", RealE2E: true, Description: "Real Codex app-server: MCPBridge"},
		{ID: CertStructuredStreaming, TestFile: "pkg/adapters/codexcli/codexcli_transcript_stream_realworld_test.go", TestName: "TestCodexAppServerStreamingRealWorldLive", RealE2E: true, Description: "Real Codex app-server: StructuredStreaming"},
		{ID: CertBridgeOnlyTools, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerBridgeOnlyP0", RealE2E: true, Description: "Real Codex app-server: BridgeOnlyTools"},
		{ID: CertSlowToolLiveInput, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerSteerP0", RealE2E: true, Description: "Real Codex app-server: SlowToolLiveInput"},
		{ID: CertSlowToolFalseIdle, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerSteerP0", RealE2E: true, Description: "Real Codex app-server: SlowToolFalseIdle"},
		{ID: CertDoneDetection, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerSteerP0", RealE2E: true, Description: "Real Codex app-server: DoneDetection"},
		{ID: CertLiveInput, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerSteerP0", RealE2E: true, Description: "Real Codex app-server: LiveInput"},
		{ID: CertBusyLiveInput, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerSteerP0", RealE2E: true, Description: "Real Codex app-server: BusyLiveInput"},
		{ID: CertDurableAck, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerSteerP0", RealE2E: true, Description: "Real Codex app-server: DurableAck"},
		{ID: CertCancellation, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerInterruptP0", RealE2E: true, Description: "Real Codex app-server: Cancellation"},
		{ID: CertPersistentCancelReuse, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerInterruptP0", RealE2E: true, Description: "Real Codex app-server: PersistentCancelReuse"},
		{ID: CertParallelIsolation, TestFile: "pkg/adapters/codexcli/codexcli_appserver_live_test.go", TestName: "TestCodexAppServerParallelIsolationP0", RealE2E: true, Description: "Real Codex app-server: ParallelIsolation"},
		{ID: CertRuntimeAvailability, TestFile: "pkg/adapters/codexcli/codexcli_runtime_availability_live_test.go", TestName: "TestCodexRuntimeAvailabilityLive", RealE2E: true, Description: "Real Codex app-server RuntimeAvailability"},
		{ID: CertPlanUsage, TestFile: "pkg/adapters/codexcli/codexcli_plan_usage_live_test.go", TestName: "TestCodexPlanUsageLive", RealE2E: true, Description: "Real Codex app-server PlanUsage"},
	},
	ProviderCursorCLI: {
		{ID: CertRuntimeAvailability, TestFile: "pkg/adapters/cursorcli/cursorcli_runtime_availability_live_test.go", TestName: "TestCursorRuntimeAvailabilityLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor binary and version"},
		{ID: CertFreshLaunch, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertPromptPaste, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertMultiTurn, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertStaleDraftCleanup, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertLifecyclePolicy, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertBoundedRetention, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertCleanup, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertSessionLossRecovery, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertStructuredMultiTurn, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertStreamNoHistoryReplay, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKLifecycleLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK fresh/warm/resume continuity, stream isolation and scoped process cleanup"},
		{ID: CertRuntimeContext, TestFile: "pkg/adapters/cursorcli/cursorcli_runtime_self_check_e2e_test.go", TestName: "TestCursorCLIRealRuntimeSelfCheckContract", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK system prompt, skills and MCP runtime"},
		{ID: CertNativeSystemPrompt, TestFile: "pkg/adapters/cursorcli/cursorcli_runtime_self_check_e2e_test.go", TestName: "TestCursorCLIRealRuntimeSelfCheckContract", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK system prompt, skills and MCP runtime"},
		{ID: CertMCPBridge, TestFile: "pkg/adapters/cursorcli/cursorcli_structured_integration_test.go", TestName: "TestCursorCLIStructuredMCPBridge", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK MCP arguments and committed tool result stream"},
		{ID: CertStructuredStreaming, TestFile: "pkg/adapters/cursorcli/cursorcli_structured_integration_test.go", TestName: "TestCursorCLIStructuredMCPBridge", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK MCP arguments and committed tool result stream"},
		{ID: CertBridgeOnlyTools, TestFile: "pkg/adapters/cursorcli/cursorcli_structured_integration_test.go", TestName: "TestCursorCLIStructuredSandboxedMCP", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK allowed MCP tool and denied native file mutation in selected workspace"},
		{ID: CertWorkingDirectory, TestFile: "pkg/adapters/cursorcli/cursorcli_structured_integration_test.go", TestName: "TestCursorCLIStructuredSandboxedMCP", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK allowed MCP tool and denied native file mutation in selected workspace"},
		{ID: CertFinalExtraction, TestFile: "pkg/adapters/cursorcli/cursorcli_reply_formatting_live_test.go", TestName: "TestCursorCLIRealReplyFormattingFidelityE2E", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK markdown compared with the same turn's streamed reply"},
		{ID: CertReplyFormattingFidelity, TestFile: "pkg/adapters/cursorcli/cursorcli_reply_formatting_live_test.go", TestName: "TestCursorCLIRealReplyFormattingFidelityE2E", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK markdown compared with the same turn's streamed reply"},
		{ID: CertLiveInput, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKSteerDuringSlowToolLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK message during a slow tool joins the same turn"},
		{ID: CertBusyLiveInput, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKSteerDuringSlowToolLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK message during a slow tool joins the same turn"},
		{ID: CertCancellation, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKCancelAndReuseLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK slow tool stays active, cancel terminates and next turn completes"},
		{ID: CertPersistentCancelReuse, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKCancelAndReuseLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK slow tool stays active, cancel terminates and next turn completes"},
		{ID: CertSlowToolFalseIdle, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKCancelAndReuseLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK slow tool stays active, cancel terminates and next turn completes"},
		{ID: CertDoneDetection, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKCancelAndReuseLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK slow tool stays active, cancel terminates and next turn completes"},
		{ID: CertParallelIsolation, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKParallelMCPIsolationLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK owners in one workspace receive only their own MCP result"},
		{ID: CertSharedWorkdirMCPIsolation, TestFile: "pkg/adapters/cursorcli/cursorcli_sdk_live_test.go", TestName: "TestCursorSDKParallelMCPIsolationLive", Env: []string{"-coding-cli-p0-live"}, RealE2E: true, Description: "Real Cursor SDK owners in one workspace receive only their own MCP result"},
	},
	ProviderPiCLI: {
		{ID: CertRuntimeAvailability, TestFile: "pkg/adapters/picli/picli_runtime_availability_live_test.go", TestName: "TestPiRuntimeAvailabilityLive", Description: "Real Pi RPC RuntimeAvailability proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertTokenUsage, TestFile: "pkg/adapters/picli/picli_token_usage_live_test.go", TestName: "TestPiTokenUsageLive", Description: "Real Pi RPC TokenUsage proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertDurableAck, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSteerAndAbortLive", Description: "Real Pi RPC DurableAck proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertStructuredMultiTurn, TestFile: "pkg/adapters/picli/picli_structured_integration_test.go", TestName: "TestPiCLIStructuredTwoTurnResume", Description: "Real Pi RPC StructuredMultiTurn proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertStructuredStreaming, TestFile: "pkg/adapters/picli/picli_structured_stream_realworld_test.go", TestName: "TestPiCLIStructuredStreamingRealWorldLive", Description: "Real Pi RPC StructuredStreaming proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertStreamNoHistoryReplay, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC StreamNoHistoryReplay proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertReplyFormattingFidelity, TestFile: "pkg/adapters/picli/picli_reply_formatting_live_test.go", TestName: "TestPiCLIRealReplyFormattingFidelityE2E", Description: "Real Pi RPC ReplyFormattingFidelity proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertRuntimeContext, TestFile: "pkg/adapters/picli/picli_runtime_self_check_e2e_test.go", TestName: "TestPiCLIRealRuntimeSelfCheckContract", Description: "Real Pi RPC RuntimeContext proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertMCPBridge, TestFile: "pkg/adapters/picli/picli_mcp_bridge_real_test.go", TestName: "TestPiCLIRealMCPBridgeOnlyToolsContract", Description: "Real Pi RPC MCPBridge proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertBridgeOnlyTools, TestFile: "pkg/adapters/picli/picli_mcp_bridge_real_test.go", TestName: "TestPiCLIRealMCPBridgeOnlyToolsContract", Description: "Real Pi RPC BridgeOnlyTools proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertStatusLine, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC StatusLine proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertFreshLaunch, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC FreshLaunch proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertResumeCompactionStartup, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC ResumeCompactionStartup proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertWorkingDirectory, TestFile: "pkg/adapters/picli/picli_mcp_bridge_real_test.go", TestName: "TestPiCLIRealNativeToolsAvailableOnEveryLaunch", Description: "Real Pi RPC WorkingDirectory proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertNativeSystemPrompt, TestFile: "pkg/adapters/picli/picli_runtime_self_check_e2e_test.go", TestName: "TestPiCLIRealRuntimeSelfCheckContract", Description: "Real Pi RPC NativeSystemPrompt proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertPromptPaste, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC PromptPaste proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertSlowToolLiveInput, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSteerAndAbortLive", Description: "Real Pi RPC SlowToolLiveInput proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertSlowToolFalseIdle, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSteerAndAbortLive", Description: "Real Pi RPC SlowToolFalseIdle proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertDoneDetection, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSteerAndAbortLive", Description: "Real Pi RPC DoneDetection proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertFinalExtraction, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC FinalExtraction proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertMultiTurn, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC MultiTurn proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertStaleDraftCleanup, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC StaleDraftCleanup proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertLiveInput, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSteerAndAbortLive", Description: "Real Pi RPC LiveInput proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertBusyLiveInput, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSteerAndAbortLive", Description: "Real Pi RPC BusyLiveInput proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertCancellation, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSteerAndAbortLive", Description: "Real Pi RPC Cancellation proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertPersistentCancelReuse, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSteerAndAbortLive", Description: "Real Pi RPC PersistentCancelReuse proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertLifecyclePolicy, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC LifecyclePolicy proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertBoundedRetention, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC BoundedRetention proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertParallelIsolation, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCParallelIsolationLive", Description: "Real Pi RPC ParallelIsolation proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertParallelStartupQueue, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCParallelIsolationLive", Description: "Real Pi RPC ParallelStartupQueue proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertSharedWorkdirMCPIsolation, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCParallelIsolationLive", Description: "Real Pi RPC SharedWorkdirMCPIsolation proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertCleanup, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC Cleanup proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertSessionLoss, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC SessionLoss proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
		{ID: CertSessionLossRecovery, TestFile: "pkg/adapters/picli/picli_rpc_live_test.go", TestName: "TestPiRPCSessionLifecycleLive", Description: "Real Pi RPC SessionLossRecovery proof", Env: []string{"-coding-cli-p0-live"}, RealE2E: true},
	},
}

// RequiredCodingAgentCertificationIDs returns the proof IDs implied by the
// provider's claimed capabilities.
func RequiredCodingAgentCertificationIDs(contract CodingAgentProviderContract) []CodingAgentCertificationID {
	seen := make(map[CodingAgentCertificationID]struct{}, len(requiredTmuxCertificationIDs))
	if contract.Transport == CodingAgentTransportTmux {
		for _, id := range requiredTmuxCertificationIDs {
			seen[id] = struct{}{}
		}
	} else {
		seen[CertFreshLaunch] = struct{}{}
		seen[CertPromptPaste] = struct{}{}
		seen[CertDoneDetection] = struct{}{}
		seen[CertFinalExtraction] = struct{}{}
	}
	for _, rule := range codingAgentCapabilityCertifications {
		if !rule.enabled(contract) {
			continue
		}
		for _, id := range rule.required {
			seen[id] = struct{}{}
		}
	}

	if acceptsBestEffortToolRestrictions(contract) {
		delete(seen, CertBridgeOnlyTools)
		seen[CertBestEffortToolRestrictions] = struct{}{}
	}
	out := make([]CodingAgentCertificationID, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func CodingAgentProviderCertifications(provider Provider) []CodingAgentCertification {
	provider = Provider(strings.ToLower(strings.TrimSpace(string(provider))))
	certs := append([]CodingAgentCertification(nil), codingAgentProviderCertifications[provider]...)
	for i := range certs {
		if certs[i].Priority == "" {
			certs[i].Priority = CodingAgentCertificationPriorityForID(certs[i].ID)
		}
		if certs[i].Priority == CodingAgentCertificationPriorityP0 {
			// P0 has one supported gate: the authenticated live runner. Legacy
			// per-provider RUN_* environment switches remain metadata for P1
			// integration tests, but must not define or weaken P0 execution.
			certs[i].Env = []string{"-coding-cli-p0-live"}
		}
	}
	sort.Slice(certs, func(i, j int) bool {
		if certs[i].ID == certs[j].ID {
			return certs[i].TestName < certs[j].TestName
		}
		return certs[i].ID < certs[j].ID
	})
	return certs
}

// CodingAgentP0ReleaseEntry is one row of the release matrix: an active P0
// provider and the Go package containing its registered P0 tests.
type CodingAgentP0ReleaseEntry struct {
	Provider Provider
	Package  string
}

// CodingAgentP0ReleaseMatrix derives the release matrix from the registry:
// every active (tmux, non-deprecated) provider with required P0 proofs,
// with the package holding its P0 tests. Release tooling resolves its
// provider set and test packages from this function instead of
// hardcoding them, so onboarding a provider cannot silently skip it.
func CodingAgentP0ReleaseMatrix() ([]CodingAgentP0ReleaseEntry, error) {
	var matrix []CodingAgentP0ReleaseEntry
	seen := make(map[Provider]bool)
	for _, contract := range CodingAgentProviderContracts() {
		if seen[contract.Provider] {
			continue
		}
		seen[contract.Provider] = true
		if contract.Transport != CodingAgentTransportTmux && contract.Transport != CodingAgentTransportAppServer && contract.Transport != CodingAgentTransportRPC && contract.Transport != CodingAgentTransportACP {
			continue
		}
		required := RequiredP0CodingAgentCertificationIDs(contract)
		if len(required) == 0 {
			continue
		}
		packagePath, err := codingAgentP0TestPackage(contract.Provider, required)
		if err != nil {
			return nil, err
		}
		matrix = append(matrix, CodingAgentP0ReleaseEntry{Provider: contract.Provider, Package: packagePath})
	}
	return matrix, nil
}

// codingAgentP0TestPackage derives the test package for a provider from the
// registered P0 tests' file locations. Proof completeness stays with the
// callers (the P0 runner rejects missing required tests per provider);
// this only requires the registered P0 tests to agree on one package.
func codingAgentP0TestPackage(provider Provider, required []CodingAgentCertificationID) (string, error) {
	need := make(map[CodingAgentCertificationID]bool, len(required))
	for _, id := range required {
		need[id] = true
	}
	packagePath := ""
	for _, cert := range CodingAgentProviderCertifications(provider) {
		if !need[cert.ID] {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(cert.TestFile))
		if packagePath == "" {
			packagePath = dir
		} else if dir != packagePath {
			return "", fmt.Errorf("%s P0 tests span packages %s and %s", provider, packagePath, dir)
		}
	}
	if packagePath == "" {
		return "", fmt.Errorf("%s has required P0 proofs but no registered P0 tests", provider)
	}
	return packagePath, nil
}

// CodingAgentCertificationPriorityForID returns the release priority for a
// proof ID. Priority derives from requirement: an ID is P0 exactly when
// some active contract requires it, so the two can never disagree the
// way the streaming pair once did. New certifications default to P1
// until a requirement names them.
func CodingAgentCertificationPriorityForID(id CodingAgentCertificationID) CodingAgentCertificationPriority {
	for _, contract := range CodingAgentProviderContracts() {
		for _, required := range RequiredP0CodingAgentCertificationIDs(contract) {
			if id == required {
				return CodingAgentCertificationPriorityP0
			}
		}
	}
	return CodingAgentCertificationPriorityP1
}

// RequiredP0CodingAgentCertificationIDs returns the non-negotiable runtime
// proofs for active tmux coding-agent providers. The base list deliberately
// includes bridge-only containment without consulting SupportsBridgeOnlyTools:
// provider self-description cannot weaken the platform's mcp_only release bar.
// Muse's documented best-effort exception has its own required live P0 proof.
func RequiredP0CodingAgentCertificationIDs(contract CodingAgentProviderContract) []CodingAgentCertificationID {
	if contract.Transport != CodingAgentTransportTmux && contract.Transport != CodingAgentTransportAppServer && contract.Transport != CodingAgentTransportRPC && contract.Transport != CodingAgentTransportACP {
		return nil
	}
	ids := append([]CodingAgentCertificationID(nil), requiredP0CertificationIDs...)
	if contract.Transport == CodingAgentTransportAppServer || contract.Transport == CodingAgentTransportRPC || contract.Transport == CodingAgentTransportACP {
		// Protocol-native transports retain the real lifecycle P0s; terminal
		// rendering and workspace trust menus do not exist on this interface.
		filtered := ids[:0]
		for _, id := range ids {
			if id != CertTmuxTurnExecution && id != CertTrustAuthPrompts && !(contract.Transport == CodingAgentTransportACP && (id == CertLiveInput || id == CertBusyLiveInput)) {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}

	if acceptsBestEffortToolRestrictions(contract) {
		for i, id := range ids {
			if id == CertBridgeOnlyTools {
				ids[i] = CertBestEffortToolRestrictions
			}
		}
	}
	// Streaming is release-blocking only for providers that actually stream
	// structured transcript chunks. A provider that merely reads a transcript for
	// a final-answer summary (pi today) is not required to certify streaming —
	// until its live tailer + streaming E2E land and it flips the flag on.
	// Both proofs spread from the shared definition: the stream AND the
	// no-history-replay guarantee are required together.
	if contract.SupportsStructuredStreaming {
		ids = append(ids, structuredStreamingCertificationIDs...)
	}
	// Workflow steps and background agents use structured execution. Every
	// persistent provider must independently prove that the native session
	// survives process exit and resumes on the next message; tmux multi-turn
	// certification exercises a different lifecycle and is not a substitute.
	if contract.UsesPersistentSession && contract.SupportsNativeResume {
		ids = append(ids, CertStructuredMultiTurn)
	}
	// A provider claiming DiagnoseTurnCompletion (PLAT-116) must prove it
	// works against a real turn, the same way structured streaming above
	// must prove itself for providers that claim it.
	if contract.SupportsStalledTurnDiagnosis {
		ids = append(ids, stalledTurnDiagnosisCertificationIDs...)
	}
	// A provider claiming rollout/transcript-backed submit confirmation
	// must prove the durable record actually confirms a busy steer.
	if contract.SupportsDurableAck {
		ids = append(ids, durableAckCertificationIDs...)
	}
	// A provider claiming token usage must prove a real turn reports it.
	// Declaration without this proof is how silent cost blindness ships.
	if contract.SurfacesTokenUsage {
		ids = append(ids, tokenUsageCertificationIDs...)
	}
	// A provider claiming plan usage must prove a real turn reports it: the UI
	// shows it and the watchdog trusts it over screen text.
	if contract.SurfacesPlanUsage {
		ids = append(ids, planUsageCertificationIDs...)
	}
	// A provider whose chat-history catch-up reads the CLI's native
	// transcript must prove the reader against a real conversation:
	// transcript formats drift per release and a silent misread backfills
	// wrong history (PLAT-178). Scoped to providers with a registered live
	// proof; the older cursor/pi/muse readers predate this requirement and
	// carry their own fixture tests.
	if contract.Provider == ProviderAgyCLI && contract.AdapterReadsTranscript {
		ids = append(ids, CertNativeTranscriptRecovery)
	}
	// Agy's sidecar bridge proof rides its own requirement: peers cover
	// tools-in-tmux through their long-standing bridge entries.
	if contract.Provider == ProviderAgyCLI {
		ids = append(ids, CertInteractiveMCPBridge)
	}
	return ids
}

// MissingP0CodingAgentCertifications is intentionally independent from the
// broader known-gap mechanism: P0 gaps can never be waived as TODOs.
func MissingP0CodingAgentCertifications(contract CodingAgentProviderContract) []CodingAgentCertificationID {
	have := make(map[CodingAgentCertificationID]struct{})
	for _, cert := range CodingAgentProviderCertifications(contract.Provider) {
		have[cert.ID] = struct{}{}
	}
	var missing []CodingAgentCertificationID
	for _, id := range RequiredP0CodingAgentCertificationIDs(contract) {
		if _, ok := have[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

func MissingCodingAgentCertifications(contract CodingAgentProviderContract) []CodingAgentCertificationID {
	have := make(map[CodingAgentCertificationID]struct{})
	for _, cert := range CodingAgentProviderCertifications(contract.Provider) {
		have[cert.ID] = struct{}{}
	}
	var missing []CodingAgentCertificationID
	for _, id := range RequiredCodingAgentCertificationIDs(contract) {
		if _, ok := have[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

// Muse (2026-09-11) and Agy (2026-09-20) are the explicitly accepted
// best-effort exceptions. Other providers retain the strict requirement
// even if they clear a flag. Agy's print lane auto-denies every tool
// headless with no selective switch, so strict bridge-only containment is
// unprovable; the best-effort proof pins the denial posture instead.
func acceptsBestEffortToolRestrictions(c CodingAgentProviderContract) bool {
	if c.SupportsBridgeOnlyTools || len(c.ToolRestrictionGaps) == 0 {
		return false
	}
	return c.Provider == ProviderMuseCLI || c.Provider == ProviderAgyCLI
}
