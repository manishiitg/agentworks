[← schedules / occurrences](index.md)

# PLAT-080 — an old cron schedule with no durable fire-decision row restarted from “now,” silently losing earlier due occurrences

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | schedules |
| Area | occurrences |
| Summary | P1 — enabled recurring work can be skipped without a run or a |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — runtime reverify pending |
| Last synchronized | `2026-08-10` |

- **Priority:** P1 — enabled recurring work can be skipped without a run or a
  skip-decision record after scheduler state is introduced, replaced, or lost
- **Owner:** scheduler cron-cursor bootstrap (`SchedulerService.LoadSchedule`)
- **Found on:** PLAT-073 cluster E, build-in-public `7c4ac1525a8b45f7`

## Evidence and root cause

The original finding grouped several schedule observations together. Most were
already explained by the durable occurrence ledger: LinkedIn's 2026-08-04
weekly run is recorded as `skipped_paused`, and Build-in-Public's daily gaps
are recorded as `skipped_paused` or `skipped_busy`. The legacy
`config/schedule-execution-history.json` only records launches, so reviewing it
alone made those explained skips look silent.

Two Build-in-Public Sunday schedules exposed the remaining real boundary:

- `3dcf6bc0-b58c-49f9-bbc7-125f0b1e3b06` has a persisted tracking window from
  `2026-08-05T12:28:59Z` and was due on 2026-08-09 at 08:00 Asia/Kolkata;
- `1e69ef5a-265f-4e86-a7e8-2ecef4623129` has a persisted tracking window from
  `2026-08-07T03:53:07Z` and was due on 2026-08-09 at 10:00 Asia/Kolkata;
- neither schedule has an execution record **or** any row in
  `_system/schedule-state.sqlite:schedule_fire_decisions` for that occurrence;
- after registration on 2026-08-10, both immediately reported their next run
  as 2026-08-16.

`SchedulerService.LoadSchedule` explained the loss exactly. When
`LatestFireDecision` returned no row, it initialized `lastFired` to
`time.Now()-30s`. `dueCronOccurrences` therefore never saw anything between the
schedule's real tracking-window start and this process start. This is different
from a new schedule (whose tracking window really is now) and made an old
schedule with an empty/replaced state DB indistinguishable from a new one.

## Fix

For workflow cron schedules, the durable fire-decision row remains the first
choice. When it is absent, `LoadSchedule` now resumes from the schedule's
persisted `WindowStartAt` in `config/schedule-execution-history.json`. A truly
new schedule still gets a window beginning now, so it does not backfill time
before it existed. Existing history retention bounds recovery to the supported
window.

`TestWorkflowScheduleTrackingWindowStartSurvivesEmptySchedulerState` pins the
fallback source so an empty scheduler-state database cannot erase the older
tracking cursor again.

## Classification of the remaining E findings

- LinkedIn `3e42ae71ebde573b`: **not an open platform bug**. Its 2026-08-04
  occurrence has a durable `skipped_paused` decision; the reviewer consulted
  the launch-only history instead of the scheduler ledger.
- LinkedIn `3565d07c3f3c63d7`: **stale observation / current-state mismatch**.
  The cited manual Engage run completed, and current cron occurrences are
  recorded normally. It should be rechecked through `list_schedules` plus the
  durable decision ledger, not inferred from a past `next_run` projection.
- Build-in-Public `7c4ac1525a8b45f7`: mixed finding. Daily omissions have
  `skipped_paused`/`skipped_busy` decisions; only the two Sunday schedules
  exercise PLAT-080.

## Verification

- Focused scheduler/history tests pass.
- `go build ./...` passes.
- `go test ./cmd/server/... ./pkg/orchestrator/agents/workflow/step_based_workflow/...`
  remains at exactly 22 pre-existing failures (19 guidance, one virtual-tool,
  two step-based-workflow); this patch added none.
- Runtime reverify: restart with a workflow schedule whose tracking window
  predates an unrecorded due occurrence; confirm the occurrence receives a
  durable `attempted` plus terminal decision and is not advanced silently.
