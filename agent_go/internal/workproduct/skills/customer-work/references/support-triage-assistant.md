# Support Triage Assistant

This skill gives one Crew the Support Triage Assistant capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s support systems as sources and reviewed action destinations.

## First useful result

1. Confirm authorized inbox, product, customer and case identity rules, priority policy, time zone, owner, and response deadlines.
2. Read current case status, thread, attachments, prior contacts, incident signals, and account context within the approved scope.
3. Deduplicate by case and linked issue IDs; distinguish the customer’s reported symptom from a verified product incident.
4. Apply the customer’s priority and escalation rules with explicit evidence and uncertainty; assign a proposed owner and next action.
5. Return a source-linked queue and ask the support lead to review a representative case before any status or assignment write.

Deliver **A case queue with stable IDs, customer and channel scope, issue summary, priority rationale, duplicate state, response deadline, owner, and next evidence.** Every urgency and duplicate claim needs case IDs, observed times, policy rule, and supporting source references.

## Source probe and acceptance

Read one current case thread, its status and prior replies, the proposed duplicate case, and the active priority/SLA policy. Record exact case IDs, account join, observation times, rule version and timezone. An incident claim requires a separate current incident source.

Reproduce the priority and first-response deadline from the policy and case timestamps. Demonstrate that a shared symptom without an account or issue join is not silently merged, and that an unverified incident remains unverified.

## Follow-through

Reconcile the same case IDs, status and prior replies; add only new evidence, do not reopen resolved cases or create duplicate owner alerts.

## Fictional worked example

Fictional input: case=helpdesk-701; account=acct-22; product=dashboard; policy=support-priority-v3; message=login returns 403; owner=support-lead.

Fictional output: case=helpdesk-701; account=acct-22; observed=2026-09-26T09:10Z; symptom=403 after SSO; severity=P2 proposal under policy-v3 because three users report blocked access; linked cases=helpdesk-699 and helpdesk-700; incident=unverified; owner=auth-support; first-response due=2026-09-26T11:10Z; next=check current auth incident and draft acknowledgment; sources=case thread and status page.

Inadequate: “Critical outage; assign Engineering and tell everyone it is fixed soon.” Reason: It claims an incident and outcome without evidence, gives no policy basis or case IDs, and proposes unsanctioned assignment and messaging.

## Automation handoff

Support Case to Reviewed Resolution consumes support-case-triage/v1 with tenant, case, account, channel, policy, priority evidence, duplicate state, owner, response deadline, and source refs. Reply or escalation must re-read the current thread. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Recheck current source state.

## Boundaries

Do not change priority, assign a case, merge tickets, contact a customer, or declare an incident without the authorized route and owner review. Installation enables no schedule, trigger, function, Automation, case write, notification, or customer message. Never copy another Crew’s credentials.
