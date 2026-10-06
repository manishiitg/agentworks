[← goals / pulse / general](index.md)

# PLAT-311 — Goal metric groups and linked supporting measurements

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | goals |
| Area | pulse/general |
| Summary | The current implementation accepts one or more primary metrics, with optional goal_id/goal_name grouping. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and deployed to RTS; verification scope below |
| Last synchronized | 2026-09-12 |
| Implementation commits | 4d7725c22 |

## Delivered behavior and verification

The current implementation accepts one or more primary metrics, with optional goal_id/goal_name grouping. Supporting measurements explicitly name parent primary IDs with supports when multiple primaries exist, and carry support_kind. Validation enforces valid parent references and compatible breakdown units/direction/window. Single-primary legacy supporting lists remain compatible.

Goal UI, report widgets, run/Pulse summaries and strategic/architecture guidance use grouped source-backed measurements. Missing/stale observations remain explicit. Metric setup saves definitions; it does not prove that collection works or automatically launch collectors.

Current workflow-boundary guidance groups work that must be planned, executed and evaluated together; multiple primary metrics alone do not force a split. This records the shipped code rather than the earlier discussion of a one-primary-per-workflow rule. Existing workflow boundaries and targets are not automatically changed.

Evidence: goal_metric_tools.go, step_based_workflow/goal_metrics.go and their tests; goalMetricGroups/goalMetricProgress and report metric tests shipped in the commit. Deployed to RTS; a new real-workflow metric collection acceptance run was not performed here. See [goal measurement](../../../../../pulse-goal-measurement.md).

## Deployment receipt

Included in RTS app release `bb7ac6d17ec43750e74a5c92d73ef067f66c69bf`
(`bb7ac6d-20260912135437`), with provider `570ede69fb85beef251ddca9792a2e5ad0dfe95d`.
All three services were active and the public health endpoint was healthy after deployment.
Deployment health is distinct from the feature-specific acceptance scope above.
