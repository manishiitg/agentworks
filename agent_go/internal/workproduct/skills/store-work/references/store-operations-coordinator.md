# Store Operations Coordinator

This skill gives one Crew the Store Operations Coordinator capability. It can seed a new Crew or support a compatible existing Crew without replacing its identity. Work through the merchant's current Shopify and adjacent SaaS records.

## First useful result

1. Confirm store ID, order ID, current time and timezone, customer-safe service policy, owner, and authorized data scope.
2. Join Shopify order, payment, fulfillment, inventory, tracking, and support records by exact IDs. Keep an unmatched external tracking record unresolved.
3. Separate paid, captured, fulfilled, shipped, delivered, cancelled, returned, and refunded states; note partial items and split fulfillments.
4. Classify the exception, customer deadline, affected line items, prior contact, and the responsible operations or fulfillment owner.
5. Produce one proposed next action and evidence needed to close each case. Route return or refund requests to the policy specialist.

Deliver **An order exception queue with exact order and fulfillment IDs, current state, evidence, owner, deadline, and proposed next action.** Cite order, fulfillment, tracking, and support record IDs with observation times. A label-created shipment is not proof of delivery.

## Source probe and decision rules

Read one scoped Order with its line items and payment state, then its FulfillmentOrders/fulfillments and a matching warehouse or carrier record. Record the Shopify IDs, external ID, observed times, and which source owns each state. A missing external match stays unknown.

- Classify at the affected line-item/fulfillment level; an order can have partial or split fulfillment.
- A shipping label or tracking number is not proof of carrier acceptance or delivery. Escalate a missed service deadline using the merchant policy and a named owner.
- If the customer requests money or a return, hand over the exact case and source references; do not turn the operations queue into a refund decision.

## Illustrative output and rejection

Fictional good output, illustrating the minimum facts and next action. Replace every value with authorized merchant evidence:

```json
{"case_id":"case-507","store_id":"store-example-1","order_id":"order-4102","line_item_id":"line-1","fulfillment_ref":"warehouse:shipment-501","observed_state":"label_created; carrier acceptance unknown","source_refs":["shopify:order-4102@2026-09-24T14:30Z","warehouse:shipment-501@2026-09-24T14:28Z"],"owner":"store-ops-owner","next_action":"Ask fulfillment provider for acceptance scan before promising delivery","closure_evidence":"carrier acceptance or merchant-approved alternative"}
```

Fictional output to reject: “Order shipped and customer will receive it tomorrow.” Reject: only a label exists; there is no carrier acceptance or delivery promise source.

This example does not prove setup. Show one real order with a line-level fulfillment join, a deliberately missing or stale external tracking state, the merchant service deadline, and the named owner decision.

## Follow-through

Re-read the same order and fulfillment IDs before another action; compare new versus resolved exceptions and check prior contact or refund state to avoid duplicates.

## Automation handoff

For Order Exception to Resolution, emit `store-order-exception/v1` with store, order, line-item, payment, fulfillment, return and support references; current states; policy version; owner; and proposed action. Keep absent external IDs explicit. The Workflow must provide the schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; a merchant reviews source truth and consequential decisions.

## Boundaries

Do not cancel, fulfill, edit inventory, issue a refund, or contact a customer without an authorized action route and exact owner decision. Installing this skill enables no schedule, trigger, function, Automation, customer message, refund, or store write. Never copy another Crew's credentials or another store's data.
