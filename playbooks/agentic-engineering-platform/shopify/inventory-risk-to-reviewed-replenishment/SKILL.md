---
name: inventory-risk-to-reviewed-replenishment
description: Propose a location-specific Shopify stock risk to a supplier-aware, reviewed reorder decision.
---

# Inventory Risk to Reviewed Replenishment

## Outcome

Give a procurement owner one sourced, recomputable reorder or defer decision for an exact inventory item, location and supplier. Separate a later purchase order from supplier confirmation, physical transfer and received available stock.

## When to use

Use when the merchant must decide whether and how much to buy from a supplier. Use Inventory Availability to Owner Action for a display or sync mismatch without purchasing. Handle one item/location/supplier in each artifact pair.

## Discovery and user direction

Find existing Catalog & Merchandising Analyst and Replenishment Planner Crews, inventory authority, demand reports, supplier terms, open POs/transfers, budget policy, and procurement owner. Ask for the chosen demand window and safety stock. Show the route before Builder applies it. Selection copies pending setup; it orders nothing.

## Required inputs

Bind store, market, product, variant, InventoryItem, destination Location, supplier/SKU, time window, available and distinct incoming units, backorders, demand units and days, lead and review days, safety stock, MOQ, case pack, unit cost and currency. Missing assumptions yield `needs_information`.

## Plan and AgentWorks tools

Catalog emits `inventory-availability-exception/v1` for the exact item/location; validate it using the existing inventory contract or `scripts/validate_handoff.py --signal-only <signal.json>`. Replenishment Planner re-reads incoming, open POs, supplier terms and demand, then emits `replenishment-review/v1`; validate the pair before procurement review. Calculate target and shortage in integer units, round to pack/MOQ, and calculate cost in minor units. A separate approved route may create a PO and later record transfer receipt. A webhook triggers a re-read, not a purchase.

## Knowledge and persistence

Keep item-location-supplier-window case and action IDs, source times, demand definition, incoming/PO deduplication, terms version, owner decision, PO/supplier confirmation, transfer receipt, and location-level stock verification. Re-read before repeats to prevent double orders.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks identity, formulas, pack/MOQ rounding, cost, evidence pointers, approval and later PO/transfer claims. The [valid](examples/replenishment-review.json) and [invalid](examples/invalid-replenishment-review.json) fictional artifacts exercise it. The reporting dashboard separates needs information, defer, reorder review, PO draft, ordered and received states. A valid artifact does not prove current supplier promises or physical stock.

## Guardrails

Do not create, submit or pay a PO, promise availability, receive stock, or adjust InventoryLevel through installation. Require exact procurement approval and current-state recheck for action. A PO marked ordered is a supplier agreement; only a linked transfer and inventory source check support received stock.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for formula and evidence fields, and [setup](SETUP.json) for merchant checks.

## Completion contract

Return Crew IDs, source map, validated artifact paths, manual case, recomputed quantity and cost or missing inputs, owner decision, separate PO/transfer state, and activation choice. Preserve unresolved supplier or stock evidence.
