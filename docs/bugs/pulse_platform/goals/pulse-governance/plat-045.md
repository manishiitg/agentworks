[← goals / pulse-governance](index.md)

# PLAT-045 — resolved findings leave their linked decision open

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | Pulse finding and human-input lifecycle |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P1
- **Owner:** Pulse finding and human-input lifecycle
- **Source workflow:** Upwork

## Problem

A finding could ask a durable human question and later reach a terminal review
outcome while the answered `report_human_inputs` row remained unconsumed. The UI
then continued to present a decision that no longer controlled any work.

## Fix

Finding events retain their linked `human_input_id`. When the finding reaches
`fixed_verified`, `verified_no_change`, `changed_unverified`, or `rejected`, the
same transaction consumes the linked decision if and only if it is already
answered. Pending questions and unrelated decisions are untouched.

## Verification

`TestAwaitingUserRequiresARealPendingQuestion` now covers the full transition:
pending question → awaiting-user finding → answered question → terminal finding
outcome → consumed decision.
