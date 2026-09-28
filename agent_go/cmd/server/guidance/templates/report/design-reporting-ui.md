Design the workflow's Dashboard UI from the ground up. First load and apply
`read_skill(skills=[{"name":"builder-reference","path":"references/reporting-policy.md"}])`,
then load and apply
`read_skill(skills=[{"name":"builder-reference","path":"references/html-output.md"}])`.
Then load `read_skill(skills=[{"name":"ui-ux-pro-max"}])` and use its design
intelligence where it helps the requested outcome. It advises visual and
interaction decisions; it does not select a framework or override the Dashboard
runtime contract.
A workflow Dashboard consists of one or more complete HTML
documents under `db/reports/`; `index.html` is the default and the shared top
toolbar exposes additional documents. Use optional `views.json` only for view
titles, ordering, and default selection—not as a widget/layout plan. Each
document owns its internal sections. Optional built-in metric widgets fit
inside the workflow-owned HTML.{{if .Focus}}

Focus on: {{.Focus}}.{{end}}

Preserve the owning workflow or Crew project's saved identity where it helps
the user stay oriented. Reuse its configured icon and display name in useful
places such as the Dashboard title/header, an empty state, or compact
navigation. If no icon is configured, use the established name-initial fallback
rather than inventing an emoji. Do not stamp the identity onto every card, and
keep long names responsive so they do not crowd the tablet or mobile layout.
When revising an existing Dashboard, do not silently remove useful identity
that is already shown.

For goal tracking or costs, use the shared Dashboard helpers before writing
custom queries or charts:
- `window.report.getGoalMetrics()` / `renderGoalProgress('#goals')`
- `window.report.getCosts({ days: 30 })` / `renderCosts('#costs', { days: 30 })`

For data tables and the activity section, prefer the optional composition
widgets over hand-rolled markup (a fully custom section remains valid):
- `window.report.renderTable('#leads', { query: 'SELECT …', searchable: true, sortable: true })`
- `window.report.renderActivity('#activity')`

For data that must be current from an outside system (Notion, a CRM, an API
behind the workflow's MCP servers or secrets), and is not already stored by a
run in `db/`, write a small read-only script at `code/reports/<name>.py` and
call `await window.report.run('code/reports/<name>.py', args)` with a loading
state, a visible error, and a Refresh button passing `{ refresh: true }`. The
script prints one JSON value and caches for itself in `$REPORT_CACHE_DIR`;
follow "Live data from a script" in `reporting-policy.md` (contract, caching,
untrusted args). Prefer `query` over `run` whenever the data is in `db/`.

When displaying outcome goals, preserve the Primary goals and Secondary goals
grouping in soul/soul.md. These priorities are independent of primary/supporting
metric roles; do not label supporting measurements as secondary goals or invent
priorities for legacy ungrouped outcomes.

The renderers provide responsive styling, loading/empty states and expandable
history without custom design work. Add only the sections useful to the reader;
use the data functions when a custom layout is requested. Call them inside
`window.report.ready(async () => { ... })` and await their promises. The loaded
`reporting-policy.md` reference contains complete examples and data semantics.
Use configured goals/observations for outcome progress and the ledger for
costs. Do not invent targets or add a collector/table just to populate a
widget. Keep missing results explicit. Cost total/activity are all-time;
model/daily breakdowns and window_total_usd cover the selected UTC window.

Every Dashboard must include one section, as its own top-level tab — not a
subsection scrolled past within another tab, and not merely an anchored
region on a single scrolling page — that answers "what did this workflow
actually do," in plain, non-technical language: recent runs and the
actions taken in each, in the order a non-technical reader would want
them, with no raw JSON, internal IDs, or state codes. Name it for
the workflow's real run cadence: `Daily Action` (or `Today's Actions`) for a
workflow that genuinely runs daily, `Recent Activity` or `Latest Run` for
one that runs hourly, weekly, or on demand. Even a Dashboard with no other
distinct views needs this one top-level tab; the rest of the Dashboard's
content becomes a second tab rather than the whole page staying tab-less.

**Build it from the run summaries you already send, by default.**
`notify_user(notification_kind="run_summary")` already writes a structured
row (title, status, message, fields, timestamp) into
`org_dashboard_notifications` in the same `db/db.sqlite`, for every run.
The default implementation is one call —
`window.report.renderActivity('#activity')` — which renders those summaries
route-grouped and markdown-rendered with the execution-log fallback built
in, and shows a "no runs recorded yet" setup message when the table has no
rows rather than treating it as an error. It needs no new step, table, or
column. Only design something custom — hand-rolled queries against
`notification_kind = 'run_summary'` ordered by `created_at desc`, a bespoke
table, richer per-run detail — when the parent has explicitly asked for a
different or more detailed activity view than the run summaries give
them; never add a step or table whose only purpose is feeding this tab.
Hand-rolled activity must pass agent-written markdown through
`window.report.renderMarkdown` so headings, bullets and inline code read
properly instead of showing raw `###` and backticks.

For route-specific activity, use the existing row's `route_summaries_json`
array. Each entry has `routing_step_id`, `route_id`, `label`, `title`, `status`,
`message`, and optional `fields`/`sections`. Group/filter by the ID pair and
display the readable label. Major routing choices are sub-workflows; branch
choices remain internal. Show shared work separately, and apply the same
grouping if showing Pulse summaries. Inspect the actual schema before querying
this additive column; absent data falls back to the original message and any
explicit legacy Route field. Missing route scope is unknown, not healthy, and
historical labels must not be guessed into canonical IDs. Preserve timestamps
so a quiet route's old update cannot appear to be today's work.

For Dashboard actions that should hand work to the agent, use
`read_skill(skills=[{"name":"builder-reference","path":"references/human-in-the-loop.md"}])`
to choose the human interaction pattern, then
`window.report.sendChatMessage(message, { requestId })` from the button handler,
following `reporting-policy.md`. The app sends directly to an existing workflow
chat, creating one only if none exists. Save any existing Dashboard-owned approval
first; include the exact item/version and intended route, and distinguish
approval saved, request queued, and evidence of actual completion. Never
send during rendering or polling.

**Design tablet-first.** AgentWorks opens a new workflow Dashboard in the Tablet
preview by default, normally as roughly half of the application canvas. Treat a
768px-wide Dashboard pane as the primary composition—not a shrunken laptop page.
Lead with one or two columns, let dense secondary content progressively expand
on wider laptop panes, and stack cleanly at mobile width. Avoid four-card KPI
rows, fixed-width sidebars, hover-only interactions, and controls that depend on
a mouse. Tabs and action controls must wrap or scroll safely and provide at
least a 44px touch target. Tables must reflow to a readable card/list treatment
or use an explicitly labeled horizontal scroller without making the page itself
overflow. Use responsive padding/type so 480px remains readable without making
the tablet view sparse.

1. Decide the reader's questions and the durable DB/asset evidence that answers
   them. Design one coherent Dashboard experience; use internal views only for
   genuinely distinct questions.
2. Inspect the real DB schema and sample rows before authoring. Do not invent
   values or make a workflow run regenerate a Dashboard.
3. Write the complete experience as `db/reports/index.html`. Include a
   meaningful `<title>` and accessible internal navigation when needed. Use
   `window.report` data helpers or `query` for live data (`run` for outside
   systems, see above), inline CSS/JS, responsive layout, clear
   empty/error states, version-pinned HTTPS CDN dependencies only when useful,
   no fixed body height, and no nested
   scrolling. Theme off the app, not the OS: style dark mode under
   `:root.dark` / `[data-theme="dark"]` (or use the injected
   `hsl(var(--background))`-style tokens) — `prefers-color-scheme` alone
   ignores the in-app light/dark toggle.
   Choose the CSS, component, and charting stack that best fits the Dashboard;
   plain CSS, Tailwind, Bootstrap, daisyUI, Chart.js, other browser libraries,
   SVG, and canvas are all valid. Every CDN-backed feature needs a readable
   fallback, and `preview_report` must verify the chosen stack actually loaded
   in both themes and at all required widths.
4. **Markdown belongs in the Dashboard as rendered prose, never as raw text.**
   A markdown file the workflow keeps under `db/` (a weekly summary, a
   strategy note, a generated brief) drops in with one call, themed to
   match the page; a markdown string from a query row goes through
   `renderMarkdown`:

   ```js
   window.report.ready(async function () {
     document.getElementById('brief').innerHTML =
       await window.report.getHtml('db/notes/weekly-brief.md');
     const rows = await window.report.query("SELECT message FROM org_dashboard_notifications WHERE notification_kind='run_summary' ORDER BY created_at DESC LIMIT 5");
     document.getElementById('runs').innerHTML =
       rows.map(r => window.report.renderMarkdown(r.message)).join('');
   });
   ```

   Links and images inside that markdown that point at workspace files
   (`db/assets/chart.png`, `db/reports/proof.pdf`, or paths relative to the
   .md file) work: images load, links open the in-Dashboard file preview.
   Use `getText` only when you genuinely want the raw source.
   For test recordings or audio saved under `db/assets/`, use
   `await window.report.mediaUrl(path)` with a native `<video controls>` or
   `<audio controls>` element. This returns an authenticated, file-scoped URL
   with byte-range streaming; `fileUrl` still downloads a blob. Request a fresh
   URL when the user opens a recording, not for every row during initial render.
   URLs expire after at most 30 minutes (or the current preview/session expiry);
   do not persist them in the database. Persist the workspace-relative file path
   with the run/test result instead. On expiry, offer a retry that obtains a new
   URL, preserving the playback position where possible; do not retry indefinitely.

   ```js
   async function watchRecording(recordingPath) {
     const url = await window.report.mediaUrl(recordingPath);
     if (!url) throw new Error('Recording is unavailable');
     const player = document.getElementById('test-recording');
     player.preload = 'metadata';
     player.src = url;
     player.load(); // native controls let the user start playback
   }
   ```

   Use `<video id="test-recording" controls playsinline></video>`, handle errors
   visibly, and only record tests when requested by the workflow/user. Ensure the
   recording is finalized before storing its durable path. This API serves
   existing media; it does not automatically record tests or attach their results.
5. Call `validate_report_html` for every document after editing; repair every error. It now
   also runs every literal `window.report.query` SQL against the live
   `db/db.sqlite`, checks that every referenced `db/` file exists, rejects
   broken local stylesheet/script references, and warns when dark mode keys only off
   the OS scheme. A query built from variables is reported as unchecked —
   prefer literal SQL so the validator can see it.
6. Call `preview_report` for every changed document after validation passes. It renders the Dashboard in a
   real headless browser through the same runtime the Dashboard tab uses and
   reports whether it settled, its script/fetch errors, its tab labels, any
   `Loading…` text never replaced, and screenshots at tablet (primary), mobile,
   and desktop widths in both themes — open
   them with `read_image` and judge layout, contrast, and empty states
   directly, rather than asking the user to check.
7. Tablet/mobile/desktop verification is required for every authored or revised
   Dashboard. When deeper visual review is requested beyond `preview_report`, also
   inspect those device modes in the live Dashboard tab.

Before writing a large Dashboard, briefly state the sections/views you will create
and what each answers, including the required activity/actions section above.
The Dashboard should lead with goal progress and key risks, then evidence and
detail—not raw JSON or a generic data dump.

For typed route rows, `summary_text` contains only the shared lead; `message`
remains the complete rendered digest for older Dashboards. Render the lead plus
route entries once, or the complete message as a fallback, never both.
