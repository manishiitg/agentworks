---
name: spend-payables-coordinator
description: Review vendor bills, subscriptions, company card charges, and expenses against approval policy with a source-linked payables queue.
---

# Spend & Payables Coordinator

Use this skill to organize what the business owes and what it has spent. It is a separate Crew when vendor, card, or payment permissions differ from finance analysis. It may also be a supporting capability in a Crew with the same approved owner and access.

## Setup in chat

Read `templates/spend-payables-coordinator/TEMPLATE_SETUP.json` and `SETUP.md`. Verify each check with the owner before adding its ID to `completed_steps`. Preserve existing progress and an existing primary Crew identity. Chat-only or no-recurrence is a valid completed decision for optional checks. Report verified work, exceptions, and the next pending check.

## Produce the first payables queue

1. Confirm entity, period, currency, vendor owner, bill approval and payment policy, budget owners, and duplicate criteria. Obtain authorized bill, card, expense, or vendor exports; a live connection is optional. Identify which system is authoritative for bill status and whether payment is already scheduled.
2. Extract bill ID, vendor identity, invoice number, invoice date, due date, amount, currency, tax fields present, PO/contract reference, receipt, category, approval status, and payment status. Preserve the original document and source link. Treat a receipt, card transaction, and vendor invoice as different records until matched.
3. Check exact and near duplicates using vendor, invoice number, amount, currency, dates, and document evidence. Check credits, partial payments, recurring subscriptions, missing receipts or POs, unusual amounts, policy limits, and expenses coded to the wrong period. Flag uncertainty rather than accusing a vendor or employee.
4. Produce a queue with one row per bill or exception: source IDs, due date, amount/currency, approval owner, current state, policy reason, proposed next step, and evidence gap. Separate **ready for owner review**, **needs more evidence**, and **already paid/scheduled**. Do not represent an uncoded bill as an approved payment.
5. Summarize spend by vendor/category and compare to a supplied budget only when periods and definitions match. Ask the owner to review sample matches, classifications, and priorities before completing setup.

## Fictional worked example

Input: document `invoice-document-88` version `v1`, hash `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`, claims vendor `vendor-42`, invoice `INV-88`, USD 108.00. Intake `intake-88`, AP snapshot `ap:vendor-42@rev-7`, and policy `policy:ap-v3` refer to the same fictional entity. The AP search finds no exact duplicate at 10:10 UTC; approval is pending. No current payment source or provider receipt is supplied.

First result: review `review-88` links the document hash and version to the intake and AP snapshot. It records duplicate_state **none within the checked AP scope**, approval_state **pending**, disposition **ready_for_owner_review**, action_state **none**, USD 108.00, policy reference and `ap-owner`. Ask the owner to confirm invoice fields, required evidence and current paid/scheduled status before an approval proposal. Keep document intake, AP review, approval and payment as separate states. On a later run, recheck the AP revision and duplicate search while retaining the document and review IDs.

## Inadequate output to reject

“INV-88 is approved and paid because it appears once in the AP export.” Reject: one export match does not prove approval, current payment state or a provider receipt. Do not create a bill or initiate a payment from this review.

This fictional example does not complete setup. Reproduce a bill against authorized AP and payment-status sources and save the owner's decision.

## Purchase decision from a vendor comparison

For Vendor Evaluation to Purchase Decision, receive only a validated `vendor-comparison/v1` with exact request, requirements revision, vendor/product/plan/quote, term, seat count, currency, total cost, criterion evidence and unresolved must-haves. Re-read the current vendor register, existing subscriptions, open purchase orders, budget, security/privacy disposition and approval policy for the same entity. Produce `vendor-purchase-review/v1` with the selected quote and cost unchanged, a dated duplicate result, gates, owner and pending or recorded decision. A possible duplicate, expired quote, unknown must-have, security or privacy blocker, or missing budget source remains pending.

Fictional input: comparison `comparison-demo-1` recommends `vendor-b / support-team / quote-q17` for 20 seats and 12 months at USD 7,260 including onboarding. Current vendor register `vendors@rev-8` has no exact vendor or overlapping subscription; budget `budget@rev-3` has USD 8,000 available; security and privacy owners have recorded cleared states. Output: purchase review `purchase-demo-1` is ready for the named budget owner's decision, with `purchase_action_state=none`. A later owner approval may be recorded with an exact receipt but does not sign a contract, create a PO or pay a vendor. Reject “approved and purchased because the quote is under budget”: price alone does not prove policy approval or provider action.

## Boundaries

The default skill reads and prepares decisions. Do not create a vendor, sign terms, create a PO or bill, change bank details, approve an expense, initiate a payment, or post ledger entries from template installation. Purchase and payment execution require separate authorized routes, current duplicate checks, exact approvals and provider receipts. Payment also needs verified payee details; changed bank details go through the owner's independent verification process. A schedule or Automation needs separate setup and testing.
