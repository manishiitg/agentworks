# Shopify Playbook library review

## Later library extension

Checkout Signal to Reviewed Recovery adds a sixth Shopify Crew and Playbook locally. It pairs a minimal Shopify Growth Analyst signal with Checkout Recovery Coordinator's exact checkout, linked-order, consent, suppression, and prior-send review. Its validator and fictional cases check that unknown contact eligibility cannot become an eligible draft, a send requires owner approval and provider evidence, and an order claim needs a trusted checkout/order link. The original five-route review below remains the dated assessment. No merchant account or message route was activated by this extension.

Inventory Risk to Reviewed Replenishment adds a seventh Shopify Crew and Playbook locally. Its procurement Crew recomputes a one-location target, shortage, case-pack/MOQ quantity and cost from explicit demand and supplier inputs. The validator rejects an unaccounted open PO, premature PO/receipt claims and location mismatches. Fictional examples and tests do not verify a supplier's actual terms or merchant stock; the original review remains historical.

Date: 2026-09-26. Scope: local `feat/shopify-playbook-library-20260926` branch. The new library is not deployed. Dominion currently runs the prior four-Crew/two-Playbook Shopify release.

## Coverage

| Merchant decision | Playbook | Crews | Source-specific stop rule |
| --- | --- | --- | --- |
| Resolve a delayed order with a return/refund request | Order Exception to Resolution | Store Operations → Returns & Refunds | An unfulfilled line cannot take a Shopify Return route; money claims need transaction proof. |
| Correct buyer-facing variant friction | Storefront Opportunity to Verified Change | Shopify Growth → Catalog & Merchandising | A qualitative observation is not a conversion claim; a published edit needs approval and a retest. |
| Reconcile stock and availability | Inventory Availability to Owner Action | Catalog & Merchandising → Store Operations | Inventory is per item and location; no write without authority, owner approval, and receipt. |
| Decide what an order's payment permits | Payment Exception to Order Decision | Payment Operations → Store Operations | Authorization is not capture; this narrow release route requires a successful CAPTURE or SALE transaction. |
| Publish a product to a market | Product Launch Readiness to Go/No-Go | Catalog & Merchandising → Shopify Growth | Catalog blockers or a failed/unknown buyer journey block go review; approval is not publication. |

The fifth Crew, **Payment Operations Investigator**, has its own transaction source probe and nine pending chat checks. The four previously released Shopify Crew v1 packages remain byte-for-byte unchanged; the new Playbooks provide route-specific instructions when they reuse those Crews. This avoids silently changing an installed v1 skill under the same version.

## Quality checks added

- All five Shopify Playbooks have a manifest, ten pending setup checks, a concise Builder skill, a domain-specific handoff reference, fictional valid/invalid artifacts, and a Python validator.
- The package validator now runs nine executable contract suites across the Playbook library, including all five Shopify handoffs. It also validates the 33 package manifests and skill structure.
- The frontend build checks that its 33 Playbook catalog entries match manifest identity, version, category, order, inputs, tools, slots, handoffs, and setup IDs. Shorter UI descriptions remain editorial text.
- Backend installation checks cover the new Shopify packages with pending setup; frontend and Builder Crew tests cover the fifth template.

## Remaining boundary

These Playbooks are Builder proposals. The Python validator is bundled and exercised by the release gate, but generic Workflow Crew steps do not yet invoke it automatically before a consumer Crew runs. Builder must place an explicit validator step in each applied route and retain its result. No merchant Shopify account, payment gateway, supplier, or storefront preview was connected during this work, so customer-specific setup and business outcomes remain unverified.

The next runtime improvement should make validated artifact handoffs a first-class Workflow step: bind a producer artifact path, run the package validator, stop the consumer on failure, and save producer/consumer run IDs and validation evidence. This is the main readiness gap before calling the library self-service.

## Candidate additions after merchant validation

1. Abandoned checkout recovery with protected customer data, current order state, marketing consent, suppression, and provider-confirmed send. Keep this separate from payment exceptions.
2. Supplier replenishment planning with purchase-order, lead-time, and multi-location inventory authority. The current inventory Playbook intentionally stops at a reviewed action.
3. Shopify-aware customer support triage as a capability of the Customer Support category when ticket ownership and contact policy differ from Returns & Refunds.

Sources for key boundaries: [Shopify InventoryLevel](https://shopify.dev/docs/api/admin-graphql/latest/objects/inventorylevel), [OrderTransaction](https://shopify.dev/docs/api/admin-graphql/latest/objects/ordertransaction), [Publication](https://shopify.dev/docs/api/admin-graphql/latest/objects/Publication), and [AbandonedCheckout](https://shopify.dev/docs/api/admin-graphql/latest/objects/AbandonedCheckout).
