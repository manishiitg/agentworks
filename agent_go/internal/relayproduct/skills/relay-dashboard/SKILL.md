---
name: relay-dashboard
description: Create or update a Flow Dashboard using the shared HTML runtime, project data and read-only scripts.
external: true
external_description: "How a Flow's dashboard is authored and fed with recorded run data."
external_scopes: ["relays:write"]
external_note: "External mapping: dashboard action=list|get|link|create|update|validate|preview|publish|restore uses the shared revisioned dashboard handlers and existing project authoring consent. Read/write Flow helper source through files when allowed, or delegate broader changes through builder action=chat. Internal Flow bridge variables and client URLs are server execution context, not externally usable credentials."
---

# Flow Dashboard usage

Follow the user's layout, colors, typography, charts and motion preferences.
This skill describes platform usage. Inspect existing documents before editing.

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

Use optional `styles` for authored CSS. The renderer supplies the app's light/dark
theme variables; use classes and CSS to express the requested design. Its bundled
Tailwind classes cover supplied components, not every dynamically invented utility.
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

- For existing HTML dashboards, keep documents under `db/reports/`. `index.html` is the default;
  supporting assets belong under `db/assets/`. The Dashboard toolbar discovers
  multiple documents. Optional `db/reports/views.json` controls titles, order
  and default: `{"schema_version":1,"default":"overview","views":[{"id":"overview","title":"Overview","path":"db/reports/index.html","order":0}]}`.
- Open the **Dashboard** button in Flows. Graph remains the default Flow view.
  The Dashboard uses the same host runtime as Goals and Crew.
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
- The script uses the Flow's selected MCP tools, secrets and variables under
  the signed-in person's live permissions. Access to the Flow does not
  grant credentials. Use `POST $MCP_MCP/<server>/<tool>` with `$MCP_AUTH` for
  selected MCP tools and `SECRET_*`/`VAR_*` for admitted values. Never print secrets.
  Scripts should only read external sources; use explicit chat actions for changes.
- Scripts read the workspace and an optional read-only `DB_PATH` snapshot.
  `DB_PATH` is unset when no database exists. Only `REPORT_CACHE_DIR` is writable.
  Every call executes afresh; implement caching there if useful. A refresh flag
  in args is your script's convention, not platform caching.
- Read recorded Flow invocations with `await window.report.getRelayRuns({ version: 'draft', limit: 20 })`.
  The response is `{ version, runs }`. Each run contains `run`, `run_id`, `status`,
  `attempt`, `started_at`, `duration_s`, `steps` (name/status/reused) and `result`.
  Select an explicit published version, e.g. `version: 'v2'`, to show its runs;
  draft and published histories are separate. Limits are 1–50. Render an empty
  state before the first run, and a clear error when data cannot be fetched.
  Never enumerate `runs/` from a shell or report script: slot filesystem
  privacy prevents listing it. The authenticated endpoint returns only this
  Flow's recorded summaries and JSON results, without its private journals,
  agent prompts or tool arguments. It never starts a run.
- Dashboard scripts run independently of `relay.py`: they do not get the Flow
  SDK `ctx`, start a published Flow or invoke `ctx.call_agent`. Do not run a
  Flow with external effects during dashboard load or refresh.
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
  Builder: `export_dashboard_motion`), with the project selector, `dashboard_id`
  and a unique `submission_id`. By default it captures that published page with
  frozen data through the shared runtime. Pass the person's chosen `theme`,
  `viewport_width` and `viewport_height`; otherwise state the dark 1280×720
  default. Optional `revision`, `duration_s` and `fps` select the saved version
  and recording settings. The shared clock or seekable CSS/Web Animations drive
  frames, including the terminal state. Without a clock, export returns a still
  video and a note; connect the existing drawing code to the shared clock for
  animation, preserving the user's layout and appearance.
  The reply returns `export_id` immediately; poll the same action with that id
  until completed/failed. Reuse the submission id only for retries of that same
  export. A separate explainer is optional: pass `source_mode: "composition"`
  and `composition_path` under `code/motion/`, containing HyperFrames
  `index.html`, local assets and `snapshot.json` with `as_of`, `source` and
  `data`. That composition reads its local snapshot, not live dashboard data.
  Export requires current Builder authoring access and, over MCP, existing
  authoring consent plus `dashboards:read` and `runs:execute`. The server needs
  Node/npm, Chrome, FFmpeg and FFprobe. Limits: ten minutes, 128 MB source,
  256 MB video. Interactive motion needs none of those rendering tools.
  Use completed `result.path` exactly, under `db/assets/motion/`, with
  `window.report.mediaUrl(result.path)` and native video controls. Never replace
  it with an `outputs/` or `code/` path, or save the expiring URL. Handle media
  metadata/errors visibly and show snapshot time where freshness matters.
  Run people can watch/download shared exports without gaining Builder execution.
  Never start export on load/refresh. All placement and style remain user choices.
- Run `validate_report_html` for each changed document and `preview_report`
  when rendering is available. Check data, errors, interactions and requested
  pane sizes in both themes. Use `get_report_link` for an authenticated live
  dashboard URL; present the returned URL and its shareable/warning metadata.
  It grants no access. `publish_relay` publishes the API source, not a Dashboard.

Flow execution still has no automatic DB/KB/learnings closing stages. Dashboard
assets and authoring tools are optional and do not add workflow plans or Pulse.

## Optional native chat widgets

In a managed Crew, workflow or Flow dashboard, the designer may add an open
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

Crew includes projects formerly called Code. Both use these same widgets and
the signed-in person’s existing project conversation; Run access stays in Run
mode. Keep the project identity returned by discovery; do not convert it to
another origin. Preview has no conversation: `chat.available` is false. Check
availability before calling widget controls. These widgets require the managed dashboard
page; loose HTML documents do not acquire native chat through markup alone.

## Managed dashboards and external MCP

Use `create_dashboard` to create a draft from `index.json` by default, or
`index.html` for existing HTML, optional assets and
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
Crew includes projects formerly called Code; keep the returned project identity.
Existing HTML documents remain discoverable; use their returned
`document_path` for get/validate/preview/link. To manage an existing document with
revisions, read it and create a new managed dashboard from its source.

External MCP clients use `get_api_spec` then `call_tool`. MCP uses the same
access as the website. Request explicit `dashboards:read` plus the returned
project's existing product authoring consent for drafts and publication.
Workflow/Flow consent retains its companion read/run scopes and Builder enablement. Current
account/product and project owner permissions (and workflow editor permissions), ID bounds and folder caps still
apply. Connections without existing authoring consent gain no editing rights;
connections receive no replacement scopes automatically.
Preview additionally requires `runs:execute` and edit access because it can run
live data scripts. It keeps the connection's scope, expiry and revocation checks.
Publishing does not run the Flow API or a workflow. SQLite files, transcripts,
secrets and runtime selections stay outside dashboard authoring.

## Share the Flow

Use Share on the dashboard: Only me / Chosen people / Everyone. Shared people open its dashboard, Ask, use published functions and read its files, runs and logs over MCP. Secret values and private logs stay excluded. External clients use `share` action=get|set with `flow_id` (the existing `workflow_id` selector also works); `get_dashboard_share` and `share_dashboard` remain callable aliases. Only the owner changes sharing, with existing authoring consent. Preserve existing people when replacing the people list. The server creates a standard dashboard when one is needed.
