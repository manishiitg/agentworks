---
name: invoice-intake-to-reviewed-payable
description: Propose a source-verified invoice extraction and payables review with duplicate, approval and payment-state checks.
---

# Invoice Intake to Reviewed Payable

## Outcome

Prepare one traceable vendor invoice extraction and one current payables review. Distinguish received document, validated fields, existing bill, owner approval, scheduled payment and paid state. A review is not a bill write or a payment.

## When to use

Use when a SaaS team receives vendor invoices as documents and needs an accountable payables reviewer. One Crew may hold both skills when owner and access are the same; split Crews when document privacy or AP permissions differ.

## Discovery and user direction

Inspect existing intake and payables Crews, document store, vendor and bill system, current credits and payments, duplicate rule, and approval authority. Ask for missing scope only. Show a concrete proposal before Builder applies it; selection leaves setup pending.

## Required inputs

Bind legal entity, invoice document ID/hash/version, vendor identity, invoice number, currency, dates, net, tax, total, page spans, current AP source, duplicate policy, approval owner and time zone. Keep a missing field or unavailable source explicit.

## Plan and AgentWorks tools

Reuse Document Intake Assistant and Spend & Payables Coordinator. Intake writes document-intake-record/v1. Run the bundled validator before Payables reads it. Payables re-reads current vendor, bill, credit and payment records, then writes payable-review/v1; validate the joined pair. Insert explicit validator steps in the Workflow. Any destination write needs separate exact approval, re-read, idempotency key and provider receipt. Payment remains a separate route.

## Knowledge and persistence

Save entity, document hash/version, vendor and invoice key, existing bill ID, source spans, run, reviewer, policy revision, approval, receipt and payment-state IDs. On repeat, compare the same invoice key with current AP state before suggesting another bill.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks source spans, arithmetic, document and entity joins, duplicate state, review outcome and payment evidence. Fictional [intake](examples/document-intake-record.json) and [review](examples/payable-review.json) pass; the [invalid review](examples/invalid-payable-review.json) fails. The reporting dashboard separates needs-review, existing/paid, ready-for-owner-review, approved, written and payment-scheduled cases with source coverage.

## Guardrails

Do not infer vendor identity from an unverified logo, fill missing tax values, erase a duplicate, create a vendor or bill, change bank details, approve a bill or pay it through installation. Escalate changed bank details to independent owner verification.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for invoice and payment states; [setup](SETUP.json) has ten checks.

## Completion contract

Return Crew IDs, source map, validated artifacts, duplicate and owner decisions, write and payment receipts or none, manual run, activation choice and blockers. Keep recurrence off until reviewed.
