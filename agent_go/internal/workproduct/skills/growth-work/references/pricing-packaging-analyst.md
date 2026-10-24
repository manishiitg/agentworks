# Pricing & Packaging Analyst

This skill gives one Crew a GTM capability. It can seed a new Crew or be added to a compatible Crew. Customer source systems and owner policies remain authoritative.

## First useful result

1. Bind product, market, currency, plan versions, buyer segment and pricing decision owner.
2. Reconcile displayed price, billing catalog, entitlements and current contract exceptions by exact version.
3. Compare buyer evidence with cost, margin and sales exceptions; show unknown demand elasticity rather than inventing it.
4. Prepare keep, test or change options with implementation and customer-communication implications for owner review.

Deliver **An offer decision memo with exact plan versions, entitlement differences, buyer fit, cost and margin assumptions, current contracts, owner options and risks.** Record source IDs, revisions, observed times, coverage, owner and unknowns.

## Source probe and acceptance

Read one public price revision, corresponding billing price and entitlement IDs, two eligible buyer or deal records and a current cost policy. Show any mismatch and one protected contract exception.

Recompute unit economics using stated currency and billing period, preserve grandfathered terms and separate a hypothetical package from an approved offer. Missing cost or buyer coverage blocks a margin conclusion.

## Fictional worked example

Fictional input: plans=team-v3,business-v2; public price=page-v5; billing prices=price-31,price-44; currency=USD/month; owner=pricing-lead.

Fictional output: memo=offer-8; team-v3 public page and price-31 match; business-v2 page lists export entitlement absent from billing catalog; unit cost missing for large accounts; recommendation=resolve entitlement and cost before a package test; owner decision=pending; price change=none.

Inadequate: “Raise Business price 20% tomorrow; everyone will pay.” Reason: It ignores source mismatch, contract terms, cost coverage, buyer evidence and approval.

## Automation handoff

GTM Strategy Analyst or Sales Follow-up Coordinator may consume gtm-offer-decision/v1 only with approved plan and price versions, buyer segment, limitations and owner decision; seller quotes need current account terms. Builder must validate the exact artifact identity and schema before another Crew consumes it.

## Later run

Compare exact price, entitlement and contract revisions on each review; log changed assumptions and do not reapply a proposed offer to existing contracts.

## Boundaries

Do not alter a billing price, entitlement, contract or public page, quote a customer, or apply a discount without Finance, Product and pricing-owner review. Installation enables no schedule, trigger, function, Automation or external write.
