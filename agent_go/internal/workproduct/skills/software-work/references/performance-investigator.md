# Performance Investigator

This skill gives one Crew the Performance Investigator capability. It can seed a new Crew or be added to a compatible existing Crew without replacing its identity. Use the customer's existing systems as sources and approved action destinations.

## First useful result

1. Agree on the user journey or endpoint, metric, percentile, units, budget, environment, traffic segment, and baseline/current windows.
2. Check measurement coverage and comparable load, device, cache, region, and release conditions before calculating a change.
3. Correlate slow spans, resource timing, errors, and deploys; keep correlation separate from a proven cause.
4. Return one or more reproducible hypotheses with exact measurement references, owner, proposed experiment, and rollback or risk consideration.
5. Define a retest using the same metric and comparable conditions; report unknown if the available observations cannot support a conclusion.

Deliver **A regression brief with comparable baseline and current measurements, reproduction conditions, likely bottleneck, and verification plan.** Show baseline and current values, sample sizes and windows when available, source links, and any confounders; never claim an improvement before a comparable retest.

## Fictional worked example

Fictional input: production GET /api/search, EU region, p95 latency in milliseconds, instrumentation v3. The approved budget is 500 ms. A comparable 24-hour baseline has p95 420 ms from 10,200 requests; the post-release window has p95 690 ms from 10,050. Traces T-8 show database span p95 110 → 350 ms, but the query plan and cache-hit mix are unavailable.

Reviewable output: **regression open**. p95 rose 270 ms, or (690 − 420)/420 = 64.3%; the current p95 exceeds the 500 ms budget by 190 ms. Evidence: APM exports B-1/C-1, trace set T-8, release SHA abc123. Hypothesis: slower database work contributed; confidence medium because traffic composition and query plan are missing. Owner: search-service lead. Next: inspect the query plan and cache mix, then retest the same route/region/segment against the 500 ms budget. No fix or measured improvement is claimed.

## Inadequate output to reject

“Latency doubled because of the deploy; increase the database budget and close the incident.” This fails the arithmetic, asserts causality from timing, changes the budget without review, and has no comparable retest.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Follow-through

Compare later runs to the recorded baseline rule; note instrumentation or traffic changes and close the regression only after a comparable retest.

## Automation handoff

A performance Workflow can consume `performance-regression/v1` with route, metric definition, baseline/current evidence, hypotheses, proposed change, and retest criteria. QA or Engineering verifies the same route and environment. The Workflow must provide the artifact schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; source truth still needs review.

## Boundaries

Do not alter production configuration, change a performance budget, deploy a fix, or treat one synthetic run as a customer-wide outcome without owner review. Installing this skill enables no schedule, trigger, function, Automation, notification, or system write. Never copy another Crew's credentials.
