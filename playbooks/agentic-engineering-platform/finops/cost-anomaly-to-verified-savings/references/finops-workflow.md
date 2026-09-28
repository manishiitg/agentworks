# FinOps cost anomaly and optimization workflow

The installable v0.6 route starts with the three Crew jobs and blocking artifact checks in [team and handoff](team-and-handoffs.md). This reference supplies deeper provider, IaC and savings methods for that route. A first manual run may end with a pending action and unknown realized savings.

## Establish the cost and ownership contract

Define exact cloud scope, environments, billing account/project/subscription identity, currency and conversion date, cost basis (for example invoiced, net, amortized, or unblended as supplied), time granularity, attribution rules, credits/refunds/tax treatment, commitments, allocation tags, shared-cost rules, owner mapping, history window, and data freshness.

Every resource/service observation retains provider/source ID, region, service/SKU, environment, owner, cost/usage interval, quantity/unit, price/currency, commitment or credit effects, source update time, and ingestion provenance. Missing ownership or allocation remains unknown/unallocated rather than silently assigned.

## Detect and investigate anomalies

Use a scripted step to ingest and normalize cost, usage, inventory, utilization, demand, deployment, and service-health data with durable cursors and idempotent upserts. Detect anomaly candidates through the customer's budgets/baselines and seasonality rules. Preserve expected value/range, observed value, absolute and percentage variance, attribution, and data-quality status.

Use a message sequence to test plausible causes against evidence: workload/demand change, deployment or scaling event, price/SKU/region change, commitment/credit expiry, tag/allocation drift, idle/orphaned resource, configuration change, data delay/correction, or unknown. An anomaly is an investigation trigger, not permission to resize.

## Create a rightsizing candidate

Load [Service cost analysis](service-cost-analysis.md), then use only the guide for the affected service family and the installed provider adapter. This keeps the agent context small while preserving detailed billing, utilization, exclusion, and verification rules.

A candidate links the exact resource and IaC owner to:

- current and proposed configuration;
- observation and peak/percentile utilization window chosen by policy;
- workload seasonality, scheduled jobs, failover/headroom, autoscaling, quotas, and dependency constraints;
- service SLO/health signals and rollback trigger;
- price inputs, projected gross/net savings, confidence, and exclusions;
- blast radius, maintenance/deployment path, validation plan, verification window, and owner.

Do not recommend a change when telemetry is stale, missing representative peaks, or disconnected from the current resource/build/configuration. “Unused now” is insufficient evidence for deletion or downsize.

## Prepare, approve, and apply IaC

Resolve the canonical IaC repository, module/resource address, state/workspace, base revision, formatting/lint/test commands, plan behavior, CODEOWNERS/review requirements, deployment pipeline, and rollback procedure. Prepare the smallest candidate diff in an isolated branch/worktree. Run syntax, formatting, policy, module, and plan checks without applying.

Persist the exact diff, plan, projected savings, evidence window, capacity/SLO risk, rollout, health checks, rollback, and reviewer as pending review, then finish preparation. A reviewer may later approve, reject, or defer. A separate action route verifies the decision, exact diff, and current IaC/resource state before applying through the authorized pipeline; verify commit, plan, deployment, resource/configuration, and rollback receipts. Direct-console changes are allowed only when the customer's explicit operating policy makes them canonical and auditable.

## Verify savings and health

After rollout, monitor the configured stabilization and verification windows. Compare equivalent billing periods, workload/demand, price basis, currency, commitment/credit treatment, and environment. Verify service health, capacity/headroom, scaling, latency, errors, and customer-defined SLOs. Trigger the approved rollback path when health thresholds fail.

Keep these values distinct:

- **projected savings:** model estimate before change;
- **implemented savings:** recalculated estimate from the deployed configuration;
- **verified savings:** observed comparable cost reduction after the complete verification window;
- **avoided cost:** separately modeled future spend, never added to realized savings;
- **one-time credit/refund:** separate financial adjustment.

Record gross and net savings, verification dates, comparison method, demand, confidence, workload variance, price/commitment effects, and health result. Prevent double counting across overlapping resource, service, commitment, or anomaly candidates.

## AgentWorks plan pattern

A compact plan binds Cost Analyst → Delivery Coordinator → Finance Analyst as distinct Crew steps, with `cloud-cost-review/v1` and `cloud-change-review/v1` validated before the next Crew reads them. It may use:

1. `ingest-and-detect` — scripted cost/utilization sync and anomaly candidates;
2. `investigate-anomaly` — message sequence for cause, evidence, and candidate;
3. `prepare-iac-change` — scripted isolated diff, validation, and plan;
4. `queue-optimization-review` — persist a pending decision and end preparation;
5. `apply-approved-change` — separate action route that validates durable approval and current state;
6. `verify-health-and-savings` — scheduled or delayed scripted measurements plus agentic interpretation only when evidence conflicts;
7. `finalize-optimization` — scripted completeness, accounting, and report state.

Split ingestion by credential/rate-limit boundary and deployment by approval/security boundary. Do not create one step per billing query, metric, or resource.

## Persistence and dashboard

Declare sources, resources, owners, cost/usage observations, utilization/health observations, baselines, anomalies, candidates, IaC changes, validations, decisions, deployments, rollbacks, verification samples, savings, and artifacts in `db/README.md`. Preserve original observations and every candidate/version.

The report shows current cost and attribution health, anomaly timeline and cause, candidate economics/risk, IaC diff and validation, approval/deployment/rollback state, projected versus verified savings, service-health comparison, verification countdown/completeness, and limitations. A dashboard total sums only non-overlapping verified savings in the selected currency/basis/window.

## Acceptance cases

Exercise real usage increase, credit expiry, billing correction, tag drift, idle-resource candidate, stale/missing utilization, seasonal peak, autoscaled resource, overlapping candidates, IaC drift, plan failure, source/base drift, human reject/defer/approve, deployment failure, health regression with rollback, incomplete verification window, workload drop, price change, and verified savings. Confirm no unapproved change, false anomaly from incomplete data, unsafe rightsize, or double-counted savings.
