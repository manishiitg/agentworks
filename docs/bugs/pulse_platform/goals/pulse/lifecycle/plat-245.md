[← goals / pulse / lifecycle](index.md)

# PLAT-245 — Pulse contract-upgrade prompts omitted required workflow identity

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | pulse/lifecycle |
| Summary | harness issue, severity high. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; scheduler restart and LinkedIn retry required` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness issue, severity high.
- **Observed workflow:** LinkedIn contract upgrade from `1.0.31` to `1.0.32`.

## Root cause

The `1.0.32`, `1.0.33`, and `1.0.34` scheduler-generated migration prompts
showed `record_pulse_migration_reconciliation` and `get_pulse_state` calls
without `workspace_path`. Both HTTP-backed tools require that field, so the
argument validator rejected the migration before it touched Pulse data. The
safeguard correctly prevented the workflow contract stamp, but every later
schedule would retry the same malformed instruction.

## Fix

The three prompt templates now carry an explicit workspace placeholder. The
scheduler binds it to the exact `ScheduleContext.WorkspacePath` before sending
an upgrade turn, using a quoted value safe for the displayed tool-call syntax.
The workflow-contract status view applies the same binding so the operator sees
the actual instruction that will run.

## Verification

`TestPulseMigrationUpgradeTurnsBindRequiredWorkspacePath` covers all three
Pulse migration rungs and requires both calls to contain the exact workflow
path, while rejecting an unbound placeholder. The complete
`go test ./cmd/server/...` suite passes.

## Reverify

Restart the scheduler onto this build and trigger LinkedIn again. Its next
upgrade turn must reconcile `Workflow/linkedin`, read the resulting backlog,
and stamp `1.0.32`; the same preflight can then advance the remaining rungs.
