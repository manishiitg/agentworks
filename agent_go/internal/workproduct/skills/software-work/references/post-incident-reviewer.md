# Post-Incident Reviewer

This skill gives one Crew the Post-Incident Reviewer capability. It can seed a new Crew or be added to a compatible existing Crew without replacing its identity. Use the customer's existing systems as sources and approved action destinations.

## First useful result

1. Confirm recovery or stable status from the canonical incident record; freeze incident ID, service, environment, source revisions, time window, reviewer and privacy scope.
2. Reconcile impact numerator and denominator, affected scope and milestone timestamps from dated sources; label absent telemetry and clock gaps.
3. Build an event timeline with source links; keep temporal correlation separate from a confirmed contributing condition.
4. State what worked, what failed, alternative explanations and unresolved questions without assigning individual blame.
5. Propose bounded improvements with exact gap, owner, due/review date and verification test; leave publication and work-item creation pending review.

Deliver **A sourced post-incident review with exact incident and snapshot revision, reconciled impact, response timeline, confirmed facts, bounded contributing-condition hypotheses, unknowns, and proposed actions.** Every material timeline event, impact claim and factor needs a frozen source reference and observation time. A recent deploy alone does not prove cause.

## Fictional worked example

Fictional input: INC-1042 checkout-api production was stable at 10:30 UTC under recovery record REC-1042. Between 10:00 and 10:30, monitoring counted 160 5xx responses in 2,000 requests. Alert A-71, deployment D-89 and logs L-12 are frozen; a database saturation trace is missing.

Reviewable output: impact=160/2,000=8% 5xx in the incident window. Timeline: 10:01 D-89 deployed, 10:04 A-71 fired, 10:08 L-12 sampled timeouts, 10:30 REC-1042 confirmed stable indicators. The deployment is a **hypothesis**, not established cause. Gap: detection alerted after failures began. Proposed action ACT-1: service owner reviews an alert-threshold exercise by Oct 14, verified only by a replay result. Review state: pending owner approval; no issue or publication receipt.

## Inadequate output to reject

“Engineer A deployed the outage, so publish the root cause and close the action.” Reject: no causal proof, review approval, issue receipt or verified improvement exists, and assigning personal blame violates the review boundary.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Follow-through

Preserve review and incident IDs, source revisions and reviewer corrections. A newly discovered fact creates a new reviewed revision; do not silently overwrite a published review.

## Automation handoff

Post-Incident Review and Actions emits post-incident-review/v1 from a stable incident snapshot. Improvement Follow-Through Coordinator consumes only the exact validated review and owner decision; a draft cannot create work or prove completion. The Workflow must provide the artifact schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; source truth still needs review.

## Boundaries

Do not rewrite the canonical incident, rank people, publish a review, create tickets or state root cause without reviewed evidence and an authorized action route. Installing this skill enables no schedule, trigger, function, Automation, notification, or system write. Never copy another Crew's credentials.
