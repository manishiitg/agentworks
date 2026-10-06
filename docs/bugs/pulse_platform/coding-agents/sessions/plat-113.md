[← coding-agents / sessions](index.md)

# PLAT-113 — session turn occupancy is decided by a display flag, so scheduled workflow runs hang

| Field | Value |
|---|---|
| State | in progress |
| Priority | P0 |
| Product | coding-agents |
| Area | sessions |
| Summary | A scheduled workflow run hangs for hours, is killed by the idle-wait watchdog, |

| Field | Value |
|---|---|
| Status | `partially implemented; unverified at runtime` — steps 1 and 2 landed 2026-08-16 (`6397b7cce`, `9f345ef42`). Step 3 pending. **The backend has been down since 2026-08-16 10:08 IST, so neither fix has executed once.** Do not treat this as resolved until a live scheduled run exercises it |
| Priority | P0 |
| Owner | session turn occupancy and auto-notification queueing |
| Reported | 2026-08-16 |
| Related | [PLAT-035](plat-035.md), [PLAT-047](../../schedules/history/plat-047.md)), [PLAT-100](../agents/plat-100.md), [PLAT-105](plat-105.md), [PLAT-108](plat-108.md) |

## Problem

A scheduled workflow run hangs for hours, is killed by the idle-wait watchdog,
blocks the next scheduled run, and then keeps the UI polling a dead session
overnight. The completion detection everyone suspects is **not** at fault: tmux
final assessment and background-agent completion both worked correctly.

## Live reproduction — social-media, 2026-08-15

Session `schedule-cron--5227790a_1786786259588980000`, schedule
`5227790a` (Daily Execution x3, 10:00 / 15:00 / 20:00 IST).

```text
15:00:59  run starts
15:05:02  first synthetic-turn:steer-message-… registered
   …      one roughly every 12 minutes, 25 in total
20:00:59  ⏰ Cron fired for 5227790a (20:00 slot)
20:00:59  ⚠️ LATE_FIRE expected=2026-08-15T09:30:00Z drift=5h1m0s
20:00:59  ⏭️ Schedule is already running, skipping
20:15:10  failed in 18851408ms: workshop idle wait timed out:
          execution query_1786786259597883000 made no progress
          for 10m0s (running_children=10)
20:15:10  [PULSE] workflow did not start in this invocation;
          skipping Gate, reviewers, Fixer, dashboard and publish
21:01     server restarted — polling resumes for the dead session
09:21+1   polling finally stops
10:09+1   [ACTIVE_SESSION] Marked session inactive after verified idle timeout
```

Cost of this one run: **314 minutes** of wall clock, **one skipped scheduled
run**, Pulse's whole review chain skipped, and **23,965 API responses /
4.50 GB** spent polling a session that had already failed.

`run_metadata.json` for the same run records `"status": "completed"` — the work
finished; only the host-side lifecycle did not.

## Root cause

The 25 stale executions were **not** tmux sessions or background agents. Every
one was `synthetic-turn:steer-message-…` with an empty `workspace=`, i.e. the
auto-notification turns created to deliver *successful* background-agent
completions back into the parent conversation
(`background_agents.go:2281`).

There is already a mechanism designed to prevent exactly this. Auto-notifications
are supposed to queue while a turn is running and be delivered afterwards, in a
single batch:

- `isSessionBusyForAutoNotification()` → queue instead of execute
- `queuePendingCompletion()` / `schedulePendingStartNotificationRetry()`
- `drainPendingAutoNotificationsAfterTurn()` — *"Drain only after releasing the
  lane; synthetic turns acquire it"*
- batching already exists: `"[AUTO-NOTIFICATION] Multiple step completions:"`

That mechanism never engaged, because its gate is one line
(`background_agents.go:993`):

```go
if !api.isSessionBusy(sessionID) { return false }
```

and `sessionBusy` is deliberately **not set for workflow turns**
(`server.go:4098`):

```go
// Set user-facing busy state for regular chat turns.
if !isWorkflowPhase {
    api.setSessionBusy(sessionID, true)
```

The run was `mode=workflow`. So: turn running → `sessionBusy` false →
auto-notification skips the queue → `executeSyntheticTurn` registers the turn as
running **and then** blocks on `lane.mu.Lock()`, which the parent turn holds.

Two consequences compound:

1. **The parent judges its own health by counting children that are blocked on
   the parent.** `trackSyntheticConversationTurnStart` runs *before*
   `lockSessionInputLane`, so a turn that has not started — and cannot start —
   is counted in `running_children`. The idle-wait sees 10 such children, no
   progress, and kills a healthy run.
2. **The more successful background work a run does, the faster it dies.** Every
   completed background agent adds one blocked synthetic turn.

`sessionBusy` is a **display flag being used as a concurrency signal.** Those
were the same thing when only chat existed. Workflows separated them, and the
flag still carries one bit for two meanings.

## Why this is really a complexity problem

Ten mechanisms currently answer overlapping versions of "is a turn occupying
this session":

| mechanism | refs in `cmd/server` |
|---|---|
| `trackedExecution` | 221 |
| `sessionBusy` | 58 |
| `pendingCompletions` | 23 |
| `sessionInputLane` | 20 |
| `isSyntheticTurn` | 12 |
| `hasActiveTurnCancel` | 8 |
| `schedulePendingStartNotificationRetry` | 6 |
| `drainPendingAutoNotificationsAfterTurn` | 4 |
| `clearStaleBusyIfNeeded` | 3 |
| `SessionHasBusyCodingTmux` | 2 |

`isSessionBusyForAutoNotification` consults five of them in fourteen lines. Two
of the ten — `clearStaleBusyIfNeeded` and the stale-execution reaper — exist
only to repair drift between the others. Repair code for your own state is the
signal that the state has no single owner.

Only four questions are genuinely distinct, and each is forced by a real
constraint:

| question | why it is irreducible | mechanism |
|---|---|---|
| who may proceed | one tmux pane per session; two turns cannot type into it | the lane |
| what is waiting | work arriving mid-turn must not be lost or delivered early | pending queue |
| is it still alive | the provider is external and can stall silently | idle-wait watchdog |
| what do we display | the UI renders a tree of running work | execution registry |

The remaining six are not new questions. They re-derive *who may proceed* from
weaker evidence — a display flag, a tmux scrape, a synthetic-turn marker — and
then need repair jobs when those disagree with reality.

This is the same shape as [PLAT-106](../../app/chat/plat-106.md)) (transport envelope vs the
event's own session), [PLAT-107](../../app/navigation/plat-107.md)) (declared kind vs execution
identity) and [PLAT-108](plat-108.md) (working directory vs Codex thread ID):
**state inferred from a nearby proxy instead of read from the authority.**

## Required repair

### Step 1 — make the lane authoritative for occupancy ✅ landed 2026-08-16

`sessionInputLane` is not a signal *about* occupancy; it *is* occupancy — a turn
occupies the session exactly when it holds that mutex. Add a read-only accessor
and consult it in `isSessionBusyForAutoNotification` **in addition to** the
existing checks, so the change can only cause more queueing, never less.

Effect: workflow turns queue their auto-notifications like chat turns already
do, and the pileup cannot form.

### Step 2 — register after acquiring, not before ✅ landed 2026-08-16

`trackSyntheticConversationTurnStart` now runs after `lockSessionInputLane`
returns. A turn blocked on the lane has not started and is no longer counted as
a running child of the turn blocking it. The unreachable-session branch returns
before registering, so it no longer completes an execution that was never
started; every path after registration still completes it.

### Step 3 — demote `sessionBusy` to display only

Make it a projection of the lane rather than an independent flag, then delete
`clearStaleBusyIfNeeded` and the busy-related half of the stale reaper once
nothing depends on drift. 58 references, so this is its own change with its own
review — deliberately not bundled here.

## Acceptance

1. A `mode=workflow` turn causes auto-notifications to queue, exactly as a chat
   turn does. Proven by a test that runs a workflow turn and asserts the
   notification is queued rather than executed.
2. No execution is counted in `running_children` before it holds the lane.
3. A run that completes N background agents during one turn produces **one**
   batched auto-notification afterwards, not N blocked synthetic turns.
4. The idle-wait watchdog does not fire on a run whose only "children" are
   queued notifications.
5. A real social-media schedule completes three consecutive daily slots with no
   `already running, skipping` and no idle-wait timeout.
6. After a run ends, polling for that session stops without requiring a server
   restart or shutdown.

## Verification state — 2026-08-16

**Nothing here has run yet.** The backend was shut down at 10:08 IST (the log
ends on `⏳ Still waiting for HTTP handlers (5s elapsed)`) and has not been
restarted, so neither step 1 nor step 2 has executed a single time in a real
process. What is confirmed is only:

- `go build ./...` clean; `cmd/server` green apart from three failures that
  predate this work and fail identically on `main`;
- both new tests were verified to fail when their fix is reverted.

Because the server is down, social-media has also produced **no runs today**.
Its schedules are `0 10,15,20 * * *` and `30 8 * * *` (Asia/Kolkata), so the
08:30 and 10:00 slots were missed outright.

**The next real check is the 15:00 IST execution slot**, if the backend is up by
then. What to look for in `agent_go/logs/schedule.log`:

| signal | broken | fixed |
|---|---|---|
| `⏭️ Schedule … is already running, skipping` | present | absent |
| `⚠️ LATE_FIRE … drift=` | hours | absent or seconds |
| `workshop idle wait timed out … running_children=N` | present, N ≈ 10 | absent |
| `synthetic-turn:steer-message-…` in `[EXEC_TRACKER] Reaping stale` | ~25 | none |
| auto-notifications after the turn | 25 separate | one `Multiple step completions` batch |
| polling after the run ends | continues for hours | stops |

Until that run is observed, this ticket stays `unverified at runtime` regardless
of how green the unit tests are.

## Note on verification

Acceptance 5 and 6 are the ones that matter and neither can be proven by a unit
test. The failure only appears when a real workflow turn runs long enough for
background agents to complete underneath it. A test that constructs the state
directly will pass against the broken code — the same trap recorded in
[PLAT-105](plat-105.md)'s IC-11 anti-requirement.

## 2026-09-17 — Notification delivery ownership integration

The [PLAT-106 notification-ownership follow-up](../../app/chat/plat-106.md#notification-ownership-follow-up--2026-09-17) checks which session may receive a completion before accepted delivery through live steering or the single/batched completion paths. A busy unrelated chat is never an alternate destination for a scheduled completion. The frontend delayed queue now carries its original tab/session rather than following selection, and rejected submissions restore non-stale messages only to that original queue.

This is delivery isolation, not completion of this ticket's sessionBusy demotion or occupancy redesign. Preserve the input-lane ordering and batching guarantees here. The focused background/notification tests passed locally; live acceptance must combine a busy Chat with its own child work and an unrelated scheduled run. The August outage statement below describes the historical verification environment, not a newly checked current outage.

Follow-up status: **implemented and tested locally; not deployed; runtime re-verification pending**. PLAT-106 remains the canonical implementation/test record. This note does not close the original ticket or change its assigned agent.
