# Project Status Reporter

This skill gives one Crew the Project Status Reporter capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.

## First useful result

1. Bind exact project, milestone, reporting cutoff, time zone, owner, and status definitions.
2. Read current task and milestone records, last owner updates, and any validated meeting action register.
3. Join actions to project IDs and prior task IDs; classify accepted, open, blocked, done, unknown, and overdue from source state.
4. Explain changes since the last report with dates and source links; distinguish a plan from observed completion.
5. Prepare owner decisions and task update proposals separately from the read-only report.

Deliver **A milestone and action status report with project ID, as-of time, source coverage, completed versus open work, blockers, owner requests, and next evidence.** A completed milestone needs authoritative task, deliverable, or release evidence; a meeting promise is not completion.

## Source probe and setup acceptance

Read one real project and milestone by exact IDs, current tracker task state, the prior status report, and any validated meeting action for that milestone. Record reporting cutoff, revisions, and which delivery source is unavailable.

Show the owner an open or blocked milestone as open or blocked even when meeting notes promise completion. The setup remains pending if it marks work done without authoritative deliverable or release evidence, or hides missing source coverage.

## Follow-through

Reconcile prior action IDs and source revisions, preserve open decisions and late evidence, and update the same status history rather than creating a fresh unsupported report.

## Fictional worked example

Fictional input: project=site-launch-17; tracker=rev-9; due=2026-10-01; meeting register=launch-sync-42; status policy=delivery-v2.

Fictional output: as-of=2026-09-26T12:00Z; milestone=site copy review open, source=tracker:task-41@rev9; meeting action A1 owner acceptance=pending, not yet a committed task; launch risk=review may miss 2026-09-28; decision=product-lead confirm scope; coverage=tracker current, deployment source unavailable.

Inadequate: “Launch is complete because the team discussed it.” Reason: Discussion is not a shipped artifact; the report omits project identity, task state, source coverage, and owner decision.

## Automation handoff

Meeting Decision to Owned Follow-through consumes meeting-action-register/v1 and emits project-action-status/v1 for exact project/action IDs. Chief of Staff may summarize validated status when access permits. Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.

## Boundaries

Do not mark tasks complete, change deadlines, notify stakeholders, or declare a project green from stale or partial records. Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.
