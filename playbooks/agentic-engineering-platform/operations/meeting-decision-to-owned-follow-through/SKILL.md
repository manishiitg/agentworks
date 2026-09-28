---
name: meeting-decision-to-owned-follow-through
description: Propose a meeting-action and project-status handoff with confirmed owners and source-observed task state.
---

# Meeting Decision to Owned Follow-through

## Outcome

Produce a sourced meeting action register and a project status report that distinguishes proposed, accepted, open, blocked, and completed work. A note, task proposal, approved task, provider-created task, and observed completion are separate states.

## When to use

Use when a small team wants decisions from authorized meeting notes tracked against a project. One Crew can carry both capabilities when owner and access are shared; use two Crews for a distinct project owner or lifecycle.

## Discovery and user direction

Inspect existing Operations Crews, meeting revision, participant map, task tracker, prior actions, project scope, status rules, and review owner. Propose reuse or creation. Show the exact route before Builder applies it; selection leaves setup pending.

## Required inputs

Bind tenant, meeting and notes revision, project, time zone, source spans, action and task IDs, accepted owners, due dates, duplicate rule, status evidence, and reporting owner.

## Plan and AgentWorks tools

Reuse Meeting Actions Coordinator and Project Status Reporter; add Chief of Staff for a distinct leadership review. Meeting writes `meeting-action-register/v1`; run the bundled validator before Status consumes it. Status re-reads current task records and writes `project-action-status/v1`; validate the pair. Optional Review writes `operations-review-brief/v1` from validated status. Insert explicit validator steps in the Workflow. Any task write or stakeholder message requires a separate exact approval and provider receipt.

## Knowledge and persistence

Save tenant, meeting, revision, project, action, task, decision, owner acceptance, due date, run, source, approval, receipt, and status IDs. On later notes revisions, reconcile the same action keys and existing tracker records before proposing another write.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks identity, source spans, owner acceptance, duplicate links, status joins, and completion evidence. Fictional [meeting](examples/meeting-action-register.json), [status](examples/project-action-status.json), and [review](examples/operations-review-brief.json) pass. The [false completion](examples/invalid-project-action-status.json) fails. The reporting dashboard separates pending confirmation, open, blocked, done, duplicate, and unknown actions with owner decisions and cost.

## Guardrails

Do not infer a decision from a suggestion or assign a person without confirmation. Do not create tasks, change deadlines, publish notes, notify stakeholders, or mark work done through installation. A meeting promise is not tracker completion.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for action rules and [setup](SETUP.json) for ten checks.

## Completion contract

Return Crew IDs, source map, validated artifacts, owner decisions, task receipts or none, manual run, activation choice, and blockers. Keep recurrence off until reviewed.
