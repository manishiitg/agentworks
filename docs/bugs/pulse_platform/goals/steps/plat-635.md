[← goals / steps](index.md)

# PLAT-635: Agents get local time in the turn header

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | steps |
| Summary | Step prompts teach agents to convert the IST turn-header time to UTC; the platform should give UTC |

## What happened

After the 1.0.46 description upgrade, sales outreach `step-research-and-draft-outreach` carries the rule
"generated_at and verified_at are true UTC (the turn header time is IST; subtract 5 hours 30 minutes)". The turn
header gives the server's local time, so a workflow taught every step to convert it. A platform quirk should not be
worked around in each step's prompt.

## Fix

Give agents the current time in UTC (with the local time alongside if wanted) in the turn header, then let Workflow
Review drop the conversion rules from step descriptions.

Built 2026-10-07: step prompts (message_sequence, execution-only steps and planning) get the date and time from one
helper, `promptClock` (`step_based_workflow/prompt_clock.go`). The header now reads e.g.
`## Context: 2026-10-07 | 20:45:00 UTC (server local 2026-10-08 02:15 IST)`; the date is the UTC date. Test:
`TestPromptClockIsUTCWithLocalAlongside`.

Left: existing step descriptions that tell agents to subtract 5:30 now get UTC already and would convert twice.
Workflow Review should drop those rules (sales outreach `step-research-and-draft-outreach`); until then the step's own
rule is wrong by 5:30.
