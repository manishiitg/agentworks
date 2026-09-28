import type { CrewTemplate } from './crewTemplates'

export type SecuritySpecialistId = 'security-findings-analyst' | 'access-review-analyst' | 'security-remediation-coordinator'

type Specialist = {
  id: SecuritySpecialistId
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
    id: 'security-findings-analyst', name: 'Security Findings Analyst', icon: '🛡️', subcategory: 'Finding triage',
    role: 'Authorized application security findings analyst',
    purpose: 'Validate in-scope scanner or reported findings against the actual asset, classify risk with evidence, and prepare owner-reviewed remediation criteria.',
    firstResult: 'A finding queue with exact asset and build, source and reproducibility evidence, confidence, customer severity, owner, and proposed verification rule.',
    minimumInput: 'Written authorization and asset scope, finding source and IDs, environment or commit, allowed techniques, severity policy, evidence restrictions, and decision owner.',
    optionalConnections: 'Authorized scanner, repository, dependency alerts, issue tracker, asset inventory, and deployment records; a scoped finding export can support read-only triage.',
    exampleRequests: ['Triage these authorized dependency alerts for our deployed service and identify which need owner action.', 'Check whether this reported web finding applies to the approved staging target and prepare a safe verification plan.'],
    method: [
      'Confirm written target, environment, time window, allowed methods, rate and side-effect limits, stop conditions, and evidence access before any active check.',
      'Bind each observation to an exact asset, source revision or deployed build, finding ID, detector, timestamp, and affected component.',
      'Validate applicability and reproducibility with permitted evidence. Label scanner-only or stale reports unconfirmed rather than treating them as vulnerabilities.',
      'Apply the customer severity and asset-criticality policy; distinguish technical impact, exposure, confidence, and business priority.',
      'Return an owner decision queue with bounded remediation options, risk, due date, verification criteria, and restricted evidence reference.',
    ],
    evidence: 'A scanner severity is an input, not a verified business risk; retain source IDs, scope proof, confidence, and missing checks.',
    boundary: 'Do not test outside written scope, run intrusive probes without permission, expose exploit details or secrets, accept risk, or change code without an authorized review route.',
    handoff: 'Finding to Verified Remediation consumes security-finding/v1 keyed by asset, environment, build, finding ID, evidence, confidence, severity rule, owner, and retest criterion. Application Security Assessment and Remediation remains a method guide. A remediation Crew must recheck scope and current status.',
    repeatRule: 'On another run, reconcile stable finding and asset IDs, recheck version and deployment state, carry unresolved evidence forward, and avoid duplicate tickets or false closure.',
    sourceProbe: 'Read the written authorization, exact in-scope asset/environment/build, one detector finding and its evidence. If applicability depends on deployed code or package version, join to the deployed artifact or SBOM; a scanner title and repository dependency alone are insufficient.',
    acceptanceCheck: 'Reproduce one applicability and customer-severity decision from scope, deployed evidence and policy. Keep a scanner-only or stale result unconfirmed; withhold confirmed severity, remediation deadline and closure claims until the required source and owner decision exist.',
    workflowPlaybook: 'security-engineering/application-security/application-security-assessment-remediation',
    exampleInput: 'Fictional input: written scope=staging api.example.test only; finding=scan-44; asset=checkout-api; build=sha:a55c1; report=outdated dependency; severity policy=appsec-v2; owner=security-lead.',
    workedExample: 'Fictional output: finding=scan-44; asset=checkout-api; build=sha:a55c1; source=dependency-report:run-18; observed package version=2.3.1; deployed applicability=unknown pending SBOM confirmation; confidence=medium; customer severity=pending; owner=checkout-owner; proposed next=verify deployed SBOM then review fixed version; closure proof=deployed artifact and repeat scan.',
    inadequateExample: '“Critical vulnerability found; patch immediately.”',
    inadequateReason: 'It assumes scanner output is confirmed, omits the actual deployed version and customer severity rule, and gives no owner or retest criterion.',
  },
  {
    id: 'access-review-analyst', name: 'Access Review Analyst', icon: '🔐', subcategory: 'Permissions',
    role: 'Role, resource, and tenant access review analyst',
    purpose: 'Compare approved permission policy with observed UI and server behavior using authorized actors, isolated fixtures, and exact matrix evidence.',
    firstResult: 'An actor-by-resource-by-action matrix with expected and observed access, direct-route and cross-tenant evidence, exceptions, and an accountable owner.',
    minimumInput: 'Approved role and tenant policy, target environment and build, authorized test actors and fixtures, resource/action matrix, evidence policy, and decision owner.',
    optionalConnections: 'Identity provider read access, application test accounts, browser runner, API client, audit logs, and policy files; an approved export supports a first matrix review.',
    exampleRequests: ['Compare these admin and member permissions with our approved role matrix on staging.', 'Check whether a user from tenant A can reach tenant B data through a direct API route.'],
    method: [
      'Confirm approved policy source, actor roles, tenant and ownership states, target build, safe fixtures, and written scope.',
      'Enumerate expected allowed and denied cells before testing; use isolated actors and resources so results do not leak customer data.',
      'For each selected cell, capture UI behavior and server-observable result where applicable, including direct navigation and cross-tenant requests.',
      'Report mismatches, untested cells, policy ambiguity, and evidence gaps separately; a hidden button alone does not prove denial.',
      'Prepare an owner-reviewed exception or policy clarification and a same-cell retest rule.',
    ],
    evidence: 'Every claimed permission result needs actor, role, tenant, resource, action, expected policy, observed status, build, and source evidence.',
    boundary: 'Do not use real users or production identities without authorization, broaden privileges, make destructive privileged changes, or infer policy from current UI behavior.',
    handoff: 'Access Exception to Verified Fix consumes access-review-matrix/v1 keyed by policy revision, build, environment, actor role and tenant, resource and tenant, action, expected/observed direct result, attempt ID, and evidence. Role and Permission Validation remains the test-method guide. Security remediation receives only validated, owner-reviewed exceptions.',
    repeatRule: 'Preserve earlier matrix attempts, compare policy and build revisions, and re-execute the same denied cells after an approved fix before closing an exception.',
    sourceProbe: 'Read the approved role/tenant policy revision, isolated actor and resource fixture, exact build, and one expected allow or deny cell. Capture both UI state and a direct server attempt with actor, tenant, resource, action, HTTP result, trace ID and observation time.',
    acceptanceCheck: 'Verify the observed direct response against the predeclared policy cell. A hidden UI link does not prove a server denial; an unknown or blocked response is untested. For a cross-tenant exception, retain the same safe actor/resource cell for a later deployed retest.',
    workflowPlaybook: 'browser-qa/role-permission-validation',
    exampleInput: 'Fictional input: policy=roles-v4; build=sha:b912e; staging tenant A/member and tenant B/resource-7; expected=deny tenant A member read of tenant B invoice; test actor=test-member-a.',
    workedExample: 'Fictional output: cell=roles-v4/member/tenant-b-invoice/read; build=sha:b912e; actor=test-member-a; expected=403; UI=invoice link absent; direct API GET /invoices/7 observed=200 with tenant B fields; verdict=fail; evidence=trace:access-07 redacted; owner=authorization-team; retest=same cell after deployed fix.',
    inadequateExample: '“The invoice link is hidden, so tenant isolation works.”',
    inadequateReason: 'It skips direct server denial, exact policy and build identity, and the cross-tenant data observation.',
  },
  {
    id: 'security-remediation-coordinator', name: 'Security Remediation Coordinator', icon: '🧰', subcategory: 'Remediation',
    role: 'Security finding remediation and retest coordinator',
    purpose: 'Track one approved finding through owner assignment, reviewed change, deployment, independent retest, and evidence-backed closure.',
    firstResult: 'A finding action ledger with owner, decision, exact change and deployment IDs, retest status, and closure or risk-acceptance evidence.',
    minimumInput: 'Validated finding ID and asset, approved severity and fix criteria, owner and deadline, change/build/deployment sources, retest rule, and risk-acceptance policy.',
    optionalConnections: 'Issue tracker, source host, CI, deployment platform, scanner or test runner, and security reporting destination; supplied change records support read-only tracking.',
    exampleRequests: ['Track this approved finding from fix PR through deployed retest and tell me what remains open.', 'Which security fixes are merged but not verified in the affected environment?'],
    method: [
      'Confirm finding ID, asset, scope, severity, approved remediation or accepted-risk decision, owner, SLA, and evidence restrictions.',
      'Join issue, PR, commit, build, deployment, and retest by exact IDs and affected environment; keep proposed, merged, deployed, and verified states distinct.',
      'Check approval for the exact protected change or risk acceptance, including expiry and reviewer; do not infer approval from an issue comment.',
      'Require a retest against the deployed artifact using the finding-specific criterion and preserve negative or blocked results.',
      'Close only when the owner policy and independent retest evidence support it; otherwise return the next action and accountable owner.',
    ],
    evidence: 'A merged PR or passing unit test is not deployed remediation. Show actual deployment identity, retest target and result, and decision receipt.',
    boundary: 'Do not merge, deploy, change severity, accept risk, close a finding, or publish sensitive details without the required owner approval and authorized tool route.',
    handoff: 'Finding to Verified Remediation uses security-remediation-ledger/v1 keyed by finding, asset, issue, approved change SHA, affected deployed build, independent retest and disposition. Access Exception to Verified Fix uses access-remediation-ledger/v1 tied to the exact policy cell, actor, resource and direct retest. Each Playbook supplies its own validator and output path; the finding or policy source remains authoritative.',
    repeatRule: 'Re-read current finding, change, deployment, and retest records; preserve prior failures and risk decisions; do not duplicate tickets or close on a stale build.',
    sourceProbe: 'Read one exact finding and issue, owner approval, PR/commit, build and affected deployment record; fetch the retest criterion and current retest run separately. Record source IDs and observation times for every state transition, including an absent deployment or failed retest.',
    acceptanceCheck: 'Prove the approved change SHA reached the affected environment and an independent reviewer tested the finding-specific criterion on that deployed build before closure. A merged PR, passing unit test, old-build scan or self-reviewed retest must leave the finding open.',
    workflowPlaybook: 'security-engineering/application-security/application-security-assessment-remediation',
    exampleInput: 'Fictional input: finding=sec-81; asset=checkout-api; approved fix=PR-92; affected staging build=sha:c100a; required retest=deny unauthenticated /orders access; owner=appsec-owner.',
    workedExample: 'Fictional output: finding=sec-81; PR-92 merged at sha:c101b; deployed build=sha:c100a remains old; retest=not run against fix; state=awaiting deployment; owner=release-owner; next=deploy approved sha:c101b then rerun auth test; closure=blocked. No risk acceptance recorded.',
    inadequateExample: '“PR merged, security issue resolved.”',
    inadequateReason: 'It confuses code merge with deployment and omits the affected environment, independent retest, and closure decision.',
  },
]

const securityTemplateVersion = 2

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm Security role and owner', instructions: 'Confirm whether ' + spec.name + ' is the Crew’s primary role or a supporting capability. Preserve an existing identity and name the accountable Security and asset owners.' },
    { id: 'skill', title: 'Verify the selected skill', instructions: 'Confirm skills/' + spec.id + '/SKILL.md exists and ' + spec.id + ' is selected for this Crew.' },
    { id: 'scope', title: 'Record written scope and authorization', instructions: 'Record asset, environment, methods, time and rate bounds, stop conditions, evidence restrictions, owner, and first job. Minimum input: ' + spec.minimumInput },
    { id: 'access', title: 'Prove the role-specific source join', instructions: spec.sourceProbe + ' ' + spec.optionalConnections + ' A named product is not verified access.' },
    { id: 'policy', title: 'Verify the role-specific acceptance rule', instructions: spec.acceptanceCheck + ' Record policy revision, evidence restrictions and decision owner. Unknown stays unknown.' },
    { id: 'first_result', title: 'Produce the first sourced result', instructions: 'Use real authorized evidence to produce ' + spec.firstResult + ' ' + spec.evidence + ' A fictional fixture does not complete this check.' },
    { id: 'review', title: 'Review finding and next action', instructions: 'Show scope, exact source links, unknowns, owner, proposed action, review boundary, and any risk acceptance expiry. Record the owner decision.' },
    { id: 'delivery', title: 'Choose action and disclosure route', optional: true, instructions: 'Choose read-only chat or a separately authorized ticket, status, remediation, or notification route. Read-only chat completes this decision. ' + spec.boundary },
    { id: 'recurrence', title: 'Choose repeat and Automation route', optional: true, instructions: 'Choose manual-only or a reviewed trigger or schedule with deduplication, scope gate, and retest. Manual-only completes this decision. ' + spec.repeatRule },
  ]
  return JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: securityTemplateVersion, checks, completed_steps: [] }, null, 2) + '\n'
}

function skill(spec: Specialist): string {
  return [
    '---', 'name: ' + spec.id, 'description: ' + spec.purpose, '---', '',
    '# ' + spec.name, '',
    'This skill gives one Crew the ' + spec.name + ' capability. It can seed a new Crew or be added to a compatible existing Crew. The customer’s written scope and policy govern all security work.', '',
    '## Setup through chat', '',
    'Read templates/' + spec.id + '/TEMPLATE_SETUP.json and templates/' + spec.id + '/SETUP.md. Verify each check with actual authorized sources and decisions before adding its ID to completed_steps. Preserve progress and report verified, blocked, and next. Chat-only and manual-only are valid optional decisions.', '',
    '## First useful result', '',
    ...spec.method.map((step, index) => String(index + 1) + '. ' + step), '',
    'Deliver **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Source probe and acceptance', '', spec.sourceProbe, '', spec.acceptanceCheck, '',
    '## Follow-through', '', spec.repeatRule, '',
    '## Fictional worked example', '', spec.exampleInput, '', spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Workflow Playbook and handoff', '',
    spec.handoff + ' The existing Workflow Playbook is ' + spec.workflowPlaybook + '. Builder must bind an exact artifact path and schema before a consumer uses it. Validate structure and recheck source truth.', '',
    '## Boundaries', '',
    spec.boundary + ' Installing this skill enables no scan, schedule, trigger, function, Automation, notification, or write. Never copy another Crew’s credentials.', '',
  ].join('\n')
}

function guide(spec: Specialist): string {
  return [
    '# ' + spec.name + ' setup', '',
    'Template ' + spec.id + ' version ' + securityTemplateVersion + '. Progress lives in templates/' + spec.id + '/TEMPLATE_SETUP.json and is verified in Crew chat.', '',
    '## First result', '',
    'Provide ' + spec.minimumInput + ' Ask: “' + spec.exampleRequests[0] + '”', '',
    'Expected output: **' + spec.firstResult + '** ' + spec.evidence, '',
    '## Fictional example and failure', '', spec.exampleInput, '', spec.workedExample, '',
    'Inadequate: ' + spec.inadequateExample + ' Reason: ' + spec.inadequateReason, '',
    '## Source and connection choice', '',
    spec.optionalConnections + ' ' + spec.sourceProbe, '',
    '## Setup acceptance', '', spec.acceptanceCheck, '',
    '## Compatible Automation handoff', '', spec.handoff, '',
    '## Workflow and recurring work', '',
    'Existing Workflow Playbook: ' + spec.workflowPlaybook + '. Builder must verify written scope, bindings, handoffs, a manual route, and the owner-approved run policy before recurrence. ' + spec.repeatRule + ' ' + spec.boundary, '',
  ].join('\n')
}

export const securitySpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = 'templates/' + spec.id
  const skillPath = 'skills/' + spec.id + '/SKILL.md'
  const setupGuidePath = base + '/SETUP.md'
  const setupPath = base + '/TEMPLATE_SETUP.json'
  return {
    id: spec.id, version: securityTemplateVersion, category: 'Security', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
