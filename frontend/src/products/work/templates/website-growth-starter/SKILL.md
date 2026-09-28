---
name: website-growth-starter
description: Audit an authorized new website, identify evidence-backed traffic opportunities, and prepare a practical 30-day growth plan.
---

# Website Growth Starter

Use this skill when an owner has launched a site and wants to attract more relevant visitors. This pack works on its own or alongside other Crew templates. It does not replace the Crew's existing identity when added later.

## Setup through chat

Read `templates/website-growth-starter/TEMPLATE_SETUP.json` and `templates/website-growth-starter/SETUP.md` when setup starts. Work through the checks with the owner. Verify existing facts before asking again. Add a check ID to `completed_steps` only after its instructions have been satisfied; preserve the rest of the file and earlier progress. An explicit decision to skip an optional capability completes that optional check. Tell the owner what was verified, what remains, and the next useful action. Do not mark a check complete merely because a question was asked.

## First useful result

1. Confirm the owner's site URL (or uploaded page files), the offer, target buyers, geography, and the one action a useful visitor should take. If one is unknown, label the assumption and ask for it.
2. Inspect a bounded set of public pages: homepage, primary product or service page, key conversion page, and any published content hub. Record the URL and observation for every finding. If browsing is unavailable, ask for a site export or screenshots and state what cannot be checked.
3. Check discoverability basics that can be observed: page response and accessibility, navigation, title and description, headings, internal links, sitemap and robots directives, canonical hints, mobile readability, and obvious performance concerns. A public crawl cannot prove that Google indexed a page; use authorized Search Console data for that conclusion.
4. Identify one or two buyer questions to pass to Search Opportunity Mapper; that specialist owns the full buyer-question map. Separate observed site issues, plausible opportunities, and ideas requiring traffic or search data. Avoid fabricated keyword volume, ranking position, traffic forecasts, or AI citation counts.
5. Produce a **Website Growth Brief**: site and audience summary; source-linked findings; the most important discovery and conversion gaps; a prioritized 30-day action table with stable action ID, page, evidence, effort, owner, and success signal; and the smallest next inputs needed. Start with three to five actions that the owner can actually review. See the worked brief and review failures in `templates/website-growth-starter/SETUP.md`.
6. Ask the owner to review the brief. Revise priorities when business context changes. If the available evidence is thin, deliver a useful partial brief instead of presenting guesses as measured results.

When invoked as the Website Growth Loop strategist step, return the final response as one plain JSON object matching `growth-priority-brief/v1` in the Loop's worked example. Do not wrap it in Markdown or add commentary outside the JSON. Include a stable `artifact_id`, inspected scope, source dates, stable finding/action IDs, baseline state, and limitations so the Workflow can validate the saved response before calling the search Crew.

## Prioritize with an explanation

For each candidate action record whether evidence is **observed**, **owner-reported**, or **hypothesized**. Prefer an observed obstacle on the path to the visitor action, then an evidenced buyer need the site fails to answer, then lower-confidence ideas. Label effort small, medium, or large based on the actual review/change needed. Put an action first only when its buyer relevance, evidence, and feasible next step justify that order; record the reason and any owner override. Do not turn these judgments into a made-up traffic score.

When a page or data source is unavailable, record the blocker and the smallest next input. A screenshot can support a copy or layout observation but cannot establish HTTP behavior, canonical responses, or indexing. A public page fetch can show a declared canonical but not prove Google's selected canonical.

## Run again without losing decisions

Read the prior brief, action IDs, owner decisions, actual changes, and evidence dates before inspecting the bounded scope again. Recheck changed pages and unresolved blockers first. Keep completed, rejected, and deferred actions with their reasons; create a new action ID only for a genuinely new issue. Report changes since the prior run, what shipped, what remains unknown, and the next review date. If there is no new evidence, say so and do not repeat the same recommendations as new findings. Wait for a comparable measurement window before describing a trend.

## Measurement and execution

If the owner provides Google Search Console, analytics, or equivalent authorized exports, record the property, date range, query/page/source definitions, and limitations before comparing periods. A newly launched site may have too little data for a trend; use an initial baseline and report that limitation. Search Console and analytics answer different questions, so do not blend impressions, clicks, sessions, and conversions into one number.

Propose content briefs or page edits as reviewable artifacts. Use a connected repository or CMS only after the owner grants this Crew access and approves the publishing path. Verify changes against the live page after an authorized release. Never silently publish pages, change site settings, submit a sitemap, send outreach, start paid campaigns, or activate schedules, triggers, functions, or Automations.

For recurring work, use the suggestions in `templates/website-growth-starter/SETUP.md`. An optional weekly review can measure whether the planned work shipped and whether the agreed signals changed; it must distinguish correlation from causation and avoid promising search rankings or traffic.
