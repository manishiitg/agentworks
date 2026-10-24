# Improvement Follow-Through Coordinator

This skill gives one Crew the Improvement Follow-Through Coordinator capability. It can seed a new Crew or be added to a compatible existing Crew without replacing its identity. Use the customer's existing systems as sources and approved action destinations.

## First useful result

1. Bind the exact reviewed incident artifact and action IDs, service, environment, owner decision and publication scope.
2. Re-read current issue state and deduplicate by stable action key before proposing any write; keep unapproved actions pending.
3. For an accepted action, record owner, due date, exact issue receipt or explicit missing issue state, intended outcome and verification method.
4. Verify the control with an independent retest, deployment health, alert exercise, runbook review or policy-approved evidence; issue closure alone is insufficient.
5. Return open, blocked and verified actions with source revisions, overdue risk and owner next decision; leave reminders or publication to separate routes.

Deliver **An incident improvement register linking exact review/action IDs to owner decisions, current work-item state, due date, independent verification evidence or a pending reason.** Match review/action/issue IDs exactly. A verified action needs a dated verification source later than acceptance and a stated pass criterion.

## Fictional worked example

Fictional input: reviewed INC-1042 action ACT-1 asks for a checkout alert-threshold exercise by Oct 14. The owner accepted it in DEC-1042; issue ENG-901 was created with receipt TKT-901 and is marked closed. Replay RUN-77 is dated Oct 12 and shows the alert fired within the five-minute criterion.

Reviewable output: ACT-1 owner=checkout-service-lead, due=Oct 14, issue=ENG-901, status=verified from RUN-77 and owner review. The ticket closure is context, not the proof. A separate proposed dashboard action ACT-2 remains pending approval with no issue or verification claim. No reminder or publication was sent.

## Inadequate output to reject

“ENG-901 is closed, so the incident fixes are verified and recurrence is impossible.” Reject: issue status is not independent control evidence, ACT-2 remains pending, and recurrence cannot be ruled out.

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Follow-through

Reconcile stable incident/action/issue IDs on each run, re-read owner and current state, retain previous evidence, and supersede a verification only with a sourced correction.

## Automation handoff

Post-Incident Review and Actions consumes validated post-incident-review/v1 and emits incident-improvement-register/v1. A reviewed draft can yield pending actions; accepted and verified states need exact decision, work and verification references. The Workflow must provide the artifact schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; source truth still needs review.

## Boundaries

Do not create or close issues, send reminders, publish a review, or claim recurrence risk is reduced without owner authorization and evidence of the actual control. Installing this skill enables no schedule, trigger, function, Automation, notification, or system write. Never copy another Crew's credentials.
