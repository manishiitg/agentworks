---
name: launch-signal-to-pipeline-review
description: Reconcile an observed launch campaign with deduplicated form, lead and opportunity records under one attribution policy.
---

# Launch Signal to Pipeline Review

## Outcome

Produce an observed launch register and a sourced funnel review with coverage, stage counts, gaps and owner action.

## When to use

Use this route when the job crosses the listed owners or access boundaries. A single Crew may do a compatible read-only task. This package is a Builder proposal, not an active automation.

## Discovery and user direction

Inspect existing Crews, source access, policy revisions, owner decisions and the exact subject. Ask only for missing facts, then show the proposed route for review.

## Required inputs

One approved offer, launch, campaign, time window and attribution model; no inferred qualified lead or revenue lift. Read provider publication evidence, campaign and form events, current CRM lead and opportunity records, identity rule and lookback window.

## Plan and AgentWorks tools

Create reviewed steps for launch-coordinator (`launch-signal-register/v1`) → revenue-operations-analyst (`gtm-funnel-reconciliation/v1`). Reuse Crews where compatible; Builder may propose `create_crew` for a missing role. Validate each typed artifact before the next step and independently re-read current source state. No campaign spend, CRM stage edit, lead contact or revenue claim follows from a form event or this Playbook installation.

## Knowledge and persistence

Store tenant, subject, Crew, artifact, source revision, decision, approval and provider receipt IDs. Re-read source truth and deduplicate on repeat; keep prior decisions in the ledger.

## Validation and reporting

The [contract validator](scripts/validate_handoff.py) checks exact identity, revisions, time order and route-specific false claims. The [first artifact](examples/launch.json), [last artifact](examples/revenue.json) and [rejected artifact](examples/invalid-revenue.json) exercise it. The dashboard must separate pending review, owner approval and observed provider action.

## Guardrails

Stage counts must reconcile in order and unmatched events retain partial attribution; contact and sales acceptance remain separate. No campaign spend, CRM stage edit, lead contact or revenue claim follows from a form event or this Playbook installation. Do not count a draft, approval or planned action as a provider outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choice.
- [Team and handoffs](references/team-and-handoffs.md) for exact contracts and [setup](SETUP.json) for the ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, manual case, owner decision, action receipts or none, dashboard, blockers and activation choice. Recurrence remains off until separately reviewed.
