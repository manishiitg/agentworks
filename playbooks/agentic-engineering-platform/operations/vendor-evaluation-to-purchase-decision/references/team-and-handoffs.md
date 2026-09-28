# Vendor evaluation to purchase decision

## Route

1. Builder binds one legal entity and request, requirement revision, must-haves, weighted criteria, decision owner, exact candidate plans, term, seats, budget and current procurement sources. It inspects existing Crew capabilities before proposing creation.
2. Vendor Researcher records at least two exact vendor/product/plan/quote candidates. Each criterion is met, not met or unknown with a dated source; quote currency, seats, monthly and onboarding charges, usage assumptions, total term cost and validity are explicit. An unmet must-have or over-budget candidate is ineligible. An unknown must-have is pending diligence, not silently met.
3. A blocking validator checks `vendor-comparison/v1`. Spend & Payables Coordinator receives only that artifact, re-reads current vendor register, subscriptions, POs, budget, security/privacy reviews and approval policy, and emits `vendor-purchase-review/v1` for the exact selected quote. A possible duplicate or missing gate remains pending.
4. Validate the pair and show the owner the full comparison, current source revisions, gaps and proposed decision. Record a named approval receipt only after review. Approval remains separate from creating a vendor, PO, signed agreement, bill or payment; those routes re-read current state and retain provider receipts.

## Identity, evidence and repeat

Match tenant, entity, request, requirements revision, selected vendor/product/plan/quote, currency, seats, term and term total across artifacts. Preserve source IDs, quote validity, owner decision and stable case key. A repeat references the earlier case; changing plan, quote, requirements or cost basis requires a new comparison and approval. A changed vendor record or budget requires a new purchase review even if the comparison remains valid.

## Example acceptance

The fictional comparison evaluates two plans for 20 seats over 12 months. Vendor B's USD 7,260 total includes monthly seats, assumed usage and onboarding; an unresolved Vendor A region claim stays unknown. Current vendor and budget reads show no overlap and enough budget. The pending review waits for the owner; a separate exact owner receipt can mark approval without claiming a purchase. The invalid example asserts approval and PO/payment action without receipts and is rejected. Fixtures do not prove a real vendor quote, customer policy or completed purchase.
