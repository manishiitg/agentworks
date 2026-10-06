[← platform / scheduler-runs](index.md)

# PLAT-041 — an expected cron occurrence can disappear without a decision

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | platform |
| Area | scheduler-runs |
| Summary | scheduler wall-clock evaluation and durable fire decisions |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P0
- **Owner:** scheduler wall-clock evaluation and durable fire decisions
- **Source finding:** `HARNESS-SCHED-SILENT-SKIP`
- **Source workflow:** Confida QA
- **Problem:** the scheduler kept `registeredJob.lastFired` only in memory and
  wrote a fire decision only after `triggerSchedule` began. A restart, reload,
  or missed evaluation could therefore erase an expected occurrence without a
  run, skip, or error record.
- **Implementation:** fire decisions now carry `scheduled_for` as durable
  occurrence identity and are upserted through attempted/start/skip/failure.
  Cron registration restores its cursor from the latest cron decision, manual
  triggers cannot move that cursor, scheduler gaps are classified, and only
  the latest due occurrence executes as catch-up. The full seven-day,
  minute-resolution recovery window is retained per trigger source; manual
  decisions cannot evict cron occurrence history.
- **Verification:** scheduler-state tests cover occurrence upsert and cron/manual
  separation; scheduler tests cover multi-occurrence gap detection.
- **Runtime acceptance:** restart or sleep across a real scheduled time and
  confirm every expected occurrence produces exactly one durable decision,
  with the newest occurrence starting once and older retained occurrences
  marked `missed_scheduler_gap`.
