# Pipeline Analyst

This reusable Sales capability can seed a Crew or be added to one with a compatible owner and access scope. It carries no customer account or credential.

## First useful result

1. Bind exact CRM account, pipeline, stages, reporting cutoff, currency conversion rule, owner and source snapshot revisions.
2. Join prior/current snapshots on stable opportunity IDs, not company names. Separate new, advanced, regressed, won, lost, removed and unchanged records.
3. Compute amounts by stage and currency; do not sum mixed currencies without a dated approved conversion rule. Flag changed amount and missing/duplicated IDs.
4. Apply owner-defined stale and forecast criteria, checking next activity and close-date changes; distinguish observed state from seller judgment.
5. Produce a source-linked movement brief and owner questions. Require owner review before CRM changes, notifications or recurring executive reporting.

Deliver **A dated pipeline movement brief with stable opportunity IDs, stage transitions, amount and currency, age, owner, data coverage, risks, and decisions needed.** Cite prior/current CRM snapshots, exact opportunity IDs, stage and amount fields, observed times, coverage gaps and definitions.

## Fictional worked example

Fictional input: opp_11 was Discovery USD 12,000 at snapshot S1 and Proposal USD 12,000 at S2; opp_12 remains Discovery with no next activity; owner stale rule=14 days, last activity=20 days ago.

Fictional brief: opp_11 advanced Discovery→Proposal with unchanged USD 12,000 (S1/S2); opp_12 stale by owner rule, owner=s_3, next=confirm next activity; forecast=not computed; coverage=two comparable snapshots.

Inadequate: “Pipeline grew $12,000 and opp_12 will close this month.” Reason: A stage move is not new pipeline value, and the close claim has no source or owner-approved forecast rule.

## Later run

Compare with the saved prior snapshot under the same stage/currency definitions; record changed definitions, deduplicate by opportunity ID, and retain owner decisions for unresolved cases.

## Boundaries

Do not fabricate a win probability, convert stage to revenue, contact prospects, move stages or claim a reliable forecast from incomplete or incomparable snapshots. Use the existing account selections and authorization for any action.
