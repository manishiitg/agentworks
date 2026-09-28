# Inventory exception handoff

Catalog owns the exact buyer-facing variant and InventoryLevel observation. Store Operations owns reconciliation, owner action, and retest. The two outputs must share store, market, product, variant, inventory item, location, and case IDs. A location total cannot stand in for a market-eligible quantity.

## `inventory-availability-exception/v1`

Required: `artifact_type`, `store_id`, `market`, `product_id`, `variant_id`, `inventory_item_id`, `location_id`, `case_id`, `observed_at`, integer `available_qty`, integer `committed_qty`, `storefront_state`, `inventory_authority_ref`, `owner_id`, nonempty `source_refs`, and `proposed_action`. Negative available stock is meaningful and must not be clamped. Incoming and on-hand may be documented separately; do not label them available. The first run can be read-only from authorized exports plus the public storefront.

## `inventory-action-review/v1`

Required: matching identity fields, `observed_at`, `decision` (`reconcile_sync`, `replenishment_review`, `storefront_update_review`, or `needs_information`), `approval_state` (`pending`, `approved`, `rejected`), `action_state` (`prepared`, `executed`, `verified`), `proposed_action`, `owner_id`, `source_refs`, `next_evidence`, and nullable `approval_ref`, `action_receipt_ref`, `verification_ref`. An executed or verified action requires owner approval and a provider receipt; verified also requires a later same-variant/location source check. An approval is not a stock adjustment or purchase order.

## Repeats

Before a second adjustment, re-read the exact InventoryLevel, storefront, supplier or warehouse record, and prior action receipt. Preserve the stable case and action ID. A Shopify inventory update event is a signal to re-read, not proof that the displayed variant is fixed. See [Shopify InventoryLevel](https://shopify.dev/docs/api/admin-graphql/latest/objects/inventorylevel) for the location-specific quantity model.
