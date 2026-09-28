---
name: vendor-evaluation-to-purchase-decision
description: Propose a sourced exact-plan vendor comparison and separate owner-reviewed purchase decision.
---

# Vendor Evaluation to Purchase Decision

## Outcome

Compare exact vendor plans against one approved requirement revision and total-cost basis, then prepare a current purchase decision. A shortlist is not procurement approval or a purchased service.

## When to use

Use when a business is choosing among vendors and needs a separate budget or procurement owner to review the choice. A standalone Vendor Researcher suffices for early exploration. A single bill already received belongs in Invoice Intake to Reviewed Payable.

## Discovery and user direction

Builder inspects existing Vendor Researcher and Spend & Payables Coordinator Crews, their owners and access. Agree on the buyer need, candidate set, legal entity, decision owner, source freshness and action boundary. Propose reuse, exact handoff and a manual case before configuration. Selection starts no vendor contact or purchase.

## Required inputs

Bind one request ID, requirement revision, must-haves, weights, region, exact plan and quote revisions, seat count, term, common currency, all known fees, budget, current vendor/subscription/PO source, security/privacy policy and approver. Unknown quote or gate evidence stays unknown.

## Plan and AgentWorks tools

Workflow steps: Vendor Researcher cites dated plan, quote and criterion sources and emits `vendor-comparison/v1`. Its cost arithmetic and must-have states must pass the [validator](scripts/validate_handoff.py) before Payables reads it. Spend & Payables Coordinator re-reads current vendor, overlapping commitments, budget and policy records; emits `vendor-purchase-review/v1` bound to the selected exact quote. Validate the pair. Ask the named owner to review or return blockers. Any vendor creation, PO, signature, payment or external message needs a separate approved action and provider receipt.

## Knowledge and persistence

Keep request and entity IDs, requirement revision, source and quote revisions, cost assumptions, gate decisions, case key, owner receipt and provider IDs. Repeat reads current offers, budget and vendor state; an expired quote or changed plan requires a new comparison.

## Validation and reporting

The validator checks exact request and plan identity, quote validity, criterion citations, must-haves, total-cost arithmetic, budget, fresh duplicate search, security/privacy gates, approval receipt and action boundary. Fictional [comparison](examples/vendor-comparison.json), [pending purchase review](examples/vendor-purchase-pending.json), [blocked review](examples/vendor-purchase-blocked.json), [approved decision](examples/vendor-purchase-approved.json) and [invalid claim](examples/invalid-vendor-purchase-review.json) show the contract. Dashboard separates sourced shortlist, unresolved diligence, owner approval and actual purchase.

## Guardrails

Do not treat marketing claims as compliance proof, convert unknown cost to zero, assume quotes are current, or treat a low price as approval. A prior vendor account may still have an overlapping subscription or PO. Do not share customer data with candidates, accept terms, change bank details or pay through this proposal.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choices.
- [Team and handoffs](references/team-and-handoffs.md) for exact comparison, current gates and repeat rules.
- [Setup](SETUP.json) for ten pending checks; examples are fictional.

## Completion contract

Return Crew IDs, exact source and rule map, validated artifacts, cost and gate gaps, owner decision or pending review, real manual case, blockers and activation choice.
