---
name: "dashboard-authoring"
description: "Dashboard MCP operations, JSON-render defaults, the full shadcn catalog, existing HTML, data scripts, preview and team links."
external: true
external_tools: ["*dashboard*", "get_report_link"]
---
# Dashboard API usage

Follow the user’s design and motion preferences. These instructions describe
platform operations and data access.

Sharing a product means sharing its dashboard: people use the published work there through Ask and available function buttons. Crew includes projects formerly called Code; existing Builder and Run roles apply to both origins. Use the current dashboard sharing controls and returned links; a copied URL alone does not give access. Describe people as Builder or Run in replies, never as permission-level labels.

When nobody has designed a dashboard, Share creates an automatic starting page.
It shows the project content and actions first, with an **Ask about this dashboard**
button that opens the existing chat. This starting layout does not constrain
authored dashboards: their designer can choose an open panel, a button, or both.
Automatic Crew and Goal pages reflect the current callable functions without
republishing. Once the owner or their AI edits a page, its content and layout belong to them:
new functions appear there only when they update the page. The platform does
not add controls to custom dashboards. Exact revisions and exports stay saved
snapshots.

## JSON-render is the default

Create new dashboards with Vercel json-render and the complete supplied shadcn
catalog. Call `get_dashboard_catalog` for the pinned component props and supported
actions. The designer chooses layout, typography, colors, navigation and placement;
there are no required sections or sample business values.

Use `index.json` as the editable source: `root`, `elements` and optional `state`.
Each element has `type`, `props` and `children` (empty for a leaf); optional
`on`, `watch`, `visible`, `repeat` and named `slots` use json-render semantics.
Supply its JSON text in `files["index.json"]`. The server validates the catalog
and tree and generates `index.html`; do not hand-edit that generated entry.
Existing HTML documents and saved HTML revisions remain supported. Preserve an
existing page's format unless the person asks to convert it.

Optional `data` declarations use `{statePath, method: "query"|"get"|"run",
sql|path, params|args}` through the existing authorized report APIs. Bind props
with `{"$state":"/summary/total"}`; form inputs can use `$bindState`. Query
params and script args can reference state. Inspect actual schemas/files and use
recorded values; missing history stays unavailable. Local state/form actions and
`refreshData` do not invoke a published function.

The catalog includes all supplied shadcn components plus `Metric`, `BarChart`,
`LineChart`, `AskButton`, `ChatPanel`, `FunctionButton` and `MotionControls`.
Ask opens the existing composer without sending. ChatPanel needs its own id and
positive height; the designer places and styles it. FunctionButton opens this
project's published function form and requires the person to press Run. Existing
project and function access applies; Flow function buttons remain unavailable.
Preview does not open a conversation or execute functions.

JSON-render is a source format, not a ban on CSS. Bundled CSS supplies component
defaults; the designer still controls the page's visual design. Put authored CSS,
media queries and theme overrides in the top-level `styles` string. Use
`props.className` where the component catalog supports it and declare those
selectors in `styles`; app light/dark theme variables are supplied. Bundled
Tailwind utilities cover the supplied components, not arbitrary new utility
classes invented at runtime.

Do not discard existing styling just because the page uses JSON or shadcn. Carry
its requested visual design into the JSON styles and component props when
converting HTML, unless the person asks to change it. CSS is optional when the
supplied props already achieve that design. Inspect the rendered preview; valid
JSON and a ready response alone do not verify the appearance.

Optional `motion: {duration: 12, statePath: "/playback"}` drives the existing shared
clock; chart progress can bind to `/playback/progress`. Place MotionControls only
when desired. MP4 captures the published layout, selected theme and frozen data.
Images/avatars use allowed project assets or inline data images; links are static
http(s)/mailto destinations opened by a click. The JSON format has no raw HTML,
script or arbitrary-fetch component. Existing HTML keeps its browser API contract.

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

## MCP operations

Call `get_api_spec` for current schemas, then `call_tool` with a discovered name
and matching arguments. Use `dashboard` with an `action` for the operations below;
app Builder uses its native dashboard tools. Keep the discovered `workspace_id`,
workflow_id or other project selector exactly as returned; do not infer a new root.

1. `dashboard action=list` discovers accessible documents and authenticated URLs.
   Omit the project selector for cross-project discovery; paginate with limit/offset.
   `published_only: true` matches the Dashboards page. Removing access removes
   discovery and blocks the link.
2. `dashboard action=get` reads the source and revision. Existing HTML may use its
   returned document_path and stays intact when adopted into a managed bundle.
3. `dashboard action=create` takes dashboard_id (lowercase slug), title and files.
   New pages supply index.json text; HTML-only pages may still supply index.html.
   Images/fonts use matching base64 data URLs. Optional scripts go under scripts/.
   Maximum:100 files and8 MiB per bundle.
4. `dashboard action=update` takes changed files/title, optional remove paths and
   expected_revision. Omitted files remain. Stale revisions are refused.
5. `dashboard action=validate` checks the exact source, SQL and referenced files.
   `dashboard action=preview` renders that revision with the real browser runtime;
   inspect errors and screenshots and verify visible content, not only ready status.
   Connected MCP attaches up to two PNG screenshot images; inspect those image
   blocks. Other screenshots open through signed-in browser links. If your client
   cannot display images, report that visual inspection is still needed.
6. `dashboard action=publish` validates expected_revision and switches the live
   pointer. Run users keep seeing the publication while drafts are edited.
   `dashboard action=restore` republishes a saved revision, checking the current draft.
7. `dashboard action=link` returns the authenticated URL. URLs contain no credentials
   and grant no access; recipients sign in with their current project access.
   Localhost links work only on that machine.

Use `{{dashboard_assets}}` and `{{dashboard_scripts}}` for immutable bundle paths.
For example a data declaration can run `{{dashboard_scripts}}/summary.py`; the
script contract below still applies. The HTML widget/API examples below are
references for existing HTML pages, not instructions to replace JSON source.

External connections need `dashboards:read` for discovery, source reads and links.
Drafts and publication use existing product authoring consent: `builder:chat` for
workflows,<!-- product:relays --> `relays:write` or Builder consent for Flows,<!-- /product --> `crews:write` for Crews,
 Workflow<!-- product:relays -->/Flow<!-- /product --> authoring retains its
companion read/run scopes and Builder enablement. Current account and project
owner permissions (and workflow editor permissions), product access, connection bounds and folder grants apply.
Preview additionally requires `runs:execute` and project edit access because live
scripts can execute. Connections without authoring consent cannot edit or publish. Retired dashboard-write
consent adds no authority; reconnect for existing product authoring consent.
Connections gain no new scopes automatically; expiry and revocation remain enforced.

## Existing HTML: optional native chat widgets

In a managed Crew or workflow<!-- product:relays --> or Flow<!-- /product --> dashboard, the designer may add an open
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

Style a panel with optional attributes on its container: `data-report-chat-palette`
(`anthropic`, `warm`, `cool` or `brand`, the app's report themes; they follow the
app's light or dark mode), `data-report-chat-radius` (for example `6px` or
`0.75rem`) and `data-report-chat-vars`, a JSON object of colours (`#rrggbb` or
`H S% L%`) for `background`, `foreground`, `card`, `primary`, `primary-foreground`,
`muted`, `muted-foreground`, `accent`, `border`, `input` and `ring`. Other
properties and invalid values are ignored. The panel's size and position remain
yours, in the dashboard's CSS.

Set the look once for the whole dashboard in its own CSS: define `--report-background`,
`--report-foreground`, `--report-primary`, `--report-border`, `--report-muted` (colours as
`#rrggbb`, `rgb(...)` or `H S% L%`) and `--report-radius` on `:root`; the chat and the
function form follow them, and the per-panel attributes above override them. Set `--report-mode: light` (or `dark`) on `:root`, or `data-report-chat-theme="light"` on a
panel, to make the chat light or dark whatever the app's own mode is. Add
`data-report-chat-fit="viewport"` to a panel to keep it inside the visible window so its
composer never sits below the fold.

Crew includes projects formerly called Code. Both use these same widgets and
the signed-in person’s existing project conversation; Run access stays in Run
mode. Keep the project identity returned by discovery; do not convert it to
another origin. Preview has no conversation: `chat.available` is false. Check
availability before calling widget controls. These widgets require the managed dashboard
host; loose HTML documents do not acquire native chat through markup alone.

## Existing HTML: function buttons

In a managed Crew or workflow dashboard, a button can open a native form for one
of this project's published functions. Mark it with the function's name:

```html
<button data-report-action="extract_ticket_ids"
        data-report-action-defaults='{"ticket":"WEB-1234"}'>Find ticket ids</button>
```

The host shows a form built from the function's input schema, with the optional
`data-report-action-defaults` JSON (an object) pre-filled and editable. The
person presses Run; it runs as the signed-in person and shows the result in the
dialog. The page cannot run a function itself: there is no script method, only
the person's click and Run. The function must be one the signed-in person can
already use in this project (the owner's published functions); a button naming
anything else shows that it is unavailable. Button text, placement and styling
are the designer's choice. Both Crew project origins and workflow dashboards
support these buttons. The chat’s `/` menu lists the same published functions.
Current project access and each function’s allowed people are checked by the
server when called.<!-- product:relays --> Flow dashboards have no function buttons yet.<!-- /product -->

## Existing HTML APIs and the shared script contract

The dashboard runs inside the shared report host. Load data in
`window.report.ready(async () => { ... })`; the host repeats it on refresh.
Use `window.report.query(sql, params)` for read-only project SQL, with positional
parameters, and `window.report.get(path)` / `getText(path)` / `getHtml(path)`
for allowed authored files. SQLite is optional. Database bytes and private
transcripts are not exposed as files.

Inside bundle HTML use `{{dashboard_assets}}` for the exact revision's asset
prefix and `{{dashboard_scripts}}` for its script prefix, for example:

```js
window.report.ready(async () => {
  const data = await window.report.run('{{dashboard_scripts}}/data.py', {refresh: false});
  // Render data using the user's requested layout.
});
```

Script arguments are JSON in REPORT_ARGS. Print exactly one JSON value to stdout;
logs go to stderr. Python may import helpers under code/ and installed sandbox
packages. Limits are 60 seconds and 2 MiB output. DB_PATH, when present, is a
read-only snapshot; REPORT_CACHE_DIR is the only writable cache folder.
Scripts use the project's selected MCP tools/secrets under the authenticated
viewer's live permissions. A shared dashboard grants no new source credentials.
Keep data scripts read-only upstream and never print secrets. Publication does
not run a workflow<!-- product:relays -->, invoke relay.py or publish a Flow API<!-- /product -->.

For requested interactive animation, use `window.report.motion.create` inside
`window.report.ready`; the designer supplies the visuals and controls. See the
on-demand `motion-guide` for play/pause/seek, real historical data and refresh.
Optional MP4 exports use `dashboard action=export_motion` (app Builder:
`export_dashboard_motion`) with the published `dashboard_id`, selected theme
and viewport. It captures the existing page and freezes its data. A separate
explainer remains explicit through `composition_path` under `code/motion/`
and `snapshot.json`. Export needs current authoring access and server
rendering tools; it is never triggered by dashboard load/refresh. The returned
video is a snapshot, played via `window.report.mediaUrl(path)`. Design, layout
and whether to include motion or export controls belong to the user.
