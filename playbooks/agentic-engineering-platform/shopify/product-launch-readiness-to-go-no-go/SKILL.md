---
name: product-launch-readiness-to-go-no-go
description: Propose a Catalog and Growth preflight for one Shopify product, publication, market, and reviewed launch decision.
---

# Product Launch Readiness to Go/No-Go

## Outcome

Produce a sourced product readiness check, an observed buyer-journey check, a merchant go/no-go decision, and post-publish proof when a launch occurs.

## When to use

Use before publishing one product and variant to one market or channel. Run a separate artifact per item in a larger launch wave; aggregate only after item-level checks.

## Discovery and user direction

Inspect existing Crews, product/variant records, inventory and price authority, Publication, approved preview or public page, target buyer, launch standards, and owner. Propose reuse or creation and show the plan before Builder applies it. Selection copies pending `SETUP.json` and publishes nothing.

## Required inputs

Bind store, market, launch, product, variant, Publication ID, currency, target time, merchant standards, buyer task, and publish owner. State any missing preview or source access.

## Plan and AgentWorks tools

Reuse or propose Catalog & Merchandising Analyst and Shopify Growth Analyst. Builder may use `create_crew` with stable keys. Catalog checks fields, price, inventory or preorder rule, media, and publication and emits `launch-catalog-readiness/v1`. Validate with `scripts/validate_handoff.py --catalog-only <catalog.json>` before Growth reads it. Growth tests the authorized preview or public buyer path, emits `launch-storefront-decision/v1`, and validates the pair. A go review requires no catalog blockers and a passing journey. Show the exact publish action and owner decision. A separate authorized route publishes; re-read Publication and storefront before reporting verified.

## Knowledge and persistence

Save store, market, launch, product, variant, publication, action, approval, provider receipt, and retest IDs. Preserve source times, blocker decisions, target time, and prior publication state. Re-read before repeat or publish.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks item identity, blockers, go-review eligibility, approval, and receipts. The fictional [catalog check](examples/launch-catalog-readiness.json) and [journey decision](examples/launch-storefront-decision.json) pass; the [invalid go/publish claim](examples/invalid-launch-storefront-decision.json) fails. The reporting dashboard shows blocked, pending review, approved, published, verified, and unknown states with sources and cost. Structure checks cannot prove actual shopper reachability.

## Guardrails

Do not publish a product, alter price or stock, spend campaign budget, or contact buyers through selection. Require exact merchant approval, authorized publish route, provider receipt, and same-market storefront retest. A scheduled reminder does not grant publish permission. Treat inaccessible previews as unknown, not passed.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for goal and route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for field contracts and [setup](SETUP.json) for ten merchant checks.

## Completion contract

Return Crew IDs, source map, validated artifact paths, manual run, blocker and owner decision, publish/retest state, activation choice, and unresolved evidence. Keep recurrence off until the manual route is reviewed.
