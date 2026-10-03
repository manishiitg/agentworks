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

- Background Pulse fix runs no longer apply answered decisions either: the
  `list_approved_fixer_decisions` tool and its pulse-fixer step are removed
  (found by ai-work-0b's review). A targeted-fixer decision is applied in the
  Builder chat with the same bounded fixer instructions.
- The unapplied card says "Answered" (and "in Slack"/"in WhatsApp" when the
  answer came from there), not "You answered".

## Done / left

- Done: `decision_apply_list_test.go`; panel test for Apply in chat; workflow
  component tests pass.
- Done: the header activity badges and the org dashboard count unanswered
  plus answered-not-applied decisions (`needsYouDecisions`, owner decision).
- Left: an answer given in Slack or WhatsApp waits in Needs you until someone
  presses Apply in chat (the owner's rule); the badges show it.
