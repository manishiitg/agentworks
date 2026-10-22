Create or update the workflow's Dashboard using the user's requested design.
Use JSON-render by default, preserving existing HTML when editing it.
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

## JSON-render dashboards (default)

Create new dashboards with the pinned Vercel json-render library and its complete
supplied shadcn catalog. Call `get_dashboard_catalog` once for the current component
props and mechanics. Layout, typography, colors, navigation and component placement
remain the designer's choice; do not add platform-required sections or sample values.

Pass `files: {"index.json": "<JSON specification>"}` to `create_dashboard` or
`update_dashboard`. The source uses `root`, `elements` and optional `state`.
Each element has `type`, `props` and `children`; `on`, `visible`, `repeat` and
named `slots` use json-render semantics. Nullable props can be omitted. The server
validates the catalog and tree and creates the HTML entry automatically. Keep
`index.json` as the editable source; do not hand-edit its generated `index.html`.

Use optional `data` entries to load actual project values:
`{"statePath":"/totals","method":"query","sql":"SELECT total FROM totals","params":[]}`,
`{"statePath":"/summary","method":"get","path":"db/summary.json"}`, or
`{"statePath":"/live","method":"run","path":"code/reports/summary.py","args":{}}`.
These use the same authorized `report.query/get/run` APIs, on load and explicit
refresh. Bind props with `{"$state":"/totals"}`; form inputs can use `$bindState`.
Data args may use `$state` references. Only local state/form actions and
`refreshData` are supported in `on`/`watch`; neither rendering nor local state
executes a published function.

The full shadcn catalog is available, plus `Metric`, `BarChart`, `LineChart`,
`AskButton`, `ChatPanel`, `FunctionButton` and optional `MotionControls`.
AskButton only opens the existing composer. ChatPanel requires an `id` and a
positive `height`; choose its position, size and responsive CSS. FunctionButton
names this project's published function and opens its existing input form; the
person presses Run. Flow function controls remain unavailable until its existing
function adapter supports them. Preview has no native conversation or function
execution. Native controls do not add access or choose another account/project.

JSON-render changes the source format; it does not prohibit CSS or replace the
designer's visual work. Bundled CSS supplies component defaults, not a complete
page design. Put authored CSS in the top-level `styles` string, including media
queries and theme overrides. On components whose catalog supports `className`,
use that prop to connect semantic class names to selectors declared in `styles`.
The renderer supplies the app's light/dark theme variables. Its bundled Tailwind
classes cover supplied components, not every dynamically invented utility; new
class names need their own CSS in `styles`.

Do not remove existing styling merely because a dashboard uses JSON or shadcn
components. When converting HTML, carry its requested visual design into the JSON
styles and component props unless the person asks to change it. CSS remains
optional when the supplied props already achieve that design. Preview and inspect
the result: valid JSON and a ready response alone do not verify its appearance.
For requested playback, optional `motion: {"duration":12,"statePath":"/playback"}`
uses the existing shared clock; chart `progress` may bind to `/playback/progress`.
Add MotionControls only where desired. Load historical values before playback;
never invent missing history. MP4 uses the same published layout and frozen data.

Images/avatars resolve project assets (db/, knowledgebase/, docs/) through the
existing file API; inline data images are also supported. Links are static
http(s)/mailto addresses and open only from a person's click. JSON contains no
raw HTML, scripts or arbitrary fetch operations. Managed assets/scripts still use
`{{dashboard_assets}}` and `{{dashboard_scripts}}` for immutable revision paths.
Validate/preview the draft, then publish it through the same dashboard tools.
Existing authored HTML dashboards and historical revisions remain supported;
when editing one, preserve its format unless the person asks to convert it.

## Dashboard UI workflow

1. Read the existing page and the user's design references. Keep their content,
   visual identity, data sources and chosen layout. When no direction is given,
   use the platform's default theme and make the layout fit the task; do not
   invent brand rules, required sections, or sample business data.
2. Read `get_dashboard_catalog` for supported props. Choose components for the
   content: readable text, metrics, charts, forms or tables as needed. A catalog
   is a set of available controls, not a complete page design. Use the actual
   data shape and include meaningful loading, empty and error states.
3. Style the composition deliberately. The default theme supplies page spacing,
   typography, themed surfaces, buttons and metric styling. Grid uses one column
   below640px, then its requested columns. Override it through supported
   className/CSS when the user's layout needs different behavior. Check long
   labels, dense tables, keyboard focus and names for icon-only controls.
4. Keep everything customizable. Top-level `styles` is normal authored CSS and
   takes precedence over the default CSS layers. `#dashboard-root` is the page
   surface. Override `--dashboard-page-width`, `--dashboard-page-padding`,
   `--dashboard-font-family`, `--dashboard-card-padding`,
   `--dashboard-metric-size` and the app's palette/radius variables as needed.
   These are CSS properties, not new dashboard settings. A full-width design can
   set page width to none and page padding to0. Do not hand-edit generated HTML.
5. Validate, then preview at desktop and mobile widths in the user's chosen
   theme; check both light and dark if the page supports them. Preview uses real
   browser widths (1280px desktop,390px mobile;768px tablet is also available).
   Inspect the attached images and each `ui_checks` record. Resolve unintended
   page overflow, missing stylesheets, broken images and unnamed controls.
   Small-text counts and scan limits are observations for review, not mandatory
   design scores. Local table scrolling can be intentional.
6. Exercise local tabs, forms and filters with representative data and inspect
   loading/empty/error handling. Preview does not send Ask messages or run
   published business functions. Verify real actions separately when authorized.
   Publish the checked revision; report any unverified behavior plainly.

JSON dashboards use the shared prebuilt React runtime. They do not have a
per-dashboard npm build step. Use the existing validate/preview/publish tools;
TypeScript and npm builds belong to changes to the shared runtime or separately
requested React source projects. A successful build or ready state never replaces
visual inspection.

## Existing HTML documents and runtime

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

For JSON dashboards, run `validate_dashboard` and `preview_dashboard`, repair errors,
then `publish_dashboard`. For existing HTML, run `validate_report_html` for every
changed document and repair errors.
It checks document structure, literal SQL, referenced files and dependencies.
Dynamic queries/paths require separate verification.
Then run `preview_report` for each changed document. It uses the same runtime
in a real browser and reports script/fetch errors, loading state and screenshots
at real768px,390px and1280px browser widths in both themes. Inspect the images
and per-capture `ui_checks`; verify the requested design and local interactions.
Use only image-viewing capabilities actually available to the current client.

## When it works
Call `get_dashboard_link` and give the person the link in plain words. Share opens this dashboard, Ask and published functions to the chosen audience and allows MCP reads of product files, runs and logs; secret values and private logs stay excluded. Tell them to press Share, or use MCP `share` action=get|set with their chosen audience. Sharing creates a standard dashboard when needed. Never put the dashboard on a public website or add a password page. For an MP4 or animation of the data, use the dashboard tool's `export_motion` action after loading the motion guide (`motion-guide` skill).

