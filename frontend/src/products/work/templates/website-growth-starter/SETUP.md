# Website Growth Starter setup

Template: `website-growth-starter` version 1. Progress belongs to this Crew in `templates/website-growth-starter/TEMPLATE_SETUP.json`. The Crew verifies checks in chat; installing the pack never marks setup complete.

## Start without a connection

Provide the public site URL or uploaded site pages, a short description of the offer, target audience, market, and the primary action a good visitor should take. Ask Crew: “Audit this newly launched site and give me the first three to five actions to grow relevant traffic over the next 30 days. Cite the pages and label assumptions.”

The first Website Growth Brief can use public pages and owner context. Google Search Console and analytics make the baseline and follow-up measurable, but a new site may have little data. Do not require either connection to begin or claim measured traffic that is unavailable.

## Worked first brief

This fictional example shows the expected evidence and decision quality. The URLs and business facts below are illustrative; inspect the owner's actual site before writing a real brief.

**Owner context:** ArborDesk sells appointment software to independent physiotherapy clinics in the UK. Its useful visitor action is requesting a demo. The site is newly launched, so there is no comparable analytics window.

**Inspected scope:** `https://arbordesk.example/` and `https://arbordesk.example/clinics` on 2026-09-25. The homepage showed a “Book a demo” link; the clinic page explained scheduling features but did not explain how patient reminders work. This is a page-content observation, not a claim about indexing or search demand. No Search Console property was available. Baseline state: unavailable; start collecting qualified demo requests from 2026-09-25 after the owner confirms the event definition.

| Action ID | Priority and reason | Action and page | Evidence | Effort / owner | Success signal |
| --- | --- | --- | --- | --- | --- |
| `ACT-001` | First: directly observed missing answer for the named buyer, on the offer page. | Add a reviewed section explaining patient reminders on `/clinics`. | `F-001`: clinic page observation above; owner confirms reminders are an actual product feature before any claim is published. | Small / site owner. | Section ships with an approved claim; begin tracking visits to `/clinics` and qualified demo requests from its visitors. |
| `ACT-002` | Second: the homepage action exists, but the owner has not confirmed that a demo request is tracked. | Confirm and test the demo-request event. | Owner goal; measurement state is unverified. | Small / analytics owner. | One test submission appears in the authorized analytics or CRM source with the agreed definition. |

**Open questions:** Is the reminder feature available on every plan? Who approves public claims? Does a demo request event already exist? Keep `ACT-001` as a draft proposal until the claim is confirmed. This is a partial two-action brief; add a third action only when further evidence supports one.

For an Automation handoff, save the structured `growth-priority-brief/v1` artifact described by the Website Growth Loop contract. Link each finding to an inspected URL or owner-provided source. The next Crew receives this bounded artifact and its source references, rather than the full workspace.

**Review failure:** “SEO looks weak, write ten blog posts, expect 50% more traffic in a month” fails: it has no inspected page, buyer question, supporting source, action owner, or measurable basis for the forecast. Ask for evidence and rewrite the plan before marking `first_audit` or `first_plan` complete.

## Next run

Load the prior brief and decision on each action ID. Record whether it was approved, drafted, shipped, deferred, or rejected. Reinspect changed pages and unresolved questions; preserve the prior evidence date. Produce a delta report. If no page changed and no comparable metric window exists, report that state and keep the plan stable. Do not rebrand an old suggestion as a new discovery.

## Optional capabilities

These are suggestions, not installed or activated capabilities.

| Capability | Suggested definition | Before enabling |
| --- | --- | --- |
| Search data | Read an authorized Google Search Console property or export for page/query impressions, clicks, CTR, and indexing evidence. | Owner selects the property and grants this Crew access; test the property and date range. |
| Visitor data | Read an authorized analytics property or export for sources, landing pages, engagement, and conversions. | Owner confirms event definitions and privacy boundaries; test access. |
| Page editing | Prepare a reviewable repository change or CMS draft for approved pages. | Owner grants access, chooses a review/publish path, and verifies a sample edit. |
| Crew schedule | Review last week's site changes, Search Console/analytics signals if available, and the next three actions. | Agree on cadence, timezone, source, and delivery; test a run. |
| Authenticated trigger | Recheck a newly published page after a `page_published` event. Payload: `url` and `published_at`. | Choose an authenticated event source, validate allowed URLs, and test against a real page. |
| Crew function | `site_audit(url)` returns observed issues, page references, and open questions. | Restrict scope to authorized domains and test the typed contract before exposing it. |
| Goal-chasing Automation | Website Growth Loop tracks agreed actions shipped and changes in qualified traffic or conversions. | Create separately after choosing the metric, baseline, data source, cadence, and approval policy. |

Do not copy another Crew's token or connection. No schedule, trigger, function, or Automation is created by this pack.

Useful external references: [Google SEO Starter Guide](https://developers.google.com/search/docs/fundamentals/seo-starter-guide) and [Search Console setup](https://developers.google.com/search/docs/monitor-debug/search-console-start).
