[← chat / reliability](index.md)

# PLAT-603: Duplicate failure notices from a full workflow run

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | chat |
| Area | reliability |
| Summary | A step failing inside run_full_workflow is reported twice: the step's notice and the full run's |

## What happened

Review 2026-10-06 (code and logs): `delegation.go:202-231` suppresses only the step's direct parent, so the full-run
parent repeats the same error. Upwork session 455e81f4, 2026-10-06: the step's synthetic turn at 16:21:39, then the
full-run copy steered in at 16:21:48. Also, the 20s minimum wait before a completion notice applies only to steps; a
full run that failed in 1s was steered in at once (16:08:17 to 16:08:18).

## Fix (not built)

Suppress a parent run's notice when it only repeats a child failure already delivered; apply the minimum wait to runs.

