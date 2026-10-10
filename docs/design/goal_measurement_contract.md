# Shared goal measurement

Builder and Pulse use one measurement contract. Builder chooses and implements
meaningful measurements in ordinary workflow steps. Pulse checks the source,
history and comparability and works with Builder to improve gaps. Go validates
records and calculates facts; agents judge whether a metric represents the goal
and whether evidence supports progress.

The existing workflow DB holds `workflow_goal_metrics` definitions and immutable
`pulse_goal_observations` history. Existing business tables remain source data;
no separate measurement service or duplicated business dataset is introduced.

| Record | Contract |
|---|---|
| Definition | Stable metric/criterion IDs, formula, source/method, unit, window, fixed route/environment/dimensions, cadence and freshness. Changed meaning uses a new metric ID. |
| Reading | Exact metric/criterion/unit/scope, value or unavailable status, source capture time, evidence and opaque source batch/execution ID. Never turn unknown into zero. |
| Period | Actual RFC3339 `window_start` and `window_end`, both present or both absent; start before end, end no later than capture time. |
| Snapshot | Definition window `instant`, `snapshot` or `point_in_time`; actual capture time, no invented period. |
| History | Append distinct source batches, preserve original timestamps and retired definitions. Retry identical rows; reject conflicts. |
| Comparison | Current and immediately preceding distinct capture, matching immutable meaning and scope. Periods must be equal length and nonoverlapping. An unavailable baseline, duplicate period or unknown period produces no numerical difference. Agents review completeness, calendar effects, method and attribution. |

Example ordinary step output for a complete daily count:

```json
{
  "metric": "qualified_responses_daily",
  "criterion_id": "qualified_responses",
  "unit": "responses",
  "run_id": "source-batch-2026-10-09",
  "observed_at": "2026-10-10T00:05:00Z",
  "window_start": "2026-10-09T00:00:00Z",
  "window_end": "2026-10-10T00:00:00Z",
  "value": 4,
  "evidence": ["responses table: complete UTC day, qualified rows only"]
}
```

## Upgrade existing workflows

The platform adds the two period columns to the existing DB table when it opens
the ledger. Existing rows remain untouched with empty period fields. This is an
additive schema migration, not a second measurement implementation or a rewrite
of execution identities. Reads use one evaluator for current and historical data.

No workflow topology migration is required. Builder updates an existing
period-producing step to supply the actual bounds through
`record_goal_observations`, verifies the DB readback with Pulse, and records the
baseline and any gap in goal memory. Snapshot producers can keep their format.
Old period readings still count as measured if source-backed and fresh, but
without provable boundaries they cannot support a period comparison. Never
guess old periods, overwrite history, or replay business actions to migrate it.
If an existing definition has the wrong meaning, create a new metric ID rather
than changing the definition under old readings. Retired series stay in the DB.

`get_goal_status` exposes per-metric facts and bounded matching history;
`get_goal_metrics` exposes complete definitions and bounded observations.
Current active series retain up to 120 readings independently of the global
recent-row limit. This is a bounded view, not deletion of the older DB history.
Agents must query source history for a comparison outside this view.

Run-folder association remains an execution diagnostic. Its absence cannot
erase a DB reading or classify a measured goal as unmeasured. A source capture
time is not the time a recovery step re-recorded an old reading. Code `ok`
means no mechanical alarm, not an agent verdict that the goal is on track.
