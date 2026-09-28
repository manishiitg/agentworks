---
name: support-case-to-reviewed-resolution
description: Propose a support triage, grounded reply, and optional owned escalation with separate delivery and outcome evidence.
---

# Support Case to Reviewed Resolution

## Outcome

Route one authorized customer case to an owned, sourced response or escalation. A drafted reply, approved reply, provider delivery, and observed resolution are separate states.

## When to use

Use when a support team has a real case source, current approved help content, a contact owner, and a priority policy. Start with one manual case. Feedback theme analysis is a separate route.

## Discovery and user direction

Inspect existing Support Crews, case and thread history, known incident state, help sources, prior contact, owners, and provider access. Propose reuse or reviewed creation. Show the exact case route before Builder applies it; selection leaves setup pending.

## Required inputs

Bind tenant, case, account, recipient, channel, current thread revision, owner, priority rules, approved source revisions, and any escalation policy. Record who may approve contact and what source proves case closure.

## Plan and AgentWorks tools

Reuse Support Triage Assistant and Support Reply Drafter; add Escalation Coordinator for a distinct receiving owner or boundary. Triage writes `support-case-triage/v1`; run the bundled validator before Reply or Escalation consumes it. Reply writes an unsent `support-reply-draft/v1`. Escalation writes `support-escalation-brief/v1` with accepted owner state; validate it before Reply uses its claims. Insert explicit validator steps in the Workflow. Send only through a separately authorized action after exact approval and a fresh prior-contact check.

## Knowledge and persistence

Save tenant, case, account, thread revision, source revisions, run, draft, approval, message fingerprint, delivery receipt, and outcome IDs. Re-read current case and thread before retries or scheduled runs; skip duplicate sends.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks joins, source evidence, draft state, escalation acceptance, delivery receipt, and observed closure. Fictional [triage](examples/support-case-triage.json), [reply](examples/support-reply-draft.json), [escalation](examples/support-escalation-brief.json), [delivery](examples/provider-delivery.json), and [outcome](examples/case-outcome.json) illustrate valid stages; the [unsupported reply](examples/invalid-support-reply-draft.json) fails. The reporting dashboard shows queued, awaiting review, escalated, delivered, observed resolved, and blocked separately with source links and cost.

## Guardrails

Do not send, close, page, or update a case through installation. Never infer customer identity from name, claim an incident is confirmed from a symptom, or call an unsent draft resolved. Require owner approval of exact recipient and message, authorized provider receipt, and later source-observed case state.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for contracts and [setup](SETUP.json) for ten checks.

## Completion contract

Return Crew IDs, source map, validated artifacts, manual run, owner decisions, provider state, observed case outcome or unknown, activation choice, and blockers. Keep recurrence off until reviewed.
