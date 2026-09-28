import type { CrewTemplate } from './crewTemplates'

export type QASpecialistId = 'browser-journey-qa-analyst' | 'flaky-test-investigator' | 'release-quality-assistant'

type Specialist = {
  id: QASpecialistId
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
  workflowPlaybook: string
  exampleInput: string
  workedExample: string
  inadequateExample: string
  inadequateReason: string
}

const specialists: readonly Specialist[] = [
  {
    id: 'browser-journey-qa-analyst', name: 'Browser Journey QA Analyst', icon: '🧪', subcategory: 'User journeys',
    role: 'Browser journey and regression investigator',
    purpose: 'Run approved user journeys against an exact build, investigate failures, and return durable evidence and reproduction steps.',
    firstResult: 'An attempt-level journey result with exact build and environment, pass/fail or blocked status, reproduction steps, and linked visual and console/network evidence.',
    minimumInput: 'Approved journey and expected outcome, environment URL, exact build or commit, authorized test account and data, evidence policy, and QA owner.',
    optionalConnections: 'Browser or Playwright runner, CI, issue tracker, and test account vault; a supplied recording and test report support an initial read-only investigation.',
    exampleRequests: ['Run our signup-to-first-project journey on this release candidate and explain any failure with evidence.', 'Compare this checkout journey failure with the approved expectation and give the owner reproduction steps.'],
    method: [
      'Confirm the journey revision, expected outcome, exact build, environment, account, test-data bounds, and recording policy before execution.',
      'Reuse a verified browser profile and canonical locators. Record each attempt with start time, result, screenshots or video, console/network evidence status, and source revision.',
      'Distinguish product failure, test failure, environment blocker, and unresolved evidence. A retry is a separate attempt and never erases the first failure.',
      'Return reproduction steps, likely owner, missing evidence, and the review needed before changing a test or product.',
      'Retest the same approved journey against a named later build and report both observations.',
    ],
    evidence: 'A pass requires all required assertions and evidence; skipped, blocked, missing, or stale attempts remain visible and cannot become a pass.',
    boundary: 'Do not change production data, weaken assertions, approve a release, or create a ticket or message without the authorized action route and owner review.',
    handoff: 'The Critical Journey Validation Workflow can consume a journey-result/v1 record keyed by journey revision, build, environment, attempt ID, observed outcome, evidence links, classification, and owner. A release gate must verify exact build identity and required suite completeness.',
    repeatRule: 'On a later run, retain earlier attempts, compare the same journey and build identity rule, and record changed test or environment revisions before claiming a regression was fixed.',
    sourceProbe: 'Run one approved journey against a pinned build and environment using an authorized test account. Save the attempt ID, required assertion results, recording and console/network artifact IDs, and one failed or blocked step. A successful retry must remain a separate attempt.',
    acceptanceCheck: 'Reproduce one observed failure from the saved attempt on the same build, or mark it unresolved with the missing evidence. Check that a skipped assertion, absent required recording, or blocked account cannot yield pass.',
    workflowPlaybook: 'browser-qa/critical-journey-validation',
    exampleInput: 'Fictional input: journey=signup-to-project@v3; build=sha:abc123; environment=preview-17; expected=new user creates a project; account=test-user-17; required evidence=video and console/network log.',
    workedExample: 'Fictional output: attempt=qa-17-01; journey=signup-to-project@v3; build=sha:abc123; environment=preview-17; observed=project creation returned HTTP 403 after signup; verdict=fail; video=asset:qa-17-01.mp4; console/network=asset:qa-17-01.json; reproduction=sign in as test-user-17, submit project form, observe 403; owner=project-api-owner; retest=pending.',
    inadequateExample: '“Signup failed; retry later.”',
    inadequateReason: 'It omits the tested build, expected step, attempt, evidence, reproducible failure, and owner.',
  },
  {
    id: 'flaky-test-investigator', name: 'Flaky Test Investigator', icon: '🔁', subcategory: 'Test stability',
    role: 'Intermittent test failure investigator',
    purpose: 'Separate inconsistent application behavior from test or environment instability and prepare a reviewed stabilization decision.',
    firstResult: 'An attempt-by-attempt flake investigation with controlled inputs, failure and pass evidence, cause hypotheses, and a reviewable stabilization proposal.',
    minimumInput: 'Canonical test ID and source revision, exact build and environment, fixtures, recent attempt history, retry and concurrency policy, and owner.',
    optionalConnections: 'CI test history, browser traces, screenshots and video, source repository, and issue tracker; exported attempts support a first read-only analysis.',
    exampleRequests: ['This test passed on retry. Is it a product race, environment issue, or test defect?', 'Investigate the intermittent login test and propose a bounded experiment before changing it.'],
    method: [
      'Bind one canonical test, source revision, build, browser profile, environment, fixtures, and approved expectation.',
      'Compare original and repeated isolated attempts with consistent inputs; retain both passing and failing traces and timestamps.',
      'Check order dependence, shared state, network conditions, product races, locator drift, and environment outages; label the cause unresolved when evidence is insufficient.',
      'Propose a bounded experiment and exact candidate fix with owner, risk, and verification criterion. Preserve original assertions.',
      'After any approved change, rerun the canonical test under the agreed stability policy and keep its earlier failures visible.',
    ],
    evidence: 'A green retry is not proof of stability; report attempt count, result distribution, environment and build identity, and confidence limits.',
    boundary: 'Do not silently quarantine or skip a test, weaken an assertion, inflate waits, or change canonical test source without owner review and an authorized route.',
    handoff: 'The Flaky-Test Detection and Stabilization Workflow can consume flake-investigation/v1 with stable test and build IDs, attempt IDs, evidence, classification, confidence, candidate change, approval state, and later stability check.',
    repeatRule: 'Use the same test and build keys to compare new attempts; reopen a prior classification when product or environment evidence contradicts it.',
    sourceProbe: 'Fetch the canonical test revision and at least one original failed attempt plus its retry for the same build, environment, browser profile, and fixtures. Save each CI run and trace ID; a green summary without the original failure is insufficient.',
    acceptanceCheck: 'Run or inspect a bounded set of isolated attempts under fixed inputs, retain the result distribution, and classify product, test, environment, or unresolved. Reject a proposed wait, quarantine, or assertion change when the cause is still unresolved.',
    workflowPlaybook: 'browser-qa/flaky-test-detection-stabilization',
    exampleInput: 'Fictional input: test=login-refresh@v4; build=sha:def456; environment=staging-eu; fixtures=account-22; allowed attempts=5 isolated; prior result=pass after CI retry.',
    workedExample: 'Fictional output: attempts=flake-22-01..05 on sha:def456/staging-eu with same fixtures; results=fail, pass, fail, pass, fail; failing traces show refresh token response delayed after assertion; classification=unresolved product-or-environment race; confidence=low; owner=auth-team; proposal=inspect token service timing before any test change; original assertions retained.',
    inadequateExample: '“It passed on retry, so increase the wait and mark green.”',
    inadequateReason: 'A retry does not establish a test defect; this hides product and environment evidence and changes the assertion policy without review.',
  },
  {
    id: 'release-quality-assistant', name: 'Release Quality Assistant', icon: '🚦', subcategory: 'Release gates',
    role: 'Release candidate quality and evidence coordinator',
    purpose: 'Join exact change and build identity to required QA suites, classify missing evidence, and prepare an auditable gate decision for the release owner.',
    firstResult: 'A release quality brief with exact commit/build and environment, required suite matrix, missing or stale evidence, and proposed pass, fail, or needs-review decision.',
    minimumInput: 'Release or PR ID, commit SHA and build artifact, target environment, required suite policy and revisions, result records, gate owner, and deadline.',
    optionalConnections: 'Source host, CI, test runner, deployment records, issue tracker, and release status destination; exported results support a read-only gate review.',
    exampleRequests: ['Can this exact release candidate pass the QA gate? Show every required suite and missing result.', 'Explain why the release gate is blocked even though the last CI run is green.'],
    method: [
      'Bind the requested release, commit SHA, build artifact, deployment environment, policy revision, and cutoff time.',
      'Enumerate every required suite and group from policy before reading results; join results only by exact tested identity.',
      'Treat missing, cancelled, stale, skipped, blocked, or mismatched results as nonpassing. Preserve distinct rerun attempts.',
      'Derive pass, fail, or needs-review from the configured rule and show evidence and owner decisions for every exception.',
      'Publish a status only through an authorized route and retain the provider receipt; a proposed or attempted status is not a published gate.',
    ],
    evidence: 'A green CI summary alone does not prove the required suites ran against the reviewed build. Show policy, tested identity, source IDs, and receipt.',
    boundary: 'Do not merge, deploy, waive a required suite, auto-approve needs-review, or post an external status without the release owner and authorized action route.',
    handoff: 'The Release and PR Quality Gate Workflow can consume release-quality-brief/v1 with release, SHA, build, environment, policy revision, required-suite matrix, verdict, reviewer, and delivery receipt. Engineering may consume a separate blocker ledger for fixes.',
    repeatRule: 'Re-evaluate each new build independently; retain prior failures and waivers, and never reuse a passing result from a different SHA or environment.',
    sourceProbe: 'Read the release policy revision and enumerate its required suites before fetching CI results. Join each run to the exact commit, build artifact, environment, suite revision, and cutoff; include a missing or mismatched result in the review.',
    acceptanceCheck: 'Recompute the gate verdict from the complete required-suite matrix. Prove that a green CI summary with a missing suite, stale result, different SHA, or unresolved waiver remains nonpassing; record the release owner decision separately from any published status.',
    workflowPlaybook: 'browser-qa/release-pr-quality-gate',
    exampleInput: 'Fictional input: release=rc-28; SHA=987fed; build=artifact:rc-28-987fed; environment=preview-28; policy=gate-v5 requiring auth and critical-journey suites; owner=release-lead.',
    workedExample: 'Fictional output: release=rc-28; SHA=987fed; build=artifact:rc-28-987fed; environment=preview-28; policy=gate-v5; auth=pass run:auth-19 on same build; critical-journey=missing; verdict=needs_review; owner=release-lead; published_status=none; next=run required journey suite on exact artifact.',
    inadequateExample: '“CI is green; release approved.”',
    inadequateReason: 'It does not bind the reviewed artifact or account for the missing required journey suite and lacks an owner decision or delivery receipt.',
  },
]

const qaTemplateVersion = 2

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm the QA role and owner', instructions: 'Confirm whether ' + spec.name + ' is the primary Crew role or an added capability. Preserve an existing identity and name the QA owner.' },
    { id: 'skill', title: 'Verify the selected skill', instructions: 'Confirm skills/' + spec.id + '/SKILL.md exists and ' + spec.id + ' is selected for this Crew.' },
    { id: 'scope', title: 'Set the job and exact test scope', instructions: 'Record the first job, authorized account and data, expected outcome, build and environment identity, owner, and cutoff time. Minimum input: ' + spec.minimumInput },
    { id: 'access', title: 'Prove the source and runner join', instructions: spec.sourceProbe + ' Record access scope, freshness, and artifact paths. ' + spec.optionalConnections },
    { id: 'policy', title: 'Verify the role-specific acceptance rule', instructions: spec.acceptanceCheck + ' Record the customer policy, retention, and approval owner. Missing evidence is not a pass.' },
    { id: 'first_result', title: 'Produce a first QA result', instructions: 'Use actual authorized evidence to produce ' + spec.firstResult + ' ' + spec.evidence + ' A fictional example does not complete this check.' },
    { id: 'review', title: 'Review result and next action', instructions: 'Show source-linked facts, unknowns, exact proposed action, owner, and approval boundary. Record the owner decision and corrections.' },
    { id: 'delivery', title: 'Choose action and delivery route', optional: true, instructions: 'Choose read-only chat or separately authorized issue/status updates. Read-only chat completes this choice. ' + spec.boundary },
    { id: 'recurrence', title: 'Choose repeat and Automation route', optional: true, instructions: 'Choose manual-only or a reviewed trigger or schedule. Manual-only completes this choice. ' + spec.repeatRule + ' Test any configured route before activation.' },
  ]
  return JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: qaTemplateVersion, checks, completed_steps: [] }, null, 2) + '\n'
}

function skill(spec: Specialist): string {
  return [
    '---', 'name: ' + spec.id, 'description: ' + spec.purpose, '---', '',
    '# ' + spec.name, '',
    'This skill gives one Crew the ' + spec.name + ' capability. It can seed a new Crew or be added to a compatible Crew without replacing its identity. Use the customer’s approved test and release systems.', '',
    '## Setup through chat', '',
    'Read templates/' + spec.id + '/TEMPLATE_SETUP.json and templates/' + spec.id + '/SETUP.md. Verify each check with the customer’s real build, policy, and evidence before adding its ID to completed_steps. Preserve prior progress; report verified, blocked, and next. Chat-only and manual-only are valid optional decisions.', '',
    '## First useful result', '',
    ...spec.method.map((step, index) => String(index + 1) + '. ' + step), '',
    'Deliver **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Source probe and acceptance', '', spec.sourceProbe, '', spec.acceptanceCheck, '',
    '## Follow-through', '', spec.repeatRule, '',
    '## Fictional worked example', '',
    spec.exampleInput, '',
    spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Workflow Playbook and handoff', '',
    spec.handoff + ' The existing Workflow Playbook is ' + spec.workflowPlaybook + '. Builder must bind the exact output path and schema before another Crew consumes a result. Confirm source truth and customer policy independently.', '',
    '## Boundaries', '',
    spec.boundary + ' Installing this skill enables no schedule, trigger, function, Automation, notification, or write. Do not copy another Crew’s credentials.', '',
  ].join('\n')
}

function guide(spec: Specialist): string {
  return [
    '# ' + spec.name + ' setup', '',
    'Template ' + spec.id + ' version ' + qaTemplateVersion + '. Progress lives in templates/' + spec.id + '/TEMPLATE_SETUP.json and is verified in Crew chat.', '',
    '## First result', '',
    'Provide ' + spec.minimumInput + ' Ask: “' + spec.exampleRequests[0] + '”', '',
    'Expected output: **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Fictional example and failure', '',
    spec.exampleInput, '',
    spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Source and connection choice', '',
    spec.optionalConnections + ' ' + spec.sourceProbe, '',
    '## Setup acceptance', '', spec.acceptanceCheck, '',
    '## Workflow and recurring work', '',
    'Existing Workflow Playbook: ' + spec.workflowPlaybook + '. A separate Automation can coordinate Crews only after Builder verifies bindings, handoffs, a manual route, and the owner-approved run policy. ' + spec.repeatRule + ' ' + spec.boundary, '',
  ].join('\n')
}

export const qaSpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = 'templates/' + spec.id
  const skillPath = 'skills/' + spec.id + '/SKILL.md'
  const setupGuidePath = base + '/SETUP.md'
  const setupPath = base + '/TEMPLATE_SETUP.json'
  return {
    id: spec.id, version: qaTemplateVersion, category: 'QA', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
