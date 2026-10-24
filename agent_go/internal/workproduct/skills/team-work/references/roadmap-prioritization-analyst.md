# Roadmap Prioritization Analyst

This skill gives one Crew a Product capability. It can seed a new Crew or be added to a compatible Crew. Customer source systems and owner policies remain authoritative.

## First useful result

1. Freeze the candidate set, product goal, scoring policy version, decision horizon and owner.
2. Join each candidate to current research, issue, customer impact and Engineering estimate by exact ID; keep unknown effort and reach explicit.
3. Apply the same rule to each candidate and show sensitivity when a missing input could change the order.
4. Present now, next, later or needs-evidence options and record the owner decision separately from the recommendation.

Deliver **A ranked or unranked opportunity decision table with source evidence, scoring rule, uncertainty, dependencies, owner decision and deferred options.** Record source IDs, revisions, observed times, coverage, owner and unknowns.

## Source probe and acceptance

Read two current opportunity records, their evidence links, the approved strategy goal, current roadmap revision and estimate source. Verify that neither opportunity is already committed or duplicated under another issue ID.

Recompute one ranking or explain why it is unrankable; include uncertainty, capacity and a rejected option. A revenue anecdote or feature-request count cannot silently become a weighted score.

## Fictional worked example

Fictional input: goal=reduce first-week setup failures; candidates=opp-8,opp-12; policy=priority-v2; capacity=one squad; owner=product-lead.

Fictional output: opp-8 has three source-backed setup failures and an unestimated dependency; opp-12 has ten requests but no denominator; neither receives a false numeric rank. Recommendation=investigate opp-8 dependency first; roadmap state=unchanged; owner decision=pending.

Inadequate: “Opp-12 has ten requests, so commit it for next sprint.” Reason: Request count is not comparable impact, capacity is unverified and no owner accepted a date.

## Automation handoff

Product Requirements Coordinator can consume product-priority-decision/v1 only after an accountable owner accepts the exact opportunity, goal and scope. Engineering estimates remain separately owned. Builder must validate the exact artifact identity and schema before another Crew consumes it.

## Later run

Retain candidate and rule versions, compare current evidence and roadmap state, and reopen the decision only for a material change or scheduled owner review.

## Boundaries

Do not reprioritize a live board, alter delivery dates, promise work to a customer or treat a model score as an approved roadmap decision. Installation enables no schedule, trigger, function, Automation or external write.
