# Search Console Optimizer

This skill provides the Search Console Optimizer capability (Search Console page and query optimization analyst). Find page/query opportunities in authorized Search Console data and propose bounded page improvements. When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## First useful result

1. Confirm property, country, device, date range, data freshness, and page scope.
2. Group queries by intent and compare only compatible periods and dimensions.
3. Inspect the live pages before proposing title, copy, or internal-link changes.
4. Prioritize opportunities using actual impressions, clicks, CTR, position caveats, and business relevance.
5. Draft bounded edits and a follow-up measurement window.

Deliver **A sourced page/query opportunity report with proposed title, copy, or link changes.** Each recommendation includes property, query/page rows, date range, page observation, change proposal, and comparison limitation.

## Query aggregation and coverage

Use the same property, search type, country, device, filters and comparable date windows. Recompute CTR as sum(clicks) / sum(impressions), never the average of row CTRs. Query exports can omit anonymized terms and totals can differ from chart aggregates; label sampled/partial coverage. Inspect the target page before recommending copy or links. Prefer a relevant observed page-query mismatch over a position-only score.

## Fictional worked example

Input: authorized Search Console export for https://arbordesk.example/, UK/mobile/web, 2026-08-01 to 2026-08-31; /clinics has two reminder-query rows: 12 clicks/600 impressions and 8/400; page inspected 2026-09-25. Good output: aggregate CTR=20/1000=2.0%; query rows are partial because anonymized queries may be omitted; /clinics mentions scheduling but not reminder timing; propose one reviewed explanatory section, compare same dimensions after indexing and data lag. Inadequate: “Average CTR is 3% because one row had 4%; add the keyword everywhere.” It averages row rates and ignores page intent.

## Boundaries

Do not treat average position as a fixed ranking, invent query data, keyword-stuff copy, or publish without review. Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this skill was added.
