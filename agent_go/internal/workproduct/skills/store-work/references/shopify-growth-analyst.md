# Shopify Growth Analyst

This skill gives one Crew the Shopify Growth Analyst capability. It can seed a new Crew or support a compatible existing Crew without replacing its identity. Work through the merchant's current Shopify and adjacent SaaS records.

## First useful result

1. Confirm store market, buyer, product set, primary conversion action, attribution limits, owner, and reporting window.
2. Inspect public storefront journeys and authorized traffic, product, cart, checkout, and order data; align timezones, currencies, and denominators.
3. Separate qualitative usability issues from measured funnel changes. Do not treat sessions, checkouts, and orders as interchangeable counts.
4. Rank bounded page, merchandising, acquisition, or checkout hypotheses with source evidence, effort, owner, and success metric.
5. Ask for approval before theme, product, ad, or campaign changes; define a comparable post-change observation window.

Deliver **A store growth brief with measured funnel or page evidence, source limits, ranked opportunities, and a verification plan.** Cite URL or report, date range, segment, numerator and denominator where relevant, and source coverage. Unknown attribution remains unknown.

## Source probe and decision rules

Inspect one public product or collection journey and one authorized report/export for the same market and date range. Record metric definition, segment, timezone, currency, source coverage, and whether a checkout event or order is actually observed.

- A public page audit supports a usability hypothesis, not a measured conversion-loss claim.
- Use the same market, segment, event definitions, and observation windows when comparing; sessions, add-to-carts, checkouts, and orders have different denominators.
- Verify the exact approved page or product change shipped before attributing a subsequent metric move; mark low-volume or missing data inconclusive.

## Illustrative output and rejection

Fictional good output, illustrating the minimum facts and next action. Replace every value with authorized merchant evidence:

```json
{"opportunity_id":"growth-12","market":"US","page":"/products/linen-shirt","observation":"Size guide is below the buy button on mobile","measurement":"Product-page sessions 1200; add-to-cart sessions 96; 2026-09-01..14; US mobile","source_refs":["storefront:/products/linen-shirt@2026-09-24","analytics:report-44"],"hypothesis":"Make size guidance visible near variant choice","owner":"growth-owner","success_rule":"Compare US mobile add-to-cart/session over equivalent windows after verified publish"}
```

Fictional output to reject: “Checkout conversion fell 8%, so publish a new theme.” Reject: the cited count is product-page sessions, checkout denominator and comparable period are missing, and no owner approved a theme write.

This example does not prove setup. Show one real storefront observation and the exact report behind any performance claim, with market, window, metric definition, denominator, and owner-reviewed experiment rule.

## Follow-through

Track the same opportunity and experiment IDs; verify what shipped before comparing outcomes, and report inconclusive results when traffic or instrumentation is insufficient.

## Automation handoff

For Storefront Opportunity to Verified Change, emit `shopify-growth-opportunity/v1` with exact store, market, product, variant, page, observation, source refs, evidence kind, and a measured metric only when a valid numerator/denominator exists. Consume the Catalog review and verify any shipped change before measuring it. A broader strategy brief may still use `shopify-growth-brief/v1`. The Workflow must provide the schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; a merchant reviews source truth and consequential decisions.

## Boundaries

Do not publish theme changes, edit a product or campaign, spend ad budget, or claim conversion lift before a reviewed change and comparable measurement. Installing this skill enables no schedule, trigger, function, Automation, customer message, refund, or store write. Never copy another Crew's credentials or another store's data.
