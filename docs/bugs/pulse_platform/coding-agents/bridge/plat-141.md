[← platform / coding-agent-bridge](index.md)

# PLAT-141 — no interactive (tmux) adapter emits a tool-call end, on any provider, so completed commands show as unresolved

| Field | Value |
|---|---|
| State | in progress |
| Priority | P2 |
| Product | coding-agents |
| Area | bridge |
| Summary | this is **not** a Claude Code bug. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `partially implemented` — Claude Code interactive-transport recovery shipped, live reverify pending; the same interactive-adapter gap still exists on codexcli, cursorcli, picli. Separately, a THIRD mechanism (agent_go ignoring ToolCallErrorEvent in its own settle bookkeeping, provider/transport-agnostic) found and fixed 2026-08-20, tested fail-before/pass-after |
| Last synchronized | `2026-08-20` |

- **Scope correction (2026-08-19):** this is **not** a Claude Code bug. It is a
  property of the interactive transport, and every provider has it:

  | provider | structured adapter | interactive adapter |
  |---|---|---|
  | claudecode | emits `ToolCallEndEvent` | **never** |
  | codexcli | emits | **never** |
  | cursorcli | emits | **never** |
  | picli | emits | **never** |

  It is inherent rather than an oversight. An interactive adapter scrapes a
  terminal pane, and a pane carries no structured tool events, so there is
  nothing to emit an end *from*. That is why "make the adapter emit it properly"
  is not available as a fix, and why recovery from the provider's own record is
  the shape this has to take.

  The affected workflow (`tectonicusadaytrading`) runs `transport: tmux`.

- **Priority:** P2 — display only. No work is lost and no decision is made on
  missing data, but the chat misreports completed commands, and the compensating
  code added on 2026-08-18 fabricates durations while doing so.
- **Owner:** whatever converts a coding-agent turn's tool activity into
  `tool_call_start` / `tool_call_end` store events, plus
  `internal/events/event_store.go` (the settle that currently papers over it).

## Measured

`tectonicusadaytrading`, 2026-08-18, tool call `toolu_01GTECvgs77yocXxYx1nbfpb`:

| source | fact |
|---|---|
| Claude Code transcript `598f5b59` | `tool_use` at `15:52:26.430Z`, `tool_result` at `15:52:26.471Z`, 71-char result |
| our event store | `START` logged `21:22:26` (= `15:52:26Z`); **no `END` ever logged, under any session** |
| our UI | chip settled at `21:23:23`, displayed duration **45.4s** |

**The command completed in 41 milliseconds.** The result has been in the
transcript ever since. Our store never produced an end event for it, and the
duration shown to the user is start-to-settle, not the tool's real duration.

Earlier in the same session, 117 tool calls started and 103 ended — the ones
that fail to pair are a minority and are not distinguished by tool name
(`execute_shell_command` appears on both sides of the split).

## What is proven

1. The tool ran and returned promptly; nothing failed, nothing was lost.
2. No `tool_call_end` event entered the store for these calls. This is not
   lateness — a longer wait cannot recover an event that is never produced.
3. The 5-second grace window added on 2026-08-18 (`3c527c9d4`) does not address
   this population. It was sized on a genuinely-late case measured on a
   different session (turn end `20:34:28`, tool end `20:34:29`) and helps there.
4. The settle's displayed duration is fabricated relative to the tool's real
   duration (45.4s shown, 41ms actual).

## Hypothesis resolved (2026-08-19)

The pairing theory below is **wrong**. The store's telemetry logs every
`ToolCallEndEvent` added under any session, and `END id=toolu_01GTECvg…` returns
zero lines. The end is not mis-routed to a sub-session; it is **never emitted at
all**. The sub-session identifiers are real but incidental.

## Fix shipped

`claudecode.ToolResultsFromTranscript` reads completed calls out of the CLI's own
transcript, keyed by tool-call id, returning the real output and the real
runtime. The settle asks for them before closing a chip, so a 41ms command now
reads as 41ms and shows what it printed.

The event store does not know any of this. It calls a resolver that `cmd/server`
installs — a package that models events should not know which CLI wrote them —
so the native session id, working directory and transcript shape stay on the
server side, where the live handle already is.

Every path that cannot answer returns not-found rather than a guess: a session
already torn down, a non-Claude provider, an empty result, a missing or
unreadable transcript. The chip then closes blank, as it did before.

## A third, distinct mechanism found and fixed (2026-08-20, pi structured)

Not the interactive-adapter gap this ticket was originally about, and not
[PLAT-160](plat-160.md)'s polling-tailer race either — both of those are about
an adapter never emitting a signal at all. This one is about `agent_go` itself
ignoring a signal the adapter DID emit correctly.

Found live on a **structured** pi-cli turn (`pi --print --mode json`, group
`mahima`, `check-form-26as-xspaces`) — a transport this ticket's own table
already lists as reliably emitting `ToolCallEndEvent`. Two tool calls failed
with real, specific errors ("Working directory does not exist"; separately an
ACCESS DENIED write to the wrong scoped folder), each correctly reported as a
`ToolCallErrorEvent`. Both still turned up minutes later inside a "N of N tool
call(s) produced no end event" settle, shown in the UI (after `48bea2f0`'s
`SyntheticSettle` labeling fix, shipped earlier the same day) as "no result
reported" — indistinguishable from a call that genuinely got no response.

Root cause: `logToolCallTelemetry`'s `openToolCall` bookkeeping
(`event_store.go:1596`) only had a case for `*events.ToolCallEndEvent`. A
`*events.ToolCallErrorEvent` — which reported an outcome, just a failing one —
was never removed from the pending map, so it sat "open" until turn end and
was settled exactly like true silence.

This is **provider- and transport-agnostic**: it lives in `agent_go`'s own
telemetry tracker, not any CLI adapter, so every provider whose tool calls can
error is exposed, structured or interactive alike. Unlike the interactive-only
gap above, it doesn't require a missing adapter capability to reproduce — any
real tool failure triggers it.

Fixed by adding the missing case, mirroring the existing `ToolCallEndEvent`
handling exactly (clear the pending entry, log start/duration). Verified
fail-before/pass-after with `TestErroredToolCallIsNotSweptIntoSyntheticSettle`
(`tool_call_settle_test.go`): reverting the new case reproduces the exact
production log line —
`PLAT-141: 1 of 1 tool call(s) produced no end event within 20ms` — for a call
that had, in fact, reported a specific error a moment earlier.

### Still to do

- **Root cause and fix direction now consolidated**: [PLAT-160](plat-160.md)
  connects this ticket's compensating recovery to why the loss happens at all
  (the interactive transcript tailer's poll loop can lose the final event at
  turn-end) and to PLAT-149's already-proven synchronous alternative. This
  ticket's recovery mechanism stays as the necessary backstop either way.
- **Live reverify**: confirm recovered chips show real output and real durations
  on Claude Code before replicating anything.
- **The other three providers.** `SetToolResultResolver` is already
  provider-agnostic; only the implementation is Claude-specific. Each package
  already carries transcript readers to build on — codexcli has five files
  including a rollout binding and a completion reader, cursorcli two, picli one
  — so each is a `ToolResultsFromTranscript` plus a case in `recoverToolResult`.
  Deliberately not done yet: replicating an unverified fix three times is how one
  wrong assumption becomes four.
- **Measure the blast radius first.** If most workflows run structured, this is
  narrow and three more readers are not worth their maintenance; if most run
  tmux, it is the opposite. That count should drive the decision, not symmetry.
- **Delete the compensating code** once verified. The 5s grace window helps a
  genuinely-late population measured on a different session and can stay for
  now, but the blank settle exists only for this gap.
- A session torn down before the settle cannot be recovered — the native id
  lives on the live handle. If that proves common, the id needs persisting.

## What was NOT proven (superseded by the resolution above)

The tools are dispatched under **sub-session identifiers** while the store
tracks open calls under the parent schedule session. Three appear in the same
minute:

    session_id=msgseq-iteration-0-default-step-1-place-paper-trades
    session_id=schedule-manual--cd0655e9_1787068265820483000
    session_id=sub-exec-eval-signal-freshness-1787068341244552000

A plausible reading is that the start is attributed to the schedule session and
the completion is produced under the step's own session, so the two never pair.
**This has not been demonstrated** — no `END` was found under any session id for
the affected call, which is equally consistent with the end never being emitted
at all. Both readings must be tested before a fix is chosen; picking one on
plausibility is how the wrong cause got shipped twice already today.

## Candidate fixes

1. **Attribute both events to the same session.** Correct at source if the
   pairing hypothesis holds. Touches session routing that other work is
   currently editing.
2. **Backfill from the transcript.** The result and its true duration are on
   disk. `readClaudeTranscriptMessages` already parses this shape in
   `multi-llm-provider-go`'s `claudecode` package, but is unexported. This fixes
   the display regardless of which reading above is right, and repairs the
   fabricated durations.

Option 2 also lets the grace window and the settle placeholder be **deleted**
rather than kept — both exist only to make this gap survivable.

## Acceptance

- A `message_sequence` step's tool calls pair start-to-end in the store.
- A settled chip, if any remain, shows the tool's real duration.
- The grace window and `(output not captured)` placeholder are removed once the
  underlying events are complete.
