[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-533 — One-time schedules for Crew and Code ("check the deploy in 3 hours")

| Coordination | Value |
|---|---|
| State | built on main; not deployed; not yet tried live (owner to test) |
| Date | 2026-10-05 |
| Owner | scheduler-runs |

## Source

Owner: "in crew/code ... do we have something like one time schedule ... like check something after 3 hours" — no; Crew/Code schedules were cron or every-N-hours only (`create_project_schedule` required a cron line).
Scope: Crew and Code only (workflows already have dated calendar items).

## Done

- `productschedule.Schedule.RunAt` (RFC3339): the third timing form beside `CronExpression` and `CadenceHours`; `Validate` requires exactly one. `Decide`: not due before the moment; due once at or after it
  (a server that was down runs it on its next tick); never again after a success; a failed attempt retries with the usual backoff and gives up after `OneTimeMaxFailures` (3).
- Agent tools `create_project_schedule` / `update_project_schedule` (`work_schedule_tools.go`): `in_minutes` (1 to a year) or `run_at` (RFC3339 with offset, must be in the future) instead of `cron_expression`;
  `timezone` is only required with a cron line. Exactly one of cron / in_minutes / run_at on create. Setting a one-time or cron form on update replaces the other form.
- Schedules view: a one-time schedule is reported as a calendar entry (date + time in its timezone, " (one time)" in the description), with its next run until it has run, none after.
- External Crew spec (`crewScheduleSpec`) accepts `run_at` too, same replace-the-other-form rule.
- Tests: `TestOneTimeScheduleRunsOnceAndOnlyOnce` (decision + validation), `TestOneTimeRunAtFromToolArguments` (past refused, offsets, in_minutes), one-time response in `TestProductScheduleJobResponseShape`. The frontend keeps unknown schedule keys, so `run_at` survives its manifest rewrites (read, not run).

## Left

- Live check by the owner: in a Crew/Code chat ask "check X in 5 minutes"; the schedule appears in Schedules as one-time and the agent runs it once in the chosen conversation; it never fires again.
- Not done: the finished one-time schedule stays in the list (enabled, no next run) until deleted (`delete_project_schedule`); an auto-delete or a "done" label if that clutters.
- Not done: a Schedules-view form for creating one by hand (the UI has no create form for Crew/Code schedules today; the agent creates them).
