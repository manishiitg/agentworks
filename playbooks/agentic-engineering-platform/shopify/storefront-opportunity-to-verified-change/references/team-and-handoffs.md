# Growth to catalog handoff

Growth owns the buyer observation and metric hypothesis. Catalog owns the exact product/variant facts, proposed merchant edit, and storefront retest. Both artifacts use stable store, market, product, variant, and opportunity IDs. One Crew can fill both slots only with compatible access and two separately validated outputs.

## `shopify-growth-opportunity/v1`

Required: `artifact_type`, `store_id`, `market`, `product_id`, `variant_id`, `opportunity_id`, `observed_at`, `page_url`, `buyer_task`, `observation`, `hypothesis`, `evidence_kind`, `metric`, `source_refs`, and `owner_id`. `evidence_kind` is `qualitative` or `measured`. A measured claim requires `metric` with a name, source, start/end dates, segment, nonnegative integer numerator, and positive integer denominator. A qualitative observation has `metric: null` and must not state a measured loss or lift. Include the exact market-specific storefront URL and ProductVariant ID rather than only a product title.

## `catalog-change-review/v1`

Required: `artifact_type`, matching `store_id`, `market`, `product_id`, `variant_id`, `opportunity_id`, `owner_id`, `observed_at`, `current_value`, `proposed_value`, `change_reason`, `approval_state`, `publish_state`, `approval_ref`, `publish_receipt_ref`, `retest_ref`, `measurement_state`, `source_refs`, and `next_evidence`. `approval_state` is `pending`, `approved`, or `rejected`. `publish_state` is `not_published`, `published`, or `verified`. Published or verified requires a durable approval reference and provider publish receipt; verified also requires a later storefront/Shopify retest reference. `measurement_state` is `not_started`, `inconclusive`, or `comparable_result`. A comparable result requires measured input plus `followup_report_ref` and `followup_metric` with the same metric name and segment, a positive denominator, and a later non-overlapping window. The validator checks these references and joins, not causal attribution.

## Owner decision and recurrence

Present the current and proposed values as a diff for the exact variant and market. The merchant chooses whether to approve it; even approval does not publish. A separate authorized route records the write receipt. Re-read current state before publishing and on every repeat to avoid overwriting later merchant edits. Compare the same segment and denominator only after verifying what shipped. Keep rejected choices closed unless new evidence appears.
