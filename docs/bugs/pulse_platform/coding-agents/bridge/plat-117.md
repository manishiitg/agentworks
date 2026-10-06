[← platform / coding-agent-bridge](index.md)

# PLAT-117 — a dropped UI progress event can permanently prevent a turn from completing

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | coding-agents |
| Area | bridge |
| Summary | The Social Media run in session |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — liveness fix shipped and fail-before/pass-after verified; one further decoupling designed but deliberately not built (see last section) |
| Last synchronized | `2026-08-17` |

## 2026-08-20 follow-up — unified execution tracker remained busy

The Social Media run in session
`schedule-manual--5227790a_1787203229327335000` exposed one more copy of the
same lifecycle record. At `15:53:43 IST`, parent
`workflow-full-mt12p13b01` reconciled orphan progress child
`workflow-full-mt12p13b01-step-0-mt17a9lh09`. Its background-agent record and
terminal became terminal, and the provider turn, finalizer, schedule run, and
foreground session all later settled. Global Monitor nevertheless continued
to show `[Execute] Run Today's Actions` as running.

The authoritative runtime snapshot made the split explicit:

- `raw_session_status=completed`
- `foreground_turn.busy=false`
- `terminal_busy=false`
- runtime phase `running` only because `tracked child execution is active`

`workshopExecutionBgNotifier.OnExecutionComplete` called
`ReconcileOrphanedProgressChildren` and emitted the durable completion, but it
did not call `completeTrackedExecution` for each reconciled orphan. The unified
tracker therefore retained a second, independently-running copy that powers
`/api/workflow/running` and Global Monitor.

**Repair:** orphan reconciliation now settles the background-agent record, the
unified tracked execution, and the durable completion together. A product-path
regression test starts the real parent/orphan pair, completes the parent through
`workshopExecutionBgNotifier`, and proves the Global Monitor list becomes empty.
Keeping the retained tmux session alive is intentionally unrelated to this
busy signal.

- **Priority:** P0 — this is the mechanism behind the social-media false-failure
  emails. It makes `terminal()` unreachable, so an affected turn is
  *guaranteed* to end in an idle-wait timeout no matter how well the underlying
  work went, and the operator is then told the workflow never ran.
- **Owner:** background-agent registry (`background_agents.go`), conversation
  turn lifecycle (`conversation_turn_lifecycle.go`), workflow progress bridge
  (`planning_exports.go`), workshop execution notifier (`delegation.go`)
- **Related:** [PLAT-116](plat-116.md) (the false-failure symptom this explains),
  [PLAT-071](../../schedules/runs/plat-071.md)) (regressed separately, see below),
  [PLAT-091](../../goals/evaluation/plat-091.md)) (`ReconcileOrphanedProgressChildren`, the compensator
  this makes unnecessary for liveness), [PLAT-114](plat-114.md) (durable log
  rows left permanently `running` by the same omission),
  [PLAT-112](../../app/ui/plat-112.md)) (turned off the only individual consumer of these
  records)

## The category error

`workflowProgressBridge` (`planning_exports.go:1756-1793`) turns each workflow
step's start/end progress events into a **background-agent record**, so the UI
can show per-step progress. `workshopExecutionBgNotifier` registers them, and
its own doc comment states the intent plainly:

> registering workshop step/background executions in bgAgentRegistry **so that
> HasRunningAgents() returns true and the frontend keeps polling for events**

That intent is a *display* concern. The problem is that `bgAgentRegistry`
answers two very different questions from one data structure:

| Question | Read by | Needs |
|---|---|---|
| "Is background work happening?" (display, polling) | `HasRunningAgents()` — 9 call sites | best-effort is fine |
| "May this turn finish?" (lifecycle) | `conversationTurnTreeSnapshot` → `RunningChildren` → `terminal()` | must be exact |

Progress mirrors are produced by best-effort event pairing: a start mints an ID
cached under `agentType:stepIndex:agentName`, and only a matching end closes it.
Any dropped, mismatched, or never-emitted end leaves a record open forever.
That is tolerable for a spinner. It is not tolerable for turn completion — and
today it feeds both.

**So a dropped UI progress event can permanently prevent a real turn from
completing.** The work itself is meanwhile recorded in two more reliable
places: the run's own durable per-step files on disk
(`runs/<iteration>/<group>/execution/<step>/session.json` — what Pulse Gate
actually reads), and the parent `workflow-full-<id>` agent, which is registered
for the entire run and already supplies the aggregate display signal.

## Live evidence (social-media, 2026-08-16)

Session `schedule-manual--5227790a_1786893680879441000`, schedule
`5227790a` ("Daily Execution x3").

1. `15:22:25Z` parent `workflow-full-msvyee8q01` starts. Two step-0 progress
   mirrors open at `15:25:39Z` and `17:46:32Z` (same step index, different
   agentType/agentName keys) and never receive an end event.
2. `18:57:13Z` the parent completes — the real work is done.
3. `00:27:13 IST` `ReconcileOrphanedProgressChildren` (PLAT-091) settles both
   orphans via `agent.SetError(...)`. That sets `Status=Failed` and
   `CompletedAt`, but **does not** mark them notified and **never calls**
   `emitBackgroundAgentCompleted`.
4. `conversation_turn_lifecycle.go:209-211` deliberately counts a
   completed/failed-but-unnotified child as **running** (an anti-race measure
   for real children). Both orphans therefore stay "running" permanently.
5. `terminal()` requires `RunningChildren == 0`. With two permanent phantoms the
   tree **can never be terminal** — the turn was structurally guaranteed to time
   out regardless of what the provider did.
6. Worse, `RunningChildren > 0` also triggers the three-hour
   `schedulerWorkshopLiveChildCeiling` grace, so the run burned **4h09m**
   instead of failing fast.
7. The turn's real answer arrived `00:50:43 IST` — *"Done — the pinned execution
   route ran exclusively... One original post landed and was verified"* — with a
   real post live on X. Ten minutes of no further progress later, the wait
   timed out at `01:00:43` reporting `running_children=2`, and the operator was
   emailed **"Workflow did not start. No results were produced."**

Corroboration from PLAT-114's durable log: those exact two agent ids
(`workflow-full-msvyee8q01-step-0-msvyijui03`, `-step-0-msw3jq3i0f`) are still
stored `status='running', completed_at=NULL`, because step 3's omission also
skips the durable-log completion hook. Same omission, two visible symptoms.

## Why the existing compensator did not save it

`ReconcileOrphanedProgressChildren` exists precisely to settle orphans left by
missing end events ("a finished parent cannot still have live progress
children"). It is a compensator for unreliable event pairing, and it has its own
gap: it settles the record's *status* but not its *notified* flag, and the
liveness snapshot treats settled-but-unnotified as running anyway. Making the
compensator more correct is not the fix — the fix is that a progress mirror
should not be able to hold a turn open in the first place.

## Required repair

**1. Make the distinction first-class, not an inline filter.** One authoritative
predicate for "this record is a progress mirror, not work", derived from the
declared `Kind` (`workflow_step`) with the legacy `execution_type` metadata
fallback, mirroring the existing `isWorkflowStepTrackingExecution`
classification. Not re-sniffed per call site.

**2. Split the two registry questions explicitly** so a future consumer must
choose rather than inherit the wrong one by default:
   - `HasRunningAgents()` — unchanged, the *display* question. Progress mirrors
     still count, so no UI/polling regression at any of its 9 call sites.
   - a new *work* predicate — excludes progress mirrors — documented as the
     question lifecycle code must ask.

**3. `conversationTurnTreeSnapshot` counts only real work in `RunningChildren`.**
Progress mirrors still contribute `LastProgressAt`: an advancing step is real
evidence the turn is alive, it just cannot hold the turn open by itself.
Excluding them cannot end a turn early, because a step record is minted from its
parent's id (`fmt.Sprintf("%s-step-%d-%s", b.parentID, ...)`) and so can never
outlive a parent that is not itself registered.

**4. Fix `ReconcileOrphanedProgressChildren` to settle completely** — mark
notified and emit the completion — so PLAT-114's durable log stops accumulating
permanently-`running` rows. Still required after (3), because the log is wrong
today independently of liveness.

**5. Restore PLAT-071 (separate regression, found while investigating this).**
Its fix shipped 2026-08-10, was extended by PLAT-084 on the 11th, and was
dropped whole on 2026-08-13 by `d18e071e1` ("Unify scheduled turn lifecycle and
runtime tab routing") when `waitForWorkshopIdle` was replaced by
`waitForConversationTurnTree`. `workshopRunStartedDuringInvocation` has had zero
callers since, and `ProducedRunEvidence` is once again only computed on the
success path — so any failed turn reports "the workflow did not run" without
anything having looked. Both its unit tests still pass, because they call the
helper directly instead of driving `executeWorkshopJob`.

## Not fixed here

- **Whether these registry records need to exist at all.** They exist to keep
  the frontend polling, but the parent already supplies that aggregate, the
  per-step *events* reach the UI independently of the registry, and PLAT-112
  (2026-08-16) turned off the diagnostic rail that was their only individual
  consumer. Deleting the mirror — and `ReconcileOrphanedProgressChildren` with
  it — is likely correct and strictly simpler, but requires proving the runtime
  tab's live progress is event-driven rather than registry-driven. Deliberately
  not assumed.
- **PLAT-116's own root cause** is narrowed but not closed by this. This
  explains the `running_children=2` incident completely; the earlier
  `running_children=0` incident (2026-08-16 19:08) has a different shape and
  still needs its own trace.

## Acceptance

- A turn whose only outstanding children are progress mirrors reaches
  `terminal()` and completes normally.
- `HasRunningAgents()` behaviour is unchanged at all 9 call sites.
- An orphaned progress mirror settles completely: not counted as running, and
  written to the durable log as terminal.
- A failed turn that produced real run evidence never reports "the workflow did
  not run".
- Every one of the above is proven by a test that reaches the state through the
  product path, not by constructing it — the specific gap that let PLAT-071 be
  deleted with its tests still green.

## Correction (2026-08-17): the first predicate missed the actual records

The first version of `IsProgressMirror` matched `Kind == "workflow_step"` (plus
the legacy `execution_type` metadata). Checked against the live durable log
while investigating a later run, **the two orphans that caused this incident are
stored `kind=orchestrator`** — so the predicate did not match them, and the fix
did not fix the reported bug.

The cause is a rule stated in `OnExecutionStart` itself: *"a declared kind always
wins"*. The `workflow_step` override is applied only when the creator declared
nothing, and these records declare `orchestrator`. Their identity is in their
ids (`workflow-full-<parent>-step-<n>-<token>`), which is exactly what
`isWorkflowStepTrackingExecution` already recognises — so the predicate now
delegates to that canonical classifier instead of re-deciding from `Kind`.

**Why the tests did not catch it, which is the more important part.** The
reproduction constructed its orphans with `Kind: "workflow_step"` — the value the
fix expected, not the value production stores. It therefore passed against a
record shape that never occurs. This is the third time in this register that a
test certified a fix by building the state it wanted instead of the state the
product produces (PLAT-105's IC-11, PLAT-071's deleted call site, now this).
Verified after correcting: reverting the predicate makes the reproduction fail
with `RunningChildren = 2`, the exact live symptom.

Real kinds observed in production, for anyone extending this later:
`full_run` (the parent), `orchestrator` (per-step progress records),
`message_sequence_item` and `sub_agent` (real work — must keep holding a turn
open), and `workflow_step` (rare, only where nothing was declared).

## Simplification pass (2026-08-17): what was done, and what was rejected

After the fix landed, the surrounding machinery was reviewed for whether it is
essential or accidental. `background_agents.go` is 2,642 lines with 36
notification-related functions, so the question was worth asking properly rather
than assuming.

**Done — the four notification booleans are now three concerns, not four flags.**
`notified` and `notificationInFlight` encoded a single lifecycle
(none → in flight → delivered) as two independent booleans, making illegal
combinations representable and forcing every reader to consult both. They are
now one `completionNotificationState`. `startNotified` and `terminalNotified`
were deliberately left alone: they are separate axes, not stages of the same
machine, and merging them would have been a worse model, not a simpler one.

**Done — deleted `BackgroundAgentsStatusBar`.** It rendered a pill per background
agent with a live elapsed timer, built purely by folding
`background_agent_started`/`completed` events. It was exported from the events
barrel and imported by nothing. Genuinely dead.

**Rejected — deleting the progress mirrors outright.** Investigated seriously,
because no UI justification survives: the status bar above was dead, PLAT-112
turned off the diagnostic rail, and Global Monitor works entirely off
session-level status and aggregate booleans (`statusTone` →
`headerStatusLabel`), never per-step records. But two things make deletion the
wrong move:

1. **They are registered in two stores, not one.** `OnExecutionStart` calls both
   `bgAgentRegistry.Register` and `trackWorkshopExecutionStart`, and
   `conversationTurnTreeSnapshot` walks both. Removing the registry half leaves
   them in `trackedWorkflowExecutions`, still counted as running children, still
   orphanable — the same bug through the other door.
2. **PLAT-084 depends on them.** `scheduledWorkflowStepProducedEvidence` reads
   exactly these records to tell Pulse that an `execute_step`-driven schedule did
   real work. Deleting them silently removes that evidence signal — the same
   class of failure this ticket is about.

So they are not legacy code; they are dual-purpose infrastructure of which
exactly one use (vetoing turn completion) was wrong. Classification plus
exclusion from liveness is the correct shape, not removal. Recorded here so this
is not re-opened later as easy cleanup.

**Rejected — merging the two pending queues.** `pendingCompletions` and
`pendingStartNotifications` have near-identical queue/drain/dedup helpers, which
looks like an obvious dedup. It is not: `pendingMu` guards both the queue map
*and* its `completionRetryScheduled` guard map, and the two are read together
atomically in three files (e.g. `server.go:8090`). Extracting a queue type with
its own mutex would split that lock, trading ~30 lines of trivial map code for a
concurrency change in the delivery path that just caused a P0. Not worth it.

## Built: the notification hold is now bounded

The design sketched here first was to close the completion race by *ordering* —
register the owed continuation as a real tracked execution at completion time,
so liveness never needs to read notification state. Investigating the delivery
path before building it showed a simpler answer that is strictly better, so that
design was dropped in favour of this one.

**What the delivery path actually does:** a queued completion is retried every
5s by a self-re-arming timer, and is explicitly *discarded* if the session
becomes unreachable. So the legitimate hand-off window is seconds, and — this is
the part that decides it — the hold only has any effect once nothing else in the
tree is running, because a busy session keeps the tree alive on its own.

**So the hold is now bounded rather than replaced.** A finished-but-unnotified
child still holds its turn open, but only for `continuationHandoffGrace`
(2 minutes) after it completed. That is orders of magnitude more than any real
hand-off needs, and it converts *every* stuck-notification path from a permanent
hang into a bounded delay — including two beyond this ticket's own bug: the
deliberate discard when a session goes unreachable with completions pending, and
any future path that marks a child terminal without settling its notification.

**Why this is better than the placeholder design.** The placeholder had to be
registered on every path that owes a continuation and resolved on every path
that stops owing one — including the queue-fallback retry, where nothing is
registered yet. A placeholder that leaks is the same permanent hang in a new
place, so it would have traded a known bug for an unknown one. The bound needs
no new state, cannot leak, and fails in the safe direction: worst case a turn
completes two minutes later than ideal, never earlier than correct.

**Verified** by simulating the old unbounded behaviour (forcing the grace check
to always pass): `TestUnnotifiedChildHoldsTurnOnlyBriefly` fails on the
finished-long-ago case with `RunningChildren = 1`, and passes once restored,
while the just-finished and inside-grace cases keep holding the turn open in
both — so the race protection is demonstrably intact, not merely asserted.

## Original design, superseded by the bound above


The remaining coupling is that `conversationTurnTreeSnapshot` reads notification
state at all:

```go
if (Completed || Failed) && !suppressNotification && !CompletionNotified {
    status = trackedExecutionStatusRunning
}
```

This exists to close a real race: a finished child is about to produce a
continuation (a synthetic turn, or a steered message into a live turn), and
declaring the tree terminal in that gap would let the scheduler advance to Pulse
while the agent is still working. Holding the child "running" until its
notification is confirmed closes the window.

The cost is that turn completion now depends on notification bookkeeping, so any
path that marks a child terminal without marking it notified hangs the turn
permanently — which is exactly how this ticket's bug worked.

**The fix is to close the race by ordering rather than by a flag**: when a child
completes and a continuation is owed, register that continuation in the tree as a
real tracked execution *at completion time*, and resolve it when the continuation
actually starts or is suppressed. The steered path already does this
(`trackSyntheticConversationTurnStart` is called before the child's notification
hold is released, precisely so "the exact conversation tree can never appear
terminal in the hand-off gap") — it simply is not the general rule. With a
placeholder always present, liveness reduces to the two conditions it should
have: the model gave its final message, and no real work is still running.

**Why it was not built here.** The queue-fallback path is the hard part: when a
steer fails, the completion returns to the pending queue with a retry owed and no
continuation registered. Dropping the flag before that path registers a
placeholder would reopen the race in the *early*-completion direction — ending
turns before their work is done, which is worse than ending them late. This is
turn-lifecycle code on every scheduled run and deserves its own change with its
own tests, not a same-session addition to the fix that was already verified.

## 2026-09-17 — Notification delivery ownership integration

The [PLAT-106 notification-ownership follow-up](../../app/ui/plat-106.md#notification-ownership-follow-up--2026-09-17) protects the same background-agent registry and completion paths: registration cannot re-home an owned execution, and foreign-session ownership or a known foreign tracked parent excludes a completion from the destination's notification batch/live steer.

Ownership checks must not be interpreted as a redesign of notification liveness. This follow-up does not implement the continuation-placeholder proposal or remove the completion-notified hold described later in this ticket. Legitimate same-session completion/retry and progress-mirror behavior must remain intact; the focused background/notification and conversation-turn tests passed. Live verification remains a separate requirement.

Follow-up status: **implemented and tested locally; not deployed; runtime re-verification pending**. PLAT-106 remains the canonical implementation/test record. This note does not close the original ticket or change its assigned agent.
