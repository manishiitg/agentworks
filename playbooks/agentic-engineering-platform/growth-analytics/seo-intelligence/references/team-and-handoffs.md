# SEO Intelligence team and handoff

## Crew jobs

Use two distinct authorized Crews. **SEO Analyst** observes one bounded site scope and saves `seo-issue-list/v1`; it does not infer indexing from a public fetch or edit the site. **Search Opportunity Mapper** reads that exact validated issue list plus dated buyer-question evidence and inspected pages, then saves `seo-opportunity-list/v1`. Reuse existing Crews when their selected skills, owner and access fit. Builder proposes new Crews only for a missing role or a distinct access boundary.

The first manual run should cover one market, locale, device and page set. If Search Console is unavailable, demand and ranking remain unknown. If it is available, compare the same property, page/query filter and device across equal windows; state coverage and data lag. Search Console counts do not prove that a proposed content change caused a movement.

## Blocking route

1. SEO Analyst saves an artifact to the Crew step's supplied path. Run `python3 scripts/validate_handoff.py issue <issue.json>` as a blocking Workflow step.
2. Search Opportunity Mapper reads the validated file by checked workflow alias and names its `artifact_id`. It saves the opportunity artifact to its supplied path. Run `python3 scripts/validate_handoff.py opportunity <issue.json> <opportunity.json>` before any report or action uses it.
3. Save Crew run IDs, artifact paths, validator output, source references and owner corrections. A passing shape check does not establish that the underlying page or buyer source is true.

The fictional site example contains a fetched clinic page whose canonical points to the homepage. The mapper has an owner-sourced question about patient reminders, but the page gives only a weak answer and no Search Console data is present. It recommends reviewing the canonical issue **first**, then verifying product facts before a page update brief. The [invalid opportunity](../examples/invalid-seo-opportunity-list.json) claims publication from a pending proposal and must be rejected.

## Owner action and repeat run

The action ledger keeps stable issue and question IDs, exact site/page/market, source revision, technical blocker, owner, approval state, proposed edit, retest, success signal and next check. Distinguish observed issue → proposed change → approved edit → provider-backed publication → comparable measured outcome. An approved canonical edit needs a fresh fetch of the exact page; indexation needs its own authorized source. A page update belongs in a separately reviewed publishing route such as Website Growth Loop.

On repeat runs, re-read the same page and approved buyer-question sources, compare source revisions, retain unresolved IDs and suppress duplicate proposals. Reopen a closed issue only when a new observation supports it. Record a manual-only choice or a paused schedule with crawl limits, timezone, data freshness and budget. The Playbook does not enable recurrence or site writes at install time.
