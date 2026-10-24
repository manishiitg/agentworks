# Payment Operations Investigator

This skill gives one Crew the Payment Operations Investigator capability. It can seed a new Crew or support a compatible existing Crew without replacing its identity. Work through the merchant's current Shopify and adjacent SaaS records.

## First useful result

1. Confirm store, order, transaction, payment method or gateway, currency, amount, time, owner, and the merchant capture policy.
2. Read OrderTransaction kind and status, parent transaction, authorization expiry, capture/refund history, order financial state, and gateway record where available.
3. Distinguish authorization, capture, sale, void, refund, pending, and failure. Do not infer successful capture from an order total or a Refund object.
4. Classify one exception, its affected fulfillment decision, retry risk, money or contact approval owner, and exact source evidence.
5. Produce an unsent action proposal; verify a later provider transaction before saying payment was captured, voided, or refunded.

Deliver **A payment exception brief with exact order and transaction IDs, kind, status, currency, amount, affected fulfillment decision, owner, and next evidence.** Cite exact store, order, transaction and parent transaction IDs, kind, status, amount, presentment currency, provider reference, and observation time. Keep an absent gateway record unknown.

## Source probe and decision rules

Read one authorized Order and matching OrderTransaction, including transaction kind, status, parent ID, presentment amount/currency, and gateway reference. Check a provider record if the claim depends on settlement. Do not join an abandoned checkout to an order by email or amount alone.

- An authorization reserves funds but is not a successful capture. A pending or failed transaction is not evidence of payment.
- Use the exact presentment currency and verified capturable amount; multi-capture is not universally available and must be checked for the merchant and transaction.
- A Refund object does not prove returned money; inspect the associated transaction status before reporting the outcome.

## Illustrative output and rejection

Fictional good output, illustrating the minimum facts and next action. Replace every value with authorized merchant evidence:

```json
{"case_id":"pay-19","store_id":"store-example-1","order_id":"order-4102","transaction_id":"txn-602","kind":"AUTHORIZATION","status":"SUCCESS","presentment_minor":8500,"currency":"USD","capture_state":"not_observed","source_refs":["shopify:order-4102@2026-09-24T14:30Z","shopify:txn-602@2026-09-24T14:30Z"],"owner":"payments-owner","next_action":"Review capture policy and expiry before fulfillment release"}
```

Fictional output to reject: “Payment complete; ship the order.” Reject: the only confirmed transaction is an authorization, no successful capture was observed, and fulfillment release was not approved.

This example does not prove setup. Show one real order/transaction join with kind, status, amount and currency; test a pending or authorization-only case and record the payment owner decision.

## Follow-through

Re-read the same transaction and order before each retry or fulfillment decision; use a stable case/action key and stop when a later successful capture, void, dispute, or refund supersedes the proposal.

## Automation handoff

For Payment Exception to Order Decision, emit `payment-exception/v1` with exact order and transaction identity, observed kind/status and amount, owner, and proposed decision. Store Operations consumes the validated artifact and emits `payment-order-decision/v1` only after re-reading the order and fulfillment state. The Workflow must provide the schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; a merchant reviews source truth and consequential decisions.

## Boundaries

Do not capture, retry, void, refund, mark paid, release fulfillment, or contact a buyer without current state, merchant approval, an authorized route, and a provider receipt. Installing this skill enables no schedule, trigger, function, Automation, customer message, refund, or store write. Never copy another Crew's credentials or another store's data.
