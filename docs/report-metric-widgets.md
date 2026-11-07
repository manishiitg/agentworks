# Reusable report data and widgets

Workflow reports can read existing platform data or render optional prebuilt sections.
The optional widgets include responsive styles and can be placed in the user's
chosen layout. Custom sections can use the same data functions. Verify the
complete document at the supported pane widths and in both app themes.

| Data function | Optional widget | Source |
| --- | --- | --- |
| `getGoalMetrics()` | `renderGoalProgress('#goals')` | Saved rows in `workflow_goal_metrics` and `goal_observations` (see [goal-measurements.md](goal-measurements.md)) |
| `getCosts({ days: 30 })` | `renderCosts('#costs', { days: 30 })` | Same canonical ledger summary as the Costs view |

```html
<section id="goals"></section>
<section id="costs"></section>
<script>
window.report.ready(async () => {
  await Promise.all([
    window.report.renderGoalProgress('#goals'),
    window.report.renderCosts('#costs', { days: 30 })
  ]);
});
</script>
```

Use empty `div`/`section` containers. Each renderer replaces its own contents on
refresh, returns its data, and rejects on load failure. The app and `dashboard action=preview`
share the runtime. No additional collector or duplicate reporting tables are needed.

## Composition widgets

Optional helpers for the two most repeated dashboard patterns. Prefer them
over hand-rolled markup; a fully custom section remains valid.

```html
<section id="leads"></section>
<section id="activity"></section>
<script>
window.report.ready(async () => {
  await Promise.all([
    window.report.renderTable('#leads', {
      query: 'SELECT name, status, value FROM leads ORDER BY value DESC',
      searchable: true,
      sortable: true
    }),
    window.report.renderActivity('#activity')
  ]);
});
</script>
```

- `renderTable(target, { query, searchable, sortable })` runs read-only SQL
  and renders a themed, responsive table with an empty state. Columns come
  from the returned rows; numeric columns align right. `searchable` adds a
  filter box matching every cell; `sortable` makes headers toggle
  ascending/descending sort. `query` is required.
- `renderActivity(target, { limit })` renders the policy-required activity
  section from the run summaries in `org_dashboard_notifications`,
  route-grouped via `route_summaries_json` and markdown-rendered, with the
  execution-log fallback built in. `limit` is an integer 1–100 (default 30).
  Missing history tables render a setup message, not an error.

## Cost data

`getCosts(options)` returns `summary`, `history`, `window_total_usd`, and `state`.
The `summary.total` and `by_scope` cover **all time**; `by_model`, `by_date`, and
`window_total_usd` cover the selected UTC window. The widget labels these scopes
separately and provides a daily trend and expandable date/activity/model tables.
An unavailable ledger returns a null summary and undefined period total.

Options: `days` is an integer from 1 to 90 (default 30); `before` is an exclusive
YYYY-MM-DD date. For older daily history, pass `history.next_before` when
`history.has_more` is true. All-time totals repeat on each page; do not sum them.
Amounts are recorded USD costs, not an assertion that all usage has been priced.
The preview endpoints are bound to the preview token's workflow and force the
bounded cost-summary reader. Metric helpers expose data reads only.
