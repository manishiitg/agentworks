---
name: signed-deal-to-onboarding-handoff
description: Propose a verified Sales to Customer Success handoff for one signed SaaS deal, with an explicit receiving-owner acceptance.
---

# Signed Deal to Onboarding Handoff

## Outcome

Give Customer Success the purchased scope, agreed first result, target date and owner for one signed account. Acceptance requires current contract and entitlement checks; it does not prove onboarding or first value.

## When to use

Use before New Customer to First Value when Sales and CS have distinct owners or access. For self-serve accounts, start onboarding from an authorized subscription source.

## Discovery and user direction

Builder inspects existing Sales and CS Crews, CRM, agreement, entitlement and onboarding route. Propose reuse or reviewed creation and each Crew's access. Ask for missing owner or policy choices. Installation leaves setup pending.

## Required inputs

Bind tenant, CRM account, opportunity, customer account, executed contract/revision, purchased scope, first-value goal/date, Sales and CS owners, entitlement rule, current sources and prior handoff keys. Conflicting CRM notes stop acceptance.

## Plan and AgentWorks tools

Workflow steps: Sales reads CRM and agreement, emits `sales-cs-handoff/v1`, then a blocking validator runs. CS re-reads contract and entitlement, emits `onboarding-acceptance/v1`, and validates the pair. The receiving owner accepts or returns it with a reason. An accepted artifact may feed New Customer to First Value, whose validators still apply.

## Knowledge and persistence

Keep stable handoff key, IDs, source revisions, first-value rule, decisions, validation and prior handoffs. Re-read sources before retrying. Changed contract or owner starts a new review.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks execution, identity, revision, scope, provisioning, duplicate keys, entitlement, owner receipt and chronology. Fictional [accepted](examples/onboarding-acceptance.json) and [needs resolution](examples/needs-resolution.json) cases pass; [false acceptance](examples/invalid-onboarding-acceptance.json) fails. The reporting dashboard separates pending, needs resolution and accepted, with blockers and owner.

## Guardrails

Closed Won is not an executed agreement. Do not import unsupported promises, provision, message, create tasks or activate recurrence from installation. Provisioning, CRM writes and contact require separate checks, approval and receipt.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for scope and chronology rules; [setup](SETUP.json) for ten pending checks.
- [Sales handoff](examples/sales-cs-handoff.json), [accepted review](examples/onboarding-acceptance.json), [blocked review](examples/needs-resolution.json), and [rejected review](examples/invalid-onboarding-acceptance.json) are fictional contract examples.

## Completion contract

Return Crew IDs, source map, validated artifacts, CS decision, blockers, real manual case and activation choice. Only accepted handoffs enter onboarding.
