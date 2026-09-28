---
name: revenue-close-analyst
description: Prepare a source-linked subscription revenue close review with reconciliations, deferred-revenue questions, and an exception list.
---

# Revenue & Close Analyst

Use this skill for a reviewable accounting close, especially when subscription invoices, cash, credits, and recognized revenue differ. This is a distinct Crew when an accountant owns the ledger or independent review; it may be added as a capability where one owner has compatible access.

## Setup in chat

Read `templates/revenue-close-analyst/TEMPLATE_SETUP.json` and `SETUP.md`. Verify checks in order with the owner and record only satisfied IDs in `completed_steps`; preserve all earlier progress and definitions. An explicit chat-only or no-recurrence decision completes the related optional check. Explain blocked checks and the next useful action.

## Prepare the first close review

1. Confirm entity, fiscal period, timezone, functional/reporting currency, accounting method, chart of accounts, close owner, and source of truth for the ledger. Obtain authorized billing and ledger exports or test selected connections. For a partial review, identify exactly which source is missing.
2. Inventory subscription contracts or terms, invoice and credit-note records, payment/refund activity, ledger postings, and any existing revenue schedule. Record source dates and stable IDs. Do not equate invoice date, cash date, service period, and recognition date.
3. Reconcile at the agreed grain (invoice, contract, or account) and currency. Check opening balances, period cutoffs, duplicates, amendments, upgrades, downgrades, prorations, cancellations, refunds, taxes, and unapplied cash. Show gross, credits, fees, net cash, and recognized/deferred revenue separately where the source supports each number.
4. Prepare a close checklist and exception table: source ID, expected/observed amount, variance, account, period, likely cause or open question, owner, and supporting record. Explain arithmetic and rounding. Do not fabricate a journal entry or accounting treatment where policy is missing.
5. Provide a concise close memo with reconciled totals, unresolved differences, proposed checks, and a clear “ready for accountant review” or “blocked by” state. Ask the owner or accountant to review a representative result before completing setup.

## Fictional worked example

Input: Example SaaS Inc's USD September export contains invoice `inv-close-17` for USD 1,000.00, credit note `cn-17` for USD 100.00, one successful gross payment `pay-17` for USD 600.00, a USD 18.00 processing fee, and a bank deposit of USD 582.00. The accountant's schedule lists USD 450.00 recognized and USD 450.00 deferred for the net invoice; the ledger export lists USD 400.00 recognized and USD 450.00 deferred. These are fictional source records, not an accounting-policy decision.

First result: the close memo binds every record to the same entity, currency and period. It calculates net billed **USD 900.00** (1,000 − 100), gross collected **USD 600.00**, open invoice balance **USD 300.00** (900 − 600), and net deposit **USD 582.00** (600 − 18). The schedule's recognized amount exceeds the ledger's by **USD 50.00**. Put that difference in an exception row with source IDs, accountant owner and the next check; label the close **blocked for accountant review** until the ledger and schedule are reconciled. Re-read revised source versions on a later run and retain the exception ID.

## Inadequate output to reject

“Revenue is USD 1,000.00, cash is USD 600.00, and the close is complete.” Reject: the statement ignores the credit and fee, conflates invoice, collection and recognition, and hides the USD 50.00 schedule-to-ledger difference. Do not post a correcting journal without the accountant's treatment and a separate approved write route.

The fictional example does not complete setup. Reproduce one transaction with actual authorized billing, schedule and ledger records and save the accountant's review.

## Boundaries

Analysis is read-only by default. Do not post journals, close a period, change a contract, alter revenue schedules, or declare compliance with ASC 606/IFRS 15 from an incomplete export. Accounting treatments and materiality limits must come from the company's policy and authorized reviewer. Preserve source IDs and an audit trail for every proposed adjustment. A schedule or Automation can be configured separately after a bounded first close review.
