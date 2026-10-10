# Dashboard API usage

Follow the user’s design and motion preferences. These instructions describe
platform operations and data access.

For external MCP, call `get_api_spec` to discover tool schemas, then `call_tool`
with the chosen name and arguments. App Builder tools are bound to their project;
external operations use the authorized `workspace` returned by discovery.

1. `list_dashboards` lists accessible documents, project roots and live URLs.
   Omit workspace for cross-project discovery; use limit/offset for pagination.
   Goals, <!-- product:relays -->Relays and <!-- /product -->Crews use current project access. Code is owner-only.
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
7. `get_dashboard_link` returns a live authenticated URL. For existing Goals or
   Relay documents, `get_report_link` also returns it directly. URLs contain no
   credentials and grant no access. Recipients must sign in with current project
   access. Check shareable/warning: localhost links work on that machine only.

External connections need explicit dashboards:read for discovery, source reads
and links, and dashboards:write for drafts/publication. Preview also requires
runs:execute and project edit access because live scripts can execute. Existing
connections gain no new scopes automatically. Workflow/Crew bounds, folder grants
and live connection expiry/revocation remain enforced.

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
not invoke relay.py, publish a Relay API, or run a workflow.

Dashboards may animate HTML using the user's choices; exported MP4 explainers
use the available video tools and frozen input data. A video is a snapshot.
Managed bundles currently accept the image/font/text formats above; existing
videos can remain in db/assets/ and use window.report.mediaUrl(path).
