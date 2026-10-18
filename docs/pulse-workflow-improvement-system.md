# Workflow improvement through Pulse

Current model: PLAT-825 and PLAT-871, October 2026. Earlier multi-pass reviewer
and autonomy designs are historical; the decisions log is in the private
platform deployment repository.

## Two settings

- **Pulse on/off.** ON pursues at most one coherent improvement change set per
  cycle. OFF saves a weekly suggestions report, with no workflow edits, runs,
  action requests, decisions or faster follow-up wakeups.
- **Outward actions.** Ask first, or allow within the workflow's existing access,
  selected accounts, operational caps and explicit owner constraints.

There is no autonomy ladder, pace selector, per-reviewer switch or per-schedule
Pulse mode. Settings are managed through the website Builder or the same backend
checks over MCP. Saved legacy settings are converted once, preserving effective
permission and housekeeping choices. A canonical explicit OFF always wins.

## One continuing agent

Pulse judges progress, evidence, measurement, plan consistency, costs and the
approach. Builder implements changes. They exchange explicit messages; delivery
is not completion, replies are optional and final chat text is not forwarded.
Focused skills are methods for this agent, not separate scheduled reviewers.

Pulse chooses evidence through tools or asks Builder. Metrics and history are
read from the workflow DB; completed runs and numerical differences alone do
not establish goal improvement. Missing evidence stays unknown.

An improvement uses the existing Goal Work item: hypothesis, comparable
baseline, outcome window, rollback condition, actual action and check_at.
When comparable goal results worsen, Pulse asks Builder to revert, verifies
restoration and records the numbers. Reusable verified methods belong in
workflow skills; facts belong in the knowledge base.

## One saved conclusion and clock

Every cycle must save a new record_pulse_goal_check in the existing history.
An old report or provider's final chat sentence cannot complete another cycle.
ON chooses its next useful check between six hours and seven days, considering
existing due questions and outcome dates. OFF always reports weekly. The
launcher and screen read that same clock. Failed-run messages use the same
continuing Pulse; no separate Gate, reviewer, fixer or finalizer runs.

## Ordinary execution

Starting a step or workflow performs real work. A quick bounded deterministic
reference observation logs advice; no AI review holds execution. Required input
files still must be readable. Existing validation, permissions, queues, leases,
idempotency and restart rules remain.

Backup, Publish and Notify are ordinary after_run options, independent of Pulse.
Disabled schedules are shown separately from active run issues. A run with
verified completed and failed groups has a partial outcome. The server owns
these projections; the browser displays them.
