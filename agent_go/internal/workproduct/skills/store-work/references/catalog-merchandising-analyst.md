# Catalog & Merchandising Analyst

This skill gives one Crew the Catalog & Merchandising Analyst capability. It can seed a new Crew or support a compatible existing Crew without replacing its identity. Work through the merchant's current Shopify and adjacent SaaS records.

## First useful result

1. Confirm store, market, language, collection and product scope, canonical product rules, and owner.
2. Join product and variant IDs to storefront URLs, inventory, price, images, attributes, collection placement, and search presentation.
3. Flag missing or inconsistent facts only when observed; distinguish a deliberate merchandising choice from a data defect.
4. Prioritize issues by affected product and buyer task, with source references, proposed correction, owner, and acceptance criteria.
5. Retest the storefront after an approved edit and preserve unresolved or rejected issue IDs.

Deliver **A product and variant issue queue with exact IDs, observed storefront effect, proposed edit, owner, and retest.** Show exact product and variant IDs, market, observed page or export field, and observation time. Do not infer sales impact without a defined analytics source.

## Source probe and decision rules

Read a scoped Product and its ProductVariants, inventory source, market-specific storefront URL, and collection/search presentation. Compare the exact variant shoppers can choose with the inventory and price source for the same market.

- Separate product-level facts from variant-level facts; a sold-out variant does not prove the entire product is unavailable.
- Confirm market, currency, language, publication, and inventory authority before proposing a price, availability, or copy edit.
- Rank an issue by the buyer task and observed defect. Call revenue impact a hypothesis unless measured with a defined analytics source.

## Illustrative output and rejection

Fictional good output, illustrating the minimum facts and next action. Replace every value with authorized merchant evidence:

```json
{"issue_id":"catalog-24","product_id":"product-81","variant_id":"variant-81-m","market":"US","observed":"Size M selectable on product page; inventory export shows zero available","source_refs":["shopify:variant-81-m@2026-09-24T10:00Z","storefront:/products/linen-shirt?variant=81-m@2026-09-24T10:05Z"],"proposal":"Merchant to verify inventory sync and availability display","owner":"merch-owner","retest":"Same market and variant after approved change"}
```

Fictional output to reject: “Hide the product; it is out of stock and loses revenue.” Reject: only one variant was checked, availability authority is unresolved, and revenue loss was not measured.

This example does not prove setup. Show one real product and variant ID, the storefront result in a named market, the inventory authority, a merchant-reviewed correction, and a same-variant retest rule.

## Follow-through

Recheck the same product and variant IDs after changes; close only issues with observed fixes and avoid reopening an owner-rejected choice without new evidence.

## Automation handoff

For Storefront Opportunity to Verified Change, consume validated `shopify-growth-opportunity/v1` for the same store, market, product, and variant; emit `catalog-change-review/v1` with current/proposed value, source proof, owner approval state, publish receipt, and retest. An independent catalog audit may still produce `catalog-issue-queue/v1`. The Workflow must provide the schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; a merchant reviews source truth and consequential decisions.

## Boundaries

Do not change product copy, pricing, availability, collections, or inventory without merchant review and a separately authorized write route. Installing this skill enables no schedule, trigger, function, Automation, customer message, refund, or store write. Never copy another Crew's credentials or another store's data.
