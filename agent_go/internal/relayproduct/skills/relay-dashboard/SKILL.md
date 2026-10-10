---
name: relay-dashboard
description: Create or update a Relay Dashboard using the shared HTML runtime, project data and read-only scripts.
---

# Relay Dashboard usage

Follow the user's layout, colors, typography, charts and motion preferences.
This skill describes platform usage. Inspect existing documents before editing.

- Save HTML documents under `db/reports/`. `index.html` is the default;
  supporting assets belong under `db/assets/`. The Dashboard toolbar discovers
  multiple documents. Optional `db/reports/views.json` controls titles, order
  and default: `{"schema_version":1,"default":"overview","views":[{"id":"overview","title":"Overview","path":"db/reports/index.html","order":0}]}`.
- Open the **Dashboard** button in Relays. Graph remains the default Relay view.
  The Dashboard uses the same host runtime as Goals, Crew and Code.
- Put data loading inside `window.report.ready(async () => { ... })`.
  The callback runs again on refresh. Keep one-time animation initialization
  separate from data updates; cancel old loops and ignore stale async results.
  Respect reduced-motion settings and support the app's light and dark themes.
- Read allowed authored files with `window.report.get(path)`, `getText(path)`
  or `getHtml(path)` using relative paths under `db/`, `docs/` or other allowed
  authored folders. Run scratch folders are not exposed by these methods.
- Use an optional managed database only when durable structured data is needed.
  Inspect schema with `query_workflow_db`, save idempotent SQL migrations under
  `db/migrations/` and apply with `apply_workflow_db_migration`. Mutate through
  `mutate_workflow_db`. Never directly edit SQLite, WAL or SHM files.
  Query with `await window.report.query('SELECT name FROM items WHERE status = ?', ['open'])`.
  Optional `renderTable(target, { query, params, searchable, sortable })` uses
  the same parameter bindings. No database is required for a file/script Dashboard.
- For current external data or calculations, write `.py`, `.js` or `.mjs`
  scripts under `code/`, commonly `code/reports/`, and call
  `await window.report.run('code/reports/summary.py', { days: 7 })`.
  Args arrive as JSON in `REPORT_ARGS`; validate them. Print exactly one JSON
  value on stdout and send logs to stderr. Limits are 60 seconds, 2 MB stdout,
  16 KB args and four concurrent platform report runs. Python can import helpers
  under `code/` and installed sandbox dependencies.
- The script uses the Relay's selected MCP tools, secrets and variables under
  the authenticated viewer's live permissions. Access to the Relay does not
  grant credentials. Use `POST $MCP_MCP/<server>/<tool>` with `$MCP_AUTH` for
  selected MCP tools and `SECRET_*`/`VAR_*` for admitted values. Never print secrets.
  Scripts should only read external sources; use explicit chat actions for changes.
- Scripts read the workspace and an optional read-only `DB_PATH` snapshot.
  `DB_PATH` is unset when no database exists. Only `REPORT_CACHE_DIR` is writable.
  Every call executes afresh; implement caching there if useful. A refresh flag
  in args is your script's convention, not platform caching.
- Read recorded Relay invocations with `await window.report.getRelayRuns({ version: 'draft', limit: 20 })`.
  The response is `{ version, runs }`. Each run contains `run`, `run_id`, `status`,
  `attempt`, `started_at`, `duration_s`, `steps` (name/status/reused) and `result`.
  Select an explicit published version, e.g. `version: 'v2'`, to show its runs;
  draft and published histories are separate. Limits are 1–50. Render an empty
  state before the first run, and a clear error when data cannot be fetched.
  Never enumerate `runs/` from a shell or report script: slot filesystem
  privacy prevents listing it. The authenticated reader returns only this
  Relay's recorded summaries and JSON results, without its private journals,
  agent prompts or tool arguments. It never starts a run.
- Dashboard scripts run independently of `relay.py`: they do not get the Relay
  SDK `ctx`, start a published Relay or invoke `ctx.call_agent`. Do not run a
  Relay with external effects during dashboard load or refresh.
- Explicit user actions may call `window.report.sendChatMessage(message, { requestId })`
  with a stable item/action identity. A queued receipt confirms chat delivery,
  not completion. `updateField`/`updateFields` provide schema-checked row edits.
- For MP4 explainers, inspect available Video Studio/HyperFrames capabilities
  before offering export. Snapshot source data and timestamps once and render
  deterministically. Save an approved MP4 under `db/assets/` and play it using
  `await window.report.mediaUrl('db/assets/explainer.mp4')`. Exported video is a
  snapshot and does not refresh with dashboard data.
- Run `validate_report_html` for each changed document and `preview_report`
  when rendering is available. Check data, errors, interactions and requested
  pane sizes in both themes. Use `get_report_link` for an authenticated live
  dashboard URL; present the returned URL and its shareable/warning metadata.
  It grants no access. `publish_relay` publishes the API source, not a Dashboard.

Relay execution still has no automatic DB/KB/learnings closing stages. Dashboard
assets and authoring tools are optional and do not add workflow plans or Pulse.

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
Publishing does not run the Relay API or a workflow. SQLite files, transcripts,
secrets and runtime selections stay outside dashboard authoring.
