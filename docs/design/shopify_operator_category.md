# Shopify operator category

Status: local templates and Builder proposals; merchant connections, real setup, and activation remain customer-specific. This category makes existing Shopify and adjacent apps usable by a Crew acting as a store operator. It does not replace Shopify, a helpdesk, a returns platform, or an analytics product.

## Jobs and ownership

| Merchant job | Crew | First useful artifact | Next handoff |
| --- | --- | --- | --- |
| Resolve delayed, split, or stuck order work | Store Operations Coordinator | Line-level order exception queue, owner, deadline, source IDs | Returns & Refunds Coordinator when there is a customer money or return request |
| Review a return, cancellation, or refund request | Returns & Refunds Coordinator | Eligibility and money review, pending owner decision, unsent reply | Owner approval, separately authorized action, source verification |
| Fix discoverability and purchase facts | Catalog & Merchandising Analyst | Exact product/variant issue queue with observed storefront effect | Merchant-reviewed catalog edit and retest |
| Improve product discovery and conversion | Shopify Growth Analyst | Sourced opportunity and bounded measurement plan | Catalog & Merchandising Analyst for a product/variant issue |
| Resolve an order payment exception | Payment Operations Investigator | Exact transaction kind/status and owner-safe exception | Store Operations Coordinator for a hold or release review |
| Review an abandoned checkout for contact | Checkout Recovery Coordinator | Consent and suppression-aware decision with an unsent draft when eligible | Merchant owner review; separate approved send and linked-order verification |
| Decide whether to reorder a supplier item | Replenishment Planner | Item/location demand and supplier terms with recomputed quantity and cost | Procurement owner; separate PO, supplier confirmation and linked transfer receipt |

The seven installable Shopify Playbooks cover order/return resolution, storefront opportunity to catalog change, location-aware inventory, payment-to-order decision, a bounded product launch go/no-go, [checkout signal to reviewed recovery](../../playbooks/agentic-engineering-platform/shopify/checkout-signal-to-reviewed-recovery/SKILL.md), and [inventory risk to reviewed replenishment](../../playbooks/agentic-engineering-platform/shopify/inventory-risk-to-reviewed-replenishment/SKILL.md). They reuse seven Crews in different pairings. A generic Website Growth Crew may help with public-page content, but it does not gain Shopify product, order, or customer access through category membership.

## Further Shopify jobs to validate with merchants

These are **candidates**, not installable Crews. Add a separate Crew only when its owner, source access, approval boundary, and recurring decision differ from the installed roles. Otherwise add a capability pack to an existing Crew.

| Candidate job | Operator question and first output | Distinct source or decision | Likely placement |
| --- | --- | --- | --- |
| Customer service triage | “Which tickets need a reply or specialist handoff?” → sourced support queue and unsent drafts | Helpdesk identity, SLA, contact policy | Existing Customer Support category pack, Shopify-aware when order joins matter |
| Retention and lifecycle | “Which customers need a service or campaign follow-up?” → consent-aware cohort and reviewed draft | Consent, messaging platform, cohort definitions, suppression | Existing Marketing or Customer Success Crew with Shopify capability |
| Store launch and merchandising calendar | “Which launch assets are ready and what is blocked?” → release checklist with exact product, collection, theme, and channel owners | Publish calendar, content review, theme and channel permissions | Automation across Growth, Catalog, and human owner rather than a new Crew by default |

This keeps the browse category broad without presenting untested roles as ready. Merchant interviews and real runs should determine which candidate earns a reusable template next.

## Source and capability map

| Claim or action | Source to inspect | Capability test during chat setup | Stop or fallback |
| --- | --- | --- | --- |
| Order and affected item status | Scoped Shopify Order and line item | Read one authorized order and record exact store/order/line IDs and observation time | Bounded export is enough for read-only analysis; never join by customer name alone |
| Fulfillment and delivery | Shopify FulfillmentOrder/fulfillment plus warehouse/carrier evidence | Join exact affected line and provider reference, including split fulfillment | Label created is not carrier acceptance or delivery; absent external match remains unknown |
| Customer request versus Shopify Return | Helpdesk case versus Shopify Return object | Record a separate ID and state for each | A customer request is not a Return; only fulfilled items can use Shopify Return route |
| Refund and captured balance | Shopify OrderTransaction and Refund, plus payment provider when needed | Reconcile captured and already refunded amounts in one currency; check dispute and partial payment | Unknown tax, shipping, discount, store credit, or prior refund treatment leaves amount unset |
| Payment release decision | OrderTransaction kind/status and affected FulfillmentOrder | Confirm exact order/transaction and successful CAPTURE or SALE for the narrow release route | Authorization alone remains a hold; manual payment or merchant-specific auth-first policy needs its own reviewed route |
| Catalog and variant issue | Product, ProductVariant, inventory authority, market storefront | Read one product and variant and inspect the exact buyer-facing option | Do not infer that one sold-out variant makes the whole product unavailable |
| Location inventory exception | InventoryItem and InventoryLevel at a specific Location, plus market storefront | Record available and committed separately, location eligibility, oversell policy, and exact variant | Do not sum ineligible locations or treat incoming/on-hand as available |
| Product launch readiness | ProductVariant, Publication, price and inventory authority, preview or public page | Match exact store, market, product, variant, publication, and buyer task | Block go review on unresolved catalog blockers or failed/unknown buyer path |
| Traffic or conversion result | Authorized analytics export or report, with storefront observation | Verify date range, timezone, market, segment, metric definition, numerator and denominator | A public-page review gives a hypothesis only; no measured uplift claim without comparable data |

Shopify's [orders and fulfillment model](https://shopify.dev/docs/apps/build/orders-fulfillment) distinguishes orders and fulfillment work; [return management](https://shopify.dev/docs/apps/build/orders-fulfillment/returns-apps/build-return-management) queries returnable fulfilled items; [exchanges](https://shopify.dev/docs/apps/build/orders-fulfillment/returns-apps/manage-exchanges) cannot return unfulfilled items. [OrderTransaction](https://shopify.dev/docs/api/admin-graphql/latest/objects/ordertransaction) and [refundCreate](https://shopify.dev/docs/api/admin-graphql/latest/mutations/refundcreate) define money evidence. [InventoryLevel](https://shopify.dev/docs/api/admin-graphql/latest/objects/inventorylevel) tracks quantities for an item at a specific location, and [Publication](https://shopify.dev/docs/api/admin-graphql/latest/objects/Publication) scopes product visibility to a channel or catalog. [ProductVariant](https://shopify.dev/docs/api/admin-graphql/latest/objects/productvariant) connects the exact shopper option to catalog and inventory. ShopifyQL reports require particular access and API versions; the Growth Crew should accept an authorized export or another analytics source if [ShopifyQL access](https://shopify.dev/docs/apps/build/shopifyql/graphql-admin-api) is unavailable.

## Chat setup and safe actions

Each installed Crew has nine pending checks: role/skill, store scope, actual source probe, domain rules, first real output, owner review, action route, and recurrence choice. Setup records evidence from a merchant's real sources. The bundled fictional outputs teach expected quality but never complete setup. An owner can choose read-only and manual-only.

The Playbook is a **proposal**. Builder first discovers existing Crews, suggests reusing compatible ones, shows missing permissions and the proposed handoff, and asks the merchant to review the concrete roster and plan. Only the authorized Builder action can create a missing Crew or route. Connecting MCPs or skills does not grant a refund, product edit, message, or theme publish permission. Each write needs the merchant's actual provider permission, approval record, exact target and amount or diff, stable action ID, provider receipt, and later source check.

For recurring work, begin with a manual case and a source re-read. A schedule can produce read-only queues. An event route needs an authenticated Shopify delivery, persistent deduplication by webhook delivery ID, an idempotent case/action key, retry policy, and concurrency bound. [Shopify's webhook guidance](https://shopify.dev/docs/apps/build/webhooks/verify-deliveries) requires HMAC verification for HTTPS deliveries and describes duplicate deliveries. A webhook is a signal to re-read current state, not proof that a refund or delivery succeeded.

## Quality gate before public promotion

For each Crew, inspect one real authorized source probe and its first output, a counterexample it correctly rejects, owner review of any proposed action, and a repeat run against the same stable IDs. For each Playbook, validate both artifacts, deliberately fail mismatched IDs and unsupported action states, and record one manual run with actual merchant data. This release ships local templates and fixtures, not those merchant-specific proofs.
