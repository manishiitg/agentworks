# Checkout Recovery Coordinator

This skill gives one Crew the Checkout Recovery Coordinator capability. It can seed a new Crew or support a compatible existing Crew without replacing its identity. Work through the merchant's current Shopify and adjacent SaaS records.

## First useful result

1. Bind store, checkout ID, customer or guest identity, market, channel, observed time, and merchant contact policy version.
2. Read the checkout completed state and any later linked order; record an unresolved join as unknown rather than matching by email or cart amount alone.
3. Check channel-specific consent, opt-out, existing Shopify or provider recovery automation, prior sends, frequency cap, and merchant exclusions.
4. Classify suppress, needs_information, or draft_review. A draft requires current eligibility evidence and remains unsent until separate owner approval.
5. If an approved send route is later used, re-read state, use an idempotent checkout-channel-campaign key, retain provider receipt, and verify the later order separately.

Deliver **A checkout-level eligibility decision with exact source IDs, suppression reason or unsent draft, owner, and proof needed before any send.** Cite exact checkout, customer or guest, linked order when available, consent, policy, suppression, and provider record IDs with observation times. Keep personal contact details out of shared reports.

## Source probe and decision rules

Read one authorized AbandonedCheckout by exact ID, including completedAt, updatedAt, customer or guest reference, and recovery URL availability; inspect a current consent record, linked order state, and provider send history under the same store.

- The abandoned-checkout query can include recovered checkouts. A present checkout record does not prove it remains eligible; completed or later-order state can suppress it.
- Contact eligibility depends on current channel consent, merchant policy, jurisdiction, opt-out, and existing automation. Missing evidence means needs_information or suppress, never an automatic send.
- A provider send receipt proves delivery was attempted, not that an order was recovered. Verify a later order with a trusted checkout/order link and avoid double counting.

## Illustrative output and rejection

Fictional good output, illustrating the minimum facts and next action. Replace every value with authorized merchant evidence:

```json
{"case_id":"recovery-14","store_id":"store-example-1","checkout_id":"checkout-77","market":"US","channel":"email","completed_at":null,"later_order_state":"none_verified","consent_state":"subscribed","prior_send_state":"none_verified","decision":"draft_review","message_state":"unsent","source_refs":["shopify:checkout-77@2026-09-24T14:30Z","shopify:consent-34@2026-09-24T14:31Z","messaging:history-77@2026-09-24T14:32Z"],"owner_id":"lifecycle-owner","next_evidence":"Owner review and fresh source check before any send"}
```

Fictional output to reject: “Send a discount to every abandoned checkout; recovered revenue is confirmed.” Reject: the query can include recovered checkouts, contact consent and prior sends are unknown, no owner approved a send, and no linked order proves revenue.

This example does not prove setup. Show one real checkout with current completion/order, consent, suppression and send-history sources; demonstrate a suppressed or unknown case and obtain the owner decision on a separate unsent draft.

## Follow-through

Re-read checkout, linked order, consent, opt-out, suppression, and provider history before every contact decision; preserve the same case key and suppress duplicates or recovered checkouts.

## Automation handoff

For Checkout Signal to Reviewed Recovery, consume a validated `checkout-recovery-signal/v1` from Shopify Growth Analyst for the same store, market, checkout, channel, and campaign; emit `checkout-recovery-review/v1` with current eligibility, suppression checks, an unsent draft only when allowed, approval state, and separate send and order evidence. The Workflow must provide the schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; a merchant reviews source truth and consequential decisions.

## Boundaries

Do not enable a recovery automation, alter consent, send a message, issue a discount, or claim a recovered order from a draft, click, or unverified attribution. Installing this skill enables no schedule, trigger, function, Automation, customer message, refund, or store write. Never copy another Crew's credentials or another store's data.
