import type { WebsiteGrowthSpecialistId } from './websiteGrowthSpecialists'

type Depth = {
  deeperMethod: string
  workedExample: string
  specialistChecks: readonly { id: string; title: string; instructions: string }[]
}

export const websiteGrowthDepth: Partial<Record<WebsiteGrowthSpecialistId, Depth>> = {
  'seo-analyst': {
    deeperMethod: '## Severity and retest rule\n\nRank a directly observed broken path or conflicting canonical above a speculative metadata improvement on an unmeasured page. Record affected URL count, user/search impact, confidence and owner effort separately; never invent a numeric traffic gain. A public fetch can show robots and canonical markup but cannot prove Search Console indexing. Retest the same URL, device and page revision after a reviewed change, then check Search Console only when authorized.',
    workedExample: '## Fictional worked example\n\nInput: ArborDesk UK clinic page and public HTML observed 2026-09-25; Search Console unavailable. Good output: issue SEO-01, https://arbordesk.example/clinics, canonical points to / instead of /clinics in rendered HTML, observed in browser capture B-17, confidence high for markup error but indexing impact unknown, owner action review canonical target, retest fetch and rendered DOM after change. Issue SEO-02, missing descriptive internal link to /reminders, lower priority because the page remains reachable. Inadequate: “Google has deindexed the clinic page; traffic will double after fixing it.” It claims unavailable index state and fabricated lift.',
    specialistChecks: [
      { id: 'crawl_observation', title: 'Verify one technical observation', instructions: 'Inspect a representative in-scope URL and retain the raw or rendered evidence, timestamp, canonical domain, device and redirect result. State which checks need Search Console rather than public fetch.' },
      { id: 'issue_retest', title: 'Review severity and retest', instructions: 'For one issue, record affected URL, impact, confidence, effort, owner choice and the exact same-URL retest. Reject unsupported indexing or traffic-lift claims.' },
    ],
  },
  'content-brief-writer': {
    deeperMethod: '## Brief acceptance\n\nBind one approved opportunity ID, buyer question and page decision. Check existing pages before choosing a new URL. For each factual claim, name an owner-approved product document or mark verification pending. A brief is ready for page drafting only when the owner agrees the angle, unique value, source facts, internal links, target action and unresolved claims.',
    workedExample: '## Fictional worked example\n\nInput: approved opportunity Q-001 asks how reminders work for UK clinics; existing /clinics page is a weak answer; product guide PG-4 confirms email reminders but SMS is unverified. Good output: update /clinics, audience=clinic manager, angle=explain timing and consent, sections=setup, patient experience, exceptions, FAQ; cite PG-4 for email claims, flag SMS for owner verification, link to /pricing, CTA=request demo, owner review pending. Inadequate: “Create a new high-traffic reminders page and claim no-shows fall 50%.” No demand source or approved product evidence supports it.',
    specialistChecks: [
      { id: 'opportunity_trace', title: 'Verify opportunity and page decision', instructions: 'Bind the approved opportunity ID and buyer question; inspect current pages and record why this is an update or a distinct new page.' },
      { id: 'claim_review', title: 'Review factual claims and handoff', instructions: 'Trace at least one claim to an approved source, flag unsupported claims, and have the owner review angle, links, target action and brief version before page drafting.' },
    ],
  },
  'content-page-builder': {
    deeperMethod: '## Draft versus implemented page\n\nDeclare the delivery type at the start: copy draft, CMS draft, or reviewable repository change. A copy draft does not imply a preview. For an implemented draft, check desktop and mobile rendering, links, form/action path and factual claims in the preview. Record the exact revision and unresolved items; publication is a separate owner-approved action with a provider or deploy receipt.',
    workedExample: '## Fictional worked example\n\nInput: approved brief BR-12 for ArborDesk /clinics update; product guide PG-4; copy-only delivery. Good output: draft path drafts/clinics-reminders-v1.md, sections matching BR-12, email reminder claim cites PG-4, SMS claim omitted pending review, internal link /pricing, CTA=request demo; review checklist lists claim owner and accessibility read; preview=not created, publication=none. Inadequate: “The reminders page is live and performing well.” A copy file has neither a published revision nor measurement.',
    specialistChecks: [
      { id: 'brief_claims', title: 'Verify approved brief and claims', instructions: 'Check brief ID/version, page target and each factual claim against an approved source; keep missing proof visible in the draft.' },
      { id: 'draft_acceptance', title: 'Inspect the promised deliverable', instructions: 'Record whether output is copy, CMS draft or repository change. For implemented drafts, inspect desktop/mobile preview, links and target action; retain exact path/revision and owner review.' },
    ],
  },
  'website-publishing-coordinator': {
    deeperMethod: '## Publication evidence and fallback\n\nA CMS draft, merged pull request or approved copy is a pending state. Verify the exact owner-approved target and revision before any authorized publish action. After the provider returns a receipt, inspect the live canonical URL, rendered desktop/mobile page, required links and target action. If publication happened outside AgentWorks, accept a dated owner receipt only after independently inspecting the live page where access allows; otherwise keep verification pending. A live page is not a measured traffic win.',
    workedExample: '## Fictional worked example\n\nInput: ArborDesk approved /clinics draft rev4 and content brief BR-12; site owner publishes through CMS. Good output: action A-17, approved target https://arbordesk.example/clinics, draft cms:clinics@v4, owner approval AP-9, CMS receipt PUB-17 at 2026-09-26T10:00Z, live inspection LIVE-17 at 10:05Z confirms revision, reminder text, /pricing link and demo action; state verified. Inadequate: “Published because the draft is approved.” Approval and CMS draft alone provide no release receipt or live check.',
    specialistChecks: [
      { id: 'release_authority', title: 'Verify target and publication authority', instructions: 'Match the approved brief, question, draft revision and canonical target. Name the release owner, approved CMS/deploy route, rollback plan and permission scope; read-only is valid if another owner publishes.' },
      { id: 'live_retest', title: 'Verify an actual shipped change', instructions: 'For one real release, retain owner approval, provider receipt, ship time and a later same-URL live inspection of revision, links and target action. Keep setup pending if only a draft or approval exists.' },
    ],
  },
  'search-console-optimizer': {
    deeperMethod: '## Query aggregation and coverage\n\nUse the same property, search type, country, device, filters and comparable date windows. Recompute CTR as sum(clicks) / sum(impressions), never the average of row CTRs. Query exports can omit anonymized terms and totals can differ from chart aggregates; label sampled/partial coverage. Inspect the target page before recommending copy or links. Prefer a relevant observed page-query mismatch over a position-only score.',
    workedExample: '## Fictional worked example\n\nInput: authorized Search Console export for https://arbordesk.example/, UK/mobile/web, 2026-08-01 to 2026-08-31; /clinics has two reminder-query rows: 12 clicks/600 impressions and 8/400; page inspected 2026-09-25. Good output: aggregate CTR=20/1000=2.0%; query rows are partial because anonymized queries may be omitted; /clinics mentions scheduling but not reminder timing; propose one reviewed explanatory section, compare same dimensions after indexing and data lag. Inadequate: “Average CTR is 3% because one row had 4%; add the keyword everywhere.” It averages row rates and ignores page intent.',
    specialistChecks: [
      { id: 'dimension_coverage', title: 'Verify query dimensions and coverage', instructions: 'Record property, search type, country, device, filters, date range, data lag and row-versus-chart coverage; do not treat omitted queries as zero demand.' },
      { id: 'reproduce_metric', title: 'Recalculate one page recommendation', instructions: 'Sum clicks and impressions across compatible rows, compute aggregate CTR, inspect the live page, and review one bounded edit with the owner.' },
    ],
  },
  'traffic-engagement-analyst': {
    deeperMethod: '## Comparable readout\n\nFreeze analytics property, event meaning, consent and bot/internal filters, channel/landing-page dimensions, attribution and time zone. Show numerator and denominator for every rate. Compare the same period length and source coverage; label instrumentation changes and small samples. When baseline or event verification is missing, produce a baseline-first or data-quality result instead of a performance claim.',
    workedExample: '## Fictional worked example\n\nInput: authorized analytics export for /clinics, Sep 1-14: 2,000 eligible sessions and 40 demo requests; Aug 18-31: 1,800 sessions and 36 requests; same event v2 and filters. Good output: conversion 40/2000=2.0% versus 36/1800=2.0%, no observed rate change; traffic increased but source mix changed, so reason unknown; next=inspect channel mix; source=analytics export rev7. Inadequate: “Conversions improved 11%, so the new copy worked.” It confuses count growth with rate and claims causality.',
    specialistChecks: [
      { id: 'event_definition', title: 'Verify event and population', instructions: 'Read one real event and record property, event version, session/user denominator, consent, bot/internal filters, time zone and attribution. Flag instrumentation changes.' },
      { id: 'rate_reproduction', title: 'Reproduce a comparable rate', instructions: 'Calculate one numerator/denominator pair for current and baseline windows; check equal duration and coverage, then review a no-baseline or partial-data response.' },
    ],
  },
  'ai-visibility-analyst': {
    deeperMethod: '## Repeatable observation\n\nFreeze question text, locale, engine, surface/version if available, session mode and capture time. Run at least two observations when possible; record errors and unavailable responses separately from valid answers without a mention. Distinguish a brand mention from an actual cited URL. Treat comparisons as samples with variance, not stable rankings or causal proof.',
    workedExample: '## Fictional worked example\n\nInput: owner-approved question “Which scheduling tools support UK physiotherapy clinics?”, engine E, UK locale, two clean sessions on 2026-09-25. Good output: run 1 cites /clinics among three URLs; run 2 names ArborDesk but cites no ArborDesk URL; mention observed in 2/2, citation in 1/2, sample too small for a ranking; next=check whether /clinics clearly answers the question and retest under same method. Inadequate: “We rank #1 in AI search.” Two stochastic answers do not establish a stable rank.',
    specialistChecks: [
      { id: 'sampling_policy', title: 'Freeze question and test method', instructions: 'Save exact question, engine/surface, locale, session mode, run count and timestamps; capture failed/unavailable responses as their own states.' },
      { id: 'citation_check', title: 'Verify mention versus citation', instructions: 'Inspect at least one answer for exact brand mention and cited URL, repeat a sample, and have the owner review limitations before proposing a content action.' },
    ],
  },
  'landing-page-optimizer': {
    deeperMethod: '## Test choice under low traffic\n\nInspect the exact page revision and mobile journey, target action, form and source/intent. Prioritize an observed broken action over a speculative copy test. State primary metric, guardrail, baseline and decision rule before a change. When traffic is too low for a credible split test, propose a reviewed single revision with a qualitative acceptance check and a later directional comparison, not a false winner.',
    workedExample: '## Fictional worked example\n\nInput: ArborDesk /clinics rev4, 140 eligible visits/month, demo form works on desktop but mobile CTA falls below a large sticky banner in capture M-9. Good output: observed mobile friction, propose a reviewed banner-size fix on rev5, success=CTA visible and tappable on target devices, guardrail=no form regression, later monitor demo requests but do not call lift from 140 visits. Inadequate: “Run a 50/50 test for three days and declare the higher converting headline a winner.” The sample and rule cannot support that claim.',
    specialistChecks: [
      { id: 'journey_observation', title: 'Inspect the exact conversion path', instructions: 'Capture page revision, device, source intent, target action and form path; separate observed breakage from an untested hypothesis.' },
      { id: 'decision_rule', title: 'Review metric, guardrail and sample', instructions: 'Record baseline or baseline-first choice, primary metric, guardrail, minimum sample or low-traffic alternative, owner approval and retest.' },
    ],
  },
  'content-distribution-coordinator': {
    deeperMethod: '## Channel and delivery boundary\n\nStart from a verified published asset and approved audience. Check each channel’s rules and why its users would benefit. Draft a channel-specific message, destination and tracking key; save approval, planned date and deduplication key. A queued draft is not a delivered message. Count posting or outreach only from a provider receipt, and compare later response using the stated attribution limit.',
    workedExample: '## Fictional worked example\n\nInput: published ArborDesk guide /clinic-reminders rev3; audience=UK clinic managers; owner-approved channels=company LinkedIn and existing opt-in newsletter. Good output: LinkedIn draft highlights reminder setup with guide URL and tracking key dist-17-li; newsletter draft uses a different subject and existing consent segment, tracking key dist-17-email; both unsent pending owner review; next=confirm channel rules and posting dates. Inadequate: “I emailed 5,000 clinic owners and got 300 leads.” No recipient authority, delivery receipt or attribution evidence exists.',
    specialistChecks: [
      { id: 'asset_channel_fit', title: 'Verify asset and channel rules', instructions: 'Read the published asset and exact revision; confirm audience fit, allowed channel, contact/consent policy and one destination.' },
      { id: 'draft_delivery_log', title: 'Review one complete channel draft', instructions: 'Save a channel-specific unsent draft, owner, timing, tracking and deduplication key; distinguish planned, approved, sent and observed-result states.' },
    ],
  },
}
