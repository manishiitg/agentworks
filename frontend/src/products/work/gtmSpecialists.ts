import type { CrewTemplate } from './crewTemplates'

export type GTMSpecialistId = 'gtm-strategy-analyst' | 'launch-coordinator'

type Specialist = {
  id: GTMSpecialistId
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
    id: 'gtm-strategy-analyst', name: 'GTM Strategy Analyst', icon: '🎯', subcategory: 'Positioning',
    role: 'B2B go-to-market strategy and positioning analyst',
    purpose: 'Turn the offer, buyer evidence, market, and constraints into a sourced launch brief with a testable pipeline goal.',
    firstResult: 'A launch brief with approved offer and audience, evidence-backed buyer problem, message hypotheses, chosen channels, owner, metric definitions, and open decisions.',
    minimumInput: 'Offer and product claims, target buyer and market, customer or research evidence, available site and channels, budget, owner, and qualified-pipeline goal.',
    optionalConnections: 'Public and authorized customer research, website analytics, Search Console, CRM, campaign reports, and product material; supplied documents support a first read-only brief.',
    exampleRequests: ['Create a launch brief for our new B2B SaaS offer using these customer interviews and website pages.', 'Which audience and message should we test first, and what would count as qualified pipeline?'],
    method: [
      'Confirm the specific offer, approved product claims, target customer, geography, decision owner, exclusions, budget, and launch window.',
      'Separate observed customer statements, source-backed market facts, and message hypotheses; record source links and observation dates.',
      'Compare plausible segments on problem evidence, reachability, sales fit, and constraints without inventing market size or buyer intent.',
      'Select a first audience, message, channel hypothesis, primary action, qualified lead definition, and baseline or baseline-first measurement rule.',
      'Return the brief with rejected options, open questions, owner decision, and a bounded first test. Review exact claims before public use.',
    ],
    evidence: 'Every material buyer or product claim needs an owner-approved source; a proposed campaign goal is not an observed pipeline outcome.',
    boundary: 'Do not publish positioning, launch campaigns, spend budget, scrape personal contacts, or treat research interest as contact permission without an authorized route and review.',
    handoff: 'Launch to Qualified Pipeline can consume gtm-launch-brief/v1 with launch ID, offer version, audience, market, message claim references, channels, budget bounds, owner decision, and metric definitions. Launch Coordination must validate the same offer and owner before using it.',
    repeatRule: 'On a later run, compare new customer and pipeline evidence against the recorded hypothesis and metric rule; preserve previous decisions and avoid rewriting history from one campaign result.',
    sourceProbe: 'Read the current approved offer and claim source, at least two dated buyer observations for the proposed segment, and the CRM rule for a qualified demo. Record source IDs, audience fit, channel reach and baseline coverage; a website visit is not a buyer interview or a qualified lead.',
    acceptanceCheck: 'Trace one proposed audience and message claim to exact buyer/product evidence, show one rejected segment, and define qualified pipeline with a sales acceptance rule and baseline or baseline-first window. Unsupported lift or market-size claims remain hypotheses.',
    exampleInput: 'Fictional input: offer=team-analytics-v2; market=US B2B SaaS; interviews=research-set-4; website=/teams; owner=gtm-lead; budget cap=$2,000; goal=qualified demo requests.',
    workedExample: 'Fictional output: launch=launch-24; offer=team-analytics-v2; audience=RevOps leaders at 20–200 seat SaaS firms; problem=manual renewal reporting supported by interview:7 and interview:11; message hypothesis=reduce weekly reporting effort, pending claim review; channels=site page and approved customer newsletter; qualified demo=request meets ICP and sales acceptance; baseline=not yet available; owner=gtm-lead; next=approve claims and channel plan.',
    inadequateExample: '“Target all companies; run ads; revenue will double.”',
    inadequateReason: 'It lacks a bounded buyer, evidence, approved claim, budget, metric definition, owner decision, and valid baseline.',
  },
  {
    id: 'launch-coordinator', name: 'Launch Coordinator', icon: '🚀', subcategory: 'Launch operations',
    role: 'Cross-channel B2B launch and pipeline coordinator',
    purpose: 'Keep approved launch assets, channel actions, owners, dates, dependencies, and observed lead signals in one source-linked ledger.',
    firstResult: 'A launch action ledger with approved asset revisions, channel and owner, dates, dependency state, source receipts, first lead signals, and next review.',
    minimumInput: 'Approved launch brief, asset inventory, release dates, channel owners, distribution and contact policy, lead source, CRM stage rules, and measurement owner.',
    optionalConnections: 'CMS or repository, email or ad platforms, analytics, form source, CRM, calendar, and project tracker; exports can support an initial read-only ledger.',
    exampleRequests: ['Which launch assets and approvals are still blocking our release next week?', 'Connect approved campaign activity to actual inbound enquiries and show what needs owner review.'],
    method: [
      'Bind launch ID, offer version, approved audience/message, target dates, channel owner, budget and contact rules from the reviewed brief.',
      'Inventory each asset and channel action with stable ID, revision, approval, dependency, scheduled time, and observed provider state.',
      'Distinguish draft, approved, published or sent, and verified states. Require a provider receipt or public observation for execution.',
      'Read lead/form and CRM source records, join only on explicit campaign or source IDs, deduplicate events, and label attribution unknown when IDs are missing.',
      'Return blocked actions, first lead signals, owner decisions, cost, and the next check; hand qualified leads to Sales under the customer’s fit and contact policy.',
    ],
    evidence: 'A launch asset marked done in a project board is not proof it was published or sent; a form event is not a qualified lead or booked meeting.',
    boundary: 'Do not publish assets, send outreach, enroll contacts, spend on ads, alter CRM stages, or claim pipeline lift without approved actions and observed provider and sales records.',
    handoff: 'Launch to Qualified Pipeline can consume launch-signal-register/v1 with launch and offer IDs, campaign and source IDs, channel receipts, inbound event IDs, attribution confidence, consent and suppression state, and source refs. Lead Intake & Qualifier must independently read the current lead record before any contact.',
    repeatRule: 'Reconcile the same asset, campaign, event, and lead IDs; update only changed states, preserve approvals and receipts, and never issue duplicate distribution or lead follow-up.',
    sourceProbe: 'Read the approved launch brief and exact asset revision, current CMS or channel state, provider publication/send receipt if any, and two inbound form or CRM events. Join by campaign/source IDs, record consent and suppression, and show a duplicate event explicitly.',
    acceptanceCheck: 'Demonstrate that draft, approved, provider-executed and independently verified are separate states. Reconcile one inbound event to its lead without calling it qualified or contacted; missing receipt or contact eligibility blocks those claims.',
    exampleInput: 'Fictional input: launch=launch-24; approved brief=gtm-brief-24; asset=landing-v3; channel=newsletter; campaign=cmp-17; form source=form-2; owner=launch-owner.',
    workedExample: 'Fictional output: landing-v3 approved but not published; newsletter draft approved, provider send receipt absent; campaign cmp-17 has two form events e-91 and e-92, with e-92 a duplicate of e-91; lead l-55 linked to e-91 and queued for qualification; attribution=source-ID supported; owner=launch-owner; next=verify publish/send separately and pass l-55 to Sales without contacting it.',
    inadequateExample: '“Launch complete and two qualified leads generated.”',
    inadequateReason: 'It confuses approvals with publication and form events with qualification, and ignores the duplicate event.',
  },
]

const gtmTemplateVersion = 2

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm GTM role and owner', instructions: 'Confirm whether ' + spec.name + ' is the Crew’s primary role or a supporting capability. Preserve an existing identity and name the accountable GTM owner.' },
    { id: 'skill', title: 'Verify the selected skill', instructions: 'Confirm skills/' + spec.id + '/SKILL.md exists and ' + spec.id + ' is selected for this Crew.' },
    { id: 'scope', title: 'Set offer, audience, and launch scope', instructions: 'Record the offer version, audience, market, budget, dates, owner, first job, and exclusions. Minimum input: ' + spec.minimumInput },
    { id: 'access', title: 'Prove the GTM source join', instructions: spec.sourceProbe + ' ' + spec.optionalConnections + ' A named SaaS provider is not verified access.' },
    { id: 'policy', title: 'Verify the launch decision rule', instructions: spec.acceptanceCheck + ' Record claim, channel, contact and review policy.' },
    { id: 'first_result', title: 'Produce the first sourced result', instructions: 'Use actual authorized evidence to produce ' + spec.firstResult + ' ' + spec.evidence + ' The fictional example does not complete this check.' },
    { id: 'review', title: 'Review the result and next action', instructions: 'Show source-linked facts, hypotheses, unknowns, exact proposed actions, owner, and approval boundaries. Record the owner decision.' },
    { id: 'delivery', title: 'Choose publishing and delivery routes', optional: true, instructions: 'Choose read-only chat or separately authorized CMS, campaign, CRM, and message actions. Read-only chat completes this decision. ' + spec.boundary },
    { id: 'recurrence', title: 'Choose repeat and Automation route', optional: true, instructions: 'Choose manual-only or a reviewed event or schedule with deduplication and cost rules. Manual-only completes this decision. ' + spec.repeatRule },
  ]
  return JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: gtmTemplateVersion, checks, completed_steps: [] }, null, 2) + '\n'
}

function skill(spec: Specialist): string {
  return [
    '---', 'name: ' + spec.id, 'description: ' + spec.purpose, '---', '',
    '# ' + spec.name, '',
    'This skill gives one Crew the ' + spec.name + ' capability. It can seed a new Crew or be added to a compatible existing Crew. The customer’s offer, claim, contact, and measurement policies remain authoritative.', '',
    '## Setup through chat', '',
    'Read templates/' + spec.id + '/TEMPLATE_SETUP.json and templates/' + spec.id + '/SETUP.md. Verify each check with real customer scope and source records before adding its ID to completed_steps. Preserve progress and report verified, blocked, and next. Chat-only and manual-only are valid optional decisions.', '',
    '## First useful result', '',
    ...spec.method.map((step, index) => String(index + 1) + '. ' + step), '',
    'Deliver **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Source probe and acceptance', '', spec.sourceProbe, '', spec.acceptanceCheck, '',
    '## Follow-through', '', spec.repeatRule, '',
    '## Fictional worked example', '', spec.exampleInput, '', spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Automation handoff', '',
    spec.handoff + ' Builder must bind an exact artifact path and schema and validate the artifact before the next Crew consumes it. Verify current source truth independently.', '',
    '## Boundaries', '',
    spec.boundary + ' Installing this skill enables no schedule, trigger, function, Automation, publication, message, or account write. Never copy another Crew’s credentials.', '',
  ].join('\n')
}

function guide(spec: Specialist): string {
  return [
    '# ' + spec.name + ' setup', '',
    'Template ' + spec.id + ' version ' + gtmTemplateVersion + '. Progress lives in templates/' + spec.id + '/TEMPLATE_SETUP.json and is verified in Crew chat.', '',
    '## First result', '',
    'Provide ' + spec.minimumInput + ' Ask: “' + spec.exampleRequests[0] + '”', '',
    'Expected output: **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Fictional example and failure', '', spec.exampleInput, '', spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Source and connection choice', '',
    spec.optionalConnections + ' ' + spec.sourceProbe, '',
    '## Setup acceptance', '', spec.acceptanceCheck, '',
    '## Automation and recurring work', '',
    'Launch to Qualified Pipeline can coordinate this Crew with Website Growth and Sales. Builder must inspect existing Crews, verify handoffs, review a manual route, and record an owner-approved run policy. ' + spec.repeatRule + ' ' + spec.boundary, '',
  ].join('\n')
}

export const gtmSpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = 'templates/' + spec.id
  const skillPath = 'skills/' + spec.id + '/SKILL.md'
  const setupGuidePath = base + '/SETUP.md'
  const setupPath = base + '/TEMPLATE_SETUP.json'
  return {
    id: spec.id, version: gtmTemplateVersion, category: 'GTM', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
