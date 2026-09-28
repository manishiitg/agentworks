---
name: activation-retention-intelligence
description: Propose a maturity-aware Lifecycle Analyst to Growth Experiment Planner route for SaaS cohort retention.
---

# Activation and Retention Intelligence

## Outcome

Lifecycle Analyst measures day-7 activation and day-30 retention for signup cohorts. Growth Experiment Planner consumes a mature observation and proposes one test. Cohort differences do not prove cause or individual churn.

## When to use

Use with authorized signup, usage and subscription records. New Customer to First Value covers one account's result; Funnel and Conversion Intelligence covers signup-to-paid conversion. This route compares mature cohorts or records one baseline. An immature cohort waits.

## Discovery and user direction

Builder inspects lifecycle definitions, maturity cutoff, source coverage, identity join, owner and existing Crews. It proposes reuse or creation of distinct Lifecycle Analyst and Growth Experiment Planner Crews, then shows the source and review plan in chat. Selection creates no Crew, customer contact, query, variant or schedule.

## Required inputs

Record product and tenant, equal signup-cohort windows, eligible-account definition, activation and day-30 retention predicates with versions, identity and exclusion rules, timezone, maturity date, event and billing sources, minimum coverage, owner, metric, guardrail and approval policy. Feedback is optional and needs authorized access.

## Plan and AgentWorks tools

1. Bind two Crew IDs and Workflow steps. Lifecycle Analyst saves `cohort-retention-observation/v1` with source references and one of `comparable`, `baseline_first` or `pending_maturity`. Run `python3 scripts/validate_handoff.py observation <observation.json>` as a blocking step.
2. Stop if the cohort is immature. Otherwise pass the validated file by checked alias. Growth Experiment Planner preserves exact policy, cohort IDs and observed rates, then saves pending `retention-experiment-plan/v1`. Run `python3 scripts/validate_handoff.py plan <observation.json> <plan.json>` before reporting.
3. Review the result and test proposal with the owner. Assignment, power calculation, variant launch, customer action and outcome measurement need separately reviewed routes.

## Knowledge and persistence

Store cohort and predicate versions, account join rule, source/artifact IDs, maturity dates, counts, corrections, Crew runs and owner decisions. Keep private feedback within its approved source scope.

## Validation and reporting

Recompute activation and retention numerators, denominators, maturity, censoring and identity coverage. Compare only equal windows under the same rule and report a new baseline after instrumentation changes. The dashboard shows cohorts, mature versus pending states, rates, source coverage, limitations, hypotheses and owner actions. No trend exists with only one mature cohort.

## Guardrails

Never call an immature cohort churned, use a cohort average as an individual risk label, or claim a feature caused retention without valid causal evidence. No customer message, CRM write or product change follows from this proposal. A pending experiment has no winner.

## Read details when needed

- [Team and handoff](references/team-and-handoffs.md)
- [Shared workflow design and outcomes](../../references/workflow-design-and-outcomes.md)
- [Lifecycle workflow](references/lifecycle-intelligence-workflow.md) and [growth data model](../references/growth-data-model.md)
- [Comparable cohorts](examples/cohort-retention-observation.json), [baseline-first case](examples/cohort-retention-baseline-first.json), [pending maturity](examples/cohort-retention-pending-maturity.json), [pending plan](examples/retention-experiment-plan.json) and [rejected false plan](examples/invalid-retention-experiment-plan.json)
- [Setup checklist](SETUP.json) and [catalog metadata](playbook.json)

## Completion contract

Return the Crew plan, source policy, validated artifact paths, maturity and coverage status, calculated rates or explicit unknowns, owner decision, manual-run proof and paused repeat choice.
