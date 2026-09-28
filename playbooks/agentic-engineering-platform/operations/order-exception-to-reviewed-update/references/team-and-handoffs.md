# Order exception team and handoffs

Installation copies a Builder proposal and a pending checklist. It does not connect commerce, carrier or helpdesk providers or authorize a message, refund or reship.

## Route

1. **Operations:** Read the exact order, payment, fulfillment, shipment and prior-contact sources. Emit `order-exception/v1` for one business, order, customer and support case. A label without a carrier acceptance scan is `pickup_unverified`, not delivered or lost.
2. **Validate:** Run `python3 scripts/validate_handoff.py order <order-artifact-path>` as a blocking Workflow step. A Crew attachment alone does not validate the artifact.
3. **Support:** Re-read the current case, recipient, channel, prior contact and customer-facing policy. If contact is warranted, prepare a clearly unsent `order-customer-update-review/v1` linked to the exact order artifact; otherwise record `no_message` and why. Run `python3 scripts/validate_handoff.py update <order-artifact-path> <update-artifact-path>` before owner review.
4. **Act separately:** An owner reviews the exact message and recipient. Re-read case and order state, check duplicate sends, then use a separately authorized provider route with an idempotency key. A send receipt does not prove delivery of goods or case resolution.
5. **Observe:** Re-read carrier and case records. Record acceptance, delivery, refund and resolution only from their respective authoritative sources and receipts.

For fictional fixtures:

    python3 scripts/validate_handoff.py order examples/order-exception.json
    python3 scripts/validate_handoff.py update examples/order-exception.json examples/order-customer-update-review.json
    python3 scripts/validate_handoff.py update examples/order-exception.json examples/invalid-order-customer-update-review.json

The last command must fail. Replace example paths with installed Workflow artifact paths. Builder must check customer source truth; the validator cannot authenticate provider records.

## Stable joins and claims

Order and update artifacts share business, order, customer and case IDs. Operations also records payment, fulfillment and shipment IDs, source revision and observation time. The source payment and fulfillment records must name that order, and the carrier record must name that shipment. Support cites the exact order artifact and current case revision; the case source must name the same order and customer. An email address, display name or order amount is not an identity join.

`label_created` and `not_accepted` support only `pickup_unverified`. A provider carrier acceptance event supports `carrier_accepted`; an in-transit event supports `in_transit`; a delivered event supports `delivered`. Unknown source state remains unknown. The draft's `claim_state` cannot advance beyond the source-observed exception state. A pending or failed payment cannot be described as captured. The route's output remains `unsent` with `approval_state: pending` until a separate owner decision and action route.

## Repeat and approval

Use `business_id + order_id + case_id + exception_state` as the investigation key and include the message fingerprint in any later contact key. Re-read exact source state before retrying or scheduling. An unchanged exception should update its case history, not create another draft or message. Owner approval, provider send receipt, customer response and resolved case status are separate records.
