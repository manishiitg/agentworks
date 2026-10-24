# Experiment Run Coordinator

This skill gives one Crew the Experiment Run Coordinator capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Bind the exact frozen plan and owner approval record, including version, metric, guardrail, assignment unit, sample and stop rule.
2. Re-read the current provider variant, flag or campaign state; identify drift, conflicting launches, exposure eligibility and the rollback owner.
3. If action authority is absent or approval is pending, report pending without launching. If a separate approved route acts, record its provider receipt and exact launch timestamp.
4. Preserve a stable experiment/action ID, configuration revision, assignment and exposure evidence; never equate a task ticket with a live variant.
5. Hand a validated execution record and frozen readout window to Growth Outcome Analyst only when there is a provider-confirmed launch.

Deliver **An experiment execution record linking the frozen plan, owner decision, exact variant and exposure state, provider receipt or pending reason, rollback owner and readout window.** Cite plan revision, owner approval, provider object/version, rollout receipt and exposure records; distinguish proposed, approved, launched and rolled back.

## Source probe and acceptance

Read the frozen experiment plan revision, exact owner approval, provider variant/flag/campaign object and current exposure state for the same experiment ID. Record rollback owner, provider receipt if any, observation time and conflicting launches.

Separate pending approval, approved configuration, provider-confirmed launch and actual exposure. Reject a ticket-only launch claim or mismatched plan/variant; do not pass an execution record to Outcome without a matching provider receipt and exposure evidence.

## Follow-through

Re-read provider state by stable experiment ID; record revised configuration or rollback separately, prevent duplicate launch actions and preserve the original pre-registered readout rule.

## Fictional worked example

Fictional input: experiment exp-guided-setup-7, frozen plan rev3, approved by growth owner at 09:00, 50/50 account assignment, guardrail support blockers, readout after 30 days; feature flag ff-guided-setup-7.

Fictional output: plan rev3 and approval apr-7 match flag revision 4; provider receipt launch-7 confirms Oct 1 rollout to UK self-serve eligible accounts at 50/50. Exposure source exp-7 is linked; rollback owner product-lead; the final Oct 31 exposure reaches day 30 on Nov 30, so readout starts after that and source lag. No winner is asserted.

Inadequate: “The Jira ticket is done, so the experiment is live and winning.” Reason: A work item is not owner approval, provider launch, exposure evidence or measured outcome.

## Automation handoff

Growth Experimentation and Follow-Through emits experiment-execution-record/v1 to Growth Outcome Analyst after exact plan, owner approval, provider launch and exposure checks. A pending approval or missing launch receipt stops outcome claims. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not launch, publish, spend, send, change allocation or claim an experiment is live from a plan, ticket or verbal approval. Writes require an explicit reviewed action route and provider receipt. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.
