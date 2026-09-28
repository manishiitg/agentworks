# Activation and Retention Intelligence team and handoff

## Crew jobs and boundary

**Lifecycle Analyst** owns the maturity-aware cohort observation. It joins authorized signup, product and subscription records by exact product, tenant and account identity. **Growth Experiment Planner** owns a pending test proposal for a mature cohort signal. It does not change the product, label a customer at risk or claim that a treatment has won. Reuse Crews when their skills and access fit; propose a separate Crew when the job or source boundary is missing.

This is a cohort job. Product Adoption Analyst's first-value readout proves one account's agreed result and cannot be used as the denominator for a cohort. Customer Health Coordinator's account brief cannot infer individual churn from a cohort average.

## Three honest outcomes

The fictional [two-cohort case](../examples/cohort-retention-observation.json) compares equal 30-day signup windows. June: 200 eligible, 140 day-7 activated, 120 day-30 retained. July: 220 eligible, 140 activated, 110 retained. Retention falls from 60% to 50%, while account join coverage is about 98% in each cohort. This observed difference has no proven cause. The [single mature cohort](../examples/cohort-retention-baseline-first.json) is a baseline without a trend. The [August cohort](../examples/cohort-retention-pending-maturity.json) has not reached its full day-30 outcome window; retained count and change remain null, and planning stops.

## Blocking manual route

1. Freeze policy version, signup windows, activation and retention predicates, day-30 maturity, exclusions and identity rule. Lifecycle Analyst saves `cohort-retention-observation/v1` at its Crew step path. Run `python3 scripts/validate_handoff.py observation <observation.json>` as a blocking Workflow step. It checks account counts, rate arithmetic, equal comparison windows, maturity, censoring, identity coverage and distinct product/billing sources.
2. If `pending_maturity`, wait for the full cohort and re-read sources; do not pass a churn claim to Planner. For `baseline_first` or `comparable`, Planner consumes the validated file by checked alias, cites the exact artifact and preserves cohort IDs/policy. Run `python3 scripts/validate_handoff.py plan <observation.json> <plan.json>` before reporting.
3. Save Crew run IDs, artifact paths, validator output, source revisions, unmatched accounts, owner corrections and missing evidence. Shape and arithmetic checks do not prove the customer records are accurate.

The [pending plan](../examples/retention-experiment-plan.json) proposes reviewing one guided setup variant and leaves power calculation and owner decision open. The [baseline-first plan](../examples/retention-baseline-first-plan.json) has no observed change. The [invalid plan](../examples/invalid-retention-experiment-plan.json) invents an approved launch and winner, uses the wrong rate and omits a guardrail; it must stop.

## Repeat and action

Keep stable cohort and policy IDs. Wait for the latest signup plus 30 days and the agreed billing lag before a retention rate. A later correction requires a source revision; a changed event or paid-state rule starts a new baseline. Variant launch, customer contact and later measured outcome require their own approved route and receipts. A schedule stays paused until separately reviewed with timezone, source freshness, cost and notifications.
