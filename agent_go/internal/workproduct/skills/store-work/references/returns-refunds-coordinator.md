# Returns & Refunds Coordinator

This skill gives one Crew the Returns & Refunds Coordinator capability. It can seed a new Crew or support a compatible existing Crew without replacing its identity. Work through the merchant's current Shopify and adjacent SaaS records.

## First useful result

1. Confirm exact store, order, customer and request identity, affected line items, currency, dates, and the policy version that applies.
2. Read fulfillment, delivery, existing return, capture, refund, credit, dispute, gift-card, and prior-contact states from authorized sources.
3. Check eligibility, deadline, item condition evidence, and remaining refundable amount under the store policy; mark tax, shipping, fees, and partial payments for owner verification.
4. Prepare a decision brief that separates confirmed facts, policy interpretation, unresolved money questions, and the requested owner approval.
5. Draft a customer-safe response without claiming that a refund, replacement, label, or message has already been sent.

Deliver **A source-linked return or refund review with eligibility, amount-to-verify, owner decision, and an unsent customer response.** Link exact order, transaction, refund, return, tracking, and ticket IDs. A draft or approval is not a processor receipt; report an issued refund only from a confirmed transaction record.

## Source probe and decision rules

Read one scoped Order, captured OrderTransaction, existing Refund/return records, affected fulfillment line item, policy version, and matching helpdesk request. Prove they belong to the same store/order/customer; do not substitute ticket text for a payment receipt.

- Distinguish a customer asking for a return in helpdesk from an actual Shopify Return object. Only a fulfilled item is eligible for a Shopify return; an unfulfilled item needs the merchant-approved order edit, cancellation, or refund route instead.
- Calculate remaining refundable capture in integer minor units and one currency; leave the proposed amount null until tax, shipping, discounts, partial capture, prior refunds, and disputes are verified.
- An owner approval is not execution. Count a refund, label, or customer message only after the relevant provider receipt and later source check.

## Illustrative output and rejection

Fictional good output, illustrating the minimum facts and next action. Replace every value with authorized merchant evidence:

```json
{"case_id":"case-507","order_id":"order-4102","request_kind":"helpdesk_refund_request","affected_line_fulfillment":"unfulfilled","route":"refund_or_order_edit_review","captured_minor":8500,"already_refunded_minor":0,"proposed_refund_minor":null,"approval":"pending","customer_message":"unsent","source_refs":["shopify:transaction-602","helpdesk:case-507","policy:returns-policy-2026-08"],"next_evidence":"Verify shipping and tax treatment with the approval owner"}
```

Fictional output to reject: “Shopify return approved and $85 refunded.” Reject: the item is unfulfilled, no Shopify Return was confirmed, amount treatment is unresolved, and no refund transaction receipt exists.

This example does not prove setup. Show a real request matched to one order and affected line, classify helpdesk request versus Shopify Return, calculate or explicitly defer a refundable amount, and record a pending owner decision with unsent draft.

## Follow-through

Re-read the current order, return, refund, dispute, and prior-message state before another touch; keep a stable case ID and never propose the same refund twice.

## Automation handoff

For Order Exception to Resolution, consume the validated `store-order-exception/v1` for the same store and order, then emit `return-resolution-review/v1` with policy check, money verification, owner decision, unsent draft, and proof needed for closure. The Workflow must provide the schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; a merchant reviews source truth and consequential decisions.

## Boundaries

Do not issue a refund, create a return label, cancel an order, promise a delivery date, or send a customer message without reviewed authorization and a verified tool route. Installing this skill enables no schedule, trigger, function, Automation, customer message, refund, or store write. Never copy another Crew's credentials or another store's data.
