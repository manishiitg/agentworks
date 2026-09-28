# Product launch preflight handoff

Catalog owns exact product, variant, price, stock or preorder, and Publication facts. Growth owns the authorized preview/public buyer path and go/no-go proposal. Both artifacts share store, market, launch, product, variant, and publication IDs. One artifact covers one item; a multi-item launch needs one validated pair per item before an aggregate decision.

## `launch-catalog-readiness/v1`

Required: `artifact_type`, `store_id`, `market`, `launch_id`, `product_id`, `variant_id`, `publication_id`, `observed_at`, `currency`, nonnegative integer `price_minor`, integer `available_qty`, `inventory_policy`, `publication_state`, `product_state`, boolean `media_ready`, `blockers` as a list of nonempty strings, boolean `ready_for_review`, `owner_id`, and nonempty `source_refs`. `ready_for_review: true` requires an empty blocker list and media ready, but does not itself publish. The merchant's preorder or backorder rule decides whether zero stock is a blocker. A Publication associates products and collections with a channel or catalog; record the exact target rather than assuming all markets share visibility.

## `launch-storefront-decision/v1`

Required: matching identity and currency, `observed_at`, `page_url`, `buyer_task`, `journey_state` (`pass`, `blocked`, `unknown`), `decision` (`hold`, `go_review`, `needs_information`), `approval_state` (`pending`, `approved`, `rejected`), `publish_state` (`not_published`, `published`, `verified`), `owner_id`, `source_refs`, `next_evidence`, and nullable `approval_ref`, `publish_receipt_ref`, `retest_ref`. `go_review` requires catalog ready, no blockers, and a passing buyer journey. Published/verified requires an approved go review and provider publication receipt; verified also requires a later same-market storefront retest. Approval is not publication.

## Repeats

Before publishing, re-read current ProductVariant, Publication, inventory policy, and preview. A changed price or stock state can reopen a previously passed check. After publishing, test the same market and variant and retain the receipt. See [Shopify Publication](https://shopify.dev/docs/api/admin-graphql/latest/objects/Publication) for channel/catalog publication behavior.
