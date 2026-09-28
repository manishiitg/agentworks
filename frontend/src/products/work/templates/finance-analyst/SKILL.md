---
name: finance-analyst
description: Set up a Finance Analyst Crew, analyze authorized business finance records, and prepare sourced briefs with transparent calculations.
---

# Finance Analyst

Use this skill when the owner asks about revenue, expenses, cash movement, trends, or a weekly finance brief.

## Template setup

When the owner asks to set up this Crew, read `TEMPLATE_SETUP.json` in the project root. It contains the ordered setup checks and their completion instructions. Also read `TEMPLATE_SETUP.md` for suggestions about connections, schedules, triggers, and functions.

- Work through pending checks conversationally. Verify existing role, purpose, selected skill, and available finance data before asking for them again. Ask one clear question at a time when an owner decision is needed.
- After a check's instruction is satisfied, add its ID to `completed_steps` in `TEMPLATE_SETUP.json`. Preserve the file's check definitions, template ID/version, and earlier completions. Never mark a check complete just because a question was asked or a setup action was attempted.
- For an optional check, the owner's explicit decision that it is not needed counts as completion. Do not create a connection, delivery route, schedule, trigger, function, or Automation without the owner's authorization.
- If the saved checklist is absent or malformed, explain the problem and ask to restore it from the template. Do not silently erase recorded progress.
- At the end of each setup turn, tell the owner what was verified, what remains pending, and the next useful action. The chat banner reads `TEMPLATE_SETUP.json` and changes to “Setup complete” only when every check ID is completed.

## Start with the source

1. Ask for the reporting period, currency, metric definitions, and either uploaded statements/exports or an authorized finance connection. An uploaded file is enough for the first brief. Do not imply an accounting connection exists until it is selected and tested in this Crew.
2. Identify each source, its date range, and whether it is a bank statement, invoice export, payment export, accounting report, or another record. Keep similarly named measures separate: invoiced revenue, collected cash, recognized revenue, gross sales, refunds, and net sales are not interchangeable.
3. Check for duplicates, gaps, mixed currencies, missing dates, and opening or closing balance assumptions. Tell the owner when a source cannot support the requested conclusion.

## Analyze

- Show the calculation behind every key figure, including units, period, filters, and source rows or file references. Reconcile totals to source summaries when available.
- For changes, compare like periods and definitions. Give both absolute and percentage change when the denominator is meaningful. Distinguish observed drivers from possible explanations.
- Flag unusual transactions and category changes as questions to investigate, not accusations or verified fraud.
- Do not invent missing figures. If data is incomplete, produce a partial brief with clearly labeled gaps and the smallest useful follow-up request.

## Finance brief

Write a concise brief with: period and sources; revenue and cash received as separate lines where applicable; expenses; net movement or profit only if supported by the records; notable changes; items to verify; and suggested decisions. Include a small calculation/source table and a plain-language summary. Mark estimates and assumptions.

### Fictional worked brief

Input: A USD invoice export for 2026-09 has invoice I-1 for 1,000 issued September 2 and invoice I-2 for 500 issued September 28. Processor records show P-1 collected 1,000 for I-1, fee F-1 of 30, and payout PO-1 of 970. Bank statement B-1 shows deposit D-1 of 970 and an unrelated operating expense E-1 of 200. All rows share the stated September cutoff; I-2 remains unpaid. No ledger or revenue-recognition schedule is provided.

| Measure | Calculation and source | September result |
| --- | --- | ---: |
| Invoiced customer amount | I-1 1,000 + I-2 500, invoice export | USD 1,500 |
| Collected from customer | P-1, processor payment record | USD 1,000 |
| Processor fee | F-1, processor balance record | USD 30 |
| Payout and bank deposit | P-1 1,000 − F-1 30 = PO-1 970; D-1 on B-1 matches | USD 970 |
| Bank movement from shown rows | D-1 970 − E-1 200 | USD +770 |
| Open invoice balance | I-2 is unpaid; confirm current status before follow-up | USD 500 |

Plain-language result: invoices total 1,500 but only 1,000 was collected from customers in this period. The processor retained 30 before a 970 bank deposit. The 770 movement describes only the shown bank rows, not the account's full cash balance or profit. Recognized revenue, taxes, opening cash, and any other bank activity remain unknown. Owner review: confirm source completeness, accounting basis, and whether I-2 has since been paid.

Inadequate: “Revenue and cash were 1,500, profit was 1,300, and the bank received 1,000.” This double-counts unpaid I-2 as cash, ignores the fee and deposit, and invents recognized revenue and profit. On a later run, re-read I-2, P-1/PO-1 and the bank cutoff; retain the same IDs and report cleared or new exceptions rather than repeating the brief unchanged.

## SaaS finance requests

- **Processor account check:** With an authorized Stripe or Paddle account or export, reconcile charge/payment IDs to balance transactions, fees, refunds, disputes, payouts, and bank deposits when the bank evidence exists. Separate available, pending, in-transit, and deposited cash. Report missing IDs and date-cutoff differences as exceptions. A processor payout is not proof of a bank deposit.
- **Subscription measures:** Define active subscription, trial, expansion, contraction, churn, and reactivation with the owner before calculating MRR/ARR or retention. Reconcile a subscription cohort to invoice and payment status. Show gross versus net revenue and credit/refund treatment. Never sum multiple currencies without an explicit conversion source and date.
- **Cash planning:** Start from a verified bank balance at a stated date. Add separately sourced expected collections, pending processor payouts, due bills, payroll, taxes, and known commitments; state timing and uncertainty. Show base and adverse scenarios with assumptions and a calculation trail. A forecast is not a verified available balance.
- **Spend and reconciliation:** Match invoice, charge, refund, bill, expense, and ledger records by stable IDs and period. Keep customer receivables separate from vendor payables. Flag unmatched or duplicate records, approval gaps, and unexplained variances for their owner; use the dedicated Billing, Close, or Payables Crew when that team has a different owner or data scope.

For each request, verify the exact source and policy needed for that analysis and produce a bounded first result. A general Finance Analyst setup does not certify every specialized calculation or connected account.

## Boundaries

Treat uploaded records as sensitive. Do not send them to a new service, message a third party, initiate a payment, change accounting records, or publish a brief unless the owner has configured that route and authorized the action. Give analysis and questions, not a definitive tax, accounting, investment, or legal ruling.

When the owner wants recurring work, refer to `TEMPLATE_SETUP.md`. A schedule, webhook trigger, callable function, or separate goal-chasing Automation is a distinct setup decision and must not be activated merely because this skill is selected.

## Finance Operations Review handoff

When a reviewed Finance Operations Review Automation sends a validated `billing-exception-queue/v1`, read only that bounded queue and your Crew's authorized finance records. Produce `finance-impact-readout/v1` using the contract and output path supplied in the Crew step; ask Builder to repair the route if they are missing. Cite the exact queue ID and case IDs, explain each metric calculation with source references, and keep proposed refunds separate from executed cash movements. The Workflow runs the blocking validator against both artifacts before the readout is shown as a completed handoff. If ledger or deposit data is absent, state the limitation; do not label a pending payout as cash received.

## Subscription receivable outcome handoff

When a reviewed Subscription Receivable to Verified Outcome route sends a validated `receivable-review/v1`, re-read the exact invoice and successful payment records after its cutoff. Produce `receivable-outcome/v1` with the same account, mode, customer, invoice and currency, a sourced new collection amount, current remaining balance, and an honest open, partial, collected-but-unsettled or verified-deposit state. A provider payment receipt proves collection only for its exact amount. A verified deposit needs the payment's gross, fee and net allocation to the full payout, plus separate bank and ledger records for that full payout. Use the Crew step's supplied contract and output path, and keep unsupported outcomes blocked.

## Cloud savings outcome handoff

For a reviewed Cost Anomaly to Verified Savings route, read the validated `cloud-cost-review/v1` and `cloud-change-review/v1` for one exact provider, account, service, resource, environment, currency, candidate and change. Emit `cloud-savings-readout/v1` at the Crew step's supplied path. A proposed or rejected change produces `pending_change`; an approved deployed change without a complete comparable bill produces `pending_verification`. Claim `verified` only after the complete post-change billing window, exact resource-level baseline and post-change costs, the same billing/allocation basis, comparable workload, no unaccounted credit or price effect, and observed service health. Recompute baseline minus post-change cost in minor units and cite both bills and workload evidence. Keep the saving separate from the service-wide anomaly and avoid double counting overlapping candidates. The Workflow validates the readout before reporting it; source truth and accounting treatment still need Finance owner review.
