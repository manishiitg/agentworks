# Funnel Analyst

This skill gives one Crew the Funnel Analyst capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Freeze product, eligible cohort, ordered stages, event versions, identity join, deduplication, consent and timezone rules before comparing windows.
2. Read exact event and paid-state sources; check freshness, identity join rate, billing lag, excluded internal/test users, and missing-source coverage.
3. Count unique eligible users who reached each stage in order; keep stage counts monotone and compute rates from a named denominator.
4. Compare equal windows on the same population and stage definitions; state observed differences and sample limits without inferring a cause from correlation.
5. Ask the owner to review one likely friction point and a source-check or experiment question; do not change signup, billing or messaging.

Deliver **A versioned funnel observation with exact cohort, identity and event rules, stage counts, a comparable change or honest first baseline, coverage gaps, and one investigation question.** Show cohort and source revisions, stage definitions, unique-user counts, denominator, identity coverage, paid-state evidence and an explicit unknown when a source is missing.

## Source probe and acceptance

Read authorized signup and activation events plus the matching subscription paid-state source for one frozen cohort. Record event versions, account join, internal/test exclusions, billing lag, timezone and missing-source coverage.

Reproduce ordered unique-account stage counts and paid/eligible rate from source IDs; stage counts cannot increase downstream. Show a duplicate and a missing-billing case, and call a single complete window a baseline rather than a trend.

## Follow-through

Preserve funnel definition and source revisions, re-read the same cohort keys and later paid events, wait for billing lag, and avoid counting users twice or comparing changed event semantics as a trend.

## Fictional worked example

Fictional input: product=ArborDesk; cohort=UK self-serve new accounts; baseline Aug 2-31 and current Sep 1-30; signup/activated/paid events v2; eligible users 1,000 versus 1,200; authorized product event and billing exports.

Fictional output: baseline signup=300, activated=180, paid=60 of 1,000 eligible users; current signup=360, activated=180, paid=48 of 1,200. Paid/eligible fell from 6.0% to 4.0%; activated/signup fell from 60% to 50%. Identity join=98%, billing current through Oct 3, cause unknown; next=review activation flow change and tracking before one bounded test.

Inadequate: “Our checkout redesign caused churn, so raise prices and email everyone.” Reason: No exact cohort, ordered counts, billing proof, comparison rule or causal evidence supports the diagnosis or unauthorized actions.

## Automation handoff

Funnel and Conversion Intelligence emits funnel-observation/v1 to Growth Experiment Planner only after exact product, cohort, stage rule, window, source and count checks. A drop-off is an observed signal, not an approved experiment. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not call a trial a paid customer, infer purchase from a click, claim causality, inspect unconsented sessions, or modify pricing, checkout or campaigns from installation. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.
