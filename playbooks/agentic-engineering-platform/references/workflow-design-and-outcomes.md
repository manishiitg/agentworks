# Workflow design and outcomes

Use this check before adapting any playbook. Its purpose is to preserve the customer's operating model while making the resulting workflow measurable.

## Inspect the current system

Read the workflow objective and success criteria, then inspect the current plan, routes, triggers, schedules, integrations, permissions, durable data, report, and installed playbooks. Reuse established names, owners, environments, policies, and metrics. Identify what is verified, assumed, missing, or conflicting.

## Goals and metrics

Map the requested capability to an existing goal when one already covers the outcome. Recommend a new or revised goal only when the desired outcome is otherwise unowned or unmeasurable. Define:

- the outcome and decision it supports;
- success, leading, quality, reliability, cost, and safety indicators that matter for this playbook;
- baseline, target or decision threshold supplied by the customer;
- calculation, dimensions, evidence source, owner, and review cadence;
- incomplete, stale, or unavailable states.

Never invent a target after observing results. When no approved threshold exists, record a baseline and report the result as unrated until the customer chooses a policy. Reuse existing metrics when their definition, population, environment, and time window are compatible.

## Current or separate workflow

The current catalog targets small engineering teams. Prefer one understandable workflow with coherent routes when the capability shares its objective, owner, access boundary, environment, durable data, and operating lifecycle. Minimize connectors, stores, dashboards, schedules, and approval paths the team must maintain.

Recommend a separate workflow when it has an independent objective or owner; a materially different trigger, cadence, environment, tenant, credential or permission boundary; an approval or blast-radius boundary; distinct scaling, retention, deployment, or reliability needs; or a lifecycle that should succeed and fail independently.

Do not split because a playbook has several stages, a source refreshes more frequently, or a route has its own trigger. Use a separate workflow only when the boundary must succeed, fail, deploy, retain data, or restrict access independently. For separate workflows, define the supported handoff, stable identifiers, data contract, permissions, retry/idempotency behavior, and reporting ownership. Do not copy secrets or rely on another workflow's private paths.

## Triggers and schedules

Decide how the capability should start: manual request, workflow handoff, webhook or product event, pull request or deployment event, or recurring schedule. Reuse a compatible existing trigger when it has the same scope and policy. Recommend a separate trigger or schedule when the playbook needs an independent cadence, event source, environment, owner, budget, or failure lifecycle.

For every proposed trigger or schedule, define the route or workflow it starts, event filters or cadence and timezone, enabled environments, overlap and concurrency policy, idempotency and retry behavior, execution budget, notification and escalation behavior, and the durable result that proves completion. State whether it runs the full workflow or a bounded route. Use event-driven execution when the source can emit a reliable event; use schedules for recurring assurance, monitoring, aggregation, and review.

Never invent cadence, enable a schedule, or attach a webhook without the user's approved operating policy. When cadence is unknown, recommend options with their cost and detection-delay tradeoffs. Preserve existing schedules, thresholds, notification preferences, and budgets unless the user changes them explicitly.

## Durable human decisions

Do not assume a reviewer is present while a workflow runs. For consequential changes, prefer two independently runnable phases:

1. A preparation route produces and verifies the exact proposal, persists its identity, evidence, target/base revision, expiry, and a `pending_review` decision, exposes it in the dashboard, then ends without applying anything.
2. A later action route loads an approved decision by stable ID, verifies reviewer authority and that the approved proposal hash, target, base revision, scope, and policy still match, then applies it once and records the receipt. Stale or changed state returns to review.

The reviewer may approve, reject, or defer from the supported durable decision surface after the preparation run finishes. A dashboard refresh never constitutes approval and does not execute the action route. Use a blocking human branch only when the customer explicitly chooses a bounded attended run; do not use it as the default for scheduled, webhook, long-running, or variable-duration work.

## Autopilot review focus

Read the playbook manifest's `pulse_focus` recommendation for Strategic Review. Compare it with current Autopilot configuration and retained review coverage. Recommend it only when it answers a useful goal or product question:

- Strategic Review checks whether the capability and measurements advance the customer's goals and whether a materially different approach deserves consideration.

Playbooks do not specialize Plan Drift, Technical, or Architecture Review. Technical chooses its scope from operational evidence; Architecture chooses its scope from structural risk and change. A Strategic focus refines the review but does not replace its canonical behavior or lightweight safety scan. Recommend manual, event-driven, or scheduled review based on risk and evidence volume. Do not activate persistent specialization or a schedule without the user's approved policy.

## Builder recommendation

Before structural changes, present:

1. the current objective, relevant goals, metrics, and workflow boundaries;
2. the recommended placement: extend the current workflow, create a route, or create a separate workflow;
3. the concrete reasons and tradeoffs;
4. proposed goal/metric additions or changes, with missing customer decisions;
5. the recommended manual, event, webhook, release, or scheduled activation model;
6. the plan, data, reporting, trigger, integration, and permission changes that follow.

Proceed with the user's chosen structure. Record the decision and customer overrides in the installed playbook setup record.
