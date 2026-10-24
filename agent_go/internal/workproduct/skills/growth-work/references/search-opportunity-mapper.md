# Search Opportunity Mapper

This skill provides the Search Opportunity Mapper capability (Buyer-question and search opportunity researcher). Map target buyers’ questions and search intent to existing pages and evidence-backed content gaps. When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## First useful result

1. Confirm the offer, audience, geography, buying stage, and site scope; read the strategist brief when this is a Website Growth Loop run.
2. Collect buyer questions from approved owner notes, customer conversations, site copy, or authorized search data; label each origin and deduplicate equivalent questions.
3. Inspect representative product, service, FAQ, and resource pages, then map each question to a page that actually answers it, a weak answer to improve, or a documented gap.
4. Use authorized search data when available; otherwise mark demand and ranking as unknown.
5. Rank opportunities by buyer relevance, answer gap, evidence quality, and feasible next action; explain why an existing-page update or a new page is justified.

Deliver **A ranked buyer-question-to-page map with gaps and source links.** Each opportunity has a stable ID, exact buyer question, source, intent, current page or gap, evidence URLs, confidence, next action, and reason for rank. Link the source strategist finding when present.

## Mapping and decision rules

Keep the candidate set bounded to the approved audience and offer. Cluster questions only when a single page can answer the same user need; retain distinct buying-stage or product questions. Mark each candidate **answered** when a page directly answers it, **weak answer** when a relevant page exists but leaves the decision question unresolved, or **gap** when no inspected page addresses it. A relevant title alone is not a complete answer.

Choose **update existing page** when the page already serves the same buyer and intent. Choose **new page** only when the user need is distinct enough to deserve its own page and the owner has useful source material. Choose **verify first** when the question or claimed demand has weak evidence. Prefer a high-relevance, evidenced gap with a feasible owner over a speculative high-volume idea. Record the reason for the order; do not create a numeric opportunity score without measured inputs.

Save stable question IDs so later runs can mark answered, deferred, or rejected questions without rediscovering them. On a repeat run, read the prior map and owner decisions; inspect changed pages and new evidence first. Report a delta and state when nothing has changed. Search Console rows may omit anonymized queries, so treat a query export as a partial view of buyer demand.

## Worked opportunity map

Fictional owner context: ArborDesk sells appointment software to independent physiotherapy clinics in the UK. The site is newly launched. The strategist observed that the clinic page discusses scheduling but does not explain reminders. Two pages were inspected on 2026-09-25: `https://arbordesk.example/` and `https://arbordesk.example/clinics`. No Search Console data was available.

| ID | Buyer question and source | Page decision | Evidence and confidence | Next action / reason for order |
| --- | --- | --- | --- | --- |
| `Q-001` | “How do patient reminders work?” Owner-confirmed buyer question, linked to strategist finding `F-001`. | Weak answer on `/clinics`. | Page mentions scheduling but not reminders; observed on 2026-09-25. Confidence: medium until product facts are confirmed. Search demand: unknown. | First: update the existing clinic page after the owner confirms the feature. It serves the same buyer and intent; a separate page is premature. |
| `Q-002` | “Can patients reschedule themselves?” Hypothesis from a site-content gap; no customer or search evidence yet. | Gap in the two inspected pages; uninspected pages remain unknown. | Inspected URLs above; confidence: low. Search demand: unknown. | Verify with the owner and inspect the remaining site before proposing a page. |

Save a structured `search-opportunity-list/v1` artifact for the Loop with `site_url`, `buyer_questions[]`, evidence references, confidence, and proposed next action. Include the inspected scope and source date. The first result may be partial when buyer research is unavailable; label hypotheses rather than filling the list with invented demand.

**Review failure:** “physiotherapy software: 5,000 monthly searches, build a new page” fails without an authorized query source, the actual buyer question, an inspected existing-page answer, and a reason a new page is better.

When invoked as the Website Growth Loop search step, return one plain JSON object matching search-opportunity-list/v1. Do not wrap it in Markdown or add other commentary. Include a stable artifact_id and source_brief_artifact_id matching the validated strategist run, inspected URLs, exact question sources, stable IDs, evidence, confidence, unknown demand, and any strategist finding IDs so the Workflow can validate the saved response. For the distinct SEO Intelligence route, use seo-opportunity-list/v1 instead: set source_issue_artifact_id to the validated seo-issue-list/v1 artifact ID, cite exact blocking issue IDs, preserve site/market/locale/device, source each buyer question and page decision, keep demand unknown without authorized metrics, and leave publication pending a separate approved route. For AI Visibility Intelligence, use ai-citation-opportunity/v1 linked to the exact validated ai-visibility-snapshot/v1 ID, preserve site, question version, engine, surface and locale, cite a real page observation and approved fact, reproduce citation counts, and leave the content decision pending owner review with publication not_published.

## Boundaries

Do not invent search volume or ranking difficulty; avoid producing keyword lists detached from an audience need. Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this skill was added.
