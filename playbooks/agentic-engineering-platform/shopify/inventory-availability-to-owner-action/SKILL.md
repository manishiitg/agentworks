---
name: inventory-availability-to-owner-action
description: Propose a Catalog and Store Operations handoff for a location-aware Shopify inventory exception and reviewed owner action.
---

# Inventory Availability to Owner Action

## Outcome

Produce a sourced variant/location exception, a merchant decision, and observed inventory or storefront evidence after any approved action.

## When to use

Use for a stock mismatch, stale sync, oversell risk, or availability presentation problem with a specific Shopify variant and location. Do not treat the whole product or all locations as one stock number.

## Discovery and user direction

Inspect existing Crews, inventory authority, warehouse or supplier source, storefront, policies, and owner. Propose reuse or creation and show the plan before Builder applies it. Selection copies guidance and pending `SETUP.json`; it adjusts nothing.

## Required inputs

Bind store, market, product, variant, inventory item, location, observation time, authority, oversell rule, owner, and buyer impact. Record whether the location can fulfill the chosen market.

## Plan and AgentWorks tools

Reuse or propose Catalog & Merchandising Analyst and Store Operations Coordinator. Builder may use `create_crew` with stable keys. Catalog reads ProductVariant and location-specific InventoryLevel and emits `inventory-availability-exception/v1`. Validate it with `scripts/validate_handoff.py --exception-only <exception.json>` before Operations reads it. Operations reconciles source authority and emits `inventory-action-review/v1`. Validate the pair, present the exact action and current value to the merchant, then use a separate authorized route for any stock or storefront write. Re-read the same location and buyer-facing page after execution.

## Knowledge and persistence

Save stable store, market, product, variant, inventory item, location, case, action, approval, and provider IDs. Keep source times, old and proposed values, owner decisions, and prior retests. Re-read current state before repeats.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks identity, quantity types, approval, and receipts. The fictional [exception](examples/inventory-availability-exception.json) and [review](examples/inventory-action-review.json) pass; the [wrong-location executed review](examples/invalid-inventory-action-review.json) fails. The reporting dashboard shows open, pending, executed, verified, and blocked cases with source links and cost. The validator cannot establish warehouse truth.

## Guardrails

An available quantity can be negative under oversell; do not clamp it to zero. Available, committed, on-hand, and incoming are distinct states. Never add locations without fulfillment eligibility. Require owner approval, exact authorized change, provider receipt, and later source retest before calling an adjustment complete. A schedule may refresh a read-only queue but grants no write access.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for goal and route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for the fields and [setup](SETUP.json) for ten merchant checks.

## Completion contract

Return Crew IDs, authority map, validated artifact paths, manual run, owner decision, action and retest evidence, activation choice, and blockers. Keep recurrence off until the manual route is reviewed.
