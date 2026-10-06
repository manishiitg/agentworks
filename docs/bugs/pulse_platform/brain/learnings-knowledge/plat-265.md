[← brain / learnings-knowledge](index.md)

# PLAT-265 — Retire the workflow-wide knowledgebase lock

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | brain |
| Area | learnings-knowledge |
| Summary | product simplification, severity medium. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-31` |

- **Priority:** product simplification, severity medium.
- **Related:** [PLAT-257](../../goals/learnings/plat-257.md), [PLAT-260](../../goals/pulse/lifecycle/plat-260.md)),
  [PLAT-263](../../goals/learnings/plat-263.md).
- **Renumbering note:** this work was initially recorded as PLAT-262 in a
  concurrent local change. Upstream already owned PLAT-262 for read-only
  workflow users, so the KB-lock work moved intact to PLAT-265.

## Problem

The workflow-wide `lock_knowledgebase` flag overlapped with per-step
`knowledgebase_access` and `knowledgebase_contribution`. It described a
retired writer architecture and could disable unrelated, legitimate KB
contributors across the entire workflow.

## Implemented contract

- KB write authority is step-based only.
- A step that should consume without contributing uses
  `knowledgebase_access="read"`.
- The global lock is absent from runtime/session state, Builder configuration,
  status output, and current guidance.
- Legacy manifest values are consumed and removed during normal workflow
  configuration normalization; they no longer influence execution.

This deliberately avoids a second KB lifecycle. Pulse and plan-drift review
may judge each contributor's access and contribution contract independently.
