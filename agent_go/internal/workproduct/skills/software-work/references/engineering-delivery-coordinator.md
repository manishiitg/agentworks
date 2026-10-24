# Engineering Delivery Coordinator

This skill gives one Crew the Engineering Delivery Coordinator capability. It can seed a new Crew or be added to a compatible existing Crew without replacing its identity. Use the customer's existing systems as sources and approved action destinations.

## First useful result

1. Confirm release or issue scope, cutoff time, expected destination environment, ownership, and release policy.
2. Join issue, PR, CI, and deployment records with exact IDs and commit SHA; flag ambiguous links instead of inferring them from similar titles.
3. Separate code complete, review complete, CI passed, deployed, and verified states. Find stale failures, missing approvals, and unowned handoffs.
4. Prepare a blocker ledger with each state, source link, accountable owner, next action, due time, and needed proof.
5. Ask the release owner to approve any ticket update, notification, rerun, merge, or deployment action.

Deliver **A sourced blocker ledger linking issue, change, CI result, deployment, owner, and next action.** A merged PR is not a deployed change; a green CI run is not production verification. Cite exact IDs, SHA, environment, and observation time.

## Fictional worked example

Fictional input: release R-7 includes issue ENG-42 and PR 81 at SHA abc123. CI run CI-81 passed at 14:20 UTC; PR 81 merged at 14:25. Staging deployment ST-19 runs abc123; production deployment PRD-18 still runs def456. The release policy requires a production canary check.

Reviewable output: ledger row **ENG-42 / PR 81 / abc123**. Code review: complete (PR 81). CI: passed (CI-81). Staging: deployed (ST-19). Production: **not deployed** (PRD-18 has a different SHA). Verification: pending canary. Blocker B-1: release owner decides by 16:00 UTC whether to schedule the production rollout and names the canary verifier. No issue update, deploy, or notification has been sent. Next check: 17:00 UTC if a rollout is approved, otherwise after the next owner decision.

## Inadequate output to reject

“PR 81 is merged and CI is green, so ENG-42 is live in production.” This fails because the production deployment source shows a different SHA and the required canary has no result.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Follow-through

Reconcile the same stable issue and change IDs on later runs, close only blockers with observed resolution, and avoid duplicate follow-ups.

## Automation handoff

A release Workflow can consume `engineering-blocker-ledger/v1` with stable issue and change IDs, SHA, environment, CI and deployment evidence, owner, and approval state. QA can add independent verification evidence. For Cost Anomaly to Verified Savings, consume only the validated `cloud-cost-review/v1` for the exact account, service, resource, environment and candidate. Return `cloud-change-review/v1` at the Crew step path: an unapproved proposal has no deployment receipt; a deployed result needs distinct risk, approval, IaC plan, deployment and health sources before Finance Analyst reads it. For Engineering Operations Intelligence, consume only validated `engineering-metric-observation/v1` for the exact team, service, policy and window; re-read current issue ownership and return `engineering-improvement-review/v1` with a bounded owner question. Issue writes and claimed outcomes remain separate. The Workflow must provide the artifact schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; source truth still needs review.

## Boundaries

Do not merge, rerun CI, deploy, update an issue, or send a team message without an authorized route and reviewed action. Installing this skill enables no schedule, trigger, function, Automation, notification, or system write. Never copy another Crew's credentials.
