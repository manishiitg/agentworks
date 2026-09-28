---
name: dispute-to-reconciled-outcome
description: Propose a sourced payment-dispute review and separate provider and finance outcome verification.
---

# Dispute to Reconciled Outcome

## Outcome

Track one dispute through evidence review, approved submission, provider outcome and finance reconciliation. These are distinct states.

## When to use

Use for an authorized dispute with a known account, payment, deadline, owner and finance source. Start with one manual case.

## Discovery and user direction

Inspect Finance Crews and Dispute Review, current case, payment, prior submissions, relevant sources, owner policy and ledger. Propose Crew reuse or creation and show the route before Builder applies it. Selection leaves setup pending.

## Required inputs

Bind legal entity, provider account and mode, dispute, payment, customer, amount and currency, case revision, reason, response deadline with timezone, source IDs, evidence privacy rule, submission approver and finance owner. Identify the authoritative source for the provider outcome and principal and fee ledger entries.

## Plan and AgentWorks tools

Use Billing Operations Coordinator with Dispute Review to emit `dispute-case/v1`. Set the Crew step's JSON `context_output` and a structural `validation_schema`. Run the bundled business validator as a blocking step before Revenue & Close Analyst consumes the exact artifact. Finance emits `dispute-finance-review/v1`; validate that pair. Stage evidence and request an owner decision before a separate approved submission route. A current processor receipt, not the draft or approval, proves submission.

## Knowledge and persistence

Save the provider case revision, payment and account IDs, source references, evidence packet hash, decision, submission idempotency key, provider receipt, outcome revision, ledger IDs and next review time. Re-read case status and deadline before every submission attempt or follow-up; deduplicate against earlier provider submissions.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks scope, evidence and deadline presence, submission and outcome receipts, finance status and ledger joins. Fictional [pending case](examples/dispute-case-pending.json) and [pending finance review](examples/dispute-finance-pending.json), [submitted case](examples/dispute-case-submitted.json) and [awaiting finance](examples/dispute-finance-awaiting.json), and [closed loss](examples/dispute-case-closed-lost.json) with [ledger review](examples/dispute-finance-reconciled.json) pass. [Invented submission](examples/invalid-dispute-case.json) and a [premature finance win](examples/invalid-dispute-finance-won.json) fail. The reporting dashboard separates evidence needed, awaiting approval, submitted, under review, provider decided, and finance reconciled, with source links and costs.

## Guardrails

Do not submit evidence, upload sensitive customer data, contact a customer, post a journal or enable recurrence through installation. A missing usage log cannot prove non-use or use. Do not call a dispute won from a submitted packet, and do not call it reconciled from provider status alone. Exact evidence and amount require owner review; provider and ledger states need independent source records.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route and approval design.
- [Team and handoffs](references/team-and-handoffs.md) for the state contract and [setup](SETUP.json) for ten checks.

## Completion contract

Return Crew IDs, exact case/source map, validated artifacts, one manual case result, owner decision, actual provider and finance states or explicit unknowns, next review, activation choice and blockers. Keep provider writes and recurrence off until separately reviewed.
