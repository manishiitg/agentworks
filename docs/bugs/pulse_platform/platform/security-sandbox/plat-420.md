[← platform / security-sandbox](index.md)

# PLAT-420 — Bulk writes and paged reads in the existing DB tools

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | platform |
| Area | security-sandbox |
| Summary | fixed on `main`, not deployed: `mutate_workflow_db` takes `param_sets` (many rows, one transaction) and up to 200 statements; `query_workflow_db` takes `offset`/`next_offset` and up to 10,000 rows. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | security-sandbox |
| Related | PLAT-419 (what a step may do) |

## Why

`mutate_workflow_db` took at most 20 statements per call, one row each, and
`query_workflow_db` clamped results to 1,000 rows while the workspace service
itself allows 50,000. A large import or scan therefore needed hundreds of calls
and lost its single transaction. The owner asked for the existing tools to be
extended rather than a separate helper.

## Done (backward compatible: every new argument is optional)

- `mutate_workflow_db`: `param_sets` (one `sql`, many parameter lists, run in the
  caller's transaction on a prepared statement; the receipt sums the rows affected;
  a failing row aborts and names itself: `param_sets row N`); statements per call
  20 -> 200; at most 5,000 executions per call in total (a statement takes `params`
  or `param_sets`, never both). Migrations keep their own cap of 20.
- `query_workflow_db`: `max_rows` up to 10,000 (default stays 500); `offset` for
  SELECT/WITH results, and a truncated result carries `next_offset`.
- Step guidance (read and write blocks) documents both.
- Tests: handler (many rows in one transaction, rollback on a bad row, caps, params
  vs param_sets), tool (offset wrapper, schemas), and an end-to-end test that drives
  the real tool executors against the real workspace handlers over HTTP (3,000 rows,
  a rolled-back bad batch, paged read-back with no loss or duplication).

## Left

- `output_file` (big results to a JSONL file) and a Python helper for scripts are not
  built; scripted steps keep `$DB_PATH`. See the owner's decision in PLAT-419's thread:
  schema changes come from the Builder through migrations, and a script that no
  longer matches the schema fails and is autofixed.
- Per-call latency through the bridge has not been measured.

## Register notes

[PLAT-420](plat-420.md), fixed on `main`, not
deployed: `mutate_workflow_db` takes `param_sets` (many rows, one transaction) and
up to 200 statements; `query_workflow_db` takes `offset`/`next_offset` and up to
10,000 rows.
