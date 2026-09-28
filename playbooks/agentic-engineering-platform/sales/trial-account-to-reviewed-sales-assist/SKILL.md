---
name: trial-account-to-reviewed-sales-assist
description: Propose an exact-account trial usage review and a permission-gated, unsent Sales assist.
---

# Trial Account to Reviewed Sales Assist

## Outcome

Turn one authorized B2B SaaS trial into a sourced product-use observation, then an owner-reviewed Sales draft, no-contact decision or unresolved review. Product use is neither purchase intent nor contact permission.

## When to use

Use when a company has trial/subscription and product-event records plus an accountable seller. Start with one manual trial. Inbound enquiry qualification and cohort funnel analysis use separate Playbooks.

## Discovery and user direction

Inspect existing Product Adoption Analyst and Sales Follow-up Coordinator Crews, exact trial and event sources, CRM account mapping, recipient permission, suppression, previous replies and meetings. Propose Crew reuse or reviewed creation; show the route before Builder applies it. Selection leaves setup pending.

## Required inputs

Bind entity, product, tenant, product account, trial/subscription and status revision; use rule, window, event coverage and cutoff; exact CRM account mapping, seller, permitted recipient and channel; fit, consent, suppression, prior-contact and offer policy. Identify sources for any later provider delivery or meeting claim.

## Plan and AgentWorks tools

Product Adoption Analyst emits `trial-usage-observation/v1` as JSON. Set its Crew step's exact `context_output` and `validation_schema`, then run the bundled business validator as a blocking step before Sales consumes it. Sales re-reads trial and CRM state and emits `trial-sales-assist/v1`: `draft`, `no_contact` or `needs_review`. A draft remains unsent. Any send is a separate, exact-message approved route; booking or paid conversion requires later source observation.

## Knowledge and persistence

Save trial/status and event-rule revisions, source IDs, account mapping, CRM contact and policy revision, prior-contact check, decision, message fingerprint, owner approval, provider receipt and next review. On repeat, re-read trial conversion, contact suppression, reply and meeting state before suggesting another touch.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks scope, event coverage, CRM join, contact gates and unsent state. Fictional [observed trial](examples/trial-usage-observed.json) supports an [unsent draft](examples/trial-sales-draft.json); [unknown coverage](examples/trial-usage-unknown.json) supports [needs review](examples/trial-sales-needs-review.json). [False inactivity](examples/invalid-trial-usage-inactive.json) and [blocked contact claimed as draft](examples/invalid-trial-sales-draft.json) fail. The reporting dashboard separates observed use, unknown coverage, no contact, draft review, approved send and provider-observed outcomes.

## Guardrails

Do not infer intent from one event, treat partial coverage as inactivity, select a person from a tenant name, or assume a trial signup permits outreach. Installation does not email, update CRM, mark conversion, book a meeting or activate recurrence. Require owner review of exact recipient and message before any separately authorized send.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route and approval design.
- [Team and handoffs](references/team-and-handoffs.md) for joins and state rules; [setup](SETUP.json) has ten checks.

## Completion contract

Return Crew IDs, source map, validated artifacts, manual case and owner decision, provider outcome or unknown, next review, activation choice and blockers. Keep customer contact and recurrence off until reviewed.
