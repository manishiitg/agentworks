[← platform / frontend-chat](index.md)

# PLAT-256 — Interactive HTML reports: `window.report.updateField`/`updateFields`

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | frontend-chat |
| Summary | platform feature, not a bug fix. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-30` |

- **Type:** platform feature, not a bug fix. Filed here at the user's explicit
  request, for the same visibility/credit the other platform work in this
  register gets — no Pulse finding is linked.
- **Origin:** the user wanted an in-app report (e.g. "show generated emails,
  let me approve them") to write back to its own workflow data, not just
  read it. `report_human_inputs`/`create_human_input_request` (Pulse's
  existing decision mechanism) was ruled out for this: it creates one full
  "Needs your decision" card per call, which doesn't scale to a bulk case
  like approving 100 emails a day, and coupling a report's own approve
  button to Pulse's reviewer semantics was the wrong dependency direction.

## What was built

Reports were read-only before this: the sandboxed iframe bridge
(`window.report.*`) only exposed `query`/`get`/`getText`/`getHtml`/
`renderMarkdown`/`fileUrl`/`openFile` — no write path at all, and the one
existing SQL endpoint reports could reach (`/api/query`) is deliberately
opened `query_only` at the SQLite connection level.

Added two new bridge functions, backed by one new endpoint:

- `await window.report.updateField(table, row_id, column, value)` — writes
  one cell.
- `await window.report.updateFields(table, row_id, {col1: v1, col2: v2})` —
  writes several columns on the same row in one atomic call (a form
  submit): all fields apply, or none do.

Both resolve `{ oldValue(s), newValue(s) }` once committed.

### Design: direct writes to the workflow's own tables, not a raw-SQL bridge

The report never gets to write arbitrary SQL — no `execute(sql)`-shaped
call exists. The new endpoint (`POST /api/report-field`,
`workspace/handlers/query.go`'s `UpdateReportField`) takes a structured
`{db_path, table, row_id, fields}` request and, before touching the
database, validates against the table's own **live schema** (via
`PRAGMA table_info`, same helper `GetWorkflowDBTables` already uses):

- the table exists and isn't one of a small platform-reserved set
  (`report_human_inputs`, `report_human_input_events`,
  `schema_migration_log` — these already have their own dedicated,
  audited write paths and must never be touched by a report's own edit
  action);
- the table has exactly one primary-key column (so a row can be targeted
  unambiguously — a table without a clean single-column PK is rejected as
  "not editable" rather than guessed at);
- every named column exists on that table, and none of them is the
  primary key, ends in `_id`, or is named `created_at`/`updated_at` — a
  column that identifies or timestamps the row is never a legitimate
  "approve this email" target, so it's rejected before it ever reaches
  SQL, regardless of what generated the calling report;
- every value is a plain string/number/boolean/null (object/array
  rejected), with a length cap.

Only then does one parameterized `UPDATE table SET col1=?, col2=? WHERE
pk=?` run, inside a transaction alongside a durable audit-log insert
(`report_field_update_log`: table, row id, column, old value, new value,
who, when) — so the write and its audit trail commit or roll back
together, and any mistaken or unexpected edit is traceable and
reversible-by-inspection, not silent.

This intentionally does **not** require a workflow author to pre-declare
which columns are report-writable — the report-authoring agent already
designed both the table and the report in the same breath, so requiring a
separate permission step first would be bureaucratic for no real safety
gain. The schema-derived guards above are a floor against one specific
failure mode (a write accidentally targeting the row's own bookkeeping
columns instead of the business field it meant to touch), not a
permission system.

### Why a new endpoint, not the existing `/api/mutate`

`/api/mutate` (agent-only, gated by `requireWorkspaceAPIToken()`) already
accepts arbitrary parameterized INSERT/UPDATE/DELETE SQL from an
authorized agent session — it was not safe to expose to report iframe JS,
which runs in the browser on the same auth tier as `/api/query` (a normal
user session, no elevated token). The new `/api/report-field` route sits
on that same lower, browser-reachable tier, but — unlike `/api/mutate` —
the caller never supplies SQL at all, only structured
table/row/column/value data that the handler validates itself.

## Verification

- `go build ./workspace/... ./agent_go/...` clean.
- `go test ./workspace/...` — new suite
  `workspace/handlers/report_field_update_test.go` (6 tests): single-field
  apply + audit row, multi-field atomic apply, every guarded-column case
  rejected individually AND in a mixed valid+guarded call (confirming no
  partial write), reserved-table rejection, non-primitive value rejection,
  unknown table/column/row rejection. All pass; full package suite still
  green.
- `npx tsc --noEmit` and `npx eslint` on all touched frontend files clean
  (two pre-existing, unrelated lint errors in `ReportViewer.tsx`/
  `HtmlWidgetFrame.tsx` confirmed via `git stash` to predate this change).
- `npm run build` (frontend) succeeds.

## Reverify

Confirm live: from an in-app HTML report, call
`window.report.updateField('<table>', <row_id>, '<column>', '<value>')`
against a real workflow's `db.sqlite` and confirm the cell changes, an
audit row appears in `report_field_update_log`, and a guarded-column or
reserved-table attempt is rejected with a clear error instead of
succeeding.
