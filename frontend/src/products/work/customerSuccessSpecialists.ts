import type { CrewTemplate } from './crewTemplates'

export type CustomerSuccessSpecialistId = 'customer-onboarding-coordinator' | 'product-adoption-analyst' | 'customer-health-coordinator' | 'lifecycle-analyst' | 'renewal-coordinator'

type Specialist = {
  id: CustomerSuccessSpecialistId
  version?: number
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
  deeperMethod: string
  specialistProbe: string
  workedExample: string
  inadequateExample: string
  setupScope?: string
  automationRoute?: string
  setupCase?: string
  setupSource?: string
  setupAccessScope?: string
}

const specialists: readonly Specialist[] = [
  {
    id: 'customer-onboarding-coordinator', name: 'Customer Onboarding Coordinator', icon: '🚀', subcategory: 'Onboarding',
    role: 'B2B customer onboarding coordinator',
    purpose: 'Turn an authorized new-customer handoff into an owned milestone plan and a source-linked blocker queue.',
    firstResult: 'An onboarding milestone register with owners, evidence, blockers, and the next customer decision.',
    minimumInput: 'Customer/account reference, purchased scope, desired first result, onboarding owner, target date, and an authorized handoff or export.',
    optionalConnections: 'CRM or signed deal handoff, onboarding tracker, product workspace, and approved customer inbox; a supplied handoff file is enough for the first plan.',
    exampleRequests: ['Create an onboarding plan for this new customer and show which milestones lack an owner or evidence.', 'Review this week’s new accounts and flag onboarding blockers without sending customer messages.'],
    method: [
      'Confirm the account, purchased product or plan, promised outcomes, first-value definition, onboarding owner, dates, and permitted handoff scope.',
      'Read the authorized signed handoff and current onboarding tracker. Separate contractual facts from sales notes and assumptions; reconcile conflicting dates or scope.',
      'Define a short sequence of observable milestones with a stable ID, accountable owner, due date, completion evidence, and blocker state for each.',
      'Mark a milestone complete only from an authorized product, customer, or owner record. Treat an invitation or setup email as an invitation, not adoption.',
      'Return the current register, missing inputs, at-risk dates, and one next decision for the owner to review.',
    ],
    evidence: 'Every completion and blocker cites a source and observation time; unobserved milestones remain pending or unknown.',
    boundary: 'Do not promise implementation dates, provision access, change account settings, or contact the customer without an authorized route and exact owner decision.',
    handoff: 'For New Customer to First Value, emit plain JSON `onboarding-milestone-register/v1` at the path supplied by the Crew step. Include account and handoff IDs, first-value goal, milestone state, source references, and owner. The Workflow validates it before Adoption consumes it.',
    deeperMethod: `## Milestone quality

Use the customer's actual promised scope; do not import a generic SaaS checklist as fact. Stable milestone IDs let the next run report changes rather than duplicate tasks. If the sales handoff is incomplete, show the exact missing decision. Customer outreach is a separate approved action with contact policy and prior-message checks.`,
    specialistProbe: 'Join one signed handoff to the onboarding tracker by exact account and tenant IDs. For each milestone, check owner, due date, approved first-value rule and evidence source. Reproduce one complete versus pending decision; reconcile conflicting scope or dates with the account owner before claiming progress.',
    workedExample: `Fictional input: signed handoff signed-handoff binds account-example-001 to tenant-example-001, names the onboarding owner and customer sponsor, and promises a production workspace plus a first project report export by 2026-09-30 17:00 UTC. Workspace record workspace-record shows the tenant provisioned at 09:45 on 2026-09-25. At the 10:00 review cutoff, no authorized export event is available.

Reviewable output: register register-example-001 for the exact account/tenant. Milestone “workspace-ready”: **complete**, owner Onboarding, due 2026-09-25 17:00, evidence workspace-record at 09:45. Milestone “first-report”: **pending**, owner Customer sponsor, due 2026-09-30 17:00, no completion evidence. First-value rule first-report-v1 is an authorized customer user exporting a project report from production. Next decision: account owner confirms the sponsor and whether training is needed; customer contact remains pending review. No invitation or workspace provision is called first value.`,
    inadequateExample: '“Onboarding is complete because the workspace invitation was sent.” Reject: an invitation is neither observed production provisioning nor the agreed customer report export, and the claimed milestone lacks a source.',
  },
  {
    id: 'product-adoption-analyst', name: 'Product Adoption Analyst', icon: '📈', subcategory: 'Adoption',
    version: 3,
    role: 'Evidence-led product adoption analyst',
    purpose: 'Verify customer first value, released-feature adoption, or one trial account’s bounded product use without inferring purchase intent.',
    firstResult: 'A first-value, feature-adoption, or trial-usage observation with exact identity, source records, coverage and next owner question.',
    minimumInput: 'Choose one route: first-value account and agreed event; released feature, flag, segment and use rule; or exact trial/account, subscription state, usage rule, window and authorized sources.',
    optionalConnections: 'Product analytics or bounded event export, onboarding tracker, and trial or subscription records; approved customer notes are optional.',
    exampleRequests: ['Did this customer reach the agreed first-value event? Show the exact evidence and gaps.', 'What did this trial account actually use, and what remains unknown? Do not infer buying intent.'],
    method: [
      'Confirm account identity, product entitlement, event names, success threshold, timezone, and observation window with the owner.',
      'Read only the authorized usage or setup source. Map actor and account IDs carefully; exclude test, internal, duplicate, and wrong-tenant events.',
      'Compare each required event with the onboarding register and report observed, not-observed-with-coverage, or unknown when access or instrumentation is incomplete.',
      'Keep seat invites, sign-ins, feature use, and the agreed outcome separate. Do not turn activity into value without the owner-defined rule.',
      'Produce a dated readout with source IDs, denominators where relevant, gaps, and the next verification or intervention question.',
      'For a released feature, bind release/build and flag revision, eligible segment, distinct account join, exposure and use predicates. Count exposed accounts separately from eligible accounts and feature users; keep partial coverage or insufficient sample as not evaluable.',
      'For a trial, bind one exact product account, tenant, trial/subscription and status revision. Deduplicate authorized events under a predeclared use rule and window; distinguish observed use, complete-window non-observation and unknown coverage. Do not infer contact permission or purchase intent.',
    ],
    evidence: 'Every observed event includes a source ID and time; feature counts cite eligibility and exposure, while trial use cites the exact trial state and event-coverage source. A missing event is not a failure when coverage is incomplete.',
    boundary: 'Do not invent adoption scores, infer churn from sparse use, expose another tenant’s data, or message the customer from an analysis request.',
    handoff: 'For New Customer to First Value, emit `first-value-readout/v1` linked to the onboarding register. For Released Feature to Adoption Decision, emit `feature-adoption-observation/v1` with exact release/flag/rule, eligible, exposed and used counts. For Trial Account to Reviewed Sales Assist, emit `trial-usage-observation/v1` with exact account, tenant, trial, subscription/status revision, rule, window, events and coverage. The Workflow validates the selected artifact before its consumer runs.',
    deeperMethod: `## First-value and feature measurement

The first-value event definition is customer-specific. Record the predicate and exclusions. Missing instrumentation returns **unknown**. For feature adoption, freeze release/build, feature flag revision, eligible segment and distinct-account rule before counting exposure and use. Compare only equal windows under the same rule; a single measured window is a baseline, not a trend or causal effect. For a trial, read current trial/subscription state separately from events, keep complete-window non-observation distinct from partial coverage, and pass only a bounded observation to Sales. A product signal does not establish a person’s consent, buying intent, or an approved follow-up. On repeat runs show late or corrected events.`,
    specialistProbe: 'For the selected route, prove its source join on one authorized case. First value: join the register to exact account/tenant events. Feature adoption: read release/flag, eligibility, exposure and use rows; recompute distinct-account counts and coverage. Trial: read one exact trial/subscription state and product account/tenant event window, exclude internal/test/duplicate events, verify a rule-matching event or complete non-observation versus unknown coverage, then ask the owner to review the bounded observation. Never test all routes by completing only one route’s setup.',
    workedExample: `Fictional input: validated register register-example-001 defines first value as an authorized customer user exporting a project report from production before 2026-09-30 17:00 UTC. Product export product-event contains event event-example-001, “project_report_exported”, for account-example-001 / tenant-example-001 at 2026-09-25 11:00. Coverage record coverage confirms complete event collection for the 2026-09-24 through 2026-09-25 11:10 window; no test actor or duplicate is present.

Reviewable output: readout readout-example-001 links register-example-001 and rule first-report-v1. Status: **reached** at 11:00, supported by event-example-001 and coverage. The workspace milestone was already observed; the report-export milestone can now be rechecked and marked complete by the onboarding owner. Seat invites, login count, broader adoption and renewal impact remain **unknown**. Next: account owner verifies the sponsor can use the exported report.

Separate fictional feature input: release R-7 / flag f7-r2 exposed 80 of 120 eligible accounts in the named segment during a complete week; 24 exposed accounts used the feature under rule U-1. Output: 80/120 exposed (66.67%) and 24/80 used (30%); target 40% is below on this one baseline window. Cause and future retention impact are unknown.

Separate fictional trial input: trial T-42 for account A-42 / tenant N-42 is trialing under subscription S-42@r3 until 2026-10-02. Authorized export E-42 covers 2026-09-24 through 2026-09-26 10:00 UTC and contains one non-test qualifying project-created event EV-42 at 09:00. Output: trial-usage-observation U-42 records **observed use** of the owner-defined rule with exact source and coverage; purchase intent, recipient identity and contact permission remain unknown.`,
    inadequateExample: '“All 20 invited seats adopted the product because one report was exported.” Reject: one valid event proves only the agreed account predicate. Also reject “the release hurt retention” from one feature-use window and “this trial is ready to buy” from one login: neither claim follows from the product evidence.',
    setupScope: 'Select first value, released feature or trial usage, then bind only that route’s exact account/cohort, rule version, window, owner and authorized sources. A trial needs trial/subscription and account/tenant IDs but does not require CRM contact data.',
    setupAccessScope: 'Record the selected route’s exact source revision, identity join, observation cutoff, event coverage and missing access. For a trial, read current trial status and one product event or complete-window absence.',
    automationRoute: 'New Customer to First Value, Released Feature to Adoption Decision, or Trial Account to Reviewed Sales Assist',
    setupCase: 'Reproduce one authorized case for the selected route and record the owner’s decision; a first-value case does not complete trial setup.',
  },
  {
    id: 'lifecycle-analyst', name: 'Lifecycle Analyst', icon: '🔁', subcategory: 'Retention',
    role: 'SaaS activation and cohort retention analyst',
    purpose: 'Compare mature signup cohorts under one activation and retention policy, exposing denominators, censoring and coverage before an owner decision.',
    firstResult: 'A source-linked cohort observation with fixed activation and day-30 retention rules, eligible and mature counts, rates, coverage, and a bounded owner question.',
    minimumInput: 'Product and tenant, signup cohort windows, activation and retention definitions, maturity cutoff, event and subscription sources, identity rule, timezone, and review owner.',
    optionalConnections: 'Product analytics, warehouse exports and subscription billing through scoped MCPs or files; one mature cohort supports a baseline-first readout.',
    exampleRequests: ['Compare day-30 retention for these two signup cohorts without counting immature accounts as churn.', 'Give me the first activation and retention baseline for our new SaaS cohort.'],
    method: [
      'Freeze exact product, tenant, signup cohort windows, eligible-account definition, activation event, day-30 retention predicate, identity join and timezone.',
      'Read authorized product and subscription records; exclude internal/test accounts, duplicates and wrong-tenant events while recording source freshness and missing joins.',
      'Separate mature eligible accounts from still-censored accounts; never count a cohort whose day-30 outcome window has not closed as churn.',
      'Compute activation per eligible and retention per mature eligible with explicit numerators, denominators, policy version and comparable cohort lengths.',
      'Report observed differences and alternative explanations as hypotheses, then ask the owner which instrumentation or experience question merits a bounded test.',
    ],
    evidence: 'Show cohort IDs and windows, cutoff, rule versions, distinct source references, account-level join coverage, excluded immature counts and rate arithmetic.',
    boundary: 'Do not declare churn for immature cohorts, infer a feature caused retention, assign an individual account a risk label from cohort averages, contact customers, or change product experience from installation.',
    handoff: 'Activation and Retention Intelligence emits cohort-retention-observation/v1 to Growth Experiment Planner only after exact cohort maturity, identity, source and rate checks. A cohort difference is an observed signal, not a causal result or approved test.',
    deeperMethod: `## Cohort maturity and repeat rule

Keep activation and retention predicates versioned. Compare equal-duration signup cohorts at the same day-30 maturity rule; wait for late billing or event records. If a cohort is immature, return pending_maturity with null retention and no trend. If only one mature cohort exists, return baseline_first. On repeats, preserve prior cohort IDs and reopen a rate only for a sourced correction; changed event semantics start a new baseline.`,
    specialistProbe: 'Join one authorized signup cohort to product activation events and active subscription records by exact product, tenant and account IDs. Reproduce eligible, matured, activated and retained counts with day-30 cutoff. Check excluded test accounts, late records and source coverage separately; show an immature cohort as pending rather than churn and have the owner review the result.',
    workedExample: `Fictional input: ArborDesk UK self-serve cohorts under lifecycle policy v2. June cohort has 200 eligible accounts and July cohort has 220, both mature at the 2026-09-02 cutoff. Product event and subscription exports are authorized, with about 98% identity join coverage. Day-7 activation means first scheduling workflow; day-30 retention means active paid subscription plus qualifying use in days 23–30.

Reviewable output: June activated 140/200=70%, retained 120/200=60%; July activated 140/220≈63.64%, retained 110/220=50%. Difference: minus 10 percentage points in day-30 retention, with about 2% unmatched accounts in each cohort. Cause unknown. Next: check whether activation event semantics or customer mix changed before proposing a guided setup experiment. No account is labeled likely to churn.`,
    inadequateExample: '“August retention is zero because its 30-day window has not finished; the new setup caused churn.” Reject: immature accounts are censored, and the source records cannot establish causality.',
    setupScope: 'Confirm product, tenant, signup cohort policy, activation and retention rule versions, maturity cutoff, owner, timeframe and authorized event/billing scope.',
    automationRoute: 'Activation and Retention Intelligence',
    setupCase: 'Reproduce one real authorized cohort case and record the owner\'s decision.',
    setupSource: 'Select live cohort sources only after the owner authorizes product, tenant, identity and time scope and a representative read succeeds.',
    setupAccessScope: 'Record exact product, tenant, signup window, source revision, observation cutoff and missing access.',
  },
  {
    id: 'customer-health-coordinator', name: 'Customer Health Coordinator', icon: '💚', subcategory: 'Account health',
    role: 'Customer success account health coordinator',
    purpose: 'Combine first-value progress, support history, and renewal timing into a sourced account review and owner action.',
    firstResult: 'A current customer health brief with evidence, unknowns, risks, and an owner-reviewed next action.',
    minimumInput: 'Customer/account reference, first-value or current usage observation, account owner, health rules, authorized support scope, and renewal date if relevant.',
    optionalConnections: 'CRM or customer success platform, support inbox, product usage, and billing or renewal source; bounded exports are sufficient for a first review.',
    exampleRequests: ['Review this account’s onboarding and support signals and tell me what needs a human follow-up.', 'Prepare a current account health brief without guessing a churn score.'],
    method: [
      'Confirm account identity, owner, lifecycle stage, review window, and the customer’s actual health and escalation definitions.',
      'Read validated first-value progress or a current usage observation plus authorized support, relationship, and renewal records; preserve each source and freshness.',
      'Separate active blockers, recent resolved issues, usage gaps, and renewal timing. Label unsupported risk claims as hypotheses.',
      'Choose the next useful owner action, escalation or watch condition; include a due date and the evidence needed to close it.',
      'Return a concise brief and request owner review before customer outreach, CRM updates, or renewal commitments.',
    ],
    evidence: 'Each risk or positive signal cites a record and observation time; missing support or renewal access is an explicit limitation.',
    boundary: 'Do not present a model guess as a verified health score, promise a renewal outcome, change terms, or contact the customer without an approved route.',
    handoff: 'For New Customer to First Value, emit `customer-health-brief/v1` linked to the validated first-value readout and onboarding register. For Renewal Risk to Owned Decision, emit bounded `renewal-health-brief/v1` with exact account, observation cutoff, observed signals, hypotheses and source coverage; the Workflow validates it before Renewal reads contract terms. Customer-facing actions remain pending review.',
    deeperMethod: `## Health decisions

Use the account owner's rules for severity and renewal timing. An unopened ticket, low login count, or unobserved event is not automatically churn risk. Separate data quality from customer risk. On repeat reviews, report meaningful changes and whether previous owner actions were completed. For a renewal route, preserve the source window and signal IDs; do not infer contract terms from health records.`,
    specialistProbe: 'Bind the current first-value readout and support or renewal records to one exact account and tenant. Inspect signal dates and the owner health rule. Distinguish observed positive/negative state, hypothesis and missing source coverage; obtain an owner decision before any CRM risk label or customer contact.',
    workedExample: `Fictional input: readout readout-example-001 shows the agreed report export reached for account-example-001 / tenant-example-001 at 11:00 on 2026-09-25. Support ticket ticket-17 at 11:20 asks for training; its impact on adoption is not yet known. The account owner's rule H-1 places an account on watch when first value is observed but a potentially blocking question is open for review. The renewal system is unavailable in this review.

Reviewable output: health brief health-example-001 for the exact account, status **watch** under the owner's review rule. Positive observed signal: first value reached, linked to readout-example-001. Open question: ticket ticket-17 may slow broader adoption, labeled **hypothesis** until the sponsor or owner confirms impact. Renewal timing and sentiment: **unknown** due to missing sources. Next action: Customer Success owner confirms the sponsor can use the report and reviews ticket-17 by the next business day. No outreach or CRM update has been sent.

For the separate renewal route, fictional bounded health brief health-acme-42 observes first value from product:report-export-7 and an open support:ticket-17 in the same tenant/account window. The training case's effect remains a hypothesis; contract terms, notice and billing are left to Renewal Coordinator. An inadequate renewal output would claim “this account will churn” from the case alone.`,
    inadequateExample: '“The customer will churn because there is one training ticket; set health to red and email the sponsor.” Reject: the ticket is not proof of churn, renewal and relationship evidence are missing, and outreach lacks review.',
  },
  {
    id: 'renewal-coordinator', name: 'Renewal Coordinator', icon: '📅', subcategory: 'Renewals',
    role: 'Evidence-led SaaS renewal decision coordinator',
    purpose: 'Verify one customer contract, notice deadline, billing state and health evidence, then prepare an owned renewal decision without changing terms or contacting the customer.',
    firstResult: 'An exact-contract renewal register with current terms, notice timing, source coverage, owner options, decision state and next verification.',
    minimumInput: 'Account and tenant, authorized contract or subscription, renewal and notice policy, current billing source, bounded health brief and accountable renewal owner.',
    optionalConnections: 'CRM and signed agreement, subscription billing, Customer Success platform and support/usage exports through scoped MCPs or files; read-only review works from current exports.',
    exampleRequests: ['Which renewals need an owner decision before their notice deadline? Show exact contract evidence.', 'Review this account for renewal readiness using current terms and health signals; do not contact the customer.'],
    method: [
      'Bind the exact account, tenant, contract, subscription, entity, owner and review cutoff. Re-read executed terms and revisions; resolve auto-renewal, notice deadline and effective renewal date.',
      'Read a validated bounded health brief and current billing state. Separate observed first-value, usage, support and payment facts from relationship hypotheses or missing coverage.',
      'Calculate days to notice under the agreed timezone. Flag a passed deadline without inventing whether notice was sent or whether the contract renewed.',
      'Prepare investigate, customer-discussion review, terms review or no-action options with a stable case key and due rule. A recommendation is not an accepted owner decision.',
      'Present contract facts and uncertainty to the renewal owner. Any customer message, CRM change or term amendment needs a separate approved exact-object route and provider receipt.',
    ],
    evidence: 'Cite the exact executed agreement and revision, subscription and billing observation, notice rule, health artifact and current source timestamps. Keep contract facts apart from health hypotheses.',
    boundary: 'Do not infer churn probability, promise a renewal, change contract terms, issue credits, mark an invoice paid, send notice or contact the customer from installation or a read-only review.',
    handoff: 'Renewal Risk to Owned Decision consumes validated renewal-health-brief/v1 and emits renewal-decision-register/v1 for the same tenant/account, with a current contract and subscription read, notice calculation and owner state. The route validates both artifacts before reporting.',
    deeperMethod: `## Renewal decision and repeat rule

Retain contract revision, renewal case key and prior owner decisions. Re-read terms, notice receipts, invoice state and health evidence before each review. A changed contract or auto-renewal term requires a new decision; a health signal cannot override executed terms. Keep any outreach or amendment in a separate approved route.`,
    specialistProbe: 'Read one executed contract, one current subscription or billing record and one validated health brief for the exact tenant/account. Recompute notice days from the specified timezone, check contract revision and auto-renewal rule, show unknown or late evidence explicitly, then have the renewal owner review the proposed decision.',
    workedExample: `Fictional input: tenant-demo/account-acme has executed contract C-42 revision r3, renewal 2026-12-01 and a 60-day notice deadline of 2026-10-02 in UTC. A current billing export on 2026-09-26 shows one open September invoice. Validated health brief H-42 says first value reached, with one unresolved training case; its renewal effect is unknown. No notice receipt exists.

Reviewable output: register R-42 binds C-42/r3 and the same account, calculates six days to notice at the 2026-09-26 cutoff, labels the invoice open rather than unpaid cash loss, and proposes owner review of the training case, billing status and contract options by 2026-09-28. Decision=pending; notice_sent=false; amendment=none; customer contact=none. It does not predict churn or claim the contract will renew.`,
    inadequateExample: '“This customer will churn because an invoice is open. Send a discount and cancel auto-renewal now.” Reject: an open invoice and training case do not prove intent, and neither a discount nor a contract change was reviewed or receipted.',
    setupScope: 'Confirm tenant, account, legal entity, exact contract and subscription, executed revision, timezone, renewal/notice terms, owner and health evidence scope.',
    automationRoute: 'Renewal Risk to Owned Decision',
    setupCase: 'Reproduce one real renewal case plus a changed-contract or missed-notice control and record the owner decision.',
    setupSource: 'Connect live contract and billing accounts only after scoped read access and one current exact-object probe succeed.',
    setupAccessScope: 'Record exact contract revision, subscription ID, notice policy, observation cutoff, health artifact ID and source gaps.',
  },
]

function checklist(spec: Specialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm the Customer Success role', instructions: `Confirm whether ${spec.name} is this Crew's primary role or a supporting capability. Preserve an existing Crew identity and record the named owner.` },
    { id: 'skill', title: 'Verify the selected skill', instructions: `Confirm skills/${spec.id}/SKILL.md exists and ${spec.id} is selected for this Crew.` },
    { id: 'scope', title: 'Set account or cohort scope', instructions: `${spec.setupScope ?? 'Confirm the customer/account, purchased scope, intended first result, owner, timeframe, and authorized source scope.'} Minimum input: ${spec.minimumInput}` },
    { id: 'access', title: 'Test source access', instructions: `Read one representative authorized source or export. ${spec.optionalConnections} ${spec.setupAccessScope ?? 'Record exact account, date coverage, and missing access.'} A named provider is not a connected account.` },
    { id: 'definitions', title: 'Agree on evidence and action rules', instructions: spec.specialistProbe },
    { id: 'first_result', title: 'Produce the first result', instructions: `Produce ${spec.firstResult} ${spec.evidence} Use actual authorized data; a fictional example alone does not complete this check.` },
    { id: 'review', title: 'Review the first result', instructions: 'Show a representative result to the owner and record corrections, decision, and next action before completing setup.' },
    { id: 'delivery', title: 'Decide on delivery', optional: true, instructions: 'Choose chat-only or a separately authorized customer/CRM delivery route. Chat-only completes this decision. A draft, reminder, or health brief is not permission to message a customer or mutate an account.' },
    { id: 'recurrence', title: 'Decide on recurrence', optional: true, instructions: 'Choose manual-only or a separately reviewed schedule, authenticated trigger, callable function, or Automation. Manual-only completes this decision; test any configured route before activation.' },
  ]
  return `${JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: spec.version ?? 1, checks, completed_steps: [] }, null, 2)}\n`
}

function skill(spec: Specialist): string {
  return `---
name: ${spec.id}
description: ${spec.purpose}
---

# ${spec.name}

This skill provides the ${spec.name} capability. It can seed one Crew or be added to an existing Crew without changing that Crew's primary identity.

## Setup through chat

Read \`templates/${spec.id}/TEMPLATE_SETUP.json\` and \`templates/${spec.id}/SETUP.md\`. Verify each check before adding its ID to \`completed_steps\`. Preserve the checklist and previous progress. A chat-only or manual-only choice completes the corresponding optional check. Report verified, blocked, and next.

## First useful result

${spec.method.map((step, index) => `${index + 1}. ${step}`).join('\n')}

Deliver **${spec.firstResult}** ${spec.evidence}

${spec.deeperMethod}

## Fictional worked example

${spec.workedExample}

## Inadequate output to reject

${spec.inadequateExample}

This example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

${spec.handoff} The Crew step must supply schema fields and an output path; ask Builder to repair a route that omits them. A validator checks shape and references; a human checks source truth.

## Boundaries

${spec.boundary} Never copy another Crew's credentials. Installing this skill enables no schedule, trigger, function, Automation, customer message, CRM write, or account action.
`
}

function guide(spec: Specialist): string {
  return `# ${spec.name} setup

Template \`${spec.id}\` version ${spec.version ?? 1}. Progress lives in \`templates/${spec.id}/TEMPLATE_SETUP.json\` and is verified in Crew chat.

## First result

Provide ${spec.minimumInput} Ask: “${spec.exampleRequests[0]}”

Expected output: **${spec.firstResult}** ${spec.evidence}

## Fictional example and failure

${spec.workedExample}

Reject: ${spec.inadequateExample}

The example does not prove source access. ${spec.setupCase ?? "Reproduce one real account case and record the owner's decision."}

## Source and connection choice

${spec.optionalConnections} A file or export supports a first read-only result. ${spec.setupSource ?? 'Select a live account only after the owner authorizes scope and a representative read succeeds.'}

## Optional recurring work

A schedule can repeat this Crew's own review. A separately proposed ${spec.automationRoute ?? 'New Customer to First Value'} Automation coordinates distinct Crews; Builder must test handoffs manually before recurrence. ${spec.boundary}
`
}

export const customerSuccessSpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = `templates/${spec.id}`
  const skillPath = `skills/${spec.id}/SKILL.md`
  const setupGuidePath = `${base}/SETUP.md`
  const setupPath = `${base}/TEMPLATE_SETUP.json`
  return {
    id: spec.id, version: spec.version ?? 1, category: 'Customer Success', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
