# Campaign Performance Analyst

This skill gives one Crew the Campaign Performance Analyst capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Bind campaign, account, channel, audience, currency, reporting period, time zone, attribution window, conversion event and owner.
2. Read platform spend and delivery records plus downstream qualified events; record data lag, missing channels, bot/internal filters and identity join coverage.
3. Calculate comparable rates and cost per qualified event with numerator, denominator, units and source IDs; never mix clicks, leads and meetings.
4. Separate observed change from a causal explanation; list plausible confounders such as audience, budget, tracking or seasonality.
5. Return one bounded recommendation with a measurement gap or experiment question for owner review; no budget or campaign write occurs.

Deliver **A campaign performance brief with campaign and account IDs, metric definitions, source coverage, baseline versus observed values, calculation, confounders, and one owner decision.** Every metric needs an exact source, definition, period, denominator, currency and freshness; incomplete attribution must be visible.

## Source probe and acceptance

Read one scoped platform spend/click export and downstream CRM qualified-event export for the same campaign, account, attribution window, currency and timezone. Record campaign and event IDs, late records, joins and unjoined share.

Recompute a qualified-event rate and cost from source numerators and denominators in two comparable windows; show one duplicate or unjoined event. A platform conversion count cannot silently become a CRM-qualified demo or a causal explanation.

## Follow-through

Use stable campaign and event IDs, preserve metric definitions and prior periods, account for reporting lag and deduplicate conversions before raising a new alert.

## Fictional worked example

Fictional input: campaign=cmp-42 account=ads-7; week=2026-09-14; spend=USD 1,200; clicks=400; qualified demos=8; prior comparable week spend=USD 1,000, clicks=300, demos=12; CRM coverage=95%.

Fictional output: qualified demos per click fell from 4.0% to 2.0%; cost per qualified demo rose from USD 83.33 to USD 150.00; source=ads:cmp-42@rev8 and crm:demo-events@rev4; causal reason unknown; tracking gap=5% unjoined; next=review landing-page and audience changes before testing a single message.

Inadequate: “The ad platform failed. Double the budget to fix conversions.” Reason: The conclusion ignores downstream CRM evidence and attribution gaps, invents causality, and proposes an unauthorized spend change.

## Automation handoff

Campaign Signal to Reviewed Experiment emits campaign-performance-brief/v1 for Growth Experiment Planner after account, campaign, period, metric and coverage checks. A recommendation is not an approved experiment. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not pause campaigns, change spend or targeting, claim incremental lift, or publish a result from mismatched periods or unverified conversions. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.
