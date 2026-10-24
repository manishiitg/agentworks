# Order Operations Coordinator

This skill gives one Crew the Order Operations Coordinator capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Bind store or business, order, fulfillment and shipment IDs, time zone, promised date, customer policy, and owner.
2. Read current order, payment, inventory, fulfillment, carrier and prior-contact state from authorized sources.
3. Classify delayed, incomplete, duplicate, disputed, and already resolved cases using the customer’s thresholds and source timestamps.
4. Propose a bounded next action and draft a customer update only when the exact recipient and policy are known.
5. Review one representative exception with the owner before any refund, fulfillment, inventory, or contact write.

Deliver **An exception queue with exact order and shipment IDs, current source states, promised dates, policy rule, owner, customer contact state, and safe next action.** A carrier label is not delivery; separate created, accepted, in-transit, delivered, and unknown states with provider IDs.

## Source probe and setup acceptance

Read one real exception by exact order, payment, fulfillment and carrier IDs, plus promised time and prior customer contact. Record source timestamps and whether the carrier has accepted the parcel rather than only a label being created.

Classify a paid order with a created label and no acceptance scan as pickup unverified, with a bounded owner next step. The setup remains pending if it asserts delivery or loss, refunds, reships, or contacts the customer without current state, policy and approval.

## Follow-through

Re-read order, payment, shipment and prior-contact state; retain case and action IDs and avoid a second refund, shipment, or message for the same issue.

## Fictional worked example

Fictional input: order=ord-88; fulfillment=ful-12; promised=2026-09-25; carrier=track-44; policy=late-orders-v2; owner=ops-lead.

Fictional output: order ord-88 paid, fulfillment ful-12 label created 2026-09-23, carrier track-44 has no acceptance scan by promised date; status=pickup unverified, not delivered; owner=warehouse-lead; next=confirm handoff with provider by 16:00Z; customer draft=unsent; sources=order export and carrier event.

Inadequate: “The package is lost; refund the customer now.” Reason: A missing carrier scan does not prove loss, and the refund lacks current payment state, policy, approval, and receipt.

## Automation handoff

Order Watchdog can consume order-exception/v1 with business, order, fulfillment, shipment, source status, policy, owner and action ID. Shopify-specific Store Operations remains a separate store-focused skill. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not refund, reship, cancel, mark delivered, alter inventory, or message a customer without exact approval and provider receipt. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.
