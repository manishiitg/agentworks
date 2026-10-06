[← goals / pulse-governance](index.md)

# PLAT-142 — Pulse's own review evidence has the same orphaned-tool-call gap as the chat UI, and it is bigger there

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | P1 — this is not display. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `partially implemented` — recovery shipped for the regular/message_sequence step path on Claude Code; todo-task steps and the other three providers are explicit follow-up |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — this is not display. Pulse's Gate/reviewers read
  `execution-attempt-*-conversation.json` to judge whether a step's work is
  legitimate. A tool call recorded with no result is evidence with a hole in
  it, and on some workflows most of the evidence has one.
- **Owner:** `pkg/orchestrator/context_aware_bridge.go` (`captureToolCallEvent`,
  the durable-evidence writer), `pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
  (`saveExecutionConversationLogs`).

## How it was found

While reverifying PLAT-141's chat-UI fix, the same question was asked of Pulse's
own evidence files rather than only the chat.

## Measured

745 execution-attempt logs across every workflow in this workspace:

    tool calls total: 15,601
    missing a result:  2,563  (16.4%)

    testing                 88%   (1474/1677)
    social-media              9%
    tectonicusadaytrading    10%
    hetznerssh                9%
    salesoutreach              7%
    rtslatency                 7%
    instagram                  7%
    linkedin                   3%
    upwork                     3%

## Root cause: PLAT-141's gap, at a different consumer

`Agent.emitTypedEvent` (`mcpagent/agent/agent.go:3095`) is a single fan-out
point: one raw event reaches every tracer AND every listener in one call. It is
genuine pub/sub, not accidental duplication — tracers feed the live chat UI,
listeners feed `ContextAwareBridge`'s durable per-step record, and both exist
for real, different reasons (ephemeral display vs. permanent audit trail).

The bug is upstream of that fan-out. For an interactive-transport tool call, no
`ToolCallEndEvent` is ever constructed and passed to `emitTypedEvent` at all —
this is inherent, not a routing failure: a terminal pane carries no structured
tool events, so there is nothing to emit one from (confirmed for all four
providers in PLAT-141). Because the gap is upstream of the fan-out, it hits
**every** consumer identically. PLAT-141 recovered it for the chat UI only, by
patching one downstream consumer; `ContextAwareBridge` never received the
equivalent fix and had zero compensation until this ticket.

### What is NOT yet pinned down

The exact code path that emits an orphaned Start with no matching End was not
conclusively located. Two real, confirmed mechanisms exist and either could be
the active one for a given call:

1. `claudecode_transcript_stream.go`'s live tailer (`streamClaudeTranscript`)
   polls the JSONL transcript every 250ms and exits on `ctx.Done()` **without a
   final read** — a genuine race for a tool call fast enough to complete between
   the last poll and turn-end cancellation (every measured call was 35-41ms,
   well under one poll interval). This mechanism is opt-in via
   `WithStreamTranscript` and, as far as could be confirmed, is not enabled for
   this platform's workflow steps — `family-server` is the only caller found.
2. The interactive adapter's own end-of-turn reconstruction
   (`readClaudeTranscriptMessages`, called from
   `claudecode_interactive_adapter.go:656`) — referenced by a comment in
   `mcpagent/agent/tool_registry.go:210` ("a wrapped Run already receives the
   CLI transcript's canonical tool intent/result pair") as the canonical source
   for interactive-transport tool events. The exact point where mcpagent turns
   that reconstructed history into `ToolCallStart`/`ToolCallEnd` AgentEvents was
   not located in this session.

Do not assume (1) is the live mechanism without checking whether
`WithStreamTranscript` is set anywhere in this platform's call path first —
that was checked here and appeared not to be, but was not exhaustively proven.

## Fix shipped

`backfillMissingToolResults` (`tool_call_backfill.go`), called once at the top
of `saveExecutionConversationLogs` — the single function every regular and
`message_sequence` step's evidence write already funnels through. It does not
reimplement transcript reading: it calls the exact same
`claudecode.ToolResultsFromTranscript` PLAT-141 built and verified for the chat
UI, resolved against the step's own executing agent's session handle (already
available at this call site — no new plumbing to find it).

Only entries in the exact orphaned shape are touched —
`toolCallEntryLooksOrphaned`: an id, no result, no error, zero duration. A call
that genuinely failed or genuinely returned an empty string already has a real
outcome and is left alone.

## What this does not cover, named rather than assumed fixed

- **Todo-task steps.** `saveTodoTaskExecutionLog` is a second, separate
  evidence-writing function with its own `toolCalls` parameter and does not yet
  call the backfill. Regular and `message_sequence` steps — the majority of
  workflow steps in this workspace — are covered; todo-task steps are not.
- **codexcli, cursorcli, picli.** Same architecture, same gap, per PLAT-141.
  Each already has transcript-reading infrastructure to build on.
- **The upstream fix.** The correct long-term shape is one reconciliation at
  `Agent.emitTypedEvent` itself, so every current and future consumer — chat UI,
  Pulse evidence, and whatever reads the stream next — receives a complete
  event without each needing its own patch. That is a cross-repo change to
  mcpagent's core turn lifecycle and needs a live run to verify; it was not
  attempted blind in this session. What shipped here and in PLAT-141 are two
  call sites sharing one recovery function, which is the safe, verifiable
  version of the same idea — not the final architecture.

## Acceptance

- A `message_sequence` or regular step's `execution-attempt-*-conversation.json`
  has no `tool_calls` entry with an id, no result, no error and zero duration,
  when Claude Code's transcript has a result for that id.
- `testing`'s 88% figure is re-measured after a live run and drops sharply.
- Todo-task steps and the other three providers are picked up as separate,
  explicitly-scoped work — not silently assumed covered by this ticket.
