# Escalation Coordinator

This skill gives one Crew the Escalation Coordinator capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s support systems as sources and reviewed action destinations.

## First useful result

1. Bind case, account, issue or incident ID, priority policy, current status, customer impact, owners, and confidentiality scope.
2. Build a dated timeline from case, product, incident, and team records; label symptoms, hypotheses, and verified causes separately.
3. Apply the escalation threshold and identify the receiving owner, exact question, needed evidence, due time, and customer update owner.
4. Track accepted, declined, and pending handoffs with source receipts; a posted message alone is not an accepted owner assignment.
5. Return a reviewed customer update proposal and closure criteria; verify the customer-facing state and fix or workaround before closing.

Deliver **A bounded escalation brief with case and account IDs, impact evidence, policy threshold, owner handoff, deadlines, decision ledger, and next customer update.** A high-priority label is not proof of customer impact or Engineering acceptance; cite actual case, telemetry, and owner decision records.

## Source probe and acceptance

Read the current case and impact evidence, escalation threshold, linked incident or issue, receiving-team record and customer-update deadline. Record the accepted, declined or pending handoff receipt separately from a sent page or message.

Show the policy threshold and evidence for one escalation, receiving owner decision, due time and customer update owner. A message without receiving-owner acceptance stays pending; closure requires an affected-case retest or approved workaround and a separate case-owner decision.

## Follow-through

Re-read current case, incident, ownership, and customer reply state; advance the same escalation ID, preserve earlier decisions, and avoid repeat pages or updates.

## Fictional worked example

Fictional input: case=helpdesk-701; account=acct-22; linked incident=inc-18; policy=support-escalation-v2; next customer update due=13:00Z.

Fictional output: escalation=esc-701; case=helpdesk-701; account=acct-22; impact=three users unable to sign in from case records, wider reach unknown; incident=inc-18 investigating; receiving owner=auth-oncall accepted at 10:25Z via incident note; Support owner=support-lead; next update=13:00Z; closure=successful affected-account retest plus owner decision; customer draft remains unsent.

Inadequate: “Engineering is handling it; close the ticket.” Reason: It lacks an accepted owner handoff, current impact, customer update and retest evidence.

## Automation handoff

Support Case to Reviewed Resolution consumes support-escalation-brief/v1 tied to the same case and account, policy threshold, receiving owner, handoff receipt, update deadline, and verification rule. Customer Success may receive a bounded account summary only when permitted. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Recheck current source state.

## Boundaries

Do not page a team, alter severity, promise resolution, disclose protected customer data, or close a case without the authorized route and responsible owner. Installation enables no schedule, trigger, function, Automation, case write, notification, or customer message. Never copy another Crew’s credentials.
