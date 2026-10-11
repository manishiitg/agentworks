---
name: work-dashboard
description: Create or update a {{product}} project's visual Dashboard for tasks, notes, plans, status, research, project information, or any other content the user wants to manage visually.
---

# {{product}} Dashboard

Use this skill when the user asks for a dashboard, board, tracker, visual home,
or another project view intended to organize or manage information visually.

## Contract

Layout, typography, colors, charts, navigation and visual style follow the
user's preferences and supplied references. This skill describes platform usage.

- Dashboard documents are HTML files under `db/reports/`; `index.html` is the
  backward-compatible default. Supporting assets belong under `db/assets/`.
- For multiple top-toolbar views, create one HTML document per useful view.
  The toolbar discovers them automatically. Optionally add `db/reports/views.json`
  with `{"schema_version":1,"default":"overview","views":[{"id":"overview","title":"Overview","path":"db/reports/index.html","order":0}]}`
  to control titles, order, and the default. Paths must remain under
  `db/reports/` and end in `.html`.
- This is a general project artifact, not an AgentWorks workflow Dashboard. Do not
  create a workflow, plan, phase, step, route, Pulse configuration, or managed
  workflow merely to provide a Dashboard. A project-owned managed database is
  optional and should be added only when the view needs durable structured data.
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

## Requested motion

When the user asks for Dashboard animation, an MP4 explainer, or both, read this
skill's `references/motion-guide.md`. It documents playback, refresh and the
available Video Studio export path, while leaving design to the user. A
Dashboard project does not automatically acquire Video Studio's renderer or
presentation tools; check the active capabilities before offering an export.

## Project data and actions

- Use `window.report.query(sql, params)` for live structured data. Inspect the
  schema with `query_workflow_db`; create idempotent migrations under
  `db/migrations/` and apply them with `apply_workflow_db_migration`. Change rows
  only through `mutate_workflow_db`. Never open or edit `db/db.sqlite`, WAL, or
  SHM files directly.
- Dashboard documents can read allowed project files with `window.report.get`,
  `getText`, or `getHtml` when a database is unnecessary.
- A Dashboard action that needs the agent to change project state may call
  `window.report.sendChatMessage(message, { requestId })`. Use a stable,
  item-specific request ID and make the message describe the exact requested
  change. Queued only means the message reached the project chat; it does not
  prove completion.
- `window.report.updateField` and `updateFields` may be used for explicit,
  user-initiated edits. Keep SQL parameterized and scope updates to stable keys.
- For data that must be current from an outside system (Notion, a CRM, an
  API behind the project's MCP servers or secrets), write a small read-only
  `.py`, `.js` or `.mjs` script under `code/`, commonly `code/reports/<name>.py` and call
  `await window.report.run('code/reports/<name>.py', args)` inside
  `window.report.ready`, with a loading state, a visible error, and a Refresh
  button that passes `{ refresh: true }`. Prefer `query` when the data is
  already in the project database. The script contract:
  - Supported in Goals/Workflow, <!-- product:relays -->Relays, <!-- /product -->Crew and Code project roots. Crew
    readers follow the existing project access rules; Code scripts are owner-only.
    Scripts use the project's selected MCP tools and secrets (`$SECRET_*`) under
    the authenticated viewer's live MCP<!-- product:mcp-gateway -->/Vault<!-- /product --> permissions. Viewing a project does
    not grant access to its credentials. Published static copies cannot run scripts.
  - Args arrive as JSON in `$REPORT_ARGS` (`{}` when none). Treat them as
    untrusted and validate them.
  - Print exactly one JSON value on stdout; logs go to stderr. Limits: 60 s
    and 2 MB. A failure reaches the page as an error with the stderr tail.
  - Call MCP tools with `POST $MCP_MCP/<server>/<tool>` and the `$MCP_AUTH`
    header.
  - `$DB_PATH` is a read-only snapshot, unset when no database exists. Python
    can import helpers under `code/` and installed sandbox dependencies. The only writable folder is
    `$REPORT_CACHE_DIR`.
  - There is no platform cache. Cache per query in `$REPORT_CACHE_DIR`:
    include `fetched_at`, write atomically, let `{"refresh": true}` bypass it,
    and return the cached copy with `stale: true` when the source fails.
  - Never create, update, or send anything upstream from a Dashboard script,
    and never print secrets.
  - Test the script once from the shell with `REPORT_ARGS` set before wiring
    it into the page.
- Treat files outside `db/reports/` as project evidence. Do not move or rewrite
  unrelated project content solely to fit a Dashboard layout.

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
  run and Pulse summaries in `org_dashboard_notifications`, route-grouped
  via `route_summaries_json` and markdown-rendered, with the execution-log
  fallback built in. `limit` is an integer 1–100 (default 30). Missing
  history tables render a setup message, not an error.

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
  protected by current project access. Crew readers can open shared Crew
  dashboards; Code dashboards remain owner-only. It contains no credential and
  grants no access or public publication. Inspect `shareable`,
  `scope`, and `warning`; when `shareable` is false (including localhost and
  loopback deployments), describe it only as a same-machine preview and relay
  the warning instead of presenting it as shareable. It uses AgentWorks SSO and
  must not receive a second publish password/login gate; only a separately
  hosted public/static Dashboard uses publish visibility controls.
- Tell the user the Dashboard is available from the {{product}} **Dashboard** button.

## Managed dashboards and external MCP

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
returns an authenticated published URL; users need current project access. Code
stays owner-only. Existing HTML documents remain discoverable; use their returned
`document_path` for get/validate/preview/link. To manage an existing document with
revisions, read it and create a new managed dashboard from its source.

External MCP clients use `get_api_spec` then `call_tool`. Request explicit
`dashboards:read` and `dashboards:write` consent; older connections gain no new
rights automatically. Read/write scopes retain workflow and Crew ID bounds.
Preview additionally requires `runs:execute` and edit access because it can run
live data scripts. It keeps the connection's scope, expiry and revocation checks.
Publishing does not run a workflow<!-- product:relays --> or the Relay API<!-- /product -->. SQLite files, transcripts,
secrets and runtime selections stay outside dashboard authoring.
