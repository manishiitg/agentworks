---
name: new-customer-to-first-value
description: Build a chat-led customer onboarding route with owned milestones, verified first-value evidence, and optional account health review.
---

# New Customer to First Value

## Outcome

Help a new B2B customer reach its agreed first result. Coordinate an owned onboarding register, observed product adoption, and an optional health review. Report unknown when evidence is missing.

## When to use

Use when onboarding and product adoption have distinct owners, sources, or review needs. A single Crew can handle a simple account check in chat or on its own schedule.

## Discovery and user direction

Inspect the existing Automation, Crews, handoff, account and tenant identity, onboarding source, usage source, first-value rule, and policies. Propose concrete steps and the first useful result. Selection saves guidance only. Record verified choices in `SETUP.json`.

## Required inputs

Resolve an authorized new-customer handoff, purchased scope, named owner, customer-specific first-value event and target date, onboarding milestones, product event coverage, and contact policy. Files or exports support a manual first run.

## Plan and AgentWorks tools

Use ready Customer Onboarding Coordinator and Product Adoption Analyst Crews; add Customer Health Coordinator only when useful. Reuse suitable Crews. After review, create missing ones with `create_crew` and stable idempotency keys. Specify schema fields and output paths in Crew steps. Insert blocking validator script steps before consumers and pass only bounded validated artifacts through authorized attachments. A missing or mismatched account ID stops the handoff.

## Knowledge and persistence

Save account and tenant IDs, source scope, rule version, owners, Crew and run IDs, milestone IDs, artifact paths, validation, owner decisions, and stable action IDs. Compare repeated runs with the same account and rule; deduplicate events and proposed actions.

## Validation and reporting

Validate `onboarding-milestone-register/v1` before Adoption reads it. When this follows Signed Deal to Onboarding Handoff, validate that package's artifact pair first, then validate onboarding with `--accepted-handoff` to join exact account, purchased scope, rule, target and accepted artifact. Validate `first-value-readout/v1` against the register and rule; validate optional `customer-health-brief/v1` before reporting. Check source truth separately. The reporting dashboard shows milestone states, first-value status, coverage, blockers, owners, dates and cost. Test one real manual route before recurrence.

## Guardrails

Do not mark a milestone complete without evidence or infer value from a sign-in. Missing instrumentation means unknown, not failed. A health hypothesis is not a verified churn prediction. No Playbook selection messages a customer, changes account access, writes CRM, creates tasks, or enables a schedule. Customer-facing or account writes need a separate approved route and recorded provider result. Keep tenant data within its authorized Crew scope.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md): goal and route decisions.
- [Team and handoffs](references/team-and-handoffs.md): Crew binding and validation.
- [Artifact validator](scripts/validate_customer_success_artifact.py): blocking contract checks.
- [Onboarding example](examples/onboarding-milestone-register.json), [adoption example](examples/first-value-readout.json), [health example](examples/customer-health-brief.json), and [invalid adoption](examples/invalid-first-value-readout.json): fictional fixtures.
- [Setup progress](SETUP.json): checks and evidence.

## Completion contract

Return version, owner choices, Crew IDs and setup state, source map, Workflow steps, validated handoffs, real manual run, first-value status and coverage, owner actions, activation state, and blockers.
