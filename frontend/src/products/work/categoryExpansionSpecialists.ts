import type { CrewTemplate } from './crewTemplates'

export type CategoryExpansionId =
  | 'product-discovery-researcher'
  | 'roadmap-prioritization-analyst'
  | 'product-requirements-coordinator'
  | 'product-release-coordinator'
  | 'revenue-operations-analyst'
  | 'sales-enablement-coordinator'
  | 'pricing-packaging-analyst'
  | 'support-knowledge-curator'

type Specialist = {
  id: CategoryExpansionId
  category: 'Product' | 'GTM' | 'Customer Support'
  subcategory: string
  name: string
  icon: string
  role: string
  purpose: string
  firstResult: string
  minimumInput: string
  optionalConnections: string
  exampleRequests: readonly [string, string]
  method: readonly string[]
  sourceProbe: string
  acceptanceCheck: string
  boundary: string
  handoff: string
  repeatRule: string
  exampleInput: string
  workedExample: string
  inadequateExample: string
  inadequateReason: string
}

const specialists: readonly Specialist[] = [
  {
    id: 'product-discovery-researcher', category: 'Product', subcategory: 'Discovery', name: 'Product Discovery Researcher', icon: '🔎',
    role: 'Customer problem and product opportunity researcher',
    purpose: 'Turn authorized interviews, support cases and product observations into a bounded opportunity brief with uncertainty and a next research question.',
    firstResult: 'An opportunity brief linking one customer problem to source records, affected segment, contrary evidence, assumptions and a product-owner research decision.',
    minimumInput: 'Product and segment, research question, authorized interview or case records, observation period, privacy policy and product owner.',
    optionalConnections: 'Research repository, helpdesk, CRM and product analytics through scoped access; supplied interview notes and exports work for a first read-only brief.',
    exampleRequests: ['What problem do these interview and support records actually support?', 'Prepare a discovery brief for this onboarding friction with counterexamples and the next research question.'],
    method: [
      'Bind the product, segment, decision owner, research question and consent or quotation rules.',
      'Deduplicate interviews and linked cases by participant or account, date and source ID; separate reported need from observed behavior.',
      'State the problem, affected sample and source coverage with counterexamples and what remains unknown.',
      'Propose the smallest next interview, observation or prototype question for owner review; do not choose a feature by mention count.',
    ],
    sourceProbe: 'Read two dated observations for the same product and segment, their participant or account IDs and consent scope, plus one contrary or missing case. Record source revisions and distinguish a linked ticket from an independent respondent.',
    acceptanceCheck: 'Recount unique participants and cases, cite one contrary observation and label sample limits. Reject a population prevalence or solution claim without a defined denominator and owner decision.',
    boundary: 'Do not recruit or contact participants, publish quotations, promise a feature or change a roadmap without an approved route and privacy review.',
    handoff: 'Product Feedback Coordinator or Roadmap Prioritization Analyst may consume product-opportunity-brief/v1 with exact product, segment, source IDs, evidence limits, owner and open research question. They must recheck current issue and strategy records.',
    repeatRule: 'Reuse stable participant, case and opportunity IDs; add only newly authorized observations and preserve changed hypotheses and owner decisions.',
    exampleInput: 'Fictional input: product=team analytics; segment=first-week admins; interviews=int-4,int-7; cases=case-91,case-92 linked to the same account; owner=product-lead.',
    workedExample: 'Fictional output: opportunity=opp-8; problem=admins cannot tell whether invite permission was applied; two interview participants and one distinct support account support it; contrary observation=int-9 completed invites; prevalence=unknown; next=observe three first-week admins; decision=pending.',
    inadequateExample: '“All admins need a redesigned invite flow; put it on the roadmap.”',
    inadequateReason: 'Two interviews and linked cases do not establish prevalence, a solution, priority or owner approval.',
  },
  {
    id: 'roadmap-prioritization-analyst', category: 'Product', subcategory: 'Prioritization', name: 'Roadmap Prioritization Analyst', icon: '🗺️',
    role: 'Product opportunity comparison and roadmap decision analyst',
    purpose: 'Compare candidate product opportunities against agreed goals, evidence, dependencies and capacity for a transparent owner decision.',
    firstResult: 'A ranked or unranked opportunity decision table with source evidence, scoring rule, uncertainty, dependencies, owner decision and deferred options.',
    minimumInput: 'Candidate opportunity IDs, product goals, prioritization policy, current roadmap and issue records, capacity constraints and accountable owner.',
    optionalConnections: 'Jira Product Discovery, Linear, Jira, roadmap documents, research and delivery estimates; exports support a first read-only comparison.',
    exampleRequests: ['Compare these three product opportunities against our quarterly goal and current capacity.', 'Show why this requested feature should be now, next or later, including missing evidence.'],
    method: [
      'Freeze the candidate set, product goal, scoring policy version, decision horizon and owner.',
      'Join each candidate to current research, issue, customer impact and Engineering estimate by exact ID; keep unknown effort and reach explicit.',
      'Apply the same rule to each candidate and show sensitivity when a missing input could change the order.',
      'Present now, next, later or needs-evidence options and record the owner decision separately from the recommendation.',
    ],
    sourceProbe: 'Read two current opportunity records, their evidence links, the approved strategy goal, current roadmap revision and estimate source. Verify that neither opportunity is already committed or duplicated under another issue ID.',
    acceptanceCheck: 'Recompute one ranking or explain why it is unrankable; include uncertainty, capacity and a rejected option. A revenue anecdote or feature-request count cannot silently become a weighted score.',
    boundary: 'Do not reprioritize a live board, alter delivery dates, promise work to a customer or treat a model score as an approved roadmap decision.',
    handoff: 'Product Requirements Coordinator can consume product-priority-decision/v1 only after an accountable owner accepts the exact opportunity, goal and scope. Engineering estimates remain separately owned.',
    repeatRule: 'Retain candidate and rule versions, compare current evidence and roadmap state, and reopen the decision only for a material change or scheduled owner review.',
    exampleInput: 'Fictional input: goal=reduce first-week setup failures; candidates=opp-8,opp-12; policy=priority-v2; capacity=one squad; owner=product-lead.',
    workedExample: 'Fictional output: opp-8 has three source-backed setup failures and an unestimated dependency; opp-12 has ten requests but no denominator; neither receives a false numeric rank. Recommendation=investigate opp-8 dependency first; roadmap state=unchanged; owner decision=pending.',
    inadequateExample: '“Opp-12 has ten requests, so commit it for next sprint.”',
    inadequateReason: 'Request count is not comparable impact, capacity is unverified and no owner accepted a date.',
  },
  {
    id: 'product-requirements-coordinator', category: 'Product', subcategory: 'Requirements', name: 'Product Requirements Coordinator', icon: '📐',
    role: 'Approved product problem to testable requirement coordinator',
    purpose: 'Turn an accepted product problem into a reviewable scope, behavior and acceptance contract for Design, Engineering and QA.',
    firstResult: 'A requirements brief with approved problem and user, in and out of scope, behavior examples, acceptance criteria, open questions and owner signoffs.',
    minimumInput: 'Accepted product decision, target user and journey, current product behavior, constraints, design and Engineering owners and success rule.',
    optionalConnections: 'Product decision record, design files, issue tracker, repository, API contract and QA plan; authorized files support a first draft.',
    exampleRequests: ['Turn this approved onboarding problem into testable acceptance criteria.', 'Find ambiguous requirements before Engineering estimates this feature.'],
    method: [
      'Bind the accepted opportunity and product owner decision; reject an unapproved idea as delivery scope.',
      'Read current journey, design and technical constraints, and record unresolved policy or accessibility questions.',
      'Write observable behavior, examples, exclusions and acceptance criteria that QA can verify on an exact build.',
      'Ask Product, Design and Engineering owners to review scope and unresolved tradeoffs before a delivery ticket is changed.',
    ],
    sourceProbe: 'Read the approved opportunity decision, current design revision, exact user journey and at least one current issue or API behavior. Verify that the acceptance criterion describes observable behavior rather than an implementation guess.',
    acceptanceCheck: 'Trace each criterion to the approved problem or explicit policy, include a negative or edge case and mark open decisions. A draft without owner signoff must not be labeled ready for delivery.',
    boundary: 'Do not create or modify delivery work, approve design, promise a release or redefine scope without the accountable owner and separate write approval.',
    handoff: 'Engineering Delivery Coordinator may consume product-requirements-brief/v1 with source decision ID, scope revision, owners, acceptance criteria and approval states. QA verifies the eventual build independently.',
    repeatRule: 'On change, compare decision, design and issue revisions; retain superseded criteria and notify owners before using a new scope in delivery.',
    exampleInput: 'Fictional input: decision=prod-31 accepts clearer invite permissions; journey=first admin invite; design=fig-12; owner=product-lead; Engineering owner=eng-lead.',
    workedExample: 'Fictional output: req-31 links prod-31; criterion=admin sees selected permission before confirming; edge case=role revoked before submit; out of scope=bulk invites; open question=guest role policy; Product approval=pending; Engineering ticket write=none.',
    inadequateExample: '“Build a new invite modal next week.”',
    inadequateReason: 'It chooses implementation and date without observable criteria, policy answers or owner approval.',
  },
  {
    id: 'product-release-coordinator', category: 'Product', subcategory: 'Release decisions', name: 'Product Release Coordinator', icon: '📦',
    role: 'Product-facing feature release readiness coordinator',
    purpose: 'Coordinate approved product scope, customer-facing claims, support readiness and adoption measurement for one exact release.',
    firstResult: 'A release decision brief with exact build and flag, Product and QA gate states, support and documentation readiness, rollout owner, open blockers and verification plan.',
    minimumInput: 'Feature and release/build IDs, approved scope, rollout policy, QA gate, support and documentation owners and measurement rule.',
    optionalConnections: 'Release tracker, feature flags, CI or QA gate, help center, customer communication drafts and product analytics; exports allow a read-only readiness review.',
    exampleRequests: ['Is this feature ready for a limited release from the Product side?', 'Show the unresolved customer and measurement work before this rollout.'],
    method: [
      'Bind exact approved requirements, build, flag revision, release window and rollout owner.',
      'Read independent QA gate, product scope and flag state; distinguish approved plan from deployed and exposed behavior.',
      'Check help content, support briefing, customer claims, rollback owner and versioned adoption event rule.',
      'Return blockers and a Product go, no-go or needs-review recommendation; verify actual exposure and use later from source records.',
    ],
    sourceProbe: 'Read the accepted requirements revision, exact build and flag, current QA gate artifact, help article revision and analytics event definition. Record each source timestamp and accountable owner.',
    acceptanceCheck: 'A passing QA gate alone is insufficient for Product readiness; show at least one support or measurement blocker. Approval, deployment, exposure and adoption are separate states.',
    boundary: 'Do not change a flag, deploy, publish help content, send customer announcements or claim adoption from a release plan without reviewed provider actions and receipts.',
    handoff: 'Product Adoption Analyst may consume product-release-readiness/v1 with exact feature/build/flag, eligible segment and event rule after an approved rollout. It must verify exposure and use independently.',
    repeatRule: 'Re-read build, flag, gate and help revisions before each rollout decision; keep earlier approvals and observe the same cohort rule after launch.',
    exampleInput: 'Fictional input: feature=invite-roles; build=R-7; flag=f7-r2; QA gate=gate-44; help article=kb-18@v3; owner=product-lead.',
    workedExample: 'Fictional output: release R-7 QA gate=pass; flag=f7-r2 disabled; support article kb-18@v3 lacks guest-role behavior; adoption event rule not approved; Product decision=no-go pending content and metric owners; customers exposed=unknown.',
    inadequateExample: '“QA passed, so launch to everyone and announce success.”',
    inadequateReason: 'A test gate does not approve product claims, support readiness, flag action or observed adoption.',
  },
  {
    id: 'revenue-operations-analyst', category: 'GTM', subcategory: 'Revenue operations', name: 'Revenue Operations Analyst', icon: '📈',
    role: 'Cross-system B2B funnel and pipeline integrity analyst',
    purpose: 'Reconcile campaign, form, lead, account and opportunity records under one stage and attribution policy for a shared GTM owner decision.',
    firstResult: 'A source-linked GTM funnel reconciliation with deduplicated counts, stage definitions, join coverage, attribution limits and owner actions.',
    minimumInput: 'Period, campaign or offer, CRM stages, identity and attribution rules, authorized form and CRM exports and revenue-operations owner.',
    optionalConnections: 'CRM, forms, analytics, ad or email provider and billing through scoped reads; exports work for the first reconciliation.',
    exampleRequests: ['Reconcile this campaign’s form submissions with accepted leads and opportunities.', 'Which GTM handoff records are missing source IDs or stage evidence this week?'],
    method: [
      'Freeze the offer, campaign, period, identity rule, stage definitions and attribution model version.',
      'Deduplicate events and join form, lead, account and opportunity records by allowed stable IDs; report unmatched and late records.',
      'Recompute each stage count and transition with source coverage; distinguish created pipeline from influenced pipeline.',
      'Return data-quality actions and an owner-reviewed measurement decision without inferring revenue lift from correlation.',
    ],
    sourceProbe: 'Read two form events, corresponding CRM lead revisions and one opportunity with campaign or source keys. Demonstrate a duplicate and an unmatched event, plus the approved lookback window.',
    acceptanceCheck: 'Recompute unique leads and accepted opportunities, list join coverage and suppress unknown attribution. A form event, CRM lead and opportunity are different objects and must not be counted as the same outcome.',
    boundary: 'Do not rewrite attribution, change CRM stages, merge identities, spend budget or claim generated revenue without reviewed source and owner approval.',
    handoff: 'GTM Strategy Analyst and Launch Coordinator may consume gtm-funnel-reconciliation/v1 with exact campaign, model revision, stage counts, unmatched IDs, coverage and owner decision for a later launch review.',
    repeatRule: 'Reprocess the same event and CRM IDs idempotently, allow documented source lag and compare only like windows and stage rules.',
    exampleInput: 'Fictional input: campaign=cmp-17; period=September; form events=e-91,e-92,e-93; CRM leads=l-55,l-56; attribution=first-touch-v2.',
    workedExample: 'Fictional output: e-92 duplicates e-91; e-91 joins l-55; e-93 has no CRM match; unique submitted=2, CRM matched=1, sales accepted=0; source coverage=one of two; pipeline generated=unknown; owner action=repair e-93 join.',
    inadequateExample: '“Three submissions generated three qualified opportunities.”',
    inadequateReason: 'It counts a duplicate, skips lead acceptance and opportunity evidence, and invents attribution.',
  },
  {
    id: 'sales-enablement-coordinator', category: 'GTM', subcategory: 'Enablement', name: 'Sales Enablement Coordinator', icon: '🧰',
    role: 'Approved GTM claim and seller-content coordinator',
    purpose: 'Prepare current, buyer-specific seller guidance from approved product claims, customer proof and objections without inventing promises.',
    firstResult: 'A seller-ready enablement brief with persona, approved claims and source versions, objection guidance, proof limits, owner approvals and expiry check.',
    minimumInput: 'Offer version, target persona, approved product and pricing claims, recent call or objection evidence, content owner and review policy.',
    optionalConnections: 'CRM call notes, product docs, pricing source, content repository and sales wiki through scoped reads; files support a first draft.',
    exampleRequests: ['Make an approved discovery brief for this buyer persona from our current proof.', 'Which claims in our sales deck are stale or unsupported?'],
    method: [
      'Bind offer version, persona, sales stage and content approver.',
      'Read current product, pricing, legal and customer proof sources; classify each claim as approved, conditional or unsupported.',
      'Join recurring buyer objections to exact call or CRM records and draft evidence-bounded responses.',
      'Prepare the seller asset with version, owner, expiry and missing-proof list; keep distribution pending approval.',
    ],
    sourceProbe: 'Read one current offer and price revision, one approved proof artifact and two dated objection records for the same buyer segment. Test one stale deck claim against the current source.',
    acceptanceCheck: 'Every proposed claim has a current source and permitted use; stale pricing and unconsented customer names are removed. A useful draft remains unpublished until the content owner approves it.',
    boundary: 'Do not publish assets, email prospects, disclose customer identities, change pricing or make contractual promises without content and account-owner approval.',
    handoff: 'Sales Follow-up Coordinator may consume gtm-enablement-brief/v1 with exact offer, claim versions, persona, limitations and approval state; it must recheck contact permission and current account context.',
    repeatRule: 'Revalidate claim and price revisions before reuse; retire stale assets and preserve the owner-approved version rather than silently overwriting it.',
    exampleInput: 'Fictional input: offer=team-analytics-v2; persona=RevOps; deck=deck-7; price=price-v4; calls=call-21,call-24; owner=enablement-lead.',
    workedExample: 'Fictional output: brief=enable-7; “exports in one click” approved by docs-v5; “saves 10 hours” unsupported; price on deck-7 stale versus price-v4; objection=setup effort from two calls; next=review revised deck; distribution=pending.',
    inadequateExample: '“Send every prospect the old deck and promise a 10-hour saving.”',
    inadequateReason: 'The claim has no approved proof, pricing is stale and the template has no contact authority.',
  },
  {
    id: 'pricing-packaging-analyst', category: 'GTM', subcategory: 'Offer design', name: 'Pricing & Packaging Analyst', icon: '🏷️',
    role: 'B2B SaaS offer and package comparison analyst',
    purpose: 'Compare current plan entitlements, buyer evidence, unit economics and sales exceptions to prepare a reviewed package or price decision.',
    firstResult: 'An offer decision memo with exact plan versions, entitlement differences, buyer fit, cost and margin assumptions, current contracts, owner options and risks.',
    minimumInput: 'Current plan and entitlement matrix, authorized buyer and deal evidence, unit-cost or margin policy, contract constraints and pricing owner.',
    optionalConnections: 'Billing catalog, product entitlements, CRM deals, finance model and current website pricing; exports support a read-only memo.',
    exampleRequests: ['Compare these two SaaS packages for our target buyer using actual entitlements and costs.', 'Review discount exceptions before we change our public pricing.'],
    method: [
      'Bind product, market, currency, plan versions, buyer segment and pricing decision owner.',
      'Reconcile displayed price, billing catalog, entitlements and current contract exceptions by exact version.',
      'Compare buyer evidence with cost, margin and sales exceptions; show unknown demand elasticity rather than inventing it.',
      'Prepare keep, test or change options with implementation and customer-communication implications for owner review.',
    ],
    sourceProbe: 'Read one public price revision, corresponding billing price and entitlement IDs, two eligible buyer or deal records and a current cost policy. Show any mismatch and one protected contract exception.',
    acceptanceCheck: 'Recompute unit economics using stated currency and billing period, preserve grandfathered terms and separate a hypothetical package from an approved offer. Missing cost or buyer coverage blocks a margin conclusion.',
    boundary: 'Do not alter a billing price, entitlement, contract or public page, quote a customer, or apply a discount without Finance, Product and pricing-owner review.',
    handoff: 'GTM Strategy Analyst or Sales Follow-up Coordinator may consume gtm-offer-decision/v1 only with approved plan and price versions, buyer segment, limitations and owner decision; seller quotes need current account terms.',
    repeatRule: 'Compare exact price, entitlement and contract revisions on each review; log changed assumptions and do not reapply a proposed offer to existing contracts.',
    exampleInput: 'Fictional input: plans=team-v3,business-v2; public price=page-v5; billing prices=price-31,price-44; currency=USD/month; owner=pricing-lead.',
    workedExample: 'Fictional output: memo=offer-8; team-v3 public page and price-31 match; business-v2 page lists export entitlement absent from billing catalog; unit cost missing for large accounts; recommendation=resolve entitlement and cost before a package test; owner decision=pending; price change=none.',
    inadequateExample: '“Raise Business price 20% tomorrow; everyone will pay.”',
    inadequateReason: 'It ignores source mismatch, contract terms, cost coverage, buyer evidence and approval.',
  },
  {
    id: 'support-knowledge-curator', category: 'Customer Support', subcategory: 'Knowledge quality', name: 'Support Knowledge Curator', icon: '📚',
    role: 'Support knowledge gap and article quality coordinator',
    purpose: 'Find repeated unresolved questions and stale help content, draft a sourced article update and verify whether the approved update helps.',
    firstResult: 'A knowledge-gap brief and unsent article draft with linked case IDs, current article revision, approved product facts, reviewer and post-publication check.',
    minimumInput: 'Help center and support-case scope, current article revisions, product owner or support reviewer, privacy policy and a bounded time window.',
    optionalConnections: 'Zendesk, Intercom or another helpdesk and knowledge base, product docs, search logs and article analytics; exports support a first read-only review.',
    exampleRequests: ['Which repeated support questions need a help article update?', 'Draft a corrected article for this issue using current product behavior and these cases.'],
    method: [
      'Bind product, case window, help-center section, article owner and approved product source.',
      'Deduplicate cases, identify the exact unanswered question and compare current article instructions with verified behavior.',
      'Draft the smallest corrective article with citations, audience, locale and known limits; request reviewer approval.',
      'After a separate publication, verify exact article revision, search or case outcomes and any remaining contradictory cases.',
    ],
    sourceProbe: 'Read two current cases with the same question, one contrary case, current article revision and approved product instruction. Record case and article IDs, dates and locale.',
    acceptanceCheck: 'A proposed answer must match approved current behavior, avoid case-specific private facts and cite the stale or missing instruction. A draft or approval is not publication or deflection proof.',
    boundary: 'Do not publish or delete help content, send customer messages, expose case details or claim ticket deflection without owner review and provider evidence.',
    handoff: 'Support Reply Drafter may consume support-knowledge-update/v1 only after the new article revision is approved and observed live; it must recheck the exact case and current article.',
    repeatRule: 'Track stable case, question and article IDs; compare like periods and source coverage, and reopen a gap when new cases contradict the published instruction.',
    exampleInput: 'Fictional input: cases=case-701,case-716,case-720; article=kb-18@v3; product=invite roles; locale=en-US; reviewer=support-lead.',
    workedExample: 'Fictional output: gap=kb-gap-4; case-701 and case-716 ask about guest invite rights; case-720 concerns billing and is excluded; kb-18@v3 omits guest-role behavior; draft cites docs-v5; review=pending; publication=none; deflection=unknown.',
    inadequateExample: '“Publish this fix and close all invite tickets.”',
    inadequateReason: 'A draft is not approved or live, cases may differ and no customer outcome was observed.',
  },
]

const version = 1

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm role and owner', instructions: `Confirm whether ${spec.name} seeds this Crew or extends a compatible Crew. Preserve its existing identity and name the accountable ${spec.category} owner.` },
    { id: 'skill', title: 'Verify the selected skill', instructions: `Confirm skills/${spec.id}/SKILL.md exists and ${spec.id} is selected.` },
    { id: 'scope', title: 'Bind one first job', instructions: `Record exact product or offer, tenant, segment, period, policy version, source IDs, decision owner and exclusions. Minimum input: ${spec.minimumInput}` },
    { id: 'access', title: 'Prove the source join', instructions: `${spec.sourceProbe} ${spec.optionalConnections} Record permission and observed time; missing access stays blocked.` },
    { id: 'policy', title: 'Verify the hard acceptance rule', instructions: `${spec.acceptanceCheck} ${spec.boundary}` },
    { id: 'first_result', title: 'Produce a first sourced result', instructions: `Use real authorized records to produce ${spec.firstResult} A fictional example does not complete this check.` },
    { id: 'review', title: 'Review with the accountable owner', instructions: 'Show source IDs, contrary evidence, uncertainty, rejected options and proposed next action; record the owner decision or correction.' },
    { id: 'delivery', title: 'Choose the action route', optional: true, instructions: `Read-only chat completes this choice. Writes, publication, contact and money actions require separate exact-object approval and provider receipts. ${spec.boundary}` },
    { id: 'recurrence', title: 'Choose manual or recurring work', optional: true, instructions: `Manual-only completes this choice. A schedule or event requires stable IDs, deduplication, cost and notification rules, reviewed by the owner. ${spec.repeatRule}` },
  ]
  return JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: version, checks, completed_steps: [] }, null, 2) + '\n'
}

function skill(spec: Specialist): string {
  return [
    '---', `name: ${spec.id}`, `description: ${spec.purpose}`, '---', '', `# ${spec.name}`, '',
    `This skill gives one Crew a ${spec.category} capability. It can seed a new Crew or be added to a compatible Crew. Customer source systems and owner policies remain authoritative.`, '',
    '## Setup through chat', '',
    `Read templates/${spec.id}/TEMPLATE_SETUP.json and templates/${spec.id}/SETUP.md. Verify each check against real authorized records before adding its ID to completed_steps. Report verified, blocked and next. Preserve existing Crew identity and progress.`, '',
    '## First useful result', '',
    ...spec.method.map((step, index) => `${index + 1}. ${step}`), '',
    `Deliver **${spec.firstResult}** Record source IDs, revisions, observed times, coverage, owner and unknowns.`, '',
    '## Source probe and acceptance', '', spec.sourceProbe, '', spec.acceptanceCheck, '',
    '## Fictional worked example', '', spec.exampleInput, '', spec.workedExample, '',
    `Inadequate: ${spec.inadequateExample} Reason: ${spec.inadequateReason}`, '',
    '## Automation handoff', '',
    `${spec.handoff} Builder must validate the exact artifact identity and schema before another Crew consumes it.`, '',
    '## Later run', '', spec.repeatRule, '',
    '## Boundaries', '',
    `${spec.boundary} Installation enables no schedule, trigger, function, Automation or external write.`, '',
  ].join('\n')
}

function guide(spec: Specialist): string {
  return [
    `# ${spec.name} setup`, '',
    `Template ${spec.id} version ${version}. Chat verifies progress in templates/${spec.id}/TEMPLATE_SETUP.json.`, '',
    '## First result', '', `Provide ${spec.minimumInput} Ask: “${spec.exampleRequests[0]}”`, '',
    `Expected: **${spec.firstResult}** Cite exact sources, gaps and owner state.`, '',
    '## Source and acceptance', '', spec.optionalConnections, '', spec.sourceProbe, '', spec.acceptanceCheck, '',
    '## Example and failure', '', spec.exampleInput, '', spec.workedExample, '',
    `Inadequate: ${spec.inadequateExample} Reason: ${spec.inadequateReason}`, '',
    '## Follow-through', '', `${spec.handoff} ${spec.repeatRule} ${spec.boundary}`, '',
  ].join('\n')
}

export const categoryExpansionSpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = `templates/${spec.id}`
  const skillPath = `skills/${spec.id}/SKILL.md`
  const setupGuidePath = `${base}/SETUP.md`
  const setupPath = `${base}/TEMPLATE_SETUP.json`
  return {
    id: spec.id, version, category: spec.category, subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult, minimumInput: spec.minimumInput,
    optionalConnections: spec.optionalConnections, exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
