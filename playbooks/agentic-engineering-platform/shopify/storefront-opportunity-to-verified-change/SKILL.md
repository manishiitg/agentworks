---
name: storefront-opportunity-to-verified-change
description: Propose two Shopify Crews to turn a sourced storefront opportunity into a merchant-reviewed catalog change and verified result.
---

# Storefront Opportunity to Verified Change

## Outcome

Turn one observed shopper problem into a precise product or variant correction, a merchant decision, a verified publish state, and a comparable result or an explicit inconclusive finding.

## When to use

Use for a Shopify product-page or variant issue in one store and market. A public-page observation supports a hypothesis; measured conversion claims need an authorized report.

## Discovery and user direction

Inspect existing Crews, storefront, catalog and inventory authority, analytics access, merchant goal, and publish owners. Propose reuse or creation and show the plan before Builder applies it. Installation copies guidance and pending `SETUP.json`; it activates nothing.

## Required inputs

Bind store, market, currency, language, buyer task, URL, product and variant IDs, owner, and observation time. For a measured goal, agree on source, event definition, segment, numerator, denominator, baseline window, and follow-up window. Qualitative-only is valid.

## Plan and AgentWorks tools

Reuse or propose Shopify Growth Analyst and Catalog & Merchandising Analyst. Builder may use `create_crew` with stable keys for missing Crews. Growth writes `shopify-growth-opportunity/v1`; run `scripts/validate_handoff.py --opportunity-only <opportunity.json>` before Catalog reads it. Catalog rechecks the exact ProductVariant, inventory authority, and market storefront, then writes `catalog-change-review/v1` with current and proposed values. Validate both artifacts. Show the exact diff to the merchant, then use a separately authorized publish route only if approved. Growth checks what shipped before comparing a follow-up report.

## Knowledge and persistence

Save store, market, product, variant, opportunity, action, approval, publish, retest, and report IDs. Preserve before and proposed values, source times, metric rules, owner decisions, and prior results. Re-read the variant before any repeat edit.

## Validation and reporting

Run the [handoff validator](scripts/validate_handoff.py) on real artifacts. The fictional [opportunity](examples/shopify-growth-opportunity.json) and [catalog review](examples/catalog-change-review.json) pass; the [wrong-variant published review](examples/invalid-catalog-change-review.json) fails. The reporting dashboard shows opportunities, pending reviews, published and retested changes, comparable results, inconclusive results, blockers, and run cost. Structure checks cannot prove causal lift.

## Guardrails

Selection never publishes a product, collection, theme, or campaign or spends budget. Require merchant approval, exact authorized diff, provider receipt, and later storefront retest. Do not call a changed count uplift when the market, segment, metric, or denominator differs. A schedule may refresh a read-only queue but grants no write permission.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for goal and route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for field contracts and [setup](SETUP.json) for ten customer checks.

## Completion contract

Return Crew IDs, source map, validated artifact paths, manual run, merchant decision, exact publish and retest state, comparable or inconclusive measurement, activation choice, and blockers. Keep recurrence off until the manual route is reviewed.
