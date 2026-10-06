[← goals / pulse-governance](index.md)

# PLAT-044 — normal finalizer ownership is filed as a platform defect

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | Pulse review guidance and final-command lifecycle |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P1
- **Owner:** Pulse review guidance and final-command lifecycle
- **Source workflow:** Upwork

## Problem

Reviewers treated a dashboard or published snapshot being unavailable before
its ordered finalizer stage as a missing platform capability. That created
`external_action_required` findings even though the same Pulse pass could later
complete the owning dashboard or publish command successfully.

## Fix

- Reviewer guidance now distinguishes a waiting/running finalizer stage from a
  terminal failed or blocked stage.
- `record_pulse_finding` states the same boundary at the tool schema.
- When dashboard or publish completes, the lifecycle closes only findings with
  the known finalizer-ownership reason codes. It never closes a genuine
  platform failure merely because its prose looks similar.
- Reconciliation runs on both scheduler-owned and agent-reported completion.

## Verification

`TestSuccessfulFinalCommandClosesOnlyStageOwnershipFindings` proves the
agent-reporting path closes the stage-owned record while preserving an adjacent
`missing_platform_tool` record.
