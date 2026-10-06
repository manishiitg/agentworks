[← platform / scheduler-runs](index.md)

# PLAT-191 — four confida-login "scheduler has no misfire recovery" findings were a misdiagnosis; `get_schedule_runs` couldn't show why

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | schedules |
| Area | runs |
| Summary | P2 — no scheduler defect existed; the real gap was a missing |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — `get_schedule_runs` now surfaces skip decisions; `ops-review.md` updated; the four misdiagnosed harness entries below are corrected here since the live `platform_harness_issues`/`platform_harness_occurrences` tables have no status/lifecycle field to mark them closed in place |
| Last synchronized | `2026-08-28` |

- **Priority:** P2 — no scheduler defect existed; the real gap was a missing
  read path that let four separate Technical Review passes independently
  theorize a wrong root cause instead of finding the true one in one query.
- **Owner:** `agent_go/cmd/server/scheduler.go` (`SchedulerService.ListFireDecisions`),
  `agent_go/cmd/server/server.go` (`get_schedule_runs` handler),
  `agent_go/cmd/server/guidance/templates/review/ops-review.md`.
- **Related:** found while auditing `confida-login`'s pending platform-harness
  issues (`_system/pulse-platform.sqlite`, `platform_harness_issues`/
  `platform_harness_occurrences`) for this workflow, unprompted by any other
  ticket.

## The four misdiagnosed entries (correcting the record here)

All four share `workspace_path='Workflow/confida-login'`, `module=technical_review`
or `workflow_review`, and describe the same story escalating across three weeks:

- `harness:workflow-scheduler:weekday-cron-silently-skips-firing` (critical,
  first seen 2026-08-03) — one weekday cron schedule stopped firing with no
  execution record and no error, while a sibling schedule kept working.
- `harness:workflow-scheduler:cron-silently-skips-firing` (high, first seen
  2026-08-17) — reproduced on two demonstrably-enabled schedules.
- `harness:scheduler:missed-slot-recovery` (high, first seen 2026-08-21) —
  claimed root cause: *"the platform scheduler has no durable missed-slot
  (misfire) recovery. next_run is computed forward from process start, so
  every slot that elapses while the platform process is down is dropped
  silently and leaves no record anywhere."*
- `harness:scheduler:missed-slot-silent-recompute` (high, first seen
  2026-08-24) — escalation: all 4 of the workflow's enabled schedules hadn't
  fired in 6 days, `next_run` "silently recomputed forward... skipping the
  entire 08-18..08-24 gap with no trace."

**This diagnosis is factually wrong about the current code**, and was
provably wrong at the time each of the three later entries was filed
(08-17/08-21/08-24) — the scheduler's real misfire-recovery mechanism
(`dueCronOccurrences`, a durable `lastFired` cursor restored on every
restart via `s.latestCronOccurrence`, explicit `WAKE_DETECTED` logging) was
added 2026-08-07, in commit `2ebb0397`, and is a deliberate, well-reasoned
design — its own code comment names and defends against the exact scenario
the harness issues describe.

**What actually happened**, confirmed by directly querying the durable
`schedule_fire_decisions` table in `_system/schedule-state.sqlite` (ground
truth the reviewer never checked): every single occurrence for all four of
`confida-login`'s schedules, from **2026-08-19 through 2026-08-24**, was
evaluated exactly on time (fired within seconds/minutes of `scheduled_for`,
every tick, zero gaps), with decision `skipped_paused`, reason
`"global scheduler pause is active"`. Firing resumed normally 2026-08-25.
The scheduler worked correctly for six straight days; a human (or something)
had flipped a **global scheduler pause** on and it stayed on. There is no
missing recovery mechanism, no silent drop, no unexplained gap — the real
answer was sitting in the durable log with a plain-English reason the whole
time.

## Why four separate reviews missed it

Not reviewer laziness — a real tool gap. `get_schedule_runs` (the only
schedule-history tool a Technical Review session has) reads `schedule_runs`,
which only contains rows for occurrences that actually started. A
`skipped_paused` decision returns early before a run is ever created
(`scheduler.go` around the `IsGloballyPaused` check), so it **never appears
in `get_schedule_runs`'s output at all**. The reviewer wasn't ignoring
evidence — the evidence it needed was invisible through the only tool it had.

The read function for the real data already existed and was already correct
(`schedulerstate.Store.ListFireDecisions`, in `pkg/schedulerstate/store.go`)
but was dead code — confirmed via grep, zero callers anywhere before this fix.

## Fix

1. **`SchedulerService.ListFireDecisions`** (new, `scheduler.go`) — resolves
   `workspacePath` + `scheduleID` to the store's `(scope_type="workflow",
   scope_id=workspacePath)` key and calls the existing
   `schedulerstate.Store.ListFireDecisions`.
2. **`get_schedule_runs` handler** (`server.go`) now also calls this and
   appends a `## Skipped/Non-Run Occurrences` section listing every
   non-`started` decision (`skipped_paused`, `skipped_busy`,
   `missed_scheduler_gap`, etc.) with its real reason, alongside the existing
   Run History section. Tool description updated to say so inline, per this
   session's established policy of stating load-bearing rules directly in
   tool descriptions rather than only in a separately-fetched doc.
3. **`ops-review.md`**'s "Schedule execution model" section gained an
   explicit instruction: check `get_schedule_runs`'s skip list before
   concluding a schedule "silently skipped" or theorizing about a missing
   misfire-recovery mechanism, with this exact incident named as the
   cautionary example. Only escalate a scheduler-code finding once that list
   is checked and doesn't explain the gap.

## Why the four harness-table rows aren't hand-edited

`platform_harness_issues`/`platform_harness_occurrences` have no
status/lifecycle column — they're a pure dedup/occurrence tracker written
only through the platform's own typed Pulse-finding flow at review time, not
something meant to be hand-edited via raw SQL from outside that flow. This
ticket is the durable correction record instead; a future reviewer who finds
those four rows should be pointed here.

## Verification

- `go build ./...` clean.
- New test `TestListFireDecisionsSurfacesSkippedOccurrences` (`scheduler_test.go`)
  proves a recorded `skipped_paused` decision is returned with its real
  reason and correct `scheduled_for`.
- Full `cmd/server`, `cmd/server/guidance`, and `step_based_workflow` suites
  run; failure counts (17, 2, 2 respectively) match the pre-existing baseline
  exactly — confirmed via prior baseline runs this session, no regressions.
- Not yet live-verified: no real Technical Review pass has run against a
  paused schedule since this shipped to confirm the new section actually
  renders and gets read correctly in practice.
