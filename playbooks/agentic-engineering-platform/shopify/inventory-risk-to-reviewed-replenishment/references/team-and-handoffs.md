# Replenishment handoff and calculation

Catalog owns the exact item/location stock observation. Replenishment Planner owns demand, supplier and purchasing review. The outputs share store, market, product, variant, InventoryItem, Location and case IDs. Each artifact pair covers one supplier and destination location. The existing [inventory availability route](../../inventory-availability-to-owner-action/references/team-and-handoffs.md) handles display and sync corrections; this route handles a procurement decision.

## `inventory-availability-exception/v1`

Use the existing Catalog contract: exact identity, `observed_at`, integer `available_qty` (negative permitted), nonnegative `committed_qty`, buyer-facing state, inventory authority, owner and source references. Shopify [InventoryLevel](https://shopify.dev/docs/api/admin-graphql/latest/objects/inventorylevel) is per InventoryItem and Location. Available already reflects commitments; do not subtract `committed_qty` again. Do not pool a second location without an approved transfer.

## `replenishment-review/v1`

Match identity and carry `supplier_id`, `supplier_sku`, `currency`, `terms_version`, `observed_at`, `decision` (`needs_information`, `defer`, `reorder_review`), `owner_id`, `source_refs`, `next_evidence`, `approval_state` (`pending`, `approved`, `rejected`), `po_state` (`not_created`, `draft`, `ordered`), and `stock_state` (`not_received`, `received`). For a computable review, require matching `supplier_quote_currency` and `budget_currency`; nonnegative integers `demand_units`, `demand_window_days` (>0), `confirmed_incoming_qty`, `open_po_incoming_qty`, `backorder_qty`, `lead_time_days`, `review_period_days`, `safety_stock_qty`, `min_order_qty`, `unit_cost_minor`; positive `case_pack_qty`; and calculated integers `target_qty`, `shortage_qty`, `proposed_qty`, `total_cost_minor`. `open_po_state` is `none_verified`, `accounted_in_incoming`, `unknown`, or `unaccounted`; the last two block a calculation. Count open-PO units within confirmed incoming only once. `available_qty` must equal Catalog's observation. References must include inventory, demand, incoming/open-PO, supplier terms and budget review. Missing inputs use `needs_information` with null calculated values; do not fabricate a zero-demand decision.

Calculate `target_qty = ceil(demand_units × (lead_time_days + review_period_days) / demand_window_days) + safety_stock_qty`. `shortage_qty = max(0, target_qty - available_qty - confirmed_incoming_qty + backorder_qty)`. Available already excludes committed units. If shortage is zero, `defer` and proposed quantity zero. Otherwise `reorder_review` proposes `ceil(max(shortage_qty, min_order_qty) / case_pack_qty) × case_pack_qty`. `total_cost_minor = proposed_qty × unit_cost_minor`; quote and budget currency must match. Case pack and MOQ come from current supplier terms, not a previous PO.

Approval is not a PO. A PO draft needs approval, a provider draft reference and pre-action source recheck. `ordered` also needs supplier confirmation. `received` needs an ordered PO, linked transfer receipt and later same-item/location InventoryLevel verification. Shopify [purchase orders](https://help.shopify.com/en/manual/products/inventory/purchase-orders/creating-purchase-orders) record the commercial agreement; [linked transfers](https://help.shopify.com/en/manual/products/inventory/purchase-orders/creating-inventory-transfers) record movement and receipt. Supplier payment is outside this route.

## Repeats

Re-read the same item/location, demand period, incoming transfer, open PO and supplier terms. Deduplicate by item-location-supplier-window and preserve rejected or already ordered decisions. An event or schedule is a cue to recheck sources; it never supplies purchase authority.
