# Lifecycle intelligence workflow

## Define lifecycle before analysis

Version the activation definition (events, thresholds, time window), cohort keys (signup period, plan, channel, segment), retention windows and censoring rules, feature/flag inventory with launch dates, and revenue linkage scope. Reuse customer definitions when their populations and windows are compatible; never silently redefine activation to improve a trend.

Record cohort comparability rules: cohorts must share definitions, taxonomy revision, and observation windows before their curves are compared. Flag immature cohorts explicitly instead of ranking them against mature ones.

## Adapt the plan

Use scripted steps for activation counts, cohort retention rates, authorized product and billing joins, and completeness checks (freshness, denominators, censoring, missing events). A difference is reportable as an observation only when both cohorts are mature and pass the customer's comparison and data-quality rules. Reserve claims of impact for a separately designed experiment.

Use a message sequence to investigate a supported difference: contrast cohorts on behavior, feature use, plan, and consented feedback themes; test alternative explanations (mix shift, seasonality, tracking change, outage, pricing change); and produce bounded hypotheses with limitations. Correlations can inform experiments; they do not prove causes or identify an individual as likely to churn.

For recurring monitoring, prove an on-demand analysis first. Keep a proposed schedule paused until the owner separately reviews scope, cohort maturity, billing lag, cadence, timezone, cost and notification conditions.

## Validation and report

Validate lifecycle reproducibility from durable snapshots, denominator and censoring disclosure on every rate, cohort compatibility, revenue-join reconciliation against billing samples, feedback sampling and consent basis, and evidence for every finding. Verify that taxonomy or tracking changes surface as data-quality events rather than silent retention shifts.

Build a lifecycle report showing the eligible and matched populations, mature versus pending cohorts, activation and retention rates, source coverage, hypotheses, limitations and owner decisions. One mature cohort supplies a baseline, not a trend. Incomplete or untrusted states stay visibly unrated.

## Handoff

The v0.2 team route passes a validated `cohort-retention-observation/v1` from Lifecycle Analyst to Growth Experiment Planner. Planner returns a pending `retention-experiment-plan/v1` with a metric, guardrail and owner review point. Growth Experimentation and Follow-Through can later consume an approved plan through its own action route. Return policy versions, exact cohorts and windows, source references and unknowns; do not ask downstream agents to reconstruct counts from chat history. The [team guide](team-and-handoffs.md) supplies the blocking checks.
