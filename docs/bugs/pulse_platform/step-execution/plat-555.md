[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-555 — Step descriptions grow into manuals: give them a fixed layout (Goal, Inputs, Output, Rules, Done when, Guides)

| Coordination | Value |
|---|---|
| State | guidance on main (2026-10-06); pilot on Upwork  in progress (owner reviews before save); review-side enforcement open |
| Date | 2026-10-06 |
| Owner | step-execution |

## Source

Owner: "step description is like a user message ... what to do -> skills is how to best do it -> knowledgebase is overall global facts, decisions. Is this getting followed?" and "should we divide the step description into a proper logic format".

## Evidence (Upwork, read 2026-10-06)

- 28 steps carry 163,044 characters of instructions; the largest are  (26,574),  (21,611),  (21,273),  (14,667).
- Descriptions hold procedure (shell working-directory rules, route guards), platform mechanics, and dated incident history: 37 of 158 sentences in  state dates or decisions ("MEASURED 2026-08-10 ...").
- In message sequences the charter does the turns' job:  has a 13,103-character description and two items of 551 and 1,011 characters.
- KB notes  and  mix decisions with procedure; the skill () is mostly true how-to.
- Why it is not cleaned up: Technical review (476 records on Upwork) repairs failures, usually by adding to descriptions; Plan Drift checks prompt quality only for changed working fields and only the diff (description-only edits do not trigger it since 2026-09-26); Architecture review, which owns prompt clarity, KB freshness and learning applicability, has never run on Upwork (0 records).

## Done

- : a required section layout for descriptions (Goal, Inputs, Output, Rules, Done when, Guides), where everything else belongs (phases in items, procedures in skills/learnings, facts and decisions in the KB, platform mechanics nowhere), and "a description holds no history": a repair changes the rule, item or guide, it does not append a story. Guidance tests pass.

## Left

- Pilot: rewrite Upwork  into the layout, move procedure to the skill and decisions to the KB, delete history; owner reviews before saving; run once and compare.
- Review side (Pulse; the Pulse session is not running): let a description edit trigger a prompt-quality-only Plan Drift check, and a whole-step review when a description is large or carries dated text; find out why Architecture review never runs on Upwork.
- Migrate other long descriptions step by step through the reviews, not in one sweep.
