[← goals / pulse-governance](index.md)

# PLAT-556 — The improvement loop never closes: fixes accrete in step descriptions and nothing consolidates

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | open: design proposal (layers, Brain option B, Pulse roles, budgets, delivery); nothing built yet. |

| Coordination | Value |
|---|---|
| State | open: design proposal written (2026-10-06); Brain option B chosen by the owner; nothing built under this ticket yet |
| Priority | P1 |
| Date | 2026-10-06 |
| Owner | pulse-governance (dedicated Pulse session; coordinate before editing Pulse code) |

## Source

Owner, 2026-10-06: "this is an issue we have been struggling with for very long" and "is there some flaw in the overall design we are missing".

## Evidence and design

See [workflow knowledge layers and the improvement loop](../../../../design/workflow_knowledge_layers.md). In short (Upwork): 334 description edits grew descriptions by 81,855 characters (builder chat +44k, Pulse +38k); Architecture review ran once (2026-09-28) and is since skipped as `plan_drift_review:exclusive_prerequisite`; every Architecture focus including `prompt_design` shows 0 reviews; the size nudge (2026-09-17) is ignored.

Earlier attempts each fixed one piece: PLAT-049 (platform mechanics), PLAT-258 (Plan Drift module), PLAT-285/290 (prompt-health tool), PLAT-303 (exception-driven Technical), PLAT-305 (separate QA/Architecture/Strategy; open: no observed autonomous improvement), PLAT-329 (plan tools), the size nudge, PLAT-555 (layout).

## Left (the six changes of the design note)

1. Guaranteed delivery of guides and notes a step names (Inputs, Guides).
2. Budgets as triggers (size vs plan median, dated text, duplication, stale KB) making consolidation due; never a gate.
3. Architecture may apply a consolidation under a no-loss check plus one comparison run.
4. Plan Drift must not starve Architecture; over-budget steps make Architecture due with `prompt_design` focus.
5. Repairs routed by type in fixer and builder-chat guidance.
6. One ownership map; retire per-step learnings folders and `graph.json`; KB stops deferring to descriptions.
Plus Brain option B (shared/org facts in Brain, workflow-only facts local; no cutover for these projects), tracked with PLAT-538.

## Register notes

[PLAT-556](plat-556.md), P1, open: design proposal (layers, Brain option B, Pulse roles, budgets, delivery); nothing built yet.
