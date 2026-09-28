import type { CrewTemplate } from './crewTemplates'

export type BillingPackId = 'invoice-chasing' | 'failed-payment-recovery' | 'refund-review' | 'dispute-review'

type Pack = {
  id: BillingPackId
  name: string
  icon: string
  role: string
  purpose: string
  firstResult: string
  minimumInput: string
  optionalConnections: string
  exampleRequests: readonly [string, string]
  method: readonly string[]
  evidence: string
  boundary: string
  repeatRule: string
  exampleInput: string
  workedExample: string
  inadequateExample: string
  inadequateReason: string
  providerNote: string
  automationOutput?: string
}

const packs: readonly Pack[] = [
  {
    id: 'invoice-chasing', name: 'Invoice Chasing', icon: '📨',
    role: 'Overdue invoice review and follow-up capability for a Billing Operations Crew',
    purpose: 'Find genuinely overdue customer invoices, suppress duplicate contact, and prepare a policy-safe follow-up for owner review.',
    firstResult: 'A ranked overdue-invoice queue with invoice/customer IDs, open amount, due and last-payment dates, prior or scheduled contact, owner, and unsent next message.',
    minimumInput: 'Authorized invoice and payment records, business and account ID, reporting cutoff, invoice terms, contact policy, suppression rules, and collections owner.',
    optionalConnections: 'Billing provider such as Stripe or Paddle, CRM and support inbox via scoped MCPs or exports; a read-only export can support the first queue.',
    exampleRequests: ['Which invoices actually need a follow-up today?', 'Draft the next reminder for this overdue invoice without sending it.'],
    method: [
      'Bind exact business, provider account and mode, invoice, customer, currency, due-date policy, cutoff, owner and contact channel.',
      'Re-read current invoice status, amount remaining, credits, partial payments, collection method, payment attempts and contact history.',
      'Exclude draft, paid, void, disputed or owner-suppressed cases; identify an active automatic reminder or retry before suggesting another message.',
      'Rank actionable open balances by due date and policy priority; retain a stable case and contact action key.',
      'Prepare a factual unsent message with exact recipient, amount, invoice link if approved, and owner decision; delivery is separate.',
    ],
    evidence: 'A follow-up row needs current invoice/payment source IDs, observed time, amount remaining in minor units and currency, prior-contact source, policy rule, and reason it is due now.',
    boundary: 'Do not send a reminder, change terms, mark an invoice paid or uncollectible, or initiate a charge through installation. A send needs exact-recipient approval, current state recheck and provider receipt.',
    repeatRule: 'Re-read current payment and prior-contact state, deduplicate by invoice and contact action ID, respect quiet periods and provider dunning, and close or defer resolved cases.',
    exampleInput: 'Fictional input: invoice in_42 open, USD 500.00 due 2026-09-20, amount_remaining=USD 300.00 after a partial payment; provider reminder queued for 2026-09-27; cutoff=2026-09-26; owner=AR lead.',
    workedExample: 'Fictional output: case AR-42, source invoice in_42@rev5 and payment p_8; open balance USD 300.00, six days past due at cutoff; automatic reminder scheduled tomorrow, so manual send=hold; next=AR lead review after provider attempt. No message was sent.',
    inadequateExample: '“Invoice 42 is unpaid for 500 dollars. Email the customer now.”',
    inadequateReason: 'It ignores the partial payment and scheduled reminder, lacks exact customer and approval, and falsely implies a send.',
    providerNote: 'For Stripe, verify the invoice status and amount_remaining from the exact invoice object; draft, open, paid, uncollectible and void are distinct states. https://docs.stripe.com/api/invoices/object',
    automationOutput: 'In Subscription Receivable to Verified Outcome, return only receivable-review/v1 JSON for one exact invoice. Preserve entity, provider account/mode, customer, cutoff, total, credits, prior successful collections, remaining amount, contact suppression, approval and delivery state. A draft is unsent until an approved exact action has a provider receipt.',
  },
  {
    id: 'failed-payment-recovery', name: 'Failed Payment Recovery', icon: '🔁',
    role: 'Failed subscription payment investigation and recovery capability for a Billing Operations Crew',
    purpose: 'Explain a failed subscription payment, identify the current retry and customer-contact state, and propose one safe next step.',
    firstResult: 'A failed-payment case with invoice/payment/subscription IDs, failure and retry state, amount, customer contact history, policy next step, owner, and unsent draft if appropriate.',
    minimumInput: 'Authorized subscription, invoice and payment events, provider account and mode, retry/dunning policy, customer contact rules, and recovery owner.',
    optionalConnections: 'Billing provider, CRM and support inbox through scoped MCPs or exports; one failed-invoice export plus current status supports a first read-only investigation.',
    exampleRequests: ['Why did this subscription payment fail, and what is the next safe action?', 'Which failed invoices need a customer update after automatic retries?'],
    method: [
      'Bind exact provider account, mode, customer, subscription, invoice, payment attempt, currency, policy revision, observed time and owner.',
      'Re-read current invoice and payment state, failure code, retryability, next automatic attempt, updated payment method and prior contact.',
      'Separate retry scheduled, retry exhausted, already recovered, customer action needed and unknown; avoid interpreting one event as current state.',
      'Apply the owner’s retry and consent policy, considering the provider’s automated dunning and prior customer messages.',
      'Return one policy-safe next action and an unsent draft when contact is permitted; a charge retry or message is a separate approved route.',
    ],
    evidence: 'Cite the latest invoice/payment attempt, failure and next-attempt source, time, amount/currency, prior contact and policy decision. A missing next attempt must not be described as scheduled.',
    boundary: 'Do not trigger a charge, change a payment method, cancel a subscription, or contact a customer without exact approval and provider evidence. Never request card data in chat.',
    repeatRule: 'Deduplicate events by provider event and invoice IDs, re-read next_payment_attempt and invoice status, stop after recovery or policy exhaustion, and avoid duplicate messages during automatic dunning.',
    exampleInput: 'Fictional input: subscription sub_9, invoice in_9 open for USD 120.00; attempt att_3 failed 2026-09-25; next automatic attempt=2026-09-28; provider dunning email already queued; cutoff=2026-09-26.',
    workedExample: 'Fictional output: recovery case REC-9, status=automatic retry pending, source in_9@rev4 and att_3; owner action=verify whether the queued provider email was delivered after the retry; manual send=hold; amount remaining USD 120.00; no charge or contact receipt exists.',
    inadequateExample: '“The card failed; retry it now and send an email.”',
    inadequateReason: 'It ignores the provider retry and dunning schedule, does not verify current invoice state, and proposes unapproved charge and contact actions.',
    providerNote: 'For Stripe, inspect invoice status, amount_remaining, attempt_count and next_payment_attempt on the current invoice; automatic retries and dunning may already be configured. https://docs.stripe.com/api/invoices/object',
    automationOutput: 'In Subscription Receivable to Verified Outcome, return only receivable-review/v1 JSON for one exact invoice. Include the current failed/pending attempt and next provider retry with sources, current balance, prior contact and suppression. Do not turn a scheduled retry into a successful collection or propose duplicate customer contact.',
  },
  {
    id: 'refund-review', name: 'Refund Review', icon: '↩️',
    role: 'Customer refund request and remaining-balance review capability for a Billing Operations Crew',
    purpose: 'Reconcile a customer refund request to its original payment and previous refunds, then prepare an exact amount and policy decision for an approver.',
    firstResult: 'A refund decision record with payment/charge ID, original and prior-refunded amounts, remaining refundable amount, requested amount, currency, policy match, approver, and proposed state.',
    minimumInput: 'Authorized refund request, original payment and current refund records, policy, reason, currency, customer identity, and approval owner.',
    optionalConnections: 'Stripe or another processor, billing system and support inbox via scoped MCPs or exports; records support read-only preparation without refund permission.',
    exampleRequests: ['Can this customer receive the requested partial refund under our policy?', 'Prepare a refund approval record and customer-safe draft, but do not issue it.'],
    method: [
      'Bind exact account and mode, customer, original payment or charge, request, amount in minor units, currency, reason, policy revision and approver.',
      'Re-read payment capture/success state and every succeeded, pending or failed prior refund; separate provider state from a support request.',
      'Calculate remaining refundable amount under provider and owner policy; block duplicate or overlapping requests and cross-currency ambiguity.',
      'Compare the proposed amount and reason with policy, prior concessions and approval limits; label exceptions for a named reviewer.',
      'Return a decision record and optional unsent reply. Actual refund needs a separate exact-object approval, state recheck, idempotency key and provider receipt.',
    ],
    evidence: 'Show original, previously refunded, pending and requested amounts in the same minor-unit currency, all provider IDs, observed time, approval policy and remaining amount before any action.',
    boundary: 'Do not issue a refund, promise its completion, change a payment, or send a message from installation. A reviewed proposal is not a processed refund.',
    repeatRule: 'Re-read payment and refund objects immediately before any action, retain stable request and idempotency keys, avoid a second refund, and update customer state only from provider receipts.',
    exampleInput: 'Fictional input: charge ch_88 succeeded for USD 100.00; prior succeeded refund re_1 USD 25.00; no pending refund; new request USD 50.00; policy allows partial refund with owner approval.',
    workedExample: 'Fictional output: request RF-88, original=10,000 cents, prior refunded=2,500, remaining=7,500, requested=5,000, after-request remainder=2,500; currency USD; owner decision=pending; provider refund ID=none; customer draft=unsent.',
    inadequateExample: '“Refund 100 dollars; it has already been processed.”',
    inadequateReason: 'It ignores the prior refund and requested amount, exceeds the remaining balance, and asserts a provider action without a receipt.',
    providerNote: 'Stripe supports partial refunds up to the remaining unrefunded amount of the charge; use a Charge or PaymentIntent identifier and an amount in the smallest currency unit. https://docs.stripe.com/api/refunds/create',
  },
  {
    id: 'dispute-review', name: 'Dispute Review', icon: '🧾',
    role: 'Payment dispute evidence and deadline coordination capability for a Billing Operations Crew',
    purpose: 'Keep one payment dispute’s current deadline, reason, evidence gaps, and responsible owner reviewable without silently submitting evidence.',
    firstResult: 'A dispute case brief with dispute/charge IDs, amount and currency, status, response deadline, evidence inventory, missing proof, owner, and next review.',
    minimumInput: 'Authorized dispute and linked payment, provider account and mode, current status/deadline, relevant transaction evidence, dispute policy, and owner.',
    optionalConnections: 'Processor dispute records, signed agreement, delivery or access logs, support history and document store through scoped MCPs or exports.',
    exampleRequests: ['What evidence is missing for this dispute before the response deadline?', 'Prepare a dispute review packet and mark what still needs owner approval.'],
    method: [
      'Bind exact provider account and mode, dispute, charge/payment, customer, reason, amount/currency, status, deadline and responsible owner.',
      'Re-read current dispute state and submission history; distinguish an inquiry, needs-response case, under-review case and closed outcome.',
      'Collect only relevant, authorized contract, usage, delivery and contact evidence with source IDs, dates and privacy scope.',
      'List missing evidence and questions for the owner; do not treat an allegation, log gap or customer silence as proof.',
      'Prepare a review packet with a safe handoff deadline. Submission, acceptance, customer contact and closure each require separate provider state and approval.',
    ],
    evidence: 'Every factual claim needs a linked source, observation time and relevance to the specific dispute reason; the deadline and current status come from the provider record.',
    boundary: 'Do not submit or close a dispute, upload personal data to an unapproved service, promise a win, or contact a customer without the authorized process. Staging is not submission.',
    repeatRule: 'Re-read dispute status and deadline before a follow-up, keep evidence versions and prior submission receipts, stop duplicate submissions and preserve a lost or won outcome as observed provider state.',
    exampleInput: 'Fictional input: dispute du_42 needs_response for charge ch_42 USD 200.00; provider deadline 2026-10-01; signed agreement AG-42 exists; product access log for the disputed service period is missing.',
    workedExample: 'Fictional output: case DSP-42, deadline and status cited from du_42@rev3, agreement AG-42 available, access evidence missing; next=owner requests authorized access log by 2026-09-28 and reviews relevance; submitted=false, outcome=unknown.',
    inadequateExample: '“The customer used the product, so we won the dispute. Submit this evidence now.”',
    inadequateReason: 'The access record is missing, the provider outcome is unknown, and evidence submission lacks review and a provider receipt.',
    providerNote: 'For Stripe, use the current Dispute object for status and evidence details. Updating evidence can submit it; a reviewed draft must not be treated as a submitted case. https://docs.stripe.com/api/disputes',
  },
]

function checklist(pack: Pack): string {
  const checks = [
    { id: 'identity', title: 'Confirm Billing Crew identity and owner', instructions: 'Confirm whether this pack extends an existing Billing Operations Coordinator Crew or is its primary capability. Preserve the existing Crew identity and name its accountable owner.' },
    { id: 'skill', title: 'Verify pack skill', instructions: 'Confirm skills/' + pack.id + '/SKILL.md exists and ' + pack.id + ' is selected for this Crew.' },
    { id: 'scope', title: 'Bind exact case and account scope', instructions: 'Record business, provider account/mode, customer, case type, currency, time zone and first job. Minimum input: ' + pack.minimumInput },
    { id: 'access', title: 'Probe current records', instructions: 'Read one real authorized current case plus linked status/action history; record IDs, observed time and missing coverage. ' + pack.optionalConnections },
    { id: 'policy', title: 'Confirm policy and action boundary', instructions: 'Record owner policy, deadline or retry rule, approval authority, recipient/contact boundary and duplicate key. ' + pack.boundary },
    { id: 'first_result', title: 'Produce a sourced first result', instructions: 'Produce ' + pack.firstResult + ' ' + pack.evidence + ' Fictional examples do not complete setup.' },
    { id: 'review', title: 'Review with case owner', instructions: 'Show provider state, calculations, prior actions, unknowns and proposed next step. Record an owner correction or explicit approval decision.' },
    { id: 'delivery', title: 'Choose read-only or action route', optional: true, instructions: 'Choose chat-only review or a separately authorized exact-object action route. Chat-only completes this choice; a draft is never a sent message, refund, retry or dispute submission.' },
    { id: 'recurrence', title: 'Choose manual or recurring work', optional: true, instructions: 'Choose manual-only or a reviewed authenticated event/schedule with stable IDs, deduplication, retries, cost and notifications. Manual-only completes this choice. ' + pack.repeatRule },
  ]
  return JSON.stringify({ schema_version: 1, template_id: pack.id, template_version: 1, checks, completed_steps: [] }, null, 2) + '\n'
}

function skill(pack: Pack): string {
  return [
    '---', 'name: ' + pack.id, 'description: ' + pack.purpose, '---', '',
    '# ' + pack.name, '',
    'This is a Billing Operations capability pack. Add it to an existing compatible Crew when owner and access are shared. It also works as a primary Crew role when those boundaries differ. Installation selects no external account or action route.', '',
    '## Setup in chat', '',
    'Read templates/' + pack.id + '/TEMPLATE_SETUP.json and templates/' + pack.id + '/SETUP.md. Verify checks with current authorized records before adding IDs to completed_steps. Preserve existing Crew identity, other packs and setup progress. Report verified, blocked and next.', '',
    '## First useful result', '',
    ...pack.method.map((step, index) => String(index + 1) + '. ' + step), '',
    'Deliver **' + pack.firstResult + '** ' + pack.evidence, '',
    '## Provider-specific probe', '', pack.providerNote, '',
    '## Fictional worked example', '', pack.exampleInput, '', pack.workedExample, '',
    'Inadequate: ' + pack.inadequateExample + ' Reason: ' + pack.inadequateReason, '',
    '## Later run', '', pack.repeatRule, '',
    ...(pack.automationOutput ? ['## Automation handoff', '', pack.automationOutput, ''] : []),
    '## Boundaries', '', pack.boundary + ' Installation enables no schedule, trigger, function, Automation, provider write or customer message. Never copy another Crew’s credentials.', '',
  ].join('\n')
}

function guide(pack: Pack): string {
  return [
    '# ' + pack.name + ' setup', '',
    'Template ' + pack.id + ' version 1. Progress lives in templates/' + pack.id + '/TEMPLATE_SETUP.json and is verified in Crew chat.', '',
    '## First result', '', 'Provide ' + pack.minimumInput + ' Ask: “' + pack.exampleRequests[0] + '”', '',
    'Expected output: **' + pack.firstResult + '** ' + pack.evidence, '',
    '## Fictional example and failure', '', pack.exampleInput, '', pack.workedExample, '',
    'Inadequate: ' + pack.inadequateExample + ' Reason: ' + pack.inadequateReason, '',
    '## Connection and action choice', '', pack.optionalConnections + ' Start with one authorized record or export. ' + pack.providerNote + ' ' + pack.boundary, '',
    '## Repeat policy', '', pack.repeatRule + ' Builder must inspect existing Crews, test one real case, keep external actions separate, and review any schedule or event before activation.', '',
  ].join('\n')
}

export const billingPacks: readonly CrewTemplate[] = packs.map(pack => {
  const base = 'templates/' + pack.id
  const skillPath = 'skills/' + pack.id + '/SKILL.md'
  const setupPath = base + '/TEMPLATE_SETUP.json'
  const setupGuidePath = base + '/SETUP.md'
  return {
    id: pack.id, version: 1, category: 'Finance', subcategory: 'Billing capability pack', name: pack.name, icon: pack.icon,
    role: pack.role, purpose: pack.purpose, firstResult: pack.firstResult, minimumInput: pack.minimumInput,
    optionalConnections: pack.optionalConnections, exampleRequests: pack.exampleRequests, selectedSkills: [pack.id],
    setupPath, setupGuidePath, requiredFiles: [skillPath, setupGuidePath, setupPath],
    files: { [skillPath]: skill(pack), [setupGuidePath]: guide(pack), [setupPath]: checklist(pack) },
  }
})
