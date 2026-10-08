## Pulse skill: inspect runs, iterations and steps

Read-only. Use it to see what a run did, whether a step worked, and why one
failed. You find and diagnose; the Builder chat fixes (`ask_builder`).

**Where a run lives.** `runs/<iteration>/<group>/`:
- Iterations are output folders: `iteration-0` is interactive Builder work,
  `iteration-N-sched` a schedule's runs, `iteration-N-hook` a webhook's. A
  higher N is a later run. Groups are listed in `variables/variables.json`.
- `run_metadata.json`: `status`, `started_at`, `completed_at`, `duration_ms`,
  `trigger_source`, `plan_revision`, and `completion_receipts` per step.
- `execution/<step-id>/`: the step's outputs (`route_selection.json` for a
  routing step says which route ran).
- `logs/<step-id>/`: the step's session, validation results
  (`pre_validation*.json`) and errors.
- `list_executions` and `get_schedule_runs` list current and past runs;
  `query_step` reads one step's state.

**Did a step work?** Check, in order:
1. It finished: a completion receipt in `run_metadata.json`, no error in its log.
2. Its outputs exist, are non-empty and match its output contract (the step's
   context output and validation schema; validation results in its logs).
3. The numbers are plausible next to earlier runs of the same step.
4. If it is goal work: it recorded its goal reading (`get_goal_metrics`).
5. Its `CONCERNS:` lines (in `get_pulse_state(view="step_concerns")`).

**Debug a failure.** Read the step's log and the error first. Compare with the
last run where the step worked: what changed between them, the inputs, the
plan (`plan_revision`, `planning/changelog`), credentials or the site. Name
the root cause in one line; say plainly when you cannot tell.

**Hand off.** Tell the Builder chat: what broke, where (iteration, run, step,
file), the evidence, the likely cause, and what "fixed" looks like (which
output or reading a next run must show). Then check that run when it happens.
