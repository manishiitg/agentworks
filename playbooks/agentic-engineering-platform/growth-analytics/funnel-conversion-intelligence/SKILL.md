---
name: funnel-conversion-intelligence
description: Propose a two-Crew signup-to-paid investigation that reconciles stage counts and produces a reviewed experiment plan.
---

# Funnel and Conversion Intelligence

## Outcome

Funnel Analyst reconciles eligible, signup, activated and paid accounts for a defined cohort. Growth Experiment Planner consumes the validated observation and proposes one bounded test. An observed rate change is not a causal explanation or an experiment result.

## When to use

Use for a SaaS signup-to-paid question with authorized event and billing records. Campaign Signal covers campaign responses; Website Growth Loop covers site traffic. A new company can start with one complete baseline window and no trend.

## Discovery and user direction

Builder inspects sources, event definitions, identity join, existing Crews and owner. It proposes distinct Funnel Analyst and Growth Experiment Planner Crews in chat. Selection creates no Crew, query, experiment or schedule.

## Required inputs

Record product/tenant and eligible cohort, ordered stage versions, time zone, exclusions, identity and deduplication rules, event and paid-state sources, source freshness, comparison windows, minimum join coverage, owner, primary metric, guardrail and action policy. Consent is required before session replay; replay is optional.

## Plan and AgentWorks tools

1. Bind two Crew IDs and Workflow steps. Funnel Analyst reads scoped event and billing records, captures unique-user counts and source revisions, and saves `funnel-observation/v1` at its step path. Run `python3 scripts/validate_handoff.py observation <observation.json>` as a blocking step.
2. Growth Experiment Planner reads the validated artifact through a checked alias. It preserves cohort and metric IDs, states a falsifiable hypothesis, assignment unit, guardrail and pending sample calculation, then saves `funnel-experiment-plan/v1`. Run `python3 scripts/validate_handoff.py plan <observation.json> <plan.json>` before reporting.
3. Review the result and proposed test with the owner. A separate approved route must calculate power, launch a variant and later compare outcomes. No schedule or external write follows from this proposal.

## Knowledge and persistence

Store cohort/event versions, identity rules, source and artifact IDs, counts, gaps, Crew runs and owner decisions. Keep private sessions out of reports.

## Validation and reporting

Recompute monotone stage counts, paid/eligible and activated/signup rates, identity coverage and billing cutoff for equal windows. The dashboard shows denominators, source freshness, drop-offs, uncertainty, owner action and drill-downs. Changed event semantics or incomplete paid evidence starts a new baseline rather than a comparable trend.

## Guardrails

A click or trial is not paid state. Do not claim a funnel change caused churn, expose unconsented sessions, or change billing, pricing, messaging or checkout without a separate reviewed route. A pending experiment has no winner.

## Read details when needed

- [Team and handoffs](references/team-and-handoffs.md)
- [Shared workflow design and outcomes](../../references/workflow-design-and-outcomes.md)
- [Conversion workflow](references/conversion-intelligence-workflow.md) and [growth data model](../references/growth-data-model.md)
- [Reconciled observation](examples/funnel-observation.json), [baseline-first case](examples/funnel-baseline-first.json) and [plan](examples/funnel-baseline-first-plan.json), [pending experiment](examples/funnel-experiment-plan.json) and [rejected false plan](examples/invalid-funnel-experiment-plan.json)
- [Setup checklist](SETUP.json) and [catalog metadata](playbook.json)

## Completion contract

Return the reviewed team plan, source/metric policy, validated artifact paths, calculated rates, owner decision, missing evidence, manual-run proof and paused repeat choice.
