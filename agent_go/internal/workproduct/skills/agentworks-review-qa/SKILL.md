---
name: agentworks-review-qa
description: "Use when a run looks wrong or a step may have failed quietly: did it work, why did it fail, do its files match its plan."
---

# Review lens 1: QA (did it work, and if not, why)

This is the old Autopilot 'technical review', now yours. Use it when a run looks wrong, a step may have failed quietly, or a number looks implausible. You find and diagnose; the Goal's Builder fixes (and only where you may act, see your prompt).

## Where a run lives
`runs/<iteration>/<group>/` in the Goal's folder.
- `iteration-0` is interactive Builder work, `iteration-N-sched` a schedule's runs, `iteration-N-hook` a webhook's. A higher N is a later run. Groups are in `variables/variables.json`.
- `run_metadata.json`: `status`, `started_at`, `completed_at`, `duration_ms`, `trigger_source`, `plan_revision`, and `completion_receipts` per step.
- `execution/<step-id>/`: the step's outputs (`route_selection.json` for a routing step says which route ran).
- `logs/<step-id>/`: the step's session, validation results and errors.
- `read_linked_goal` action `runs` lists the runs and their status; read the saved `run_metadata.json`, step outputs and logs for detail.

## Did a step work? Check in this order
1. It finished: a completion receipt in `run_metadata.json`, no error in its log.
2. Its outputs exist, are non-empty, and match its output contract (its validation schema and the validation results in its logs).
3. The numbers are plausible next to earlier runs of the same step.
4. If recording is part of this step's contract, check its reading in the database. Other work may be measured by a different step, so a missing reading elsewhere does not prove this step failed.
5. Any `CONCERNS:` lines in the saved run summaries.

## Debug a failure
Read the step's log and the error first. Compare with the last run where the step worked: what changed between them (inputs, the plan revision and changelog, credentials, the site). Name the root cause in one line; say plainly when you cannot tell. Do not re-run a failed step just to read its logs; they are saved.

## Also check that the Goal's files agree with its plan
A plan that changed while a report, dashboard, prompt or stored setting still describes the old plan is drift. Compare the plan changelog with the artifacts that depend on it (reports, dashboards, step prompts, schedules, variables). Name each stale artifact and what it should say.

## Hand off
To the Goal's Builder: what broke, where (iteration, run, step, file), the evidence, the likely cause, and what 'fixed' looks like (which output or reading the next run must show). Then check that next run when it happens.
