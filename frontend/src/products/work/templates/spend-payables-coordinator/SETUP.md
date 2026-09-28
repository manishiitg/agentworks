# Spend & Payables Coordinator setup

Template: `spend-payables-coordinator` version 2. Progress is saved in `templates/spend-payables-coordinator/TEMPLATE_SETUP.json`.

## First result

Choose the first route with the owner. For payables, provide one period of authorized vendor bills, card/expense exports and the approval policy. Ask: “Review payables due this month, find duplicates and policy gaps, and prepare a source-linked approval queue. Do not approve or pay anything.” For a proposed purchase, provide one validated comparison, current vendor/commitment and budget sources, security/privacy policy and the purchase owner; ask for a pending decision on the exact quote.

An export is enough. BILL, Ramp, Brex, QuickBooks, and Xero are examples of systems a customer may use; naming one does not connect it. Choose the authoritative AP system, test access in this Crew, and keep accounting-ledger data separate from payment execution. Do not copy another Crew's secret or approval authority.

## Source probe and example

For one real bill, join the original document hash and version to intake and AP records by entity, vendor and invoice ID. Recheck exact and near duplicates, approval policy, paid or scheduled state and any changed bank details. Ask the AP owner to accept or reject the first disposition; preserve the source revision and uncertainty.

Fictional example: invoice `INV-88` from vendor `vendor-42` is USD 108.00. The checked AP snapshot finds no duplicate, but approval is pending and no payment source is supplied. Its disposition is **ready for owner review**, with action_state **none**. Reject any output that says it was approved or paid. Reproduce one authorized bill before completing setup.

For a proposed purchase, first validate the exact `vendor-comparison/v1` plan and quote. Re-read the current vendor register, overlapping subscriptions or POs, remaining budget, security/privacy dispositions and purchase approval policy for the same entity. A fictional comparison at USD 7,260 under an USD 8,000 budget can reach **ready for owner review** only when must-haves and all required gates are cleared; it is never proof of purchase. Reproduce one authorized request and owner decision before completing setup.

## Optional work after the first queue

| Capability | Setup decision |
| --- | --- |
| Weekly payable review | Agree on cut-off, source freshness, due-date horizon, reviewer, and timezone; test before enabling. |
| New bill intake trigger | Authenticate source, deduplicate by provider event and bill ID, then re-read current bill state. |
| Approval or payment action | Require exact bill, vendor, bank-change verification, amount, currency, approver, and a separate permitted write route. |
| Finance handoff | Send a bounded approved/pending liability summary to Finance Analyst or Revenue & Close Analyst through a separately configured Automation. |

Installation does not activate payment, approval, delivery, recurrence, or an Automation.
