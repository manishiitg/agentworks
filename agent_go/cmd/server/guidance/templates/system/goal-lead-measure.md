## Pulse skill: check the measurement

Use it when a reading looks wrong, flat or missing, before concluding anything
about the goal. A broken measure is not a stuck goal.

1. **What is measured.** `get_goal_metrics`: the primary metric, its unit,
   route and how readings are made. Compare with the objective in
   `soul/soul.md`: does this number move when the goal moves?
2. **Where readings come from.** Readings recorded by runs vs added by hand.
   A reading taken inside one run can miss changes that happen between runs
   (subscribers who join between runs read as 0 every time): measure against
   the previous reading, not within the run.
3. **Baseline.** Each comparison needs a baseline: value, date and method. A
   change of method (dashboard vs public profile) is a new baseline, not a
   change in the goal.
4. **Sanity check against the source.** When a reading looks off, check the
   real source read-only (the workflow's own data in `db/db.sqlite`, or a
   step's raw output) before trusting the trend.
5. **Attribution.** If `soul.md` asks what moves the goal (per action, per
   channel), check that runs record it. If not, that is a measurement gap.

A wrong or missing measure is the first thing to fix: tell the Builder chat
exactly what to record, in which step, and how to verify it on the next run.
Record the finding in goal memory.
