---
name: seo-intelligence
description: Coordinate technical SEO and buyer-question Crews for a source-backed, owner-reviewed site opportunity.
---

# SEO Intelligence

## Outcome

Produce a validated technical issue list and buyer-question map for an approved site. The first outcome is an owner-reviewed action.

## When to use

Use for two-Crew search review, including a new site without Search Console history. Reuse Growth Data Foundation when available; otherwise mark demand and ranking unknown. Website Growth Loop handles publication and measurement.

## Discovery and user direction

Inspect existing Crews, access and source coverage. Propose distinct SEO Analyst and Search Opportunity Mapper bindings, boundaries, validators, a manual case and owner review. Record the owner's direction. Installation does not approve execution.

## Required inputs

Confirm canonical site, approved host/page scope, locale, device, offer, buyer, market, one sourced buyer question, crawl limits and page-change owner. Record an authorized Search Console property and comparable-window policy when available; otherwise record a baseline-first choice. Competitor and rank-tracker sources are optional.

## Plan and AgentWorks tools

SEO Analyst saves `seo-issue-list/v1` at the Crew step's path. Builder runs the blocking issue validator before Search Opportunity Mapper reads that exact artifact. The mapper combines it with buyer-question and page evidence, then saves `seo-opportunity-list/v1`. Builder validates this second file before reporting or creating an action. Ask Builder to repair a missing output path or validator. Prove one manual run before proposing paused recurrence. Page edits and publication need separate approval.

## Knowledge and persistence

Store site and metric policy, Crew IDs, artifact paths, source revisions, validator results, owner decisions and stable issue/opportunity IDs. Retain the action ledger across runs. Re-read changed pages and buyer sources before reopening a case. Keep observed issue, proposed update, approved edit, verified publication and measured result as separate states.

## Validation and reporting

Run `scripts/validate_handoff.py issue <issue.json>` and `scripts/validate_handoff.py opportunity <issue.json> <opportunity.json>` as blocking Workflow steps. Match site, market, locale, device, issue artifact and cited IDs. A public fetch cannot prove indexation. For measured demand, verify property, filters, equal windows, clicks, impressions, CTR, freshness and coverage. The dashboard shows page issues, buyer questions, evidence, blockers, owner decisions, unknowns and next checks; show trends only from comparable authorized data. A structural pass does not prove source truth.

## Guardrails

Do not crawl outside scope, use deceptive SEO, invent search volume, or alter robots, redirects, canonicals or content without approval. A proposed fix is neither shipped nor measured. Retest the exact page after an approved change; ranking movement alone does not prove cause.

## Read details when needed

- [Shared workflow design and outcomes](../../references/workflow-design-and-outcomes.md).
- [Team, handoff and repeat method](references/team-and-handoffs.md).
- [SEO analysis rules](references/seo-intelligence-workflow.md).
- [Fictional issue](examples/seo-issue-list.json), [opportunity](examples/seo-opportunity-list.json) and [rejected claim](examples/invalid-seo-opportunity-list.json).
- [Pending setup checklist](SETUP.json) and [catalog metadata](playbook.json).

## Completion contract

Return Playbook version, reviewed Crew roster, site and metric policy, saved artifact paths, blocking validator results, source coverage, manual run IDs, owner-reviewed action and next check, plus the manual or paused recurrence decision. Claim publication or measured impact only from separate provider and comparable source evidence.
