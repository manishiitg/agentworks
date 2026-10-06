[← goals / evaluation](index.md)

# PLAT-091 — evaluation step children never complete, so their session looks permanently busy and Pulse stalls until the 3-hour ceiling

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | evaluation |
| Summary | P1 — a Pulse pass that reaches the Gate successfully still |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — parent-completion reconciliation shipped and tested; runtime reverify pending |
| Last synchronized | `2026-08-11` |

- **Priority:** P1 — a Pulse pass that reaches the Gate successfully still
  loses Review+Fix, Finalize, backup and notification. The run is not marked
  failed, so nothing signals the loss.
- **Owner:** background-agent completion for evaluation step executions
  (`bgAgentRegistry` lifecycle, evaluation step dispatch)
- **Found on:** live, 2026-08-11, social-media
  `schedule-cron--5227790a_1786440610089109000`

## Evidence

Four background agents were registered for the full-evaluation run and **never
received any completion**. Each one's last log line is its own registration:

```
17:52:36  eval-full-iteration-0/default-1786450956765003000-step-0-msomrwct0g
18:06:34  eval-full-iteration-0/default-1786450956765003000-step-0-mson9usj0i
18:18:16  eval-full-iteration-0/default-1786450956765003000-step-0-msonowed0j
18:22:35  eval-full-iteration-0/default-1786450956765003000-step-0-msonug9q0k
```

Grepping each id for `complete|done|finalize|failed` returns **0 lines**. Their
parent, by contrast, finished cleanly:

```
18:25:33  [FINALIZE_EXEC] exec=eval-full-iteration-0/default-1786450956765003000 … — done
```

So the parent evaluation completed while four of its children stayed
registered as `BGAgentRunning`.

## Why this stalls Pulse

`sessionHasLiveChildWork` (`scheduler.go:3796`) is true when
`bgAgentRegistry.HasRunningAgents(sessionID)` is true.
`backgroundAgentCountsAsLiveActivity` (`background_agents.go:547`) treats
`BGAgentRunning` as live unconditionally — only *completed/failed* agents age
out, via an 8-second grace window. The registry also never deletes entries:
`Register` adds, `OnExecutionComplete` mutates in place, and the only
`Cleanup` call is the explicit `/stop-session` handler.

An agent that never completes therefore pins the session as busy **forever**.

The Pulse Gate turn itself finished normally — its output ends *"Stopping here
for the Review+Fix turn."* But `waitForPulseTurnCompletion` then drains child
work before sending Review+Fix, and that drain never ends:

```
18:54:35  Pulse turn quiet for 10m0s … but child work is still running; continuing to wait
19:04:35  (same)
19:14:35  (same)
19:24:35  (same)
19:34:35  (same)
```

Five consecutive 10-minute waits, 55+ minutes, with **no Pulse child dispatch
in the logs after 18:44** — and the Gate prompt explicitly forbids launching
reviewers, so there should be none.

The comparison is decisive: today's instagram Pulse pass, which completed
Gate → Review+Fix → Finalize normally, logged **zero** quiet-waits. This one
logged five. It is anomalous, not normal pacing.

## Consequence

`schedulerWorkshopLiveChildCeiling` is 3 hours (`scheduler.go:3787`), so the
wait ends around 21:40 with `errWorkshopIdleWaitTimeout` rather than hanging
forever. But the outcome is still: **Review+Fix, Finalize, backup and
notification never run for this Pulse pass.** The Gate's own findings — four
answered-but-unapplied operator decisions, 131 open concerns, 206 unreviewed
plan changes, a DB join defect, and an eval-surfaced contradiction where the
pinned arm followed 4 strangers against a documented decision to stop — are
recorded but never acted on, and nobody is told.

Because the run itself is not marked failed, this looks like a completed run
from outside.

## Where to look

The gap is in the completion path for evaluation step executions specifically.
The parent evaluation execution completes; its `-step-N-` children do not.
Note also that `isWorkflowStepTrackingExecution` (`delegation.go:867`) matches
only `workflow-full-…-step-…`, not the `eval-full-…-step-…` shape these ids
use — worth checking whether these children are being dispatched down a path
whose completion handler was only ever wired for the workflow-step id form.

## Fix shipped (2026-08-11) — direction 2

`BackgroundAgentRegistry.ReconcileOrphanedProgressChildren(sessionID,
parentExecutionID, reason)` settles any still-running progress child whose id
carries the finished parent's `"<parentID>-step-"` prefix. It is called from
`workshopExecutionBgNotifier.OnExecutionComplete` right after the parent
reaches a terminal state.

**Why the parent boundary rather than chasing the missing end event.** A
progress child cannot still be doing work once the execution that owns it has
settled, so this holds regardless of *which* path dropped the end event —
abandoned evaluation, superseded duplicate, or the `todo_task_orchestrator`
success-break waiting on a `TodoTaskStepCompleted` that never comes. Fixing
only the abandoned-evaluation path would have left the other two shapes live.
Direction 1 (make every eval child emit its own end) remains worth doing as
the deeper repair; this stops the stall in every shape today.

Deliberately bounded so it cannot mask a genuine hang:

- **Only descendants of the finished parent.** A different parent's live child
  still holds the session open, which is exactly what the drain-wait is for.
- **Only `-step-` progress children**, matched by prefix — a sibling that
  merely shares the parent's name is untouched.
- **Only agents still `BGAgentRunning`.** A child that already reported its own
  result or failure keeps it, so a real failure is never relabelled as this
  bookkeeping repair.

Tests (`plat091_orphaned_progress_child_test.go`) use the four real ids
captured live. They cover: the session stops reporting live child work once the
grace window lapses (the precise signal the scheduler blocks on); another
parent's running child and a non-`-step-` sibling both survive; already-settled
children keep their own outcomes; an empty parent id is a no-op rather than a
session-wide sweep. Verified to fail with the sweep neutered and pass with it
restored.

## Suggested fix directions (the alternatives considered)

1. **Fix the completion path** so an evaluation step child always reaches a
   terminal status, including on the abandoned/superseded path. This run had a
   known duplicate evaluation — the Gate reports the first manual attempt
   "raced the live run into a throwaway 0/10" — so *abandoned* children are
   very likely the shape that never completes.
2. **Do not let an unterminated child pin a session forever.** A registered
   agent with no completion and no event activity for well beyond any
   plausible child lifetime should stop counting as live, or should be
   reconciled against whether its execution still exists. The 3-hour ceiling
   is a backstop against a hung child, not a substitute for correct
   bookkeeping.
3. **Make the stall visible.** Five identical 10-minute waits produced no
   operator-visible signal; this was only found by reading logs.

## Verification

- `go build ./...` clean.
- Four new regression tests pass; confirmed they fail with the sweep disabled.
- Full suite (`./cmd/server/... ./pkg/orchestrator/...`) shows 24 failures,
  all accounted for: the known 22-failure baseline plus two from another
  session's in-flight work (`TestArtifactDriftAuditsTheSchedule`,
  `TestUpgradeDirectHTMLReportsPreservesPrimaryDocuments`). Zero unexplained.
- **Not yet reverified live** — needs a restart, then a scheduled run whose
  evaluation produces an abandoned or superseded attempt; expect the Pulse
  drain-wait to release into Review+Fix instead of logging repeated
  "child work is still running" waits.
- The social-media run that exposed this (2026-08-11) is unrecovered: it was
  already past the point of repair when the cause was found, and will have
  lost Review+Fix, Finalize, backup and notification for that pass.

## Acceptance

- An evaluation run's step children all reach a terminal status, including
  when an evaluation is superseded or abandoned.
- A Pulse Gate that completes is followed by Review+Fix without waiting on
  children that are not running.
- A session held busy by a non-progressing child surfaces somewhere an
  operator can see, rather than only in `schedule.log`.
