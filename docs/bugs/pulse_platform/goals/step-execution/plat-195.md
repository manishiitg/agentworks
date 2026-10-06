[← goals / step-execution](index.md)

# PLAT-195 — `query_workflow_db`'s integrity-pragma block was already fixed 4 days after being reported; the harness finding was just never closed

| Field | Value |
|---|---|
| State | closed |
| Priority | P3 |
| Product | goals |
| Area | step-execution |
| Summary | P3 — no live defect. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — fixed before this ticket was even filed; this is a correction/closure record, not a new fix |
| Last synchronized | `2026-08-28` |

- **Priority:** P3 — no live defect. A stale finding sitting in the platform
  harness table describing a gap that was already closed.
- **Owner:** N/A — no code change made or needed by this ticket.
- **Related:** `harness:query_workflow_db:readonly-pragma-allowlist-excludes-integrity-checks`
  (medium), the confida-login finding this closes.

## The finding, and why it was real when filed

*"The guarded DB read tool blocks the three read-only integrity pragmas, and
the sandbox blocks direct file access to the same database, so no reviewer
can verify SQLite integrity even though the module contract requires it."*

Filed **2026-08-03T06:39:49Z**. At that time, `workspace/handlers/query.go`'s
read-pragma allowlist (`safePragmaPattern`, a single regex) only permitted:
`table_info`, `table_xinfo`, `index_list`, `index_info`, `index_xinfo`,
`foreign_key_list`, `database_list`, `journal_mode`, `user_version`,
`schema_version` — confirmed directly via `git log -L` on the function.
`integrity_check`, `quick_check`, and `foreign_key_check` were genuinely not
in that list. The finding was accurate.

## Already fixed — confirmed via git history, not assumed

Commit `2ebb0397` ("2026-08-07 fix scheduler and pulse audit contracts") —
**the same commit that fixed PLAT-191's scheduler misfire-recovery gap** —
also replaced `safePragmaPattern` with `isSafeReadPragma`, a structured
per-pragma-name switch that explicitly allows `integrity_check`,
`quick_check` (each with an optional bounded numeric argument), and
`foreign_key_check` (with an optional table-name argument). Verified this
is genuinely wired into the live `/api/query` HTTP handler
(`validateReadSQL` → `isSafeReadPragma`, `query.go:577`), which is exactly
what `query_workflow_db`'s `QueryAuthorizedWorkflowDB` client method calls
(`POST /api/query`). Also directly verified SQLite itself does not block
these pragmas under `query_only=true` (the read connection's own mode) —
ran all three against a real scratch database, all returned cleanly.

The agent-facing tool description in
`agent_go/cmd/server/virtual-tools/workflow_db_tools.go` already documents
this correctly today: *"Supported integrity checks include PRAGMA
integrity_check, quick_check[(N)], and foreign_key_check[(table)]."*

## Why this stayed open for 25 days

The harness finding was filed against the pre-fix state and never
re-verified after the fix landed 4 days later — the same failure mode
PLAT-191 found for the scheduler findings: nothing in this platform
currently re-checks an open harness finding against the *current* code
before a reviewer re-encounters (or simply doesn't re-encounter) it. This is
the second time this exact pattern has shown up in one review pass of a
single workflow's harness table; worth treating as a signal, not a
coincidence, if a third instance turns up.

## Verification

- `git log -L` on `isSafeReadPragma` confirms the fix's exact date and
  content, cross-referenced directly against the finding's `first_seen_at`.
- Confirmed live wiring: `/api/query` → `validateReadSQL` →
  `isSafeReadPragma`, the same path `query_workflow_db` uses.
- Confirmed directly with real `sqlite3`: `PRAGMA integrity_check`,
  `quick_check`, and `foreign_key_check` all succeed under
  `PRAGMA query_only=true`.
- No code changed by this ticket — nothing to build or test.
