import type { CrewTemplate } from './crewTemplates'

export type SalesExpansionId = 'sales-call-briefing' | 'proposal-drafter' | 'pipeline-analyst' | 'deal-follow-through-coordinator'

type Spec = {
  id: SalesExpansionId
  name: string
  icon: string
  subcategory: string
  role: string
  purpose: string
  firstResult: string
  minimumInput: string
  optionalConnections: string
  examples: readonly [string, string]
  method: readonly string[]
  evidence: string
  boundary: string
  repeatRule: string
  exampleInput: string
  goodExample: string
  badExample: string
  badReason: string
}

const specs: readonly Spec[] = [
  {
    id: 'sales-call-briefing', name: 'Sales Call Briefing Assistant', icon: '📞', subcategory: 'Sales meetings',
    role: 'Evidence-led seller call preparation assistant',
    purpose: 'Prepare an exact-meeting brief from approved CRM and account context, separating verified facts from discovery questions.',
    firstResult: 'A meeting brief with exact attendee and meeting references, approved account facts, open questions, relevant product proof, and a seller review decision.',
    minimumInput: 'Meeting or opportunity ID, company identity, approved CRM notes and offer material, seller, meeting time, and source freshness rule.',
    optionalConnections: 'Calendar and CRM through scoped MCPs or supplied exports; public account research is optional and does not imply permission to contact.',
    examples: ['Brief me for tomorrow’s call with this account and show what is still unknown.', 'Compare the meeting invite with CRM notes and tell me which claims I should verify live.'],
    method: [
      'Bind the exact meeting, opportunity, company, seller, attendees, time zone and offer; resolve company-domain ambiguity before using public research.',
      'Read current authorized invitation and CRM stage/notes. Separate prospect statements, seller notes, approved product claims and public observations by source and date.',
      'Identify the buyer’s expressed problem, prior commitments, decision participants and unknowns. Do not invent budget, authority, urgency or intent.',
      'Prepare a short agenda, evidence-backed talking points and prioritized discovery questions; flag stale or conflicting facts for the seller.',
      'Ask the seller to review the brief and corrections before using it in a meeting. Do not alter the invite, CRM or customer thread.',
    ],
    evidence: 'Every claimed fact cites a meeting, CRM, approved product or public source ID and observation time; questions are labeled as questions.',
    boundary: 'No calendar change, CRM write, customer contact, recording access or promise about product behavior is authorized by this template.',
    repeatRule: 'Re-read the exact meeting and opportunity before each call; keep meeting ID and notes revision, report changed attendees/stage/commitments, and retire the brief when the meeting is cancelled.',
    exampleInput: 'Fictional input: meeting mtg_42 with Acme on 2026-10-02; CRM opportunity opp_42 has a dated note about reporting delays; one attendee role is unknown; approved case study CS-7 covers reporting setup.',
    goodExample: 'Fictional brief: mtg_42/opp_42, seller=s_2; verified issue=reporting delay from CRM note n_12; attendee role=unknown; agenda=confirm current reporting process, show approved CS-7 only if relevant; questions=who owns the report and how is success measured; owner review=pending.',
    badExample: '“Acme has approved a $50k budget and the VP will sign after this call.”',
    badReason: 'Neither budget nor decision authority is in the supplied records, and the meeting brief must not convert a hypothesis into a commitment.',
  },
  {
    id: 'proposal-drafter', name: 'Proposal Drafter', icon: '📑', subcategory: 'Proposals',
    role: 'Reviewed B2B proposal preparation assistant',
    purpose: 'Turn documented discovery and current approved offer/pricing rules into a scoped, unsent proposal with explicit gaps and approval owners.',
    firstResult: 'An unsent proposal draft with account and opportunity IDs, source-backed scope, deliverables, pricing basis, assumptions, exclusions, open questions, approval status, and version.',
    minimumInput: 'Opportunity ID, discovery notes version, approved product and pricing material, commercial owner, proposal format, and scope/claim rules.',
    optionalConnections: 'CRM, document repository and pricing system through scoped MCPs or supplied files; a first draft can use authorized exports.',
    examples: ['Draft a proposal from these reviewed discovery notes and current price list; flag unknowns.', 'Check this proposal against the agreed scope and mark unsupported claims before I send it.'],
    method: [
      'Bind exact account, opportunity, currency, offer, discovery revision, pricing version, proposal owner and recipient context.',
      'Extract buyer-stated goals, constraints and agreed scope with note citations; separate seller suggestions and unconfirmed assumptions.',
      'Use only current approved product capabilities, price/discount rules and legal language. Recompute line-item arithmetic and flag unavailable terms.',
      'Draft deliverables, timeline assumptions, exclusions, price basis and acceptance questions; attach a source or owner question to every material claim.',
      'Return an unsent draft for commercial and, when needed, legal/finance review. Sending, signature, CRM stage changes and discounts require separate routes.',
    ],
    evidence: 'Cite discovery note spans, approved offer and price version, currency and arithmetic; keep requested versus approved discounts distinct.',
    boundary: 'Do not invent scope, price, discount, guarantee, delivery date or legal terms; do not email, publish, sign or update CRM through installation.',
    repeatRule: 'Retain opportunity and proposal version, re-read changed discovery/pricing before revision, show the delta to the owner, and avoid overwriting an approved or sent proposal.',
    exampleInput: 'Fictional input: opp_42 discovery n_13 requests 10 seats and monthly reporting; approved price v3 is USD 20/seat/month, no discount approved; implementation timeline is not confirmed.',
    goodExample: 'Fictional draft P-42 v1: 10 seats × USD 20 = USD 200/month before taxes; reporting scope cites n_13 and approved capability C-4; timeline=needs owner confirmation; discount=none approved; commercial review=pending; delivery=unsent.',
    badExample: '“We guarantee a two-week implementation for $150/month and have sent the proposal.”',
    badReason: 'The timeline and discount lack approval, the price conflicts with the current list, and no send receipt exists.',
  },
  {
    id: 'pipeline-analyst', name: 'Pipeline Analyst', icon: '📊', subcategory: 'Pipeline',
    role: 'Source-linked B2B opportunity pipeline analyst',
    purpose: 'Explain real pipeline movement and stale deals from comparable CRM snapshots without presenting guesses as a forecast.',
    firstResult: 'A dated pipeline movement brief with stable opportunity IDs, stage transitions, amount and currency, age, owner, data coverage, risks, and decisions needed.',
    minimumInput: 'Authorized CRM snapshot or export, stage definitions, reporting window, currency policy, owners, and at least one comparison snapshot for movement claims.',
    optionalConnections: 'HubSpot, Salesforce or another CRM via scoped MCP or export; a single snapshot supports current-state review but not movement claims.',
    examples: ['What changed in pipeline since last Friday, with exact opportunity IDs?', 'Which opportunities are stale under our policy, and who should review them?'],
    method: [
      'Bind exact CRM account, pipeline, stages, reporting cutoff, currency conversion rule, owner and source snapshot revisions.',
      'Join prior/current snapshots on stable opportunity IDs, not company names. Separate new, advanced, regressed, won, lost, removed and unchanged records.',
      'Compute amounts by stage and currency; do not sum mixed currencies without a dated approved conversion rule. Flag changed amount and missing/duplicated IDs.',
      'Apply owner-defined stale and forecast criteria, checking next activity and close-date changes; distinguish observed state from seller judgment.',
      'Produce a source-linked movement brief and owner questions. Require owner review before CRM changes, notifications or recurring executive reporting.',
    ],
    evidence: 'Cite prior/current CRM snapshots, exact opportunity IDs, stage and amount fields, observed times, coverage gaps and definitions.',
    boundary: 'Do not fabricate a win probability, convert stage to revenue, contact prospects, move stages or claim a reliable forecast from incomplete or incomparable snapshots.',
    repeatRule: 'Compare with the saved prior snapshot under the same stage/currency definitions; record changed definitions, deduplicate by opportunity ID, and retain owner decisions for unresolved cases.',
    exampleInput: 'Fictional input: opp_11 was Discovery USD 12,000 at snapshot S1 and Proposal USD 12,000 at S2; opp_12 remains Discovery with no next activity; owner stale rule=14 days, last activity=20 days ago.',
    goodExample: 'Fictional brief: opp_11 advanced Discovery→Proposal with unchanged USD 12,000 (S1/S2); opp_12 stale by owner rule, owner=s_3, next=confirm next activity; forecast=not computed; coverage=two comparable snapshots.',
    badExample: '“Pipeline grew $12,000 and opp_12 will close this month.”',
    badReason: 'A stage move is not new pipeline value, and the close claim has no source or owner-approved forecast rule.',
  },
  {
    id: 'deal-follow-through-coordinator', name: 'Deal Follow-through Coordinator', icon: '📌', subcategory: 'Pipeline',
    role: 'Owner-reviewed B2B opportunity next-step coordinator',
    purpose: 'Turn a sourced opportunity into an owned next-step decision or a verified signed-deal handoff without silently changing CRM or contacting a customer.',
    firstResult: 'An exact-opportunity action register or signed-deal handoff with current CRM state, seller and receiving owner, source-backed scope, approval state and verification plan.',
    minimumInput: 'For pipeline follow-through, a validated exception plus current opportunity and contact records; for a signed handoff, the exact CRM opportunity, executed agreement, purchased scope, entitlement rule and receiving CS owner.',
    optionalConnections: 'CRM, agreement and entitlement sources, plus authorized email/calendar history through scoped MCPs or exports. Read-only review needs current records; sends and CRM updates need separate approved routes.',
    examples: ['Review these stale opportunities and prepare next-step decisions for each seller.', 'Check this signed deal against its current agreement and prepare a bounded CS handoff; do not provision or contact the customer.'],
    method: [
      'Bind exact tenant, CRM account, pipeline, opportunity, seller, report window and source revision from a validated pipeline brief.',
      'Re-read current opportunity stage, status, amount, next activity, last contact, replies, meeting and opt-out state before proposing any action.',
      'Apply the owner-defined stale rule to the current record; distinguish an observed gap from a seller judgment or missing history. Block duplicate and already-owned actions.',
      'Prepare investigate, seller-review, no-action or approved-route options with a stable action key, due rule and source references; keep proposed versus owner-accepted state explicit.',
      'Present the exact register for seller review. Any CRM update, message or meeting change requires fresh source checks, exact approval and a provider receipt in a separate route.',
      'For a signed-deal handoff, verify the executed contract revision, purchased scope, first-value promise, entitlement/provisioning rule and receiving CS owner. Emit sales-cs-handoff/v1 as ready or blocked; Closed Won alone is insufficient.',
    ],
    evidence: 'For pipeline work cite the validated brief, current opportunity revision, activity and prior-contact sources. For handoff cite CRM, executed agreement, first-value clause and entitlement sources with exact revisions and observation times. Keep owner decision and any provider receipt separate.',
    boundary: 'Do not infer buyer intent from stale stage age, treat a draft as sent, create duplicate tasks, move a stage, contact a prospect, report a forecast or promise onboarding from CRM status without approved evidence.',
    repeatRule: 'Keep the same opportunity and action or handoff keys, re-read current CRM, contract, entitlement and activity as relevant, close superseded suggestions, and count actual action only from a matching provider receipt.',
    exampleInput: 'Fictional input: opp_12 is Discovery at snapshot S2, has no next CRM activity and last recorded contact was 20 days ago; owner stale rule is 14 days. Seller=s_3; no reply or opt-out is recorded in the authorized export.',
    goodExample: 'Fictional register A-12: opp_12/S2, stale under 14-day rule; propose seller review of next contact because current reply history coverage is incomplete; owner decision=pending; CRM write=none; message=unsent; recheck=before any approved action.',
    badExample: '“The buyer lost interest, so I moved opp_12 to Closed Lost and sent a final email.”',
    badReason: 'Stage age does not prove intent, the source coverage is incomplete, and no exact seller approval or provider receipts authorize those actions.',
  },
]

function checklist(spec: Spec): string {
  const checks = [
    { id: 'identity', title: 'Confirm Crew role and owner', instructions: `Confirm whether ${spec.name} is the Crew’s primary role or a supporting capability. Preserve existing identity and name the accountable owner.` },
    { id: 'skill', title: 'Verify selected skill', instructions: `Confirm skills/${spec.id}/SKILL.md exists and ${spec.id} is selected for this Crew.` },
    { id: 'scope', title: 'Bind exact sales scope', instructions: `Record business, source account, opportunity or meeting, offer, currency/timezone, first job and source versions. Minimum input: ${spec.minimumInput}` },
    { id: 'access', title: 'Probe authorized sources', instructions: `Read one representative current source or export and record IDs, observation time and gaps. ${spec.optionalConnections}` },
    { id: 'policy', title: 'Confirm claim and action policy', instructions: `Record approved claims, pricing or stage definitions as applicable, owner approval, recipient/contact rules, and duplicate key. ${spec.boundary}` },
    { id: 'first_result', title: 'Produce a sourced first result', instructions: `Produce ${spec.firstResult} ${spec.evidence} A fictional example does not complete setup.` },
    { id: 'review', title: 'Review with sales owner', instructions: 'Show the real result, source coverage, calculations, unknowns and proposed next step. Record owner corrections or explicit decision.' },
    { id: 'delivery', title: 'Choose chat-only or an action route', optional: true, instructions: 'Chat-only completes this choice. Any send, CRM change or calendar action needs a separately approved exact-object route and provider receipt.' },
    { id: 'recurrence', title: 'Choose manual or recurring work', optional: true, instructions: `Manual-only completes this choice. A schedule or authenticated event needs reviewed scope, deduplication, retries, cost and notification rules. ${spec.repeatRule}` },
  ]
  return JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: 1, checks, completed_steps: [] }, null, 2) + '\n'
}

function skill(spec: Spec): string {
  return [
    '---', `name: ${spec.id}`, `description: ${spec.purpose}`, '---', '', `# ${spec.name}`, '',
    'This reusable Sales capability can seed a Crew or be added to one with a compatible owner and access scope. It carries no customer account or credential.', '',
    '## Setup in chat', '',
    `Read templates/${spec.id}/TEMPLATE_SETUP.json and templates/${spec.id}/SETUP.md. Verify checks against real authorized records before adding IDs to completed_steps. Preserve existing Crew identity and checklist progress. Report verified, blocked and next.`, '',
    '## First useful result', '',
    ...spec.method.map((step, index) => `${index + 1}. ${step}`), '',
    `Deliver **${spec.firstResult}** ${spec.evidence}`, '',
    '## Fictional worked example', '', spec.exampleInput, '', spec.goodExample, '',
    `Inadequate: ${spec.badExample} Reason: ${spec.badReason}`, '',
    '## Later run', '', spec.repeatRule, '',
    '## Boundaries', '', `${spec.boundary} Installation enables no schedule, trigger, function, Automation, message or account write.`, '',
  ].join('\n')
}

function guide(spec: Spec): string {
  return [
    `# ${spec.name} setup`, '', `Template ${spec.id} version 1. Progress lives in templates/${spec.id}/TEMPLATE_SETUP.json and is verified in Crew chat.`, '',
    '## First result', '', `Provide ${spec.minimumInput} Ask: “${spec.examples[0]}”`, '',
    `Expected: **${spec.firstResult}** ${spec.evidence}`, '',
    '## Source and action choice', '', `${spec.optionalConnections} An export can support the first read-only result. ${spec.boundary}`, '',
    '## Repeat policy', '', `${spec.repeatRule} Choose manual-only or review and test a real source, permission, owner, deduplication rule and first run before recurrence.`, '',
  ].join('\n')
}

export const salesExpansion: readonly CrewTemplate[] = specs.map(spec => {
  const base = `templates/${spec.id}`
  const skillPath = `skills/${spec.id}/SKILL.md`
  const setupPath = `${base}/TEMPLATE_SETUP.json`
  const setupGuidePath = `${base}/SETUP.md`
  return {
    id: spec.id, version: 1, category: 'Sales', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult, minimumInput: spec.minimumInput,
    optionalConnections: spec.optionalConnections, exampleRequests: spec.examples, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
