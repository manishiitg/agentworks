---
name: subscription-receivable-to-verified-outcome
description: Review one subscription receivable across Billing and Finance Crews, with exact invoice evidence, policy-safe contact and verified collection state.
---

# Subscription Receivable to Verified Outcome

## Outcome

Take one overdue invoice or failed subscription payment from a sourced case review to a truthful open, partial, collected or deposited result.

## When to use

Use when Billing must decide a next step and Finance must independently verify the money state. Use Finance Operations Review for a period-wide exception queue. One Crew can handle a simple chat-only reminder review.

## Discovery and user direction

Inspect existing Crews, selected Invoice Chasing or Failed Payment Recovery pack, source access, policy and the Automation. Propose Crew reuse, exact case, handoff, cost and blockers. Selection is a proposal; record actual setup evidence in `SETUP.json`.

## Required inputs

Resolve entity, provider account and live/test mode, customer, invoice, subscription if present, currency, due date, cutoff, retry/contact rules, suppression and approval owner. Read current billing and finance records; label unavailable evidence.

## Plan and AgentWorks tools

Use distinct Billing Operations Coordinator and Finance Analyst Crews. The Billing step emits `receivable-review/v1`; a blocking validator checks balance arithmetic and contact evidence. The Finance step receives its bounded artifact, re-reads exact provider state, and emits `receivable-outcome/v1`; a second validator checks the invoice join and claimed payment/deposit proof. Reuse ready Crews; create missing Crews or add a billing pack only after owner review. Run one case manually first.

## Knowledge and persistence

Save stable case and invoice IDs, provider account/mode, source times, approved contact action key, receipts, Crew IDs, validation results and next check. Re-read prior contact and payment state before repeating; deduplicate events by provider event ID.

## Validation and reporting

Test both validators against real artifacts and a rejected fixture. The dashboard separates amount due, observed new collection, and verified deposit. Report invoice, payment, approval and contact states with run IDs and source gaps. A valid JSON reference is not proof that the provider record is true.

## Guardrails

Do not retry a charge, send a reminder, write off a balance, post a ledger entry or enable recurrence from Playbook selection. A customer message needs separate exact-action approval, current suppression check and delivery receipt. A successful payment is not a payout or bank deposit. Check disputed invoices against policy before contact.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md): goal, route and schedule decisions.
- [Team and handoff contract](references/team-and-handoffs.md): artifact fields, validation and manual route.
- [Decision and repeat cycle](references/action-and-repeat.md): suppression, approvals, collection and deposit states.
- [Artifact validator](scripts/validate_handoff.py): deterministic exact-invoice checks.
- [Worked case](examples/receivable-review.json), [finance outcome](examples/receivable-outcome.json), [verified deposit](examples/deposited-outcome.json), [invalid case](examples/invalid-receivable-review.json), and [false deposit](examples/invalid-receivable-outcome.json): fictional contract samples.
- [Setup progress](SETUP.json): ten evidence-backed checks.

## Completion contract

Return Playbook version, owner choices, actual Crew IDs and setup state, source map, Workflow step IDs, artifact and validator paths, manual run and dashboard links, case decision, collection/deposit limits, activation state and unresolved work.
