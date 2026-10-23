---
name: example-playbook
description: Guide the AgentWorks builder in creating a reusable example capability. Use when the customer requests the example outcome or needs its existing workflow repaired.
---

# Example Playbook

## Outcome

State the durable workflow capability and the result the customer receives.

## When to use

State positive triggers, scope, and any important exclusion.

## Discovery and user direction

Inspect the current workflow, goals, metrics, configuration, capabilities, stores, reports, and triggers before proposing changes. Summarize reusable design and gaps, then ask focused questions for unresolved customer choices such as scope, success, approvals, thresholds, ownership, and budgets. Record the answers as customer direction. Installation alone does not approve workflow changes or execution. Default to one small-team workflow; split only for incompatible access or lifecycle boundaries.

## Required inputs

List the minimum information, access, policies, and existing artifacts the builder must resolve. Treat unavailable required inputs as explicit blockers.

## Plan and AgentWorks tools

Explain how to compose this into the plan: important scripted steps, message sequences, routes, branches, schedules, or approvals; existing steps to reuse; and AgentWorks capabilities to resolve. Preserve the user's process and link to detailed guidance.

Recommend whether this route should start manually, from a workflow handoff, webhook or product event, release event, or schedule. Define the proposed scope, filters or cadence and timezone, concurrency, retries, budget, notifications, and durable completion evidence. Do not enable an unapproved trigger or invent cadence.

Keep review asynchronous unless the customer explicitly requests an attended run: persist a concrete `pending_review` proposal and end preparation, then use a separate action route that validates the later approval and current target state before applying it.

Publish review requests with `create_human_input_request` so they appear in the Report dashboard. Treat Ask in chat as discussion and Approve/Reject/Defer as saved decisions. Choose the later executor by target: a writable runtime code/folder route, typed Builder/Fixer tools for plan or configuration, or an approved external integration.

## Knowledge and persistence

State what belongs in workflow context, knowledgebase notes, learnings, database tables, and durable assets.

## Validation and reporting

Define machine-checkable readiness and failure behavior. Describe the dashboard's primary status/metrics, filters, evidence drill-down, history or trends, and incomplete/restricted states. Back it with durable workflow data.

## Guardrails

List the few constraints that must survive customer adaptation.

## Read details when needed

- [Workflow design and outcomes](../../agentic-engineering-platform/references/workflow-design-and-outcomes.md): goals, metrics, workflow boundaries, activation, and Autopilot focus decisions.
- [Implementation guide](references/implementation.md): topic-specific decisions and examples.
- [Example output](examples/output.json): illustrative shape only.
- [Catalog metadata](playbook.json): UI presentation and optional tool recommendations.

## Completion contract

Return the installed workflow and source versions, material customer overrides, trial result, artifact/report locations, and unresolved blockers.
