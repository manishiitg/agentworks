---
name: feedback-theme-to-product-decision
description: Propose a source-verified customer feedback theme and a separate product owner decision.
---

# Feedback Theme to Product Decision

## Outcome

Turn a bounded set of customer feedback into a product decision brief with exact source coverage, denominator, current issue state and next evidence. A theme is not an approved roadmap item or a customer promise.

## When to use

Use when Support hears a recurring product problem and Product must investigate, link, defer or decline it. Feedback & Review Analyst owns source normalization; Product Feedback Coordinator owns the product-side review. Reuse compatible Crews, but keep source privacy and owner boundaries explicit.

## Discovery and user direction

Inspect existing Crews, feedback sources and allowed quotations, account segments, current issue/roadmap source, product evidence, and decision owner. Ask for missing scope only. Show a concrete proposal before Builder configures it; selection creates no issue or message.

## Required inputs

Bind tenant, product, feedback period, source channels, deduplication rule, theme ID, unique reporter denominator, privacy policy, product owner, current issue source and decision criteria. Record missing coverage and counterexamples.

## Plan and AgentWorks tools

Create steps for Feedback brief, blocking validation, Product review and blocking validation. Feedback emits `feedback-theme-brief/v1` with stable record IDs, counts, denominator and representative evidence. Product receives only the validated bounded artifact and authorized references, re-reads issue/roadmap and product records, then emits `product-feedback-decision/v1`. An issue creation, status change or customer update is a separate exact-object approved route with a provider receipt.

## Knowledge and persistence

Save source-window and theme IDs, deduplicated feedback IDs, numerator and denominator, segment, evidence and privacy scope, current issue IDs, owner decisions, action keys and receipts. On repeats, compare like source coverage and preserve earlier decisions.

## Validation and reporting

The [validator](scripts/validate_handoff.py) checks scope joins, unique IDs, counts, denominator, source references, decision state and issue-action evidence. The fictional [theme](examples/feedback-theme-brief.json) and [decision](examples/product-feedback-decision.json) pass; an [invalid decision](examples/invalid-product-feedback-decision.json) fails. The dashboard distinguishes unreviewed, investigate, linked, deferred, declined and separately actioned themes with source coverage and owner.

## Guardrails

Do not expose private customer text to an unauthorized Crew, generalize one segment to all users, infer an incident, create a ticket, promise a feature or publish a roadmap change from installation. A product owner must review recommendation and action authority.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md) for route decisions.
- [Team and handoffs](references/team-and-handoffs.md) for theme identity, duplicate and action rules; [setup](SETUP.json) has ten checks.

## Completion contract

Return Crew IDs, source and privacy map, validated theme and product decision artifacts, owner decision, issue or customer-action receipts or none, one manual case, blockers and activation choice. Keep recurrence off until reviewed.
