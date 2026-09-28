---
name: refund-request-to-reconciled-outcome
description: Propose a sourced customer refund decision and a separate finance reconciliation of the observed outcome.
---

# Refund Request to Reconciled Outcome

## Outcome

Take one customer refund request from exact payment and policy review to a finance impact record. The first result is a reviewed proposal; an actual refund is an optional, separately approved action. If no refund has happened, the finance result must say pending, denied, or blocked rather than reconciled.

## When to use

Use for a SaaS customer refund where support, billing and finance records must agree. Billing Operations Coordinator holds the Refund Review pack; Revenue & Close Analyst owns the ledger treatment. Reuse compatible Crews. Split identities when money permissions, owner, or independent review require it.

## Discovery and user direction

Inspect existing Crews, exact provider account and mode, customer request, payment, prior refunds, refund policy, finance source, and owners. Ask for missing facts and show a concrete plan before Builder configures anything. A customer request does not authorize a provider write or customer message.

## Required inputs

Bind entity, customer, request ID, charge or payment ID, provider account and mode, currency, original amount, previous and pending refunds, requested amount, policy version, billing reviewer, finance owner, and authoritative ledger or export. Preserve unknowns and conflicting sources.

## Plan and AgentWorks tools

Create steps for Billing review, blocking validation, and Finance review. Billing emits `refund-decision/v1` with exact minor-unit arithmetic and an owner decision. Validate it before handing it to Revenue & Close. Finance re-reads the payment, refund and ledger sources and emits `refund-reconciliation/v1`. An optional approved refund action sits between reviews, with exact payment, amount, reason, idempotency key, current-state recheck, and provider receipt. A draft reply or approval alone never becomes a refund or ledger posting.

## Knowledge and persistence

Keep stable request, payment, refund and customer IDs; provider account and mode; policy revision; amounts and currency; source observation times; owner decisions; action keys and receipts; ledger references; and next review. On repeat, re-read payment and refund state before proposing another action.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks identity joins, amount arithmetic, approval and receipt evidence, and finance state. The fictional [decision](examples/refund-decision.json) and [reconciliation](examples/refund-reconciliation.json) pass; an [invalid decision](examples/invalid-refund-decision.json) fails. The dashboard separates pending review, denied, approved but unprocessed, provider processed but unreconciled, and reconciled cases. It must display missing source coverage and owner decisions.

## Guardrails

No refund, charge adjustment, customer message, or ledger write follows from installation. Keep source records within authorized access. Do not treat a pending refund as settled cash, infer success from a request or approval, or retry a refund without checking the original idempotency key and provider result.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoff contract](references/team-and-handoffs.md) for state and receipt rules; [setup](SETUP.json) has ten checks.

## Completion contract

Return bound Crew IDs, exact request and payment scope, source map, validated decision and reconciliation artifacts, owner approvals, provider and ledger receipts or none, one manual-case result, next review, and activation choice. Keep recurrence off until separately reviewed.
