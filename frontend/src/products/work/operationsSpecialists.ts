import type { CrewTemplate } from './crewTemplates'

export type OperationsSpecialistId =
  | 'chief-of-staff'
  | 'meeting-actions-coordinator'
  | 'project-status-reporter'
  | 'order-operations-coordinator'
  | 'vendor-researcher'
  | 'document-intake-assistant'

type Specialist = {
  id: OperationsSpecialistId
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
    id: 'chief-of-staff', name: 'Chief of Staff', icon: '🧭', subcategory: 'Business review',
    role: 'Business priorities and decision follow-through coordinator',
    purpose: 'Turn authorized team goals, project status, and decisions into a sourced operator brief with accountable next actions.',
    firstResult: 'A dated priorities brief with goal IDs, source-linked progress, blockers, decisions needed, owners, due dates, and changes since the last review.',
    minimumInput: 'Team goals and period, current project or customer updates, decision owners, reporting cadence, and authorized source scope.',
    optionalConnections: 'Project tracker, CRM, documents, calendar, and team updates through scoped MCPs or exports; a bounded file set supports a first read-only brief.',
    exampleRequests: ['Prepare this week’s operator brief from these goal and project records.', 'Which decisions are blocking the launch, and who owns each one?'],
    method: [
      'Bind goal IDs, team, period, owner, reporting time zone, source scope, and decision policy.',
      'Read current project, customer, finance, and meeting records only within the owner-approved scope; track freshness and missing coverage.',
      'Join updates to the same goal or action by stable IDs; separate observed progress from a teammate’s estimate or hypothesis.',
      'Rank blockers and decisions by the customer’s stated priority rules, with accountable owner and due time.',
      'Deliver a concise sourced brief for owner review; draft notifications or task edits separately.',
    ],
    evidence: 'A status claim needs the source record, observed time, goal or action ID, and an explicit gap when one system is missing.',
    boundary: 'Do not invent a management decision, change a team priority, create tasks, message people, or disclose private account data through installation.',
    handoff: 'Meeting Decision to Owned Follow-through may ask this Crew to produce operations-review-brief/v1 as an optional summary after validated action and status records. It cannot turn an unaccepted task into completed work.',
    repeatRule: 'Carry stable goal and decision IDs forward, compare the same period and source coverage, recheck owner decisions, and avoid repeating unchanged requests.',
    sourceProbe: 'Read one real goal by stable ID, its current project task and customer update, and the prior decision or brief for the same period. Record each revision and observed time; mark an unavailable metric or missing system as a coverage gap.',
    acceptanceCheck: 'Show the owner a brief that keeps an open blocker at risk and an unmeasured goal unknown. The setup remains pending if the Crew declares either on track, changes priorities, or proposes a notification without an owner decision.',
    exampleInput: 'Fictional input: goals=launch-2026-Q4 and onboarding-quality; week=2026-09-21; project export=rev-12; owner=operations-lead.',
    workedExample: 'Fictional output: launch goal=at risk because docs review task-41 is open, source=tracker:task-41@rev-12; decision needed=approve release notes owner=product-lead due=2026-09-28; onboarding goal=measurement unavailable because event coverage is partial; prior-week action task-35 complete from tracker; next review=2026-10-02.',
    inadequateExample: '“Everything is on track; tell the team to move faster.”',
    inadequateReason: 'It ignores the open blocker and missing measurement, lacks owners and source IDs, and proposes an unsanctioned message.',
  },
  {
    id: 'meeting-actions-coordinator', name: 'Meeting Actions Coordinator', icon: '📝', subcategory: 'Meeting follow-through',
    role: 'Meeting decision and action register coordinator',
    purpose: 'Extract owner-reviewed decisions and action items from authorized notes into a traceable register without silently assigning work.',
    firstResult: 'A meeting action register with meeting and source IDs, decision versus proposal, action owner, due date, uncertainty, duplicate link, and review state.',
    minimumInput: 'Authorized notes or transcript, meeting ID and time, participant and owner map, task conventions, decision policy, and review owner.',
    optionalConnections: 'Calendar, transcript or document source, task tracker, and meeting notes through scoped MCPs or files; notes alone support a first draft.',
    exampleRequests: ['Extract the decisions and action items from this meeting and show what needs owner confirmation.', 'Compare today’s notes with our open action register and avoid duplicate tasks.'],
    method: [
      'Confirm meeting identity, source revision, participants, consent and transcript handling, task convention, and review owner.',
      'Separate an explicit decision from a suggestion, question, or inferred assignment; retain a source span for each item.',
      'Normalize each action to a stable meeting/action key, proposed owner and due time; mark missing owners or ambiguous dates for confirmation.',
      'Check existing task and prior-meeting records for duplicates and already completed actions.',
      'Return a reviewed register; create or update tasks only through a separately authorized route with destination receipts.',
    ],
    evidence: 'Every decision and action needs a source span, meeting revision, owner state, and a reason when the text is ambiguous.',
    boundary: 'Do not record a suggestion as an approved decision, assign a person without confirmation, publish notes, or create tracker tasks from installation.',
    handoff: 'Meeting Decision to Owned Follow-through passes meeting-action-register/v1 to Project Status Reporter only after checking meeting revision, action identity, owner acceptance, due date, and source evidence.',
    repeatRule: 'Reuse meeting and action IDs, re-read transcript revisions and tracker state, preserve corrections, and avoid duplicate tasks or reminders.',
    sourceProbe: 'Read one authorized meeting notes revision with source spans, the participant and decision-owner map, and the current task register. Match one proposed action to its meeting/action key and search for an existing task before drafting a new one.',
    acceptanceCheck: 'Produce a register that distinguishes a decision from a proposal and leaves an ambiguous owner or due date pending. The setup remains pending if it treats a suggestion as approved, duplicates a tracker task, or writes a task before owner acceptance.',
    exampleInput: 'Fictional input: meeting=launch-sync-42 at 2026-09-26T09:00Z; notes revision=3; participant map=product-lead and design-lead; tracker export=rev-9.',
    workedExample: 'Fictional output: decision D1=release notes require product review, source=notes:42@rev3 lines 18-20, confirmed by product-lead; action A1=design-lead to supply hero image by 2026-09-28, source=lines 24-25, owner acceptance=pending; action A2 duplicates tracker:task-41, no new task; next=confirm A1 before tracker write.',
    inadequateExample: '“Everyone agreed to launch tomorrow. I assigned all tasks.”',
    inadequateReason: 'The notes do not establish that decision, no source spans or accepted owners are shown, and assignments were made without review.',
  },
  {
    id: 'project-status-reporter', name: 'Project Status Reporter', icon: '📌', subcategory: 'Project follow-through',
    role: 'Project milestone and action status coordinator',
    purpose: 'Reconcile project milestones, task state, and owner updates into a sourced status report with decisions and blockers.',
    firstResult: 'A milestone and action status report with project ID, as-of time, source coverage, completed versus open work, blockers, owner requests, and next evidence.',
    minimumInput: 'Project and milestone IDs, target dates, task or update export, status rules, owner map, reporting period, and review owner.',
    optionalConnections: 'Project tracker, repository or deployment source, documents, meeting action register, and team updates through scoped MCPs or exports.',
    exampleRequests: ['Explain the current launch project status from tracker records and meeting actions.', 'Which milestones slipped, and what evidence supports the owner action?'],
    method: [
      'Bind exact project, milestone, reporting cutoff, time zone, owner, and status definitions.',
      'Read current task and milestone records, last owner updates, and any validated meeting action register.',
      'Join actions to project IDs and prior task IDs; classify accepted, open, blocked, done, unknown, and overdue from source state.',
      'Explain changes since the last report with dates and source links; distinguish a plan from observed completion.',
      'Prepare owner decisions and task update proposals separately from the read-only report.',
    ],
    evidence: 'A completed milestone needs authoritative task, deliverable, or release evidence; a meeting promise is not completion.',
    boundary: 'Do not mark tasks complete, change deadlines, notify stakeholders, or declare a project green from stale or partial records.',
    handoff: 'Meeting Decision to Owned Follow-through consumes meeting-action-register/v1 and emits project-action-status/v1 for exact project/action IDs. Chief of Staff may summarize validated status when access permits.',
    repeatRule: 'Reconcile prior action IDs and source revisions, preserve open decisions and late evidence, and update the same status history rather than creating a fresh unsupported report.',
    sourceProbe: 'Read one real project and milestone by exact IDs, current tracker task state, the prior status report, and any validated meeting action for that milestone. Record reporting cutoff, revisions, and which delivery source is unavailable.',
    acceptanceCheck: 'Show the owner an open or blocked milestone as open or blocked even when meeting notes promise completion. The setup remains pending if it marks work done without authoritative deliverable or release evidence, or hides missing source coverage.',
    exampleInput: 'Fictional input: project=site-launch-17; tracker=rev-9; due=2026-10-01; meeting register=launch-sync-42; status policy=delivery-v2.',
    workedExample: 'Fictional output: as-of=2026-09-26T12:00Z; milestone=site copy review open, source=tracker:task-41@rev9; meeting action A1 owner acceptance=pending, not yet a committed task; launch risk=review may miss 2026-09-28; decision=product-lead confirm scope; coverage=tracker current, deployment source unavailable.',
    inadequateExample: '“Launch is complete because the team discussed it.”',
    inadequateReason: 'Discussion is not a shipped artifact; the report omits project identity, task state, source coverage, and owner decision.',
  },
  {
    id: 'order-operations-coordinator', name: 'Order Operations Coordinator', icon: '📦', subcategory: 'Order exceptions',
    role: 'Cross-system order exception coordinator',
    purpose: 'Investigate authorized order, fulfillment, carrier, and customer-service records into a prioritized owner action queue.',
    firstResult: 'An exception queue with exact order and shipment IDs, current source states, promised dates, policy rule, owner, customer contact state, and safe next action.',
    minimumInput: 'Order and fulfillment export, business policy, carrier or shipment source, exception threshold, customer contact rule, and operations owner.',
    optionalConnections: 'Commerce or ERP platform, fulfillment provider, carrier tracking, helpdesk, and inventory sources through scoped MCPs or exports; no Shopify dependency is assumed.',
    exampleRequests: ['Which orders are stuck between payment, fulfillment, and carrier pickup?', 'Review these late shipments and prepare owner decisions without contacting customers yet.'],
    method: [
      'Bind store or business, order, fulfillment and shipment IDs, time zone, promised date, customer policy, and owner.',
      'Read current order, payment, inventory, fulfillment, carrier and prior-contact state from authorized sources.',
      'Classify delayed, incomplete, duplicate, disputed, and already resolved cases using the customer’s thresholds and source timestamps.',
      'Propose a bounded next action and draft a customer update only when the exact recipient and policy are known.',
      'Review one representative exception with the owner before any refund, fulfillment, inventory, or contact write.',
    ],
    evidence: 'A carrier label is not delivery; separate created, accepted, in-transit, delivered, and unknown states with provider IDs.',
    boundary: 'Do not refund, reship, cancel, mark delivered, alter inventory, or message a customer without exact approval and provider receipt.',
    handoff: 'Order Watchdog can consume order-exception/v1 with business, order, fulfillment, shipment, source status, policy, owner and action ID. Shopify-specific Store Operations remains a separate store-scoped template.',
    repeatRule: 'Re-read order, payment, shipment and prior-contact state; retain case and action IDs and avoid a second refund, shipment, or message for the same issue.',
    sourceProbe: 'Read one real exception by exact order, payment, fulfillment and carrier IDs, plus promised time and prior customer contact. Record source timestamps and whether the carrier has accepted the parcel rather than only a label being created.',
    acceptanceCheck: 'Classify a paid order with a created label and no acceptance scan as pickup unverified, with a bounded owner next step. The setup remains pending if it asserts delivery or loss, refunds, reships, or contacts the customer without current state, policy and approval.',
    exampleInput: 'Fictional input: order=ord-88; fulfillment=ful-12; promised=2026-09-25; carrier=track-44; policy=late-orders-v2; owner=ops-lead.',
    workedExample: 'Fictional output: order ord-88 paid, fulfillment ful-12 label created 2026-09-23, carrier track-44 has no acceptance scan by promised date; status=pickup unverified, not delivered; owner=warehouse-lead; next=confirm handoff with provider by 16:00Z; customer draft=unsent; sources=order export and carrier event.',
    inadequateExample: '“The package is lost; refund the customer now.”',
    inadequateReason: 'A missing carrier scan does not prove loss, and the refund lacks current payment state, policy, approval, and receipt.',
  },
  {
    id: 'vendor-researcher', name: 'Vendor Researcher', icon: '🔎', subcategory: 'Vendor decisions',
    role: 'Requirements-based vendor evaluation analyst',
    purpose: 'Compare a bounded vendor set against owner-approved requirements, evidence, costs, and risks to prepare a reviewable shortlist.',
    firstResult: 'A vendor comparison with exact products and plan versions, requirement-level evidence, unknowns, total-cost assumptions, risk questions, and owner decision.',
    minimumInput: 'Purchase goal, must-have and weighted criteria, budget and region, approved vendor set or discovery scope, security constraints, and decision owner.',
    optionalConnections: 'Vendor websites and documentation, procurement files, CRM notes, security questionnaires, and spend records; public pages alone support a preliminary read-only comparison.',
    exampleRequests: ['Compare these three support tools against our requirements with sources and unknowns.', 'Prepare a shortlist for the operations lead, including contract and data-residency questions.'],
    method: [
      'Confirm buyer need, must-have criteria, weights, budget, geography, purchase stage, and evaluation date.',
      'Identify exact vendor, product, plan, quoted term and source revision; do not compare vague brand names.',
      'Score each criterion only from current vendor or customer-provided evidence and label unknown or self-reported claims.',
      'Calculate comparable cost ranges with seats, usage, onboarding, commitments, currency, and exclusions exposed.',
      'Return a shortlist and due-diligence questions for owner review; keep outreach and purchase separate.',
    ],
    evidence: 'Every score needs a dated source and criterion; an unsupported marketing claim is not a verified capability.',
    boundary: 'Do not claim a vendor is compliant, request a quote, share customer data, sign terms, or commit spend without an authorized process.',
    handoff: 'Vendor Review can consume vendor-comparison/v1 with requirements revision, product/plan IDs, evidence, weights, cost assumptions, and owner decision. Procurement action remains separate.',
    repeatRule: 'Recheck cited pages and quotes before a later decision, preserve scoring rules and prior choices, and flag changed plans or expired offers.',
    sourceProbe: 'Read the owner-approved must-have list and one current exact product/plan quote or dated vendor page for each compared option. Record seats, usage, term, currency, region, source revision and any expired or self-reported claim.',
    acceptanceCheck: 'Calculate comparable total costs and mark an unverified must-have as unknown or failing, with a dated citation and owner question. The setup remains pending if it calls an unsupported vendor compliant, ranks a missing quote as cheapest, or initiates purchase or outreach.',
    exampleInput: 'Fictional input: need=helpdesk for 20 agents; criteria=EU data region must-have, shared inbox weight 3, API export weight 2; vendors=A/B/C; budget=USD 8k/year.',
    workedExample: 'Fictional output: Vendor A plan Pro meets shared inbox from docs@2026-09-25 but EU region unverified; Vendor B plan Team has quoted EU region in proposal Q-17, API export unclear; Vendor C fails budget at 20 seats under listed annual price; shortlist=B pending API and contract review; owner=ops-lead.',
    inadequateExample: '“Vendor A is the best and fully compliant.”',
    inadequateReason: 'The must-have data region is unverified, the plan and price are omitted, and compliance is asserted without evidence.',
  },
  {
    id: 'document-intake-assistant', name: 'Document Intake Assistant', icon: '📄', subcategory: 'Document intake',
    role: 'Structured document intake and exception reviewer',
    purpose: 'Extract customer-defined fields from authorized incoming documents, validate them against source pages and rules, and route uncertain values for review.',
    firstResult: 'A source-linked extraction with document and version ID, page or span references, field values and confidence, validation failures, duplicate state, and reviewer queue.',
    minimumInput: 'Sample documents and allowed document types, required schema, validation and duplicate rules, privacy policy, owner, and destination choice.',
    optionalConnections: 'Document storage, OCR or parsing skill, CRM/ERP destination, and review queue through scoped MCPs or files; local supplied PDFs support a first read-only extraction.',
    exampleRequests: ['Extract the required contract fields from these files and flag anything that needs review.', 'Check whether this invoice duplicates an existing document before preparing an intake record.'],
    method: [
      'Confirm authorized document set, type, source revision, allowed fields, retention, privacy and reviewer.',
      'Extract each field with page/span evidence; distinguish printed value, inferred normalization, and missing value.',
      'Validate types, totals, dates, identities and cross-field rules against the owner-approved schema.',
      'Check duplicate keys and current destination records; keep low-confidence or conflicting fields in a review queue.',
      'Return a reviewable structured record; create a destination record only after exact approval and provider receipt.',
    ],
    evidence: 'A field is not verified merely because OCR produced text; show source span, validation rule, uncertainty, and document version.',
    boundary: 'Do not upload sensitive files to unapproved tools, silently fill missing fields, create duplicate records, or post an extraction without review.',
    handoff: 'Document Intake Queue can consume document-intake-record/v1 with document/version ID, schema revision, field source spans, validation, reviewer decision, and destination receipt.',
    repeatRule: 'Use source document hash and destination ID, reprocess changed versions intentionally, retain review corrections, and avoid duplicate writes.',
    sourceProbe: 'Read one authorized document by hash and version with page or span evidence, its approved schema revision, and the current destination duplicate keys. Check required identity fields and arithmetic against the original file, not OCR text alone.',
    acceptanceCheck: 'Return a needs-review record when a required vendor identity is absent or duplicate state cannot be resolved, even if totals add up. The setup remains pending if it fills a missing field, calls OCR verified without a source span, or writes a destination record without approval and receipt.',
    exampleInput: 'Fictional input: file=invoice-88.pdf hash=abc123; schema=vendor-invoice-v3 requiring vendor ID, invoice number, currency, net, tax and total; destination=accounts-payable queue.',
    workedExample: 'Fictional output: invoice number INV-88 page1 line4; net=100.00 USD page1 line15; tax=8.00 USD page1 line16; total=108.00 USD page1 line17, arithmetic valid; vendor ID missing, confidence=unknown; duplicate key INV-88/vendor unknown cannot be checked; state=needs review; destination write=none.',
    inadequateExample: '“Invoice processed successfully.”',
    inadequateReason: 'The vendor ID and duplicate check are unresolved, no page evidence is shown, and no authorized destination receipt exists.',
  },
]

const operationsTemplateVersion = 2

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm operations role and owner', instructions: 'Confirm whether ' + spec.name + ' is this Crew’s primary role or an added capability. Preserve existing identity and name the accountable owner.' },
    { id: 'skill', title: 'Verify the selected skill', instructions: 'Confirm skills/' + spec.id + '/SKILL.md exists and ' + spec.id + ' is selected for this Crew.' },
    { id: 'scope', title: 'Set exact job and source scope', instructions: 'Record business, project, order or document identity, period and time zone, owner, privacy boundary, and first job. Minimum input: ' + spec.minimumInput },
    { id: 'access', title: 'Probe representative source access', instructions: spec.sourceProbe + ' Available connection choices: ' + spec.optionalConnections },
    { id: 'policy', title: 'Confirm rules and review boundary', instructions: spec.acceptanceCheck + ' Action boundary: ' + spec.boundary },
    { id: 'first_result', title: 'Produce first sourced result', instructions: 'Use real authorized input to produce ' + spec.firstResult + ' ' + spec.evidence + ' Fictional examples do not complete this check.' },
    { id: 'review', title: 'Review result with owner', instructions: 'Show source links, unknowns, confidence, proposed next action, and owner correction. Record the review decision.' },
    { id: 'delivery', title: 'Choose read-only or action route', optional: true, instructions: 'Choose read-only chat or a separately authorized task, status, contact, purchase, or record-write route. Read-only completes this choice.' },
    { id: 'recurrence', title: 'Choose repeat rule', optional: true, instructions: 'Choose manual-only or a reviewed event/schedule with stable IDs, deduplication, cost, and notifications. Manual-only completes this choice. ' + spec.repeatRule },
  ]
  return JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: operationsTemplateVersion, checks, completed_steps: [] }, null, 2) + '\n'
}

function skill(spec: Specialist): string {
  return [
    '---', 'name: ' + spec.id, 'description: ' + spec.purpose, '---', '',
    '# ' + spec.name, '',
    'This skill gives one Crew the ' + spec.name + ' capability. It can seed a new Crew or be added to a compatible Crew. Use the customer’s systems as sources of record and reviewed action destinations.', '',
    '## Setup through chat', '',
    'Read templates/' + spec.id + '/TEMPLATE_SETUP.json and templates/' + spec.id + '/SETUP.md. Verify each check with actual customer records before adding its ID to completed_steps. Preserve progress and report verified, blocked, and next. Chat-only and manual-only are valid optional decisions.', '',
    '## First useful result', '',
    ...spec.method.map((step, index) => String(index + 1) + '. ' + step), '',
    'Deliver **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Source probe and setup acceptance', '',
    spec.sourceProbe, '', spec.acceptanceCheck, '',
    '## Follow-through', '', spec.repeatRule, '',
    '## Fictional worked example', '', spec.exampleInput, '', spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Automation handoff', '',
    spec.handoff + ' Builder must bind an exact artifact path and schema and validate before another Crew consumes it. Re-read current source state.', '',
    '## Boundaries', '',
    spec.boundary + ' Installation enables no schedule, trigger, function, Automation, external write, notification, or customer message. Never copy another Crew’s credentials.', '',
  ].join('\n')
}

function guide(spec: Specialist): string {
  return [
    '# ' + spec.name + ' setup', '',
    'Template ' + spec.id + ' version ' + operationsTemplateVersion + '. Progress lives in templates/' + spec.id + '/TEMPLATE_SETUP.json and is verified in Crew chat.', '',
    '## First result', '',
    'Provide ' + spec.minimumInput + ' Ask: “' + spec.exampleRequests[0] + '”', '',
    'Expected output: **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Fictional example and failure', '', spec.exampleInput, '', spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Source and connection choice', '',
    spec.optionalConnections + ' ' + spec.sourceProbe, '',
    '## Setup acceptance', '', spec.acceptanceCheck, '',
    '## Automation and recurring work', '',
    'A multi-Crew Automation is useful when ownership, access or review boundaries differ. Builder must inspect existing Crews, verify handoffs, run one manual case, and record owner-approved run policy. ' + spec.repeatRule + ' ' + spec.boundary, '',
  ].join('\n')
}

export const operationsSpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = 'templates/' + spec.id
  const skillPath = 'skills/' + spec.id + '/SKILL.md'
  const setupGuidePath = base + '/SETUP.md'
  const setupPath = base + '/TEMPLATE_SETUP.json'
  return {
    id: spec.id, version: operationsTemplateVersion, category: 'Operations', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
