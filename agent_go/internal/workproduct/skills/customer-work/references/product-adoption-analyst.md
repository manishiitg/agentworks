# Product Adoption Analyst

This skill provides the Product Adoption Analyst capability. It can seed one Crew or be added to an existing Crew without changing that Crew's primary identity.

## First useful result

1. Confirm account identity, product entitlement, event names, success threshold, timezone, and observation window with the owner.
2. Read only the authorized usage or setup source. Map actor and account IDs carefully; exclude test, internal, duplicate, and wrong-tenant events.
3. Compare each required event with the onboarding register and report observed, not-observed-with-coverage, or unknown when access or instrumentation is incomplete.
4. Keep seat invites, sign-ins, feature use, and the agreed outcome separate. Do not turn activity into value without the owner-defined rule.
5. Produce a dated readout with source IDs, denominators where relevant, gaps, and the next verification or intervention question.
6. For a released feature, bind release/build and flag revision, eligible segment, distinct account join, exposure and use predicates. Count exposed accounts separately from eligible accounts and feature users; keep partial coverage or insufficient sample as not evaluable.
7. For a trial, bind one exact product account, tenant, trial/subscription and status revision. Deduplicate authorized events under a predeclared use rule and window; distinguish observed use, complete-window non-observation and unknown coverage. Do not infer contact permission or purchase intent.

Deliver **A first-value, feature-adoption, or trial-usage observation with exact identity, source records, coverage and next owner question.** Every observed event includes a source ID and time; feature counts cite eligibility and exposure, while trial use cites the exact trial state and event-coverage source. A missing event is not a failure when coverage is incomplete.

## First-value and feature measurement

The first-value event definition is customer-specific. Record the predicate and exclusions. Missing instrumentation returns **unknown**. For feature adoption, freeze release/build, feature flag revision, eligible segment and distinct-account rule before counting exposure and use. Compare only equal windows under the same rule; a single measured window is a baseline, not a trend or causal effect. For a trial, read current trial/subscription state separately from events, keep complete-window non-observation distinct from partial coverage, and pass only a bounded observation to Sales. A product signal does not establish a person’s consent, buying intent, or an approved follow-up. On repeat runs show late or corrected events.

## Fictional worked example

Fictional input: validated register register-example-001 defines first value as an authorized customer user exporting a project report from production before 2026-09-30 17:00 UTC. Product export product-event contains event event-example-001, “project_report_exported”, for account-example-001 / tenant-example-001 at 2026-09-25 11:00. Coverage record coverage confirms complete event collection for the 2026-09-24 through 2026-09-25 11:10 window; no test actor or duplicate is present.

Reviewable output: readout readout-example-001 links register-example-001 and rule first-report-v1. Status: **reached** at 11:00, supported by event-example-001 and coverage. The workspace milestone was already observed; the report-export milestone can now be rechecked and marked complete by the onboarding owner. Seat invites, login count, broader adoption and renewal impact remain **unknown**. Next: account owner verifies the sponsor can use the exported report.

Separate fictional feature input: release R-7 / flag f7-r2 exposed 80 of 120 eligible accounts in the named segment during a complete week; 24 exposed accounts used the feature under rule U-1. Output: 80/120 exposed (66.67%) and 24/80 used (30%); target 40% is below on this one baseline window. Cause and future retention impact are unknown.

Separate fictional trial input: trial T-42 for account A-42 / tenant N-42 is trialing under subscription S-42@r3 until 2026-10-02. Authorized export E-42 covers 2026-09-24 through 2026-09-26 10:00 UTC and contains one non-test qualifying project-created event EV-42 at 09:00. Output: trial-usage-observation U-42 records **observed use** of the owner-defined rule with exact source and coverage; purchase intent, recipient identity and contact permission remain unknown.

## Inadequate output to reject

“All 20 invited seats adopted the product because one report was exported.” Reject: one valid event proves only the agreed account predicate. Also reject “the release hurt retention” from one feature-use window and “this trial is ready to buy” from one login: neither claim follows from the product evidence.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

For New Customer to First Value, emit `first-value-readout/v1` linked to the onboarding register. For Released Feature to Adoption Decision, emit `feature-adoption-observation/v1` with exact release/flag/rule, eligible, exposed and used counts. For Trial Account to Reviewed Sales Assist, emit `trial-usage-observation/v1` with exact account, tenant, trial, subscription/status revision, rule, window, events and coverage. The Workflow validates the selected artifact before its consumer runs. The Crew step must supply schema fields and an output path; ask Builder to repair a route that omits them. A validator checks shape and references; a human checks source truth.

## Boundaries

Do not invent adoption scores, infer churn from sparse use, expose another tenant’s data, or message the customer from an analysis request. Never copy another Crew's credentials. Installing this skill enables no schedule, trigger, function, Automation, customer message, CRM write, or account action.
