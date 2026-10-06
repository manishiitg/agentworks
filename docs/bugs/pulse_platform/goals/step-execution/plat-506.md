[← goals / step-execution](index.md)

# PLAT-506 — The schedule override `force=true` was rejected by `update_step`, so a plan repair during a running schedule could never be approved

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | step-execution |
| Summary | fixed on main: the guard strips its own `force` before the strict tool validates. |

| Coordination | Value |
|---|---|
| State | fixed on main; needs a restart |
| Date | 2026-10-05 |
| Owner | step-execution |

## Source

salesoutreach chat, 14:56 (and jobsearch, 14:51): `update_step` rejected a repair twice. With `force=true`: `invalid update_step arguments: jsonschema validation failed ... additional properties 'force' not allowed`,
although the tool's API spec lists `force`. Without it: `schedule_running` (a schedule is active on the workflow, so plan edits are blocked until the user approves a concurrent change).

## Cause

`GuardScheduleTools` adds `force` to the exposed schema of guarded tools and lets the call through when the guard check accepts it, but passed the arguments, `force` included, to the real tool. `update_step`
(the consolidated plan tool) validates its arguments strictly against a schema that does not know `force` and rejected them. So the documented override could never run for it. The `schedule_running` block
itself is correct.

## Done

- The guard removes `force` before calling the real tool, unless the tool declares its own `force` parameter (which it keeps).
- Test `TestScheduleOverrideReachesTheRealUpdateStep` drives the real `update_step`: blocked without `force`, applied with it. It fails on the old code with the exact error above.

## Left

- The chats that hit this still hold the repair; after a restart and the user's approval, `update_step` with `force=true` should apply. Not retried live.
- The guard still stops plan edits for the whole time a schedule runs (by design); long scheduled runs leave a long window where a Builder fix needs the override.

## Register notes

[PLAT-506](plat-506.md), fixed on main: the guard strips its own `force` before the strict tool validates.
