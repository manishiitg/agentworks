## Pulse skill: check the measurement

Measurement comes first: a goal that is not measured cannot be owned. Use this
skill when there is no goal metric yet, and when a reading looks wrong, flat or
missing, before concluding anything about the goal. A broken measure is not a
stuck goal.

**Setting it up properly** (the `measurement-plan` guide has the platform
rules): the metric is defined with `configure_goal_metrics` (name, unit,
direction, which route moves it); the step that already produces the number
records it with `record_goal_observations`, once per run, scoped to that run;
a dedicated measurement step only when no existing step can own it; the
reading cadence matches the schedule that drives the goal (a daily route reads
daily); the target and its date are in `soul.md`. Work with Builder to choose the best measurement for the goal, agree its
source, formula, unit, scope and windows, and verify actual DB readings and
history after implementation. Ask for a bounded repair within autonomy;
definitions alone do not establish that measurement works.

1. **What is measured.** `get_goal_metrics`: the primary metric, its unit,
   route and how readings are made. Compare with the objective in
   `soul/soul.md`: does this number move when the goal moves?
2. **Where readings come from.** Read source-backed DB history, independently
   of run-folder names. `run_id` identifies provenance; missing execution
   attribution does not make a valid DB number unmeasured. Keep execution
   recording coverage separate from current metric freshness.
   A reading taken inside one run can miss changes that happen between runs
   (subscribers who join between runs read as 0 every time): measure against
   the previous reading, not within the run.
3. **Baseline.** Each comparison needs a baseline: value, date and method. A
   change of method (dashboard vs public profile) is a new baseline, not a
   change in the goal. Actual period boundaries must be compatible; a
   partial day is not a full-day baseline. Gaps and unavailable outcomes stay
   explicit. `get_goal_status` exposes per-metric history and freshness;
   `get_goal_metrics` exposes the complete definitions and bounded history.
   Code differences are arithmetic evidence, not an automatic progress verdict.
4. **Sanity check against the source.** When a reading looks off, check the
   real source read-only (the workflow's own data in `db/db.sqlite`, or a
   step's raw output) before trusting the trend.
5. **Attribution.** If `soul.md` asks what moves the goal (per action, per
   channel), check that runs record it. If not, that is a measurement gap.

A wrong or missing measure is the first thing to fix: tell the Builder chat
exactly what to record, in which step, and how to verify it on the next run.
Record the finding in goal memory.
