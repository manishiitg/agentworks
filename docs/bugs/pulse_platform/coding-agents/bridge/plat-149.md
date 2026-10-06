[← platform / coding-agent-bridge](index.md)

# PLAT-149 — two independent mechanisms report the same bridge tool call under different identities, and one of them drops ~10% of results

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | coding-agents |
| Area | bridge |
| Summary | P1 — real, active event duplication in production (not just the |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — shared, provider-agnostic recovery shipped for the chat UI and Pulse evidence; the unreliable mechanism's exact construction site remains unlocated and unsuppressed |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — real, active event duplication in production (not just the
  missing-result gap PLAT-141/142 already covered), and the fix for that gap
  is now provider-agnostic instead of Claude-specific.
- **Owner:** `agent_go/pkg/toolcallrecovery` (new), `agent_go/pkg/agentwrapper/llm_agent.go`
  (the reliable mechanism), whatever constructs the unreliable one (still
  unlocated — see below).

## How it was found

While scoping "what would a proper architectural fix take" for PLAT-141/142,
tracing where the platform's tool-call events actually originate (not just
where they were recovered from).

## Measured, live, from one production session

    toolcalllog-backed mechanism (agentwrapper hook):  1 of 36  unpaired (~3%)
    Claude-native-ID mechanism (unlocated):            59 of 605 unpaired (~10%)

Both fire for the same physical tool calls. Confirmed by id shape alone: 118
short (`toolu_<N>`, a local counter) and 607 long (Claude's own opaque format)
distinct ids logged in one session's `[TOOL]` telemetry. Two mechanisms,
same calls, different identities, both flowing to the same consumers,
unreconciled.

## What is proven

1. **`agent_go/pkg/agentwrapper/llm_agent.go:1143`** registers a
   `toolcalllog.RegisterHook` whose `OnStart`/`OnEnd` fire from
   `mcpagent/executor/handlers.go`'s `HandleCustomExecute` — the HTTP handler
   every bridge tool call, on every provider, goes through. Read end to end:
   `RecordEnd` is called on every return path, success and error both. This
   mechanism is reliable by construction, not by luck, and it is
   provider-agnostic — `HandleCustomExecute` does not know or care which CLI
   is calling it.
2. **`toolcalllog`'s ids are a disconnected id space.**
   `fmt.Sprintf("toolu_%d", atomic.AddUint64(&idSeq, 1))` — a local, global,
   incrementing counter, formatted to resemble Claude's id shape but
   structurally nothing alike (Claude's real ids are long, mixed-case,
   opaque). Confirmed from source, not runtime — no new logging needed to
   settle this. A second, unlocated mechanism carries the *real* ids and is
   the one PLAT-141/142 already found dropping results.
3. Both flow through the platform's single event consumers (the chat UI's
   event store, and `ContextAwareBridge`'s Pulse-evidence writer) — PLAT-142's
   finding that these are two independent listeners on one fan-out point
   still holds; this ticket is about a THIRD signal (toolcalllog) that turns
   out to already answer the question PLAT-141/142 were solving with
   Claude-transcript parsing.

## What was not located, despite an exhaustive search

The unreliable, real-id mechanism's construction site. Ruled out, with
evidence, not assumption:

- `claudecode_interactive_adapter.go` — zero references to
  `StreamChunkTypeToolCallStart`/`End` in the whole file.
- `streamClaudeTranscript` (`claudecode_transcript_stream.go`) — real ids,
  transcript-based, but opt-in via `WithStreamTranscript` and, as far as
  could be confirmed, only ever called by `family-server`, not this platform.
  Its own design also has a genuine latent bug worth noting separately: its
  poll loop exits on `ctx.Done()` without a final read, which would drop a
  tool call fast enough to complete between the last poll and turn-end
  cancellation — exactly the shape measured here (35-41ms calls). This may
  still be relevant if the platform enables it in the future.
- `mcpagent/agent/tool_registry.go`'s `observedDirectToolExecutor` —
  constructs real events, but explicitly skips emission whenever
  `isTurnInFlight()`, which is true for essentially all normal step
  execution.
- `mcpagent/agent/parallel_tool_execution.go` / `llm_generation.go`'s
  agentic-loop tool dispatch — plausible in shape, but Claude Code
  auto-enables `useCodeExecutionMode`, and the bridge MCP config
  (`coding_agents_bridge.go`) is built to be handed to the CLI process
  directly — suggesting the CLI calls the bridge server itself, independent
  of mcpagent's Go-side dispatch, for these tools.
- `claudecode_retained_turn.go`'s `ReadRetainedTurnMessages` — zero callers
  anywhere in either repo. Dead export, not the mechanism.

Whoever picks this up next: start from the ruled-out list above rather than
re-treading it.

## Fix shipped

`agent_go/pkg/toolcallrecovery` — one small, tested, provider-agnostic package.
`Recover(sessionID, Candidate{ToolName, StartedAt})` matches an orphaned entry
against `toolcalllog.Snapshot(sessionID)` by tool name and closest start time
within a 5s window — not by id, since the two mechanisms share no id space —
and returns the real result and real duration.

Wired as the **first-tried** source at both existing PLAT-141/142 recovery
sites, ahead of the Claude-transcript fallback:

- `cmd/server/tool_result_recovery.go` (chat UI settle) — trivial to add: the
  session id the orphaned event already carries is the exact key
  `toolcalllog` uses, so no live-agent-handle search is needed for this path
  at all.
- `pkg/orchestrator/agents/workflow/step_based_workflow/tool_call_backfill.go`
  (Pulse evidence) — uses `hcpo.sessionID`, already in scope.

Both call sites still fall through to the existing Claude-transcript recovery
for whatever `toolcalllog` does not cover (a session already torn down before
settle, or a genuinely different gap).

## What this does not do

- **Does not suppress the unreliable mechanism.** Its construction site is
  unlocated, so nothing was removed — the duplication (two events per call)
  is still happening; only the orphaned-result symptom is now covered by a
  second, more reliable source. Suppressing it properly is the actual
  architectural fix and needs the site found first. **Update (2026-08-20):**
  the site is found — [PLAT-152](plat-152.md)'s investigation confirmed the
  interactive transcript tailers this ticket's own "ruled out" section
  suspected are in fact live in production. [PLAT-160](plat-160.md)
  consolidates that with this ticket's own reliable mechanism into a fix
  direction: promote `toolcalllog` from recovery-only to the primary live
  signal.
- **Does not touch mcpagent's `Agent.emitTypedEvent` fan-out.** The originally
  scoped "one reconciliation point reaching every consumer identically" idea
  from PLAT-142 is not what shipped — this is two call sites sharing one
  matcher, safer and verifiable tonight, not the final architecture.
- **Live reverify outstanding**, same as PLAT-141/142.

## Acceptance

- A bridge tool call orphaned by the unreliable mechanism recovers via
  `toolcallrecovery` with its real output and real (not open-to-settle)
  duration, without needing a live agent handle or the Claude transcript.
- The unreliable mechanism's construction site is found, and either
  suppressed for bridge tools (removing the duplication) or reconciled at
  mcpagent's single fan-out point — not by adding a third parallel patch.
