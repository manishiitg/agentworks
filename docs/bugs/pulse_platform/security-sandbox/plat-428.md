[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-428 — Scripted steps use the managed DB helper; contract 1.0.45

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | security-sandbox |
| Related | PLAT-420 (bulk DB tools), PLAT-419 (what a step may do), PLAT-423 (named Python tools) |

## Why

Scripted steps opened `db/db.sqlite` themselves (`sqlite3`, `$DB_PATH`) while agents
use the managed tools: two access paths, the script one unvalidated and unbounded.
The owner decided scripts use the same managed layer; DDL is known when a script is
written (schema changes are Builder migrations), and a script that no longer
matches the schema fails and autofix repairs it. `$DB_PATH` stays for backward
compatibility only.

## Done

- **Helper** `agentworks_db` (`workspace/security/runtime/agentworks_db.py`, stdlib
  only, staged next to `agentworks_output.py` in the slot helper dir and the
  non-slot scratch dir): `query`, `iter_query` (auto-paged), `query_one`, `scalar`,
  `describe`, `execute`, `insert`, `execute_many` (2000 rows per call, `atomic=True`
  up to 5000), `transaction` (up to 200). It calls `query_workflow_db` /
  `mutate_workflow_db` through the step's own bridge session (`MCP_CUSTOM`,
  `MCP_AUTH`); every failure is a `DBError`. Tested against a fake bridge
  (`workspace/security/db_runtime_test.go`).
- **Authoring guidance**: code-authoring.md "Database access (strict)", the scripted
  step prompt, mcp-bridge, stores, step-config, design-plan: new and repaired
  scripts use the helper; DDL is a `db/migrations/` file; `$DB_PATH` is marked
  backward compatibility.
- **Contract 1.0.45** (`upgrade-managed-db-scripts`): the instruction maps every
  sqlite call to the helper, moves DDL to migrations, tests, and stamps. A
  deterministic scan (`pkg/scriptdb`, tool `scan_workflow_script_db_usage`) lists
  scripts that import `sqlite3` or use `DB_PATH`/`db.sqlite`, and schema statements;
  `test_*.py`, `conftest.py` and `code/reports/` (read-only snapshot scripts) are
  not scanned. The stamp executor refuses 1.0.45 while the scan is not clean.
  1.0.44 workflows are no longer execution-compatible until migrated.
- `cli-step-contract` checks the database: the scripted step does a bulk write,
  a transaction, a paged read and a refused `CREATE TABLE` through the helper; the
  agent step inserts through `mutate_workflow_db`; both are verified from the file.
  Live on a Mac isolated server 2026-10-04: PASS for claude-code, codex-cli, muse-cli.

## Consequence to plan for

Every workflow is blocked from running or scheduling until it is migrated. On the
owner's workspace the scan finds 32 of 48 scripts (7 workflows) to convert, 6 with
schema statements. Migrations are started by the owner from each workflow's chat.

## Left

- Named Python tools (PLAT-423, `code/tools/*`) are scanned too; whether their
  runtime provides the bridge environment the helper needs is not verified.
- `$DB_PATH` is still set; removing it is a later contract once no workflow uses it.
- Linux/slot run of the helper (Landlock helper dir) and a deploy: not done.
