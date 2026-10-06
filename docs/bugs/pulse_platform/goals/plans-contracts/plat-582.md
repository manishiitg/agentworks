[← goals / plans-contracts](index.md)

# PLAT-582: Old unreadable inputs make Plan Drift due once

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | plans-contracts |
| Summary | An old unreadable input never made Plan Drift due, so agents left it; it now flags its step once |

## What happened

Owner, 2026-10-06: Upwork's `toptal-submit` reads `toptal_selected.json`, which `toptal-scan-draft` declares but does
not list in `context_output`. Asked why agents had not fixed it, and that a hand fix is not the answer. The reference
map called it a warning (message_sequence), and [PLAT-565](plat-565.md) only flags breaks that are new since the first
look, so nothing ever made Plan Drift look at it.

## Fix

The unreadable-input kinds of [PLAT-579](plat-579.md) (`dependency_unproduced`, `dependency_not_staged`,
`dependency_step_without_output`, `relative_dependency_unresolved`, `missing_step_ref`) now flag their step once even
when they are old, recorded in `strict_flagged` in `planning/reference_map_flags.json`. Plan Drift becomes due with the
reason and its guidance says how to repair it (list the file in the producer's `context_output`). Other old breaks
still only flag when new, so notes that mention history cannot keep Drift due forever. One test pins it.

## Left

- Prove it live: after a restart, the Upwork Pulse card should show Plan Drift due for `toptal-submit`; Run Drift
  check, and Drift (not a person) should add `toptal_selected.json` to `toptal-scan-draft`'s `context_output`.
