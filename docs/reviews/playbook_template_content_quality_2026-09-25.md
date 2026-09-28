# Authored Playbook and agent-template quality review

Date: 2026-09-25

## Scope and verdict

This review assesses the instructions we authored: usefulness, specialist depth, setup questions, output definitions, composition, and repeatability. It covers all 12 currently available Crew templates (Finance Analyst, Tax Export Preparer, Website Growth Starter, and nine growth specialists), plus Website Growth Loop. Existing SEO Intelligence, AI Visibility Intelligence, and Growth Experimentation references were inspected for comparison and reusable material. This is not a full review of all 24 Workflow Playbooks.

The templates have credible purposes, useful minimum-input descriptions, and strong guidance against unsupported claims. Their maturity is uneven. Finance Analyst and Website Growth Starter are the strongest starting points. Most newer specialists are concise role procedures that still need worked outputs, deeper domain methods, and meaningful pass/fail criteria. Website Growth Loop is a reasonable discovery-and-planning pilot; its authored process does not yet fully specify a sustained growth cycle.

These are content-review judgments, not measured agent performance scores. No real-site or real-financial-data evaluation was run in this pass. No template content was changed by this review.

## Follow-up implementation

Later on 2026-09-25, Website Growth Starter and Search Opportunity Mapper gained decision rules, worked examples, and repeat-run guidance. The Mapper gained two specialist checks. Website Growth Loop v0.3.0 now uses Mapper for the required buyer-question handoff, has a separate optional technical SEO slot, tracks action states, and includes a deterministic validator plus valid and invalid examples for the first two artifacts. The other eight growth specialists and the two finance templates remain at the content depth assessed below. The validator checks artifact structure and references; a customer-site run and source-truth review still need to be demonstrated.

On 2026-09-26, the other eight Website Growth specialists gained domain-specific decision rules, a fictional input and reviewable output with a failed example, and two checks for their hardest task. Their generated skills and setup guides now carry those examples; each specialist has ten pending checks. This closes the authored-example and generic-checklist gaps for Website Growth, but it does not establish a real customer-site evaluation, live source truth, or operational readiness. The older findings below remain the dated review record.

Website Growth Loop v0.4.0 now binds the strategist and search artifacts by exact IDs and validates the selected optional search → content brief → page draft chain. The new fixtures reject an unapproved, low-confidence question and a draft that uses an unverified claim or claims publication. This is an authored contract improvement; Builder still must insert blocking validator steps and test them on customer data.

Website Growth Loop v0.5.0 adds Website Publishing Coordinator and optional page → verified ship → distribution and measurement handoffs. The fictional fixtures and executable validator distinguish approved draft, publication receipt, live-page verification, unsent channel plan, delivery receipt and comparable traffic readout. Tests reject an unapproved publication claim, invalid distribution and misleading traffic comparison. This improves the authored library; it does not prove a real provider integration or customer outcome. Builder must wire blocking checks to actual run files and a reviewer must inspect underlying source truth.

Finance Analyst now includes a worked invoice/payment/payout/bank calculation that keeps invoiced, collected, deposited and recognized amounts separate. Its setup checks require source row, basis, sign, FX, cutoff and reconciliation decisions. Tax Export Preparer now includes a fictional CSV, signed refund/fee normalization, deposit-transfer deduplication, reconciliation and an explicit classification exception. Its setup checks require recipient format and professional review. These are authored examples, not validated customer accounting or tax results.

## Most important content findings

### 1. Specialist checklists repeat the same structure without enough specialist verification

All nine generated growth specialists receive the same eight checks: identity, skill, scope, inputs, access, first result, review, recurrence. Their minimum inputs and evidence sentences vary, which is useful, but the checks do not independently prove the specialist's most difficult work.

Examples of missing specialist checks:

- Search Console Optimizer: verify compatible dimensions/filters, query coverage, aggregate calculations, and a reproduced recommendation from source rows.
- AI Visibility Analyst: freeze the exact question set and engine surface, distinguish failed/unavailable observations from absent mentions, and test repeatability under an explicit sampling policy.
- Content Page Builder: confirm factual review and inspect the rendered preview, mobile behavior, links, and target action when an implementation is delivered.
- Traffic Analyst: reproduce one metric, verify event meaning and denominator, and test the no-baseline/incomplete-data response.
- Distribution Coordinator: validate one real channel against audience fit and its rules, and review one complete channel-specific draft and tracking record.

Keep shared identity/access checks, then use the remaining checks for these domain-specific acceptance conditions. Optional recurrence can be decided after the first useful result without repeating broad onboarding questions.

Source: [specialist generator](../../frontend/src/products/work/websiteGrowthSpecialists.ts), especially `checklist`, `skill`, and `guide`.

### 2. Output descriptions are useful but lack inspectable worked examples

The 12 Crew packs contain skill/setup instructions and a checklist. They do not ship a worked input/output example for the promised deliverable. Website Growth Loop's example is a fictional run receipt containing `validated: true`; it does not show the actual strategist brief, opportunity map, failed handoff, or a useful final business report.

For each pack, add one small fictional input, a complete good output, and a deficient output with reasons it fails review. Specify artifact name, fields/sections, evidence format, minimum useful scope, and handling of missing inputs. The goal is consistency of judgment, not forcing every customer into identical prose.

### 3. The search-slot substitution is semantically incorrect

Website Growth Loop permits Search Opportunity Mapper or SEO Analyst in the same slot and expects `search-opportunity-list/v1`, defined as buyer questions, intent, existing page/gap, evidence, and next action. SEO Analyst is authored to produce technical issues with impact, effort, and retests. Those deliverables have different purposes.

Use Search Opportunity Mapper for the buyer-opportunity route. Make technical SEO a separate optional route/slot with its own issue contract, or explicitly teach and verify the additional opportunity-mapping capability before allowing substitution. A similar name or category is insufficient evidence of compatibility.

Sources: [specialist methods](../../frontend/src/products/work/websiteGrowthSpecialists.ts), [Loop team and contracts](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/references/team-and-handoffs.md).

### 4. The Loop needs an explicit route from recommendations to shipped work

The required route is strategist → search → combined action review. Optional additions are content briefs and measurement. There is no authored publication/change-record producer, yet the content-to-measurement edge expects `shipped-change/v1`.

Specify the full cycle: select an opportunity → approve a bounded change → draft/build → verify → record actual publication/change → distribute where appropriate → observe an agreed window → retain/iterate/stop. Human execution is valid: an owner can supply a verified change record. The Playbook must distinguish proposed, approved, drafted, shipped, and measured work.

Content Page Builder and Distribution Coordinator already exist as templates but are not composed into this route. Offer them only when the chosen plan needs them. Content generation and analytics access should not be requirements for a purely technical-fix route.

Source: [Loop manifest](../../playbooks/agentic-engineering-platform/website-growth/website-growth-loop/playbook.json).

### 5. Repeat runs are underspecified

The specialists primarily describe their first useful result. Their shared recurrence guidance says to set up and test a schedule or Automation, but does not define what a second run should read, update, defer, or avoid repeating.

Add a compact repeat-run procedure: load prior findings and decisions; identify new inputs or shipped changes; update stable action IDs; preserve deferred/rejected decisions; avoid reissuing the same recommendation; wait for the agreed measurement window; report “no new evidence” when appropriate. The strategist should consume specialist evidence and own prioritization, so repeated runs do not all recrawl and re-audit the same site.

The older [Growth Experimentation workflow](../../playbooks/agentic-engineering-platform/growth-analytics/growth-experimentation-follow-through/references/experimentation-workflow.md) already provides useful concepts: frozen hypotheses, owner/action state, delivery receipts, guardrail metrics, and inconclusive outcomes. Reuse a small-business-sized subset.

### 6. Prioritization and confidence need explicit meaning

Several templates say to rank by impact, effort, confidence, relevance, or gap severity without defining those judgments. Results may look polished while the ordering changes arbitrarily between runs.

Add a simple qualitative rubric with worked examples. For example, distinguish a directly observed broken conversion path from a plausible content opportunity with unknown demand. Explain the ranking and preserve the owner's override. Avoid invented traffic forecasts or numeric scores that imply unsupported precision.

### 7. MCP/skill and reusable-action guidance is uneven

Finance Analyst supplies a concrete proposed `analyze_finances` input/result schema and example schedule/trigger instructions. Website Growth Starter names a proposed `site_audit` function and event fields. The nine specialists mostly list optional connection names and generic recurrence guidance.

For each specialist, define the actual capability needed, a successful probe, the file/manual fallback, and the limits of that fallback. For example, screenshots can support a copy review but do not prove an HTTP redirect or indexing state. If a callable function is offered, include its input/result definition and example call. Keep connections optional only when the advertised first result can still be delivered.

## Template-by-template assessment

| Template | Current strengths | Main content work needed |
| --- | --- | --- |
| Finance Analyst | Distinguishes cash, invoiced and recognized amounts; asks for source reconciliation; includes concrete function proposal. | Worked calculation fixture; explicit accounting basis, period boundaries, sign/FX treatment and tolerance decisions; tie each reported figure to a reproducible calculation. Clarify behavior when balance/source totals do not reconcile. |
| Tax Export Preparer | Clear recipient-format, classification, traceability and exception focus. | Exact sample export and field mapping; normalization and duplicate rules; reconciliation summary; rounding/sign/currency decisions agreed with the recipient. No tax-rule automation should be inferred from this pack. |
| Website Growth Starter | Good new-site fallback, bounded inspection, source-linked plan and baseline guidance. | Finished example brief, prioritization rubric, conditional site/business routes, and a second-run procedure. Narrow strategist work when specialists already supplied evidence. |
| SEO Analyst | Good distinction between public observations and indexing evidence. | Reproducible technical checklist, rendered-vs-source inspection guidance, precise issue severity/retest examples, and explicit unsupported-check reporting. |
| Search Opportunity Mapper | Buyer intent and existing-page mapping are useful and differentiated. | Research method/source quality, opportunity clustering and deduplication, existing-page-update versus new-page criteria, and ranked example map. |
| Content Brief Writer | Requires approved opportunity, attributable claims and internal links. | Complete brief example, research/source selection method, unique value and duplication checks, and a clear writer handoff. |
| Content Page Builder | Approved-brief input and reviewable draft output are well bounded. | Clarify whether each run delivers copy or an implemented page; provide a concrete review/preview acceptance checklist and a change-record handoff. |
| Search Console Optimizer | Compatible periods/dimensions and average-position caveats are present. | Query coverage/aggregation checks, correct aggregated CTR, sparse-data handling, comparison method and a worked page recommendation. |
| Traffic & Engagement Analyst | Denominators, instrumentation changes and causality caveats are strong. | Metric calculation examples, event verification, traffic segmentation policy and a complete baseline-first/insufficient-data report. |
| AI Visibility Analyst | Records question, engine, time and citation; acknowledges stochastic answers. | Exact surface/version/session controls, repeated sampling, error versus absence handling, citation matching rules and comparison policy. Treat as experimental until this is demonstrated. |
| Landing Page Optimizer | One journey, a bounded hypothesis and a decision rule fit the task. | Structured diagnosis, exact revision example, guardrail metric, and a low-traffic route that does not default to an impractical A/B test. |
| Content Distribution Coordinator | Requires a published asset and channel fit; prepares drafts for review. | Concrete channel-selection procedure, campaign/message examples, tracking and deduplication conventions, and outcome-to-next-action guidance. |

## Domain reference checks

Google documents that Search Console query rows can omit anonymized queries and that chart/table totals can differ. The optimizer's generic evidence sentence should be expanded to handle this explicitly; otherwise a partial export could be interpreted as complete demand data. [Google Search Console data filtering and limits](https://developers.google.com/search/blog/2022/10/performance-data-deep-dive).

Google's guidance also connects visibility in its generative Search features to established SEO practices. The AI Visibility agent should avoid prescribing unproven special formatting or treating one sampled absence as evidence that a site needs an entirely different optimization strategy. Its existing caution about stochastic answers is a good starting point. [Google generative AI Search optimization guidance](https://developers.google.com/search/docs/fundamentals/ai-optimization-guide).

## Content quality bar for the next revision

Each pack should have:

1. A precise job and a clear decision about when another specialist is needed.
2. Minimum inputs, source-quality rules, and honest fallback/blocked paths.
3. A domain-specific method with decision rules and examples.
4. An inspectable output contract and complete fictional worked example.
5. Five to ten checks that verify the actual job, including a reproduced first result.
6. Saved customer decisions, evidence locations, and repeat-run behavior.
7. Concrete optional capability/function/recurrence recipes where relevant.
8. A few evaluated cases: normal input, incomplete input, conflicting evidence, and a repeat run.

For Website Growth Loop, additionally require compatible producer/consumer outputs, routes for different useful actions, an actual shipped-change record, and a complete illustrative discovery-to-readout cycle. Content quality should be evaluated using those outputs before expanding the catalog.
