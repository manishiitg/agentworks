---
name: billing-operations-coordinator
description: Review subscription billing, overdue invoices, failed payments, refund requests, and disputes with source-linked next actions.
---

# Billing Operations Coordinator

Use this skill for the customer's billing and payment exception queue. It can be this Crew's primary role or a supporting capability. Keep an existing Crew identity intact when added later.

## Setup in chat

Read `templates/billing-operations-coordinator/TEMPLATE_SETUP.json` and `SETUP.md`. Work through the pending checks with the owner. Record a check ID in `completed_steps` only after its instruction is verified. Preserve the checklist and prior progress. A decision to keep an optional route in chat or to skip recurrence completes that optional check. Report what is ready, blocked, and next after each setup turn.

## Build the first exception queue

1. Confirm the business, billing provider and account, live or test mode, reporting window, timezone, currencies, and the authoritative invoice/payment source. An authorized export is enough. Do not assume Stripe, Paddle, Chargebee, email, or CRM is connected.
2. Read the owner's invoice terms, failed-payment retry policy, refund policy, dispute ownership, customer-contact rules, and approval limits. Ask for missing decisions; do not invent a policy.
3. Match records by stable customer, subscription, invoice, payment, charge, refund, and dispute IDs. Keep invoice status separate from payment status. Check data freshness, duplicates, partial payments, credits, cancellations, and prior messages before calling an invoice overdue or recommending contact.
4. Make one row per actionable case: type, source IDs/links, customer, amount and currency, due or response deadline, current status, last known contact/action, evidence, proposed next step, owner/approver, and uncertainty. Sort by deadline and owner policy. Put missing data in a separate verification queue.
5. For a refund request, show the original payment, previous refunds, remaining refundable amount, reason, policy match, and proposed approver. For a dispute, show the response deadline and evidence gaps. Draft a customer message only when the contact policy supports it; label it as a draft.
6. Ask the owner to review a representative queue and correct statuses, policy, and priority before marking the first-result check complete.

## Fictional worked example

Input: Example SaaS Inc's September billing export has charge `ch_example_001` for USD 100.00. Provider records show USD 10.00 previously refunded and no additional refund issued at the 08:00 UTC cutoff. A customer requests USD 25.00 back. The owner's refund policy is referenced by `docs/fictional-refund-policy.md#section=2`; whether this request meets the policy still needs owner review.

First result: `case-refund-001` in `billing-exception-queue/v1` cites the exact charge, export row, policy, cutoff and Finance owner. It shows 10,000 original minor units − 1,000 previously refunded − 2,500 proposed = **6,500 minor units remaining after the proposal**. Current action state is **not issued**; the next step is owner review of the USD 25.00 proposal and a fresh provider check. Do not mix this refund with an overdue invoice or imply that queue creation moves money. On a later run, re-read the charge and refund list and retain the case ID.

## Inadequate output to reject

“Refund the full USD 100.00 now and tell the customer it is done.” Reject: it ignores the prior refund, exceeds the remaining balance, lacks policy approval and a current provider check, and claims an executed refund without a receipt.

This fictional example does not complete setup. Reproduce one case from an authorized billing account or export and save the owner's review.

## Boundaries

The default outcome is analysis and reviewed drafts. Do not retry a charge, change a subscription or invoice, send a message, issue a refund, submit dispute evidence, or write to an accounting system just because this template is installed. Each action requires a separately selected account, narrow permission, exact-object approval, and a recorded result. Verify current provider state again immediately before an approved action; use idempotency where the provider supports it. A provider's automated dunning may already be active, so avoid duplicate recovery messages.

Schedules, event triggers, Crew functions, and multi-Crew Automations are separate setup decisions. A single Crew can review a queue on a schedule; add an Automation when distinct Crews need a verified handoff.

## Finance Operations Review handoff

When a reviewed Finance Operations Review Automation requests a handoff, follow the artifact contract supplied in that Crew step and emit bounded `billing-exception-queue/v1` JSON. The step should provide the output path and fields; ask Builder to repair the route if it does not. Use real source IDs and observation times, stable case IDs, minor-unit amounts, and the current action state. For refunds, include original, previously refunded, proposed, and remaining amounts. The Workflow runs the blocking validator before Finance Analyst consumes the file. A valid JSON shape does not replace checking provider records or approving a refund.

## Subscription receivable handoff

For a reviewed Subscription Receivable to Verified Outcome route, install or verify Invoice Chasing or Failed Payment Recovery. Emit only the `receivable-review/v1` JSON requested by the Crew step for one exact invoice. Include the source revision, cutoff, total less credits and prior successful collections, current due, retry and prior-contact state, policy, approval and provider delivery receipt only if a reminder was actually sent. The Workflow validates this before Finance Analyst consumes it. Re-read the invoice and suppression state before any separately approved contact.
