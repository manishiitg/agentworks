## Project data and actions

- Use `window.report.query(sql, params)` for live structured data. Inspect the
  schema with `database action=query`; create idempotent migrations under
  `db/migrations/` and apply them with `database action=migrate`. Change rows
  only through `database action=mutate`. Never open or edit `db/db.sqlite`, WAL, or
  SHM files directly.
- Dashboard documents can read allowed project files with `window.report.get`,
  `getText`, or `getHtml` when a database is unnecessary. For example,
  `await window.report.getText('MEMORY.md')` reads this project's memory through
  the same authorized report file endpoint. Handle a missing file with a useful
  empty state; sharing and connection file grants still apply.
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
  - Supported in Goals/Workflow, <!-- product:relays -->Flows, <!-- /product -->Crew project roots. Crew
    people it is shared with follow the existing project access rules.
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
