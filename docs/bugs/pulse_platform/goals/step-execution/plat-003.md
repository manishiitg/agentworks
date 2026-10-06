[← goals / step-execution](index.md)

# PLAT-003 — granted DB access does not produce a reachable DB tool

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | goals |
| Area | step-execution |
| Summary | workflow-step capability materialization/API bridge |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-05` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P0
- **Owner:** workflow-step capability materialization/API bridge
- **Source finding:** `HARNESS-DBTOOL-NOT-EXPOSED-EXEC-2026-08-03`
- **Primary source database:** `Workflow/build-in-public/db/db.sqlite`
- **Related evidence:** Instagram `route-design-plan`; RTS collectors denied
  direct SQLite and unable to discover the sanctioned DB path; Social Media
  standalone Pulse Fixer `manual-fixer--20260803T164745Z-1785775665916620000`
  spent 27 minutes, then its approved one-row normalization was denied with
  effective `db_access=""`.
- **Problem:** steps with `db_access=read-write` cannot resolve
  `query_workflow_db`/`mutate_workflow_db` as callable tools. The same tools may
  exist behind an undocumented raw `$MCP_CUSTOM` curl route.
- **Impact:** the permission contract and actual capability disagree. Agents
  burn failed calls, abandon persistence, or publish a false claim that the DB
  capability does not exist.
- **Current implementation (expanded 2026-08-04):** every workflow execution
  step now receives the same managed read-write database capability.
  Agentic steps materialize both `query_workflow_db` and
  `mutate_workflow_db`; saved scripted code retains `$DB_PATH` compatibility.
  Persisted `db_access=read` and message-sequence item-level DB flags remain
  loadable compatibility data but no longer narrow the runtime grant. Parent,
  child, evaluation, and message-sequence execution paths therefore cannot
  disagree about whether a workflow step is a DB writer. Direct SQLite/WAL/SHM
  paths remain blocked for managed agentic sessions, and mutation still fails
  closed for sessions that are not workflow execution steps.
- **Verification (2026-08-03):** the real stdio MCP bridge → custom executor →
  workspace HTTP API → WAL-mode SQLite E2E passes for query and mutation.
  Focused capability tests pass for read-only/read-write exposure and for
  read-only/no-grant mutation denial. The three source findings should be moved
  to platform reverify rather than prompting another workflow-level repair.
- **Fixer follow-up (2026-08-03):** isolated workshop stage tools already used
  the child MCP session directly, but `execute_shell_command` inherited the
  parent workshop's MCP URL. A Fixer calling the managed DB tool through the
  shell bridge was therefore authorized as the wrong session. Workshop stage
  sessions now override the bridge URL/session in their trusted shell env. A
  Pulse Fixer also receives an explicit read-write DB capability and must pass
  a pre-provider capability check covering DB write scope, logical grant,
  child-session bridge routing, and both query/mutation tool executors. Missing
  capability fails before the expensive LLM run; mutation remains fail-closed.
- **Current workaround:** raw `$MCP_CUSTOM/query_workflow_db` and
  `$MCP_CUSTOM/mutate_workflow_db` calls when their exact API is known.
- **Verification (uniform-access change):** focused tests cover legacy
  `db_access=read`, evaluation steps, message-sequence turns, item-specific
  folder-guard overrides, tool materialization, and session shell environment
  setup. Each workflow-step route retains the DB write path and exposes both
  managed DB tools.
- **Argument-contract follow-up (2026-08-05):** a Social Media scheduled agent
  reached the correct custom tool but naturally sent
  `{"query":"SELECT …"}`. The read tool accepted only the synonymous `sql`
  field, rejected the call, and forced a describe/retry cycle before the run
  continued. `query_workflow_db` now documents and accepts `query` as a
  compatibility alias for `sql`. Both names enter the same row-bounded,
  query-only backend. Supplying different non-empty values for both fields
  fails before any database request. Regression tests cover schema exposure,
  successful alias execution, and conflict rejection.
- **Acceptance:** a producing workflow run must prove that ordinary steps,
  evaluation steps, and asynchronous/message-sequence children can query and
  mutate through the managed tools without permission drift. Non-workflow
  sessions must remain unable to manufacture a write grant.
