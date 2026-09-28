---
name: growth-experimentation-follow-through
description: Propose an approved experiment run and guarded outcome readout through two reusable Crews.
---

# Growth Experimentation and Follow-Through

## Outcome

Experiment Run Coordinator verifies a frozen, approved plan and provider launch. Growth Outcome Analyst measures pre-registered primary and guardrail metrics after the full window. A plan, ticket or early difference is not a live experiment or win.

## When to use

Use after a sourced growth question has a plan from Campaign Signal, Funnel and Conversion, Activation and Retention, SEO, AI Visibility, or an equivalent source. Freeze and approve the plan before launch. This Playbook tracks execution and readout; it grants no change authority.

## Discovery and user direction

Builder inspects plan revision, decision, Crews, provider, allocation, sources, owners, window and sample rule. It proposes run and outcome Crews in chat. Selection creates no Crew, ticket, flag, message or schedule.

## Required inputs

Record experiment/product/tenant IDs, frozen plan artifact and revision, eligible account rule, control/treatment IDs and allocation, primary and guardrail definitions, minimum sample, readout window, stop rule, approval policy, rollout and rollback owners, provider state and source access.

## Plan and AgentWorks tools

1. Bind Crew IDs and Workflow steps. Save a sourced `frozen-experiment-plan/v1` snapshot. Run Coordinator saves `experiment-execution-record/v1` with `pending_approval` or `launched` state. A launch needs exact plan approval, provider launch receipt and exposure source. Run `python3 scripts/validate_handoff.py execution <plan.json> <record.json>` as a blocking step.
2. A pending record stops outcome claims. For provider-confirmed launch, pass the validated artifact by checked alias. Outcome Analyst saves `experiment-outcome-readout/v1` as `pending_window`, `inconclusive` or `measured`. Run `python3 scripts/validate_handoff.py readout <plan.json> <record.json> <readout.json>` before reporting.
3. The owner reviews measured or inconclusive evidence. Shipping, rollback, budget, CRM, contact and notification actions use separately approved routes and provider receipts.

## Knowledge and persistence

Store plan and decision revisions, stable experiment and variant IDs, provider receipts, exposure and outcome sources, readout windows, denominator/guardrail counts, Crew runs, corrections and owner decisions. Never overwrite a pre-registered rule with a later result.

## Validation and reporting

Check identity, decision and provider linkage, allocation, maturity, counts, rates, sample and guardrail. The dashboard shows source coverage, launch and readout states, limitations and owner decision. Underpowered or breached tests are inconclusive; complete readouts need owner review.

## Guardrails

Never infer launch from a task, claim success from an early or unpowered rate, change targets after seeing results, or perform a customer/product action from installation. Missing approval or receipt blocks the route.

## Read details when needed

- [Team and handoff](references/team-and-handoffs.md)
- [Shared workflow design](../../references/workflow-design-and-outcomes.md), [growth data model](../references/growth-data-model.md), and [experiment workflow](references/experimentation-workflow.md)
- [Frozen plan](examples/frozen-experiment-plan.json), [launched execution](examples/experiment-execution-record.json), [pending approval](examples/experiment-pending-approval.json), [pending window](examples/experiment-pending-window.json), [inconclusive readout](examples/experiment-inconclusive-readout.json), [measured readout](examples/experiment-measured-readout.json), and [false winner](examples/invalid-experiment-readout.json)
- [Setup checklist](SETUP.json) and [catalog metadata](playbook.json)

## Completion contract

Return Crew plan, exact frozen policy and source IDs, validated artifact paths, launch and measurement states, rates or unknowns, owner decision, manual-run proof and paused repeat choice.
