---
name: launch-to-qualified-pipeline
description: Propose a chat-led B2B launch route from approved positioning through source-linked inbound signals, Sales qualification, and verified pipeline outcomes.
---

# Launch to Qualified Pipeline

## Outcome

Produce a sourced launch brief, reviewed channel action ledger, linked inbound signals, and a qualified pipeline decision backed by Sales records. Draft, approved, published, lead captured, qualified, contacted, and booked are distinct states.

## When to use

Use when a B2B company wants one accountable route from a new offer and site to qualified enquiries. A simple strategy question can stay in one Crew. Use the existing Sales Playbook for its qualification, follow-up, and meeting subroute.

## Discovery and user direction

Inspect current Crews, site and offer, customer evidence, approved claims, assets, channels, campaign IDs, form and CRM records, contact policy, budget, owners, and meeting source. Propose reuse or reviewed creation. Show the exact plan before Builder applies it; selection changes nothing.

## Required inputs

Bind launch and offer version, audience and market, source-backed message, channel and budget bounds, visitor action, qualification and contact policy, attribution rule, baseline or baseline-first choice, and review owners. An export supports a manual first route.

## Plan and AgentWorks tools

Reuse or propose GTM Strategy Analyst, Launch Coordinator, Lead Intake & Qualifier, and Sales Follow-up Coordinator. Add Website Growth Starter only for needed site work. Builder may use \`create_crew\` with stable keys. Strategy emits \`gtm-launch-brief/v1\`; validate it before Launch reads it. Launch emits \`launch-signal-register/v1\`; validate it and match an exact event/lead before Qualification. Then use [Inbound Lead-to-Meeting Review](../../sales/inbound-lead-to-meeting-review/SKILL.md) and its validator for the qualification-to-follow-up route. Publishing, spend, and contact each require separate authorized action routes.

## Knowledge and persistence

Save offer, launch, asset, campaign, source, event, lead, action, Crew, and run IDs; approvals; provider receipts; metric definitions; source times; and prior decisions. Re-read current states and deduplicate before every repeat.

## Validation and reporting

The [GTM validator](scripts/validate_handoff.py) checks launch and source identity, approved brief, duplicate event handling, and lead joins. The [valid artifacts](examples/gtm-launch-brief.json) and [invalid register](examples/invalid-launch-signal-register.json) exercise the handoff. The reporting dashboard separates approved, shipped, captured, qualified, contacted, and booked states with source links and cost. Source truth and Sales outcomes require independent checks.

## Guardrails

Never claim a traffic or pipeline lift without comparable evidence. A form event is not a qualified lead; a message draft is not sent; a send is not a meeting. No public claim, publication, spend, CRM write, or contact follows from installation. Recheck consent, suppression, replies, duplicates, and booking before a reviewed outbound action.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for goal and route choices.
- [Team and handoffs](references/team-and-handoffs.md) for contracts and [setup](SETUP.json) for ten checks.
- [Sales validator](../../sales/inbound-lead-to-meeting-review/scripts/validate_sales_artifact.py) for later lead and follow-up contracts.

## Completion contract

Return Crew IDs, approved scope and source map, validated artifact paths, manual run, owner decisions, actual provider and pipeline evidence, dashboard, activation choice, and blockers. Keep recurrence off until the manual route is reviewed.
