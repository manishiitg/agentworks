[← platform / coding-agent-bridge](index.md)

# PLAT-160 — interactive tool-call completion is reconstructed by polling a file that can lose the final event at turn-end, when an already-proven synchronous signal exists and is only used as a fallback

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | P1 — this is the actual reason tool calls go missing for |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `partially implemented` — the settled timestamp, shell-command settle presentation, and Claude/Codex turn-end transcript read race are fixed. The Claude final-flush channel-ownership regression found live on 2026-08-21 is also fixed and regression-tested. The architectural fix this ticket is actually about (promote `toolcalllog` to primary) remains unstarted and out of scope for a same-pass fix, see "Scope correction" and "Final-read fix" below |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — this is the actual reason tool calls go missing for
  interactive (tmux) coding-CLI sessions at all. PLAT-141's settle/recovery
  path and PLAT-149's shared recovery both compensate for this after the
  fact; neither stops it from happening.
- **Owner:** `multi-llm-provider-go/pkg/adapters/{claudecode,codexcli,cursorcli}`
  interactive transcript tailers (the lossy signal), `agent_go/pkg/agentwrapper/llm_agent.go`
  + `mcpagent/executor/handlers.go`'s `toolcalllog` hook (the reliable signal,
  currently recovery-only)
- **Related:** [PLAT-141](plat-141.md) (settle + transcript recovery, the
  compensating fix), [PLAT-149](plat-149.md) (found and proved the reliable
  mechanism, used it for recovery only), [PLAT-152](plat-152.md) (closed as
  not-a-defect on its own claim, but reconfirmed live in production that the
  lossy watcher below actually runs)

## Why this ticket exists

Reported live: a batch of settled tool-call chips all displayed the identical
clock time despite having different, real, recovered durations — a
[PLAT-141](plat-141.md)-class symptom pointing at the same underlying gap that
ticket compensates for but doesn't close. Explaining *why* the backend loses
these calls in the first place surfaced that the explanation, and a concrete
fix direction, already existed — split across three tickets, never connected.

## The two mechanisms, and why one is lossy

**The lossy one.** For interactive/tmux coding-CLI sessions, tool-call
completion is reconstructed by periodically polling the CLI's own transcript
file on disk — e.g. `claudecode_transcript_stream.go`'s tailer, a
`time.NewTicker`-driven loop:

```go
for {
    events, err := tailer.Read(time.Now(), turnStart, pendingToolStarts)
    if err == nil {
        for _, e := range events {
            ...
            select {
            case streamChan <- chunk:
            case <-ctx.Done():
                return   // <-- exits WITHOUT a final read
            }
        }
    }
    select {
    case <-ctx.Done():
        return           // <-- also exits WITHOUT a final read
    case <-ticker.C:
    }
}
```

If the turn's context is cancelled (normal completion, timeout, or a user
Stop) in the gap between one poll and the next, the loop returns immediately.
Anything the CLI wrote to its transcript in that gap — including a tool call
that finished just before teardown — is never read and never becomes an
event. This is confirmed current code, not a stale finding.

[PLAT-149](plat-149.md) noted this exact race as an aside while investigating
a different question, at a time when it believed this tailer was "opt-in...
and, as far as could be confirmed, only ever called by family-server, not
this platform" — i.e. not a live production exposure.
[PLAT-152](plat-152.md)'s investigation (closed as not-a-defect on its own
unrelated claim) incidentally disproved that belief: `enableStreaming`
auto-enables this exact tailer (`WithClaudeStreamTranscript` /
`WithCodexStreamTranscript` / `WithCursorStreamTranscript`) for ordinary
interactive sessions on this platform, confirmed via
`mcpagent/agent/coding_agent_integrations.go` and
`coding_agent_options.go`. So the race is live in production today, not a
theoretical opt-in path.

**The reliable one.** [PLAT-149](plat-149.md) separately proved a second,
independent mechanism exists: `agent_go/pkg/agentwrapper/llm_agent.go:1143`
registers a `toolcalllog.RegisterHook` whose `OnStart`/`OnEnd` fire from
`mcpagent/executor/handlers.go`'s `HandleCustomExecute` — the HTTP handler
every bridge tool call, on every provider, actually goes through to execute.
This is synchronous by construction: it is called at the exact moment a tool
starts and the exact moment it returns, not on a timer. Measured on one
production session: 1 of 36 calls unpaired via this mechanism, versus 59 of
605 (~10%) via the polling mechanism above.

## Why this hasn't already been fixed

Not for lack of a proof it works — [PLAT-149](plat-149.md) shipped it, but
only as a **fallback**: `cmd/server/tool_result_recovery.go` and
`tool_call_backfill.go` try `toolcalllog.Recover` after a call has already
gone unpaired, to patch up the display after the fact. It was never promoted
to be the **primary** live source the UI/Pulse evidence draws from while a
turn is still running. [PLAT-149](plat-149.md)'s own "what this does not do"
section says as much: "does not suppress the unreliable mechanism... needs
the site found first" — and the site (the polling tailer above) is now
found.

## What a real fix looks like (not implemented here)

1. Route live tool-call display through `toolcalllog`'s synchronous
   `OnStart`/`OnEnd` as the primary signal for interactive sessions, not only
   as post-hoc recovery.
2. Either stop constructing `ToolCallStartEvent`/`ToolCallEndEvent` from the
   polling transcript tailer for interactive sessions (removing the
   duplication PLAT-149 measured between the two mechanisms), or reconcile
   the two at a single point so a consumer sees one signal, not two
   competing ones.
3. Independently, the transcript tailer's own `ctx.Done()`-without-a-final-
   read shape is a real, fixable bug regardless of (1) and (2) — a `select`
   that tries one more non-blocking `tailer.Read()` before returning would
   close some of the gap even if the tailer keeps its current role.
4. `toolcalllog`'s ids (`toolu_<decimal>`, its own counter) and the
   transcript tailer's ids (the provider's real `toolu_...`/`call_...`
   opaque ids) are a disconnected id space — confirmed in
   [PLAT-149](plat-149.md). Any consolidation has to either unify identity
   or explicitly document that `toolcalllog` is keyed differently by design.

## What this ticket does not do

- No code change. This connects an already-proven root cause
  ([PLAT-149](plat-149.md)'s reliable mechanism, [PLAT-152](plat-152.md)'s
  confirmation the lossy one is live in production) to a fix direction that
  wasn't written down anywhere as an actionable plan.
- Does not re-litigate [PLAT-141](plat-141.md)'s settle/backfill mechanism,
  which stays correct and necessary as a safety net regardless of whether
  this ticket's fix ships — a synchronous primary signal reduces how often
  the fallback is needed, it doesn't make it unnecessary.
- Does not extend to Pi CLI, which [PLAT-149](plat-149.md)/[PLAT-152](plat-152.md)
  already found uses a different mechanism (an inline marker side-channel,
  not a polled transcript file) and so is not exposed to this specific race.

## Acceptance

- Live tool-call display for interactive sessions is driven by the
  synchronous `toolcalllog` signal, not the polling transcript tailer, or
  the two are reconciled to a single non-duplicating signal.
- The measured ~10% unpaired rate from the polling mechanism drops for
  whichever signal ends up primary — verified against a real session, not
  a synthetic one, per this register's standing discipline for this class
  of ticket.
- [PLAT-141](plat-141.md)'s settle/backfill path still exists as a backstop
  and its existing tests still pass, but fires measurably less often.

## Scope correction: `toolcalllog`'s own live path is itself non-functional today

Checked directly rather than assumed, while scoping how to implement the
"promote `toolcalllog` to primary" direction above: its live-emission path
does not currently reach any real consumer in production, for a different
reason than the polling race this ticket opened with.

`LLMAgentWrapper`'s registered hook (`agent_go/pkg/agentwrapper/llm_agent.go:1143`)
emits via `w.emitEvent(ev)` → `w.tracer.EmitEvent(event)`. `w.tracer` is
whatever `cmd/server/server.go:3044` constructed:
`tracer := observability.GetTracer(tracingProvider)`, where `tracingProvider`
defaults to `"noop"` (`TRACING_PROVIDER` is not set in normal deployment).
Confirmed live: `grep`ing a full day's `server_debug.log` for `toolcalllog`'s
own id shape (`toolu_<decimal>`) found zero occurrences, live or recovered,
despite real interactive sessions running the whole time. Separately,
`base_agent.go` — the path workflow-step sessions actually run through, not
`LLMAgentWrapper` — never registers this hook at all.

So "promote to primary" is not a priority flip. It needs:
1. A working live path from the hook to the platform's real event consumers
   (EventStore / Pulse evidence) — either fixing `w.tracer` to not be a noop
   in this context, or having the hook call something that reaches
   `a.listeners` directly instead of the tracer.
2. The same hook registered in `base_agent.go`, which does not have it today.
3. Only then does suppressing/reconciling the polling tailer (the original
   "what a real fix looks like" list, items 1-2) become safe to do without
   losing live display entirely for workflow-step sessions.

What *is* confirmed working regardless of the above: `toolcalllog.RecordStart`/
`RecordEnd` themselves run unconditionally on every bridge tool call, on every
provider, independent of the hook or the tracer — this is exactly what
[PLAT-141](plat-141.md)'s recovery already queries successfully via
`toolcalllog.Snapshot`. The data is reliable; only the *live* delivery path
built on top of it is currently dead.

## What shipped: the immediate live symptom

The concrete complaint that led to this investigation — a batch of settled
tool-call chips all displaying the identical clock time despite different
real recovered durations — is fixed. `EventStore.settleOpenToolCalls`
(`internal/events/event_store.go`) stamped every settled event's
`Timestamp` with the settle-batch's `now`, even when a real duration had
been recovered from the provider transcript. Now, when a real duration is
recovered, the displayed timestamp is computed as `startedAt + realDuration`
— the call's own real completion time — instead of the moment the batch
happened to run. When nothing is recovered, the settle moment remains the
only signal available and is used exactly as before.

`TestSettledTimestampReflectsRealCompletionNotSettleMoment`
(`internal/events/tool_call_settle_test.go`): fail-before/pass-after,
reverted the fix and confirmed the test fails with a ~168ms drift between
the displayed timestamp and the real completion time, consistent with the
live symptom; restored, confirmed it passes with the timestamp within 5ms
of the expected value. Full existing settle-test suite re-verified passing
alongside it.

This does not touch the architectural question above — it is the smallest
fix that matches what a recovered event is already capable of reporting
correctly (it already has the real duration; it just wasn't using it to
compute the displayed time).

## Review after the partial implementation (2026-08-20)

**Verdict: the timestamp patch is correct and tested, but PLAT-160 is not yet
the fix for the user-visible receipt problem that motivated it.** The ticket
must remain `partially implemented`.

### What the current patch genuinely fixes

- `EventStore.settleOpenToolCalls` no longer assigns one settlement-batch
  timestamp to every recovered call. When recovery supplies a real duration,
  the event is dated at `startedAt + realDuration`.
- `TestSettledTimestampReflectsRealCompletionNotSettleMoment` is a meaningful
  fail-before/pass-after regression test for that behavior.

### What the live reproduction proves is still broken

The Social Media allocator displayed a group of green cards labelled
`Command Completed`, all with `Turn: 0`, empty/meaningless output controls,
and durations such as 13.3m, 3.0m, and 1.1m. Server telemetry for that same
session explicitly recorded the relevant calls as PLAT-141 synthetic settles:
their end events did not arrive within the grace window and the reported
open-to-settle duration was **not tool runtime**.

The generic frontend renderer already handles this honestly, but shell calls
bypass it:

1. `ToolCallEndEventDisplay` classifies `execute_shell_command` as a code
   execution tool.
2. It routes the event to
   `CodeExecutionToolCallEndDisplay` before the generic renderer's
   `synthetic_settle` handling runs.
3. The specialized shell renderer ignores `synthetic_settle` and always
   presents a non-error event as green `Command Completed`, includes the
   meaningless `Turn: 0`, labels open-to-settle time as `Duration`, and offers
   an output toggle even when no output was recovered.

Therefore the timestamp patch will make recovered timestamps more truthful
after restart, but it will not stop the screenshot's misleading completion
cards.

### Required completion work

1. **Fix presentation now:** make every specialized tool renderer honor the
   same synthetic-settle contract as the generic renderer. A synthetic shell
   settle must be neutral, omit `Turn: 0`, label unrecovered elapsed time as
   open/unverified rather than runtime, and hide the output control when there
   is no output. When start arguments are available, show a short command
   summary instead of an anonymous `Command Completed` label.
2. **Fix live delivery at the platform boundary:** connect the synchronous
   bridge `toolcalllog` hook to EventStore/Pulse consumers without going
   through the default noop tracer, and register it for the `base_agent.go`
   workflow-step path as well as `LLMAgentWrapper`.
3. **Reconcile identity once:** bridge-generated and provider-native tool-call
   IDs must either become one canonical ID or be correlated at one central
   boundary. Consumers should receive one start and one terminal receipt, not
   competing duplicate streams.
4. **Retain transcript safety:** add a final transcript read on cancellation
   and keep PLAT-141 settlement as a last-resort backstop, not the ordinary
   completion path.
5. **Prove it live:** run a real interactive workflow-step session and assert
   that every displayed tool start has exactly one terminal receipt, real
   results retain their output and duration, synthetic settlement is rare and
   never shown as success, and Pulse sees the same receipts as formatted chat.

### Wording correction for future implementers

The reliable artifact that already exists is `toolcalllog`'s synchronous
`RecordStart`/`RecordEnd` data. Its **live delivery path is not reliable or
operational yet**. Describing the hook itself as an already-working live
signal obscures the noop-tracer and missing-`base_agent` work discovered while
implementing this ticket.

## Presentation item 1 done: shell-command renderer now honors synthetic_settle

Verified the review's core claim directly against the code before fixing it:
`ToolCallEndEvent.tsx` (the generic renderer) already special-cases
`synthetic_settle`, but `execute_shell_command` — the tool behind the
screenshot that started this whole investigation — is routed to
`CodeExecutionToolCallEndDisplay.tsx` first, which had zero references to
`synthetic_settle` anywhere. Its status line was hardcoded to
`isError ? '❌ Command Failed' : '✅ Command Completed'`, with
`Turn: ${event.turn}` always shown — and a settled event carries no turn
number, so it always rendered `Turn: 0`, exactly matching the screenshot.

Fixed the `execute_shell_command` branch to match the generic renderer's
contract: on a synthetic settle, the icon and status text are neutral (not a
green checkmark), `Turn` is omitted, and the duration is labelled based on
whether a real result was recovered — `Runtime (recovered): Xms` when the
backend found real output via PLAT-141's transcript recovery (this is a real
measured duration now, not open-to-settle time), or `Open for: Xs (not tool
runtime)` when nothing was recovered at all. Existing error detection
(exit-code/traceback sniffing on the recovered output) is unchanged, so a
settle that recovered a genuine failure still renders as a failure, not
neutral.

`CodeExecutionToolCallEndDisplay.test.tsx`: two new tests, fail-before/
pass-after (temporarily forced `isSyntheticSettle = false`, confirmed both
new tests fail with the exact live symptom — unconditional "Command
Completed" and "Turn: 0" — restored, confirmed both pass). Full
`src/components/events/` suite and `tsc -b` re-verified clean.

**Scope note:** this fixes the one branch that produced the reported
screenshot. The review's item 1 said "every specialized tool renderer" —
this file has several other tool-specific branches (`get_api_spec`, a few
code-discovery renderers further down) that still don't check
`synthetic_settle`. Not fixed here; flagged so the next person doesn't
assume full coverage from this pass.

## Final-read fix: the transcript tailers' turn-end race is closed for Claude and Codex

Asked directly whether to attempt the full "promote toolcalllog to primary"
architecture in this same pass. Declined that specific piece — it needs a
working live path built first (fix or bypass the noop tracer, register the
hook on `base_agent.go`), then a way to reconcile it against the polling
tailer without rendering every tool call twice; that's real design work
against a live event pipeline every session depends on, not something to
rush through alongside smaller fixes. Instead, implemented the one item
from this ticket's original "what a real fix looks like" list that is
small, low-risk, and directly closes the confirmed root cause:

`cursorcli`'s transcript tailer already did a final flush on `ctx.Done()`
using `context.Background()` for the send (so it doesn't race the same
cancellation that triggered it) — confirmed by reading it before writing
anything. `claudecode`'s and `codexcli`'s did not; both returned
immediately on cancellation, which is the exact mechanism this ticket
already confirmed live in production.

- `claudecode_transcript_stream.go`: extracted the per-tick read+send into
  a `poll(sendCtx)` closure so the `ctx.Done()` branch calls
  `poll(context.Background())` once before returning — same shape as
  `cursorcli`. New test cancels the context ~20ms after starting the
  tailer (well under its 250ms poll interval, so its own ticker cannot
  have fired), and asserts a tool-call row already on disk before the
  tailer started is still delivered. Fail-before/pass-after: reverted to
  the immediate return, confirmed the new test times out; restored,
  confirmed it passes. Full `claudecode` package suite (180+ tests)
  re-verified passing.
- `codexcli_interactive_adapter.go`: codex's tailer polls inline inside
  the main response-wait loop's ticker branch (its own existing comment
  already notes polling must happen before the completion check, so a
  normal end-of-turn tick is covered) — but its `ctx.Done()` branch
  returned without polling at all. Added the same
  `poll(context.Background(), streamChan)` call there. The full
  response-wait loop isn't unit-testable in isolation without mocking the
  tmux pane-capture machinery, so the new test instead exercises
  `codexTranscriptStreamState.poll` directly with
  `context.Background()` against a tool-call row already on disk — the
  exact call that was added, proving it delivers. Full `codexcli` package
  suite re-verified passing.

Both changes are additive and self-contained to the two files above — no
tracer, dedup, or cross-repo wiring touched, so the duplication risk that
makes the toolcalllog-promotion piece genuinely risky does not apply here.

### Still open, unchanged by this fix

- **toolcalllog live delivery** still goes through the noop tracer and is
  absent from workflow-step (`base_agent.go`) agents — the architectural
  fix this ticket is actually named for.
- **Other specialized tool renderers** (`get_api_spec`, code-discovery
  branches in `CodeExecutionToolCallEndDisplay.tsx`) still bypass
  `synthetic_settle`.
- **The Output-toggle control** still renders on an empty synthetic settle
  even though there is nothing to expand — a small, real, low-risk gap in
  the presentation fix, not yet addressed.

## Final-flush ownership regression — 2026-08-21

The final-read fix above introduced a second, more severe lifecycle requirement
for Claude Code. `streamClaudeTranscript` intentionally calls its last
`poll(context.Background())` after cancellation so a transcript row written at
turn end is not lost. But the tailer was launched as a detached goroutine from
`ClaudeCodeInteractiveAdapter.generateContentTmuxBody`. The outer
`llmtypes.WithObservability` wrapper owns `StreamChan` and closes it as soon as
that body returns. A slow final poll could therefore send into the already
closed channel and panic the entire AgentWorks process:

```
panic: send on closed channel
claudecode.streamClaudeTranscript.func1
```

This was observed live in a Social Media scheduled run at 20:19 IST. The
missing background-conversation JSON warning immediately before it was a
separate best-effort restore warning, not the process crash.

### Correct ownership rule

A producer that deliberately performs a final flush must be stopped **and
joined** before its channel owner is allowed to close that channel. Cancellation
alone is not sufficient: it requests the final flush, but does not prove it has
finished.

### Implemented repair

`multi-llm-provider-go` commit `ed317ca` adds
`startClaudeTranscriptStream`, which returns an idempotent stop function that
cancels and waits for the transcript goroutine. The interactive adapter defers
that stop function, so the body cannot return to `WithObservability` until the
final poll has completed. This mirrors Cursor's existing stop-and-join
lifecycle rather than suppressing the useful final transcript read.

`TestClaudeTranscriptStreamStopFlushesBeforeCallerClosesStream` proves that the
final tool receipt is delivered before the caller closes the stream channel;
the full Claude adapter package passed. A server restart is required before
live verification.

### Follow-through

This repair makes the polling fallback safe; it does not change the broader
PLAT-160 direction. Every provider-side detached live producer should have the
same explicit ownership rule and a regression test, while the durable
architecture remains one primary, synchronous `toolcalllog` source reconciled
at the platform boundary.
