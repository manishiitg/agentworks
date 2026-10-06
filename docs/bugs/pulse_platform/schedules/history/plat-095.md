[← schedules / history](index.md)

# PLAT-095 — Scheduled messages had no exact lifecycle identity

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | schedules |
| Area | history |
| Summary | P0 — every schedule, upgrade, Pulse turn, and Chief-of-Staff |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — exact query-rooted lifecycle and shared Global Monitor projection; live reverify pending |
| Last synchronized | `2026-08-12` |

- **Priority:** P0 — every schedule, upgrade, Pulse turn, and Chief-of-Staff
  sequence depends on advancing exactly once after the current message finishes.
- **Owner:** query dispatch, background-agent lifecycle, scheduler sequencing,
  runtime activity projection.
- **Supersedes:** the symptom reconciliations in [PLAT-071](../stopping/plat-071.md) and
  [PLAT-094](../../goals/pulse/lifecycle/plat-094.md)).

## Actual defect

The scheduler sent a message and then tried to rediscover whether it had ended
from session-wide symptoms. Two independent algorithms answered that question:

1. `waitForWorkshopIdleWithInactivityTimeout` polled tmux, session busy flags,
   tools, and every execution in the session.
2. `waitForPulseTurnCompletion` watched the session event stream and a settle
   timer, then Pulse ran a third `abortIfTurnStillBusy` recheck.

None of them identified **the message being waited on**. Consequently:

- stale tmux/runtime state could keep a completed message alive;
- quiet time could complete a live message;
- an unrelated child or old turn in the same session could affect the answer;
- a child could finish and release the wait before the main agent processed its
  auto-notification;
- Go then marked still-waiting backup/publish/notify receipts permanently
  failed even though the agent had never reported those outcomes.

The shared busy-signal helper previously recorded in this ticket reduced one
symptom but did not fix the missing identity. Runtime re-verification exposed
that limitation, so the earlier partial solution has been replaced.

## Implemented design

### One identity per sent message

`/api/query` already creates and returns a `query_id`. That ID is now the
canonical execution root for the message; no second turn identifier was added.
`startSessionInternal` dispatches the message and waits on that exact ID.

### One recursive execution tree

First-level background agents inherit the active query ID as
`parent_execution_id`. Nested agents retain their direct parent. Synthetic
auto-notification turns are also tracked and linked to the originating child’s
parent, so the original message remains active while the main agent processes a
child result. A completed child is held open during the narrow interval before
that continuation is registered.

Completion now means:

1. the exact query root is terminal; and
2. every recursively linked descendant is terminal; and
3. every non-suppressed child completion has been dispatched back to its parent.

Events only wake the waiter. Silence, tmux prompt text, session-wide idle, and an
unrelated completion can never declare the message complete.

### One projection for the scheduler and Global Monitor

The execution roots and descendants live in the existing tracked-execution and
background-agent stores consumed by `authoritativeRuntimeSnapshot`. Both global
and workspace-scoped running-workflow APIs now use one enrichment function to
attach `RuntimeState` and `DisplayStatus`. The scheduler waits on the exact tree;
the UI projects the same underlying lifecycle records.

### No inferred Pulse action failures

The scheduler no longer rewrites unresolved backup/publish/notify commands to
`failed`, `timed_out`, or `skipped` on a wait error, panic, interruption, or
server restart. Those durable states change only when an agent explicitly
records the command outcome. Go may expose that a turn failed; it may not invent
the business action’s result.

## Removed code

- `waitForWorkshopIdle*`
- `waitForPulseTurnCompletion`
- `abortIfTurnStillBusy`
- `reconcileSessionBusySignal`
- `pulseStepFailureMustStopBeforeNextTurn`
- automatic unresolved-final-command terminalizers

This is intentionally a replacement, not another fallback layered over the old
algorithms.

## Regression coverage

- an unrelated completed turn in the same session cannot complete the target;
- recursive descendants hold the target open;
- a completed child remains live until its parent notification dispatches;
- exact root failure is returned as a failed turn;
- workspace activity and Global Monitor receive the same runtime/display
  projection;
- unrecorded Pulse final commands remain `waiting` rather than receiving a
  Go-inferred outcome.

## Verification

- `go build ./...` passes.
- All new lifecycle, Global Monitor projection, and Pulse command-state tests
  pass together.
- The full server suite still has the unrelated existing
  `TestWorkflowScheduleTrackingWindowStartSurvivesEmptySchedulerState` failure;
  the terminal-pipe E2E is timing-sensitive (failed once in the full run and
  passed alone). The workflow package retains its two existing prompt-contract
  failures. None touch lifecycle code.
- Live schedule re-verification is pending a server restart with this commit.

## Acceptance

- Every internally sequenced message advances only on its own query-rooted tree.
- Child result processing is part of that tree.
- Global Monitor and scheduler state derive from the same lifecycle records.
- Go never fabricates a Pulse final-command result.

## Follow-up boundary repair

[PLAT-100](../../coding-agents/agents/plat-100.md)) fixes the workshop-launch boundary that could detach a
full workflow or other workshop background execution from this ticket's exact
query root. PLAT-095 defines the canonical lifecycle and waiter; PLAT-100 makes
all workshop descendants participate in that lifecycle.

## 2026-09-17 — Notification delivery ownership integration

The [PLAT-106 notification-ownership follow-up](../../app/chat/plat-106.md#notification-ownership-follow-up--2026-09-17) adds a delivery boundary to this ticket's exact query-rooted lifecycle: a completion must stay in its initiating session, and a known tracked parent in another session cannot authorize delivery. The currently selected chat, shared workflow, or newest human turn is not a replacement owner.

A completed original query remains a valid notification parent when the user asks another question in the same chat. Scheduled continuations still return to their schedule session and must retain the original query tree. The focused conversation-turn/steered-continuation tests passed alongside the new ownership tests. This does not replace the lifecycle waiter or establish live scheduler verification.

Follow-up status: **implemented and tested locally; not deployed; runtime re-verification pending**. PLAT-106 remains the canonical implementation/test record. This note does not close the original ticket or change its assigned agent.
