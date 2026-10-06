[← goals / plans-contracts](index.md)

# PLAT-565: Reference breaks make Plan Drift due (Go-side check)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | plans-contracts |
| Summary | A new broken reference (step, KB, learnings, eval, soul) makes Plan Drift due from the Go side, whatever wrote the file |

## What happened

[PLAT-561](plat-561.md) added the reference map but it only reported: at edit time for plan tools and for the builder's
`diff_patch_workspace_file`, and on read in prompt health. Nothing made Plan Drift due, and a native CLI edit or a
shell write was never seen until Pulse next read. On Upwork this left 75 breaks nobody was asked to fix, because Drift
is due only for step changes.

## Fix

- When the workflow's tracked files change (plan, step_config, eval plan, soul, KB and learnings markdown; checked by
  a stat fingerprint, 3.6 ms unchanged, 80 to 120 ms when it recomputes), the Go side rebuilds the map and compares the
  breaks with those seen last time. A NEW break flags its owning step, or the workflow-level record for a note, eval
  or learnings file. Whatever wrote the file does not matter.
- The flag makes `CollectPlanDriftDueItems` and `CollectPlanDriftCandidates` report the step as due with the reason
  "New broken references since the last review: ..." until a drift review is recorded after the flag.
- Only new breaks flag. Breaks that already existed stay Plan Drift's evidence in `get_plan_prompt_health` but do not
  keep it due, so a break Drift cannot or should not fix never blocks Technical and Architecture.
- The state is `planning/reference_map_flags.json` (a baseline of break keys and the open flags), never
  `step_config.json`, so the check cannot race a builder edit of the plan config.
- `plan-drift-review.md` tells Drift how to treat these candidates. One test pins it (new break flags, old break does
  not, a later review clears).

## Left

- The first look at a workflow only records the baseline, so breaks that exist today are not flagged. Upwork's 75 are
  Drift's backlog, read through prompt health.
- The map reports at most 150 issues; a break past that cap is neither flagged nor in the baseline.
- Rule contradictions between notes and `soul.md` and values an eval asserts need the AI pass, still next in PLAT-561.
- Confirm live: after a Plan Drift run on Upwork, add a broken path to a KB note and see "New broken references" on
  the Pulse Drift card.
