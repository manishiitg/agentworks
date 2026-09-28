---
name: pipeline-health-to-owned-action
description: Propose a source-verified pipeline exception and separate seller-owned next-step decision.
---

# Pipeline Health to Owned Action

## Outcome

Turn one stale B2B opportunity in comparable CRM snapshots into an owned next-step decision after checking its current activity and contact state. A pipeline exception is not a forecast, permission to contact a buyer, or proof that CRM changed.

## When to use

Use when a sales manager needs to understand a stale opportunity and ask the seller for an exact next decision. Pipeline Analyst owns the snapshot comparison; Deal Follow-through Coordinator owns current-state review and the seller action register. Run one opportunity per handoff; aggregate counts can be reported only after each bounded record is checked.

## Discovery and user direction

Inspect existing Sales Crews, CRM account and pipeline, prior/current snapshot coverage, stage and currency definitions, stale rule, seller owner, current opportunity/activity source and contact policy. Show a concrete proposal before Builder configures it. A single snapshot supports a current-state brief, but this route needs two comparable snapshots.

## Required inputs

Bind tenant, CRM account, pipeline, reporting window, exact opportunity, snapshot IDs and revisions, stage and amount/currency fields, stale threshold, activity and contact source, seller owner and approval policy. Record gaps and changed definitions.

## Plan and AgentWorks tools

Create Pipeline exception, blocking validation, Deal current-state review and blocking validation steps. Pipeline emits `pipeline-exception-brief/v1` with an exact opportunity, comparable snapshots, source IDs, stale calculation and limits. Deal receives the validated artifact, re-reads current CRM, activity and prior contact, and emits `deal-action-register/v1` with an unsent, unexecuted seller decision. A CRM write, message or meeting change is a separate exact-object approved route with a provider receipt.

## Knowledge and persistence

Save snapshot and opportunity IDs, definitions, stale rule, activity source revision, action key, seller decisions and any exact provider receipts. On repeats, re-read current state, compare like windows, preserve prior decisions and retire superseded suggestions.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks exact scope and artifact joins, snapshot ordering, stale age, current-state coverage, action deduplication and receipt claims. The fictional [exception](examples/pipeline-exception-brief.json) and [action register](examples/deal-action-register.json) pass; an [invalid action](examples/invalid-deal-action-register.json) fails. The dashboard reports proposed, seller-approved, deferred, suppressed and provider-observed states separately.

## Guardrails

Do not call a stage move new revenue, infer win probability or buyer intent from inactivity, use stale source state after a newer activity, contact an opted-out buyer, create a duplicate task, or mark a draft as sent. Seller review is required before action; provider receipts are required for claims of execution.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for snapshot, stale and action rules; [setup](SETUP.json) has ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated exception and action register, seller decision, CRM/contact action receipts or none, one manual case, blockers and activation choice. Keep recurrence off until reviewed.
