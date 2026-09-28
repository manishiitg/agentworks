## HTML-only Dashboard policy

The Dashboard tab renders workflow-owned HTML documents under `db/reports/`.
`index.html` remains the default. Additional `.html` documents become views in
the shared top toolbar. Auto-discovery uses each document's `<title>`; an optional
`db/reports/views.json` can set stable IDs, titles, order, and default:
`{"schema_version":1,"default":"overview","views":[{"id":"overview","title":"Overview","path":"db/reports/index.html","order":0}]}`.
There is no Dashboard generation step or widget-layout registry.

### Page contract

- Write one or more complete `.html` documents under
  `db/reports/`. Use separate documents for genuinely distinct destinations;
  keep closely related sections inside one document.
- Include a non-empty `<title>` and accessible internal navigation when the
  Dashboard has multiple views or sections.
- Keep CSS and JavaScript inline. Do not pin body height or create a nested
  scroll container.
- CSS and component-library choice belongs to the Dashboard author. HTTPS CDN
  stylesheets and browser scripts are supported, including Tailwind's browser
  build. Pin library versions rather than using floating `latest` URLs, and
  use `preview_report` to prove the chosen stack works in the Dashboard sandbox.
  Keep Dashboard-specific CSS/JS inline when that is the simpler choice.
  daisyUI is CDN-only. To use it, inspect/install the official
  `saadeghi/daisyui` skill, read it, add `data-report-ui="daisyui"` to the
  document's `<html>` element, and include the pinned stylesheet
  `https://cdn.jsdelivr.net/npm/daisyui@5.7.38/daisyui.css`. The host supplies
  that same CDN link for opted-in Dashboards that omit it. Use inline CSS
  for layout when Tailwind utilities are not loaded.
- Choose any suitable charting approach: Chart.js, another browser library,
  SVG/canvas, or HTML/CSS are all valid. Match the implementation to the
  visualization, make it responsive and theme-aware, and render a readable
  fallback when an external dependency fails to load.
- Design for the default Tablet Dashboard pane first (~768px). Use one or two
  primary columns, responsive spacing/type, 44px minimum touch targets, no
  hover-only interactions, and tabs that wrap or scroll without clipping.
  Tables must reflow or stay inside an intentional horizontal scroller; the
  page itself must never overflow. Treat 480px mobile and 1280px laptop as
  required secondary widths, with laptop as progressive enhancement.
- Read durable live data with `window.report.query`, `get`, `getText`,
  `getHtml`, `renderMarkdown`, `fileUrl`, `mediaUrl`, and `openFile`. Write a business
  field on an already-existing row with `window.report.updateField`/
  `updateFields` (see below). Do not bake changing run results into the
  document or add a step that regenerates it each run.
- For data that lives outside the workflow (Notion, a CRM, an API), a
  Dashboard can run a script from `code/` on each load or Refresh with
  `window.report.run(path, args)` — see "Live data from a script" below.
  Prefer `db/` for anything a run already stores.
- A user action button can offer a contextual workflow-agent request through
  `window.report.sendChatMessage`. The app sends it directly to an existing automation chat,
  creating one only if none exists. For Dashboard-owned approvals, save first and then offer the request;
  see "Sending a Dashboard request to the workflow agent" below.
- A markdown file under `db/` renders inline with
  `el.innerHTML = await window.report.getHtml('db/notes/brief.md')`; a
  markdown string from a query row with `window.report.renderMarkdown(text)`.
  Both come back themed (`.report-markdown`), and links/images inside them
  that point at workspace files (`db/...`, or paths relative to the .md
  file) load and open the in-Dashboard preview. Never show markdown as raw text.
- For audio or video under `db/assets/`, persist the workspace-relative path
  in the database and obtain a fresh playback URL with
  `await window.report.mediaUrl(path)` only when the user opens the media.
  Assign that URL to a native `<audio controls preload="metadata">` or
  `<video controls playsinline preload="metadata">` element. The platform
  owns authentication, expiring URLs, byte ranges, and streaming: never call
  an internal report-preview/report-media HTTP endpoint directly, persist the
  returned URL, embed media as base64/data URLs, or eagerly load every media
  row with `fileUrl`. `fileUrl` remains appropriate for ordinary downloadable
  files and backward-compatible Dashboards, but `mediaUrl` is the explicit
  contract for new audio/video Dashboard code. Show playback errors and let a
  retry request a fresh URL. Preserve the existing media element and its source
  during data refresh when the recording has not changed. Compare the workspace
  path plus a run/version identifier, not the expiring signed URL; repeated
  `innerHTML` replacement or `src` assignment resets playback. Initialize the
  player once after data is ready, and ignore stale asynchronous media responses.
- Theme off the app, not the OS: dark styles under `:root.dark` /
  `[data-theme="dark"]` (or the injected `hsl(var(--token))` palette), with
  `report:theme` for live re-styling. `prefers-color-scheme` alone follows
  the viewer's OS and ignores the in-app toggle.
- After editing, call `validate_report_html` for every changed document. Beyond the document shape it
  runs every literal `window.report.query` SQL against the live
  `db/db.sqlite`, confirms every referenced `db/` file exists, checks local
  stylesheet/script references, and warns on OS-only dark mode. HTTPS CDN
  stylesheets and scripts are valid Dashboard dependencies.
- `validate_report_html` is static and fast; it cannot tell you the page
  actually renders. Call `preview_report` for each changed document after it passes, or whenever
  visual review is requested: it opens the Dashboard in a real headless browser
  through the same runtime the Dashboard tab uses, waits for it to settle, and
  returns whether it errored, its script/data-fetch errors, its tab labels,
  any `Loading…` placeholder still on screen, and screenshots at tablet
  (primary), mobile, and desktop widths in both themes
  under `db/reports/preview/` — open them with `read_image`. Prefer it over
  asking the user to open the Dashboard tab for you.
- Always include one section, as its own top-level tab (not a subsection
  scrolled past within another tab, and not merely an anchored region on a
  single scrolling page), that Dashboards what the workflow actually did, in
  plain non-technical language (recent runs and the actions taken in
  each) — named for the workflow's real run cadence (e.g. `Daily Action`
  for a daily workflow, `Recent Activity` for hourly/weekly/on-demand ones).
  A Dashboard with no other tabs still needs this one; the rest of its content
  becomes a second tab.
  **Default source, no extra step needed:** every `notify_user(notification_kind="run_summary")`
  call (required at the end of a Pulse cycle, and normal after an ordinary
  run) already writes a row — title, status, message, structured fields,
  timestamp — into `org_dashboard_notifications` in this same `db.sqlite`.
  Query `notification_kind = 'run_summary'` from it for this tab; that is
  the default and needs no new step, table, or column. The `message` is
  agent-written markdown — render it with `window.report.renderMarkdown`,
  never as raw text, or the reader sees `###` and stray backticks. The
  structured `fields_json` / `sections_json` render as plain values. Build something
  custom only if the parent explicitly asks for a different or richer
  activity view than the run summaries already give them — never invent a
  step or table whose sole purpose is feeding this tab. See the
  `design-reporting-ui` skill for the full authoring requirement.

  **Routes are sub-workflows.** When route data is present, the Daily Action /
  Recent Activity tab groups or filters runs by `(routing_step_id, route_id)`
  from `route_summaries_json` in the same notification row. Parse that JSON
  array and render each entry's label, title, status, message, fields, and
  sections; do not display raw JSON or merge same-named routes. Keep a shared
  workflow section for top-level facts. `branch` choices are not separate
  sub-workflows. Use the same grouping for any Pulse-summary history.
  Check the real schema first: absent table/column or older rows use the old
  message and explicit legacy Route field, with scope shown as not recorded
  when absent. Never backfill route identity from prose, show an unreviewed
  route as clean, or copy a workflow total into each route. No extra plan step
  or workflow-owned activity table is needed.

### Responding to `report:focus` (tabbed Dashboards)

A Dashboard with top-level tabs should let the agent point the reader at one:

```js
window.addEventListener('report:focus', function () {
  // window.report.focus is the tab name the agent asked for.
  switchToTab(window.report.focus);
});
```

`perform_ui_action(action="open", view="report", target="<tab>")` sets `report.focus` and
fires this event. It is optional — a Dashboard that ignores it simply stays on
its current tab — but a tabbed Dashboard that honors it lets the agent say
"here is the answer" and land the reader on the right tab.

### Data lifecycle: always gate on `window.report.ready(fn)`

`window.report` is injected into the page AFTER the Dashboard's own `<script>`
has already parsed and started running — it does not exist on the page's
first line. Wrap every use of `window.report.*` in:

```js
window.report.ready(function () {
  // window.report.query/get/getText/getHtml/fileUrl/mediaUrl are live here.
  // Runs once on load, and again on every later data refresh.
});
```

Do NOT gate data calls on `DOMContentLoaded`, `window.onload`, or a bare
top-level `(async () => { await window.report.query(...) })()`. All three can
run before injection, before `window.report.query` is truly live — the single
most common cause of a Dashboard that shows a "data loading error" or is stuck on
"Loading…" the first time it is opened. `validate_report_html()` cannot catch
this: it parses the markup, it does not execute the page. `.ready()` is the
only pattern that is safe regardless of when it runs.

### Writing back: `window.report.updateField`/`updateFields`

A Dashboard may write a plain business field on a row it already reads via
`query` — for example an inline Approve/Reject button that flips a
`status` column, or a bulk list where each row edits independently. This
is a narrow, structured write, not a general database API:

```js
// One cell:
await window.report.updateField('emails', row.id, 'status', 'approved')
// Several columns on the same row, applied atomically (all or none) — a
// form submit:
await window.report.updateFields('emails', row.id, { status: 'approved', note: 'looks good' })
```

Both resolve `{ oldValue(s), newValue(s) }` once committed, or reject with
a clear error. The backend validates every call against the table's own
live schema before writing — there is no way to pass raw SQL through
either function, and no declaration step is needed first:

- the target table must exist, have exactly one primary-key column (the
  row is matched on it), and must not be a platform-owned table
  (`report_human_inputs`, `report_human_input_events`,
  `schema_migration_log`);
- every named column must exist on that table, and must not be the
  primary key, end in `_id`, or be named `created_at`/`updated_at` — a
  column that identifies or timestamps the row is never a legitimate
  write target;
- every value must be a plain string, number, boolean, or null — no
  objects/arrays.

Dashboard-owned approval buttons are supported: update an existing business row
(an email's `status`, an audit finding's approval), then optionally offer a
scoped agent request using `sendChatMessage` below. This does not replace the
platform's human-decision lifecycle. Platform decisions created through
`create_human_input_request` retain their options, answers, consumption, and
audit trail in the Pulse panel/chat; do not edit `report_human_inputs` through
the business-field write API or invent a duplicate platform decision store.

### Live data from a script: `window.report.run`

`await window.report.run('code/reports/open_deals.py', { days: 7 })` runs
that script on the server and resolves the one JSON value it printed. Use it
when the Dashboard must show the latest state of an outside system (the
workflow's MCP servers or an API behind its secrets) rather than what the last
run stored. Anyone who can open the workflow — owner or read-only — can
trigger it, and it always runs **as the workflow**: its selected MCP servers
and tools, its selected secrets (`$SECRET_*`), its variables (`$VAR_*`), never
the viewer's own connections. Published static copies cannot run scripts.

The script contract:

- Lives under `code/` (`.py` → `python3`, `.js`/`.mjs` → `node`); the
  convention is `code/reports/<name>.py`. The validator rejects any other
  path and a script that does not exist.
- Args arrive as JSON in `$REPORT_ARGS` (`{}` when none); treat them as
  untrusted input from any viewer: validate, bound, and never interpolate
  them into SQL, shell commands, or MCP filters without checking.
- Print exactly **one JSON value** to stdout; send logs to stderr. Non-JSON
  stdout, a non-zero exit, more than 2 MB of output, or more than 60 s fails
  the call, and the Dashboard receives the error plus the stderr tail.
- Calls MCP tools the same way a scripted step does:
  `POST $MCP_MCP/<server>/<tool>` with the `$MCP_AUTH` header. Only servers
  and tools selected for the workflow are reachable.
- Reads `db/db.sqlite` through `$DB_PATH`, which is a **read-only snapshot**
  for this call (absent if the workflow has no DB yet). A script never
  writes the store; a Dashboard writes only through `updateField(s)`.
- The only writable folder is `$REPORT_CACHE_DIR` (shared by every viewer and
  every script of this workflow). The workflow folder is readable.

Best practices:

- **Cache in the script, not the page.** Every call runs the script again and
  the platform keeps no cache. When the source is slow, paid, or
  rate-limited, keep a file per query in `$REPORT_CACHE_DIR` (named by the
  script and its normalized args), return it while it is younger than a
  sensible TTL, and include `fetched_at` in the output so the page can show
  "as of". Write the cache atomically (temp file, then rename) because two
  viewers can refresh at once. Let an explicit `{"refresh": true}` arg skip
  the cache for a Refresh button.
- **Return what the page renders.** Aggregate, filter, and trim in the script;
  never ship whole upstream payloads to the browser.
- **Degrade, don't fail.** If the upstream call fails and a cached copy
  exists, return it with `stale: true` and the error message instead of
  exiting non-zero; the page shows the stale notice.
- **Keep it fast and read-only.** Aim well under the 60 s cap: bounded page
  sizes, a timeout on every HTTP/MCP call, and no side effects on the
  outside system — a report view must never create, update, or send
  anything upstream.
- **Never print secrets.** Output reaches every viewer; errors are redacted
  of known secret values, but stdout is returned as written.
- In the page, call it inside `window.report.ready`, show a loading state
  (the call can take seconds), catch the error into a visible message, and
  wire a Refresh button that calls it again with `{ refresh: true }`.

```js
window.report.ready(async function () {
  const el = document.getElementById('deals');
  async function load(refresh) {
    el.textContent = 'Loading…';
    try {
      const out = await window.report.run('code/reports/open_deals.py', { days: 7, refresh });
      renderDeals(el, out.deals, out.fetched_at, out.stale);
    } catch (err) {
      el.textContent = 'Could not load live deals: ' + err.message;
    }
  }
  document.getElementById('refresh-deals').onclick = () => load(true);
  await load(false);
});
```

Test the script before wiring it: run it once from the shell with
`REPORT_ARGS='{"days":7}'` set, confirm one JSON value on stdout, then
`validate_report_html()` and `preview_report()` (the preview runs the script
for real).

### Sending a Dashboard request to the workflow agent

When choosing between a business approval, a Pulse decision, a blocking
checkpoint, and an agent request, read
`read_skill(skills=[{"name":"builder-reference","path":"references/human-in-the-loop.md"}])`.
The API details below implement the Dashboard-to-chat pattern.

`await window.report.sendChatMessage(message, { requestId })` sends directly
from the Dashboard action, without a second popup or chat-choice step. The app
uses the same workflow-scoped chat queue as the human-decision panel's
**Ask in chat**: reuse an interactive chat,
queue behind its running foreground turn, or create a chat if none exists.
Scheduled/view-only/bot tabs are excluded. This sends a conversational request;
it does not directly execute a route or trigger the scheduler.

Call this only from a user action handler. Never call it from `ready`, render,
polling, or refresh handlers. It is unavailable in `preview_report`. Before the
host initializes, it rejects instead of queuing a request to replay on load.
The host supplies the workspace; Dashboards cannot choose another workspace.
Messages must be non-empty and at most 12,000 characters.

For an existing Dashboard-owned approval, await the DB commit before offering the
action request. Include the exact row/proposal version and the intended route
or consumer step. Ask the agent to re-read the current approval, skip work
already applied, and act only on that item. Do not use a generic “run workflow”
message when it would repeat collection/audit or another approval gate.

```js
// In a user click handler, with the button disabled until finally.
await window.report.updateField('audit_findings', row.id, 'status', 'approved');
showStatus('Approval saved. Sending action request…');
const result = await window.report.sendChatMessage(
  `Apply only approved audit_findings row ${row.id}, proposal version ${row.proposal_version}. ` +
  `Re-read its current approval and proposed fix from the database; skip it if already applied. ` +
  `Use the existing remediation route for this item, then verify and refresh the Dashboard.`,
  { requestId: `finding:${row.id}:${row.proposal_version}:apply` }
);
showStatus(result.queuedBehindRunningTurn
  ? 'Request queued behind the current chat turn.'
  : 'Request queued in chat.');
```

Use real schema fields/versions and actual route or step IDs, never copy
placeholder names into a workflow that lacks them. The successful result is
`{ status: 'queued', tabId, reused,
queuedBehindRunningTurn }`. Queued is not proof that work started or completed;
show applied/verified outcomes only from fresh execution evidence.

The approval write and chat enqueue are separate operations. A failed enqueue
does not undo the saved approval (a later scheduled consumer can still read it).
On failure, keep that approval visible and offer **Send action request** again
without rewriting it. Catch errors locally and re-enable the
button in `finally`. Repeated clicks while sending share one request;
an optional stable `requestId` (max 200 characters) reuses a successful receipt
for the same message in the current Dashboard view (up to 100 receipts). Reloads,
different views, and later sessions still require the consumer's durable
already-applied check. This API does not provide atomic approval-and-execution.

### Referenced files must live under `db/`

A Dashboard may only reference paths under `db/`. That is the durable store the
Dashboard tab reads; anything else is invisible to it.

A step's own execution folder (`runs/iteration-N/<group>/execution/...`) is
per-run scratch, not a referenceable location. A screenshot, PDF, or export
captured there is not reachable from the Dashboard just because it exists on disk
— the step must copy it into `db/` (for example `db/reports/<key>/proof.png`)
for the Dashboard to show it.

Referencing a path that was never published produces a broken-image icon and
nothing else at runtime: no error, no log line, no failed run. Verify the file
exists at its `db/` path, not only at the path the step wrote.

`validate_report_html()` checks every literal path the document references
(`window.report.get/getText/getHtml/fileUrl/mediaUrl/openFile('db/...')`, `src="db/..."`,
`href="db/..."`) and reports the missing ones. A path assembled at runtime from
variables is invisible to it — prefer literal paths, or verify those yourself.

### Workshop and Run boundaries

Workshop owns Dashboard authoring and may create or edit `db/reports/index.html`.
Keep Dashboard-only changes presentational unless the user also asked to change
workflow behavior or evaluation. Run mode never authors Dashboard pages; it only
produces the durable data those pages read.

Platform human-decision lifecycles stay in the Pulse panel and chat. A Dashboard
can expose its own business approval buttons using `updateField`/`updateFields`
and hand a specific request to the workflow agent using `sendChatMessage`.
See "Writing back" and "Sending a Dashboard request" above.

For typed route rows, `summary_text` contains only the shared lead; `message`
remains the complete rendered digest for older Dashboards. Render the lead plus
route entries once, or the complete message as a fallback, never both.

### Goal progress: shared data and an optional ready-made widget

Dashboards can show the same primary metric, supporting metrics, and measured history
as Pulse. No additional collector, table, chart library, or custom CSS is needed:

```html
<section id="goal-progress"></section>
<script>
window.report.ready(async function () {
  await window.report.renderGoalProgress('#goal-progress');
});
</script>
```

The optional widget renders primary/supporting cards, targets, change since the
previous measurement, trends, freshness, and expandable definitions/evidence/history.
It inherits the Dashboard's text color, fits the available width, and replaces its
contents on refresh. Use an empty `div` or `section` as the container. Keep the
returned promise inside `ready` so preview_report can observe loading/errors.

For a custom layout, `await window.report.getGoalMetrics()` returns
`{ metrics, observations, progress }`. Each progress item contains `metric`,
`current`, `delta`, `state`, `stale`, `targetMet`, `latest`, `history`, and `numeric`.
`current` can be undefined: never turn missing data into zero. The latest failed
measurement remains unavailable rather than falling back to an older good value.
History includes up to 120 comparable observations per active metric, with scope,
unit, and evidence preserved. Use `window.report.query` against
`workflow_goal_metrics` and `pulse_goal_observations` for a longer/custom history;
check `sqlite_master` first for workflows that have not configured measurements.
These are platform-owned, read-only tables for Dashboards; use the managed goal tools
for changes, not `updateField`/`updateFields` or direct SQL writes. Run `/setup-goals`
when definitions or collection are missing. Do not invent targets or samples.

Adding this section is optional and does not replace the Dashboard's own navigation
or layout. Offer it when a user wants goal tracking in their dashboard. Validate
and preview the Dashboard using the normal Dashboard tools after adding it.

### Costs: matching Dashboard helper

Use the canonical cost ledger without creating Dashboard-owned copies. The
helper works in the app and `preview_report`:

```html
<section id="costs"></section>
<script>
window.report.ready(async function () {
  await window.report.renderCosts('#costs', { days: 30 });
});
</script>
```

The widget includes its own responsive styling and expandable details. No chart
library or design work is required. Add it only when useful to the user's Dashboard.

For custom layouts, call `getCosts({ days: 30 })`:
- Costs returns `{ summary, history, window_total_usd, state }`. `summary.total` and `summary.by_scope` are **all-time** recorded amounts.
  `summary.by_model`, `summary.by_date`, and `window_total_usd` cover only the requested UTC date window.
  `summary` is null and `state` is `unavailable` if the ledger is unavailable.
  Options support `days` (1–90, default 30) and `before` (exclusive YYYY-MM-DD).
  Read older daily pages using `history.next_before` when `history.has_more` is true.
  Do not add the repeated all-time total across pages. USD costs are recorded ledger
  amounts and do not prove every call was priced. No model prices are guessed.

Continue using `window.report.ready` for loading and refresh. Returned promises
reject on read failures, which the host exposes in the Dashboard's error surface.

### Composition widgets: tables and activity (optional)

Prefer these over hand-rolled tables and activity feeds. Both are optional
helpers, not mandates: a fully custom section remains valid, and mixed
Dashboards (custom hero plus a widget table) are fine. The widgets inherit
the Dashboard's theme and include responsive styling, loading/empty states,
and touch-safe controls. Use empty `div`/`section` containers; each renderer
replaces its own contents on refresh, returns its data, and rejects on load
failure. The app and `preview_report` share the runtime.

```html
<section id="leads"></section>
<section id="activity"></section>
<script>
window.report.ready(async function () {
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
  section from the run and Pulse summaries already in
  `org_dashboard_notifications`, route-grouped via `route_summaries_json`
  and markdown-rendered, with the execution-log fallback built in. Zero
  config: `limit` (1–100, default 30) is the only option. Missing history
  tables render a "no activity yet" setup message, not an error.
