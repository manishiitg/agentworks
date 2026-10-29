---
name: "dashboard-authoring"
description: "Dashboard MCP operations, React TSX defaults, shared shadcn/Recharts components and HTML compatibility, data scripts, preview and team links."
external: true
external_tools: ["dashboard"]
---
# Dashboard API usage

Follow the user’s design and motion preferences. These instructions describe
platform operations, data access and finished defaults when no design is supplied.

Sharing a product means sharing its dashboard: people use the published work there through Ask and available function buttons. Crew includes projects formerly called Code; existing Builder and Run roles apply to both origins. Use the current dashboard sharing controls and returned links; a copied URL alone does not give access. Describe people as Builder or Run in replies, never as permission-level labels.

When nobody has designed a dashboard, Share creates an automatic starting page.
It shows the project content and actions first, with an **Ask about this dashboard**
button for shared Run users that opens the existing chat. Builders use their existing project chat; dashboard Ask buttons and panels are hidden for them. This starting layout does not constrain
authored dashboards: their designer can choose an open panel, a button, or both.
Automatic Crew and Goal pages reflect the current callable functions without
republishing. Once the owner or their AI edits a page, its content and layout belong to them:
new functions appear there only when they update the page. The platform does
not add controls to custom dashboards. Exact revisions and exports stay saved
snapshots.

## React TSX dashboards

Author new dashboards as `files["index.tsx"]` through dashboard action=create or
dashboard action=update. The server compiles the submitted source and saves an immutable
hosted Dashboard revision. Do not create a separate Vite/npm application, ask the
person to run a dev server, or hand-edit the generated index.html/dashboard-app.js/
dashboard-app.css. A compile error leaves the current draft and live page unchanged.
TypeScript is transpiled; this does not claim a full TypeScript type check.

Read dashboard action=catalog for supported imports. React, react-dom,
react-dom/client, react/jsx-runtime @agentworks/dashboard and recharts are supplied by the
platform. Relative imports resolve only within the submitted bundle: .tsx, .ts,
.js, .mjs, .json, .css and image/font assets. Other npm packages and network imports
are not installed by this authoring path; bundle needed browser code as local
files or use the supplied modules. No server code runs during compilation.

Prefer the shared components exported by `@agentworks/dashboard`: Card, Stack, Grid, Heading, Text, Metric, Table, BarChart, LineChart, Button, Tabs, Accordion, Collapsible, Alert, Badge, Input, Select, Checkbox, AskButton, ChatPanel, FunctionButton and MotionControls. `components.Name` exposes the complete catalog; `RefreshButton` refreshes loadData. BarChart and LineChart use Recharts. For more complex charts, import Recharts primitives directly from `recharts`. Do not copy platform component implementations or build a JSON interpreter.

Pass catalog props directly in TSX; nullable props may be omitted. Button uses `onPress`; other catalog events use `on={{change: callback}}`. `useDashboardBinding(path)` returns a state value/setter, `useDashboardStore()` exposes the shared store, and `bindings={{value: path}}` connects inputs or Tabs to that state. The host supplies the providers. Use ordinary React state, conditions, loops and composition for bespoke layouts and interactions. JSON files may still contain data; they are not a dashboard layout format.

Default-export a React component receiving `{data, report}`. Optionally export
`async function loadData(report)` to fetch the initial data and each explicit
refresh. The host waits for that promise and committed React DOM before reporting
preview readiness or freezing export data. Use React state for local navigation,
filters and interactions. Put initial data reads in loadData, not an untracked
effect or top-level asynchronous call. Do not start business actions on page load.

Keep the compiled page below 1.5 MB. Store large datasets in project files or the database and load them through `report.get` or `report.query`; keep library assets separate. Oversized pages show a size error in the app and preview. This source limit does not restrict the data those APIs return.

```tsx
import { Card, Grid, Metric, Table, AskButton, RefreshButton } from '@agentworks/dashboard';
type Summary = { total: number; rows: (string | number)[][] };
export async function loadData(report): Promise<Summary> {
  return report.get('db/summary.json'); // inspect the actual file/schema first
}
export default function Dashboard({data}: {data: Summary}) {
  return <Card title="Overview">
    <Grid columns={2}><Metric label="Total" value={data.total}/></Grid>
    <Table columns={['Record', 'Value']} rows={data.rows}/>
    <RefreshButton/><AskButton label="Ask about this dashboard"/>
  </Card>;
}
```

For uploaded references and live video, image, character or document results,
read `panel-dashboard/references/media.md`. Shared exports include UploadZone,
MediaGallery and MediaPreview. Managed result writes refresh loadData during a
turn; preserve stable result IDs so the selected player keeps its state.

The report prop (also imported as `report` from @agentworks/dashboard) uses the
same authorized query/get/run, file/media, updates, costs, goal metrics, Ask,
published-function and motion APIs documented below. Function buttons keep the
same data-report-action attributes in JSX. Preview never sends chat or runs a
published function. Use report.motion for seekable animations and MP4 exports.
Project permissions, selected sources and existing execution rights still apply.

Import ordinary CSS for responsive design; the shared base theme is supplied.
Arbitrary Tailwind utility classes are not compiled from dashboard source. Keep
styles customizable and preserve the person's visual design. Read and inspect the
existing page, data and controls before converting; carry every view and action
across. Keep the same dashboard_id so its list entry, shared link and publication history
remain intact. Confirm publish with dashboard action=list and dashboard action=link.

Two formats, two jobs: TSX for live dashboards with data, buttons and functions;
HTML (index.html) for a quick shareable page such as a decision, a report or a
file view. HTML pages render, edit, publish, restore, share and export like TSX.
JSON dashboards are not supported: do not submit index.json.

## Shared controls and charts

Use `dashboard action=catalog` for the actual shared component props. `AskButton` and
`ChatPanel` appear only for shared Run users and are hidden for Builders and preview.
Ask opens the existing composer without sending. FunctionButton opens the current
published function form; the person presses Run to execute it. Current project and
function permissions apply; Flow function buttons remain unavailable.

Use `report.motion.create()` for recorded-data playback, pass its clock to
MotionControls, and drive chart progress through React state. Use this shared toolbar
by default rather than duplicating transport buttons. It floats inside the authored
positioned playback area; reserve space to keep data visible. Labels, visible controls,
theme and placement remain customizable, including placement="inline". See the
[motion guide](motion-guide.md) for the actual props and example. Recharts animations
are disabled in shared charts so seek/export stays deterministic. Keep ordinary CSS
and mobile layouts editable; inspect real previews and preserve the person's design.

## Finished dashboards from a plain-language request

When the person gives a goal without a design brief, take responsibility for a
finished first design. Infer the composition from the task and available data;
do not ask them to choose fonts, spacing, card layouts or a template. Ask only
for missing business meaning or data access that changes what the page can show.
A dashboard request includes designing the page, not just arranging text blocks.
Use the default dark theme as the foundation unless the person asks for light
or supplies an existing visual direction. Present dark previews first, and refine
the composition after seeing its desktop and mobile previews. Their explicit preferences always take priority.

Choose an editable starting composition, then adapt or combine it:

| What the person wants | Useful starting composition |
| --- | --- |
| Understand performance | A short title and recorded period, a small row of meaningful Metric components, a primary chart in a Card, then a compact breakdown Table. Show units, source and last refresh only when known. |
| Manage work or follow progress | A clear current status, a table of tasks or records with useful owner/date/status columns, then next actions or recent activity. Add totals only when they help a decision. |
| Research or compare options | A concise finding, a structured comparison Table, grouped evidence and source links, then an Ask entry point for follow-up. Long supporting notes can live in Tabs or Accordion. |
| Use an assistant or service | The actual purpose, Card components for the available published functions, and AskButton or a placed ChatPanel. Explain each action in ordinary words; no invented metrics or decorative charts. |

These are examples for the agent, not user-facing modes or required sections.
Use shared components and props returned by dashboard action=catalog, with React and its supplied runtime imports. For a broad page,
a vertical Stack with `align: "stretch"` avoids narrow child cards. A Grid of
Metric components gives a compact summary; a Grid of Card components can pair a
chart with its breakdown. Card already supplies its surface and heading; avoid
nesting another card around every line. Use Heading and lead Text directly for
the page introduction. Put related controls together and give the primary work
more room than secondary notes. On mobile, preserve the reading order when
columns stack; dense tables may scroll within their own region.

Keep one clear visual hierarchy: a descriptive page title, useful section titles,
short labels and formatted values. Use the theme's spacing, surfaces and restrained
accent colors consistently. Explain a number with its unit and period rather
than repeating the same label in several places. A chart should answer a question;
a line needs recorded ordered history, and a bar chart needs comparable categories.
If that data does not exist, use the actual records or an honest empty state with
a useful next step. Avoid walls of prose, equal-sized empty boxes, generic welcome
banners and repeated product labels that do not help the person use the page.

CSS is a normal part of this design work when component props do not achieve the
result. Use supported semantic className hooks and top-level styles for custom
density, emphasis, responsive arrangements and visual identity. Do not interpret
“default theme” or “shadcn” as a ban on CSS, or claim that removing CSS improves a
page. Preserve existing authored styling unless asked to change it. Everything
in the initial composition remains editable; do not inject this layout into
someone's custom page on refresh.

Before presenting it as finished, inspect the actual preview images: the first
screen should make the purpose, most important information and next action clear.
Check real content length, useful chart dimensions, whitespace, contrast and
mobile reading order. Refine the page where the screenshot looks rough even if
validation and ui_checks pass. Report missing data plainly; never fill a real
business page with fictional values to make it look complete.

## Dashboard UI workflow

1. Read the existing page and the user's design references. Keep their content,
   visual identity, data sources and chosen layout. When no direction is given,
   use the platform's default theme and make the layout fit the task; do not
   invent brand rules, required sections, or sample business data.
2. Read `dashboard action=catalog` for supported props. Choose components for the
   content: readable text, metrics, charts, forms or tables as needed. A catalog
   is a set of available controls, not a complete page design. Use the actual
   data shape and include meaningful loading, empty and error states.
3. Style the composition deliberately. The default theme supplies page spacing,
   typography, themed surfaces, buttons and metric styling. Grid uses one column
   below640px, then its requested columns. Override it through supported
   className/CSS when the user's layout needs different behavior. Check long
   labels, dense tables, keyboard focus and names for icon-only controls.
4. Keep everything customizable. Imported CSS is normal authored CSS and
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

## React dashboards stay in Dashboards

A request to “move dashboards to React” means author index.tsx through the managed
tools, retaining the same dashboard_id and publishing it in the existing list.
Inspect the existing pages, design, data and controls first. Preserve each view,
live query, script and explicit action; do not replace data with a build-time
copy or require npm run sync-data. Earlier HTML revisions stay available through restore.

Use the authorized report APIs; no new data/auth server is needed. Only create a
standalone React application when the person explicitly asks for that separate
application. Do not claim a localhost app, /src/main.tsx or unregistered dist
folder is a hosted dashboard. Validate and inspect the exact hosted revision,
publish when requested, confirm dashboard action=list and return dashboard action=link.
A copied link grants no access. If preview or publication is refused, report the
actual refusal and continue resolving it; do not replace the task with directions
for the person to start a server.

## MCP operations

Call `get_api_spec` for current schemas, then `call_tool` with a discovered name
and matching arguments. Use `dashboard` with an `action` for the operations below;
app Builder uses its native dashboard tools. Keep the discovered `workspace_id`,
workflow_id or other project selector exactly as returned; do not infer a new root.

1. `dashboard action=list` discovers accessible documents and authenticated URLs.
   Omit the project selector for cross-project discovery; paginate with limit/offset.
   `published_only: true` matches the Dashboards page. Removing access removes
   discovery and blocks the link.
2. `dashboard action=get` reads the source and revision. An HTML page may use its
   returned document_path and stays intact when adopted into a managed bundle.
3. `dashboard action=create` takes dashboard_id (lowercase slug), title and files.
   Live dashboards supply index.tsx and optional relative source/assets; a quick shareable page may supply index.html. JSON dashboards are not supported.
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
for HTML pages; TSX dashboards use the shared components instead.

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

## Buttons that send a message to chat

A dashboard button or link **can send a predefined request directly to the
project's chat**. In managed TSX use `report.sendChatMessage(message, options?)`
from the supplied `report` prop; HTML pages use `window.report.sendChatMessage`.
`AskButton`, `ChatPanel` and `report.chat.open()` are separate conversation
widgets: opening them does not submit anything. Their Run-only visibility and
`report.chat.available` do not decide whether a direct chat action can send.
Do not tell Builders that dashboard actions are unavailable merely because
those widgets are hidden beside their existing chat.

Send only from an explicit click or form submission, never from render,
loadData, effects, page load, refresh or polling. The click is the person's
request; ordinary chat actions need no extra approval record or confirmation.
For an approval workflow the person actually requested, use its existing
decision tools and save the decision before requesting its execution.
Include the selected item's identity and the requested action in the message.

```tsx
import { useState } from 'react';
import { Button } from '@agentworks/dashboard';
export function ReviewAction({ report, item }) {
  const [notice, setNotice] = useState('');
  async function review() {
    try {
      await report.sendChatMessage(`Review item ${item.id}: ${item.title}`);
      setNotice('Sent to chat. Follow progress there.');
    } catch (error) {
      setNotice(error instanceof Error ? error.message : 'Could not send to chat.');
    }
  }
  return <>
    <Button label="Review this item" onPress={() => { void review(); }}/>
    <p role="status">{notice}</p>
  </>;
}
```

The live website returns a `status: 'queued'` receipt; accepted or queued does
not mean the work completed. An embedded Crew dashboard uses the chat beside
it. Other website hosts use their supplied project-chat handler; the dashboard
does not choose another project, user or session, or gain new permissions.
Where the host supports it, optional `{requestId}` deduplicates the same
item/version/action within that view; it is not a durable execution guarantee.

Preview and static exports cannot send messages. In the external MCP App,
`sendChatMessage` opens Ask with the message drafted and tells the person to
press Send; do not report that draft as sent. Surface the host's error or next
step rather than silently falling back or claiming completion.

## HTML pages: optional native chat widgets

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
through the native composer. There is no `report.chat.send`/`ask(text)` method.
This restriction is only for the widget namespace: the separate
`report.sendChatMessage` action above sends directly in the live website.
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
another origin. Dashboard widgets are for shared Run users only: `chat.available` is false for Builders and preview, even though Builders retain their ordinary project chat and contextual questions. Use the supplied AskButton/ChatPanel or marked HTML controls, never an unconditional custom Ask button. Check
availability before calling widget controls. These widgets require the managed dashboard
host; loose HTML documents do not acquire native chat through markup alone.

## HTML pages: function buttons

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

## HTML page APIs and the shared script contract

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
`dashboard action=export_motion`) with the published `dashboard_id`, selected theme
and viewport. It captures the existing page and freezes its data. A separate
explainer remains explicit through `composition_path` under `code/motion/`
and `snapshot.json`. Export needs current authoring access and server
rendering tools; it is never triggered by dashboard load/refresh. The returned
video is a snapshot, played via `window.report.mediaUrl(path)`. Design, layout
and whether to include motion or export controls belong to the user.

## External static publishing

Use dashboard action=export_static for Surge, Vercel, Netlify, Cloudflare Pages, GitHub Pages or any static host. It exports the same saved dashboard, shared platform runtimes and real authorized data calls. Deploy every returned file; do not copy managed index.html alone or reverse-engineer SQL from TSX/minified JavaScript. Put data needed by all views, images and files in loadData. Local interactions use the frozen results; new backend reads require the signed-in live dashboard. The export includes an Updated-at notice and live link; native Ask/function/write authority remains in AgentWorks. Each existing Publish automation pass exports afresh and applies the saved provider/visibility/encryption policy. No extra npm app, service, raw DB, preview token or private chat is published.


### Projects created by the current person

For a Crew dashboard's personal project list, use a normal `created_by_user_id`
column and the shared database parameter `{"$current_user_id":true}`. The backend
resolves it from the signed-in website/MCP caller or the person whose function
is executing, even when the Crew owner's account supplies execution resources.
Do not ask a person for their internal ID or save the Crew owner as every creator.

```tsx
export async function loadData(report) {
  return report.query(
    'SELECT * FROM projects WHERE created_by_user_id = ? ORDER BY updated_at DESC',
    [{"$current_user_id": true}],
  );
}
```

Use the same parameter in `database action=query`, `database action=mutate` (including
batch/param_sets), the external MCP `database action=query` and `agentworks_db` scripts.
For creation, bind it into `created_by_user_id`; preserve that field on updates.
Shared tables, files, skills, functions and chats keep their existing access.
This is a default list filter, not a private workspace or row-level permission.
Explicit queries without the filter still use the existing shared access rules.
