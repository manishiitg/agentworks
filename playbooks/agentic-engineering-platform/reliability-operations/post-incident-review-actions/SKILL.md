---
name: post-incident-review-actions
description: Propose a sourced post-incident review and independently verified improvement follow-through through two Crews.
---

# Post-Incident Review and Actions

## Outcome

Post-Incident Reviewer reconstructs a stabilized incident and proposes improvements. Improvement Follow-Through Coordinator tracks decisions, issue receipts and independent control evidence. A draft is not published; a closed issue is not verified improvement.

## When to use

Use after verified stability or an approved near miss. Keep live mitigation in Incident to Verified Recovery. Both jobs use the same incident, service and environment. One Crew may carry both skills when access fits; validate each output.

## Discovery and user direction

Builder inspects incident/recovery records, Crews, impact rules, sources, privacy, owners and issue/verification access. It proposes both jobs in chat. Selection creates no Crew, ticket, publication, reminder or schedule.

## Required inputs

Record incident/service/environment, verified stability source, frozen evidence window, impact numerator and denominator, timeline, review owner and audience, sensitive-data rule, action owners/dates, verification criteria, issue and approval policy. Publication and recurrence are optional separate decisions.

## Plan and AgentWorks tools

1. Bind two Crew IDs and Workflow steps. Reviewer saves `post-incident-review/v1` with exact sources, impact arithmetic, factor labels, unknowns and proposed actions. Run `python3 scripts/validate_handoff.py review <review.json>` as a blocking step. Owner approval is a dated decision, separate from drafting.
2. Pass the validated review by checked alias. Coordinator saves `incident-improvement-register/v1`, preserving exact action IDs. Run `python3 scripts/validate_handoff.py register <review.json> <register.json>` before reporting. Pending review creates no issue. Accepted work needs decision and issue receipt; verified work needs later independent passing evidence.
3. Review source truth, privacy and next actions with owners. Issue creation, publication, notifications and changes require separately authorized routes and receipts.

## Knowledge and persistence

Store incident/review revisions, immutable source refs, timeline, impact calculation, factor classifications, unknowns, owner decisions, action IDs, issue receipts, verification results and corrections. Never silently rewrite a published review or erase an earlier action state.

## Validation and reporting

Check canonical stability, dated timeline, matching impact rate, distinct action IDs, exact review handoff, acceptance chronology and independent verification. The dashboard shows review state, impact, unknowns, action owners/dates, pending and verified states, source links and overdue risk. Structural checks do not prove cause; owner review remains required.

## Guardrails

Do not blame individuals, publish restricted evidence, assert a cause from timing alone, create work from a draft, or mark an issue verified from closure status. Installation grants no external action or recurrence.

## Read details when needed

- [Team and handoff](references/team-and-handoffs.md)
- [Shared workflow design](../../references/workflow-design-and-outcomes.md), [review workflow](references/post-incident-workflow.md), and [reliability event contract](../references/reliability-event-contract.md)
- [Approved review](examples/post-incident-review.json), [draft review](examples/post-incident-draft.json), [verified and pending register](examples/incident-improvement-register.json), [draft-pending register](examples/incident-improvement-pending.json), and [false completion](examples/invalid-improvement-register.json)
- [Setup checklist](SETUP.json) and [catalog metadata](playbook.json)

## Completion contract

Return Crew plan, source/policy map, validated artifact paths, reviewed impact and unknowns, exact action states, owner decisions, manual-run proof and paused repeat choice.
