# Payment to order handoff

Payment Operations owns transaction facts; Store Operations owns the order/fulfillment decision. Both artifacts share store, order, case, transaction, and presentment currency. A checkout or customer name alone cannot establish the order identity.

## `payment-exception/v1`

Required: `artifact_type`, `store_id`, `order_id`, `case_id`, `transaction_id`, nullable `parent_transaction_ref`, `observed_at`, `transaction_kind`, `transaction_status`, nonnegative integer `amount_minor`, three-letter `currency`, `payment_policy_version`, `owner_id`, `source_refs`, and `proposed_action`. Preserve Shopify's exact transaction kind/status, and label gateway evidence separately. Authorization reserves funds but is not capture. A Refund object alone does not prove the buyer received money; inspect the related OrderTransaction status.

## `payment-order-decision/v1`

Required: matching identity, `observed_at`, `fulfillment_order_ids`, `order_payment_state`, `fulfillment_state`, `decision` (`hold`, `release_review`, or `needs_information`), `approval_state` (`pending`, `approved`, `rejected`), `action_state` (`prepared`, `executed`, `verified`), `owner_id`, `source_refs`, `next_evidence`, and nullable `approval_ref`, `action_receipt_ref`, `verification_ref`. `release_review` is allowed only when the producer transaction is CAPTURE or SALE with SUCCESS in this route. Executed or verified release requires a durable owner approval and provider receipt; verified also requires a later order/fulfillment check. Hold and needs-information remain read-only decisions here.

## Repeats and other routes

Re-read the exact transaction and order before another action. Stop a stale release proposal when a new void, refund, dispute, or failed transaction appears. For manual payment, authorization-only fulfillment, and abandoned checkout recovery, Builder must design a separate merchant-approved rule. See [Shopify OrderTransaction](https://shopify.dev/docs/api/admin-graphql/latest/objects/ordertransaction) and [refund transaction status](https://shopify.dev/docs/api/admin-graphql/latest/connections/OrderTransactionConnection).
