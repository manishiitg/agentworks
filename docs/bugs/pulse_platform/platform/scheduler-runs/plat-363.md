[← platform / scheduler-runs](index.md)

# PLAT-363 — Scheduled runs lost to pauses and to Pulse, and Pulse did not act

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | platform |
| Area | scheduler-runs |
| Summary | explains 21 missed local runs: 18 were skipped by a global "Pause all", and 3 were refused while a Pulse fix run held the workflow's lock. |

| Coordination | Value |
|---|---|
| State | Pushed to main; local restart and RTS deploy pending |
| Date | 2026-09-28 |
| Owner | scheduler-runs |
| Related subsystem | pulse-governance |

## Problem

Locally, 21 scheduled runs were missed on Sep 25–28. The scheduler was alive
and fired every one of them on time (a heartbeat every minute, no gaps). None
of them ran:

- **18 were skipped by a global "Pause all".**
  - Sep 26, 12:07–~15:30 IST.
  - Sep 27, 00:20–20:09 IST.
  - Global pauses have been switched on more than 50 times since late July.
  - Resuming cleared `paused_by`, and the server logs had rotated, so nobody
    could tell who paused.
- **3 were refused because a Pulse fix run held the workflow's schedule
  lock.** With the default `skip` policy, each was dropped:
  - upwork "Daily Toptal Find + Bid", Sep 26 18:00;
  - social-media "Build-in-Public Post", Sep 26 18:00;
  - social-media weekly "Strategy Discovery", Sep 28 08:00. The fix run
    ended a minute later.

Pulse saw part of this. social-media's Technical Review noted "four days lost
to skipped schedules" and concluded nothing needed fixing. The reasons:
- the evidence framed every non-run as "the scheduler working as designed";
- changing `collision_policy` was reserved for Architecture;
- the reviewer could not tell that the blocker was Pulse itself;
- a skipped run triggers no review.

## What changed

1. **Fix runs share the Pulse lock** (`9fccab474`). `pulse-fix-run` shares the
   one-Pulse-per-workflow lock with scheduled Pulse, instead of the workflow's
   schedule lock, so Pulse no longer blocks the workflow's schedules.
2. **Pulse sees lost runs** (`get_schedule_runs`). Non-started occurrences are
   grouped as *Lost runs*, *Deferred runs* and *Deliberately not run*
   (`schedule_fire_decision_kind.go`). A busy skip is lost work and names the
   schedule's current policy; a pause is not a defect.
3. **Technical Review may repair lost runs.** When a schedule that does real
   work loses a run to a busy workflow, it may set
   `collision_policy=queue_latest` with a fitting `max_start_delay_minutes`
   itself, using `update_schedule`. The Gate makes Technical Review due for
   such lost runs. Parallel mode, dependencies and reordering stay with
   Architecture and the user.
4. **The missed badge says why.** The missed status uses the scheduler's
   recorded decision (paused, busy, expired, failed to start) instead of a
   bare "no run", and the UI shows it in plain words.
5. **Pause history.** Every pause and resume is appended to
   `config/scheduler-pause-log.jsonl` and logged, with the user, the client
   label and the User-Agent (browser vs. agent or script). The paused banner
   shows who paused and from where.
6. **Resume catch-up.** A resume returns the runs the pause skipped, filtered
   to workflows the caller can access. The Schedule Runs overview offers them
   for a manual catch-up, one run per chosen schedule. Paused runs never run
   on their own, since a burst of late sends can be wrong.
7. **No double-running a step.** Pulse holds off a step the workflow is
   running at that moment. A process-wide registry of running steps plus
   full-workflow `CurrentStepID` backs a `StepBusyForPulse` check on Pulse
   workshops only. `execute_step` then says to wait for that run's result
   instead of launching a duplicate.
8. **Local config.** 16 local schedules that do real work moved from `skip`
   to `queue_latest`, each with a start window that fits its purpose (bids,
   posts, engagement, drafts, growth, weekly strategy, the Hetzner audit).
   Pure checks and reminders stay on `skip`.

## Verification

- Unit tests:
  - the fix-run lock key;
  - lost/deferred/deliberate grouping;
  - pause and resume recording through the handler, with no event on an
    unchanged save;
  - the Pulse step hold (own run and other steps free; the workflow's run
    holds);
  - Pulse session detection;
  - the catch-up panel.
- Full `cmd/server/...`, step package and scheduler UI suites pass, except the
  four failures that already fail on main without these changes.

## Left

- [ ] Live check after a restart: a busy skip shows under *Lost runs*; the next
  Pulse repairs a real-work schedule; pause and resume leave history; the
  catch-up panel appears after a resume.
- [ ] Find out who paused on Sep 26–27. From now on the pause log answers it.

## Register notes

[PLAT-363](plat-363.md) explains 21 missed local
runs: 18 were skipped by a global "Pause all", and 3 were refused while a
Pulse fix run held the workflow's lock. Fix runs now use Pulse's own lock, so
they no longer block schedules.
- **Pulse:** its evidence now separates lost runs from deliberate skips.
  Technical Review may set `queue_latest` on a real-work schedule that lost a
  run, and Pulse holds off a step the workflow is running right now.
- **UI:** the missed badge says why a run didn't happen, pause and resume leave
  a history, and a resume offers a catch-up of the skipped runs.
Pushed to main; restart and deploy pending.
