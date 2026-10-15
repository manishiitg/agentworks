---
name: relay-dashboard
description: Create or update a Relay Dashboard using the shared HTML runtime, project data and read-only scripts.
external: true
external_description: "How a Relay's dashboard is authored and fed with recorded run data."
external_scopes: ["relays:write"]
external_note: "External mapping: dashboard action=list|get|link|create|update|validate|preview|publish|restore uses the shared revisioned dashboard handlers and existing project authoring consent. Read/write Relay helper source through files when allowed, or delegate broader changes through builder action=chat. Internal Relay bridge variables and client URLs are server execution context, not externally usable credentials."
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
- Interactive motion uses `window.report.motion.create({ id, duration, render,
  onChange })` inside `window.report.ready`. The designer supplies all visuals
  and controls. The returned player has `play`, `pause`, `replay`, `seek(seconds)`,
  `setSpeed(multiplier)`, `getState` and `destroy`. Render synchronously from
  absolute time/progress and previously loaded recorded data; no fabricated
  history. Stable ids replace previous players on refresh, preserving position.
  Playback starts paused; reduced motion initially shows the end state.
- Optional MP4 uses the core `dashboard action=export_motion` operation (app
  Builder: `export_dashboard_motion`), with `composition_path` under
  `code/motion/` and a unique `submission_id`. The reply returns `export_id`
  immediately; poll the same action with that id until completed/failed.
  Reuse the submission id for retries of that same export only. Save a standalone HyperFrames `index.html`, local assets and
  `snapshot.json` containing `as_of` (ISO timestamp), `source` and `data`.
  The composition reads that local snapshot, not `window.report` or live data.
  Export requires current Builder authoring access and, over MCP, existing
  authoring consent plus `dashboards:read` and `runs:execute`. The server needs
  Node/npm, Chrome, FFmpeg and FFprobe; if unavailable, say video export is
  unavailable there. Interactive motion needs none of those render tools.
  Limits: ten minutes, 128 MB source, 256 MB video. This is a core capability
  independent of personal products. The completed `result` returns the video `path` plus
  editable source and snapshot paths. Play with `window.report.mediaUrl(path)`
  and native video controls, and show its snapshot time. Run people can use a
  shared exported video; export grants them no new execution permissions.
  Never start export on dashboard load/refresh. Layout/style remain user choices.
- Run `validate_report_html` for each changed document and `preview_report`
  when rendering is available. Check data, errors, interactions and requested
  pane sizes in both themes. Use `get_report_link` for an authenticated live
  dashboard URL; present the returned URL and its shareable/warning metadata.
  It grants no access. `publish_relay` publishes the API source, not a Dashboard.

Relay execution still has no automatic DB/KB/learnings closing stages. Dashboard
assets and authoring tools are optional and do not add workflow plans or Pulse.

## Optional native chat widgets

In a managed Crew, workflow or Relay dashboard, the designer may add an open
chat panel, a button, or both. Choose the placement, size, label, visibility and
responsive layout in the dashboard's HTML/CSS. There is no required sidebar.
For example (dimensions are only an example):

```html
<section id="questions" data-report-chat-panel style="height:420px"></section>
<button data-report-chat-button data-report-chat-target="#questions">Ask</button>
```

Give panel containers a nonzero width and height. The host renders native chat
above that rectangle and follows scrolling, resizing and visibility. Keep the
container empty; author help text or other controls outside it. To start closed,
add `data-report-chat-state="closed"`. A button without `data-report-chat-target`
opens a native chat dialog. Button text and its location are the designer's choice.

After the runtime is ready, `window.report.chat` exposes only:

- `available`: whether this dashboard can open native project Ask.
- `mount('#questions')`: register an existing div/section as a panel.
- `open('#questions')`: open that panel; omit the selector to use the first
  panel, or a dialog if there is no panel.
- `close('#questions')`: hide that panel; without a selector, close the dialog
  and the first panel.

Call these from `window.report.ready` or an explicit button action and catch
rejections. Do not repeatedly open chat on data refresh. Opening/mounting only
opens the conversation; it never submits a question. The person types and sends
through the native composer. There is no widget `send`/`ask(text)` method.
Selectors resolve within this dashboard, and callers cannot choose another
project, account or chat. Native Ask rechecks current project access and uses
the signed-in person's existing conversation on both app dashboards and shared
published links. Right-click Ask about a value still requires explicit Submit
and captures the selected value and dashboard publication.

Preview has no conversation: `chat.available` is false. Code has no native
project Ask adapter yet; leave its chat widgets unavailable. Check availability
before calling widget controls. These widgets require the managed dashboard
viewer; loose HTML documents do not acquire native chat through markup alone.

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
`dashboards:read` plus existing product authoring consent for drafts/publication:
`builder:chat` for workflows, `relays:write` or Builder consent for Relays,
`crews:write` for Crews, and `code:run` for owned Code projects. Workflow/Relay
consent retains its companion read/run scopes and Builder enablement. Current
account/product and project owner permissions (and workflow editor permissions), ID bounds and folder caps still
apply. Connections without existing authoring consent gain no editing rights;
connections receive no replacement scopes automatically.
Preview additionally requires `runs:execute` and edit access because it can run
live data scripts. It keeps the connection's scope, expiry and revocation checks.
Publishing does not run the Relay API or a workflow. SQLite files, transcripts,
secrets and runtime selections stay outside dashboard authoring.
