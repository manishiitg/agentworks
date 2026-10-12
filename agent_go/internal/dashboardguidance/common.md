## Shared Dashboard contract

This contract is composed into each product's Dashboard instructions from one
source. Product-specific execution and access rules follow separately; this
contract does not enable capabilities or grant permissions.

## Contract

Layout, typography, colors, charts, navigation and visual style follow the
user's preferences and supplied references. This skill describes platform usage.

- Dashboard documents are HTML files under `db/reports/`; `index.html` is the
  backward-compatible default. Supporting assets belong under `db/assets/`.
  Write complete HTML with a non-empty `<title>`. Keep changing data in the
  project store and read it through the live API rather than regenerating HTML
  on every run. Do not pin the body height or create a nested page scroll container.
- For multiple top-toolbar views, create one HTML document per useful view.
  The toolbar discovers them automatically. Optionally add `db/reports/views.json`
  with `{"schema_version":1,"default":"overview","views":[{"id":"overview","title":"Overview","path":"db/reports/index.html","order":0}]}`
  to control titles, order, and the default. Paths must remain under
  `db/reports/` and end in `.html`.
- Inspect the existing `db/reports/` folder before changing it. Preserve useful
  content and the user's established organization and visual language.
- Documents may use separate toolbar views or internal navigation as requested.
  The runtime supports different pane widths; verify the requested presentation.
- Support both app themes using `:root.dark` or `[data-theme="dark"]`.
- Choose any CSS/component approach that fits the Dashboard, including plain CSS,
  Tailwind, Bootstrap, daisyUI, another framework, or a combination that works
  in the browser. Version-pinned HTTPS CDN stylesheets and browser scripts are
  supported. If using daisyUI, `<html data-report-ui="daisyui">` asks the host
  to inject its pinned CSS; daisyUI alone does not include Tailwind utilities.
  These are compatibility facts, not a preferred stack.
- Chart.js, other browser libraries, SVG/canvas and HTML/CSS are supported.
  Verify the chosen dependency loads and handle load failures.

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

## Project data and actions

Use a managed database only when durable structured data is needed. Inspect the
schema with `query_workflow_db`; save idempotent migrations under `db/migrations/`
and apply them with `apply_workflow_db_migration`. Change rows through
`mutate_workflow_db`, never direct edits to SQLite, WAL or SHM. Read with
`window.report.query(sql, params)` using positional `?` bindings.

When no database is needed, read allowed project files with `window.report.get`,
`getText` or `getHtml`. Use `getHtml` for themed inline Markdown and its workspace
links/images; never display Markdown source as the rendered document.

A user action may call `window.report.sendChatMessage(message, { requestId })`
with an item-specific stable request ID. The message describes the exact change.
Call it only from a user action handler, never from `ready`, render or refresh.
A queued receipt confirms chat delivery, not completion. Inspect fresh execution
evidence before showing a change as applied. Product wrappers describe the chat
routing and any execution boundaries.

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

### Live data from a script: `window.report.run`

`await window.report.run('code/reports/open_deals.py', { days: 7 })` runs
that script on the server and resolves the one JSON value it printed. Use it
when the Dashboard must show the latest state of an outside system (the
project's selected MCP servers or an API behind its secrets) rather than what the last
run stored. Access follows the active product and the authenticated viewer; consult
the product-specific access rules below. The workspace selects MCP servers/tools, secrets (`$SECRET_*`) and
variables (`$VAR_*`); the authenticated viewer's live MCP/Vault permissions
still govern access. Viewing a workspace does not grant credentials.
Published static copies cannot run scripts.

The script contract:

- Lives under `code/` (`.py` → `python3`, `.js`/`.mjs` → `node`); the
  convention is `code/reports/<name>.py`. The validator rejects any other
  path and a script that does not exist.
- Args are limited to 16 KB; at most four report scripts run concurrently.
  Args arrive as JSON in `$REPORT_ARGS` (`{}` when none); treat them as
  untrusted input from any viewer: validate, bound, and never interpolate
  them into SQL, shell commands, or MCP filters without checking.
- Print exactly **one JSON value** to stdout; send logs to stderr. Non-JSON
  stdout, a non-zero exit, more than 2 MB of output, or more than 60 s fails
  the call, and the Dashboard receives the error plus the stderr tail.
- Calls MCP tools the same way a scripted step does:
  `POST $MCP_MCP/<server>/<tool>` with the `$MCP_AUTH` header. Only servers
  and tools selected for the project are reachable.
- Reads `db/db.sqlite` through `$DB_PATH`, which is a **read-only snapshot**
  for this call (absent if the project has no DB yet). A script never
  writes the store; a Dashboard writes only through `updateField(s)`.
- The only writable folder is `$REPORT_CACHE_DIR` (shared by every viewer and
  every script of this project). The project folder is readable within its file guards.

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

## File attachments and inline documents

- Store an attachment's **full project-root-relative path**, for example
  `db/assets/invoices/invoice-001.pdf`, in Dashboard data. Supporting files
  belong under `db/assets/`; preserve existing evidence under other allowed
  authored roots such as `docs/` or `knowledgebase/` when accessible. Confirm
  the file exists before referencing it; copy outside evidence only when authorized.
- All `window.report` file methods resolve from the project root, **not** the
  HTML document's `db/reports/` folder. Use forward slashes, no leading `/`,
  and no `..`. Do not pass a basename (`invoice-001.pdf`), an HTML-relative
  path (`../assets/invoices/invoice-001.pdf`), an OS absolute path
  (`/Users/...`), or a project-prefixed path (`Chats/Work/projects/.../db/...`).
- For an attachment opened on click, use `window.report.openFile(path)`.
  It opens the in-Dashboard preview modal; it does not embed content in the page.
- When the user asks to display a file inline, use
  `await window.report.fileUrl(path)` with a format-appropriate renderer in
  the Dashboard itself. Images can use `<img>`; PDFs need a PDF viewer.
  `fileUrl` resolves the authenticated file but does not render it. A native
  PDF `<iframe>` may be blocked in the Dashboard sandbox: verify it, and use
  a sandbox-compatible PDF renderer (for example, a pinned PDF.js canvas viewer)
  if necessary. Render the actual PDF pages, not an HTML reconstruction of the
  invoice. For PDF.js, load a pinned library and its matching worker; follow
  the [official page-rendering example](https://mozilla.github.io/pdf.js/examples/).
  Render all pages or provide page navigation, and cancel/destroy the old viewer
  on replacement so refreshes cannot render stale pages. Do not loosen the sandbox.
  If inline rendering is unavailable,
  explain the limitation and provide a clearly labeled preview-modal fallback.
- Resolve URLs at display time. Persist paths, never blob URLs, localhost URLs,
  or expiring URLs. Do not use `get_file_link`, `/file` sharing URLs,
  `window.open`, or `target="_blank"` for in-Dashboard attachment previews.
  Use `get_file_link` only when the user requests a separate file-sharing link.

```html
<button id="invoice-preview" type="button">Preview invoice PDF</button>
<img id="invoice-image" alt="Invoice scan" hidden>
<p id="attachment-error" role="status"></p>
<script>
window.report.ready(async function () {
  document.getElementById('invoice-preview').onclick = function () {
    window.report.openFile('db/assets/invoices/invoice-001.pdf');
  };
  // Inline image example; use an actual PDF renderer for a .pdf attachment.
  const image = document.getElementById('invoice-image');
  const url = await window.report.fileUrl('db/assets/invoices/invoice-001.png');
  if (!url) {
    document.getElementById('attachment-error').textContent = 'Invoice scan unavailable.';
    return;
  }
  const previous = image.getAttribute('src');
  image.onerror = function () {
    document.getElementById('attachment-error').textContent = 'Invoice scan failed to display.';
  };
  image.src = url;
  image.hidden = false;
  document.getElementById('attachment-error').textContent = '';
  if (previous && previous.startsWith('blob:')) URL.revokeObjectURL(previous);
});
</script>
```

Adapt the examples to existing files; do not create a PNG merely to satisfy the
example. Revoke blob URLs when replaced or removed. Verify attachment clicks
and inline rendering in the app as well as the page's initial preview; a settled
`preview_report` does not prove every attachment interaction works. Check that
no browser popup opens and that missing files produce a visible error.

### Audio and video

For audio or video under `db/assets/`, persist the workspace-relative path
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

### Durable locations for referenced files

Keep newly produced Dashboard assets under `db/assets/`. Existing evidence under
allowed authored roots such as `docs/` or `knowledgebase/` can also be read through
the file API; preserve its location when accessible. Confirm existence and access
before referencing it, and copy outside evidence only when authorized.

A producer's execution folder (`runs/iteration-N/<group>/execution/...`) is
per-run scratch, not a referenceable location. A screenshot, PDF, or export
captured there is not reachable from the Dashboard just because it exists on disk
— copy it into `db/assets/` (for example `db/assets/<key>/proof.png`)
for the Dashboard to show it.

Referencing a path that was never published produces a broken-image icon and
nothing else at runtime: no error, no log line, no failed run. Verify the file
exists at its `db/` path, not only at the path the step wrote.

`validate_report_html()` checks every literal path the document references
(`window.report.get/getText/getHtml/fileUrl/mediaUrl/openFile('db/...')`, `src="db/..."`,
`href="db/..."`) and reports the missing ones. A path assembled at runtime from
variables is invisible to it — prefer literal paths, or verify those yourself.

## Composition widgets (optional)

These optional helpers provide ready-made tables and activity feeds when desired.
Custom rendering can use the same data APIs. The widgets inherit
the Dashboard's theme and include responsive styling, loading/empty states,
and touch-safe controls. Use empty `div`/`section` containers; each renderer
replaces its own contents on refresh, returns its data, and rejects on load
failure. The app and `preview_report` share the runtime. Keep every call
inside `window.report.ready` so refresh and errors work correctly.

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

- `renderTable(target, { query, params, searchable, sortable })` runs read-only SQL
  and renders a themed, responsive table with an empty state. Columns come
  from the returned rows; numeric columns align right. `searchable` adds a
  filter box matching every cell; `sortable` makes headers toggle
  ascending/descending sort. `query` is required; optional `params` binds `?`
  placeholders without interpolating viewer input.
- `renderActivity(target, { limit })` renders the activity section from the
  recorded execution summaries in `org_dashboard_notifications`, route-grouped
  via `route_summaries_json` and markdown-rendered, with the execution-log
  fallback built in. `limit` is an integer 1–100 (default 30). Missing
  history tables render a setup message, not an error.

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

## Verification

- Re-read changed files, run `validate_report_html` for every changed Dashboard
  document, and then use `preview_report` for each when browser rendering is
  available.
- Verify responsive layout, light and dark themes, navigation, empty states,
  and any buttons or filters. Use the managed browser when it materially
  improves confidence.
- When the user asks for a URL to a Dashboard document, call `get_report_link`
  with that document path (or omit it for the default) and present its returned
  `url` verbatim. It opens the full live
  Dashboard runtime, not the restricted generic HTML file preview. The link is
  protected by current project access. The active product's access rules apply. It contains no credential and
  grants no access or public publication. Inspect `shareable`,
  `scope`, and `warning`; when `shareable` is false (including localhost and
  loopback deployments), describe it only as a same-machine preview and relay
  the warning instead of presenting it as shareable. It uses AgentWorks SSO and
  must not receive a second publish password/login gate; only a separately
  hosted public/static Dashboard uses publish visibility controls.

## Managed dashboards

Use `create_dashboard` to create a draft from `index.html`, optional assets and
`scripts/*.py`, `.js` or `.mjs`. PNG/JPEG/GIF/WebP images and WOFF fonts
use matching base64 data URLs as file values. Supply the user's design; no platform style is
required. `{{dashboard_assets}}` and `{{dashboard_scripts}}` are expanded into
paths for the exact immutable revision. Read with `get_dashboard`; change only
needed files through `update_dashboard` with `expected_revision`. Omitted files
stay unchanged. Validate that revision with `validate_dashboard`, render it with
`preview_dashboard`, and repair errors before `publish_dashboard`. Publication
validates and switches the live pointer. `restore_dashboard` selects a previously
published revision and checks the current draft revision. Never directly edit
managed revision files or their state.

`list_dashboards` discovers accessible dashboards and URLs. `get_dashboard_link`
returns an authenticated published URL; users need current project access.
Existing HTML documents remain discoverable; use their returned
`document_path` for get/validate/preview/link. To manage an existing document with
revisions, read it and create a new managed dashboard from its source.

External MCP clients use `get_api_spec` then `call_tool`. Current product consent,
account/project roles, connection scope, expiry, revocation and file guards apply;
read the product-specific authoring scopes below. Read-only or retired grants do
not gain write rights. Preview additionally requires `runs:execute` and edit
access because it may run live data scripts. Publishing a Dashboard never starts
product execution. SQLite, transcripts, secrets and runtime settings stay outside
dashboard authoring.

## Requested motion

Use browser-compatible animation only when requested. Keep animation initialization
separate from data refresh, cancel replaced loops, ignore stale asynchronous
results and respect reduced-motion preferences. Check available rendering skills
and tools before offering MP4 export: a Dashboard does not acquire Video Studio's
renderer. Export a deterministic snapshot and play approved video through the
managed media API above.
