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

## What Technical actually finds (Upwork, 2026-10-06)

Owner: "if technical review is finding bugs, something is wrong ... ideally builder should write the perfect plan and drift should optimize; the technical reviewer should be finding zero bugs ideally."

Upwork has 354 Pulse issues (322 workflow, 32 harness) and 227 fix attempts (187 by Technical). A random sample of 50 issues, classified by reading each description (rough):

| Root cause | Share |
|---|---|
| Changes not carried through to dependent steps, evals and notes (inputs naming removed steps, evals checking old paths or lengths, KB vs soul.md rate conflict, stale guidance) | ~32% |
| Platform bugs (`query_workflow_db` two response shapes, `run_full_workflow` dropping an override, snapshot overflow, eval trigger not firing for scheduled runs, platform DB detail leaking into steps) | ~20% |
| Plan written wrong (schema pattern forcing a token the DB never holds, picked=false still running costly steps, report counting error rows as bids, missing `proposal_url`) | ~20% |
| Not bugs: notes and record-keeping ("correcting the record", "checked and clean", "expected by design") | ~16% |
| Not bugs: strategy and cost (zero proposals for 5 days, $52 per proposal, scan cadence, backup models) | ~12% |

## Goal: Technical finds close to zero workflow bugs

Measured per workflow: workflow-caused issues found by QA per week, by root cause. Three changes get there:

1. **Edit-time dependency updates.** A plan edit lists its dependents (consumers of a changed output, evals that check it, notes and guides that state the same rule, soul.md for business rules) in the edit response, and the editor updates them in the same change. Drift's job is done at edit time, not discovered later by QA.
2. **Platform problems become platform tickets**, filed once and fixed in the platform, never patched or worked around per workflow (proposal item 4).
3. **The builder tests before saving.** A changed step runs once in test mode (proposal item 1) before the change counts as done, so authoring bugs surface at authoring time.
Notes and strategy items stop being "issues": the single work-item record (proposal) keeps notes as notes and routes strategy to Product.
What is left for QA is the outside world changing (site changes, account restrictions, external failures).

## Left

- Edit-time dependency updates (change 1 above): the deterministic reference map is built in
  [PLAT-561](../../plans-contracts/plat-561.md) (plan and file edits list dependents; prompt health and Pulse
  carry open breaks). Left there: the AI pass for business-rule contradictions.
- The builder tests before saving (change 3) and test mode for steps with external effects (proposal item 1): step
  test mode is built in [PLAT-562](../../steps/general/plat-562.md) (`execute_step(test_mode=true)`: reads real,
  external effects stubbed and recorded, DB a copy, files in `runs/test-<id>/`, no learnings). Left there:
  full-workflow test mode, the Pulse rule that a fix to a step with external effects is verified only by a test-mode
  run (migration step 5), and a live check through a Builder chat.

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
