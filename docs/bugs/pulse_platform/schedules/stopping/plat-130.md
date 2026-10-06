[← schedules / stopping](index.md)

# PLAT-130 — the schedule Stop button marks the run stopped but does not stop the work

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | schedules |
| Area | stopping |
| Summary | P1 — this is not cosmetic. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — live-reverified 2026-08-20 (second attempt). The isSessionMarkedStopped gate holds: no auto-resume/materialize relaunch fired on a real stop, the in-flight turn cancelled cleanly, nothing ran afterward |
| Last synchronized | `2026-08-20` |

- **Priority:** P1 — this is not cosmetic. A stopped schedule can continue
  executing side-effecting work (e.g. posting) after the UI, the run history,
  and the schedule's own recorded status all say it stopped. The user cannot
  trust Stop to mean stop.
- **Owner:** `cmd/server/workflow_execution_tracker.go`
  (`cancelTrackedExecutionsForSession`), `cmd/server/session_lifecycle.go`
  (`cancelBackgroundAgents`), and whatever dispatch site spawns the goroutine
  that actually drives a `run_full_workflow` execution — not yet located.

## How it surfaced

Reported live: clicking Stop on a running schedule interrupts the current
message, but the run then continues — the next queued
`message_sequence` item (e.g. a second social post) fires anyway, in the same
run, as if nothing had been stopped.

## Root cause, confirmed layer by layer

The frontend Stop button (`WorkflowScheduleRunsPanel.tsx`'s `handleStopRun`)
calls `POST /api/scheduler/jobs/{id}/stop` →
`stopScheduledJobHandler` → `SchedulerService.StopRunningJobForWorkflow` →
`stopRunningJob`, which does two things: cancels a per-run context
(`cancelScheduleRunContext`) and tears down session-level work
(`cancelSessionRuntimeWork` → `cancelBackgroundAgents` +
`cancelTrackedExecutionsForSession`, plus tmux/CLI teardown).

Each layer below this was traced and **confirmed correct** — not assumed:

1. **Scheduler → workshop-turn context.** `registerScheduleRunContext(runID)`
   creates a real `context.WithCancel`; `runJob`/`executeWorkshopJob` receive
   that exact context; `executeWorkshopJob`'s per-turn loop calls
   `waitForConversationTurnTree`, whose `select` includes
   `case <-ctx.Done(): return ctx.Err()`. Cancellation here correctly aborts
   the *outer* workshop-chat turn loop and produces the "stopped" status the
   UI and run history show.
2. **The `message_sequence` item loop** (`executeMessageSequenceStep`,
   `for _, item := range plannedItems`) stops on *any* non-nil error from
   `executeMessageSequenceItem` — including a wrapped `context.Canceled` — so
   if the context reaching this loop were canceled, it would correctly not
   proceed to the next item.
3. **The core per-turn LLM/tool-calling loop**, `askWithHistory`
   (`mcpagent/agent/conversation.go`), checks `agentCtx.Err() != nil` at the
   top of every turn and returns `fmt.Errorf("conversation cancelled: %w",
   agentCtx.Err())` — confirmed for both the plain API path and the
   coding-CLI continuation path (`continueAgentSessionWithHistory` calls the
   identical `askWithHistory`).

**The break is at the next layer down.**
`cancelTrackedExecutionsForSession` (`workflow_execution_tracker.go:359`):

```go
for _, exec := range api.trackedWorkflowExecutions {
    if exec == nil || exec.SessionID != sessionID || exec.Status != trackedExecutionStatusRunning {
        continue
    }
    exec.Status = trackedExecutionStatusCanceled
    exec.CompletedAt = &now
}
```

and `cancelBackgroundAgents` (`session_lifecycle.go:439`, `bgAgentRegistry.CancelAll(sessionID)`)
both **only mutate an in-memory status field in a registry.** Neither calls a
`context.CancelFunc`, neither signals a channel, neither touches the goroutine
that is actually driving the execution. The status flag is read by
*watchers* — `waitForConversationTurnTree`'s poller sees the flip and stops
waiting, which is why the *schedule's own* view correctly reports "stopped."

But the goroutine that actually runs a dispatched `run_full_workflow`
execution — the one driving `executeMessageSequenceStep`'s item-by-item
loop — is not one of these watchers. Nothing in its own call path reads
`trackedWorkflowExecutions[...].Status` or holds a context tied to this
cancellation. It has no way to learn it has been marked canceled, so it keeps
running exactly as if Stop had never been clicked.

**In short: marking a tracked execution canceled tells observers to stop
watching it. It does not tell the worker to stop working.**

## Why this matters more than it looks

The schedule's recorded status, the run history entry, and the UI are all
*correct and consistent with each other* — they all say "stopped." There is
no error, no crash, no visible inconsistency to notice. The only way to see
the bug is to watch whether the actual downstream side effect (a post, a
write, an outbound call) happens anyway. That makes it exactly the shape of
bug this register has repeatedly found today (PLAT-116/117 and others): the
platform is confidently and consistently reporting a state that isn't true.

## Required repair (not started)

1. Find the dispatch site that spawns the goroutine actually driving a
   `run_full_workflow` execution (most likely inside the tool handler that
   `run_full_workflow` resolves to) and confirm whether it already has access
   to a per-execution `context.Context`/`CancelFunc`, or needs one added.
2. Wire that cancel func into `cancelTrackedExecutionsForSession` (and
   `cancelBackgroundAgents`, if background agents have the same gap) so
   canceling the registry entry also cancels the actual work, not only the
   status watchers.
3. A test that starts a `message_sequence` with at least two `user_message`
   items, cancels mid-way through the first, and asserts the second item's
   `executeMessageSequenceItem`/`executeMessageSequenceUserMessage` is never
   called — fail-before/pass-after against the real execution path, not a
   mock, matching this register's standing discipline for coding-agent code.
4. Live reverify: stop a real running schedule mid-`message_sequence` and
   confirm no further queued item executes.

## Not fixed here

- The actual fix. This ticket documents a confirmed root cause; the dispatch
  site itself has not been located.
- Whether `cancelBackgroundAgents`/`bgAgentRegistry.CancelAll` has the
  identical gap was inferred from its shape (also status-only) but not traced
  with the same rigor as `cancelTrackedExecutionsForSession` — worth
  confirming in the same follow-up rather than assuming symmetry.

## Correction to the analysis above (2026-08-18)

Two claims in the root-cause section did not survive being checked.

**`cancelBackgroundAgents` is not status-only.** The ticket inferred this "from
its shape ... but not traced with the same rigor". Traced now:
`BackgroundAgentRegistry.CancelAll` (`background_agents.go:590`) copies the
session's agents, and for each one that is running it calls `agent.cancel()`
before `agent.SetCanceled()`. The cancel func is real — `WorkshopExecutionStart`
carries a `Cancel context.CancelFunc`, the dispatch sites populate it, and
`workshopExecutionBgNotifier.OnExecutionStart` stores it on the
`BackgroundAgent`. So the chain from Stop to the worker's context *is* wired.

**The dispatch site did not need locating.** Of the eighteen
`WorkshopExecutionStart` constructions, thirteen set `Cancel`. The five that do
not are not dispatch sites: four in `planning_exports.go` mirror progress events
into the registry as observers, and the one in `controller_message_sequence.go`
registers a *notification* for an item that runs inline in the caller's
goroutine — there is no separate goroutine to cancel.

## What was actually wrong

The queue's halting was conditional on someone else's conversion.

`executeMessageSequenceStep` returns on any item error, and cancellation is
*expected* to arrive as an item error. But that only holds if some layer beneath
converts a cancelled context into a failure, and session teardown races that
conversion: a coding-CLI turn whose pane is being killed can return a
truncated-but-plausible result rather than an error. The queue reads that as
success and starts the next item — whose side effect is real and outbound.

Nothing in either loop asked the context directly before beginning new work.

## Fix

A pre-item gate in the message_sequence queue and a pre-step gate in the
workflow step loop. Both refuse to *start* new work once the run's context is
cancelled, independent of what the previous item or step reported.

Deliberately gates rather than aborts: an item already in flight unwinds through
its own error path. The guarantee is narrower and more defensible — a cancelled
run never begins anything new.

## Test coverage, and what is not covered

`messageSequenceHaltedBeforeItem` is directly covered, including the specific
reported shape: cancel during the first item, assert the second and third never
start. Verified fail-before/pass-after.

The end-to-end test this ticket originally asked for — driving the real
`executeMessageSequenceStep` and asserting `executeMessageSequenceUserMessage`
is never reached — is **not** written. No harness exists to drive that function:
it requires a constructed orchestrator with plan state, session persistence and
workspace IO, and the existing message_sequence tests all target smaller units.
Building that harness is worthwhile but is its own piece of work.

Live reverify remains outstanding: stop a real running schedule mid-sequence and
confirm no further queued item executes.

## Live reverify (2026-08-20): reproduced again, from a third mechanism

The requested live reverify happened — the user stopped the `upwork` schedule
twice while it was mid-`message_sequence` — and the bug still reproduced. Real
work ran after both stops: Step 2 (`run_full_workflow(group_name="daily-bid",
...)`) executed to completion, 22 seconds of real Claude Code activity, after
the session had already been marked stopped.

Reconstructed second-by-second from `server_debug.log` for session
`schedule-cron--78ba88d0_1787198353286401000`:

```
09:32:10  first Stop click. ACTIVE_SESSION -> stopped. Log itself flags the
          gap: "WARNING 1 background agent(s) had no cancel func — marked
          canceled but never told to stop (PLAT-130 shape)". Same second:
          Step 2's query is logged as the next item to run — a race between
          the stop and the next item already being in flight.
09:32:11  Stop's own cleanup closes the tmux-backed coding-CLI session.
09:32:14  second Stop click. ACTIVE_SESSION -> stopped again. WORKFLOW_PHASE
          correctly aborts workshop creation for this session (a working
          guard). Immediately after: [CHAT_HISTORY] Active-tab auto-resume:
          session ... tmux is gone; routing through --resume re-launch +
          materialize.
09:32:22  A brand-new Claude Code tmux session is launched.
          StreamWithEvents starts.
09:32:45  It completes normally, 22.231s elapsed, having actually run the
          daily-bid step. [COMPLETION] Preserving terminal status "stopped"
          for the session the entire time — no visible sign in the UI that
          this happened.
```

This is not the mechanism the shipped fix (the message_sequence pre-item gate
and workflow step pre-step gate) covers — those gate *starting the next
queue item*. The mechanism that actually fired here is upstream of the
queue entirely: `handleQuery`'s "Active-tab auto-resume" and "Materialize
guard" blocks (`cmd/server/server.go`, ~5977-6027), which exist to relaunch
a coding-agent's tmux pane when it's gone because of idle-reap or a stale
frontend view. Neither block checked whether the tmux was gone *because Stop
had just killed it*, so an already-in-flight `handleQuery` call for a
message_sequence item that raced the Stop click saw "tmux is gone" and
relaunched it — reviving a session Stop had already terminated twice.

### Fix

Both blocks now also gate on `!api.isSessionMarkedStopped(sessionID)`. This
reuses the exact "STOP-RACE GUARD" pattern already shipped and proven for the
analogous `workflow_phase_tools.go` case (the 2026-04-04 "can't stop" bug —
same shape, different call site: a goroutine in flight when Stop lands,
creating orphaned work).

Confirmed this cannot block a legitimate user resume: earlier in the same
`handleQuery` call, `clearSessionStopped(sessionID)` already runs whenever
the request is a real user message (not `IsAutoNotification` and not
`TriggeredBy == "cron"`). So by the time either relaunch block is reached,
`isSessionMarkedStopped` is still `true` only when the request is an
internal cron/auto-notification continuation of a session nobody has
un-stopped — exactly the shape of this incident's Step 2 query, and never
the shape of an explicit Resume click.

### What this does not fix

- The underlying race that let Step 2's query begin at all while Stop was
  being processed (line 1 of the trace above) is not addressed here — this
  fix stops the *relaunch*, not the race that queued the item in the first
  place. If a future incident shows real work executing without ever
  hitting a "tmux is gone" relaunch, that earlier race needs its own fix.
- No automated regression test. The two gated blocks live deep inside
  `handleQuery`, the same giant HTTP handler this ticket's own prior test
  section already noted has no existing harness for driving its internals
  in isolation ("no harness exists to drive that function... its own piece
  of work"). The fix mirrors an already-tested pattern
  (`workflow_phase_tools.go`'s STOP-RACE GUARD, covered by existing tests
  for that call site) rather than inventing new coverage for this one.
- Live reverify of *this* fix — stop a real schedule mid-sequence again and
  confirm no tmux relaunch happens — is itself still outstanding, same as
  every prior round of this ticket.

## Live reverify, second attempt (2026-08-20): clean

After the `isSessionMarkedStopped` gate shipped, the user stopped a second
real schedule (`schedule-manual--46a9b350_1787203163133842000`, `upwork`
group `toptal-bid`) mid-turn — two Stop clicks, one second apart, while its
one in-flight query was still running. This time, verified directly from
`server_debug.log`, not just the UI:

```
10:49:37  first Stop click. Agent execution context canceled. Session ->
          stopped. 0 background agents to cancel (none running yet).
10:49:39  second Stop click. Same result. Workshop sessions closed.
10:49:47  the in-flight Claude Code tmux is discarded on its own:
          "did not return to prompt after context cancellation: context
          deadline exceeded". COMPLETION_TRACE records outcome=error,
          elapsed=24.366s. StreamWithEvents completes with that error.
10:49:47  [COMPLETION] Preserving terminal status "stopped".
```

No `[CHAT_HISTORY] Active-tab auto-resume` or `Materialize guard` line
appears anywhere in this session's trace — the exact lines that fired and
relaunched a live tmux in the first (upwork daily-bid) reproduction. Nothing
ran after the stop. The UI showed the same thing independently: "Context
cancelled ... conversation cancelled after LLM generation: context
canceled," a real, honest cancellation instead of a silently-completed
turn.

This case didn't have a queued `message_sequence` next item racing the stop
(it was a single-message manual run), so it's a narrower confirmation than
the original incident — it proves the turn-in-flight cancellation path and
the relaunch guard both hold, not that every possible race this ticket has
ever described is closed. Still, it's the first time in this ticket's
history that a live stop-mid-run has been reverified clean rather than
reproducing.
