---
name: verified-knowledge-to-case-reply
description: Use an observed live help revision to prepare a grounded, unsent reply for an exact open support case.
---

# Verified Knowledge to Case Reply

## Outcome

Produce a verified live knowledge reference and an exact-case reply draft with source links and a contact decision.

## When to use

Use this route when the job crosses the listed owners or access boundaries. A single Crew may do a compatible read-only task. This package is a Builder proposal, not an active automation.

## Discovery and user direction

Inspect existing Crews, source access, policy revisions, owner decisions and the exact subject. Ask only for missing facts, then show the proposed route for review.

## Required inputs

One product, help article and exact open case, locale, recipient, support owner and contact policy. Read the live help revision and publication receipt, current case thread and account state, approved product facts and suppression history.

## Plan and AgentWorks tools

Create reviewed steps for support-knowledge-curator (`support-knowledge-publication/v1`) → support-reply-drafter (`support-reply-draft/v1`). Reuse Crews where compatible; Builder may propose `create_crew` for a missing role. Validate each typed artifact before the next step and independently re-read current source state. No article publication, case closure, customer send or deflection claim from selecting this Playbook or drafting a reply.

## Knowledge and persistence

Store tenant, subject, Crew, artifact, source revision, decision, approval and provider receipt IDs. Re-read source truth and deduplicate on repeat; keep prior decisions in the ledger.

## Validation and reporting

The [contract validator](scripts/validate_handoff.py) checks exact identity, revisions, time order and route-specific false claims. The [first artifact](examples/knowledge.json), [last artifact](examples/reply.json) and [rejected artifact](examples/invalid-reply.json) exercise it. The dashboard must separate pending review, owner approval and observed provider action.

## Guardrails

An approved article draft is not live knowledge. Recheck the published revision, case applicability and permission before a separate reply approval. No article publication, case closure, customer send or deflection claim from selecting this Playbook or drafting a reply. Do not count a draft, approval or planned action as a provider outcome.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choice.
- [Team and handoffs](references/team-and-handoffs.md) for exact contracts and [setup](SETUP.json) for the ten checks.

## Completion contract

Return Crew IDs, source and policy map, validated artifact paths, manual case, owner decision, action receipts or none, dashboard, blockers and activation choice. Recurrence remains off until separately reviewed.
