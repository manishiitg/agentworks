# Lifecycle Analyst

This skill provides the Lifecycle Analyst capability. It can seed one Crew or be added to an existing Crew without changing that Crew's primary identity.

## First useful result

1. Freeze exact product, tenant, signup cohort windows, eligible-account definition, activation event, day-30 retention predicate, identity join and timezone.
2. Read authorized product and subscription records; exclude internal/test accounts, duplicates and wrong-tenant events while recording source freshness and missing joins.
3. Separate mature eligible accounts from still-censored accounts; never count a cohort whose day-30 outcome window has not closed as churn.
4. Compute activation per eligible and retention per mature eligible with explicit numerators, denominators, policy version and comparable cohort lengths.
5. Report observed differences and alternative explanations as hypotheses, then ask the owner which instrumentation or experience question merits a bounded test.

Deliver **A source-linked cohort observation with fixed activation and day-30 retention rules, eligible and mature counts, rates, coverage, and a bounded owner question.** Show cohort IDs and windows, cutoff, rule versions, distinct source references, account-level join coverage, excluded immature counts and rate arithmetic.

## Cohort maturity and repeat rule

Keep activation and retention predicates versioned. Compare equal-duration signup cohorts at the same day-30 maturity rule; wait for late billing or event records. If a cohort is immature, return pending_maturity with null retention and no trend. If only one mature cohort exists, return baseline_first. On repeats, preserve prior cohort IDs and reopen a rate only for a sourced correction; changed event semantics start a new baseline.

## Fictional worked example

Fictional input: ArborDesk UK self-serve cohorts under lifecycle policy v2. June cohort has 200 eligible accounts and July cohort has 220, both mature at the 2026-09-02 cutoff. Product event and subscription exports are authorized, with about 98% identity join coverage. Day-7 activation means first scheduling workflow; day-30 retention means active paid subscription plus qualifying use in days 23–30.

Reviewable output: June activated 140/200=70%, retained 120/200=60%; July activated 140/220≈63.64%, retained 110/220=50%. Difference: minus 10 percentage points in day-30 retention, with about 2% unmatched accounts in each cohort. Cause unknown. Next: check whether activation event semantics or customer mix changed before proposing a guided setup experiment. No account is labeled likely to churn.

## Inadequate output to reject

“August retention is zero because its 30-day window has not finished; the new setup caused churn.” Reject: immature accounts are censored, and the source records cannot establish causality.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

Activation and Retention Intelligence emits cohort-retention-observation/v1 to Growth Experiment Planner only after exact cohort maturity, identity, source and rate checks. A cohort difference is an observed signal, not a causal result or approved test. The Crew step must supply schema fields and an output path; ask Builder to repair a route that omits them. A validator checks shape and references; a human checks source truth.

## Boundaries

Do not declare churn for immature cohorts, infer a feature caused retention, assign an individual account a risk label from cohort averages, contact customers, or change product experience from installation. Never copy another Crew's credentials. Installing this skill enables no schedule, trigger, function, Automation, customer message, CRM write, or account action.
