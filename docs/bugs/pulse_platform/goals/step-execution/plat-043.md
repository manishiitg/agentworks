[← goals / step-execution](index.md)

# PLAT-043 — guarded DB reads reject SQLite integrity PRAGMAs

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | step-execution |
| Summary | `query_workflow_db` SQL read policy |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P2
- **Owner:** `query_workflow_db` SQL read policy
- **Source finding:** `HARNESS-QUERY-WORKFLOW-DB-PRAGMA-ALLOWLIST`
- **Source workflow:** Confida QA
- **Problem:** the guarded query tool allowed schema inspection but rejected
  `integrity_check`, `quick_check`, and `foreign_key_check`, leaving no legal
  route for the stores-health reviewer to verify the workflow database.
- **Implementation:** the policy now parses an explicit PRAGMA name and bounded
  argument shape. It permits `integrity_check[(N)]`, `quick_check[(N)]`, and
  `foreign_key_check[(table)]`, with numeric error limits capped at 1,000,
  while continuing to reject assignments, unknown
  pragmas, malformed arguments, extension loading, and stacked statements.
- **Verification:** focused allow/reject tests cover all supported forms and
  mutating or stacked counterexamples.
- **Runtime acceptance:** a scheduled stores-health module runs all three
  checks through `query_workflow_db`, records `ok`/zero violations, and performs
  no direct SQLite shell access.
