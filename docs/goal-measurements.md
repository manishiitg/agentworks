# Goal measurements

There is no Goal engine or metrics product: no `/setup-goals` command, no goal
setup bar, and no tools that configure metrics or record observations. A step
measures with ordinary database tools, and a dashboard shows the result.

## What remains

The Goals view reads saved measurement rows. Two tables inside a workflow's own
database are plain saved data:

- `workflow_goal_metrics` (`metric_id` primary key, `definition_json`, `active`):
  one row per metric definition. Only rows with `active = 1` are shown.
- `goal_observations`: one row per reading (`criterion_id`, `metric`, `run_id`,
  `route`, `environment`, `value`, `status`, `unit`, `observed_at`,
  `window_start`, `window_end`, `evidence_json`, `recorded_at`), unique per
  criterion, metric, run, route and environment.

The read route is `GET /workflow/goal-metrics`; it returns `{ metrics, observations }`
through `LoadGoalMeasurements` (most recent observations first, bounded by a limit).

## Using the rows in a dashboard

Dashboards can read both tables by SQL (`window.report.query`). The optional
`window.report.getGoalMetrics()` and `window.report.renderGoalProgress('#goals')`
helpers still read them and show a setup message when the tables are missing.
See [report-metric-widgets.md](report-metric-widgets.md).

Nothing writes these tables automatically. A step that wants a measurement writes
its own rows with its database tools, and a missing value should be stored with a
status rather than as zero.
