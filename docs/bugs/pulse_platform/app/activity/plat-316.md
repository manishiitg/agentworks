[← app / activity](index.md)

# PLAT-316 — Animate the Pulse heartbeat when human decisions are pending

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | app |
| Area | activity |
| Summary | The Pulse toolbar icon itself animates in amber when human decisions are pending. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and deployed to RTS; verification scope below |
| Last synchronized | 2026-09-12 |
| Implementation commits | 4d7725c22 |

## Delivered behavior and verification

The Pulse toolbar icon itself animates in amber when human decisions are pending. Its accessible label and tooltip include the pending count, making the action visible without adding a separate decision icon.

Evidence: WorkflowToolbar pendingDecisionCount/pulse-decision-heartbeat implementation and usePendingDecisionCount regression tests. Included in the RTS release. No new production decision was created solely to test the animation.

## Deployment receipt

Included in RTS app release `bb7ac6d17ec43750e74a5c92d73ef067f66c69bf`
(`bb7ac6d-20260912135437`), with provider `570ede69fb85beef251ddca9792a2e5ad0dfe95d`.
All three services were active and the public health endpoint was healthy after deployment.
Deployment health is distinct from the feature-specific acceptance scope above.
