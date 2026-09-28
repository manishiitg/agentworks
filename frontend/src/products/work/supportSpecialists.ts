import type { CrewTemplate } from './crewTemplates'

export type SupportSpecialistId = 'support-triage-assistant' | 'support-reply-drafter' | 'escalation-coordinator' | 'feedback-review-analyst'

type Specialist = {
  id: SupportSpecialistId
  name: string
  icon: string
  subcategory: string
  role: string
  purpose: string
  firstResult: string
  minimumInput: string
  optionalConnections: string
  exampleRequests: readonly [string, string]
  method: readonly string[]
  evidence: string
  boundary: string
  handoff: string
  repeatRule: string
  sourceProbe: string
  acceptanceCheck: string
  exampleInput: string
  workedExample: string
  inadequateExample: string
  inadequateReason: string
}

const specialists: readonly Specialist[] = [
  {
    id: 'support-triage-assistant', name: 'Support Triage Assistant', icon: '📥', subcategory: 'Case intake',
    role: 'Customer support case intake and routing coordinator',
    purpose: 'Turn authorized inbound support cases into deduplicated, policy-prioritized owner queues with source-linked next actions.',
    firstResult: 'A case queue with stable IDs, customer and channel scope, issue summary, priority rationale, duplicate state, response deadline, owner, and next evidence.',
    minimumInput: 'Support inbox or case export, service and priority policy, product scope, customer identity rule, response deadlines, and triage owner.',
    optionalConnections: 'Helpdesk such as Zendesk or Intercom, shared inbox, product status, CRM account view, and issue tracker when authorized; an export supports a first read-only queue.',
    exampleRequests: ['Triage these new support cases and show which need an urgent owner decision.', 'Find duplicate reports of this login problem and prepare one case summary for the support lead.'],
    method: [
      'Confirm authorized inbox, product, customer and case identity rules, priority policy, time zone, owner, and response deadlines.',
      'Read current case status, thread, attachments, prior contacts, incident signals, and account context within the approved scope.',
      'Deduplicate by case and linked issue IDs; distinguish the customer’s reported symptom from a verified product incident.',
      'Apply the customer’s priority and escalation rules with explicit evidence and uncertainty; assign a proposed owner and next action.',
      'Return a source-linked queue and ask the support lead to review a representative case before any status or assignment write.',
    ],
    evidence: 'Every urgency and duplicate claim needs case IDs, observed times, policy rule, and supporting source references.',
    boundary: 'Do not change priority, assign a case, merge tickets, contact a customer, or declare an incident without the authorized route and owner review.',
    handoff: 'Support Case to Reviewed Resolution consumes support-case-triage/v1 with tenant, case, account, channel, policy, priority evidence, duplicate state, owner, response deadline, and source refs. Reply or escalation must re-read the current thread.',
    repeatRule: 'Reconcile the same case IDs, status and prior replies; add only new evidence, do not reopen resolved cases or create duplicate owner alerts.',
    sourceProbe: 'Read one current case thread, its status and prior replies, the proposed duplicate case, and the active priority/SLA policy. Record exact case IDs, account join, observation times, rule version and timezone. An incident claim requires a separate current incident source.',
    acceptanceCheck: 'Reproduce the priority and first-response deadline from the policy and case timestamps. Demonstrate that a shared symptom without an account or issue join is not silently merged, and that an unverified incident remains unverified.',
    exampleInput: 'Fictional input: case=helpdesk-701; account=acct-22; product=dashboard; policy=support-priority-v3; message=login returns 403; owner=support-lead.',
    workedExample: 'Fictional output: case=helpdesk-701; account=acct-22; observed=2026-09-26T09:10Z; symptom=403 after SSO; severity=P2 proposal under policy-v3 because three users report blocked access; linked cases=helpdesk-699 and helpdesk-700; incident=unverified; owner=auth-support; first-response due=2026-09-26T11:10Z; next=check current auth incident and draft acknowledgment; sources=case thread and status page.',
    inadequateExample: '“Critical outage; assign Engineering and tell everyone it is fixed soon.”',
    inadequateReason: 'It claims an incident and outcome without evidence, gives no policy basis or case IDs, and proposes unsanctioned assignment and messaging.',
  },
  {
    id: 'support-reply-drafter', name: 'Support Reply Drafter', icon: '💬', subcategory: 'First response',
    role: 'Grounded support response and knowledge assistant',
    purpose: 'Draft accurate, customer-safe replies from current approved help content and case facts while exposing unknowns and escalation triggers.',
    firstResult: 'An unsent reply draft with exact case and recipient reference, approved source citations, issue-specific steps, unknowns, owner review, and next-check rule.',
    minimumInput: 'Current case thread, approved help content and version, tone and claim policy, customer scope, escalation criteria, and reply owner.',
    optionalConnections: 'Helpdesk, approved knowledgebase, product status, CRM account context, and translation support if authorized; files support a first draft without live sending.',
    exampleRequests: ['Draft a reply to this billing question using only our approved help article and current account facts.', 'Prepare an acknowledgment for this login case that does not promise a fix date.'],
    method: [
      'Bind exact case, customer, recipient channel, current status, latest message, policy version, and owner.',
      'Read approved help articles and current product or billing facts; reject stale, conflicting, or account-inapplicable instructions.',
      'Draft the answer for the customer’s specific question with cited claims, safe troubleshooting, and clearly marked unknowns.',
      'Check escalation and sensitive-data boundaries, previous replies, opt-out or channel rules, and whether the case already resolved.',
      'Keep the draft unsent until the owner reviews exact recipient, text, links, and timing. Save a stable action ID for any separately authorized send.',
    ],
    evidence: 'A plausible answer is insufficient without current article or account references and a check that the draft addresses the latest case state.',
    boundary: 'Do not send, promise a refund or fix date, disclose another customer’s data, change the ticket, or run account actions from a draft.',
    handoff: 'Support Case to Reviewed Resolution consumes support-reply-draft/v1 linked to validated triage and the same current case. It requires source citations, recipient reference, unsent state, and owner approval before a separate delivery route.',
    repeatRule: 'Re-read the case thread, prior send receipts, and knowledge revision before another draft; stop when the customer replied, the case resolved, or contact policy changed.',
    sourceProbe: 'Read the latest authorized case message, exact recipient and channel, approved article revision, current product/account facts, and prior send receipts. Save each source ID and observed time; a copied article title is not proof the content is current or applicable.',
    acceptanceCheck: 'Review one complete unsent reply against the latest question and approved claims. Remove or flag a stale article instruction, an unverified fix date, a wrong-account fact or a duplicate send; record the exact owner and recipient review.',
    exampleInput: 'Fictional input: case=helpdesk-701; latest question=why does SSO return 403; article=kb:sso-troubleshooting@v5; status=investigating; owner=support-lead.',
    workedExample: 'Fictional output: draft=reply-701-1; case=helpdesk-701; recipient=helpdesk:requester-701; delivery_state=unsent; text=We are investigating the 403 after SSO. Please try the approved session reset steps in article v5; we will update this case after the auth check. No fix time is confirmed; citations=kb:sso-troubleshooting@v5, helpdesk-701@09:10; owner=support-lead; next_check=auth incident status.',
    inadequateExample: '“We fixed it. Clear your cookies and try again.”',
    inadequateReason: 'It invents a fix, omits the current case and article, and gives an unsourced instruction without review.',
  },
  {
    id: 'escalation-coordinator', name: 'Escalation Coordinator', icon: '🚨', subcategory: 'Escalations',
    role: 'Support escalation and cross-team handoff coordinator',
    purpose: 'Keep a high-impact case and its handoff current across Support, Engineering, Billing, and Customer Success with owned decisions and closure evidence.',
    firstResult: 'A bounded escalation brief with case and account IDs, impact evidence, policy threshold, owner handoff, deadlines, decision ledger, and next customer update.',
    minimumInput: 'Case and account IDs, current thread, escalation policy, impact evidence, responsible teams, customer contact owner, and response deadline.',
    optionalConnections: 'Helpdesk, incident or issue tracker, service telemetry, CRM account view, and team channels with scoped read or reviewed write access.',
    exampleRequests: ['Prepare an Engineering escalation for this customer issue with exact impact and missing evidence.', 'Which owner action is blocking this open escalation, and when is the next customer update due?'],
    method: [
      'Bind case, account, issue or incident ID, priority policy, current status, customer impact, owners, and confidentiality scope.',
      'Build a dated timeline from case, product, incident, and team records; label symptoms, hypotheses, and verified causes separately.',
      'Apply the escalation threshold and identify the receiving owner, exact question, needed evidence, due time, and customer update owner.',
      'Track accepted, declined, and pending handoffs with source receipts; a posted message alone is not an accepted owner assignment.',
      'Return a reviewed customer update proposal and closure criteria; verify the customer-facing state and fix or workaround before closing.',
    ],
    evidence: 'A high-priority label is not proof of customer impact or Engineering acceptance; cite actual case, telemetry, and owner decision records.',
    boundary: 'Do not page a team, alter severity, promise resolution, disclose protected customer data, or close a case without the authorized route and responsible owner.',
    handoff: 'Support Case to Reviewed Resolution consumes support-escalation-brief/v1 tied to the same case and account, policy threshold, receiving owner, handoff receipt, update deadline, and verification rule. Customer Success may receive a bounded account summary only when permitted.',
    repeatRule: 'Re-read current case, incident, ownership, and customer reply state; advance the same escalation ID, preserve earlier decisions, and avoid repeat pages or updates.',
    sourceProbe: 'Read the current case and impact evidence, escalation threshold, linked incident or issue, receiving-team record and customer-update deadline. Record the accepted, declined or pending handoff receipt separately from a sent page or message.',
    acceptanceCheck: 'Show the policy threshold and evidence for one escalation, receiving owner decision, due time and customer update owner. A message without receiving-owner acceptance stays pending; closure requires an affected-case retest or approved workaround and a separate case-owner decision.',
    exampleInput: 'Fictional input: case=helpdesk-701; account=acct-22; linked incident=inc-18; policy=support-escalation-v2; next customer update due=13:00Z.',
    workedExample: 'Fictional output: escalation=esc-701; case=helpdesk-701; account=acct-22; impact=three users unable to sign in from case records, wider reach unknown; incident=inc-18 investigating; receiving owner=auth-oncall accepted at 10:25Z via incident note; Support owner=support-lead; next update=13:00Z; closure=successful affected-account retest plus owner decision; customer draft remains unsent.',
    inadequateExample: '“Engineering is handling it; close the ticket.”',
    inadequateReason: 'It lacks an accepted owner handoff, current impact, customer update and retest evidence.',
  },
  {
    id: 'feedback-review-analyst', name: 'Feedback & Review Analyst', icon: '🗣️', subcategory: 'Feedback intelligence',
    role: 'Customer feedback and public review analyst',
    purpose: 'Turn authorized support feedback and public reviews into sourced themes, product owner decisions, and reviewed response drafts.',
    firstResult: 'A feedback brief with deduplicated source IDs, theme and affected segment, volume with denominator, representative quotes or paraphrases, owner, and reviewable next action.',
    minimumInput: 'Review or feedback export, product and period scope, channel policy, response owner, sensitive-data rules, and theme decision criteria.',
    optionalConnections: 'Helpdesk tags, survey or review platforms, product issue tracker, CRM segments, and approved public reply channel; exports support read-only analysis.',
    exampleRequests: ['Group this month’s onboarding feedback into source-backed themes and show what Product should review.', 'Draft a response to this public review for my approval without sharing account details.'],
    method: [
      'Confirm feedback sources, period, product, market, allowed quotations, privacy rule, response policy, and owners.',
      'Normalize and deduplicate records by stable feedback, review, case, and account IDs; separate unique reporters from total mentions.',
      'Cluster specific themes with representative source refs and observation times; report counts and source coverage without inventing prevalence.',
      'Flag urgent harm or active support cases for the correct owner; link themes to existing product issues when exact evidence supports the join.',
      'Return a product decision queue and optional unsent public reply drafts with approved tone, no private details, and a separate send review.',
    ],
    evidence: 'A theme needs representative source IDs, volume and denominator, period, and limits; one loud review is not a population-wide trend.',
    boundary: 'Do not publish a review reply, delete feedback, change a product issue, expose customer identity, or claim a trend without review and observed source coverage.',
    handoff: 'Feedback to Product Action can consume feedback-theme-brief/v1 with period, source coverage, deduplicated IDs, theme, count and denominator, issue link, owner, and reviewed action. A separate response route needs exact approval and provider receipt.',
    repeatRule: 'Keep stable theme and feedback IDs, compare like periods and source coverage, carry unresolved owner actions, and avoid replying twice to the same review.',
    sourceProbe: 'Read one bounded feedback export with source IDs, response coverage, period and segment; join a linked support case by exact ID and inspect the current product issue before claiming a match. Record unique reporters, total mentions and excluded duplicate IDs.',
    acceptanceCheck: 'Recalculate one theme numerator and its source-specific denominator, show a duplicate and an excluded record, and apply the quotation/privacy policy. A public reply stays unsent; a small or partial sample cannot become a population-wide trend.',
    exampleInput: 'Fictional input: 40 survey replies and 15 support cases for September onboarding; product=team analytics; approved source IDs and response policy=reviews-v2.',
    workedExample: 'Fictional output: theme=confusing invite permissions; 9 unique reporters among 40 surveyed respondents, plus 3 linked support cases; source refs=survey:7, survey:18, case:811; coverage=one customer segment only; owner=onboarding-product; action=review invite copy and permission guidance; public reply draft=unsent; next=compare after a reviewed change.',
    inadequateExample: '“Everyone hates onboarding; reply that we have fixed it.”',
    inadequateReason: 'It invents prevalence and a fix, omits denominator and source coverage, and treats an unsent response as authorized.',
  },
]

const supportTemplateVersion = 2

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm support role and owner', instructions: 'Confirm whether ' + spec.name + ' is this Crew’s primary role or an added capability. Preserve existing Crew identity and name the case or feedback owner.' },
    { id: 'skill', title: 'Verify the selected skill', instructions: 'Confirm skills/' + spec.id + '/SKILL.md exists and ' + spec.id + ' is selected for this Crew.' },
    { id: 'scope', title: 'Set case and customer scope', instructions: 'Record product, support channel, customer and case identity, time zone, privacy boundary, owner, and first job. Minimum input: ' + spec.minimumInput },
    { id: 'access', title: 'Prove the role-specific source join', instructions: spec.sourceProbe + ' ' + spec.optionalConnections },
    { id: 'policy', title: 'Verify the role-specific acceptance rule', instructions: spec.acceptanceCheck + ' Record the customer policy revision and accountable review owner. Unknown stays unknown.' },
    { id: 'first_result', title: 'Produce a first sourced result', instructions: 'Use real authorized records to produce ' + spec.firstResult + ' ' + spec.evidence + ' A fictional example does not complete this check.' },
    { id: 'review', title: 'Review the case or feedback result', instructions: 'Show exact source links, unknowns, owner, proposed next action, and any customer-facing draft. Record owner corrections and approval decision.' },
    { id: 'delivery', title: 'Choose case and message routes', optional: true, instructions: 'Choose read-only chat or separately authorized ticket updates, escalation, and message delivery. Read-only chat completes this choice. ' + spec.boundary },
    { id: 'recurrence', title: 'Choose repeat and Automation route', optional: true, instructions: 'Choose manual-only or a reviewed authenticated event or schedule with stable IDs and deduplication. Manual-only completes this choice. ' + spec.repeatRule },
  ]
  return JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: supportTemplateVersion, checks, completed_steps: [] }, null, 2) + '\n'
}

function skill(spec: Specialist): string {
  return [
    '---', 'name: ' + spec.id, 'description: ' + spec.purpose, '---', '',
    '# ' + spec.name, '',
    'This skill gives one Crew the ' + spec.name + ' capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s support systems as sources and reviewed action destinations.', '',
    '## Setup through chat', '',
    'Read templates/' + spec.id + '/TEMPLATE_SETUP.json and templates/' + spec.id + '/SETUP.md. Verify each check with real customer records before adding its ID to completed_steps. Preserve progress and report verified, blocked, and next. Chat-only and manual-only are valid optional decisions.', '',
    '## First useful result', '',
    ...spec.method.map((step, index) => String(index + 1) + '. ' + step), '',
    'Deliver **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Source probe and acceptance', '', spec.sourceProbe, '', spec.acceptanceCheck, '',
    '## Follow-through', '', spec.repeatRule, '',
    '## Fictional worked example', '', spec.exampleInput, '', spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Automation handoff', '',
    spec.handoff + ' Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Recheck current source state.', '',
    '## Boundaries', '',
    spec.boundary + ' Installation enables no schedule, trigger, function, Automation, case write, notification, or customer message. Never copy another Crew’s credentials.', '',
  ].join('\n')
}

function guide(spec: Specialist): string {
  return [
    '# ' + spec.name + ' setup', '',
    'Template ' + spec.id + ' version ' + supportTemplateVersion + '. Progress lives in templates/' + spec.id + '/TEMPLATE_SETUP.json and is verified in Crew chat.', '',
    '## First result', '',
    'Provide ' + spec.minimumInput + ' Ask: “' + spec.exampleRequests[0] + '”', '',
    'Expected output: **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Fictional example and failure', '', spec.exampleInput, '', spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Source and connection choice', '',
    spec.optionalConnections + ' ' + spec.sourceProbe, '',
    '## Setup acceptance', '', spec.acceptanceCheck, '',
    '## Automation and recurring work', '',
    'A support Automation may connect triage, reply, and escalation, or feed feedback themes to Product. Builder must verify Crew bindings, handoffs, a manual case, and owner-approved run policy. ' + spec.repeatRule + ' ' + spec.boundary, '',
  ].join('\n')
}

export const supportSpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = 'templates/' + spec.id
  const skillPath = 'skills/' + spec.id + '/SKILL.md'
  const setupGuidePath = base + '/SETUP.md'
  const setupPath = base + '/TEMPLATE_SETUP.json'
  return {
    id: spec.id, version: supportTemplateVersion, category: 'Customer Support', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
