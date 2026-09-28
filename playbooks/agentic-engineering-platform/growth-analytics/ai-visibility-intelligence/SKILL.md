---
name: ai-visibility-intelligence
description: Propose and set up a two-Crew AI-answer visibility route that turns sampled citations into a reviewed buyer-question content opportunity.
---

# AI Visibility Intelligence

## Outcome

AI Visibility Analyst captures answer-level mentions, citations and failures. Search Opportunity Mapper checks the question, page and approved facts before proposing an owner-reviewed improvement. A small sample establishes no rank or traffic result.

## When to use

Use for an approved buyer question on a named answer engine and market. Use [SEO Intelligence](../seo-intelligence/SKILL.md) for technical search health and Website Growth Loop for approved page changes and measurement. New sites can start with a baseline sample.

## Discovery and user direction

Builder inspects the site, questions, Crews, sources and owner. It proposes **two distinct Crews**, checks skills and access, freezes the sample method and presents the plan in chat. Selection creates no Crew, answer run, page change or schedule.

## Required inputs

Record brand domains, competitors, exact sourced question/version, engine/surface, locale, session mode, run budget, site/page scope, citation rule, owner, content policy and repeat choice.

## Plan and AgentWorks tools

1. Bind separate Crew IDs and steps. Capture dated valid and failed attempts from authorized sources. Block on `python3 scripts/validate_handoff.py snapshot <snapshot.json>`.
2. Pass `ai-visibility-snapshot/v1` by checked alias. The mapper inspects a real page and approved fact, then saves `ai-citation-opportunity/v1`. Block on `python3 scripts/validate_handoff.py opportunity <snapshot.json> <opportunity.json>`.
3. Review the sample, page proposal and limits with the owner. Keep citations separate from Search Console clicks and analytics referrals. Publication and recurrence need separate review.

## Knowledge and persistence

Store question version, sample policy, attempts, source and artifact IDs, page observation, owner decision and retest. Retain bounded captures under source rules.

## Validation and reporting

Recompute valid/failed attempts, mentions and direct citations. Failed runs are not negative answers. Compare only the same question, engine/surface, locale and method. The dashboard shows counts, denominator, question, sources, gaps, owner action and limitations. A sample trend proves no content impact.

## Guardrails

No fabricated answer, rank, market share or publication claim. A mention is not a citation. Reject deceptive content and disclose unreproducible access.

## Read details when needed

- [Team and handoff](references/team-and-handoffs.md)
- [Shared workflow design and outcomes](../../references/workflow-design-and-outcomes.md)
- [Visibility workflow and definitions](references/visibility-intelligence-workflow.md)
- [Sample snapshot](examples/ai-visibility-snapshot.json), [reviewed opportunity](examples/ai-citation-opportunity.json), and [rejected false result](examples/invalid-ai-citation-opportunity.json)
- [Setup checklist](SETUP.json) and [catalog metadata](playbook.json)

## Completion contract

Return the Crew plan, sample policy, validated artifact paths, owner decision, manual-run evidence, limits and paused repeat choice. State missing access or validation.
