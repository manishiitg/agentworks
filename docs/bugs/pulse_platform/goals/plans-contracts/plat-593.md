[← goals / plans-contracts](index.md)

# PLAT-593: Old plan changes leave the review backlog

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | plans-contracts |
| Summary | Old-format plan changes older than 30 days leave the change backlog; the reference map checks the current workflow instead |

## What happened

After the 2026-10-06 Upwork Plan Drift run, Drift reported reviewing the newest 100 unreviewed plan changes and leaving
the rest. Measured with `CollectPlanChangeBacklog`: 193 remained, all old-format (no `change_id`) from 2026-07-11 to
2026-08-27; zero current-contract coverage failures, so none made Plan Drift due, but Pulse's state kept saying "193
changes lack review" and Drift kept spending turns on them. Tracing a months-old diff one by one is wasted work once
the steps it touched have changed again.

## Fix

`CollectPlanChangeBacklog` leaves out an old-format change (no `change_id`) more than 30 days old
(`legacyPlanChangeHorizon`) and says so in the note (`superseded_legacy_count`). Changes with a `change_id`, recent
ones and undateable ones stay. Nothing is stamped; the changelog keeps the full history. What matters, whether anything
is broken now, is checked by the reference map on the current workflow ([PLAT-561](plat-561.md)) and flagged to Plan
Drift ([PLAT-565](plat-565.md), [PLAT-582](plat-582.md)). One test pins it; the existing backlog and Pulse tool tests
pass with the fixtures' July dates read as recent. Upwork's backlog is now empty.
