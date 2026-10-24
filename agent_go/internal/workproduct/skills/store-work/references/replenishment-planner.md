# Replenishment Planner

This skill gives one Crew the Replenishment Planner capability. It can seed a new Crew or support a compatible existing Crew without replacing its identity. Work through the merchant's current Shopify and adjacent SaaS records.

## First useful result

1. Bind exact store, variant, InventoryItem, destination Location, supplier and supplier SKU; confirm merchant inventory and procurement authority.
2. Reconcile Shopify available at this location with distinct confirmed incoming units and outstanding backorders. Do not subtract committed units again from available.
3. Use a named demand window, daily-unit basis, lead time, review interval, safety stock, minimum order and case pack; mark missing or stale inputs unknown.
4. Calculate target, position, shortage and proposed quantity in integer units; round to case pack and MOQ, then calculate cost in one currency and check budget.
5. Prepare reorder, defer or needs-information review. Keep a purchase-order draft, supplier confirmation, transfer receipt and verified stock as separate later states.

Deliver **A sourced reorder or defer decision with exact inventory item and location, demand basis, incoming stock, supplier terms, rounded quantity, cost, owner and next evidence.** Cite InventoryLevel, demand export, supplier terms, open PO/transfer and budget sources with timestamps and exact IDs. A PO marked ordered is not proof that units were received.

## Source probe and decision rules

Read one authorized InventoryLevel by exact item/location ID, one same-variant demand extract, one supplier terms record, and current open PO/transfer records. Confirm which source owns lead time and incoming quantity.

- Shopify InventoryLevel is per item and location. Available is already net of committed stock; count only confirmed incoming that is not in available, and never pool another location without an approved transfer.
- Target units equal ceiling(daily demand × (lead time + review interval)) plus safety stock. Shortage equals max(0, target minus available minus confirmed incoming plus outstanding backorders). Round a positive order to the case pack and MOQ; show every assumption.
- A purchase order is a supplier agreement; a linked inventory transfer records actual receipt. Do not call ordered units available before the transfer and InventoryLevel confirm them.

## Illustrative output and rejection

Fictional good output, illustrating the minimum facts and next action. Replace every value with authorized merchant evidence:

```json
{"case_id":"stock-24","inventory_item_id":"item-81-m","location_id":"location-3","supplier_id":"supplier-4","available_qty":5,"confirmed_incoming_qty":4,"backorder_qty":0,"daily_demand_units":2,"lead_time_days":7,"review_period_days":3,"safety_stock_qty":2,"target_qty":22,"shortage_qty":13,"case_pack_qty":6,"min_order_qty":12,"proposed_qty":18,"unit_cost_minor":900,"total_cost_minor":16200,"currency":"USD","decision":"reorder_review","po_state":"not_created","owner_id":"procurement-owner"}
```

Fictional output to reject: “Order 13 units and mark them in stock.” Reject: the six-unit case pack requires 18 units, procurement has not approved a PO, and ordered units are not received stock.

This example does not prove setup. Recompute one real item/location proposal from current inventory, demand, incoming and supplier terms; show a missing-data case, an open-PO duplicate check, and a procurement owner decision.

## Follow-through

Re-read inventory, incoming transfers, open POs and supplier terms before another proposal; use a stable item-location-supplier-window key and net out prior approved orders to avoid double ordering.

## Automation handoff

For Inventory Risk to Reviewed Replenishment, consume a validated `inventory-availability-exception/v1` for the same store, market, variant, inventory item, location and case. Emit `replenishment-review/v1` with explicit demand and supplier assumptions, recomputable quantity/cost, owner decision and later PO/transfer evidence. The Workflow must provide the schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; a merchant reviews source truth and consequential decisions.

## Boundaries

Do not create or submit a purchase order, promise availability, move or receive stock, or pay a supplier without a current-state recheck, procurement approval and an authorized provider route. Installing this skill enables no schedule, trigger, function, Automation, customer message, refund, or store write. Never copy another Crew's credentials or another store's data.
