---
name: case-pattern-to-knowledge-review
description: Turn a verified repeated support question into a privacy-safe, owner-reviewed help article draft without claiming publication or deflection.
---

# Case Pattern to Knowledge Review

## Outcome

Produce a reviewed case pattern and an unpublished knowledge-gap brief with article draft and next verification.

## When to use

Use this route when the job crosses the listed owners or access boundaries. A single Crew may do a compatible read-only task. This package is a Builder proposal, not an active automation.

## Discovery and user direction

Inspect existing Crews, source access, policy revisions, owner decisions and the exact subject. Ask only for missing facts, then show the proposed route for review.

## Required inputs

One product question across exact cases, current help article revision, locale and support owner. Read current case threads, priority policy, approved product instructions, current article and privacy or quotation rules.

## Plan and AgentWorks tools

Create reviewed steps for support-triage-assistant (`support-case-pattern/v1`) → support-knowledge-curator (`support-knowledge-update/v1`). Reuse Crews where compatible; Builder may propose `create_crew` for a missing role. Validate each typed artifact before the next step and independently re-read current source state. No case closure, customer reply, knowledge publication or deflection claim follows from the Playbook selection.

## Knowledge and persistence

Store tenant, subject, Crew, artifact, source revision, decision, approval and provider receipt IDs. Re-read source truth and deduplicate on repeat; keep prior decisions in the ledger.

## Validation and reporting

The [contract validator](scripts/validate_handoff.py) checks exact identity, revisions, time order and route-specific false claims. The [first artifact](examples/triage.json), [last artifact](examples/knowledge.json) and [rejected artifact](examples/invalid-knowledge.json) exercise it. The dashboard must separate pending review, owner approval and observed provider action.

## Guardrails

Case similarity needs distinct IDs and current article comparison; a draft or approval is not publication or measured deflection. No case closure, customer reply, knowledge publication or deflection claim follows from the Playbook selection. Do not count a draft, approval or planned action as a provider outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choice.
- [Team and handoffs](references/team-and-handoffs.md) for exact contracts and [setup](SETUP.json) for the ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, manual case, owner decision, action receipts or none, dashboard, blockers and activation choice. Recurrence remains off until separately reviewed.
