---
name: "dashboard-authoring"
description: "Dashboard MCP operations, revisioned bundles, live HTML data APIs, Python/JS scripts, validation, preview and authenticated team links."
external: true
---
# Dashboard API usage

Follow the user’s design and motion preferences. These instructions describe
platform operations and data access.

For external MCP, call `get_api_spec` to discover tool schemas, then `call_tool`
with the chosen name and arguments. App Builder tools are bound to their project;
external operations use the authorized `workspace` returned by discovery.

1. `list_dashboards` lists accessible documents, project roots and live URLs.
   Omit workspace for cross-project discovery; use limit/offset for pagination.
   Goals, <!-- product:relays -->Relays and <!-- /product -->Crews use current project access.<!-- product:code --> Code is owner-only.<!-- /product -->
2. `get_dashboard` returns source and revision. Existing HTML documents can be
   read using their returned document_path; adopt them by creating a new managed
   bundle from that source. Existing files are preserved.
3. `create_dashboard` takes dashboard_id (lowercase slug), title and files.
   index.html is required. Text assets use file contents. PNG/JPEG/GIF/WebP images
   and WOFF/WOFF2 fonts use matching base64 data URLs. Optional Python/JS/MJS
   scripts go under scripts/. Maximum: 100 files, 8 MiB per bundle.
4. `update_dashboard` supplies changed files/title and optional remove paths,
   plus expected_revision. Omitted files remain. A stale revision is refused.
5. `validate_dashboard` checks HTML APIs, SQL schema and referenced files.
   `preview_dashboard` renders an exact revision with the actual report runtime
   and returns state, errors and screenshot paths/authenticated URLs. Rendering
   requires a configured browser. Fix observed errors before publishing.
6. `publish_dashboard` validates the expected revision, then switches the live
   pointer atomically. Drafts remain hidden from readers, and shared URLs keep
   displaying the published bundle. `restore_dashboard` republishes a previously
   published revision with the current draft revision as expected_revision.
7. `get_dashboard_link` returns a live authenticated URL. For existing Goals<!-- product:relays --> or
   Relay<!-- /product --> documents, `get_report_link` also returns it directly. URLs contain no
   credentials and grant no access. Recipients must sign in with current project
   access. Check shareable/warning: localhost links work on that machine only.

External connections need `dashboards:read` for discovery, source reads and links.
Drafts and publication use existing product authoring consent: `builder:chat` for
workflows,<!-- product:relays --> `relays:write` or Builder consent for Relays,<!-- /product --> `crews:write` for Crews,
<!-- product:code -->and `code:run` for owned Code projects.<!-- /product --> Workflow<!-- product:relays -->/Relay<!-- /product --> authoring retains its
companion read/run scopes and Builder enablement. Current account and project
owner/editor roles, product access, connection bounds and folder grants apply.
Preview additionally requires `runs:execute` and project edit access because live
scripts can execute. Read-only connections stay read-only. Retired dashboard-write
consent adds no authority; reconnect for existing product authoring consent.
Connections gain no new scopes automatically; expiry and revocation remain enforced.

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

## Live HTML and scripts

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
not run a workflow<!-- product:relays -->, invoke relay.py or publish a Relay API<!-- /product -->.

Dashboards may animate HTML using the user's choices; exported MP4 explainers
use the available video tools and frozen input data. A video is a snapshot.
Managed bundles currently accept the image/font/text formats above; existing
videos can remain in db/assets/ and use window.report.mediaUrl(path).
