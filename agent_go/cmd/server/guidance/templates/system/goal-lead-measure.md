## Pulse skill: check the measurement

Use this skill when there is no useful goal metric, or a reading looks wrong,
flat or missing. A broken measure is not a stuck goal. Prefer measured evidence and set up a missing meaningful measurement with
Builder, without postponing useful judgment about the approach.

**Setting it up properly** (the `measurement-plan` guide has the platform
rules): agree with Builder, which configures the metric with
`configure_goal_metrics` (meaning, source,
unit, scope, window, freshness and direction). An ordinary workflow step or
separate measurement route records source-backed observations at the appropriate
cadence. No declaration of which execution drives the metric is required.
Saved metric `route` metadata must match observations when present; it is not
proof of executable work or goal contribution. Work with Builder to choose the
best measurement for the goal and verify actual DB readings and
history after implementation. Ask for a bounded repair within autonomy;
definitions alone do not establish that measurement works.

1. **What is measured.** `get_goal_metrics`: the primary metric, its unit,
   scope and how readings are made. Compare with the objective in
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
   explicit. `get_pulse_state(view="goal_status")` offers per-metric history and freshness;
   `get_goal_metrics` exposes the complete definitions and bounded history.
   Code differences are arithmetic evidence, not an automatic progress verdict.
4. **Sanity check against the source.** When a reading looks off, check the
   real source read-only (the workflow's own data in `db/db.sqlite`, or a
   step's raw output) before trusting the trend.
5. **Attribution.** If `soul.md` asks what moves the goal (per action, per
   channel), check that runs record it. If not, that is a measurement gap.

Ask Builder to repair the existing recording step or measurement route and
verify source-backed DB history. Collection must respect current access and
account constraints; when automatic collection is unsuitable, use a dated
manual reading instead of risking the account. When a baseline period ends,
or a goal still has no target after two weeks, propose one useful target as
an owner decision, reusing an existing pending question. Record the actual
measurement limitation and what would settle it; independent goal work can
continue within permission levels.
