[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-381 — Scheduled runs no longer apply decisions; Needs you keeps unapplied ones with "Apply in chat"

| Coordination | Value |
|---|---|
| State | fixed on `main` (this commit); not deployed |
| Date | 2026-10-03 |
| Owner | human-decisions |
| Related | PLAT-093 (the pre-run drain this removes) |

## Problem

A scheduled run started with a "PRE-RUN DECISION DRAIN" turn that applied
answered-but-unapplied decisions, unattended, before the run's own work. The
owner wants decisions applied only where they can watch: the UI and the
workflow's Builder chat. Answered decisions also dropped into collapsed history
labelled "being applied in chat, or at the next run", so an unapplied one was
easy to miss (social-media had two since 2026-09-25/28).

## Fix

- `scheduler.go`: the drain is removed; runs never apply decisions. The notice
  that lists unanswered decisions on the first run turn stays (it applies
  nothing).
- Decision lists carry `apply_message` for every answered decision
  (`withDecisionApplyMessages`).
- Needs you shows answered-but-unapplied decisions above pending ones,
  "Answered, not applied yet", with **Apply in chat**, which sends that message
  to the workflow's Builder chat.

## Done / left

- Done: `decision_apply_list_test.go`; panel test for Apply in chat; workflow
  component tests pass.
- Left: header activity badges count only unanswered decisions; decide whether
  unapplied ones should count too.
