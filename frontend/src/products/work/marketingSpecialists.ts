import type { CrewTemplate } from './crewTemplates'

export type MarketingSpecialistId =
  | 'competitor-intelligence-analyst'
  | 'campaign-performance-analyst'
  | 'funnel-analyst'
  | 'growth-experiment-planner'
  | 'experiment-run-coordinator'
  | 'growth-outcome-analyst'

type Specialist = {
  id: MarketingSpecialistId
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
    id: 'competitor-intelligence-analyst', name: 'Competitor Intelligence Analyst', icon: '🔭', subcategory: 'Market signals',
    role: 'Competitor change and positioning analyst',
    purpose: 'Track a bounded competitor set for sourced changes that matter to a named offer, buyer, and decision owner.',
    firstResult: 'A dated change brief with exact competitor/product/plan, primary source and capture time, prior-versus-current evidence, relevance, uncertainty, and owner question.',
    minimumInput: 'Own offer and buyer, approved competitor/product list, watch topics, source scope, comparison baseline, time window, alert threshold, and owner.',
    optionalConnections: 'Public vendor pages and changelogs, approved research sources, CRM win/loss notes, and a saved source snapshot through browser or scoped MCP; a bounded URL set supports a read-only first brief.',
    exampleRequests: ['What changed in these competitors’ plans this month, and does it affect our offer?', 'Compare these claims against our approved positioning with sources and unknowns.'],
    method: [
      'Bind own offer, target buyer, geography, competitor product and plan IDs, topics, baseline date, source scope, and decision owner.',
      'Capture dated primary pages, changelogs or owner-supplied sales records; preserve the exact URL or record revision and avoid hidden or restricted material.',
      'Compare like-for-like plan, region, seat, usage and terms. Label source claims, observed changes, estimates, and unavailable evidence separately.',
      'Score relevance against the owner’s stated positioning or sales question; do not infer customer preference from a vendor page.',
      'Deliver a short change brief with implications to review, not an automatic pricing or messaging change.',
    ],
    evidence: 'Each change needs before/after source IDs and capture times, or an explicit baseline gap; a copied claim is not independent verification.',
    boundary: 'Do not scrape behind access controls, copy protected customer data, claim market share, change prices, publish claims, or contact a competitor from installation.',
    handoff: 'Campaign Signal to Reviewed Experiment may attach competitor-context/v1 to an experiment plan only if competitor product, market, source window and own offer are bound. Competitive context cannot substitute for campaign performance evidence.',
    repeatRule: 'Retain watched URL/product keys and prior captures, compare only changed facts, recheck expiring prices and terms, and suppress unchanged alerts.',
    sourceProbe: 'Capture the current primary vendor page and an earlier dated snapshot for the same product, plan, region, seat and term; record URL, revision, capture time and any inaccessible terms. An owner-supplied sales note may explain relevance but cannot replace a missing before snapshot.',
    acceptanceCheck: 'Verify one claimed change against two comparable captures and show one unchanged or unverifiable claim. If region, plan, pricing basis or baseline differs, label the comparison unknown rather than a change or market trend.',
    exampleInput: 'Fictional input: offer=team analytics; buyer=20-seat SaaS operations team; competitors=A Pro and B Team; region=US; baseline=2026-09-01; watch=API export and annual price.',
    workedExample: 'Fictional output: A Pro now lists API export at 25 seats, versus 10 seats on archived 2026-09-01 page; current source=A pricing@2026-09-26; B Team price claim unchanged but contract term unavailable; relevance=medium for 20-seat buyers; owner question=check whether sales objections mention export thresholds.',
    inadequateExample: '“Competitor A is winning because it has better pricing. Cut our price today.”',
    inadequateReason: 'No matched plan, dated source, comparable terms, customer evidence, or owner approval supports the conclusion or action.',
  },
  {
    id: 'campaign-performance-analyst', name: 'Campaign Performance Analyst', icon: '📊', subcategory: 'Campaign measurement',
    role: 'Campaign measurement and decision analyst',
    purpose: 'Explain a bounded campaign’s spend, delivery, qualified response, and conversion changes from source records with comparable definitions.',
    firstResult: 'A campaign performance brief with campaign and account IDs, metric definitions, source coverage, baseline versus observed values, calculation, confounders, and one owner decision.',
    minimumInput: 'Campaign/platform IDs, account and attribution scope, period and time zone, spend and conversion sources, qualified-event definition, baseline, and owner.',
    optionalConnections: 'Ad, email, analytics, CRM, billing or product sources via scoped MCPs or exports; one platform export plus a conversion source supports a first analysis.',
    exampleRequests: ['Explain why this campaign’s qualified leads fell despite more clicks.', 'Compare this week’s campaign results with the agreed baseline and propose one next test.'],
    method: [
      'Bind campaign, account, channel, audience, currency, reporting period, time zone, attribution window, conversion event and owner.',
      'Read platform spend and delivery records plus downstream qualified events; record data lag, missing channels, bot/internal filters and identity join coverage.',
      'Calculate comparable rates and cost per qualified event with numerator, denominator, units and source IDs; never mix clicks, leads and meetings.',
      'Separate observed change from a causal explanation; list plausible confounders such as audience, budget, tracking or seasonality.',
      'Return one bounded recommendation with a measurement gap or experiment question for owner review; no budget or campaign write occurs.',
    ],
    evidence: 'Every metric needs an exact source, definition, period, denominator, currency and freshness; incomplete attribution must be visible.',
    boundary: 'Do not pause campaigns, change spend or targeting, claim incremental lift, or publish a result from mismatched periods or unverified conversions.',
    handoff: 'Campaign Signal to Reviewed Experiment emits campaign-performance-brief/v1 for Growth Experiment Planner after account, campaign, period, metric and coverage checks. A recommendation is not an approved experiment.',
    repeatRule: 'Use stable campaign and event IDs, preserve metric definitions and prior periods, account for reporting lag and deduplicate conversions before raising a new alert.',
    sourceProbe: 'Read one scoped platform spend/click export and downstream CRM qualified-event export for the same campaign, account, attribution window, currency and timezone. Record campaign and event IDs, late records, joins and unjoined share.',
    acceptanceCheck: 'Recompute a qualified-event rate and cost from source numerators and denominators in two comparable windows; show one duplicate or unjoined event. A platform conversion count cannot silently become a CRM-qualified demo or a causal explanation.',
    exampleInput: 'Fictional input: campaign=cmp-42 account=ads-7; week=2026-09-14; spend=USD 1,200; clicks=400; qualified demos=8; prior comparable week spend=USD 1,000, clicks=300, demos=12; CRM coverage=95%.',
    workedExample: 'Fictional output: qualified demos per click fell from 4.0% to 2.0%; cost per qualified demo rose from USD 83.33 to USD 150.00; source=ads:cmp-42@rev8 and crm:demo-events@rev4; causal reason unknown; tracking gap=5% unjoined; next=review landing-page and audience changes before testing a single message.',
    inadequateExample: '“The ad platform failed. Double the budget to fix conversions.”',
    inadequateReason: 'The conclusion ignores downstream CRM evidence and attribution gaps, invents causality, and proposes an unauthorized spend change.',
  },
  {
    id: 'funnel-analyst', name: 'Funnel Analyst', icon: '🪜', subcategory: 'SaaS conversion',
    role: 'Signup-to-paid funnel investigation analyst',
    purpose: 'Reconcile an authorized signup-to-paid cohort across event and billing sources, then identify a bounded drop-off for owner review.',
    firstResult: 'A versioned funnel observation with exact cohort, identity and event rules, stage counts, a comparable change or honest first baseline, coverage gaps, and one investigation question.',
    minimumInput: 'Product, tenant and funnel ID, ordered event definitions, identity and deduplication rule, current and optional comparable prior window, authorized event and paid-state sources, timezone, and decision owner.',
    optionalConnections: 'Product analytics, warehouse or event export and subscription billing/CRM records through scoped MCPs or files; a read-only first run can use two authorized, versioned exports.',
    exampleRequests: ['Where did our signup-to-paid funnel lose users this month?', 'Check whether mobile trial users reached paid status less often, using the same event and billing definitions.'],
    method: [
      'Freeze product, eligible cohort, ordered stages, event versions, identity join, deduplication, consent and timezone rules before comparing windows.',
      'Read exact event and paid-state sources; check freshness, identity join rate, billing lag, excluded internal/test users, and missing-source coverage.',
      'Count unique eligible users who reached each stage in order; keep stage counts monotone and compute rates from a named denominator.',
      'Compare equal windows on the same population and stage definitions; state observed differences and sample limits without inferring a cause from correlation.',
      'Ask the owner to review one likely friction point and a source-check or experiment question; do not change signup, billing or messaging.',
    ],
    evidence: 'Show cohort and source revisions, stage definitions, unique-user counts, denominator, identity coverage, paid-state evidence and an explicit unknown when a source is missing.',
    boundary: 'Do not call a trial a paid customer, infer purchase from a click, claim causality, inspect unconsented sessions, or modify pricing, checkout or campaigns from installation.',
    handoff: 'Funnel and Conversion Intelligence emits funnel-observation/v1 to Growth Experiment Planner only after exact product, cohort, stage rule, window, source and count checks. A drop-off is an observed signal, not an approved experiment.',
    repeatRule: 'Preserve funnel definition and source revisions, re-read the same cohort keys and later paid events, wait for billing lag, and avoid counting users twice or comparing changed event semantics as a trend.',
    sourceProbe: 'Read authorized signup and activation events plus the matching subscription paid-state source for one frozen cohort. Record event versions, account join, internal/test exclusions, billing lag, timezone and missing-source coverage.',
    acceptanceCheck: 'Reproduce ordered unique-account stage counts and paid/eligible rate from source IDs; stage counts cannot increase downstream. Show a duplicate and a missing-billing case, and call a single complete window a baseline rather than a trend.',
    exampleInput: 'Fictional input: product=ArborDesk; cohort=UK self-serve new accounts; baseline Aug 2-31 and current Sep 1-30; signup/activated/paid events v2; eligible users 1,000 versus 1,200; authorized product event and billing exports.',
    workedExample: 'Fictional output: baseline signup=300, activated=180, paid=60 of 1,000 eligible users; current signup=360, activated=180, paid=48 of 1,200. Paid/eligible fell from 6.0% to 4.0%; activated/signup fell from 60% to 50%. Identity join=98%, billing current through Oct 3, cause unknown; next=review activation flow change and tracking before one bounded test.',
    inadequateExample: '“Our checkout redesign caused churn, so raise prices and email everyone.”',
    inadequateReason: 'No exact cohort, ordered counts, billing proof, comparison rule or causal evidence supports the diagnosis or unauthorized actions.',
  },
  {
    id: 'growth-experiment-planner', name: 'Growth Experiment Planner', icon: '🧪', subcategory: 'Experiment design',
    role: 'Growth hypothesis and experiment decision planner',
    purpose: 'Turn one evidence-backed growth question into a bounded experiment with a decision rule, owner, safety limit, and measurement plan.',
    firstResult: 'A reviewed experiment plan with hypothesis, target population, baseline, primary and guardrail metrics, sample/stop rule, owner, implementation boundary, and follow-up decision.',
    minimumInput: 'Problem and source evidence, eligible audience, baseline and metric definitions, owner, change authority, duration or sample constraints, and guardrail.',
    optionalConnections: 'Analytics, CRM, campaign platform, feature flag, experimentation tool, and task tracker via scoped MCPs or exports; a read-only plan can start from a sourced brief.',
    exampleRequests: ['Turn this campaign performance gap into one testable message experiment.', 'Plan a landing-page test with a stop rule and what we will measure.'],
    method: [
      'Bind the source opportunity, customer goal, eligible unit, owner, current baseline and one changeable treatment.',
      'State a falsifiable hypothesis and distinguish observational evidence from a causal claim.',
      'Define primary success metric, denominator, observation window, minimum detectable practical effect and guardrail with baseline or baseline-first decision.',
      'Choose assignment or comparison method, sample and stop rules, exclusion and contamination checks, cost and rollback owner.',
      'Show the proposal for approval; activating a flag, editing a page or campaign, contacting customers and declaring success are separate steps.',
    ],
    evidence: 'An experiment plan needs the source brief ID, exact metric definition, baseline evidence or an explicit baseline-first phase, and a predeclared decision rule.',
    boundary: 'Do not launch an experiment, change budget, publish variants, send messages, or claim a winner without owner approval and comparable outcome evidence.',
    handoff: 'Campaign Signal to Reviewed Experiment consumes campaign-performance-brief/v1 and emits growth-experiment-plan/v1. Funnel and Conversion Intelligence consumes funnel-observation/v1 and emits funnel-experiment-plan/v1. Activation and Retention Intelligence consumes a mature cohort-retention-observation/v1 and emits retention-experiment-plan/v1; pending_maturity must stop before planning. In every route, freeze the upstream ID, metric denominator, owner, guardrail and pending launch state; optional context never provides a measured baseline.',
    repeatRule: 'Retain experiment ID, policy revision, assignment and decision rule; re-read outcome and guardrail data for the agreed window, record null results, and avoid repeated launches.',
    sourceProbe: 'Read the exact upstream campaign, funnel or retention artifact and current baseline source; bind its owner, eligible unit, metric denominator, evidence coverage and one changeable treatment. Read the guardrail and assignment capability before proposing launch.',
    acceptanceCheck: 'Predeclare hypothesis, population, assignment, primary/guardrail calculations, minimum sample or duration, stop rule and decision owner. If baseline or maturity is missing, produce a baseline-first or pending plan; an approved plan is not a launched experiment.',
    exampleInput: 'Fictional input: source campaign brief=brief-42; demos/click dropped from 4.0% to 2.0%; audience=US operations leaders; owner=growth-lead; landing-page control=rev4.',
    workedExample: 'Fictional output: hypothesis=message mismatch after the audience expansion; test one headline variant against rev4 for eligible clicks; primary=qualified demos/eligible click in CRM, guardrail=unsubscribes and spend cap; sample target=owner-reviewed calculator result; stop after planned window or guardrail breach; publish=none pending approval.',
    inadequateExample: '“Try a new headline and see if it wins.”',
    inadequateReason: 'There is no audience, baseline, denominator, sample or stop rule, guardrail, owner, or evidence standard for a decision.',
  },
  {
    id: 'experiment-run-coordinator', name: 'Experiment Run Coordinator', icon: '🚦', subcategory: 'Experiment operations',
    role: 'Approved growth experiment execution coordinator',
    purpose: 'Carry one approved experiment design through exact launch-state checks, provider receipts and rollback ownership without treating a proposal as permission.',
    firstResult: 'An experiment execution record linking the frozen plan, owner decision, exact variant and exposure state, provider receipt or pending reason, rollback owner and readout window.',
    minimumInput: 'Frozen experiment plan and revision, approval owner and decision, eligible population, assignment unit, variant/flag IDs, rollout and rollback policy, provider scope and planned readout window.',
    optionalConnections: 'Feature flag, CMS, campaign or experiment platform, issue tracker and analytics through scoped MCPs or exports; start with a read-only launch-state check.',
    exampleRequests: ['Check whether this approved experiment really launched and who can roll it back.', 'Prepare the launch record for this test without activating the flag.'],
    method: [
      'Bind the exact frozen plan and owner approval record, including version, metric, guardrail, assignment unit, sample and stop rule.',
      'Re-read the current provider variant, flag or campaign state; identify drift, conflicting launches, exposure eligibility and the rollback owner.',
      'If action authority is absent or approval is pending, report pending without launching. If a separate approved route acts, record its provider receipt and exact launch timestamp.',
      'Preserve a stable experiment/action ID, configuration revision, assignment and exposure evidence; never equate a task ticket with a live variant.',
      'Hand a validated execution record and frozen readout window to Growth Outcome Analyst only when there is a provider-confirmed launch.',
    ],
    evidence: 'Cite plan revision, owner approval, provider object/version, rollout receipt and exposure records; distinguish proposed, approved, launched and rolled back.',
    boundary: 'Do not launch, publish, spend, send, change allocation or claim an experiment is live from a plan, ticket or verbal approval. Writes require an explicit reviewed action route and provider receipt.',
    handoff: 'Growth Experimentation and Follow-Through emits experiment-execution-record/v1 to Growth Outcome Analyst after exact plan, owner approval, provider launch and exposure checks. A pending approval or missing launch receipt stops outcome claims.',
    repeatRule: 'Re-read provider state by stable experiment ID; record revised configuration or rollback separately, prevent duplicate launch actions and preserve the original pre-registered readout rule.',
    sourceProbe: 'Read the frozen experiment plan revision, exact owner approval, provider variant/flag/campaign object and current exposure state for the same experiment ID. Record rollback owner, provider receipt if any, observation time and conflicting launches.',
    acceptanceCheck: 'Separate pending approval, approved configuration, provider-confirmed launch and actual exposure. Reject a ticket-only launch claim or mismatched plan/variant; do not pass an execution record to Outcome without a matching provider receipt and exposure evidence.',
    exampleInput: 'Fictional input: experiment exp-guided-setup-7, frozen plan rev3, approved by growth owner at 09:00, 50/50 account assignment, guardrail support blockers, readout after 30 days; feature flag ff-guided-setup-7.',
    workedExample: 'Fictional output: plan rev3 and approval apr-7 match flag revision 4; provider receipt launch-7 confirms Oct 1 rollout to UK self-serve eligible accounts at 50/50. Exposure source exp-7 is linked; rollback owner product-lead; the final Oct 31 exposure reaches day 30 on Nov 30, so readout starts after that and source lag. No winner is asserted.',
    inadequateExample: '“The Jira ticket is done, so the experiment is live and winning.”',
    inadequateReason: 'A work item is not owner approval, provider launch, exposure evidence or measured outcome.',
  },
  {
    id: 'growth-outcome-analyst', name: 'Growth Outcome Analyst', icon: '📈', subcategory: 'Experiment measurement',
    role: 'Growth experiment outcome and guardrail analyst',
    purpose: 'Reconcile an actual launched experiment with its frozen metric, assignment and observation window, then report measured or inconclusive outcomes for owner review.',
    firstResult: 'A source-linked experiment readout with exact launched variant, eligible and exposed control/treatment counts, primary and guardrail rates, sample/window quality, limitations and an owner decision request.',
    minimumInput: 'Validated execution record, frozen plan/metric policy, provider exposure and product or CRM outcome sources, readout cutoff, identity join, guardrail threshold and review owner.',
    optionalConnections: 'Experiment or feature-flag platform, analytics warehouse, CRM/billing and support records through scoped MCPs or exports; authorized snapshots support a first read-only result.',
    exampleRequests: ['Did the approved guided setup experiment move day-30 retention, with guardrails and sample limits?', 'Show why this test is still inconclusive even though treatment is ahead.'],
    method: [
      'Validate exact experiment ID, plan revision, launch receipt, assignment unit, variant IDs, population, pre-registered metrics and readout window.',
      'Join provider exposure to authorized outcome and guardrail sources; exclude ineligible, duplicate and contaminated units and show identity coverage.',
      'Wait for the complete outcome window and source lag; compute control and treatment numerators, denominators and rates under the frozen rule.',
      'Check minimum sample, allocation, instrumentation, guardrail threshold and analysis method before a decision. Report inconclusive when any gate is missing.',
      'Return the observed effect and limitations to the owner; keep any shipping, rollback, budget or customer action in a separate approved route.',
    ],
    evidence: 'Show exact execution artifact, provider and event source revisions, control/treatment counts, rate arithmetic, guardrail evidence and frozen decision rule.',
    boundary: 'Do not declare a win from an immature window, missing control, underpowered sample or guardrail breach; do not change the pre-registered target after seeing outcomes or ship the variant from a readout.',
    handoff: 'Growth Experimentation and Follow-Through consumes experiment-execution-record/v1 and emits experiment-outcome-readout/v1 only for a provider-confirmed launch. A readout is measured or inconclusive, with separate owner decision and action receipts.',
    repeatRule: 'Keep experiment and assignment IDs stable, preserve each source revision and original policy, wait for late outcomes, and supersede a prior readout only with a cited correction.',
    sourceProbe: 'Read the validated launch receipt, frozen plan and assignment revision, eligible/exposed units for control and treatment, primary outcomes and guardrail source for the predeclared window. Record identity joins, data lag, exclusions and provider revisions.',
    acceptanceCheck: 'Recalculate each arm\'s primary and guardrail numerator/denominator under the frozen rule. A missing launch, immature window, sample shortfall, changed assignment or incomplete join yields inconclusive, never a winner or rollout instruction.',
    exampleInput: 'Fictional input: exp-guided-setup-7 launched at 50/50 under plan rev3; day-30 window closed; 200 eligible control and 200 eligible treatment accounts; support-blocker guardrail threshold 5%.',
    workedExample: 'Fictional output: control retained 100/200=50%; treatment retained 114/200=57%; observed difference +7 percentage points. Support blockers 6/200=3% control and 8/200=4% treatment, below the frozen 5% threshold. Power review target was 250 per variant, so status=inconclusive and no winner or ship action is claimed.',
    inadequateExample: '“Treatment is 7 points higher, so publish it to everyone.”',
    inadequateReason: 'The sample misses the frozen power target, and a raw difference does not authorize shipping or prove a reliable winner.',
  },
]

const marketingTemplateVersion = 2

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm marketing role and owner', instructions: 'Confirm whether ' + spec.name + ' is this Crew’s primary role or an added capability. Preserve existing identity and name the accountable owner.' },
    { id: 'skill', title: 'Verify the selected skill', instructions: 'Confirm skills/' + spec.id + '/SKILL.md exists and ' + spec.id + ' is selected for this Crew.' },
    { id: 'scope', title: 'Set exact job and measurement scope', instructions: 'Record offer, market, campaign or experiment identity, period and time zone, owner, allowed sources, and first job. Minimum input: ' + spec.minimumInput },
    { id: 'access', title: 'Prove the specialist source join', instructions: spec.sourceProbe + ' ' + spec.optionalConnections },
    { id: 'policy', title: 'Verify the specialist acceptance rule', instructions: spec.acceptanceCheck + ' Record citation, privacy, approval and action boundaries. ' + spec.boundary },
    { id: 'first_result', title: 'Produce first sourced result', instructions: 'Use real authorized input to produce ' + spec.firstResult + ' ' + spec.evidence + ' Fictional examples do not complete this check.' },
    { id: 'review', title: 'Review result with owner', instructions: 'Show calculations, source coverage, uncertainty, proposed decision, and owner correction. Record the review decision.' },
    { id: 'delivery', title: 'Choose read-only or action route', optional: true, instructions: 'Choose read-only chat or a separately authorized campaign, page, experiment, report, or contact write route. Read-only completes this choice.' },
    { id: 'recurrence', title: 'Choose repeat rule', optional: true, instructions: 'Choose manual-only or a reviewed event/schedule with stable IDs, deduplication, cost, and notifications. Manual-only completes this choice. ' + spec.repeatRule },
  ]
  return JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: marketingTemplateVersion, checks, completed_steps: [] }, null, 2) + '\n'
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
    '## Source probe and acceptance', '', spec.sourceProbe, '', spec.acceptanceCheck, '',
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
    'Template ' + spec.id + ' version ' + marketingTemplateVersion + '. Progress lives in templates/' + spec.id + '/TEMPLATE_SETUP.json and is verified in Crew chat.', '',
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

export const marketingSpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = 'templates/' + spec.id
  const skillPath = 'skills/' + spec.id + '/SKILL.md'
  const setupGuidePath = base + '/SETUP.md'
  const setupPath = base + '/TEMPLATE_SETUP.json'
  return {
    id: spec.id, version: marketingTemplateVersion, category: 'Marketing', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
