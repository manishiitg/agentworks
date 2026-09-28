import type { CrewTemplate } from './crewTemplates'
import { websiteGrowthDepth } from './websiteGrowthDepth'

export type WebsiteGrowthSpecialistId =
  | 'seo-analyst'
  | 'search-opportunity-mapper'
  | 'content-brief-writer'
  | 'content-page-builder'
  | 'website-publishing-coordinator'
  | 'search-console-optimizer'
  | 'traffic-engagement-analyst'
  | 'ai-visibility-analyst'
  | 'landing-page-optimizer'
  | 'content-distribution-coordinator'

type Specialist = {
  id: WebsiteGrowthSpecialistId
  name: string
  icon: string
  role: string
  purpose: string
  firstResult: string
  minimumInput: string
  optionalConnections: string
  exampleRequests: readonly [string, string]
  method: readonly string[]
  evidence: string
  boundary: string
  deeperMethod?: string
  workedExample?: string
  specialistChecks?: readonly { id: string; title: string; instructions: string }[]
  automationOutput?: string
}

const specialists: readonly Specialist[] = [
  {
    id: 'seo-analyst', name: 'SEO Analyst', icon: '🔎', role: 'Technical SEO analyst for this website',
    purpose: 'Find and prioritize observable crawl, indexability, metadata, internal-link, and page-experience issues.',
    firstResult: 'A page-level SEO issue list with evidence, impact, effort, and uncertainty.',
    minimumInput: 'Website URL, approved crawl scope, market, and key conversion pages.',
    optionalConnections: 'Search Console for indexing and query evidence; repository or CMS for reviewed changes.',
    exampleRequests: ['Audit our public pages for technical SEO issues and cite each affected URL.', 'Which three SEO fixes would you review first, and what evidence supports them?'],
    method: ['Confirm the canonical domain, crawl boundary, market, and important pages.', 'Inspect a bounded sample of pages, robots directives, sitemap, headings, metadata, canonicals, links, mobile layout, and observable page experience.', 'Separate directly observed issues from Search Console-only indexation claims.', 'Rank issues by likely user/search impact, confidence, effort, and affected page.', 'Produce a reviewable issue list and a small verification plan for each proposed fix.'],
    evidence: 'For each issue include the inspected URL, observation, source or capture time, confidence, affected page set, and a retest.',
    boundary: 'Do not claim that a page is indexed from a public fetch; do not change robots, redirects, canonicals, or site content without review.',
    automationOutput: 'When invoked as the SEO Intelligence technical step, return one plain JSON object matching seo-issue-list/v1 at the path supplied by the Crew step. Include canonical site, market, locale, device, crawl scope, dated page sources, stable issue IDs, observed state, confidence, owner action, retest and explicit unknown indexation. Builder must run the blocking issue validator before another Crew uses it.',
  },
  {
    id: 'search-opportunity-mapper', name: 'Search Opportunity Mapper', icon: '🧭', role: 'Buyer-question and search opportunity researcher',
    purpose: 'Map target buyers’ questions and search intent to existing pages and evidence-backed content gaps.',
    firstResult: 'A ranked buyer-question-to-page map with gaps and source links.',
    minimumInput: 'Offer, target audience, market, website pages, and buyer questions or research scope.',
    optionalConnections: 'Search Console and approved keyword research for actual demand signals.',
    exampleRequests: ['Map our buyers’ main questions to the pages we already have.', 'Find the most useful unanswered questions for our new website.'],
    method: ['Confirm the offer, audience, geography, buying stage, and site scope; read the strategist brief when this is a Website Growth Loop run.', 'Collect buyer questions from approved owner notes, customer conversations, site copy, or authorized search data; label each origin and deduplicate equivalent questions.', 'Inspect representative product, service, FAQ, and resource pages, then map each question to a page that actually answers it, a weak answer to improve, or a documented gap.', 'Use authorized search data when available; otherwise mark demand and ranking as unknown.', 'Rank opportunities by buyer relevance, answer gap, evidence quality, and feasible next action; explain why an existing-page update or a new page is justified.'],
    evidence: 'Each opportunity has a stable ID, exact buyer question, source, intent, current page or gap, evidence URLs, confidence, next action, and reason for rank. Link the source strategist finding when present.',
    boundary: 'Do not invent search volume or ranking difficulty; avoid producing keyword lists detached from an audience need.',
    deeperMethod: `## Mapping and decision rules

Keep the candidate set bounded to the approved audience and offer. Cluster questions only when a single page can answer the same user need; retain distinct buying-stage or product questions. Mark each candidate **answered** when a page directly answers it, **weak answer** when a relevant page exists but leaves the decision question unresolved, or **gap** when no inspected page addresses it. A relevant title alone is not a complete answer.

Choose **update existing page** when the page already serves the same buyer and intent. Choose **new page** only when the user need is distinct enough to deserve its own page and the owner has useful source material. Choose **verify first** when the question or claimed demand has weak evidence. Prefer a high-relevance, evidenced gap with a feasible owner over a speculative high-volume idea. Record the reason for the order; do not create a numeric opportunity score without measured inputs.

Save stable question IDs so later runs can mark answered, deferred, or rejected questions without rediscovering them. On a repeat run, read the prior map and owner decisions; inspect changed pages and new evidence first. Report a delta and state when nothing has changed. Search Console rows may omit anonymized queries, so treat a query export as a partial view of buyer demand.`,
    automationOutput: 'When invoked as the Website Growth Loop search step, return one plain JSON object matching search-opportunity-list/v1. Do not wrap it in Markdown or add other commentary. Include a stable artifact_id and source_brief_artifact_id matching the validated strategist run, inspected URLs, exact question sources, stable IDs, evidence, confidence, unknown demand, and any strategist finding IDs so the Workflow can validate the saved response. For the distinct SEO Intelligence route, use seo-opportunity-list/v1 instead: set source_issue_artifact_id to the validated seo-issue-list/v1 artifact ID, cite exact blocking issue IDs, preserve site/market/locale/device, source each buyer question and page decision, keep demand unknown without authorized metrics, and leave publication pending a separate approved route. For AI Visibility Intelligence, use ai-citation-opportunity/v1 linked to the exact validated ai-visibility-snapshot/v1 ID, preserve site, question version, engine, surface and locale, cite a real page observation and approved fact, reproduce citation counts, and leave the content decision pending owner review with publication not_published.',
    workedExample: `## Worked opportunity map

Fictional owner context: ArborDesk sells appointment software to independent physiotherapy clinics in the UK. The site is newly launched. The strategist observed that the clinic page discusses scheduling but does not explain reminders. Two pages were inspected on 2026-09-25: \`https://arbordesk.example/\` and \`https://arbordesk.example/clinics\`. No Search Console data was available.

| ID | Buyer question and source | Page decision | Evidence and confidence | Next action / reason for order |
| --- | --- | --- | --- | --- |
| \`Q-001\` | “How do patient reminders work?” Owner-confirmed buyer question, linked to strategist finding \`F-001\`. | Weak answer on \`/clinics\`. | Page mentions scheduling but not reminders; observed on 2026-09-25. Confidence: medium until product facts are confirmed. Search demand: unknown. | First: update the existing clinic page after the owner confirms the feature. It serves the same buyer and intent; a separate page is premature. |
| \`Q-002\` | “Can patients reschedule themselves?” Hypothesis from a site-content gap; no customer or search evidence yet. | Gap in the two inspected pages; uninspected pages remain unknown. | Inspected URLs above; confidence: low. Search demand: unknown. | Verify with the owner and inspect the remaining site before proposing a page. |

Save a structured \`search-opportunity-list/v1\` artifact for the Loop with \`site_url\`, \`buyer_questions[]\`, evidence references, confidence, and proposed next action. Include the inspected scope and source date. The first result may be partial when buyer research is unavailable; label hypotheses rather than filling the list with invented demand.

**Review failure:** “physiotherapy software: 5,000 monthly searches, build a new page” fails without an authorized query source, the actual buyer question, an inspected existing-page answer, and a reason a new page is better.`,
    specialistChecks: [
      { id: 'question_sources', title: 'Verify buyer-question evidence', instructions: 'Save at least one exact buyer question from an owner, customer, approved research source, or a clearly labeled hypothesis. Link its origin and date; deduplicate equivalent phrasing. Do not turn an invented keyword volume into evidence.' },
      { id: 'mapping_decision', title: 'Review the page decision', instructions: 'Inspect the relevant page set and verify at least one question is classified answered, weak answer, or gap with a source reference. Explain update-existing versus new-page versus verify-first, then save the owner review or explicit blocker.' },
    ],
  },
  {
    id: 'content-brief-writer', name: 'Content Brief Writer', icon: '📝', role: 'Evidence-led website content brief writer',
    purpose: 'Turn an approved search or buyer opportunity into a useful page brief with claims, sources, internal links, and a success signal.',
    firstResult: 'A reviewable page brief with angle, outline, evidence, and open questions.',
    minimumInput: 'Approved topic or opportunity, buyer need, offer, source material, and brand guidance.',
    optionalConnections: 'Repository, CMS, Search Console, or research tools for richer source context.',
    exampleRequests: ['Write a page brief for this approved buyer question using our product sources.', 'Turn this search opportunity into a brief the content team can review.'],
    method: ['Confirm the buyer, question, page purpose, offer, and conversion action.', 'Inspect current site pages to avoid duplication and identify relevant internal links.', 'Gather attributable support for factual claims and list claims that still need verification.', 'Draft a useful angle, outline, key sections, examples, links, and calls to action.', 'Define a realistic success signal and request owner review before page production.'],
    evidence: 'The brief links every factual claim to a source or labels it for verification, and links the source opportunity and relevant site pages.',
    boundary: 'Do not fabricate testimonials, performance claims, customer stories, or search demand; a brief is not permission to publish.',
    automationOutput: 'When invoked as the Website Growth Loop content step, return one plain JSON content-brief/v1 object with artifact_id, exact source_search_artifact_id and source_question_id, owner approval, existing-page or new-page decision, claim IDs with verified source references or explicit verification gaps, outline and success signal. Do not advance a low-confidence hypothesis or an unapproved question to page drafting.',
  },
  {
    id: 'content-page-builder', name: 'Content Page Builder', icon: '📄', role: 'Reviewable website page drafter',
    purpose: 'Draft a useful, source-grounded website page from an approved brief without publishing it automatically.',
    firstResult: 'A page draft with source notes, internal links, and a clear next action.',
    minimumInput: 'Approved content brief, brand guide, existing page context, and intended page format.',
    optionalConnections: 'Repository or CMS for a draft or reviewable change; preview/browser tools for QA.',
    exampleRequests: ['Draft a page from this approved brief and show every unverified claim.', 'Create a reviewable product comparison page with our approved source material.'],
    method: ['Check the brief is approved and identify missing claims or assets.', 'Inspect nearby site pages for terminology, navigation, and internal-link opportunities.', 'Write clear page copy matching buyer intent, with useful structure and the agreed visitor action.', 'Keep source notes and open factual questions alongside the draft.', 'If authorized, prepare a preview or reviewable repository/CMS draft and verify links and rendering.'],
    evidence: 'Return the draft path or preview, brief version, source links, unverified claims, and a review checklist.',
    boundary: 'Do not publish, overwrite live pages, add fabricated proof, or claim SEO results from an unpublished draft.',
    automationOutput: 'When invoked as the Website Growth Loop page step, return one plain JSON reviewable-page-draft/v1 object with artifact_id, exact source_content_artifact_id and question ID, target URL, reviewable draft reference, sections citing only verified claim IDs, unresolved claim IDs, pending review, and publication_state not_published. A separate publication record is needed before measurement treats this as shipped.',
  },
  {
    id: 'website-publishing-coordinator', name: 'Website Publishing Coordinator', icon: '🚀', role: 'Reviewed website publication and verification coordinator',
    purpose: 'Carry an approved page draft through a separately authorized publication route and verify the exact live page before recording a shipped change.',
    firstResult: 'A publication readiness record with blockers, owner and exact target; after an approved release, a source-linked live change record.',
    minimumInput: 'Approved page draft and content brief, canonical target URL, site owner, CMS or deploy authority, release policy, and live-page verification method.',
    optionalConnections: 'CMS, repository/deploy provider and browser for authorized release and live checks; an owner-provided release receipt plus public-page inspection supports read-only verification.',
    exampleRequests: ['Check whether this approved page draft is ready for publication and list the blockers.', 'Verify that this exact approved page revision went live at the target URL.'],
    method: ['Match the approved brief, page artifact, question, target URL and exact draft revision.', 'Confirm factual review, required links, mobile layout, target action, accessibility basics, publication owner and rollback route.', 'Record the owner decision and separately authorized CMS/deploy action. A draft or approved review is not a publication receipt.', 'After release, inspect the canonical live URL and exact revision, links and target action; record the provider receipt and observation time.', 'Emit pending when approval, release or live verification is missing; only a verified record may feed distribution or outcome measurement.'],
    evidence: 'Bind draft and content artifact IDs, approval reference, provider publish or deploy receipt, exact URL/revision, observed live check and ship time.',
    boundary: 'Do not publish, overwrite a live page, change DNS or run a deploy from template selection. Do not call a draft or merged change shipped, or a live page a traffic success without measurement.',
    automationOutput: 'When invoked in Website Growth Loop, return one plain JSON shipped-change/v1 object with the exact page/content artifact IDs, source_draft_ref and question, site and target URL, action ID, owner, pending or verified state, and separate approval, provider publication receipt, live revision and inspection evidence. Distribution and measurement consume only verified state.',
  },
  {
    id: 'search-console-optimizer', name: 'Search Console Optimizer', icon: '📈', role: 'Search Console page and query optimization analyst',
    purpose: 'Find page/query opportunities in authorized Search Console data and propose bounded page improvements.',
    firstResult: 'A sourced page/query opportunity report with proposed title, copy, or link changes.',
    minimumInput: 'Authorized Search Console property or export, page/query date range, target market, and website URL.',
    optionalConnections: 'Search Console MCP, analytics, and repository or CMS for reviewed edits.',
    exampleRequests: ['Find pages with relevant impressions but weak engagement in this Search Console export.', 'Review these queries and propose natural page improvements with evidence.'],
    method: ['Confirm property, country, device, date range, data freshness, and page scope.', 'Group queries by intent and compare only compatible periods and dimensions.', 'Inspect the live pages before proposing title, copy, or internal-link changes.', 'Prioritize opportunities using actual impressions, clicks, CTR, position caveats, and business relevance.', 'Draft bounded edits and a follow-up measurement window.'],
    evidence: 'Each recommendation includes property, query/page rows, date range, page observation, change proposal, and comparison limitation.',
    boundary: 'Do not treat average position as a fixed ranking, invent query data, keyword-stuff copy, or publish without review.',
  },
  {
    id: 'traffic-engagement-analyst', name: 'Traffic & Engagement Analyst', icon: '📊', role: 'Website traffic and engagement measurement analyst',
    purpose: 'Explain relevant visitor, landing-page, and conversion changes from authorized analytics data.',
    firstResult: 'A sourced traffic and engagement readout with a prioritized next action.',
    minimumInput: 'Analytics export or connection, event definitions, reporting window, and primary visitor action.',
    optionalConnections: 'Analytics, Search Console, and a change log for stronger interpretation.',
    exampleRequests: ['Compare useful traffic and conversions this month with the prior comparable period.', 'Which landing pages changed, and what should we investigate next?'],
    method: ['Confirm property, consent coverage, events, filters, attribution, and comparison windows.', 'Calculate source, landing-page, engagement, and conversion metrics with denominators.', 'Flag data gaps, small samples, bot/internal traffic, seasonality, and instrumentation changes.', 'Connect observed changes to shipped work without claiming causality automatically.', 'Produce a concise readout with evidence, confidence, and one or two next checks.'],
    evidence: 'Include metric definitions, numerator and denominator, source property/export, date windows, page/source dimensions, and data-quality notes.',
    boundary: 'Do not mix Search Console clicks with analytics sessions or call an unmeasured change a traffic win.',
    automationOutput: 'When invoked in Website Growth Loop after a verified ship, return one plain JSON traffic-readout/v1 object with the exact shipped-change artifact ID and optional distribution artifact ID, site and page, source exports, property, metric and event definition, timezone, filters, comparable windows, eligible-session and action counts, observed directions, decision, owner and next evidence. Use baseline_first with null trend fields when no comparable window exists; never assert causality from timing alone.',
  },
  {
    id: 'ai-visibility-analyst', name: 'AI Visibility Analyst', icon: '✨', role: 'AI answer visibility and citation analyst',
    purpose: 'Test approved buyer questions across selected answer engines and report brand/citation gaps with repeatable evidence.',
    firstResult: 'A question-level AI visibility report with citations, gaps, and content actions.',
    minimumInput: 'Brand, competitors, buyer questions, selected answer engines, and test method.',
    optionalConnections: 'Browser or approved AI answer sources and analytics for downstream referral measurement.',
    exampleRequests: ['Test these buyer questions and show where our brand is cited or missing.', 'Compare the cited sources for our product category with named competitors.'],
    method: ['Confirm brand aliases, competitor set, buyer questions, locale, and answer engines.', 'Run a bounded, repeatable sample and retain question, engine, timestamp, answer, and cited URLs.', 'Separate brand mentions, citations, unsupported claims, and absent coverage.', 'Find source pages that could answer the question more clearly and accurately.', 'Propose useful content or authority improvements and a comparable retest method.'],
    evidence: 'Include exact tested question, engine and locale, test time, observed mention/citation, URL, and sampling limitation.',
    boundary: 'Do not infer stable rankings from one stochastic answer or promise inclusion in AI-generated answers.',
    automationOutput: 'When invoked as the AI Visibility Intelligence observation step, return one plain JSON ai-visibility-snapshot/v1 object at the Crew step path. Freeze canonical brand domains, question ID/text/version and source, engine, surface, locale and session method. Save at least two dated answer attempts with valid versus failed status, exact cited URLs and source captures. Recompute valid/failed, mention, brand citation and competitor citation counts. Claim only sample_observation, disclose stochastic limits, and let the Workflow run the blocking snapshot validator before another Crew consumes it.',
  },
  {
    id: 'landing-page-optimizer', name: 'Landing Page Optimizer', icon: '🎯', role: 'Landing-page conversion path analyst',
    purpose: 'Inspect one visitor journey and propose a bounded page revision or test grounded in evidence.',
    firstResult: 'A page-level conversion diagnosis and reviewable improvement hypothesis.',
    minimumInput: 'Landing page URL, audience/traffic intent, target action, and available behavior data.',
    optionalConnections: 'Analytics, session evidence, repository or CMS, and experiment platform.',
    exampleRequests: ['Audit this landing page for the visitor action we want.', 'Draft a small test to clarify the offer on our signup page.'],
    method: ['Confirm audience, traffic source, page intent, target action, and current measurement.', 'Inspect the full page and mobile path for clarity, proof, friction, accessibility, and loading issues.', 'Use authorized analytics or session data when present; distinguish observation from hypothesis.', 'Propose one bounded revision or experiment with a decision rule and required sample caveats.', 'Prepare a reviewable draft or test plan; compare results only after a valid measurement window.'],
    evidence: 'Link the page observation, supporting behavior data, hypothesis, exact proposed change, metric, and decision rule.',
    boundary: 'Do not claim uplift without an experiment or comparable data; do not deploy a page or test without approval.',
  },
  {
    id: 'content-distribution-coordinator', name: 'Content Distribution Coordinator', icon: '📣', role: 'Website content distribution planner',
    purpose: 'Find relevant channels and prepare reviewable distribution and outreach drafts for a published asset.',
    firstResult: 'A channel plan with audience fit, message drafts, and approval points.',
    minimumInput: 'Published asset, target audience, approved channels, brand voice, and contact policy.',
    optionalConnections: 'Social, email, community, or CRM connections for approved delivery and measurement.',
    exampleRequests: ['Plan how to share this published guide with the right audience.', 'Draft channel-specific posts for review without sending them.'],
    method: ['Confirm the asset is published and its factual claims and target audience are current.', 'Evaluate channel fit, community rules, and permitted contact sources.', 'Draft channel-specific summaries and messages with an appropriate next action.', 'Set review, timing, attribution, and response-handling expectations.', 'Prepare a distribution log and a follow-up measurement plan.'],
    evidence: 'Each channel recommendation states audience fit, source, draft message, destination, approver, and measurable follow-up.',
    boundary: 'Do not scrape contacts, spam communities, send messages, or post without explicit authorization and channel access.',
    automationOutput: 'When invoked in Website Growth Loop, return one plain JSON distribution-plan/v1 object tied to the exact verified shipped-change artifact ID and published URL. For each channel, include audience fit, rules reference, draft, unique tracking key, owner, approval state, delivery state and provider receipt only if approved and actually sent. An unsent plan is a valid result and must not claim delivery.',
  },
]

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm the specialist scope', instructions: `Confirm whether ${spec.name} is this Crew's primary role or a supporting capability. Its intended role is ${spec.role}. Record its scope and boundaries without overwriting an existing primary identity.` },
    { id: 'skill', title: 'Verify the specialist skill', instructions: `Confirm skills/${spec.id}/SKILL.md exists and ${spec.id} is selected for this Crew.` },
    { id: 'scope', title: 'Confirm site and owner scope', instructions: `Confirm the owner is authorized to assess the site, the canonical URL, target audience, market, and relevant pages. Record crawl and data boundaries.` },
    { id: 'inputs', title: 'Check minimum inputs', instructions: `Resolve: ${spec.minimumInput} If missing, explain what first result remains possible and do not invent data.` },
    { id: 'access', title: 'Test required access', instructions: `Read a representative authorized source or provided export needed for the first result. Optional connections: ${spec.optionalConnections} Record the chosen source, date range, and access limitation.` },
    ...(spec.specialistChecks || []),
    { id: 'first_result', title: 'Produce the first result', instructions: `Produce ${spec.firstResult} ${spec.evidence} Label assumptions and missing evidence.` },
    { id: 'review', title: 'Review the first result', instructions: 'Show the result to the owner, resolve corrections and priority choices, then record the agreed next action.' },
    { id: 'recurrence', title: 'Decide on recurring work', optional: true, instructions: 'Ask whether this specialist should remain chat-only or suggest a separate schedule, trigger, function, or multi-agent Automation. Choosing chat-only completes this decision. Configure and test any requested recurring action separately; never activate it by template selection.' },
  ]
  return `${JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: 1, checks, completed_steps: [] }, null, 2)}\n`
}

function skill(spec: Specialist): string {
  return `---
name: ${spec.id}
description: ${spec.purpose}
---

# ${spec.name}

This skill provides the ${spec.name} capability (${spec.role}). ${spec.purpose} When it is the Crew's primary role, lead with this method; when added to another Crew, apply the method under that Crew's existing identity. Work only within the owner's authorized website and data scope.

## Setup through chat

Read \`templates/${spec.id}/TEMPLATE_SETUP.json\` and \`templates/${spec.id}/SETUP.md\`. Verify each check before adding its ID to \`completed_steps\`; preserve all other progress. An explicit chat-only choice completes the optional recurrence check. Report what is verified, what is blocked, and the next action.

## First useful result

${spec.method.map((step, index) => `${index + 1}. ${step}`).join('\n')}

Deliver **${spec.firstResult}** ${spec.evidence}

${spec.deeperMethod || ''}

${spec.workedExample || ''}

${spec.automationOutput || ''}

## Boundaries

${spec.boundary} Never claim a measurement unavailable in the source data. Do not select an external account, publish, send outreach, start paid spend, or activate a schedule, trigger, function, or Automation merely because this template was added.
`
}

function guide(spec: Specialist): string {
  return `# ${spec.name} setup

This Crew tracks progress in \`templates/${spec.id}/TEMPLATE_SETUP.json\`. Setup is completed in chat after checks are verified.

## Start

Provide ${spec.minimumInput} Ask: “${spec.exampleRequests[0]}”

Expected first output: **${spec.firstResult}** ${spec.evidence}

## Optional connections

${spec.optionalConnections} Choose an account and scope in Crew Integrations only if needed. Test the actual source before marking access verified. Never copy another Crew's credentials.

${spec.workedExample || ''}

## Recurring work

A Crew schedule can repeat this specialist's own task. A separate Website Growth Loop Automation coordinates multiple Crew agents toward a measured goal. Either requires its own setup and run test. ${spec.boundary}
`
}

export const websiteGrowthSpecialists: readonly CrewTemplate[] = specialists.map(baseSpec => {
  const spec = { ...baseSpec, ...websiteGrowthDepth[baseSpec.id] }
  const base = `templates/${spec.id}`
  const skillPath = `skills/${spec.id}/SKILL.md`
  const setupGuidePath = `${base}/SETUP.md`
  const setupPath = `${base}/TEMPLATE_SETUP.json`
  return {
    id: spec.id, version: 1, category: 'Website Growth', name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
