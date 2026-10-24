# Incident Investigator

This skill gives one Crew the Incident Investigator capability. It can seed a new Crew or be added to a compatible existing Crew without replacing its identity. Use the customer's existing systems as sources and approved action destinations.

## First useful result

1. Confirm incident ID, service, impact window, timezone, current severity, owner, and permitted investigation scope.
2. Read current alert, metrics, logs, deploy, and status records. Preserve source IDs and event times; distinguish event time from ingestion time.
3. Construct a timeline of observed facts. Label correlations and possible causes as hypotheses until evidence supports them.
4. Identify missing telemetry, conflicting reports, affected customers or services only when authorized evidence supports the claim.
5. Return a next-action queue with owner, urgency, evidence needed, and the escalation or communication review point.

Deliver **A dated incident timeline, labeled hypotheses, affected-service scope, and owner-reviewed next actions.** Cite the alert, telemetry, and deploy record behind each material claim; mark impact and cause unknown when coverage is insufficient.

## Fictional worked example

Fictional input: incident INC-42 concerns the production API between 09:10 and 09:25 UTC. Alert A-42 reports 160 5xx responses in 2,000 requests. The prior comparable window has 20 in 2,000. Deployment D-9 changed the same API service at 09:05; logs L-4 show database timeouts. No database saturation metric was retained.

Reviewable output: **INC-42, investigating**. Timeline: 09:05 D-9 deployed SHA abc123; 09:10 A-42 begins; 09:13 L-4 records timeout samples; 09:25 alert stops. Observed 5xx rate is 160/2,000 = 8%, versus 20/2,000 = 1% in the prior window. The deploy is temporally correlated, not established as the cause. Hypothesis H-1: a query or pool change increased timeouts; missing proof is a database saturation trace. Next action: on-call owner inspects traces and decides whether to start an approved rollback route. External status and rollback state remain **not started**. Next check: 09:40 UTC.

## Inadequate output to reject

“The 09:05 deploy caused the outage; roll it back and announce resolution.” This fails because the root cause is unproven, the service action lacks owner approval, and no recovery retest or communication receipt exists.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Follow-through

On another run, re-read the incident and action state, append only new evidence, and correct disproven hypotheses. Do not duplicate incident tickets or page owners twice.

## Automation handoff

A recovery Workflow can consume a bounded `incident-investigation/v1` artifact containing incident ID, service, evidence references, hypotheses, owner decisions, and proposed actions. Verify the same incident and service before a remediation step uses it. The Workflow must provide the artifact schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; source truth still needs review.

## Boundaries

Do not restart a service, roll back a deploy, change an incident severity, or publish an external status update without an authorized owner decision and tool route. Installing this skill enables no schedule, trigger, function, Automation, notification, or system write. Never copy another Crew's credentials.
