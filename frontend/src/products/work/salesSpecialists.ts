import type { CrewTemplate } from './crewTemplates'

export type SalesSpecialistId = 'lead-intake-qualifier' | 'account-researcher' | 'sales-followup-coordinator'

type SalesSpecialist = {
  id: SalesSpecialistId
  version: number
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
}

const specialists: readonly SalesSpecialist[] = [
  {
    id: 'lead-intake-qualifier', version: 1, name: 'Lead Intake & Qualifier', icon: '📥', subcategory: 'Inbound leads',
    role: 'Inbound lead intake and qualification coordinator',
    purpose: 'Turn an authorized inbound enquiry into a deduplicated, evidence-linked qualification brief and owner decision.',
    firstResult: 'A lead qualification brief with fit reasons, source references, missing details, and a proposed owner.',
    minimumInput: 'A real inbound enquiry or export, ideal-customer criteria, territory or routing rules, and a lead owner.',
    optionalConnections: 'HubSpot, Salesforce, another approved CRM, form inbox, or a supplied export. A file is enough for the first review.',
    exampleRequests: ['Review these inbound enquiries against our ideal-customer criteria and show which need a human decision.', 'Qualify this website demo request, check duplicates, and prepare a source-linked handoff without contacting the person.'],
    method: [
      'Confirm the company, offer, target market, qualification criteria, lead source, owner, and current reporting window.',
      'Read only authorized form, inbox, or CRM records. Preserve source and observed time; check for duplicate contacts, companies, and open opportunities before creating a new case.',
      'Separate prospect-provided facts, verified account facts, and inferences. Assess fit criterion by criterion and mark unknown information explicitly.',
      'Record inbound intent, urgency only when evidenced, current stage, contact policy status, and the exact owner decision needed.',
      'Produce one stable brief per lead with a qualified, needs-review, or not-fit recommendation and a reason. Ask the owner to review a representative result before marking setup complete.',
    ],
    evidence: 'Each brief cites the original enquiry or export row, qualification policy, checked duplicate scope, and the source of every fit claim.',
    boundary: 'Do not infer consent to email from a form submission, scrape private contacts, create a CRM record, or contact a lead without an authorized route and policy.',
    handoff: 'For an approved Inbound Lead-to-Meeting Review route, emit plain JSON `lead-qualification-brief/v1` at the path supplied by the Crew step. Include source IDs, fit evidence, duplicate state, contact policy state, and a stable brief ID. The Workflow validates it before Follow-up consumes it.',
    deeperMethod: `## Qualification decisions

Use the owner's actual ideal-customer profile and disqualification rules. A missing company size, budget, timing, or authority signal is **unknown**, not a negative score. Do not invent an intent score or convert a content download into a demo request. A duplicate CRM record should be linked for owner review, not silently merged or overwritten. For a not-fit or contact-blocked lead, return an internal disposition and stop the outbound handoff.

On repeat intake, use the lead source ID and stable brief ID to check whether the record, owner, or stage changed. Report the delta and avoid opening a second case for the same enquiry.`,
    specialistProbe: 'For one inbound enquiry, resolve the exact source row, company/domain, lead and contact references, approved fit-policy version and owner. Check duplicates across the authorized CRM scope and current contact/suppression state. Reproduce one criterion from source evidence; keep missing data unknown and have the owner approve the qualification and route before any CRM write or contact.',
    workedExample: `Fictional input: enquiry lead-example-001 in exports/fictional-inbound-leads.csv row 7 asks for a demo of multi-location appointment scheduling. Policy docs/fictional-ideal-customer-profile.md section 1 includes clinics in the supported market. The authorized open-lead export query reports no duplicate as of 09:40 UTC. The enquiry gives no budget and contact policy still needs review.

First result: brief-example-001 identifies the source row and policy, marks market **match** and budget **unknown**, records duplicate_state **none** within the checked export, and recommends **qualified** for owner review. The next action is to review a reply draft and contact policy. No CRM record or message is created. On a later run, recheck the source ID, duplicate scope and policy before changing the brief.`,
    inadequateExample: '“This clinic is a high-budget hot lead, so add it to the CRM and start a sequence.” Reject: the budget is unknown, the intent score is invented, contact policy is unresolved, and neither the CRM write nor outbound sequence is authorized.',
  },
  {
    id: 'account-researcher', version: 1, name: 'Account Researcher', icon: '🔎', subcategory: 'Account intelligence',
    role: 'Evidence-led B2B account researcher',
    purpose: 'Prepare a short, sourced account brief that helps a seller understand an approved prospect without fabricating buyer intent.',
    firstResult: 'A dated account research brief with verified facts, relevant hypotheses, and questions for the seller.',
    minimumInput: 'Company name or domain, approved research scope, offer, and either a lead brief or seller question.',
    optionalConnections: 'Public website and approved research sources; CRM notes or firmographic exports only when authorized.',
    exampleRequests: ['Research this inbound company and tell me what is verified versus inferred.', 'Prepare a short account brief for tomorrow’s sales call, with exact source links and open questions.'],
    method: [
      'Resolve the legal or trading company and canonical domain; flag ambiguous matches rather than blending companies.',
      'Inspect the company site and approved public or customer-provided sources within a bounded date range.',
      'Capture product, audience, geography, recent changes, and possible relevance to the seller’s offer with direct source references and observation times.',
      'Label each claim observed, owner-provided, or hypothesis. Explain why a finding matters and what the seller should verify in conversation.',
      'Produce a concise brief with no invented budget, technology stack, buying committee, or purchase intent.',
    ],
    evidence: 'Every factual finding has a stable ID, source URL or export reference, observation time, and evidence kind.',
    boundary: 'Do not scrape personal contact details, claim a company is buying from a weak signal, or send outreach. Public research is context, not permission to contact.',
    handoff: 'If included in an Inbound Lead-to-Meeting Review, return plain JSON `account-research-brief/v1` linked to the validated qualification brief. The Workflow validates company identity and source references before Follow-up uses it.',
    deeperMethod: `## Research quality

Prefer the account's own current site and the customer's approved CRM over unsourced directory summaries. Check a company-domain match before attaching a finding to a lead. Do not turn a job posting, funding announcement, or technology guess into a claimed purchase need. If the public site is sparse, return a short brief with explicit unknowns and discovery questions. Recheck time-sensitive facts before a later run.`,
    specialistProbe: 'For one approved account, match company name and canonical domain to the validated lead brief before research. Open a current company-controlled page and record its URL and observation time. Separate one observed fact from one hypothesis, trace each reference, and ask the seller to accept or correct the discovery question; do not attach a different company with a similar name.',
    workedExample: `Fictional input: validated brief-example-001 identifies harborclinic.example. Its fictional /locations page, observed at 09:50 UTC, lists three clinic locations. The original demo enquiry asks about multi-location scheduling; it does not describe the current scheduling system or budget.

First result: research-example-001 links to brief-example-001 and the matching domain. “The site lists three locations” is **observed** with the page reference. “Coordinating those locations may matter” is a **hypothesis**, supported by the page and enquiry but explicitly unconfirmed. The seller's question is “How are appointments coordinated across the locations today?” Refresh the page before using this finding on a later date.`,
    inadequateExample: '“Harbor Clinic has three locations, uses a legacy scheduler, and is ready to buy our platform.” Reject: the scheduler and buying intent are unsupported, and a public page does not establish permission to contact anyone.',
  },
  {
    id: 'sales-followup-coordinator', version: 3, name: 'Sales Follow-up Coordinator', icon: '✉️', subcategory: 'Follow-up',
    role: 'Human-reviewed B2B lead follow-up coordinator',
    purpose: 'Prepare reviewed follow-up or a no-contact decision for qualified inbound leads and trial accounts, then track only provider-backed outcomes.',
    firstResult: 'A reviewable unsent follow-up draft or sourced no-contact decision with exact recipient, owner, evidence and next check.',
    minimumInput: 'Choose a validated inbound lead brief or trial-usage observation, plus current CRM identity, approved offer, contact/suppression policy, owner and prior-contact history.',
    optionalConnections: 'Approved CRM, form or trial-account export, current contact history, verified booking URL, sender and meeting source. A draft needs no live send connection.',
    exampleRequests: ['Draft a booking-link reply to this qualified demo request for my review. Do not send it.', 'Review this trial account for a permitted seller assist; return no-contact if consent or identity is unclear.'],
    method: [
      'Confirm lead owner, current stage, contact channel and policy, allowed claims, message voice, owner-specific booking URL, and what outcome counts as a useful meeting.',
      'Read the selected validated lead brief or trial observation. For a trial, join product account to current CRM account and exact recipient through a source record; never derive a person from a tenant name. Check prior messages, replies, opt-outs, and booked meetings.',
      'Draft one short response tied to the prospect’s actual enquiry or an owner-approved trial assistance policy. Cite supported facts and mark uncertain personalization for review. If contact is blocked or unclear, return an internal no-contact decision.',
      'Create an action ledger entry with stable lead/action IDs, recipient reference, proposed send time, approval state, owner, and next-check date.',
      'Ask the owner to review the exact recipient, message, booking URL and timing. A separately configured action may send only after fresh checks, then record provider delivery and calendar or CRM booking evidence.',
    ],
    evidence: 'The draft or no-contact decision links to the selected lead or trial artifact, current CRM identity, prior-contact and permission checks, approved offer material, and any research it uses.',
    boundary: 'Do not email, message, enroll a sequence, update CRM, schedule a meeting, or mark a draft as sent without a separately approved action and verified current state.',
    handoff: 'For Inbound Lead-to-Meeting Review, emit `sales-followup-draft/v1` linked to the qualification brief. For Trial Account to Reviewed Sales Assist, consume the validated `trial-usage-observation/v1`, recheck current trial/CRM/contact state, and emit `trial-sales-assist/v1` with an unsent draft or no-contact decision. Keep `send_state` as `not_sent`; a separate approved action needs a provider receipt, and a later booked or paid outcome needs its own source event. Never infer those outcomes from a draft.',
    deeperMethod: `## Contact and repeat-run rules

An inbound request or trial signup is not blanket consent for every channel or cadence. Follow the customer's policy and current suppression state. Stop a draft when fit is unreviewed, contact is blocked, a relevant reply arrived, a meeting is booked, or the trial has converted. For a trial, keep product use separate from buying intent and require an explicit product-account-to-CRM-account mapping plus an exact permitted recipient. If CRM or inbox access is absent, return needs-review rather than a contact-ready draft. For an approved send, re-read the decision and message fingerprint, suppression, reply and meeting state; use a stable action ID and provider receipt. Report booked only from a matched calendar or CRM event.`,
    specialistProbe: 'For the selected route, read one authorized qualified lead or exact trial-usage artifact. Join its account to current CRM and recipient records where relevant, verify seller owner, fit decision, current consent/channel and suppression, prior messages/replies, meeting and converted state, approved offer claim and booking URL. Produce a no-contact or draft decision with exact source IDs; have the owner review recipient and wording. Before any separately approved send, recheck the message fingerprint and state; require a provider receipt and matched meeting event for later claims.',
    workedExample: `Fictional input: brief-example-001 qualifies lead-example-001, research-example-001 supplies a three-location observation, and the approved offer document supports a scheduling demo. The recipient is crm-export:contact-001. Contact policy remains review_required; no verified send approval or provider receipt is supplied.

First result: draft-example-001 has subject “Your scheduling demo request” and a short reply about the prospect's stated multi-location question. It cites the enquiry and approved offer, names the Sales owner, sets a next-check date, and records send_state **not_sent** with approval_required **true**. The owner reviews recipient, wording, booking URL, timing and current suppression state. A draft is neither delivered nor booked.

Separate fictional trial input: validated observation U-42 belongs to product account A-42. CRM mapping M-42 links it to CRM account C-42, but contact permission is unknown despite one observed project-created event. First result: trial-sales-assist D-42 says **needs_review**, recipient and draft are null, and Sales asks the owner to resolve permission and fit. Product use is not contact consent or purchase intent.`,
    inadequateExample: '“I sent the follow-up and booked a meeting because the lead asked for a demo.” Reject: no approved send, provider receipt or matched meeting exists. Also reject “email this trial user because they created a project”: use neither identifies an approved recipient nor proves contact permission or buying intent.',
  },
]

function checklist(spec: SalesSpecialist): string {
  const checks = [
    { id: 'identity', title: 'Confirm the sales role', instructions: `Confirm whether ${spec.name} is the primary Crew role or a supporting capability. Preserve an existing Crew identity and record the named business owner.` },
    { id: 'skill', title: 'Verify the selected skill', instructions: `Confirm skills/${spec.id}/SKILL.md exists and ${spec.id} is selected for this Crew.` },
    { id: 'scope', title: spec.id === 'sales-followup-coordinator' ? 'Select inbound lead or trial route' : 'Set business and lead scope', instructions: `Confirm the offer, target buyer, market, reporting window, and authorized source scope for this role. Minimum input: ${spec.minimumInput}` },
    { id: 'access', title: 'Test source access', instructions: `Read a representative authorized source or export. ${spec.optionalConnections} Record the actual account or file, date coverage, and missing access; a named provider is not a connected account.` },
    { id: 'policy', title: 'Verify source and decision rules', instructions: spec.specialistProbe },
    { id: 'first_result', title: 'Produce the first result', instructions: `Produce ${spec.firstResult} ${spec.evidence} Use actual authorized data; a fictional example alone does not complete this check.` },
    { id: 'review', title: 'Review the first result', instructions: 'Show a representative output to the owner and record corrections, decision, and next action before completing setup.' },
    { id: 'delivery', title: 'Decide on delivery', optional: true, instructions: spec.id === 'sales-followup-coordinator' ? 'Choose chat-only or a separately approved sending and booking-tracking route. Verify the actual sending account/grants, owner-specific booking URL, meeting source, review gate and provider receipt before enabling delivery. Chat-only is a completed decision; this template itself enables no outbound action.' : 'Choose chat-only or a separately approved CRM/email/calendar route. Chat-only is a completed decision. No outbound action or CRM mutation is enabled by this template.' },
    { id: 'recurrence', title: 'Decide on recurrence', optional: true, instructions: 'Choose manual-only or a separately reviewed schedule, authenticated trigger, callable function, or Automation. Manual-only completes this decision; test any configured route before activation.' },
  ]
  return `${JSON.stringify({ schema_version: 1, template_id: spec.id, template_version: spec.version, checks, completed_steps: [] }, null, 2)}\n`
}

function skill(spec: SalesSpecialist): string {
  return `---
name: ${spec.id}
description: ${spec.purpose}
---

# ${spec.name}

This skill provides the ${spec.name} capability. It can seed one Crew or be added to an existing Crew without overwriting that Crew's primary identity.

## Setup through chat

Read \`templates/${spec.id}/TEMPLATE_SETUP.json\` and \`templates/${spec.id}/SETUP.md\`. Verify each check before adding its ID to \`completed_steps\`. Preserve the checklist and previous progress. An explicit chat-only or manual-only decision completes the corresponding optional check. Report what is verified, blocked, and next.

## First useful result

${spec.method.map((step, index) => `${index + 1}. ${step}`).join('\n')}

Deliver **${spec.firstResult}** ${spec.evidence}

${spec.deeperMethod}

## Fictional worked example

${spec.workedExample}

## Inadequate output to reject

${spec.inadequateExample}

The fictional example does not complete setup. Reproduce one case from authorized customer records and save the owner's review.

## Automation handoff

${spec.handoff} The Crew step must supply the schema fields and output path; ask Builder to repair a route that omits them. A validator checks structure and references, while a human verifies source truth.

## Boundaries

${spec.boundary} Never copy another Crew's connection or credentials. Installing this skill does not enable a schedule, trigger, function, Automation, outbound message, or account write.
`
}

function guide(spec: SalesSpecialist): string {
  return `# ${spec.name} setup

Template \`${spec.id}\` version ${spec.version}. Progress lives in \`templates/${spec.id}/TEMPLATE_SETUP.json\` and is verified through Crew chat.

## First result

Provide ${spec.minimumInput} Ask: “${spec.exampleRequests[0]}”

Expected output: **${spec.firstResult}** ${spec.evidence}

## Fictional example and failure

${spec.workedExample}

Reject: ${spec.inadequateExample}

The example does not prove source access. Reproduce one real lead or account case and record the owner's decision.

## Source and connection choice

${spec.optionalConnections} A file or export is sufficient for a first read-only result. Select any live account for this Crew only after the owner authorizes its scope and a representative read succeeds. Do not imply that a named CRM or inbox is already connected.

## Optional recurring work

A schedule can repeat this Crew's own review with a timezone, source-freshness rule, and owner. An authenticated form or CRM event may trigger intake only after signature, event ID, account scope, and duplicate-delivery checks. A separately proposed ${spec.id === 'sales-followup-coordinator' ? 'Inbound Lead-to-Meeting Review or Trial Account to Reviewed Sales Assist' : 'Inbound Lead-to-Meeting Review'} Automation coordinates distinct Crews; Builder must test its handoffs manually before recurrence. ${spec.boundary}
`
}

export const salesSpecialists: readonly CrewTemplate[] = specialists.map(spec => {
  const base = `templates/${spec.id}`
  const skillPath = `skills/${spec.id}/SKILL.md`
  const setupGuidePath = `${base}/SETUP.md`
  const setupPath = `${base}/TEMPLATE_SETUP.json`
  return {
    id: spec.id, version: spec.version, category: 'Sales', subcategory: spec.subcategory, name: spec.name, icon: spec.icon,
    role: spec.role, purpose: spec.purpose, firstResult: spec.firstResult,
    minimumInput: spec.minimumInput, optionalConnections: spec.optionalConnections,
    exampleRequests: spec.exampleRequests, selectedSkills: [spec.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(spec), [setupGuidePath]: guide(spec), [setupPath]: checklist(spec) },
  }
})
