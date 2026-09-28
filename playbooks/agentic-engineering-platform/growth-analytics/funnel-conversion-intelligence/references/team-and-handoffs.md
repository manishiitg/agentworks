# Funnel and Conversion Intelligence team and handoff

## Crew jobs

**Funnel Analyst** owns the sourced cohort observation. It joins authorized product events to paid-state records, freezes event versions, identity and exclusion rules, and counts unique accounts reaching each ordered stage. **Growth Experiment Planner** owns a pending test proposal, not a claim that the test launched or won. Reuse existing Crews when skills and source scopes fit; propose distinct Crew IDs if a role or access boundary is missing.

The first manual case should cover one product, tenant and eligible cohort. The fictional ArborDesk example uses equal 30-day windows. Baseline: 1,000 eligible, 300 signup, 180 activated, 60 paid. Current: 1,200 eligible, 360 signup, 180 activated, 48 paid. Paid/eligible falls from 6.0% to 4.0%; activated/signup falls from 60% to 50%. Identity coverage is 98% in both periods, so the observation is partial. The drop does not identify its cause. A [new company baseline](../examples/funnel-baseline-first.json) has only the current cohort and explicitly leaves trend fields empty.

## Blocking route

1. Funnel Analyst saves `funnel-observation/v1` at the Crew step path. Run `python3 scripts/validate_handoff.py observation <observation.json>` as a blocking Workflow step. Require ordered stage IDs, monotone unique counts, rate arithmetic, equal windows when comparing, distinct event/billing references and a paid-state cutoff after the cohort window. Baseline-first must omit prior counts and change claims.
2. Planner reads the exact validated artifact by checked alias and names its `artifact_id`. It saves `funnel-experiment-plan/v1` with the same product, tenant, cohort, metric and time zone. Run `python3 scripts/validate_handoff.py plan <observation.json> <plan.json>` before any report or action uses it.
3. Record source revisions, Crew run IDs, artifact paths, validator output, missing joins, owner corrections and accepted limitations. The validator checks shape and arithmetic; source truth still requires an owner and authorized system inspection.

The fictional proposal asks whether a clearer first-schedule prompt could improve activation. It leaves the sample calculation and owner review pending. The [invalid plan](../examples/invalid-funnel-experiment-plan.json) uses the wrong baseline, omits a guardrail, claims an approved launch and calls a winner without an exposure or outcome record. It must stop.

## Owner action and repeat

An experiment launch needs a separate approved variant, assignment and power calculation, guardrail and release owner. Later measurement needs exact exposure and outcome records. Repeat runs retain cohort and event definitions, wait for billing lag, and reconcile later paid events to the same account keys. If event semantics, population or identity rules change, start a new baseline. A schedule remains paused until separately reviewed with source freshness, query cost, timezone and notifications.
