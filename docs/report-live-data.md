# Live report data from scripts (`window.report.run`)

A workflow Dashboard normally reads what runs stored in `db/db.sqlite`
(`window.report.query`). When it needs the **current** state of an outside
system (Notion, a CRM, an API), it can run one of the workflow's own scripts
on the server each time the Dashboard loads or someone presses Refresh:

```js
window.report.ready(async () => {
  const out = await window.report.run('code/reports/open_deals.py', { days: 7 });
  render(out);
});
```

The script prints one JSON value; `run` resolves with it.

It works on workflow Dashboards and on Crew project Dashboards (the Work
product's `db/reports/`). Crews learn it from the `work-dashboard` skill,
workflows from `reporting-policy` / `design-reporting-ui`.

## Who can run it, and as whom

- Crew Dashboards: the crew owner and anyone with the Crew product (Crew Run
  mode) can trigger its scripts. They run with the crew's selected MCP servers
  and secrets. Crews have no variables.

- Anyone who can open the workflow (owner, editor or read-only user) can
  trigger the scripts its Dashboard calls.
- The script always runs **as the workflow**, never as the viewer. It uses the
  workflow's own connections, the same as a scheduled run:
  - its selected MCP servers and tools;
  - its selected secrets (`$SECRET_*`);
  - its variables (`$VAR_*`, only when there is a single group; otherwise the
    defaults).
- Published static copies cannot run scripts. The publish guidance bakes the
  output in at publish time, or hides that panel.

## The sandbox

| | |
| --- | --- |
| Scripts | `.py` (python3) or `.js`/`.mjs` (node) under `code/`; convention `code/reports/<name>.py` |
| Args | JSON in `$REPORT_ARGS` (`{}` when none). Treat them as untrusted input from any viewer. |
| Output | Exactly one JSON value on stdout (at most 2 MB). Logs go to stderr. |
| Time | 60 seconds per call. At most 4 report scripts run at once on a server. |
| MCP | `POST $MCP_MCP/<server>/<tool>` with the `$MCP_AUTH` header. Only the workflow's selected servers and tools can be reached. |
| Database | `$DB_PATH` is a read-only snapshot taken for this call. It is unset if the workflow has no DB yet. |
| Files | Reads anywhere in the workflow. Writes only to `$REPORT_CACHE_DIR` (`<workflow>/.report-cache/`), which every viewer shares. |

If the script fails (non-zero exit, timeout, non-JSON output), the Dashboard
receives the error plus the tail of stderr. Known secret values are redacted
from that text.

## No platform cache: scripts cache for themselves

Every call runs the script again. The platform keeps no cache, because only the
script knows how fresh its data needs to be. Best practices (also in the
builder's `reporting-policy` guidance):

- Keep one cache file per query in `$REPORT_CACHE_DIR`, named from the script
  and its normalized args.
- Return the cached copy while it is younger than a TTL that suits the source.
  Include `fetched_at` so the page can show "as of".
- Write the cache atomically (temp file, then rename), because two viewers can
  refresh at the same time.
- Let a `{"refresh": true}` arg skip the cache, for the Refresh button.
- When the upstream call fails but a cache exists, return the cache with
  `stale: true` rather than failing.
- Aggregate in the script, keep every upstream call bounded with a timeout,
  and never create, update or send anything upstream from a report view.

## Validation

`validate_report_html()` rejects:
- a `window.report.run('…')` path that is not a script under `code/`;
- a script that does not exist.

`preview_report()` runs the scripts for real.

## Implementation

- Endpoint: `POST /api/workflow/report-preview/run`, in
  `agent_go/cmd/server/report_run.go`. It lives under the report-preview
  prefix, so the headless preview's scoped token can reach it.
- Execution goes through the workspace service's `/api/execute`, with the same
  isolator and Folder Guard as scripted steps.
- MCP scope: each run gets a `report-run-<uuid>` bridge session.
  `resolveReportRunMCPServer` authorizes each call against the workflow
  manifest's current selection while the script runs. The session is dropped
  when the script ends.
- Tests: `report_run_test.go`, and `report_run_e2e_real_test.go` (a real
  workspace service; skipped unless `REPORT_RUN_E2E_DOCS` is set).
