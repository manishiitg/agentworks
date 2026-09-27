---
name: performance-regression-to-owned-change
description: Turn a reproducible service or page regression into an owner-reviewed change plan tied to the exact build and performance budget.
---

# Performance Regression to Owned Change

## Outcome

Produce a sourced regression diagnosis and an owned blocker ledger with reproduction, uncertainty and next verification.

## When to use

Use this route when the job crosses the listed owners or access boundaries. A single Crew may do a compatible read-only task. This package is a Builder proposal, not an active automation.

## Discovery and user direction

Inspect existing Crews, source access, policy revisions, owner decisions and the exact subject. Ask only for missing facts, then show the proposed route for review.

## Required inputs

One service or page, endpoint or journey, baseline and regressed builds, environment, traffic condition and Engineering owner. Use comparable trace or test runs, performance budget revision, deployment timeline, issue/PR/CI state and change policy.

## Plan and AgentWorks tools

Create reviewed steps for performance-investigator (`performance-regression-finding/v1`) → engineering-delivery-coordinator (`engineering-performance-action/v1`). Reuse Crews where compatible; Builder may propose `create_crew` for a missing role. Validate each typed artifact before the next step and independently re-read current source state. No production change, rollback, issue write, customer claim or performance recovery claim from installation or a proposed fix.

## Knowledge and persistence

Store tenant, subject, Crew, artifact, source revision, decision, approval and provider receipt IDs. Re-read source truth and deduplicate on repeat; keep prior decisions in the ledger.

## Validation and reporting

The [contract validator](scripts/validate_handoff.py) checks exact identity, revisions, time order and route-specific false claims. The [first artifact](examples/investigation.json), [last artifact](examples/delivery.json) and [rejected artifact](examples/invalid-delivery.json) exercise it. The dashboard must separate pending review, owner approval and observed provider action.

## Guardrails

A metric change needs comparable conditions and coverage; correlation with a deploy is a hypothesis. An accepted change plan is not a deployed fix. No production change, rollback, issue write, customer claim or performance recovery claim from installation or a proposed fix. Do not count a draft, approval or planned action as a provider outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choice.
- [Team and handoffs](references/team-and-handoffs.md) for exact contracts and [setup](SETUP.json) for the ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, manual case, owner decision, action receipts or none, dashboard, blockers and activation choice. Recurrence remains off until separately reviewed.
