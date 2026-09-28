---
name: website-growth-loop
description: Build a chat-led, multi-Crew Website Growth Automation for a newly launched company site, with source-linked opportunities, measured progress, and reviewed handoffs.
---

# Website Growth Loop

## Outcome

Coordinate Crews to find traffic opportunities, prepare improvements, and measure the owner's visitor goal.

## When to use

Use for a new site seeking relevant visitors. Public pages and owner context support a first manual run. Add connected measurement later. Use a Crew schedule for one repeating specialist.

## Discovery and user direction

Inspect the Automation, Crews, skills, access, and goals. Propose the team, handoffs, cost, and blockers; record owner decisions. Selection does not authorize creation or runs.

Track setup in `SETUP.json`. Verify checks, save references under `evidence[id]`, and report blockers.

## Required inputs

Resolve site, offer, audience, market, visitor action, crawl scope, owner, metric, baseline decision, and publication boundary. Search Console and analytics are optional initially; never invent history.

## Plan and AgentWorks tools

Use Website Growth Starter and Search Opportunity Mapper as distinct required Crews. Reuse suitable Crews; technical SEO is optional. After owner review, use `create_crew` with stable idempotency keys for missing specialists. Bind internal triggers and attachments. Add blocking validation after each selected producer: strategist, search, content, page, publication, distribution, measurement. Test the route manually before proposing recurrence.

## Knowledge and persistence

Save decisions, scope, metrics, Crew bindings, evidence, and windows. Track stable action IDs and states. Pass bounded artifacts. Read previous runs and decisions before repeating; report changes.

## Validation and reporting

Verify two required Crew IDs, skills, access, exact artifact IDs, and a manual run. For optional routes, verify approved content and page claims, separate publication approval and receipt, live-page checks, channel delivery receipts, and comparable measurement counts. Prove invalid artifacts stop the next Crew. The dashboard shows action state, sources, baseline, denominators, run IDs, cost, and blockers.

## Guardrails

Do not infer indexing from a public fetch, fabricate keyword volume or traffic, blend Search Console clicks with analytics sessions, or promise rankings. Keep CMS publication, outreach, paid spend, and recurring triggers behind separate review and authorization. Recheck Crew access before each run.

## Read details when needed

- [Workflow design and outcomes](../../references/workflow-design-and-outcomes.md): goal, route, and schedule decisions.
- [Team and handoff contract](references/team-and-handoffs.md): exact roles, setup checks, artifacts, and run proof.
- [Action and measurement cycle](references/action-and-measurement.md): owner review, shipping evidence, measurement, and repeat runs.
- [Artifact validator](scripts/validate_growth_artifact.py): deterministic checks for every selected handoff through publication, distribution and measurement.
- [Example strategist brief](examples/growth-priority-brief.json), [search map](examples/search-opportunity-list.json), [content brief](examples/content-brief.json), [page draft](examples/approved-page-draft.json), [shipped change](examples/shipped-change.json), [distribution plan](examples/distribution-plan.json), and [traffic readout](examples/traffic-readout.json): fictional worked handoffs.
- [Example run record](examples/website-growth-run.json): fictional run evidence shape.
- [Setup progress](SETUP.json): ten checks, saved evidence, and completed IDs.
- [Catalog metadata](playbook.json): roster, inputs, and recommendations.

## Completion contract

Return the applied Playbook version, owner choices, actual Crew IDs, access and setup state, Workflow step IDs, validated handoffs, first run and dashboard links, metric/baseline limits, paused or manual activation choice, and unresolved work.
