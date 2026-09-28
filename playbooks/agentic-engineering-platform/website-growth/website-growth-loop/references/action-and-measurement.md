# From a growth proposal to a measured change

The two-Crew first route ends with owner-reviewed actions. Further work is chosen from the actual opportunities, data, and customer capacity. Keep an action ledger with stable IDs, evidence references, owner, next check date, and one of these states: `proposed`, `approved`, `drafted`, `shipped`, `measured`, `deferred`, or `rejected`. Record state changes rather than replacing history. An owner may do the work outside AgentWorks and provide a verified change record.

## Choose the right route

| Finding | Next work | Required proof |
| --- | --- | --- |
| Existing page leaves an important buyer question unanswered | Owner approves an update; optionally use Content Brief Writer and Content Page Builder. | Approved question, factual sources, reviewed draft, then actual page URL and publication evidence. |
| Distinct buyer question with a credible source and no suitable page | Owner reviews whether a new page is justified; use content roles if accepted. | Reviewed topic/claim sources, approved draft, actual published URL. |
| Crawl, canonical, metadata, or link issue | Use SEO Analyst in its own technical route; pass the issue to the site owner or implementer. | Observed technical state, approved change, retest of the affected URL. |
| Conversion path confusion | Use Landing Page Optimizer for a bounded diagnosis or revision. | Page observation, agreed visitor action, reviewed change and appropriate measurement method. |
| Published asset needs distribution | Use Content Distribution Coordinator only for authorized channels. | Published URL, reviewed channel draft and delivery receipt; no automatic outreach. |
| No comparable traffic history | Start a baseline from an agreed date and metric definition. | Property/export and event check when available, or explicit data gap. |

## Record a shipped change

A Content Brief Writer output is a proposal; a Page Builder output is a draft. The optional Website Publishing Coordinator binds an **approved** page artifact to `shipped-change/v1`: stable action ID, target URL, owner approval, provider receipt, publication time, live revision and inspected live result. Keep `pending` until each is verified. A reviewed pull request without a release stays `drafted`. For manual publication, capture the owner's provider evidence and inspect the live page. If verification cannot be done, keep the action unshipped. The [publication contract](team-and-handoffs.md#optional-publication-distribution-and-measurement-handoffs) has a worked example and a rejected false ship.

After a verified ship, the Content Distribution Coordinator may prepare `distribution-plan/v1` for approved channels. Preserve one distinct tracking key per channel and its audience fit, rules, exact draft, owner and decision. `unsent` is the honest default; `sent` needs a separate approval and provider receipt. Carry these channel states into the action ledger so a plan does not become a delivery claim.

## Measure honestly

Measurement is optional for the first route and requires actual access and event definitions. For a new site, record the metric and first reliable collection date; use `baseline_first` and an inconclusive decision until a comparable window exists. Preserve the same property, population, timezone, filters, and event definition across comparisons. For the `traffic-readout/v1` path, use equal-duration prepublication and postpublication windows and report only after the latter closes. Show numerator and denominator for action rates, sample size, data gaps, and any instrumentation changes. Search Console clicks and analytics sessions are different measures. A change after publication is an observation; do not assert causality without an appropriate test and enough data. Validate the readout against the exact shipped change and, if selected, distribution artifact.

An action may be `measured` with an inconclusive result. Record that outcome and the next decision: keep, adjust, defer, or stop. Do not relabel an inconclusive readout a win.

## Repeat run

Before a scheduled or manual repeat, load the owner goal, prior artifacts, action ledger, decisions, source dates, and baseline state. Recheck permissions and source freshness. Inspect changed pages and new evidence first. Update existing action IDs and explain deltas. Preserve rejected/deferred reasons. If no work shipped or no comparable metric window exists, report no new evidence and a next check date instead of generating a fresh-looking audit. New opportunities require new evidence and an owner review. If an input or handoff contract is missing, leave that route blocked and report the exact reason.

Configure recurrence only after the owner reviews the concrete route, source access, cadence, timezone, cost, retries, notification policy, and a successful manual run. A recurring check must not publish, message third parties, or activate a new Crew capability implicitly.
