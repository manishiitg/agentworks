[← platform / security-sandbox](index.md)

# PLAT-304 — Authorized run-retention updates denied by the workspace guard

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | sandbox |
| Area | security |
| Summary | Social Media `PUL-2E9A7BB5`: a repair attempted |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; local regression verified; deployment pending` |
| Last synchronized | `2026-09-09` |

## Reproduction

Social Media `PUL-2E9A7BB5`: a repair attempted
`update_workflow_config(run_retention_count=10)` twice. The typed tool was
available, but its write to root `workflow.json` failed because the agent's raw
write grant covered selected workflow folders, not the root manifest. Retention
of only three backup iterations had already removed useful review evidence.

## Fix

The typed retention handler uses a Go-owned exact-file write capability for the
current workflow's `workflow.json`. It still validates the integer range, reads
and preserves other manifest fields, and retains existing tool/phase authority.
No raw workspace or shell grant is expanded. The capability is local to this
write call and cannot name an arbitrary user-supplied destination.

A regression test exercises the real workspace HTTP guard: raw manifest writes
fail, the managed write persists, and subsequent raw writes to the manifest,
soul and another workflow remain denied. No production retention values are
changed automatically, and already-deleted history cannot be recovered by this
fix. Partial-run evidence immutability remains PLAT-047/089.
