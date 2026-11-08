---
name: agentworks-review-qa
description: "Use when a run looks wrong or a step may have failed quietly: did it work, why did it fail, do its files match its plan."
---

# Review lens 1: QA (did it work, and if not, why)

This is the old Autopilot 'technical review', now yours. Use it when a run looks wrong, a step may have failed quietly, or a number looks implausible. You find and diagnose; the Crew's Builder fixes (and only where you may act, see your prompt).

## Where a run lives
Each run has its own folder under `runs/` in the workflow's folder. Read it through the run tools, not by guessing paths:
- `runs action=get` returns the run's metadata (`status`, timings, trigger source, the Python steps and their checks) and its evidence checks and notices.
- `runs action=logs` returns bounded stdout/stderr tails, a failure traceback, the saved inputs, step timings and the paths of each agent answer and provider log.
- Files the run declared in `script.json` `outputs` are in the run folder. A workflow's own database (`db/`) holds anything it records across runs.

## Did a step work? Check in this order
1. It finished: the run `status` is complete and the step's check passed, with no error in its log.
2. Its outputs exist, are non-empty, and match what `script.json` declares (and what the next step reads).
3. The numbers are plausible next to earlier runs of the same workflow.
4. If recording is part of the workflow's job, check its reading in the database. Other work may be measured by a different step, so a missing reading elsewhere does not prove this step failed.
5. Any notices on the run's evidence (`runs action=get`).

## Debug a failure
Read the failing step's log and the error first. Compare with the last run that worked: what changed between them (inputs or preset, the source files, credentials, the site). Name the root cause in one line; say plainly when you cannot tell. Do not re-run a failed workflow just to read its logs; they are saved, and a rerun repeats real effects.

## Also check that the workflow's files agree with its source
When `main.py` or `script.json` changed while a report, dashboard, saved prompt or stored setting still describes the old behaviour, that is drift. Compare the current source with the artifacts that depend on it (reports, dashboards, agent prompts, schedules, inputs and presets). Name each stale artifact and what it should say.

## Hand off
To the Crew's Builder: what broke, where (run, step, file), the evidence, the likely cause, and what 'fixed' looks like (which output or reading the next run must show). Then check that next run when it happens.
