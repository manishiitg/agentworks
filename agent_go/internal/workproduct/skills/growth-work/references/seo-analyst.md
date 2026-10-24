# SEO Analyst

This skill provides the SEO Analyst capability (Technical SEO analyst for this website). Find and prioritize observable crawl, indexability, metadata, internal-link, and page-experience issues. When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## First useful result

1. Confirm the canonical domain, crawl boundary, market, and important pages.
2. Inspect a bounded sample of pages, robots directives, sitemap, headings, metadata, canonicals, links, mobile layout, and observable page experience.
3. Separate directly observed issues from Search Console-only indexation claims.
4. Rank issues by likely user/search impact, confidence, effort, and affected page.
5. Produce a reviewable issue list and a small verification plan for each proposed fix.

Deliver **A page-level SEO issue list with evidence, impact, effort, and uncertainty.** For each issue include the inspected URL, observation, source or capture time, confidence, affected page set, and a retest.

## Severity and retest rule

Rank a directly observed broken path or conflicting canonical above a speculative metadata improvement on an unmeasured page. Record affected URL count, user/search impact, confidence and owner effort separately; never invent a numeric traffic gain. A public fetch can show robots and canonical markup but cannot prove Search Console indexing. Retest the same URL, device and page revision after a reviewed change, then check Search Console only when authorized.

## Fictional worked example

Input: ArborDesk UK clinic page and public HTML observed 2026-09-25; Search Console unavailable. Good output: issue SEO-01, https://arbordesk.example/clinics, canonical points to / instead of /clinics in rendered HTML, observed in browser capture B-17, confidence high for markup error but indexing impact unknown, owner action review canonical target, retest fetch and rendered DOM after change. Issue SEO-02, missing descriptive internal link to /reminders, lower priority because the page remains reachable. Inadequate: “Google has deindexed the clinic page; traffic will double after fixing it.” It claims unavailable index state and fabricated lift.

When invoked as the SEO Intelligence technical step, return one plain JSON object matching seo-issue-list/v1 at the path supplied by the Crew step. Include canonical site, market, locale, device, crawl scope, dated page sources, stable issue IDs, observed state, confidence, owner action, retest and explicit unknown indexation. Builder must run the blocking issue validator before another Crew uses it.

## Boundaries

Do not claim that a page is indexed from a public fetch; do not change robots, redirects, canonicals, or site content without review. Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this skill was added.
