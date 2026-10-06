[← goals / pulse / general](index.md)

# PLAT-559: Simplify Pulse: three roles that find and fix, code due rules, one record type

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | pulse/general |
| Summary | Proposal: three roles (QA, Engineer, Product) that find and fix in their lanes, code due rules instead of a gate turn, one work-item record, short guidance, owner-only list. |

## What happened

## Fix

## Left

## Source

Owner, 2026-10-06: "is the pulse design simplified", "i think every agent should be able to find and make fixes both", "anything else you think we should do to make this whole pulse thing better".

## Proposal

[A simpler Pulse: three roles that find and fix](../../../../../design/pulse_simplified.md). Evidence: Pulse guidance 23,243 words (22,337 before PLAT-556), 21 Pulse tools, 33 `pulse_*` tables; Architecture's proposals never reached a workflow (PLAT-305).

- Three roles, each finds and fixes in its lane: QA (failures), Engineer (structure and quality, absorbing Architecture and Plan Drift's judgment), Product (goals; today's Strategic/Goal Work). Drift's mechanical checks become automatic code checks.
- Due rules in code instead of an LLM gate turn.
- A safety net instead of approval: changelog, no-loss check, a verification run, automatic restore. Owner-only: business rules and limits, goals, spending, external actions, deleting data.
- One work-item record replacing findings, issues, decisions, fix attempts/runs/verifications, interventions and impact records.
- One short guide per role, under a word budget.
- Also: a test mode for steps with external effects, a weekly owner digest, measuring Pulse itself, platform problems filed once instead of patched per workflow, learning from reverts.

## Left

Owner decisions (listed in the design note), then the migration steps in the note. Nothing built.
