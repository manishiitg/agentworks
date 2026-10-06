[← goals / steps](index.md)

# PLAT-635: Agents get local time in the turn header

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | goals |
| Area | steps |
| Summary | Step prompts teach agents to convert the IST turn-header time to UTC; the platform should give UTC |

## What happened

After the 1.0.46 description upgrade, sales outreach `step-research-and-draft-outreach` carries the rule
"generated_at and verified_at are true UTC (the turn header time is IST; subtract 5 hours 30 minutes)". The turn
header gives the server's local time, so a workflow taught every step to convert it. A platform quirk should not be
worked around in each step's prompt.

## Fix (not built)

Give agents the current time in UTC (with the local time alongside if wanted) in the turn header, then let Workflow
Review drop the conversion rules from step descriptions.
