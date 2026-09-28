---
name: cost-anomaly-to-verified-savings
description: Coordinate cloud cost analysis, reviewed engineering delivery and finance verification for one exact savings candidate.
---

# Cost Anomaly to Verified Savings

## Outcome

Explain a cloud-cost change, prepare an optimization decision, and verify resource savings from comparable billing and health evidence.

## When to use

Use for an authorized cloud account and resource with billing and utilization evidence. Start read-only when change or post-change evidence is absent. Use distinct Cost, Delivery and Finance Crews.

## Discovery and user direction

Inspect existing Crews, cost basis, resource and finance sources, and IaC ownership. Propose Cost Analyst, Delivery Coordinator and Finance Analyst bindings. Show handoffs, validators, approvals and manual first case. Record customer direction before configuration. Installation approves no change.

## Required inputs

Confirm provider, account, service, resource, environment, currency, cost basis, equal periods, owners, reliability policy, IaC route and Finance verification rule. An export supports read-only review. Record missing access.

## Plan and AgentWorks tools

Cost Analyst saves `cloud-cost-review/v1`; run its blocking validator. Delivery Coordinator reads that exact artifact and saves `cloud-change-review/v1`; validate it before Finance reads. Finance Analyst saves `cloud-savings-readout/v1` with `pending_change`, `pending_verification` or `verified`; validate before reporting. Builder supplies paths and repairs any missing validator step. Run one bounded manual case first, then propose a paused recurrence. A separately approved action may prepare or apply an IaC change after fresh state checks.

## Knowledge and persistence

Keep stable review, candidate, change and readout IDs, source revisions, Crew runs, artifact paths, validator results, owner decisions and next checks. Re-read billing and resource state on repeat runs. Keep projected, implemented and verified amounts separate, and prevent overlapping candidates from being counted twice.

## Validation and reporting

Run `scripts/validate_handoff.py cost <cost.json>`, then `change <cost.json> <change.json>`, then `savings <cost.json> <change.json> <savings.json>` as blocking Workflow steps. Recompute usage, price and one-time effects; join exact provider/account/service/resource/environment and candidate IDs. Deployment needs distinct risk, approval, plan, provider and health receipts. Verified savings need complete equal billing windows, the same basis, comparable workload and observed health. The dashboard shows cost drivers, candidate range, change state, pending evidence and verified resource savings; it never treats an estimate as realized.

## Guardrails

Do not stop or resize resources, change IaC, buy commitments, send messages or book savings from template installation. Recheck current diff, state and durable approval before any separately authorized action. Do not attribute credits, price shifts or demand drops to rightsizing, or claim a green plan is deployed.

## Read details when needed

- [Shared workflow design](../../references/workflow-design-and-outcomes.md).
- [Crew route and worked cases](references/team-and-handoffs.md).
- [Cost and change method](references/finops-workflow.md) and [service checks](references/service-cost-analysis.md).
- [Pending setup](SETUP.json), [cost example](examples/cloud-cost-review.json), [verified example](examples/cloud-savings-verified.json) and [catalog metadata](playbook.json).

## Completion contract

Return the Playbook version, three Crew bindings, exact cost scope and basis, artifact paths, validator results, owner-reviewed action, current change and Finance outcome states, source limitations, manual run IDs, next check and manual or paused activation decision. Report verified savings only from a complete comparable bill and health result.
