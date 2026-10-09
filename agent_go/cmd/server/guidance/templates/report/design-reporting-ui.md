Create or update the workflow's Dashboard using the user's requested design.
Load the platform usage references first:
`read_skill(skills=[{"name":"builder-reference","path":"references/reporting-policy.md"}])`
and
`read_skill(skills=[{"name":"builder-reference","path":"references/html-output.md"}])`.
These references describe the runtime and tools. Layout, typography, colors,
charts, navigation, and visual style follow the user's preferences and any
references they provide.{{if .Focus}}

Requested focus: {{.Focus}}.{{end}}

If motion is requested, also load
`read_skill(skills=[{"name":"builder-reference","path":"references/motion-guide.md"}])`.

## Documents and runtime

- Dashboard documents are complete HTML files under `db/reports/`.
  `db/reports/index.html` is the default. Additional HTML documents appear in
  the shared toolbar using their `<title>`. Optional `views.json` controls
  document IDs, titles, order, and default selection; each HTML document owns
  its content and navigation.
- Inspect existing documents and the real database schema before editing.
  Use actual stored data; do not invent values, targets, or observations.
  Dashboard-only requests do not authorize workflow behavior changes.
- The host injects `window.report` and owns scrolling and frame height.
  Do not fix body/html height or add a nested page scroll container.
- Wrap data calls in `window.report.ready(async () => { ... })` and await
  their promises. The callback runs on initial load and later data refreshes.
  `DOMContentLoaded` and `window.onload` can run before API injection.
- CSS/JavaScript may be inline or use version-pinned HTTPS CDN dependencies.
  The runtime supports browser-compatible CSS, component and charting libraries.
  For daisyUI, `data-report-ui="daisyui"` on `<html>` enables the host's pinned
  stylesheet; that stylesheet alone does not provide Tailwind utilities.
- The host mirrors the app theme through `:root.dark`, `[data-theme="dark"]`,
  CSS palette variables, and `report:theme`. OS `prefers-color-scheme` alone
  does not follow the app toggle.
- If the document has internal tabs, it can handle `report:focus` and read
  `window.report.focus` to respond to agent navigation requests.

## Data and optional helpers

- `window.report.query(sql)` reads the workflow's `db/db.sqlite`.
  The page reads changing data rather than being regenerated on every run.
- `get`, `getText`, and `getHtml` read allowed workspace files. Use `getHtml`
  for a markdown file and `renderMarkdown` for a markdown string from a row.
  Workspace links and images in rendered markdown use the host file preview.
- Goal data: `getGoalMetrics()` returns configured metrics, observations, and
  progress. `renderGoalProgress(target)` is an optional ready-made section.
  Missing measurements remain unavailable; do not turn them into zero.
  Preserve Primary/Secondary outcome priorities separately from
  primary/supporting metric roles.
- Costs: `getCosts({ days: 30 })` reads the canonical ledger;
  `renderCosts(target, options)` is an optional ready-made section.
  Total/activity scopes are all-time; model/daily breakdowns and
  `window_total_usd` cover the selected UTC window.
- `renderTable(target, { query, searchable, sortable })` and
  `renderActivity(target, { limit })` are optional composition helpers.
  Custom rendering can use the same underlying data.
- Activity data already exists in `org_dashboard_notifications` from
  `record_summary(kind="run_summary")`. No new step or collector is needed.
  For route-specific activity, use `(routing_step_id, route_id)` from
  `route_summaries_json`, preserve timestamps, and display the recorded label.
  Missing route scope is unknown. Inspect the schema before querying additive
  columns; older rows can use their original message. For typed rows,
  `summary_text` is the shared lead and `message` is the full digest: render
  the lead plus route entries once, or the full message as a fallback.

For current data from an outside system, write a read-only script under
`code/reports/` and call `window.report.run(path, args)`. Scripts print one JSON
value, receive args in `$REPORT_ARGS`, and cache for themselves under
`$REPORT_CACHE_DIR`. Follow the full script contract in `reporting-policy.md`;
prefer `query` when the data is already stored in `db/`.

## Actions and evidence

- `updateField` and `updateFields` support explicit edits of business fields
  on existing rows. Schema and permission checks are enforced by the backend;
  these APIs are not raw SQL writes or the platform human-decision lifecycle.
- `sendChatMessage(message, { requestId })` sends a contextual request to the
  workflow chat. Call it only from a user action, never during rendering or
  polling. Save a Dashboard-owned approval first, identify the exact item,
  version and intended route, and distinguish a queued request from completion.
  Load `builder-reference/references/human-in-the-loop.md` for decision APIs.
- For recordings under `db/assets/`, persist the workspace-relative file path.
  Request `mediaUrl(path)` when opening a native `<video controls>` or
  `<audio controls>` player. URLs expire; obtain a fresh one on retry rather
  than storing it. Preserve the player and source through data refresh when
  the recording has not changed. This API serves existing recordings; it
  does not record tests.

## Validation

Run `validate_report_html` for every changed document and repair errors.
It checks document structure, literal SQL, referenced files and dependencies.
Dynamic queries/paths require separate verification.
Then run `preview_report` for each changed document. It uses the same runtime
in a real browser and reports script/fetch errors, loading state and screenshots
at 768px, 480px and 1280px in both themes. Open the screenshots with `read_image`
to verify that the requested design renders and its data/actions work.
