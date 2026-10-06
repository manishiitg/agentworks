[← goals / step-execution](index.md)

# PLAT-555 — Step descriptions grow into manuals: give them a fixed layout (Goal, Inputs, Output, Rules, Done when, Guides)

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | goals |
| Area | step-execution |
| Summary | guidance on main; Upwork pilot and review-side enforcement open. |

| Coordination | Value |
|---|---|
| State | guidance on main (2026-10-06); pilot on Upwork `bid-pick-job` in progress (owner reviews before save); review-side enforcement open |
| Date | 2026-10-06 |
| Owner | step-execution |

## Source

Owner: "step description is like a user message ... what to do -> skills is how to best do it -> knowledgebase is overall global facts, decisions. Is this getting followed?" and "should we divide the step description into a proper logic format".

## Evidence (Upwork, read 2026-10-06)

- 28 steps carry 163,044 characters of instructions; the largest are `improve-analyze-report` (26,574), `toptal-scan-draft` (21,611), `profile-suggest-report` (21,273) and `bid-pick-job` (14,667).
- Descriptions hold procedure (shell working-directory rules, route guards), platform mechanics, and dated incident history: 37 of 158 sentences in `improve-analyze-report` state dates or decisions ("MEASURED 2026-08-10 ...").
- In message sequences the charter does the turns' job: `bid-pick-job` has a 13,103-character description and two items of 551 and 1,011 characters.
- KB notes `flow-bidding.md` and `flow-profile-update.md` mix decisions with procedure; the skill (`learnings/_global`) is mostly true how-to.
- Why it is not cleaned up: Technical review (476 records on Upwork) repairs failures, usually by adding to descriptions; Plan Drift checks prompt quality only for changed working fields and only the diff (description-only edits do not trigger it since 2026-09-26); Architecture review, which owns prompt clarity, KB freshness and learning applicability, has never run on Upwork (0 records).

## Done

- `agent_go/cmd/server/guidance/templates/system/step-description.md`: a required section layout for descriptions (Goal, Inputs, Output, Rules, Done when, Guides), where everything else belongs (phases in items, procedures in skills/learnings, facts and decisions in the KB, platform mechanics nowhere), and "a description holds no history": a repair changes the rule, item or guide, it does not append a story. Guidance tests pass.

- Plan edit tool responses (`planning_agent.go`): the description size nudge now names the layout and where content goes (how-to to a skill reference, facts and decisions to a KB note, dated history deleted). Above 3x the plan's median description size (floor 12,000 characters) it says OVER BUDGET and asks for the restructure in the same session, with a no-loss check; the edit is still saved. The ~700-character compatibility paragraph is sent on the first plan edit of a workflow and replaced by a one-line pointer for edits within the next 30 minutes of activity. Tests `TestStepDescriptionSizeNudgeOverBudgetAsksForTheMove`, `TestPlanEditImpactGuidanceIsSentOncePerSession`; the package suite passes except the pre-existing `TestValidateStepLLMConfigEnforcesAgyAlphaGate`.

## Left

- Pilot: rewrite Upwork `bid-pick-job` into the layout, move procedure to the skill and decisions to the KB, delete history; owner reviews before saving; run once and compare.
- Review side (Pulse; the Pulse session is not running): let a description edit trigger a prompt-quality-only Plan Drift check, and a whole-step review when a description is large or carries dated text; find out why Architecture review never runs on Upwork.
- Migrate other long descriptions step by step through the reviews, not in one sweep.

## Register notes

[PLAT-555](plat-555.md), guidance on main; Upwork pilot and review-side enforcement open.
