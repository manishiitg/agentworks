## MEASUREMENT — run-scoped, evidence-backed outcomes

Outcome measurement is produced by ordinary workflow steps, not a mandated
route or table. Any normal step may produce a
run-scoped, evidence-backed measurement. Pulse does not depend on which step
or route produced it.

### Builder and Pulse share the measurement

Builder chooses a meaningful primary outcome and implements its measurement;
Pulse checks whether that choice actually represents `soul.md`, inspects DB
history and challenges weak sources, missing readings or incompatible comparisons.
Use `get_goal_metrics` together. Agree the formula/definition, source method,
unit, fixed dimension/environment scope, point-in-time or period window,
collection cadence, freshness and evidence needed to verify the number.
Builder asks Pulse for goal context when uncertain; Pulse uses `ask_builder`
for implementation or repair within the existing autonomy level. Verify the
result through real source-backed DB readings and history, then record the
agreed meaning, baseline and remaining gaps in goal memory. Setup alone is not
verified measurement. Do not invent a target or silently change goal meaning.

### Shared DB format and history

- Reuse `workflow_goal_metrics` definitions and `pulse_goal_observations` in
  the workflow DB for generic goal history. No separate collector subsystem.
- A stable metric ID fixes formula, source/method, unit, window, environment,
  route and dimensions. A changed meaning uses a new ID; old history stays.
- Each observation records metric/criterion, exact unit/scope, actual
  `observed_at`, numeric `value` or an explicit unavailable `status`, source
  `evidence` and `run_id` identifying the source batch/execution. Run IDs are
  opaque provenance: an iteration folder is not required to measure a goal.
- Period-based measurements also record actual RFC3339 `window_start` and
  `window_end`. Do not fabricate boundaries when backfilling legacy readings.
  Use window `instant`, `snapshot` or `point_in_time` for totals at an instant.
- Preserve original capture times and append history; retries are idempotent,
  conflicts are rejected. Use distinct source batch IDs for distinct readings.
  Unavailable is never zero. A recovered old batch is not a new measurement.
- Go supplies per-metric freshness, bounded history and numeric differences
  only across matching scopes and mechanically comparable periods/snapshots.
  Missing/incompatible baselines stay unknown. Agents verify source quality,
  sampling, calendar effects and attribution before calling a difference
  progress. A numeric comparison is not a causal claim.

### Placing measurement

- Reuse an existing step's DB output when it already measures the outcome.
  Existing workflow tables stay the source of truth; do not copy their values
  into a second metrics store.
- Add `record_goal_observations` to that producer only when the value should
  enter Pulse's generic goal-progress history (definitions via
  `configure_goal_metrics`, reads via `get_goal_metrics`).
- Put route-specific measurements in existing route-local steps.
- Use a shared convergence step only when multiple routes genuinely require
  the same calculation.
- Add a dedicated measurement step only when no existing step can safely own
  it. It is an ordinary step with no special topology.
- Make no topology change when existing measurement is already sufficient.
- Flag ambiguous cases for manual migration rather than inventing steps or
  routes.

### Run scope and evidence

- Scope every measurement to the run that produced it; never report "latest"
  as the current run's outcome. Interactive runs reuse `iteration-0`, so a
  run folder alone is not a stable measurement identity across runs.
- Steps receive database-wide read-write access: there is no table-scoped
  authorization. A measurement reader must therefore re-verify the producer's
  rows (run scope, filters, freshness) instead of assuming a privileged write
  path kept them clean.
- A measurement failure is recorded as a measurement failure. It must never
  erase or invalidate the successful business run it measured.

### Legacy evaluation artifacts

Old `evaluation/` plans and `evaluation_report.json` files are read-only history,
as are retired `costs/evaluation/` ledgers and retired `eval_results` rows,
unless a separate cleanup migration is approved. Never write to them; never let
a new measurement depend on them.
