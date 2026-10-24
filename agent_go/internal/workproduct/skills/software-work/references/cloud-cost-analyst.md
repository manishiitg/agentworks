# Cloud Cost Analyst

This skill gives one Crew the Cloud Cost Analyst capability. It can seed a new Crew or be added to a compatible existing Crew without replacing its identity. Use the customer's existing systems as sources and approved action destinations.

## First useful result

1. Confirm account and service scope, currency, billing calendar, allocation policy, committed discounts, and comparison periods.
2. Reconcile billed and usage data by service, region, resource, and tag; separate price, usage, one-time charge, and allocation changes.
3. Check ownership and service criticality before suggesting a rightsizing, schedule, storage, or commitment change.
4. Produce a ranked candidate list with estimated savings range, source calculation, operational risk, owner, and validation experiment.
5. Request review before changing infrastructure or purchasing commitments; verify realized savings against the same billing rule later.

Deliver **A cost-change brief with reconciled period comparison, affected services, accountable owners, and reviewable savings candidates.** Show the cost arithmetic, period coverage, currency, discounts and exclusions. An estimate is not realized savings.

## Fictional worked example

Fictional input: account A-9, compute service, USD, equal 30-day windows 2026-08-02–08-31 and 2026-09-01–09-30 with unchanged discount policy. Baseline export E-8 shows 10,000 eligible compute-hours at $1.20 = $12,000. Current export E-9 shows 12,000 hours at $1.20 = $14,400 plus a separately labeled $300 one-time support charge; billed total $14,700. Resource map M-2 names the platform lead.

Reviewable output: **cost increase $2,700 (22.5%)**. Usage effect: 2,000 additional hours × $1.20 = $2,400; one-time charge: $300; price effect: $0; total explained: $2,700. Candidate C-1: inspect an idle nonproduction worker before any schedule change; estimated saving range $250–$600/month requires peak-load and dependency evidence. Owner: platform lead. Decision: review pending. Realized savings: **unknown** until an approved change and a comparable bill arrive.

## Inadequate output to reject

“September costs $2,700 more, so shut down the worker and book $600 saved.” This fails because the candidate’s usage and service risk are unverified and an estimate is not a billed saving.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Follow-through

Track candidate IDs and owner decisions; compare actual billed results after an approved change and report when savings cannot yet be observed.

## Automation handoff

For Cost Anomaly to Verified Savings, emit `cloud-cost-review/v1` at the Crew step path for one exact account, service, resource, environment, currency and billing basis. Include equal comparison windows, source IDs, recomputable usage/price/one-time effects, a bounded candidate saving range, risk, owner and next check. The Workflow validates this before Engineering Delivery Coordinator consumes it. A candidate is a proposal; no infrastructure change or realized saving follows from this handoff. The Workflow must provide the artifact schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; source truth still needs review.

## Boundaries

Do not stop resources, resize infrastructure, purchase commitments, or book savings in finance without owner approval and a verified change route. Installing this skill enables no schedule, trigger, function, Automation, notification, or system write. Never copy another Crew's credentials.
