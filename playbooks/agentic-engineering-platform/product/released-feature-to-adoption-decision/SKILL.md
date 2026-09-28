---
name: released-feature-to-adoption-decision
description: Propose a bounded released-feature adoption observation and a separate product owner decision.
---

# Released Feature to Adoption Decision

## Outcome

Show how many eligible accounts saw and used one released feature under a frozen rule, then prepare a product owner decision. The observation may be a baseline, comparable repeat, or not evaluable. Usage does not prove causality.

## When to use

Use after release or flag exposure when Product wants to investigate, iterate, keep or stop a feature. New Customer to First Value measures one customer's agreed outcome; this route measures an eligible segment. An unreleased flag does not qualify.

## Discovery and user direction

Builder inspects Product Adoption Analyst and Product Feedback Coordinator skills, release and flag source, eligible segment, event identity, coverage and owner criteria. Propose reuse of compatible Crews, validated steps and a manual case before configuration. Selecting the Playbook starts no event, issue, feature-flag change or schedule.

## Required inputs

Bind tenant, product, feature, release/build, flag revision, segment, equal-window policy, distinct-account join, eligibility, exposure and use predicates, predeclared target, minimum exposed sample, source coverage, current issue source and product owner. Missing instrumentation yields `not_evaluable` and an explicit next measurement step.

## Plan and AgentWorks tools

Workflow steps: Product Adoption Analyst reads the authorized release, flag and analytics exports, deduplicates accounts, and emits `feature-adoption-observation/v1`. Run the packaged validator as a blocking step. Product Feedback Coordinator receives only the validated artifact, re-reads current release and issue state, and emits `feature-adoption-decision/v1`; validate the exact pair. Ask the product owner to review the recommendation and gaps. Any issue write, flag change, customer message or experiment launch is a separate approved action with a provider receipt.

## Knowledge and persistence

Keep release/build, flag and rule versions, windows, eligibility and event-source revisions, observation IDs, owner decisions and case keys. On repeat, compare equal windows under the same definitions and preserve corrected counts. A changed flag or instrumentation rule starts a new baseline.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks release timing, exact scope, count bounds and arithmetic, source coverage, sample minimum, comparable-window rules, target state and decision/owner claims. Fictional [baseline](examples/feature-adoption-observation.json), [comparable repeat](examples/feature-adoption-comparable.json), [not evaluable](examples/feature-adoption-not-evaluable.json), [pending decision](examples/feature-adoption-decision.json) and [invalid decision](examples/invalid-feature-adoption-decision.json) teach the contract. The dashboard separates exposed/eligible from used/exposed, unknown coverage, one baseline versus trend, and owner review versus action.

## Guardrails

Do not use all signups as the denominator if only some accounts were eligible or exposed. Do not declare an experiment winner, cause, retention effect, customer promise or shipped product change from a usage observation. A target miss can prompt investigation; it cannot authorize a flag rollback, issue change or customer contact.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choices.
- [Team and handoffs](references/team-and-handoffs.md) for cohort and owner boundaries.
- [Setup](SETUP.json) for ten pending checks; fixtures are fictional.

## Completion contract

Return Crew IDs, source and rule map, validated artifacts, coverage and denominator limits, owner decision or pending review, real manual case, blockers and activation choice.
