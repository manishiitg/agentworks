---
name: incident-to-verified-recovery
description: Propose a two-Crew incident investigation and delivery follow-through with owner decisions and observed recovery evidence.
---

# Incident to Verified Recovery

## Outcome

Produce a sourced incident timeline, an owned delivery or rollback decision, and observed recovery evidence. A PR, green build, or completed deployment alone does not prove service recovery.

## When to use

Use when an incident needs investigation across telemetry and a governed engineering follow-through. One compatible Crew may fill both slots with separate validated outputs.

## Discovery and user direction

Inspect existing Crews, incident manager, alerts, metrics, logs, deploy history, owners, and recovery policy. Propose reuse or creation and show a concrete plan before Builder applies it. Installation copies this guide and pending `SETUP.json`; it runs nothing.

## Required inputs

Confirm incident ID, service, environment, impact window, timezone, owner, escalation rule, authorized source scope, and measurable recovery rule. Bounded exports support manual read-only investigation.

## Plan and AgentWorks tools

Reuse or propose Incident Investigator and Engineering Delivery Coordinator. Builder may use `create_crew` with stable keys for missing Crews. Investigation writes `incident-investigation/v1` with a sourced timeline, impact, hypotheses, and proposed action. Validate with `scripts/validate_handoff.py --incident-only <incident.json>` before Delivery reads it. Delivery joins exact issue, PR, commit, CI, and deploy IDs and emits `engineering-blocker-ledger/v1`. Validate the pair, present the exact action for owner review, then execute only through an authorized route. Re-read telemetry over the agreed observation window before reporting recovery.

## Knowledge and persistence

Keep stable incident, service, action, issue, PR, commit, deploy, approval, and run IDs; source times; validation results; and prior notifications. Recheck current status before a repeat to prevent duplicate tickets or remediation.

## Validation and reporting

The [handoff validator](scripts/validate_handoff.py) checks bounded artifacts. The fictional [incident](examples/incident-investigation.json) and [ledger](examples/engineering-blocker-ledger.json) pass; the [invalid ledger](examples/invalid-engineering-blocker-ledger.json) fails. The reporting dashboard shows investigated, pending approval, delivered, recovered, not recovered, and unknown states with sources and run cost. Structure validation cannot prove source truth or recovery.

## Guardrails

Stop on conflicting incident identity, unauthorized source, unsupported impact, missing owner, or invalid handoff. No production action without exact owner approval and customer tool permissions. If telemetry cannot establish the recovery rule, report unknown. A completed deployment does not close an incident; the owner decides closure.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for goal and route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for artifact fields, and [setup](SETUP.json) for ten customer checks.

## Completion contract

Return Crew IDs, source map, validated artifact paths, manual run, owner decision, production action state, telemetry result, activation choice, and blockers. Keep recurrence off until a real route and duplicate handling are verified.
