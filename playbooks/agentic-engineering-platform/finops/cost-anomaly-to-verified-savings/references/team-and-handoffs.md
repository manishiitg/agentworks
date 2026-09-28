# Cost anomaly to verified savings team

## Distinct Crew jobs

**Cloud Cost Analyst** reads authorized billing and usage records. It explains a same-basis period change and emits `cloud-cost-review/v1` for an exact provider, account, service, resource, environment and currency. It separates usage, price and one-time effects, names a risk-checked candidate and owner, and leaves the action a proposal.

**Engineering Delivery Coordinator** consumes that validated review. It checks peak load, dependencies, IaC owner/state and change policy, then emits `cloud-change-review/v1`. A proposal or rejection has no deployment receipt. A deployed state requires distinct peak/dependency review, owner approval, IaC plan, deployment and post-change health evidence. The Playbook does not grant infrastructure write access.

**Finance Analyst** consumes both validated artifacts, reads its own authorized billing and workload records, and emits `cloud-savings-readout/v1`. It returns `pending_change` before deployment, `pending_verification` after deployment but before comparable bills, and `verified` only for the exact resource after the full comparable window and service-health pass. A reviewable proposal is a valid first result; it is never counted as realized saving.

## Blocking Workflow route

1. Cost Crew saves the review at its supplied path. Run `python3 scripts/validate_handoff.py cost <cost.json>` before Delivery reads it.
2. Delivery Crew saves the change review. Run `python3 scripts/validate_handoff.py change <cost.json> <change.json>` before Finance reads it.
3. Finance Crew saves a pending or verified readout. Run `python3 scripts/validate_handoff.py savings <cost.json> <change.json> <savings.json>` before dashboard totals or notifications use it.

Builder must provide paths and insert these as blocking scripted steps. It records each Crew run ID, artifact path, validator output, source record, owner review and unresolved blocker. A structural pass does not prove cloud or finance source truth. For the first manual test, use one authorized account/service/resource and two comparable cost periods; do not apply a change merely to make the test pass.

## Fictional case and rejected claim

The [cost review](../examples/cloud-cost-review.json) explains a USD 2,700 service increase: USD 2,400 additional usage, USD 300 one-time charge, and no price effect. Candidate C-1 is a possible nonproduction worker change with USD 250–600 monthly projected savings; peak and dependency evidence is still needed. The [change proposal](../examples/cloud-change-review.json) and [pending Finance readout](../examples/cloud-savings-pending.json) honestly claim no execution or realized saving.

A separate fictional [approved deployment](../examples/cloud-change-deployed.json) plus [completed Finance readout](../examples/cloud-savings-verified.json) shows a resource billed USD 600 in the baseline window and USD 250 after, with equal workload and health evidence. Its USD 350 saving is resource-specific and cannot be added again through an overlapping service candidate. The [rejected readout](../examples/invalid-cloud-savings-verified.json) claims USD 600 verified while the change is still a proposal; it lacks authorization, deployment and comparable billing.

## Repeat and activation

Retain stable review, candidate, change and readout IDs. On repeat runs, re-read billing revisions, current resource/IaC state, owner decisions, deployment receipts, workload and health. Replace a proposal only when new evidence supports it. Keep one-time credits, price/commitment shifts, demand changes and overlapping candidate savings separate. Record manual-only or a paused schedule with timezone, API limits, budget, retries, duplicate event keys and notification policy. A later approved write route must recheck exact decision, diff and resource state before applying anything.
