# Installable Crew template profiles

Status: documentation companion to the [Crew template catalog](crew_template_catalog.md), reviewed 2026-09-26. These profiles describe the job to prove during chat setup. The [frontend catalog](../../frontend/src/products/work/crewTemplates.ts) and its specialist modules remain the source of truth for installed skill text, versions, and checklist IDs. An example or suggested connection here does not grant access, enable recurrence, or establish that a customer has completed setup.

Each profile answers: **when to use it, what a first result must contain, what setup must verify, and what changes on a later run.** The thirty-six multi-Crew journeys have linked JSON fixtures; other individual agent outputs still need complete worked examples before they are promoted as fully demonstrated public templates. See the [content quality review](../reviews/playbook_template_content_quality_2026-09-25.md).

## Finance

The three core Billing Operations, Revenue & Close, and Spend & Payables installed skills now include fictional worked and rejected cases plus role-specific source probes. Their examples separate a reviewed decision from a provider payment, refund, accounting entry, or approval. An authorized customer record and owner review remain necessary for setup.

### Finance Analyst (`finance-analyst`)

- **Use case:** explain revenue, expense, cash, or SaaS metric changes from authorized records for a named period.
- **First result:** a sourced brief that distinguishes billed, collected, recognized, and cash amounts, shows calculations and definitions, and lists unreconciled items. The [Finance Analyst skill](../../frontend/src/products/work/templates/finance-analyst/SKILL.md) includes a fictional calculation; Finance Operations Review has an [illustrative impact readout](../../playbooks/agentic-engineering-platform/finance/finance-operations-review/examples/finance-impact-readout.json).
- **Setup proof:** read one actual statement/export or connected record, agree on entity, period, currency, accounting basis, and metric definitions, reproduce one calculation, and have the owner review the brief. A spreadsheet skill or accounting MCP is optional when the file route works.
- **Later run:** preserve definitions and source IDs, explain changes against the prior period, and carry unresolved exceptions forward rather than generating a new unsupported forecast.

### Billing Operations Coordinator (`billing-operations-coordinator`)

- **Use case:** prioritize overdue invoices, failed payments, refund requests, and disputes for a subscription business.
- **First result:** a dated exception queue with invoice/payment IDs, amount and currency, deadline or status, prior-contact evidence, proposed owner action, and drafts awaiting review. See the [illustrative queue](../../playbooks/agentic-engineering-platform/finance/finance-operations-review/examples/billing-exception-queue.json).
- **Setup proof:** read one authorized billing record or export, verify the customer's invoice, refund, and contact policies, identify the owner, and review one queue item against source status. Stripe or Paddle is a provider choice, never a presumed connection.
- **Later run:** re-read current payment/refund/dispute and prior-contact state, retain stable case IDs, and avoid a second follow-up or refund proposal for a resolved case. A real refund or message requires a separate authorized action.

### Invoice Chasing (`invoice-chasing`)

- **Use case:** decide which genuinely overdue customer invoices need an owner-reviewed follow-up. Install this pack on Billing Operations Coordinator when the same owner and access apply.
- **First result:** a ranked invoice queue with current open balance, due date, partial payments or credits, prior and scheduled contact, source IDs, and an unsent next reminder. The [pack skill](../../frontend/src/products/work/billingPacks.ts) contains a fictional partial-payment example. For a separate Finance verification, the [receivable Playbook](../../playbooks/agentic-engineering-platform/finance/subscription-receivable-to-verified-outcome/SKILL.md) binds one exact invoice.
- **Setup proof:** bind the exact billing account and mode, cutoff, invoice terms, quiet period and contact owner; re-read one current invoice plus payments and contact history; have the owner review the queue. Its nine-check file remains pending until verified in chat.
- **Later run:** re-read payment and provider reminder state, keep a stable invoice/contact key, and suppress duplicate or premature reminders. Sending requires a separate approved route and receipt.

### Failed Payment Recovery (`failed-payment-recovery`)

- **Use case:** investigate a failed subscription payment and choose the next policy-safe action.
- **First result:** a case brief that binds subscription, invoice, attempt, amount, failure, next retry, prior dunning, owner and unsent draft when contact is permitted. The [receivable Playbook](../../playbooks/agentic-engineering-platform/finance/subscription-receivable-to-verified-outcome/SKILL.md) adds a validated Finance outcome for the same invoice.
- **Setup proof:** probe a current authorized invoice and payment attempt, confirm the provider's retry state and the customer's contact policy, and review one proposed next step with the owner. A failed event alone is insufficient evidence of current state.
- **Later run:** deduplicate by event and invoice, re-read payment and next-attempt state, and stop after recovery or policy exhaustion. No charge retry or message is activated by the pack.

### Refund Review (`refund-review`)

- **Use case:** prepare a decision on a customer refund request without silently issuing money.
- **First result:** an exact-amount record with original payment, prior and pending refunds, remaining refundable amount, requested amount, currency, reason, policy, approver and unsent customer response.
- **Setup proof:** match an authorized request to the current payment and refund records, reproduce the remaining-balance calculation, record approval limits, and have the owner review a real proposal.
- **Later run:** re-read provider state and prior refunds before any action, retain a stable request and idempotency key, and mark processed only from a provider receipt. Refund writes are separately approved.

### Dispute Review (`dispute-review`)

- **Use case:** coordinate evidence and an owner decision for a specific payment dispute before its response deadline.
- **First result:** a sourced case brief with current dispute status, payment ID, amount, deadline, relevant evidence, missing proof, owner and next review date.
- **Setup proof:** read the current provider dispute and linked payment, verify deadline and prior submissions, inspect one authorized evidence source, and have the owner review gaps and privacy scope.
- **Later run:** re-read status and deadline, retain evidence versions and submission receipts, and distinguish a staged packet from actual submission or a provider-observed outcome.

### Revenue & Close Analyst (`revenue-close-analyst`)

- **Use case:** prepare a reviewable subscription period close.
- **First result:** a close checklist and memo that reconciles invoices, credits, collections, and ledger or revenue-schedule amounts, with source-linked differences and their owner.
- **Setup proof:** agree on entity, close period, currency, accounting policy, and authoritative billing and ledger sources; reproduce one matching and one unmatched transaction. An export is sufficient for a first read-only review.
- **Later run:** retain the prior exception IDs, report cleared versus new differences, and avoid changing an accounting entry without separate approval and write access.

### Spend & Payables Coordinator (`spend-payables-coordinator`)

- **Use case:** review proposed purchases, bills and company spend before any contract, PO, payment or approval action.
- **First result:** a source-linked queue with due dates, duplicate candidates, missing evidence, policy exceptions, approvers, and next decisions. The [invoice intake example](../../playbooks/agentic-engineering-platform/finance/invoice-intake-to-reviewed-payable/examples/payable-review.json) shows distinct document and AP states; the [vendor purchase example](../../playbooks/agentic-engineering-platform/operations/vendor-evaluation-to-purchase-decision/examples/vendor-purchase-pending.json) keeps owner review separate from purchase.
- **Setup proof:** read a representative bill or expense export, establish entity, currency, period, approval thresholds, and paid-status source, then have an owner review a flagged item. For a purchase, also validate the exact plan/quote comparison and re-read current vendor, commitments, budget, security/privacy and policy sources.
- **Later run:** deduplicate by vendor, invoice ID and amount under the customer's policy; recheck due and paid states; preserve deferred exceptions. The Crew does not pay a bill by being installed.

### Tax Export Preparer (`tax-export`)

- **Use case:** prepare transaction records for the owner and tax professional.
- **First result:** a reconciled export with source references, period, jurisdiction, currencies, classification questions, and an exceptions list. The [Tax Export skill](../../frontend/src/products/work/templates/tax-export/SKILL.md) shows a fictional CSV and transfer-aware reconciliation.
- **Setup proof:** agree on recipient format and required fields; verify one invoice/payment/refund mapping and totals against an authorized export; have the owner or professional review unresolved treatment.
- **Later run:** carry prior source IDs and recipient decisions forward, avoid duplicate rows, and identify changed or newly missing evidence. This pack does not file or classify tax obligations automatically.

## Marketing → Website Growth

### Website Growth Starter (`website-growth-starter`)

- **Use case:** a newly launched website needs a first prioritized plan for relevant visitors.
- **First result:** a dated, sourced site audit and 30-day action brief tied to the offer, buyer, and useful visitor action. See the [illustrative priority brief](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/examples/growth-priority-brief.json).
- **Setup proof:** inspect bounded public URLs or the owner's export, record offer/audience/action and the actual evidence behind each priority, then review the order with the owner. Search Console and analytics are optional for a new site's first plan.
- **Later run:** read prior actions and decisions, inspect changed pages first, and report no new evidence if nothing changed. A traffic lift requires comparable measured data.

### SEO Analyst (`seo-analyst`)

- **Use case:** identify observable crawl, metadata, internal-link, and page-experience issues.
- **First result:** page-level issue list with inspected URL, observation time, impact and effort rationale, confidence, and retest.
- **Setup proof:** agree on canonical domain and crawl scope; reproduce one issue from a fetched or rendered page. Search Console access is required only for claims about its private indexing data.
- **Later run:** retest prior issue IDs and changed URLs; mark fixed, persistent, or unverified rather than reporting every issue as new.

### Search Opportunity Mapper (`search-opportunity-mapper`)

- **Use case:** map real buyer questions to existing pages and justified content gaps.
- **First result:** a ranked question-to-page map with source, buyer intent, answer status, evidence, and next action. See the [illustrative map](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/examples/search-opportunity-list.json) and its [rejected counterpart](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/examples/invalid-search-opportunity-list.json).
- **Setup proof:** save an exact question and its origin, inspect the relevant page set, and have the owner review one `answered`, `weak_answer`, or `gap` decision. Search volume remains unknown without an authorized source.
- **Later run:** keep stable question IDs, inspect changed pages and new question evidence, and preserve the owner's rejected or deferred choices.

### Content Brief Writer (`content-brief-writer`)

- **Use case:** turn an approved buyer question or search opportunity into a page brief.
- **First result:** intended reader and action, angle, outline, claims with sources or verification flags, relevant internal links, and an owner decision. The [illustrative approved brief](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/examples/content-brief.json) and [rejected hypothesis brief](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/examples/invalid-content-brief.json) show the optional Automation handoff.
- **Setup proof:** verify the approved opportunity, inspect existing pages for duplication, and review at least one factual claim against product material. A CMS connection is optional for a brief.
- **Later run:** update the same brief when new source facts or owner direction arrive; do not issue a second brief as if the first were never reviewed.

### Content Page Builder (`content-page-builder`)

- **Use case:** draft an approved page for human review.
- **First result:** a page draft tied to its brief, with source notes, internal links, a clear next action, and unresolved claims. The [reviewable draft](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/examples/reviewable-page-draft.json) remains unpublished; the [false publication](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/examples/invalid-page-draft.json) is rejected.
- **Setup proof:** read the approved brief and brand/source material, inspect the existing page context, and review the rendered preview if an implementation is supplied. Drafting is distinct from publication.
- **Later run:** revise the same draft against reviewer comments and changed facts; require an actual ship record before any measurement route treats it as published.

### Website Publishing Coordinator (`website-publishing-coordinator`)

- **Use case:** move an approved site-page draft through a separately authorized publication and verify what is live.
- **First result:** a readiness record with the exact draft, target and blockers; after release, a `shipped-change/v1` with approval, provider receipt, live revision and inspection evidence. The [fictional verified record](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/examples/shipped-change.json) and [rejected false ship](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/examples/invalid-shipped-change.json) show the boundary.
- **Setup proof:** bind the approved page and content artifact IDs, confirm owner and release authority, test a live-page check, and capture a provider receipt from an authorized release. A draft, merge or approval alone is not shipped.
- **Later run:** compare the same target URL and revision to prior release evidence, retest links and target action, and keep unverified changes pending. Distribution and measurement use only a verified change.

### Search Console Optimizer (`search-console-optimizer`)

- **Use case:** find existing pages with actionable query evidence.
- **First result:** page/query recommendation with property, filters, date range, source rows, interpretation, and proposed title, copy, or link change.
- **Setup proof:** test the exact Search Console property or export and reproduce one page/query metric using compatible dimensions and dates; note missing or anonymized query coverage.
- **Later run:** compare compatible windows and filters, retain the prior recommendation decision, and separate observed change from unsupported causal claims.

### Traffic & Engagement Analyst (`traffic-engagement-analyst`)

- **Use case:** explain changes in useful visits and conversion actions.
- **First result:** sourced readout with metric definitions, numerator and denominator where relevant, segments, data gaps, and one prioritized next action.
- **Setup proof:** verify analytics property/export, event definition, timezone and period; reproduce one reported metric. A new site may need a baseline-first report instead of a trend.
- **Later run:** compare like-for-like periods, flag instrumentation changes, and link any actual shipped change without claiming causality from timing alone.

### AI Visibility Analyst (`ai-visibility-analyst`)

- **Use case:** observe whether chosen answer engines cite the brand or competitors for buyer questions.
- **First result:** question-level observations with engine/surface, run time, cited URLs, absence or error state, and source-backed content gaps.
- **Setup proof:** freeze the question set and sampling method, run and retain an inspectable observation, and distinguish unavailable engine output from a genuine absence. This is an experimental measure.
- **Later run:** repeat under the same method or explicitly mark method changes; do not present one stochastic answer as a stable ranking. The [AI Visibility Intelligence Automation](../../playbooks/agentic-engineering-platform/growth-analytics/ai-visibility-intelligence/SKILL.md) links this sampled evidence to a separately reviewed page opportunity.

### Landing Page Optimizer (`landing-page-optimizer`)

- **Use case:** diagnose one page's path to a useful visitor action.
- **First result:** page-level diagnosis, observed friction, bounded revision or test hypothesis, success signal, and guardrails.
- **Setup proof:** inspect the page and target action, verify the visitor intent and available behavior data, and review one proposed change with the owner. A low-traffic page may warrant a qualitative revision rather than an A/B test.
- **Later run:** compare the reviewed change against a verified ship record and appropriate observation window; report inconclusive results honestly.

### Content Distribution Coordinator (`content-distribution-coordinator`)

- **Use case:** distribute an already published asset through relevant approved channels.
- **First result:** channel-by-channel audience fit, asset URL, drafts, approval points, tracking plan, and owner action.
- **Setup proof:** verify the live asset and an allowed channel, review one complete channel-specific draft and the contact policy. A social, email, or CRM connection is optional for planning and required for its actual route.
- **Later run:** retain campaign/action IDs and delivery receipts, avoid duplicate posts or outreach, and compare observed results without inventing attribution.

## Marketing → Other Growth

### Competitor Intelligence Analyst (`competitor-intelligence-analyst`)

- **Use case:** monitor a bounded competitor/product set for changes relevant to the owner's offer and buyer.
- **First result:** dated prior/current source comparison, exact product/plan and market, relevance to the offer, uncertainties, and an owner question. The optional [fictional context](../../playbooks/agentic-engineering-platform/marketing/campaign-signal-to-reviewed-experiment/examples/competitor-context.json) remains separate from campaign measurement.
- **Setup proof:** read one current primary source and a dated baseline capture, confirm competitor and product IDs, scope, buyer, threshold and allowed source access, then review one finding with the owner.
- **Later run:** compare stable product and URL keys, suppress unchanged alerts, recheck expiring prices and terms, and avoid treating a vendor claim as customer preference.

### Campaign Performance Analyst (`campaign-performance-analyst`)

- **Use case:** explain a bounded campaign change using platform and downstream qualified-event records.
- **First result:** campaign and account IDs, current and comparable baseline spend, clicks, deduplicated qualified events, rates, attribution window, source coverage and one owner question. See the [fictional brief](../../playbooks/agentic-engineering-platform/marketing/campaign-signal-to-reviewed-experiment/examples/campaign-performance-brief.json).
- **Setup proof:** read one real current and baseline campaign record plus CRM or analytics events, agree on denominator, qualification, currency, period and data lag, reproduce one rate, then review uncertainty with the owner.
- **Later run:** preserve metric policy and campaign/event IDs, wait for the agreed attribution lag, compare like periods, and deduplicate alerts before proposing a new experiment.

### Funnel Analyst (`funnel-analyst`)

- **Use case:** explain one signup-to-paid cohort across product events and billing, with stable identity and ordered stages.
- **First result:** [fictional reconciled observation](../../playbooks/agentic-engineering-platform/growth-analytics/funnel-conversion-intelligence/examples/funnel-observation.json) with eligible, signup, activated and paid counts, denominators, identity coverage, paid-state cutoff and limitations.
- **Setup proof:** inspect one authorized event and matching paid-state record, verify account join and event version, reproduce a current and baseline rate, and review missing identities with the owner.
- **Later run:** preserve cohort and event definitions, wait for billing lag, deduplicate accounts and start a new baseline if instrumentation changes. The [Automation Playbook](../../playbooks/agentic-engineering-platform/growth-analytics/funnel-conversion-intelligence/SKILL.md) hands the validated observation to Growth Experiment Planner.

### Growth Experiment Planner (`growth-experiment-planner`)

- **Use case:** turn one evidence-backed growth question into a bounded, reviewable experiment.
- **First result:** falsifiable hypothesis, eligible unit, one treatment, primary and guardrail metrics, baseline, sample or duration rule, stop condition, owner and next decision. The [campaign plan](../../playbooks/agentic-engineering-platform/marketing/campaign-signal-to-reviewed-experiment/examples/growth-experiment-plan.json) and [funnel plan](../../playbooks/agentic-engineering-platform/growth-analytics/funnel-conversion-intelligence/examples/funnel-experiment-plan.json) remain unlaunched.
- **Setup proof:** review a real source brief, baseline or baseline-first decision, metric definitions, target population, change authority and safety guardrail with the owner before proposing a test.
- **Later run:** retain experiment ID and predeclared rule, re-read comparable outcome and guardrail records, report null or incomplete results, and require a separate approved route for any launch.

### Experiment Run Coordinator (`experiment-run-coordinator`)

- **Use case:** verify whether one frozen and approved growth experiment actually launched in its provider, with rollout and rollback ownership.
- **First result:** [execution record](../../playbooks/agentic-engineering-platform/growth-analytics/growth-experimentation-follow-through/examples/experiment-execution-record.json) with exact plan revision, dated approval, provider launch/exposure receipts and readout window, or an honest [pending approval](../../playbooks/agentic-engineering-platform/growth-analytics/growth-experimentation-follow-through/examples/experiment-pending-approval.json).
- **Setup proof:** probe one approved plan revision, decision source and current provider object. A work item or plan status is not a launch receipt. Review any action route and rollback owner separately.
- **Later run:** re-read provider state under the same experiment and variant IDs; preserve configuration revisions, prevent duplicate launches, and report rollback or changed allocation as a new state.

### Growth Outcome Analyst (`growth-outcome-analyst`)

- **Use case:** measure a provider-confirmed growth experiment against its frozen primary and guardrail rules.
- **First result:** [inconclusive](../../playbooks/agentic-engineering-platform/growth-analytics/growth-experimentation-follow-through/examples/experiment-inconclusive-readout.json) or [measured](../../playbooks/agentic-engineering-platform/growth-analytics/growth-experimentation-follow-through/examples/experiment-measured-readout.json) readout with exact arm counts, rates, source revisions, sample/window gates and owner question.
- **Setup proof:** verify the execution artifact and exposure-to-outcome identity join; reproduce one numerator, denominator and guardrail rate from authorized records. An unfinished window or missing sample stays pending or inconclusive.
- **Later run:** wait for source lag, preserve the original plan and earlier readouts, and supersede only with sourced corrections. Shipping or rollback remains a separately approved action.

## Sales

The three inbound Sales skills include fictional worked and rejected cases aligned to Inbound Lead-to-Meeting Review. Each setup checklist now probes the specific source join and decision boundary for its role. These examples demonstrate the expected output; a real lead or account source and seller review are still required to complete setup.

### Sales Call Briefing Assistant (`sales-call-briefing`)

- **Use case:** prepare a seller for one exact buyer meeting without inventing buying intent or attendee authority.
- **First result:** a dated brief binding meeting, account and opportunity IDs to verified CRM/invite facts, approved product proof, unknowns and discovery questions. The [installable skill](../../frontend/src/products/work/salesExpansion.ts) includes a fictional good and bad brief.
- **Setup proof:** resolve company identity, read one authorized current invitation and CRM record, check approved claims and source freshness, and have the seller review a real brief. Nine checks stay pending until verified in chat.
- **Later run:** re-read the same meeting and opportunity, show changes in attendees or stage, and retire a cancelled meeting rather than repeat stale claims.

### Proposal Drafter (`proposal-drafter`)

- **Use case:** prepare an unsent SaaS proposal from seller-approved discovery and current product and pricing material.
- **First result:** versioned draft with sourced scope, line-item arithmetic, currency, assumptions, exclusions, open questions and commercial approval state.
- **Setup proof:** bind exact account/opportunity and approved discovery revision, probe the current price list and offer, reproduce one line calculation, and have the commercial owner review claims and gaps.
- **Later run:** re-read changed discovery and pricing, show a revision diff, and preserve an approved or sent version. Sending and CRM changes require separate approval and provider evidence.

### Pipeline Analyst (`pipeline-analyst`)

- **Use case:** explain observed pipeline movement and stale opportunities from comparable CRM records.
- **First result:** dated brief with stable opportunity IDs, stage movements, amount and currency, source coverage, stale-rule results and owner decisions.
- **Setup proof:** read authorized prior and current snapshots, verify stage definitions, reporting window, currency policy and one movement calculation with the pipeline owner. A single snapshot supports current-state review only.
- **Later run:** compare the same IDs under unchanged definitions, flag changed rules or missing records, and do not turn stage movements into new revenue or a claimed forecast.

### Deal Follow-through Coordinator (`deal-follow-through-coordinator`)

- **Use case:** turn a validated stale-opportunity exception into a seller-owned next step, or prepare an executed deal for CS acceptance.
- **First result:** an exact-opportunity [action register](../../playbooks/agentic-engineering-platform/sales/pipeline-health-to-owned-action/examples/deal-action-register.json) with pending decision, or a [contract-backed Sales handoff](../../playbooks/agentic-engineering-platform/customer-success/signed-deal-to-onboarding-handoff/examples/sales-cs-handoff.json) with purchased scope and agreed first result. Neither claims a send, CRM write or onboarding start.
- **Setup proof:** for pipeline work, validate a [comparable-snapshot exception](../../playbooks/agentic-engineering-platform/sales/pipeline-health-to-owned-action/examples/pipeline-exception-brief.json) and re-read current activity and contact state. For a signed handoff, verify executed agreement/revision, CRM account, entitlement and receiving CS owner. A Closed Won stage alone is insufficient.
- **Later run:** keep opportunity and action or handoff keys, re-read current stage, contract, entitlement and owner as relevant, retire superseded suggestions, and count execution only from separately approved provider evidence.

### Lead Intake & Qualifier (`lead-intake-qualifier`)

- **Use case:** decide what to do with an authorized inbound enquiry.
- **First result:** deduplicated, source-linked fit brief with unknowns, policy state and owner decision. See the [illustrative qualification brief](../../playbooks/agentic-engineering-platform/sales/inbound-lead-to-meeting-review/examples/lead-qualification-brief.json).
- **Setup proof:** read one form/CRM/export record, agree on fit and routing rules, check duplicate scope and contact policy, and have the owner review a real brief. A form submission is not permission for every channel.
- **Later run:** keep the source and brief IDs, re-read lead stage and duplicate state, and stop downstream contact for not-fit, blocked, or unresolved duplicate cases.

### Account Researcher (`account-researcher`)

- **Use case:** give a seller verified context for an approved company or lead.
- **First result:** dated company facts, source URLs, clearly labeled hypotheses, and discovery questions. See the [illustrative research brief](../../playbooks/agentic-engineering-platform/sales/inbound-lead-to-meeting-review/examples/account-research-brief.json).
- **Setup proof:** resolve the actual company and domain, inspect approved public or customer-provided sources, and review one claimed fact against its source. Public research does not authorize outreach.
- **Later run:** recheck time-sensitive facts, preserve the company binding and prior questions, and describe what changed since the last brief.

### Sales Follow-up Coordinator (`sales-followup-coordinator`)

- **Use case:** prepare a reviewed reply or booking offer for a qualified inbound lead.
- **First result:** an [unsent draft](../../playbooks/agentic-engineering-platform/sales/inbound-lead-to-meeting-review/examples/sales-followup-draft.json) with cited claims, exact recipient reference, booking path, approval owner, and next check.
- **Setup proof:** verify a valid qualification brief, prior contact, suppression and replies, policy, owner, approved offer, and booking URL. A connected sender and observed calendar/CRM source are needed only if delivery and outcome tracking are selected.
- **Later run:** re-read current state before another touch, preserve a stable action and exact draft fingerprint, and count send/booking only from linked provider and event receipts.

## Customer Success

The five installed Customer Success skills include a fictional input and reviewable output, a failed output, and a role-specific identity or evidence probe. Onboarding and first value share example account IDs; renewal has a separate exact-contract case. A real customer account and owner decision are still required to complete setup.

### Customer Onboarding Coordinator (`customer-onboarding-coordinator`)

- **Use case:** accept or return an exact signed-customer handoff, then turn an authorized handoff into an owned path to first value.
- **First result:** milestone register with purchased scope, owner, target date, evidence, blockers, and next customer decision. See the [illustrative register](../../playbooks/agentic-engineering-platform/customer-success/new-customer-to-first-value/examples/onboarding-milestone-register.json).
- **Setup proof:** for a Sales handoff, re-read current contract and entitlement and record [receiving-owner acceptance](../../playbooks/agentic-engineering-platform/customer-success/signed-deal-to-onboarding-handoff/examples/onboarding-acceptance.json) or a blocker. Then match account identity to the onboarding tracker, agree on the first-value definition, and review one milestone and evidence source with the owner.
- **Later run:** update stable milestone IDs and slippage reasons; do not claim completion from a plan or contact the customer without an approved route.

### Product Adoption Analyst (`product-adoption-analyst`)

- **Use case:** determine whether a customer achieved first value or whether eligible accounts saw and used a released feature.
- **First result:** [first-value readout](../../playbooks/agentic-engineering-platform/customer-success/new-customer-to-first-value/examples/first-value-readout.json) with event definition, observed state, source records, coverage gaps, and owner action.
- **Setup proof:** verify account-to-event identity mapping, the observation window, a representative authorized product event or export, and the agreed rule for first value. For a released feature, verify build, flag revision, eligibility, exposure and use joins, target and minimum sample against [the observation](../../playbooks/agentic-engineering-platform/product/released-feature-to-adoption-decision/examples/feature-adoption-observation.json). Missing instrumentation is unknown, not proof of no adoption.
- **Later run:** compare the same event rule and account identity, record newly observed evidence, and preserve earlier unknowns or changed instrumentation.

### Lifecycle Analyst (`lifecycle-analyst`)

- **Use case:** measure day-7 activation and day-30 retention for defined SaaS signup cohorts, distinct from an individual account's first-value readout.
- **First result:** [mature comparable cohorts](../../playbooks/agentic-engineering-platform/growth-analytics/activation-retention-intelligence/examples/cohort-retention-observation.json), [one baseline](../../playbooks/agentic-engineering-platform/growth-analytics/activation-retention-intelligence/examples/cohort-retention-baseline-first.json), or [pending maturity](../../playbooks/agentic-engineering-platform/growth-analytics/activation-retention-intelligence/examples/cohort-retention-pending-maturity.json), each with exact populations, rates or unknowns, source coverage and limitations.
- **Setup proof:** freeze versioned cohort and event rules, verify a representative authorized signup/product/billing join, recompute denominators and day-30 maturity, and review unmatched accounts with the owner. [Activation and Retention Intelligence](../../playbooks/agentic-engineering-platform/growth-analytics/activation-retention-intelligence/SKILL.md) accepts only validated mature evidence for its experiment slot.
- **Later run:** wait until the full signup cohort matures and billing lag settles, preserve policy and source revisions, and start a new baseline when instrumentation changes. A cohort difference is neither causal proof nor an individual churn label.

### Customer Health Coordinator (`customer-health-coordinator`)

- **Use case:** review an account's adoption, support and renewal signals together.
- **First result:** [health brief](../../playbooks/agentic-engineering-platform/customer-success/new-customer-to-first-value/examples/customer-health-brief.json) for first value or a bounded [renewal health brief](../../playbooks/agentic-engineering-platform/customer-success/renewal-risk-to-owned-decision/examples/renewal-health-brief.json), each with observed signals, hypotheses, coverage and an owner question.
- **Setup proof:** agree on account owner, health rules and source scope; verify one current signal and its first-value or usage source. A missing source must remain visible. Renewal terms are verified by Renewal Coordinator.
- **Later run:** update stable risk/action IDs, distinguish a resolved blocker from stale data, and avoid declaring churn risk from a single unsupported signal.

### Renewal Coordinator (`renewal-coordinator`)

- **Use case:** turn bounded account health plus current executed renewal terms into an owned contract decision.
- **First result:** an [exact-contract renewal register](../../playbooks/agentic-engineering-platform/customer-success/renewal-risk-to-owned-decision/examples/renewal-decision-register.json) with notice calculation, current billing state, owner decision or blocker, and no implied customer action.
- **Setup proof:** probe the executed agreement and revision, matching subscription, notice timezone and prior notice ledger, current billing record and validated health artifact for the exact tenant/account. Recompute days to notice and have the renewal owner review a real case.
- **Later run:** retain case key and prior decisions, re-read contract and notice receipts, and start a fresh review when terms or owner change. Count messages, amendments or cancellations only from separate approved routes and provider evidence.

## Customer Support

### Support Triage Assistant

- **Use case:** classify a current support case, its urgency, duplicate state, and next owner decision.
- **First result:** a sourced `support-case-triage/v1` with tenant, case, account, thread revision, priority policy, symptom versus verified incident state, owner, and next action. See the [illustrative triage](../../playbooks/agentic-engineering-platform/customer-support/support-case-to-reviewed-resolution/examples/support-case-triage.json).
- **Setup proof:** read one authorized case and latest thread, verify policy and owner, check prior contact and duplicate records, then review a real triage decision.
- **Later run:** re-read the current thread and status, retain case identity, and supersede outdated priority or incident claims when evidence changes.

### Support Reply Drafter

- **Use case:** prepare a customer response for the exact case and recipient from current approved material.
- **First result:** a grounded, unsent `support-reply-draft/v1` with claim references, channel, recipient, open questions, and approval state. See the [illustrative reply](../../playbooks/agentic-engineering-platform/customer-support/support-case-to-reviewed-resolution/examples/support-reply-draft.json).
- **Setup proof:** inspect a real case, approved help source, reply policy, and prior contact; have the owner review one exact unsent message.
- **Later run:** re-read the thread before revising or sending. A delivered message needs exact approval and a provider receipt; a draft is never delivery proof.

### Escalation Coordinator

- **Use case:** hand a case to a receiving team when access, ownership, or expertise changes.
- **First result:** a bounded impact brief with case and account IDs, deadline, receiving owner, acceptance state, and evidence. See the [illustrative escalation](../../playbooks/agentic-engineering-platform/customer-support/support-case-to-reviewed-resolution/examples/support-escalation-brief.json).
- **Setup proof:** verify escalation policy, accepting team, one real case, and a receiving-owner decision; do not report a pending handoff as accepted.
- **Later run:** re-read impact and owner state, preserve handoff and deadline IDs, and notify the support owner when acceptance or status changes.

### Feedback & Review Analyst

- **Use case:** group a bounded feedback or review set into themes and owner decisions.
- **First result:** sourced theme brief with time window, denominator and source coverage, examples, urgency, and a proposed product or support action; any public response remains an unsent draft.
- **Setup proof:** verify one authorized feedback export, source scope, review policy, owner, and a theme against representative and counterexample records.
- **Later run:** compare the same source and window rules, retain prior theme IDs and owner decisions, and avoid counting duplicate reviews as new signals.

### Support Knowledge Curator (`support-knowledge-curator`)

- **Use case:** find repeated unresolved support questions and stale or missing help instructions.
- **First result:** a linked case and article-gap brief with current article revision, approved product source, unsent update draft, reviewer and later publication check.
- **Setup proof:** deduplicate two real cases, exclude a contrary case, compare the exact article with current product behavior, then have the support owner review a privacy-safe draft. Draft or approval is not publication.
- **Later run:** re-read the same article revision and new cases; verify an approved publication from the provider and assess outcomes only with comparable coverage.

## Product

### Product Feedback Coordinator (`product-feedback-coordinator`)

- **Use case:** turn a validated feedback theme or released-feature adoption observation into a product owner decision against current issues and roadmap records.
- **First result:** a sourced `product-feedback-decision/v1` for a bounded theme or `feature-adoption-decision/v1` for a released feature. Each keeps the exact upstream artifact and scope, denominator and coverage, current issue match, evidence gaps, owner options and action state. See the [feedback decision](../../playbooks/agentic-engineering-platform/product/feedback-theme-to-product-decision/examples/product-feedback-decision.json) and [feature decision](../../playbooks/agentic-engineering-platform/product/released-feature-to-adoption-decision/examples/feature-adoption-decision.json).
- **Setup proof:** validate a [bounded theme brief](../../playbooks/agentic-engineering-platform/product/feedback-theme-to-product-decision/examples/feedback-theme-brief.json), read the authorized current issue source, verify match policy and product owner, and review one real theme with counterexamples. For feature adoption, also validate [the bounded observation](../../playbooks/agentic-engineering-platform/product/released-feature-to-adoption-decision/examples/feature-adoption-observation.json) and prepare [a pending decision](../../playbooks/agentic-engineering-platform/product/released-feature-to-adoption-decision/examples/feature-adoption-decision.json). An issue write, flag change or customer promise needs a separate exact approval and receipt.
- **Later run:** re-read the issue state and a comparable feedback window, retain the theme and issue IDs, distinguish a new signal from repeated records, and reopen the decision only for changed evidence or owner policy.

### Product Discovery Researcher (`product-discovery-researcher`)

- **Use case:** investigate one customer problem across authorized interviews, cases and product observations before choosing a solution.
- **First result:** an opportunity brief with exact source IDs, affected sample, contrary evidence, privacy limits and next research question.
- **Setup proof:** deduplicate linked cases and participants, check consent, cite two dated observations and a counterexample, and reject unsupported prevalence.
- **Later run:** retain opportunity and participant IDs, add new authorized evidence and record how the hypothesis changed.

### Roadmap Prioritization Analyst (`roadmap-prioritization-analyst`)

- **Use case:** compare candidate opportunities against a product goal, agreed rule, current roadmap and capacity.
- **First result:** a decision table with comparable evidence, uncertainty, dependencies, deferred option and separate owner decision.
- **Setup proof:** read current candidate and roadmap revisions, recompute one score or explain why it is unrankable, and verify the owner has not already committed the work.
- **Later run:** preserve candidate and rule versions and reopen only when evidence, capacity or strategy materially changes.

### Product Requirements Coordinator (`product-requirements-coordinator`)

- **Use case:** turn an accepted product problem into scoped, observable acceptance criteria for Design, Engineering and QA.
- **First result:** a requirements brief with source decision, behaviors, edge case, exclusions, open policy questions and signoff states.
- **Setup proof:** trace criteria to the accepted decision and current journey/design, test an edge case, then obtain owner review before any delivery write.
- **Later run:** compare source, design and issue revisions, retain superseded criteria and alert owners to changed scope.

### Product Release Coordinator (`product-release-coordinator`)

- **Use case:** decide whether one exact feature release is ready from the Product side, alongside an independent QA gate.
- **First result:** a go/no-go brief with build and flag, gate, help content, claims, measurement rule, blockers and rollout owner.
- **Setup proof:** read exact build, flag, QA artifact and article revisions; show a non-QA blocker and keep approval, deployment, exposure and adoption distinct.
- **Later run:** re-read each revision before a decision and hand a verified rollout identity to Product Adoption Analyst for separate usage measurement.

## Operations

The [Meeting Decision to Owned Follow-through](../../playbooks/agentic-engineering-platform/operations/meeting-decision-to-owned-follow-through/SKILL.md) Automation Playbook composes Meeting Actions Coordinator → Project Status Reporter with optional Chief of Staff review. The fictional [meeting register](../../playbooks/agentic-engineering-platform/operations/meeting-decision-to-owned-follow-through/examples/meeting-action-register.json) and [project status](../../playbooks/agentic-engineering-platform/operations/meeting-decision-to-owned-follow-through/examples/project-action-status.json) show its handoff; [false completion](../../playbooks/agentic-engineering-platform/operations/meeting-decision-to-owned-follow-through/examples/invalid-project-action-status.json) is rejected.

### Chief of Staff

- **Use case:** prepare a periodic business priorities and decision brief from authorized team records.
- **First result:** dated goals, progress, blockers, decisions needed, owners, due dates, source coverage, and changes since the prior review.
- **Setup proof:** verify goal IDs, reporting period, source access and freshness, decision authority, and one sourced brief with the operator.
- **Later run:** carry goal and decision IDs forward, compare like periods, and avoid repeating resolved or unchanged requests.

### Meeting Actions Coordinator

- **Use case:** turn authorized notes into a reviewed action register.
- **First result:** meeting and notes revision, explicit decisions versus suggestions, source spans, proposed or accepted owners, due dates, duplicate task links, and review state.
- **Setup proof:** inspect one real notes revision and participant map; verify an action span, existing tracker state, and owner acceptance before any task write.
- **Later run:** reuse meeting and action IDs, preserve corrections, and avoid duplicate task creation after revised notes.

### Project Status Reporter

- **Use case:** reconcile project milestones and task records into a current status report.
- **First result:** exact project and as-of time, open/blocked/done/unknown actions, source links and coverage, owner decisions, and next evidence.
- **Setup proof:** read actual tracker records, verify status rules and one completed versus open claim, and review the report with the project owner.
- **Later run:** preserve action history, re-read current task state, and never mark a meeting promise as completed work.

### Order Operations Coordinator

- **Use case:** investigate cross-system order and shipment exceptions beyond a particular commerce platform.
- **First result:** queue with order, fulfillment and carrier IDs, promised time, current provider state, policy rule, owner, prior contact, and reviewed action.
- **Setup proof:** join one authorized order to fulfillment and carrier records, check policy and time zone, and review a late or stuck case.
- **Later run:** re-read payment, shipment and prior-contact state, preserve case IDs, and avoid duplicate refunds, shipments or messages.

### Vendor Researcher

- **Use case:** compare a bounded vendor set against owner-approved buying criteria.
- **First result:** exact product and plan comparison with dated evidence, unknowns, weighted criteria, cost assumptions, risk questions, and owner shortlist.
- **Setup proof:** verify must-haves, budget, vendor scope, current evidence and one comparable calculation with the decision owner.
- **Later run:** recheck changed plans and quotes, preserve scoring rules and earlier owner decisions, and flag expired evidence. [Vendor Evaluation to Purchase Decision](../../playbooks/agentic-engineering-platform/operations/vendor-evaluation-to-purchase-decision/SKILL.md) hands the exact comparison to a distinct procurement review.

### Document Intake Assistant

- **Use case:** extract and validate required fields from authorized incoming documents.
- **First result:** source-linked document/version record with page spans, typed values, validation results, duplicate state, uncertainty and reviewer queue.
- **Setup proof:** verify sample document access, schema and privacy rules, a field against the page, one failed or ambiguous value, and destination authority.
- **Later run:** use document hash and destination ID, retain review corrections, and prevent duplicate writes for unchanged files.

## Engineering

All seven Engineering skill packs now include a fictional source-to-result example, a failed result to reject, and a role-specific source or calculation probe in the existing nine-check setup. These examples define expected output quality; a customer setup still needs one real authorized case and owner review.

### Incident Investigator (`incident-investigator`)

- **Use case:** investigate a production incident across the customer's alerts, telemetry, deploys, and incident records.
- **First result:** dated, sourced timeline with observed impact, labeled hypotheses, gaps, accountable owner, and next decisions. The skill works through a fictional 5xx-rate calculation and rejects an unsupported rollback claim. The existing [incident Workflow](../../playbooks/agentic-engineering-platform/reliability-operations/incident-investigation-coordination/SKILL.md) covers a related route.
- **Setup proof:** read one real alert and associated telemetry or deploy record; verify incident and service IDs, event versus ingestion time, source coverage, escalation policy, and an on-call review.
- **Later run:** append new evidence to the same incident, correct disproven hypotheses, and check action state before notifying or creating another ticket. Recovery actions require a reviewed route.

### Engineering Delivery Coordinator (`engineering-delivery-coordinator`)

- **Use case:** follow a blocked change across issue, PR, CI, deployment, and owner handoffs.
- **First result:** blocker ledger with exact issue and change IDs, commit SHA, CI and deployment states, environment, owner, due time, and the next evidence needed. Its worked example keeps a merged, CI-green PR blocked when production still runs a different SHA.
- **Setup proof:** join one actual issue to a PR, build, and deployment record using stable identifiers; verify release policy, owner, and whether a green build reached the intended environment.
- **Later run:** update the same blocker IDs, distinguish merged from deployed and verified, and avoid duplicate reminders. Merge, CI rerun, ticket write, and deployment need separate authorization.

### Engineering Operations Analyst (`engineering-operations-analyst`)

- **Use case:** reconcile one governed team delivery, quality or reliability measure and pose a bounded improvement question.
- **First result:** [comparable metric observation](../../playbooks/agentic-engineering-platform/engineering-operations-intelligence/engineering-operations-intelligence/examples/engineering-metric-observation.json) with exact population, rule, numerator/denominator, coverage and target; a [partial source](../../playbooks/agentic-engineering-platform/engineering-operations-intelligence/engineering-operations-intelligence/examples/engineering-metric-not-evaluable.json) remains unknown.
- **Setup proof:** freeze team/service and metric policy, join actual source rows by stable IDs, reproduce counts for equal windows, verify coverage and have the engineering owner review the question.
- **Later run:** preserve source and policy revisions, compare only compatible windows, and start a new baseline after a rule or team change. [Engineering Operations Intelligence](../../playbooks/agentic-engineering-platform/engineering-operations-intelligence/engineering-operations-intelligence/SKILL.md) passes the validated observation to Delivery for a separate owner review.

### Post-Incident Reviewer (`post-incident-reviewer`)

- **Use case:** reconstruct a stabilized production incident for a blameless owner review, distinct from active mitigation.
- **First result:** [sourced review](../../playbooks/agentic-engineering-platform/reliability-operations/post-incident-review-actions/examples/post-incident-review.json) with impact arithmetic, dated timeline, classified factors, unknowns and actions; a [draft](../../playbooks/agentic-engineering-platform/reliability-operations/post-incident-review-actions/examples/post-incident-draft.json) remains pending.
- **Setup proof:** verify canonical stability, service/environment, impact counts and two timeline sources from one authorized incident. Have the reviewer decide whether a deploy correlation is a hypothesis or supported factor.
- **Later run:** preserve incident and review revision, source corrections and sensitive-data scope. New facts require a reviewed revision, not a silent rewrite or publication.

### Improvement Follow-Through Coordinator (`improvement-follow-through-coordinator`)

- **Use case:** track exact post-incident actions from owner acceptance through independent control verification.
- **First result:** [improvement register](../../playbooks/agentic-engineering-platform/reliability-operations/post-incident-review-actions/examples/incident-improvement-register.json) linking the accepted action to its issue receipt and passing alert replay while another action stays pending.
- **Setup proof:** bind the exact review/action IDs and owner decision; probe current issue state and an independent verification source. A closed issue without the promised control evidence is [rejected](../../playbooks/agentic-engineering-platform/reliability-operations/post-incident-review-actions/examples/invalid-improvement-register.json).
- **Later run:** re-read issue and control state under stable IDs; retain previous decisions and source revisions. Writes and reminders remain separate authorized routes.

### Performance Investigator (`performance-investigator`)

- **Use case:** investigate a page or API regression using the customer's performance tools and release history.
- **First result:** regression brief with metric definition, comparable baseline/current measurements, coverage caveats, likely bottleneck, owner, and retest plan. The skill recomputes a fictional p95 delta and leaves a database-span correlation as a hypothesis.
- **Setup proof:** inspect a representative trace or measurement, confirm route, environment, percentile, units, traffic segment, baseline and current windows, and budget; review the diagnosis with the owner.
- **Later run:** repeat the same measurement rule, record changed traffic or instrumentation, and close only after a comparable retest. Existing [browser](../../playbooks/agentic-engineering-platform/performance-engineering/browser-performance-validation/SKILL.md) and [API](../../playbooks/agentic-engineering-platform/performance-engineering/api-performance-validation/SKILL.md) Workflows can inform the route.

### Cloud Cost Analyst (`cloud-cost-analyst`)

- **Use case:** explain a cloud bill change and prepare risk-checked savings decisions across billing, usage, ownership, and service data.
- **First result:** reconciled cost-change brief with currency and period rules, service owners, arithmetic, candidate savings ranges, risks, and verification plan. The skill separates a fictional usage-driven increase and one-time charge from a still unverified savings estimate.
- **Setup proof:** read an authorized bill or export, reconcile one change against usage or allocation evidence, confirm discounts and owner map, and review a candidate with the service owner.
- **Later run:** track the same candidate and approval IDs, check actual billed results after a change, and separate estimated from realized savings. The [FinOps Automation Playbook](../../playbooks/agentic-engineering-platform/finops/cost-anomaly-to-verified-savings/SKILL.md) connects this role to Engineering Delivery Coordinator and Finance Analyst with exact change and billed-outcome checks.

### QA

The [Release Candidate to Reviewed Gate](../../playbooks/agentic-engineering-platform/qa/release-candidate-to-reviewed-gate/SKILL.md) Automation Playbook composes the Journey and Gate roles with optional Flaky Test investigation. Its fictional [journey attempt](../../playbooks/agentic-engineering-platform/qa/release-candidate-to-reviewed-gate/examples/journey-result.json), [required-suite matrix](../../playbooks/agentic-engineering-platform/qa/release-candidate-to-reviewed-gate/examples/release-quality-brief.json), and [blocked flake review](../../playbooks/agentic-engineering-platform/qa/release-candidate-to-reviewed-gate/examples/flake-needs-review-gate.json) demonstrate the contract. A real setup still needs an authorized candidate and owner decision.

#### Browser Journey QA Analyst

- **Use case:** run an approved user journey against an exact build and investigate a failure.
- **First result:** an attempt-level pass, fail, or blocked result with build and environment, expected and observed behavior, reproduction steps, and durable video, screenshot, and console/network evidence.
- **Setup proof:** verify a representative authorized test account, canonical journey revision, runner, evidence policy, and real attempt. Review a failure or pass against the expected outcome with the QA owner.
- **Later run:** preserve prior attempts, record changed build or test revisions, and verify the same journey on the named later artifact. A retry never erases a failure.

#### Flaky Test Investigator

- **Use case:** investigate intermittent outcomes for the same canonical test and build.
- **First result:** a controlled attempt comparison with passing and failing evidence, classification with confidence limits, and a reviewable experiment or fix.
- **Setup proof:** bind test, source revision, build, environment, fixtures, concurrency and retry policy; inspect one real history or bounded repeat and review the diagnosis.
- **Later run:** retain earlier attempts, recheck the classification when product or environment evidence changes, and verify any approved stabilization without weakening assertions.

#### Release Quality Assistant

- **Use case:** determine whether a release candidate has complete QA evidence under an agreed gate policy.
- **First result:** a required suite matrix for the exact SHA, build, and environment; a pass, fail, or needs-review proposal; and missing evidence or owner decisions.
- **Setup proof:** verify one real release identity, required suite policy, result source, owner, and status destination. Reproduce a gate decision from the source records before enabling any publication route.
- **Later run:** evaluate each new build independently, preserve previous failures and waivers, and count a published gate only from the destination receipt.

### Security

The [Finding to Verified Remediation](../../playbooks/agentic-engineering-platform/security/finding-to-verified-remediation/SKILL.md) Automation Playbook composes Findings Analyst and Remediation Coordinator. Its fictional [finding](../../playbooks/agentic-engineering-platform/security/finding-to-verified-remediation/examples/security-finding.json), [open ledger](../../playbooks/agentic-engineering-platform/security/finding-to-verified-remediation/examples/security-remediation-ledger.json), and [verified closure](../../playbooks/agentic-engineering-platform/security/finding-to-verified-remediation/examples/verified-security-remediation-ledger.json) show the contract. They do not establish a real customer's scope authorization, deployed state, or retest.

#### Security Findings Analyst

- **Use case:** triage an authorized application, dependency, code, or configuration finding against a named asset and build.
- **First result:** a sourced finding queue with applicability, confidence, customer severity rule, owner, remediation options, and a verification criterion. Scanner output alone remains unconfirmed.
- **Setup proof:** record written scope, allowed methods, stop conditions, source and evidence restrictions; inspect one real finding and affected build with the asset owner.
- **Later run:** reconcile stable finding and asset IDs, check the deployed version and prior decision, and avoid duplicate tickets or unsupported closure.

#### Access Review Analyst

- **Use case:** compare approved role, ownership, and tenant policy with observed application behavior.
- **First result:** an actor-by-resource-by-action matrix with expected and observed UI and server results, exact build, evidence, and exceptions.
- **Setup proof:** verify the policy revision, authorized test actors and isolated fixtures, target build, direct-route rules, and one safe matrix cell with the decision owner.
- **Later run:** repeat the same denied cells after a reviewed fix, preserve older attempts, and distinguish a changed policy from a changed application.

#### Security Remediation Coordinator

- **Use case:** follow a validated finding through approved change, deployment, independent retest, and closure.
- **First result:** an action ledger joining finding, issue, change SHA, deployment, approval, retest, owner, and disposition. A merged fix without deployed retest remains open.
- **Setup proof:** verify the finding source, approved fix or risk-acceptance policy, owner, deployment source, sensitive evidence handling, and one real remediation state.
- **Later run:** re-read exact finding, change, deployment, and retest state; preserve failed checks and risk expiry; close only with owner decision and evidence from the affected environment.

## GTM

### GTM Strategy Analyst

- **Use case:** choose an evidence-backed first audience, message, channel hypothesis, and pipeline measurement rule for a B2B offer.
- **First result:** a reviewed launch brief with offer version, approved claims, buyer problem and sources, channels and budget, owner, qualified lead definition, and open decisions.
- **Setup proof:** verify offer claims, representative customer or market evidence, audience and market, measurement source, baseline or baseline-first decision, and owner approval of one real brief.
- **Later run:** compare new evidence with the recorded hypothesis and metric rule; retain prior decisions and avoid claiming lift from a single campaign result.

### Launch Coordinator

- **Use case:** carry an approved brief through channel assets, owner decisions, provider observations, and inbound lead signals.
- **First result:** an action ledger with asset and campaign IDs, approval versus publication or send state, provider receipt, event and lead IDs, deduplication, attribution confidence, and next owner action.
- **Setup proof:** inspect real asset revisions, approved channels, budget, contact policy, lead and CRM sources, and one provider or public observation. Review exact launch and lead joins with owners.
- **Later run:** reconcile stable asset, campaign, event, and lead IDs; preserve approvals and receipts, recheck contact state, and avoid duplicate distribution or follow-up.

### Revenue Operations Analyst (`revenue-operations-analyst`)

- **Use case:** reconcile campaign, form, CRM lead, account and opportunity records for a shared GTM measurement decision.
- **First result:** a stage-count and attribution brief with deduplicated IDs, join coverage, model version, source limits and data-quality actions.
- **Setup proof:** read real source and CRM revisions, show one duplicate and unmatched event, recompute a transition and reject false opportunity or revenue claims.
- **Later run:** reprocess stable IDs idempotently, allow documented lag and compare only like attribution and stage rules.

### Sales Enablement Coordinator (`sales-enablement-coordinator`)

- **Use case:** keep persona-specific seller guidance aligned with approved offer, proof, product behavior and current price.
- **First result:** an enablement brief with claim versions, objection evidence, prohibited claims, owner approval and expiry check.
- **Setup proof:** compare a real deck with current offer and price revisions, two dated objection records and one consented proof artifact; keep distribution pending review.
- **Later run:** revalidate claims and pricing, retire stale assets and preserve the approved version.

### Pricing & Packaging Analyst (`pricing-packaging-analyst`)

- **Use case:** compare SaaS plan entitlements, buyer fit, unit economics and contract exceptions before an offer decision.
- **First result:** a decision memo with exact price and entitlement revisions, cost assumptions, protected terms, owner options and unresolved mismatches.
- **Setup proof:** reconcile public, billing and entitlement sources for a real plan, recompute one economic assumption, identify one contract exception and ask the pricing owner to review.
- **Later run:** preserve plan, contract and policy revisions; a proposed package never changes a price or customer term automatically.

## Shopify

### Store Operations Coordinator (`store-operations-coordinator`)

- **Use case:** work an order exception across Shopify, fulfillment, carrier, inventory, and support records.
- **First result:** an order exception queue with exact store, order, line-item and fulfillment IDs, current states, customer deadline, owner, and next evidence. See the [illustrative order artifact](../../playbooks/agentic-engineering-platform/shopify/order-exception-to-resolution/examples/store-order-exception.json).
- **Setup proof:** read one actual authorized order and matching fulfillment or support record; verify store identity, policy, owner, partial fulfillment, and whether a label, shipment, or delivery was truly observed.
- **Later run:** re-read the same order and prior contact/refund state, preserve case IDs, and avoid duplicate follow-up, cancellation, or refund work.

### Returns & Refunds Coordinator (`returns-refunds-coordinator`)

- **Use case:** review a merchant's return or refund request using order, payment, delivery, prior-refund, and policy records.
- **First result:** [policy and money review](../../playbooks/agentic-engineering-platform/shopify/order-exception-to-resolution/examples/return-resolution-review.json) with eligibility, amount-to-verify, unknowns, owner decision, and an unsent customer draft.
- **Setup proof:** verify one actual store/order/customer match, captured and already-refunded amounts in one currency, current dispute and return state, policy version, prior messages, and approval owner. A draft or approval is not an issued refund.
- **Later run:** re-read transactions and contact state immediately before another action; retain one stable case/action ID; count refunds and messages only from provider receipts.

### Catalog & Merchandising Analyst (`catalog-merchandising-analyst`)

- **Use case:** inspect product and variant quality, availability presentation, collection placement, and search/navigation gaps.
- **First result:** exact product/variant issue queue with observed storefront effect, source, proposed edit, owner, and retest.
- **Setup proof:** inspect a bounded catalog export or authorized store plus actual public storefront, confirm market, collection, catalog rules and inventory authority, and review one issue with the merchant.
- **Later run:** retest the same product and variant IDs after approved edits, preserve deliberate merchant choices, and avoid claiming sales impact without measured evidence.

### Shopify Growth Analyst (`shopify-growth-analyst`)

- **Use case:** connect store discovery, product page, cart, checkout, and order evidence to bounded growth actions.
- **First result:** a sourced growth brief with audience and market, funnel or page observations, denominators and date range where measured, ranked actions, and a verification plan.
- **Setup proof:** inspect the storefront and one representative authorized analytics report if making performance claims; agree on market, currency, timezone, conversion action, attribution limits and owner.
- **Later run:** check what actually shipped, compare the same segment and metric rule, and report inconclusive results when traffic or instrumentation is inadequate.

### Payment Operations Investigator (`payment-operations-investigator`)

- **Use case:** investigate a Shopify order's authorization, capture, pending/failure, void, or refund transaction and its effect on the fulfillment decision.
- **First result:** a payment exception with exact order and transaction IDs, kind, status, parent, presentment currency and amount, owner, and next evidence. See the [authorization-only example](../../playbooks/agentic-engineering-platform/shopify/payment-exception-to-order-decision/examples/payment-exception.json).
- **Setup proof:** read a real authorized OrderTransaction and matching order, including kind/status, parent, amount/currency, capture policy, and owner. An authorization is not successful capture; a Refund object alone does not establish that the money arrived.
- **Later run:** re-read the transaction and order before any retry or release decision, preserve a stable case/action ID, and stop a stale proposal when a later capture, void, dispute, or refund changes the state.

### Checkout Recovery Coordinator (`checkout-recovery-coordinator`)

- **Use case:** review one abandoned checkout for a permitted, unduplicated recovery contact; keep a separate owner and provider action route.
- **First result:** a checkout-level decision with consent, later-order, suppression and prior-send evidence, or an unsent draft when all gates are clear. See the [review example](../../playbooks/agentic-engineering-platform/shopify/checkout-signal-to-reviewed-recovery/examples/checkout-recovery-review.json).
- **Setup proof:** read a real authorized checkout and current consent/order/provider history; demonstrate one suppressed or unknown case, then obtain an owner review of any draft.
- **Later run:** re-read completion, order, consent, opt-out and prior sends using a stable checkout-channel-campaign key; stop on recovery or duplicate-send risk.

### Replenishment Planner (`replenishment-planner`)

- **Use case:** decide whether a specific supplier SKU should be reordered for one Shopify InventoryItem and destination Location.
- **First result:** a sourced demand and supplier review with integer target, shortage, case-pack/MOQ rounded quantity and cost, or a named missing-input decision. See the [reorder example](../../playbooks/agentic-engineering-platform/shopify/inventory-risk-to-reviewed-replenishment/examples/replenishment-review.json).
- **Setup proof:** recompute one real item/location proposal using current available, incoming, open PO, demand and supplier terms; demonstrate a missing-data or duplicate-order case and owner decision.
- **Later run:** re-read location quantities, open POs/transfers, demand and terms before proposing again. A PO ordered is not stock received; verify the linked transfer and later InventoryLevel separately.

## Content completion rule

These profiles make the **jobs and setup evidence** explicit. The installed skills and guides include fictional good/rejected outputs and source probes. They are not substitutes for actual output evaluation. Before an agent appears as a fully demonstrated public template, run its probe with authorized customer data, review its first result, and exercise a repeat case. The existing Playbook contract suites cover selected packaged handoffs and should be linked from the relevant agent page when that work is done. Track planned roles in the [main catalog](crew_template_catalog.md); do not add them to this installable list until the Crew picker and Builder can install them.
