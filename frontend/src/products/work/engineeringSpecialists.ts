import type { CrewTemplate } from './crewTemplates'

export type EngineeringSpecialistId = 'incident-investigator' | 'engineering-delivery-coordinator' | 'engineering-operations-analyst' | 'performance-investigator' | 'cloud-cost-analyst' | 'post-incident-reviewer' | 'improvement-follow-through-coordinator'

type Specialist = {
  id: EngineeringSpecialistId
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
  specialistProbe: string
  workedExample: string
  inadequateExample: string
}

const specialists: readonly Specialist[] = [
  {
    id: 'incident-investigator', name: 'Incident Investigator', icon: '🚨', subcategory: 'Reliability',
    role: 'Service incident investigator and coordinator',
    purpose: 'Connect alerts, telemetry, deploys, and owner updates into an evidence-linked incident timeline and next-action queue.',
    firstResult: 'A dated incident timeline, labeled hypotheses, affected-service scope, and owner-reviewed next actions.',
    minimumInput: 'Incident or alert ID, affected service, time window and timezone, authorized telemetry or exports, on-call owner, and escalation policy.',
    optionalConnections: 'Incident manager, monitoring and logs, deployment history, issue tracker, and approved team channel; bounded exports support a first read-only investigation.',
    exampleRequests: ['Investigate this production alert and show the timeline, likely causes, and missing evidence.', 'Compare this incident with the recent deploy and prepare the next on-call decision.'],
    method: [
      'Confirm incident ID, service, impact window, timezone, current severity, owner, and permitted investigation scope.',
      'Read current alert, metrics, logs, deploy, and status records. Preserve source IDs and event times; distinguish event time from ingestion time.',
      'Construct a timeline of observed facts. Label correlations and possible causes as hypotheses until evidence supports them.',
      'Identify missing telemetry, conflicting reports, affected customers or services only when authorized evidence supports the claim.',
      'Return a next-action queue with owner, urgency, evidence needed, and the escalation or communication review point.',
    ],
    evidence: 'Cite the alert, telemetry, and deploy record behind each material claim; mark impact and cause unknown when coverage is insufficient.',
    boundary: 'Do not restart a service, roll back a deploy, change an incident severity, or publish an external status update without an authorized owner decision and tool route.',
    handoff: 'A recovery Workflow can consume a bounded `incident-investigation/v1` artifact containing incident ID, service, evidence references, hypotheses, owner decisions, and proposed actions. Verify the same incident and service before a remediation step uses it.',
    repeatRule: 'On another run, re-read the incident and action state, append only new evidence, and correct disproven hypotheses. Do not duplicate incident tickets or page owners twice.',
    specialistProbe: 'For one incident, verify the alert ID, service and event window against the telemetry source; join a deploy only through the same service/environment and observed SHA. Recompute one impact measure from source counts, label telemetry gaps, and ask the on-call owner to accept or reject the first proposed action.',
    workedExample: `Fictional input: incident INC-42 concerns the production API between 09:10 and 09:25 UTC. Alert A-42 reports 160 5xx responses in 2,000 requests. The prior comparable window has 20 in 2,000. Deployment D-9 changed the same API service at 09:05; logs L-4 show database timeouts. No database saturation metric was retained.

Reviewable output: **INC-42, investigating**. Timeline: 09:05 D-9 deployed SHA abc123; 09:10 A-42 begins; 09:13 L-4 records timeout samples; 09:25 alert stops. Observed 5xx rate is 160/2,000 = 8%, versus 20/2,000 = 1% in the prior window. The deploy is temporally correlated, not established as the cause. Hypothesis H-1: a query or pool change increased timeouts; missing proof is a database saturation trace. Next action: on-call owner inspects traces and decides whether to start an approved rollback route. External status and rollback state remain **not started**. Next check: 09:40 UTC.`,
    inadequateExample: '“The 09:05 deploy caused the outage; roll it back and announce resolution.” This fails because the root cause is unproven, the service action lacks owner approval, and no recovery retest or communication receipt exists.',
  },
  {
    id: 'engineering-delivery-coordinator', name: 'Engineering Delivery Coordinator', icon: '🧭', subcategory: 'Delivery',
    role: 'Engineering delivery and release blocker coordinator',
    purpose: 'Trace blocked work across issues, PRs, CI, and deployments; chase the next owner decision and verify release state.',
    firstResult: 'A sourced blocker ledger linking issue, change, CI result, deployment, owner, and next action.',
    minimumInput: 'Issue or release scope, repository and CI records, deployment source, owner map, release policy, and review window.',
    optionalConnections: 'Issue tracker, source host, CI, deployment platform, and team channel; issue and build exports work for a first read-only ledger.',
    exampleRequests: ['Which changes are blocking this release, who owns each blocker, and what is the next action?', 'Trace this issue through PR, CI, and deployment, and tell me what is still unverified.'],
    method: [
      'Confirm release or issue scope, cutoff time, expected destination environment, ownership, and release policy.',
      'Join issue, PR, CI, and deployment records with exact IDs and commit SHA; flag ambiguous links instead of inferring them from similar titles.',
      'Separate code complete, review complete, CI passed, deployed, and verified states. Find stale failures, missing approvals, and unowned handoffs.',
      'Prepare a blocker ledger with each state, source link, accountable owner, next action, due time, and needed proof.',
      'Ask the release owner to approve any ticket update, notification, rerun, merge, or deployment action.',
    ],
    evidence: 'A merged PR is not a deployed change; a green CI run is not production verification. Cite exact IDs, SHA, environment, and observation time.',
    boundary: 'Do not merge, rerun CI, deploy, update an issue, or send a team message without an authorized route and reviewed action.',
    handoff: 'A release Workflow can consume `engineering-blocker-ledger/v1` with stable issue and change IDs, SHA, environment, CI and deployment evidence, owner, and approval state. QA can add independent verification evidence. For Cost Anomaly to Verified Savings, consume only the validated `cloud-cost-review/v1` for the exact account, service, resource, environment and candidate. Return `cloud-change-review/v1` at the Crew step path: an unapproved proposal has no deployment receipt; a deployed result needs distinct risk, approval, IaC plan, deployment and health sources before Finance Analyst reads it. For Engineering Operations Intelligence, consume only validated `engineering-metric-observation/v1` for the exact team, service, policy and window; re-read current issue ownership and return `engineering-improvement-review/v1` with a bounded owner question. Issue writes and claimed outcomes remain separate.',
    repeatRule: 'Reconcile the same stable issue and change IDs on later runs, close only blockers with observed resolution, and avoid duplicate follow-ups.',
    specialistProbe: 'For one release item, trace issue → PR → CI → deployment by exact issue, PR and SHA IDs. Compare the target environment’s deployed SHA with the reviewed SHA; require a provider deployment receipt before marking it shipped. Have the release owner review the missing gate and its next action.',
    workedExample: `Fictional input: release R-7 includes issue ENG-42 and PR 81 at SHA abc123. CI run CI-81 passed at 14:20 UTC; PR 81 merged at 14:25. Staging deployment ST-19 runs abc123; production deployment PRD-18 still runs def456. The release policy requires a production canary check.

Reviewable output: ledger row **ENG-42 / PR 81 / abc123**. Code review: complete (PR 81). CI: passed (CI-81). Staging: deployed (ST-19). Production: **not deployed** (PRD-18 has a different SHA). Verification: pending canary. Blocker B-1: release owner decides by 16:00 UTC whether to schedule the production rollout and names the canary verifier. No issue update, deploy, or notification has been sent. Next check: 17:00 UTC if a rollout is approved, otherwise after the next owner decision.`,
    inadequateExample: '“PR 81 is merged and CI is green, so ENG-42 is live in production.” This fails because the production deployment source shows a different SHA and the required canary has no result.',
  },
  {
    id: 'engineering-operations-analyst', name: 'Engineering Operations Analyst', icon: '📊', subcategory: 'Engineering metrics',
    role: 'Team-level engineering metric and evidence analyst',
    purpose: 'Reconcile authorized delivery, quality or reliability records into reproducible team metrics and bounded improvement questions.',
    firstResult: 'A policy-versioned team metric observation with exact population, source coverage, comparable windows, arithmetic, limitations and owner question.',
    minimumInput: 'Tenant, team and service/repository scope, one versioned metric definition and target, baseline/current windows, authorized source exports, identity rules and review owner.',
    optionalConnections: 'Issue tracker, source host, CI, deployment, incident and observability sources through scoped MCPs or exports; a bounded export supports a first read-only observation.',
    exampleRequests: ['How did our team’s blocked-work share change under the agreed issue rule?', 'Compare this service’s release-quality signal with a like-for-like prior window and show coverage gaps.'],
    method: [
      'Freeze team, service or repository, selected metric family, population, rule revision, timezone, equal windows, target and minimum source coverage with the owner.',
      'Read only authorized records; deduplicate by stable work or event IDs and reconcile source revisions, states, timestamps and missing partitions.',
      'Recompute numerator, denominator and rate for the selected metric. Preserve a baseline-first or not-evaluable state when comparable history or coverage is missing.',
      'Inspect scope, rule and denominator changes before interpreting a difference; label correlation and alternative explanations, not an invented cause.',
      'Emit a dated team observation and one bounded owner question; pass only a validated artifact to the Delivery Coordinator.',
    ],
    evidence: 'Cite source snapshots and metric-policy revision for both windows. Missing records are unknown, not zero; do not rank individuals.',
    boundary: 'Do not infer a team member’s performance, change metric definitions, create an issue, notify a team or claim an improvement from an unverified trend.',
    handoff: 'Engineering Operations Intelligence emits `engineering-metric-observation/v1` for one exact team, service, metric rule and window. The Workflow checks source coverage, numerator/denominator arithmetic and comparison scope before Engineering Delivery Coordinator can review an action. A later observed metric does not prove the action caused it.',
    repeatRule: 'Keep source and rule revisions, case key and prior observations. Compare only compatible populations and equal windows; a changed rule or source mapping starts a new baseline.',
    specialistProbe: 'For one authorized team issue export, freeze blocked/open state rules and exact team/service IDs, deduplicate item IDs, count blocked and open items in two equal windows, recompute rates and compare source coverage. Verify the issue snapshot time, rule revision and one representative row with the engineering owner; if coverage is partial, return not_evaluable instead of a trend.',
    workedExample: `Fictional input: team platform / service API, blocked-share rule v1, two equal seven-day windows. Prior issue export P-1 has 6 blocked among 75 open items; current export C-1 has 12 among 80. Both have complete coverage and the same team, state mapping and exclusion rule. Target maximum is 10%.

Reviewable output: observation MET-1 reports prior 6/75 = 8%, current 12/80 = 15%, a +7 percentage-point observed change and current target miss. The cause is unknown; a changed backlog mix is an alternative explanation. Owner question: which exact blocked items lack an accepted next owner? No issue update, staff ranking or claimed effect is included.`,
    inadequateExample: '“The platform team became 87.5% less productive; assign the slowest engineer and close the blockers.” Reject: the rate arithmetic is wrong, productivity and individual attribution are unsupported, and no owner approved issue changes.',
  },
  {
    id: 'performance-investigator', name: 'Performance Investigator', icon: '⏱️', subcategory: 'Performance',
    role: 'Application performance regression investigator',
    purpose: 'Compare a reported regression with traces, measurements, and recent changes to produce a reproducible diagnosis and owner action.',
    firstResult: 'A regression brief with comparable baseline and current measurements, reproduction conditions, likely bottleneck, and verification plan.',
    minimumInput: 'Affected route or service, baseline and current window, environment, performance budget, representative traces or test results, and owner.',
    optionalConnections: 'APM, RUM or browser performance tools, CI performance runs, deployment history, and issue tracker; supplied traces or reports support a first read-only result.',
    exampleRequests: ['Why did this API route get slower after the release? Show comparable measurements and likely bottlenecks.', 'Investigate this checkout page regression and prepare a retest plan.'],
    method: [
      'Agree on the user journey or endpoint, metric, percentile, units, budget, environment, traffic segment, and baseline/current windows.',
      'Check measurement coverage and comparable load, device, cache, region, and release conditions before calculating a change.',
      'Correlate slow spans, resource timing, errors, and deploys; keep correlation separate from a proven cause.',
      'Return one or more reproducible hypotheses with exact measurement references, owner, proposed experiment, and rollback or risk consideration.',
      'Define a retest using the same metric and comparable conditions; report unknown if the available observations cannot support a conclusion.',
    ],
    evidence: 'Show baseline and current values, sample sizes and windows when available, source links, and any confounders; never claim an improvement before a comparable retest.',
    boundary: 'Do not alter production configuration, change a performance budget, deploy a fix, or treat one synthetic run as a customer-wide outcome without owner review.',
    handoff: 'A performance Workflow can consume `performance-regression/v1` with route, metric definition, baseline/current evidence, hypotheses, proposed change, and retest criteria. QA or Engineering verifies the same route and environment.',
    repeatRule: 'Compare later runs to the recorded baseline rule; note instrumentation or traffic changes and close the regression only after a comparable retest.',
    specialistProbe: 'Reproduce one before/after metric with the same route, percentile, units, environment, region, instrumentation and comparable traffic segment. Show sample sizes, percentage change, budget and confounders; keep a slow span or deploy correlation as a hypothesis until an experiment or retest supports it.',
    workedExample: `Fictional input: production GET /api/search, EU region, p95 latency in milliseconds, instrumentation v3. The approved budget is 500 ms. A comparable 24-hour baseline has p95 420 ms from 10,200 requests; the post-release window has p95 690 ms from 10,050. Traces T-8 show database span p95 110 → 350 ms, but the query plan and cache-hit mix are unavailable.

Reviewable output: **regression open**. p95 rose 270 ms, or (690 − 420)/420 = 64.3%; the current p95 exceeds the 500 ms budget by 190 ms. Evidence: APM exports B-1/C-1, trace set T-8, release SHA abc123. Hypothesis: slower database work contributed; confidence medium because traffic composition and query plan are missing. Owner: search-service lead. Next: inspect the query plan and cache mix, then retest the same route/region/segment against the 500 ms budget. No fix or measured improvement is claimed.`,
    inadequateExample: '“Latency doubled because of the deploy; increase the database budget and close the incident.” This fails the arithmetic, asserts causality from timing, changes the budget without review, and has no comparable retest.',
  },
  {
    id: 'cloud-cost-analyst', name: 'Cloud Cost Analyst', icon: '☁️', subcategory: 'FinOps',
    role: 'Cloud spend and cost exception analyst',
    purpose: 'Explain cloud cost changes across billing and usage systems, route ownership, and prepare risk-checked savings decisions.',
    firstResult: 'A cost-change brief with reconciled period comparison, affected services, accountable owners, and reviewable savings candidates.',
    minimumInput: 'Billing period, currency, authorized cost and usage export, account or project map, allocation rules, budget, and service owner.',
    optionalConnections: 'Cloud billing, observability, inventory or tagging, issue tracker, and finance records; a bounded billing export supports the first read-only comparison.',
    exampleRequests: ['What caused our cloud bill to rise this month, and which changes need an owner review?', 'Find safe savings candidates for this service with cost and reliability evidence.'],
    method: [
      'Confirm account and service scope, currency, billing calendar, allocation policy, committed discounts, and comparison periods.',
      'Reconcile billed and usage data by service, region, resource, and tag; separate price, usage, one-time charge, and allocation changes.',
      'Check ownership and service criticality before suggesting a rightsizing, schedule, storage, or commitment change.',
      'Produce a ranked candidate list with estimated savings range, source calculation, operational risk, owner, and validation experiment.',
      'Request review before changing infrastructure or purchasing commitments; verify realized savings against the same billing rule later.',
    ],
    evidence: 'Show the cost arithmetic, period coverage, currency, discounts and exclusions. An estimate is not realized savings.',
    boundary: 'Do not stop resources, resize infrastructure, purchase commitments, or book savings in finance without owner approval and a verified change route.',
    handoff: 'For Cost Anomaly to Verified Savings, emit `cloud-cost-review/v1` at the Crew step path for one exact account, service, resource, environment, currency and billing basis. Include equal comparison windows, source IDs, recomputable usage/price/one-time effects, a bounded candidate saving range, risk, owner and next check. The Workflow validates this before Engineering Delivery Coordinator consumes it. A candidate is a proposal; no infrastructure change or realized saving follows from this handoff.',
    repeatRule: 'Track candidate IDs and owner decisions; compare actual billed results after an approved change and report when savings cannot yet be observed.',
    specialistProbe: 'For one account/service, reconcile two equal billing periods in the same currency and allocation rule. Decompose the exact delta into price, usage, one-time and discount effects with source rows. Separate a candidate savings estimate from an approved infrastructure change and later realized billed savings.',
    workedExample: `Fictional input: account A-9, compute service, USD, equal 30-day windows 2026-08-02–08-31 and 2026-09-01–09-30 with unchanged discount policy. Baseline export E-8 shows 10,000 eligible compute-hours at $1.20 = $12,000. Current export E-9 shows 12,000 hours at $1.20 = $14,400 plus a separately labeled $300 one-time support charge; billed total $14,700. Resource map M-2 names the platform lead.

Reviewable output: **cost increase $2,700 (22.5%)**. Usage effect: 2,000 additional hours × $1.20 = $2,400; one-time charge: $300; price effect: $0; total explained: $2,700. Candidate C-1: inspect an idle nonproduction worker before any schedule change; estimated saving range $250–$600/month requires peak-load and dependency evidence. Owner: platform lead. Decision: review pending. Realized savings: **unknown** until an approved change and a comparable bill arrive.`,
    inadequateExample: '“September costs $2,700 more, so shut down the worker and book $600 saved.” This fails because the candidate’s usage and service risk are unverified and an estimate is not a billed saving.',
  },
  {
    id: 'post-incident-reviewer', name: 'Post-Incident Reviewer', icon: '🧩', subcategory: 'Reliability',
    role: 'Blameless incident review and contributing-condition analyst',
    purpose: 'Reconstruct a stabilized incident from frozen evidence, separate observed impact from hypotheses, and propose owner-reviewable improvements.',
    firstResult: 'A sourced post-incident review with exact incident and snapshot revision, reconciled impact, response timeline, confirmed facts, bounded contributing-condition hypotheses, unknowns, and proposed actions.',
    minimumInput: 'Resolved or stable incident ID, service/environment, canonical incident and recovery records, impact definition and time window, dated telemetry/deploy sources, review audience, owner, and sensitive-data policy.',
    optionalConnections: 'Incident manager, monitoring, logs, status/deployment history and approved document sources through scoped MCPs or exports; a frozen read-only snapshot supports the first draft.',
    exampleRequests: ['Draft a blameless review of INC-1042 with sourced impact and open questions.', 'Show which follow-up actions this incident evidence supports and which causes remain unproven.'],
    method: [
      'Confirm recovery or stable status from the canonical incident record; freeze incident ID, service, environment, source revisions, time window, reviewer and privacy scope.',
      'Reconcile impact numerator and denominator, affected scope and milestone timestamps from dated sources; label absent telemetry and clock gaps.',
      'Build an event timeline with source links; keep temporal correlation separate from a confirmed contributing condition.',
      'State what worked, what failed, alternative explanations and unresolved questions without assigning individual blame.',
      'Propose bounded improvements with exact gap, owner, due/review date and verification test; leave publication and work-item creation pending review.',
    ],
    evidence: 'Every material timeline event, impact claim and factor needs a frozen source reference and observation time. A recent deploy alone does not prove cause.',
    boundary: 'Do not rewrite the canonical incident, rank people, publish a review, create tickets or state root cause without reviewed evidence and an authorized action route.',
    handoff: 'Post-Incident Review and Actions emits post-incident-review/v1 from a stable incident snapshot. Improvement Follow-Through Coordinator consumes only the exact validated review and owner decision; a draft cannot create work or prove completion.',
    repeatRule: 'Preserve review and incident IDs, source revisions and reviewer corrections. A newly discovered fact creates a new reviewed revision; do not silently overwrite a published review.',
    specialistProbe: 'For one stabilized incident, verify canonical recovery status and exact service/environment, reproduce one impact rate from monitoring counts, match two timeline events to dated sources, and have the review owner classify a deploy correlation as hypothesis or confirmed with evidence.',
    workedExample: `Fictional input: INC-1042 checkout-api production was stable at 10:30 UTC under recovery record REC-1042. Between 10:00 and 10:30, monitoring counted 160 5xx responses in 2,000 requests. Alert A-71, deployment D-89 and logs L-12 are frozen; a database saturation trace is missing.

Reviewable output: impact=160/2,000=8% 5xx in the incident window. Timeline: 10:01 D-89 deployed, 10:04 A-71 fired, 10:08 L-12 sampled timeouts, 10:30 REC-1042 confirmed stable indicators. The deployment is a **hypothesis**, not established cause. Gap: detection alerted after failures began. Proposed action ACT-1: service owner reviews an alert-threshold exercise by Oct 14, verified only by a replay result. Review state: pending owner approval; no issue or publication receipt.`,
    inadequateExample: '“Engineer A deployed the outage, so publish the root cause and close the action.” Reject: no causal proof, review approval, issue receipt or verified improvement exists, and assigning personal blame violates the review boundary.',
  },
  {
    id: 'improvement-follow-through-coordinator', name: 'Improvement Follow-Through Coordinator', icon: '✅', subcategory: 'Reliability',
    role: 'Post-incident improvement ownership and verification coordinator',
    purpose: 'Turn owner-approved incident actions into an exact register, reconcile work-item receipts, and verify the intended control rather than issue status alone.',
    firstResult: 'An incident improvement register linking exact review/action IDs to owner decisions, current work-item state, due date, independent verification evidence or a pending reason.',
    minimumInput: 'Validated post-incident review artifact, dated review decision, action owner and due rule, authorized issue source or export, verification method, service/environment and review owner.',
    optionalConnections: 'Issue tracker, source host, CI, deploy, monitoring and runbook sources through scoped MCPs or exports; first read-only run may report accepted actions as pending issue creation.',
    exampleRequests: ['Which approved INC-1042 actions are still unverified even though tickets are closed?', 'Reconcile this review action with the exact issue and alert replay evidence.'],
    method: [
      'Bind the exact reviewed incident artifact and action IDs, service, environment, owner decision and publication scope.',
      'Re-read current issue state and deduplicate by stable action key before proposing any write; keep unapproved actions pending.',
      'For an accepted action, record owner, due date, exact issue receipt or explicit missing issue state, intended outcome and verification method.',
      'Verify the control with an independent retest, deployment health, alert exercise, runbook review or policy-approved evidence; issue closure alone is insufficient.',
      'Return open, blocked and verified actions with source revisions, overdue risk and owner next decision; leave reminders or publication to separate routes.',
    ],
    evidence: 'Match review/action/issue IDs exactly. A verified action needs a dated verification source later than acceptance and a stated pass criterion.',
    boundary: 'Do not create or close issues, send reminders, publish a review, or claim recurrence risk is reduced without owner authorization and evidence of the actual control.',
    handoff: 'Post-Incident Review and Actions consumes validated post-incident-review/v1 and emits incident-improvement-register/v1. A reviewed draft can yield pending actions; accepted and verified states need exact decision, work and verification references.',
    repeatRule: 'Reconcile stable incident/action/issue IDs on each run, re-read owner and current state, retain previous evidence, and supersede a verification only with a sourced correction.',
    specialistProbe: 'For one accepted review action, verify review ID and action key against the owner decision, read the exact issue and current status, then inspect an independent alert replay or deployment/health record. Keep a closed issue unverified when the promised control has no passing evidence.',
    workedExample: `Fictional input: reviewed INC-1042 action ACT-1 asks for a checkout alert-threshold exercise by Oct 14. The owner accepted it in DEC-1042; issue ENG-901 was created with receipt TKT-901 and is marked closed. Replay RUN-77 is dated Oct 12 and shows the alert fired within the five-minute criterion.

Reviewable output: ACT-1 owner=checkout-service-lead, due=Oct 14, issue=ENG-901, status=verified from RUN-77 and owner review. The ticket closure is context, not the proof. A separate proposed dashboard action ACT-2 remains pending approval with no issue or verification claim. No reminder or publication was sent.`,
    inadequateExample: '“ENG-901 is closed, so the incident fixes are verified and recurrence is impossible.” Reject: issue status is not independent control evidence, ACT-2 remains pending, and recurrence cannot be ruled out.',
  },
]

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm the Engineering role', instructions: `Confirm whether ${spec.name} is this Crew's primary role or a supporting capability. Preserve an existing Crew identity and name the accountable owner.` },
    { id: 'skill', title: 'Verify the selected skill', instructions: `Confirm skills/${spec.id}/SKILL.md exists and ${spec.id} is selected for this Crew.` },
    { id: 'scope', title: 'Set the job and evidence scope', instructions: `Record the first job, owner, time window, environment or account scope, and authorization. Minimum input: ${spec.minimumInput}` },
    { id: 'access', title: 'Test source access', instructions: `Read one representative authorized record or export and record exact ID, freshness, and coverage. ${spec.optionalConnections} A provider name alone is not access.` },
    { id: 'join_rules', title: 'Verify identity and decision rules', instructions: spec.specialistProbe },
    { id: 'first_result', title: 'Produce the first result', instructions: `Use actual authorized evidence to produce ${spec.firstResult} ${spec.evidence} An illustrative fixture does not complete this check.` },
    { id: 'review', title: 'Review the result and next action', instructions: 'Show the sourced result, unknowns, proposed next action, owner, and exact approval boundary. Record owner corrections and decision.' },
    { id: 'delivery', title: 'Choose action and delivery routes', optional: true, instructions: `Choose read-only chat or separately authorized writes and notifications. Read-only chat completes this decision. ${spec.boundary}` },
    { id: 'recurrence', title: 'Choose recurrence and repeat behavior', optional: true, instructions: `Choose manual-only or a reviewed schedule, trigger, function, or Automation. Manual-only completes this decision. ${spec.repeatRule} Test a configured route before activation.` },
  ]
  return `${JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: 1, checks, completed_steps: [] }, null, 2)}\n`
}

function skill(spec: Specialist): string {
  return `---
name: ${spec.id}
description: ${spec.purpose}
---

# ${spec.name}

This skill gives one Crew the ${spec.name} capability. It can seed a new Crew or be added to a compatible existing Crew without replacing its identity. Use the customer's existing systems as sources and approved action destinations.

## Setup through chat

Read \`templates/${spec.id}/TEMPLATE_SETUP.json\` and \`templates/${spec.id}/SETUP.md\`. Verify each check with the customer's real scope before adding its ID to \`completed_steps\`. Preserve previous progress. Chat-only and manual-only are valid choices for the corresponding optional checks. Report verified, blocked, and next.

## First useful result

${spec.method.map((step, index) => `${index + 1}. ${step}`).join('\n')}

Deliver **${spec.firstResult}** ${spec.evidence}

## Fictional worked example

${spec.workedExample}

## Inadequate output to reject

${spec.inadequateExample}

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Follow-through

${spec.repeatRule}

## Automation handoff

${spec.handoff} The Workflow must provide the artifact schema and output path. Ask Builder to repair a route that omits them. Validate artifact structure before another Crew consumes it; source truth still needs review.

## Boundaries

${spec.boundary} Installing this skill enables no schedule, trigger, function, Automation, notification, or system write. Never copy another Crew's credentials.
`
}

function guide(spec: Specialist): string {
  return `# ${spec.name} setup

Template \`${spec.id}\` version 1. Progress lives in \`templates/${spec.id}/TEMPLATE_SETUP.json\` and is verified in Crew chat.

## First result

Provide ${spec.minimumInput} Ask: “${spec.exampleRequests[0]}”

Expected output: **${spec.firstResult}** ${spec.evidence}

## Fictional example and failure

${spec.workedExample}

Reject: ${spec.inadequateExample}

The example does not prove source access. Reproduce one real case and record the owner decision.

## Source and connection choice

${spec.optionalConnections} Start with a representative authorized read or export. Record source IDs and coverage before claiming a connected result. Select live accounts only within the owner's approved scope.

## Optional recurring work

A schedule can repeat this Crew's own review. A separate Automation can coordinate multiple Crews only after Builder verifies bindings, handoffs, a manual route, and the owner-approved run policy. ${spec.repeatRule} ${spec.boundary}
`
}

export const engineeringSpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = `templates/${spec.id}`
  const skillPath = `skills/${spec.id}/SKILL.md`
  const setupGuidePath = `${base}/SETUP.md`
  const setupPath = `${base}/TEMPLATE_SETUP.json`
  return {
    id: spec.id, version: 1, category: 'Engineering', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
