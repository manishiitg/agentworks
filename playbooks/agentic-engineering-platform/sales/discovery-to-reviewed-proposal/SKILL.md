---
name: discovery-to-reviewed-proposal
description: Propose a sourced sales call brief and an unsent, owner-reviewed proposal grounded in approved discovery and pricing.
---

# Discovery to Reviewed Proposal

## Outcome

Prepare one exact-meeting brief and one unsent proposal for the same account and opportunity. The proposal uses reviewed post-call discovery notes and current approved pricing. A prepared brief, meeting held, proposal approved and proposal sent are distinct states.

## When to use

Use when a SaaS seller connects discovery and proposal work across owners or access boundaries. Call Briefing and Proposal Drafter are required; Account Researcher is optional. One Crew can handle a compatible manual case.

## Discovery and user direction

Inspect existing Crews, meeting and opportunity IDs, CRM and calendar source, approved product claims, discovery-note process, price list, discount authority and proposal owner. Ask for missing scope. Show the concrete Crew and step plan before Builder applies it. No selected template sends a proposal.

## Required inputs

Bind tenant, account, opportunity, meeting, seller, time zone, offer, approved source scope, post-call notes version, pricing version, currency, commercial owner, format and review policy. If the call has not occurred or notes are not approved, stop at the meeting brief and leave the proposal step pending.

## Plan and AgentWorks tools

Create steps for Call Brief, blocking validation, seller-confirmed discovery note, Proposal Draft and blocking validation. The brief emits `sales-call-brief/v1`; the proposal cites that exact brief and a separate approved note revision, approved offer and price IDs. Pass bounded references through a Crew attachment. Re-read opportunity and pricing before drafting. If a separately approved route later sends the proposal, require exact recipient, version, owner approval, current-state check, idempotency key and provider receipt.

## Knowledge and persistence

Keep account, opportunity and meeting IDs; invite and CRM revisions; approved discovery note ID; source and pricing versions; proposal version; owner corrections; validation results; approval state; and any delivery receipt. On repeat, compare these exact versions and preserve an approved or sent version.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks scope joins, source references, approved discovery, pricing arithmetic and unsent state. The fictional [brief](examples/sales-call-brief.json) and [proposal](examples/proposal-draft.json) pass; an [invalid proposal](examples/invalid-proposal-draft.json) fails. The dashboard distinguishes brief ready, awaiting discovery, draft review, approved and provider-sent, with missing evidence visible. A validator cannot judge whether a product promise is commercially true; the owner reviews source claims.

## Guardrails

Do not fabricate budget, buying authority, need, product capability, price, discount, legal term or delivery date. Do not treat a meeting invite as a discovery record. Installation neither contacts a prospect nor creates, publishes or sends a proposal or changes CRM stage. Keep customer notes in authorized storage.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route choices.
- [Team and handoff contract](references/team-and-handoffs.md) for exact IDs and approval states; [setup](SETUP.json) has ten checks.

## Completion contract

Return Crew IDs, source map, validated brief and proposal or a clear pending state, owner decisions, approved source versions, send receipt or none, one manual-case result and activation choice. Keep recurrence off until separately reviewed.
